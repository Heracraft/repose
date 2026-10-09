package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/obs"
)

// Polar is the Polar API client (DECISIONS I-604): net/http and
// encoding/json against https://api.polar.sh/v1 (or the sandbox, told by
// POLAR_ENVIRONMENT), an organization access token as the bearer,
// `Polar-Version` pinned, three tries on 429 and 5xx with Retry-After
// honoured. There is no SDK because the dozen calls the api makes are
// plain JSON and an SDK's shapes change under the code that reads them.
type Polar struct {
	cfg  Config
	base string
	http *http.Client
	log  *slog.Logger
	// Sleep is what a retry waits with; tests replace it.
	Sleep func(time.Duration)
	// Tries is how many attempts a call gets; three.
	Tries int
}

// APIVersion is the Polar API version every request and the webhook
// endpoint are pinned to. Polar removes a version about nine months after
// it becomes current; RUNBOOK "Polar API version" says how to move it.
const APIVersion = "2026-10"

// PolarError is what Polar answered when it refused a call: the HTTP
// status, its error type and detail. The token is never in it.
type PolarError struct {
	Status int
	Type   string
	Detail string
}

func (e *PolarError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("polar: HTTP %d", e.Status)
	}
	if e.Type == "" {
		return fmt.Sprintf("polar: %s (HTTP %d)", e.Detail, e.Status)
	}
	return fmt.Sprintf("polar: %s (%s, HTTP %d)", e.Detail, e.Type, e.Status)
}

// IsPolarStatus reports whether err is Polar refusing with that status.
func IsPolarStatus(err error, status int) bool {
	var pe *PolarError
	return errors.As(err, &pe) && pe.Status == status
}

// NewPolar builds the client. The base URL is the configured
// environment's unless cfg.BaseURL overrides it (tests).
func NewPolar(cfg Config, log *slog.Logger) *Polar {
	base := cfg.BaseURL
	if base == "" {
		base = productionBaseURL
		if cfg.Environment() == EnvSandbox {
			base = sandboxBaseURL
		}
	}
	if log == nil {
		log = obs.NewLogger(obs.LogOptions{Component: obs.ComponentAPI, Writer: io.Discard})
	}
	return &Polar{cfg: cfg, base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 30 * time.Second},
		log: log.With("component", obs.ComponentAPI), Sleep: time.Sleep, Tries: 3}
}

// Config is the configuration the client was built with.
func (p *Polar) Config() Config { return p.cfg }

// Environment is sandbox or production.
func (p *Polar) Environment() string { return p.cfg.Environment() }

// do sends one API call and decodes the answer into out (nil to
// discard). It retries 429 and 5xx up to Tries times, waiting
// Retry-After when Polar says how long and a doubling second otherwise.
func (p *Polar) do(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("polar: encode %s %s: %w", method, path, err)
		}
		payload = b
	}
	tries := p.Tries
	if tries < 1 {
		tries = 1
	}
	wait := time.Second
	var last error
	for attempt := 1; attempt <= tries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, p.base+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+p.cfg.AccessToken)
		req.Header.Set("Polar-Version", APIVersion)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := p.http.Do(req)
		if err != nil {
			last = fmt.Errorf("polar: %s %s: %w", method, path, err)
			if ctx.Err() != nil {
				return last
			}
			p.Sleep(wait)
			wait *= 2
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		_ = res.Body.Close()
		if err != nil {
			return fmt.Errorf("polar: read %s %s: %w", method, path, err)
		}
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			last = polarErr(res.StatusCode, raw)
			if attempt == tries {
				break
			}
			d := wait
			if ra := res.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
					d = time.Duration(secs) * time.Second
				}
			}
			// A long Retry-After is the next tick's, not this request's.
			d = min(d, 30*time.Second)
			if ctx.Err() != nil {
				return last
			}
			p.Sleep(d)
			wait *= 2
			continue
		}
		if res.StatusCode >= 400 {
			return polarErr(res.StatusCode, raw)
		}
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("polar: decode %s %s: %w", method, path, err)
		}
		return nil
	}
	return last
}

// polarErr reads Polar's two error shapes: {"error": type, "detail":
// text} and a validation failure's {"detail": [{"loc", "msg"}]}.
func polarErr(status int, raw []byte) error {
	e := &PolarError{Status: status}
	var body struct {
		Error  string          `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return e
	}
	e.Type = body.Error
	var text string
	if json.Unmarshal(body.Detail, &text) == nil {
		e.Detail = text
		return e
	}
	var items []struct {
		Loc []any  `json:"loc"`
		Msg string `json:"msg"`
	}
	if json.Unmarshal(body.Detail, &items) == nil {
		var parts []string
		for _, it := range items {
			loc := make([]string, 0, len(it.Loc))
			for _, l := range it.Loc {
				loc = append(loc, fmt.Sprint(l))
			}
			parts = append(parts, strings.Join(loc, ".")+": "+it.Msg)
		}
		e.Detail = strings.Join(parts, "; ")
		if e.Type == "" {
			e.Type = "validation_error"
		}
	}
	return e
}

// page is Polar's list shape.
type page[T any] struct {
	Items      []T `json:"items"`
	Pagination struct {
		TotalCount int `json:"total_count"`
		MaxPage    int `json:"max_page"`
	} `json:"pagination"`
}

// list reads every page of a list endpoint (at most ten pages of 100;
// the catalog the bootstrap reads is a handful of rows).
func list[T any](ctx context.Context, p *Polar, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", "100")
	var out []T
	for n := 1; n <= 10; n++ {
		q.Set("page", strconv.Itoa(n))
		var pg page[T]
		if err := p.do(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &pg); err != nil {
			return nil, err
		}
		out = append(out, pg.Items...)
		if n >= pg.Pagination.MaxPage {
			break
		}
	}
	return out, nil
}

// polarTime parses Polar's RFC 3339 timestamps; nil for "".
func polarTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

// --- checkout ---------------------------------------------------------

// CheckoutRequest is what POST /billing/checkout asks Polar for.
type CheckoutRequest struct {
	ProductID  string
	UserID     uuid.UUID
	Email      string
	DiscountID string
	// CustomerIP is the browser's address, so Polar picks the tax country
	// from the customer rather than from the api.
	CustomerIP string
	SuccessURL string
	ReturnURL  string
}

// Checkout is the subset of a Polar checkout session the api reads.
type Checkout struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

// CreateCheckout creates the hosted checkout the dashboard sends the
// browser to: one plan product, the user as the external customer (Polar
// makes the customer when the checkout completes), the user id in the
// metadata so the webhook can find the account, discount codes off. A
// non-empty DiscountID applies the introductory discount (DECISIONS
// I-497); the product's trial applies.
func (p *Polar) CreateCheckout(ctx context.Context, r CheckoutRequest) (Checkout, error) {
	body := map[string]any{
		"products":             []string{r.ProductID},
		"external_customer_id": r.UserID.String(),
		"metadata":             map[string]any{"user_id": r.UserID.String()},
		"allow_discount_codes": false,
		"success_url":          r.SuccessURL,
		"return_url":           r.ReturnURL,
	}
	if r.Email != "" {
		body["customer_email"] = r.Email
	}
	if r.DiscountID != "" {
		body["discount_id"] = r.DiscountID
	}
	if r.CustomerIP != "" {
		body["customer_ip_address"] = r.CustomerIP
	}
	var out Checkout
	err := p.do(ctx, http.MethodPost, "/checkouts/", body, &out)
	return out, err
}

// --- subscriptions ----------------------------------------------------

// Subscription is the subset of a Polar subscription the api reads,
// shared by GET /subscriptions/{id} and the subscription.* webhooks.
type Subscription struct {
	ID                 string         `json:"id"`
	Status             string         `json:"status"`
	CustomerID         string         `json:"customer_id"`
	ProductID          string         `json:"product_id"`
	DiscountID         string         `json:"discount_id"`
	Currency           string         `json:"currency"`
	Amount             int64          `json:"amount"`
	CurrentPeriodStart string         `json:"current_period_start"`
	CurrentPeriodEnd   string         `json:"current_period_end"`
	TrialStart         string         `json:"trial_start"`
	TrialEnd           string         `json:"trial_end"`
	CancelAtPeriodEnd  bool           `json:"cancel_at_period_end"`
	CanceledAt         string         `json:"canceled_at"`
	StartedAt          string         `json:"started_at"`
	EndsAt             string         `json:"ends_at"`
	EndedAt            string         `json:"ended_at"`
	PastDueAt          string         `json:"past_due_at"`
	CreatedAt          string         `json:"created_at"`
	ModifiedAt         string         `json:"modified_at"`
	Metadata           map[string]any `json:"metadata"`
	Customer           *struct {
		ID         string `json:"id"`
		ExternalID string `json:"external_id"`
		Email      string `json:"email"`
	} `json:"customer"`
	// PendingUpdate is a change Polar applies at the next period: the
	// scheduled downgrade.
	PendingUpdate *struct {
		ProductID string `json:"product_id"`
		AppliesAt string `json:"applies_at"`
	} `json:"pending_update"`
}

// UserID is metadata.user_id when the checkout stamped it, else the
// customer's external id, which the checkout also set.
func (s *Subscription) UserID() (uuid.UUID, bool) {
	if s.Metadata != nil {
		if v, _ := s.Metadata["user_id"].(string); v != "" {
			if id, err := uuid.Parse(v); err == nil {
				return id, true
			}
		}
	}
	if s.Customer != nil && s.Customer.ExternalID != "" {
		if id, err := uuid.Parse(s.Customer.ExternalID); err == nil {
			return id, true
		}
	}
	return uuid.Nil, false
}

// CancelAt is when a scheduled cancellation takes effect: ends_at, or the
// period's end when Polar has not set it; nil when none is scheduled.
func (s *Subscription) CancelAt() *time.Time {
	if !s.CancelAtPeriodEnd {
		return nil
	}
	if t := polarTime(s.EndsAt); t != nil {
		return t
	}
	return polarTime(s.CurrentPeriodEnd)
}

// GetSubscription reads one subscription.
func (p *Polar) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	var out Subscription
	if err := p.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Proration behaviours for a plan change (PRICING.md "Cancelling and
// changing plans"): an upgrade at once with the difference charged now,
// a downgrade at the next period.
const (
	ProrateInvoice    = "invoice"
	ProrateNextPeriod = "next_period"
)

func (p *Polar) patchSubscription(ctx context.Context, id string, body map[string]any) (*Subscription, error) {
	var out Subscription
	if err := p.do(ctx, http.MethodPatch, "/subscriptions/"+url.PathEscape(id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChangeProduct moves the subscription to another plan's product.
// dropDiscount removes the subscription's discount with it: the
// introductory offer is Solo's, and Polar would otherwise carry its $9
// onto the plan the user moves to (DECISIONS I-604).
func (p *Polar) ChangeProduct(ctx context.Context, id, productID, proration string, dropDiscount bool) (*Subscription, error) {
	body := map[string]any{"product_id": productID, "proration_behavior": proration}
	if dropDiscount {
		body["discount_id"] = nil
	}
	return p.patchSubscription(ctx, id, body)
}

// ClearPendingUpdate drops a scheduled plan change (undoing a downgrade).
func (p *Polar) ClearPendingUpdate(ctx context.Context, id string) (*Subscription, error) {
	return p.patchSubscription(ctx, id, map[string]any{"pending_update": nil})
}

// SetCancelAtPeriodEnd schedules a cancellation at the period's end, or
// removes one (POST /billing/cancel and /billing/resume).
func (p *Polar) SetCancelAtPeriodEnd(ctx context.Context, id string, cancel bool) (*Subscription, error) {
	return p.patchSubscription(ctx, id, map[string]any{"cancel_at_period_end": cancel})
}

// RevokeSubscription ends the subscription now. Polar bills no metered
// usage on a revoke, so account deletion revokes only a period with no
// overage (DECISIONS I-604).
func (p *Polar) RevokeSubscription(ctx context.Context, id string) (*Subscription, error) {
	var out Subscription
	if err := p.do(ctx, http.MethodDelete, "/subscriptions/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- overage ----------------------------------------------------------

// OverageEvent is the name of the event the overage meter sums.
const OverageEvent = "egress_overage"

// OverageExternalID is the event's dedupe key: one per subscription and
// period, so a resend is harmless.
func OverageExternalID(subscriptionID string, periodStart time.Time) string {
	return fmt.Sprintf("overage:%s:%d", subscriptionID, periodStart.UTC().Unix())
}

// SendOverage ingests the period's egress overage as one event of gb
// units for the user; the plan product's metered price bills it on the
// renewal order (PRICING.md "Egress").
func (p *Polar) SendOverage(ctx context.Context, userID uuid.UUID, externalID string, gb int64, periodStart time.Time) error {
	var out struct {
		Inserted   int `json:"inserted"`
		Duplicates int `json:"duplicates"`
	}
	err := p.do(ctx, http.MethodPost, "/events/ingest", map[string]any{"events": []map[string]any{{
		"name":                 OverageEvent,
		"external_customer_id": userID.String(),
		"external_id":          externalID,
		"timestamp":            time.Now().UTC().Format(time.RFC3339),
		"metadata":             map[string]any{"gb": gb, "period_start": periodStart.UTC().Format(time.RFC3339)},
	}}}, &out)
	return err
}

// --- portal and orders ------------------------------------------------

// CustomerPortal creates a customer session for the user and returns
// the portal's URL. Polar knows the user only after a checkout.
func (p *Polar) CustomerPortal(ctx context.Context, userID uuid.UUID, returnURL string) (string, error) {
	var out struct {
		CustomerPortalURL string `json:"customer_portal_url"`
	}
	body := map[string]any{"external_customer_id": userID.String()}
	if returnURL != "" {
		body["return_url"] = returnURL
	}
	if err := p.do(ctx, http.MethodPost, "/customer-sessions/", body, &out); err != nil {
		return "", err
	}
	return out.CustomerPortalURL, nil
}

// Order is the subset of a Polar order GET /billing/invoices reads.
type Order struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	Paid               bool   `json:"paid"`
	SubtotalAmount     int64  `json:"subtotal_amount"`
	DiscountAmount     int64  `json:"discount_amount"`
	NetAmount          int64  `json:"net_amount"`
	TaxAmount          int64  `json:"tax_amount"`
	TotalAmount        int64  `json:"total_amount"`
	Currency           string `json:"currency"`
	BillingReason      string `json:"billing_reason"`
	InvoiceNumber      string `json:"invoice_number"`
	IsInvoiceGenerated bool   `json:"is_invoice_generated"`
	SubscriptionID     string `json:"subscription_id"`
	CreatedAt          string `json:"created_at"`
	Items              []struct {
		Label string `json:"label"`
	} `json:"items"`
}

// ListOrders lists the user's orders, newest first, up to 24.
func (p *Polar) ListOrders(ctx context.Context, userID uuid.UUID) ([]Order, error) {
	q := url.Values{"external_customer_id": {userID.String()}, "limit": {"24"}, "sorting": {"-created_at"}}
	var pg page[Order]
	if err := p.do(ctx, http.MethodGet, "/orders/?"+q.Encode(), nil, &pg); err != nil {
		return nil, err
	}
	return pg.Items, nil
}

// OrderInvoiceURL is the URL of an order's generated invoice PDF.
func (p *Polar) OrderInvoiceURL(ctx context.Context, orderID string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := p.do(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/invoice", nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// GenerateOrderInvoice asks Polar to generate an order's invoice; it is
// ready on a later listing.
func (p *Polar) GenerateOrderInvoice(ctx context.Context, orderID string) error {
	return p.do(ctx, http.MethodPost, "/orders/"+url.PathEscape(orderID)+"/invoice", nil, nil)
}

// --- catalog (bootstrap) ----------------------------------------------

// Meter is a Polar meter as the bootstrap reads it.
type Meter struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

// ListMeters lists the organization's meters.
func (p *Polar) ListMeters(ctx context.Context) ([]Meter, error) {
	return list[Meter](ctx, p, "/meters/", nil)
}

// CreateOverageMeter creates the meter the overage price bills: the sum
// of metadata.gb over events named OverageEvent.
func (p *Polar) CreateOverageMeter(ctx context.Context) (Meter, error) {
	var out Meter
	err := p.do(ctx, http.MethodPost, "/meters/", map[string]any{
		"name":         "Egress overage",
		"unit":         "custom",
		"custom_label": "GB",
		"filter": map[string]any{"conjunction": "and", "clauses": []map[string]any{
			{"property": "name", "operator": "eq", "value": OverageEvent},
		}},
		"aggregation": map[string]any{"func": "sum", "property": "gb"},
		"metadata":    map[string]any{"repose": overageMeterKey},
	}, &out)
	return out, err
}

// ProductPrice is one price of a Polar product.
type ProductPrice struct {
	ID          string `json:"id"`
	AmountType  string `json:"amount_type"`
	IsArchived  bool   `json:"is_archived"`
	PriceAmount int64  `json:"price_amount"`
	Currency    string `json:"price_currency"`
	MeterID     string `json:"meter_id"`
	UnitAmount  string `json:"unit_amount"`
}

// Product is a Polar product as the bootstrap reads it.
type Product struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	IsArchived         bool           `json:"is_archived"`
	RecurringInterval  string         `json:"recurring_interval"`
	TrialInterval      string         `json:"trial_interval"`
	TrialIntervalCount int            `json:"trial_interval_count"`
	Prices             []ProductPrice `json:"prices"`
	Metadata           map[string]any `json:"metadata"`
}

// ListProducts lists the organization's products that are not archived.
func (p *Polar) ListProducts(ctx context.Context) ([]Product, error) {
	return list[Product](ctx, p, "/products/", url.Values{"is_archived": {"false"}})
}

// CreatePlanProduct creates a plan's monthly product: the fixed price,
// the metered overage price on the meter, and the trial.
func (p *Polar) CreatePlanProduct(ctx context.Context, plan Plan, meterID, key string) (Product, error) {
	var out Product
	err := p.do(ctx, http.MethodPost, "/products/", map[string]any{
		"name":                     "repose " + plan.Name,
		"description":              fmt.Sprintf("%d GB of memory for running machines, %d GB disk, %d GB egress a month; egress past the allowance $0.05 a GB.", plan.MemoryGB, plan.DiskGB, plan.EgressGB),
		"recurring_interval":       "month",
		"recurring_interval_count": 1,
		"trial_interval":           "day",
		"trial_interval_count":     plan.TrialDays,
		"prices": []map[string]any{
			{"amount_type": "fixed", "price_currency": strings.ToLower(plan.Currency), "price_amount": plan.PriceCents, "tax_behavior": TaxBehavior},
			{"amount_type": "metered_unit", "price_currency": strings.ToLower(plan.Currency), "meter_id": meterID, "unit_amount": strconv.FormatInt(OveragePerGBCents, 10), "tax_behavior": TaxBehavior},
		},
		"metadata": map[string]any{"repose": key},
	}, &out)
	return out, err
}

// Discount is a Polar discount as the bootstrap reads it.
type Discount struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Type             string         `json:"type"`
	Amount           int64          `json:"amount"`
	Duration         string         `json:"duration"`
	DurationInMonths int            `json:"duration_in_months"`
	Products         []Product      `json:"products"`
	Metadata         map[string]any `json:"metadata"`
}

// ListDiscounts lists the organization's discounts.
func (p *Polar) ListDiscounts(ctx context.Context) ([]Discount, error) {
	return list[Discount](ctx, p, "/discounts/", nil)
}

// CreateIntroDiscount creates the plan's introductory discount: a fixed
// amount off each of the first IntroMonths charges of that plan's
// product. It has no code; the api applies it by id at checkout.
func (p *Polar) CreateIntroDiscount(ctx context.Context, plan Plan, productID string) (Discount, error) {
	var out Discount
	err := p.do(ctx, http.MethodPost, "/discounts/", map[string]any{
		"name":               fmt.Sprintf("%s introductory price", plan.Name),
		"type":               "fixed",
		"amount":             plan.IntroDiscountCents(),
		"currency":           strings.ToLower(plan.Currency),
		"duration":           "repeating",
		"duration_in_months": plan.IntroMonths,
		"products":           []string{productID},
		"metadata":           map[string]any{"repose": introDiscountKey(plan)},
	}, &out)
	return out, err
}

// introDiscountKey is the metadata.repose value of a plan's introductory
// discount; it names the amount and months, so a changed introductory
// price makes a new discount rather than reusing the old.
func introDiscountKey(plan Plan) string {
	return fmt.Sprintf("intro-%s-%d-%d", plan.ID, plan.IntroCents, plan.IntroMonths)
}

// WebhookEndpoint is a Polar webhook endpoint.
type WebhookEndpoint struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Format     string   `json:"format"`
	Secret     string   `json:"secret"`
	Events     []string `json:"events"`
	Enabled    bool     `json:"enabled"`
	APIVersion string   `json:"api_version"`
}

// ListWebhookEndpoints lists the organization's webhook endpoints.
func (p *Polar) ListWebhookEndpoints(ctx context.Context) ([]WebhookEndpoint, error) {
	return list[WebhookEndpoint](ctx, p, "/webhooks/endpoints", nil)
}

// CreateWebhookEndpoint creates a raw endpoint for the events, pinned to
// APIVersion.
func (p *Polar) CreateWebhookEndpoint(ctx context.Context, endpointURL string, events []string) (WebhookEndpoint, error) {
	var out WebhookEndpoint
	err := p.do(ctx, http.MethodPost, "/webhooks/endpoints", map[string]any{
		"url": endpointURL, "name": "repose api", "format": "raw", "events": events, "api_version": APIVersion,
	}, &out)
	return out, err
}

// UpdateWebhookEndpoint sets an endpoint's events and API version.
func (p *Polar) UpdateWebhookEndpoint(ctx context.Context, id string, events []string) (WebhookEndpoint, error) {
	var out WebhookEndpoint
	err := p.do(ctx, http.MethodPatch, "/webhooks/endpoints/"+url.PathEscape(id), map[string]any{
		"events": events, "api_version": APIVersion, "enabled": true,
	}, &out)
	return out, err
}

// Organization is the subset of the token's organization the bootstrap
// reads and sets.
type Organization struct {
	ID                   string         `json:"id"`
	Slug                 string         `json:"slug"`
	SubscriptionSettings map[string]any `json:"subscription_settings"`
	PortalSettings       map[string]any `json:"customer_portal_settings"`
	EmailSettings        map[string]any `json:"customer_email_settings"`
	DefaultTaxBehavior   string         `json:"default_tax_behavior"`
}

// TaxBehavior is how repose's prices treat tax: they exclude it, and
// Polar adds the buyer's tax on top (PRICING.md).
const TaxBehavior = "exclusive"

// GetOrganization reads the token's organization.
func (p *Polar) GetOrganization(ctx context.Context) (Organization, error) {
	orgs, err := list[Organization](ctx, p, "/organizations/", nil)
	if err != nil {
		return Organization{}, err
	}
	if len(orgs) != 1 {
		return Organization{}, fmt.Errorf("the token sees %d organizations; use an organization access token", len(orgs))
	}
	return orgs[0], nil
}

// OrganizationSettings is how repose wants the organization set
// (DECISIONS I-604): one subscription per customer, trial abuse
// prevention on, plan and seat changes off in the customer portal (the
// api checks seats and fit before a plan change), metered usage shown.
// Prices exclude tax (TaxBehavior), set apart in UpdateOrganization.
// Polar's own emails that repose already sends are off (trial ending,
// payment failed, cancelled, ended, plan changed; I-291); its receipts,
// confirmations and reminders stay, being the merchant of record's.
func OrganizationSettings() (subscription, portal, emails map[string]any) {
	subscription = map[string]any{
		"allow_multiple_subscriptions":    false,
		"proration_behavior":              "prorate",
		"benefit_revocation_grace_period": 0,
		"prevent_trial_abuse":             true,
		"allow_customer_updates":          true,
	}
	portal = map[string]any{
		"usage":        map[string]any{"show": true},
		"subscription": map[string]any{"update_seats": false, "update_plan": false},
		"customer":     map[string]any{"allow_email_change": false},
	}
	emails = map[string]any{
		"subscription_trial_conversion_reminder": false,
		"subscription_past_due":                  false,
		"subscription_cancellation":              false,
		"subscription_revoked":                   false,
		"subscription_updated":                   false,
	}
	return subscription, portal, emails
}

// UpdateOrganization sets the organization's subscription, portal and
// customer email settings, and the tax behaviour. Polar replaces the
// email settings whole, so the ones repose leaves alone are sent as they
// were.
func (p *Polar) UpdateOrganization(ctx context.Context, org Organization, subscription, portal, emails map[string]any) (Organization, error) {
	merged := map[string]any{}
	for k, v := range org.EmailSettings {
		merged[k] = v
	}
	for k, v := range emails {
		merged[k] = v
	}
	var out Organization
	err := p.do(ctx, http.MethodPatch, "/organizations/"+url.PathEscape(org.ID), map[string]any{
		"subscription_settings": subscription, "customer_portal_settings": portal, "customer_email_settings": merged, "default_tax_behavior": TaxBehavior,
	}, &out)
	return out, err
}
