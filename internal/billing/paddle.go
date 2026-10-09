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

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// Paddle is the Paddle Billing API client (DECISIONS I-289): net/http and
// encoding/json against https://api.paddle.com (or the sandbox, told by
// the key's prefix), `Paddle-Version: 1`, bearer auth, three tries on 429
// and 5xx with Retry-After honoured. There is no SDK because the dozen
// calls the api makes are plain JSON and an SDK's shapes change under
// the code that reads them.
type Paddle struct {
	cfg  Config
	base string
	http *http.Client
	log  *slog.Logger
	// Sleep is what a retry waits with; tests replace it.
	Sleep func(time.Duration)
	// Tries is how many attempts a call gets; three.
	Tries int
}

// PaddleError is what Paddle answered when it refused a call: the HTTP
// status and its own code and detail. The key is never in it.
type PaddleError struct {
	Status int
	Type   string
	Code   string
	Detail string
}

func (e *PaddleError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("paddle: HTTP %d", e.Status)
	}
	return fmt.Sprintf("paddle: %s (%s, HTTP %d)", e.Detail, e.Code, e.Status)
}

// NewPaddle builds the client. The base URL is the key's environment
// unless cfg.BaseURL overrides it (tests).
func NewPaddle(cfg Config, log *slog.Logger) *Paddle {
	base := cfg.BaseURL
	if base == "" {
		base = liveBaseURL
		if cfg.Environment() == EnvSandbox {
			base = sandboxBaseURL
		}
	}
	if log == nil {
		log = obs.NewLogger(obs.LogOptions{Component: obs.ComponentAPI, Writer: io.Discard})
	}
	return &Paddle{cfg: cfg, base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 30 * time.Second},
		log: log.With("component", obs.ComponentAPI), Sleep: time.Sleep, Tries: 3}
}

// Config is the configuration the client was built with.
func (p *Paddle) Config() Config { return p.cfg }

// Environment is sandbox or live.
func (p *Paddle) Environment() string { return p.cfg.Environment() }

// envelope is Paddle's response shape: data, plus meta with pagination.
type envelope struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		RequestID  string `json:"request_id"`
		Pagination struct {
			HasMore bool   `json:"has_more"`
			Next    string `json:"next"`
		} `json:"pagination"`
	} `json:"meta"`
	Error *struct {
		Type   string `json:"type"`
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
}

// do sends one API call and decodes data into out (nil to discard). It
// retries 429 and 5xx up to Tries times, waiting Retry-After when Paddle
// says how long and a doubling second otherwise.
func (p *Paddle) do(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("paddle: encode %s %s: %w", method, path, err)
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
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
		req.Header.Set("Paddle-Version", "1")
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := p.http.Do(req)
		if err != nil {
			last = fmt.Errorf("paddle: %s %s: %w", method, path, err)
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
			return fmt.Errorf("paddle: read %s %s: %w", method, path, err)
		}
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			last = paddleErr(res.StatusCode, raw)
			if attempt == tries {
				break
			}
			d := wait
			if ra := res.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
					d = time.Duration(secs) * time.Second
				}
			}
			p.Sleep(d)
			wait *= 2
			continue
		}
		if res.StatusCode >= 400 {
			return paddleErr(res.StatusCode, raw)
		}
		if out == nil || len(raw) == 0 {
			return nil
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("paddle: decode %s %s: %w", method, path, err)
		}
		if len(env.Data) == 0 {
			return nil
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("paddle: decode %s %s data: %w", method, path, err)
		}
		return nil
	}
	return last
}

func paddleErr(status int, raw []byte) error {
	e := &PaddleError{Status: status}
	var env envelope
	if json.Unmarshal(raw, &env) == nil && env.Error != nil {
		e.Type, e.Code, e.Detail = env.Error.Type, env.Error.Code, env.Error.Detail
	}
	return e
}

// IsPaddleCode reports whether err is Paddle refusing with that code.
func IsPaddleCode(err error, code string) bool {
	var pe *PaddleError
	return errors.As(err, &pe) && pe.Code == code
}

// --- customers --------------------------------------------------------

// Customer is the subset of a Paddle customer the api reads.
type Customer struct {
	ID         string         `json:"id"`
	Email      string         `json:"email"`
	Status     string         `json:"status"`
	CustomData map[string]any `json:"custom_data"`
}

// FindCustomerByEmail returns the active customer with that email, or "".
func (p *Paddle) FindCustomerByEmail(ctx context.Context, email string) (string, error) {
	var out []Customer
	q := url.Values{"email": {email}, "status": {"active"}, "per_page": {"5"}}
	if err := p.do(ctx, http.MethodGet, "/customers?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	for _, c := range out {
		if strings.EqualFold(c.Email, email) {
			return c.ID, nil
		}
	}
	return "", nil
}

// CreateCustomer creates a customer stamped with the user id.
func (p *Paddle) CreateCustomer(ctx context.Context, email string, userID uuid.UUID) (string, error) {
	var out Customer
	err := p.do(ctx, http.MethodPost, "/customers", map[string]any{
		"email": email, "custom_data": map[string]any{"user_id": userID.String()},
	}, &out)
	if err != nil {
		return "", err
	}
	return out.ID, nil
}

// EnsureCustomer returns the user's Paddle customer id, finding one by
// email or creating it, and stores it on users.paddle_customer_id. It is
// called at checkout, not at first sign-in, because a customer without a
// subscription is a row Paddle keeps for nothing.
func (p *Paddle) EnsureCustomer(ctx context.Context, pool *db.Pool, userID uuid.UUID) (string, error) {
	u, err := store.GetUser(ctx, pool, userID)
	if err != nil {
		return "", err
	}
	if u.PaddleCustomerID != nil && *u.PaddleCustomerID != "" {
		return *u.PaddleCustomerID, nil
	}
	if u.Email == nil || *u.Email == "" {
		return "", errors.New("the account has no email address; Paddle needs one for the customer")
	}
	id, err := p.FindCustomerByEmail(ctx, *u.Email)
	if err != nil {
		return "", fmt.Errorf("find the Paddle customer: %w", err)
	}
	if id == "" {
		id, err = p.CreateCustomer(ctx, *u.Email, userID)
		if err != nil {
			return "", fmt.Errorf("create the Paddle customer: %w", err)
		}
	}
	if _, err := pool.Exec(ctx, "update users set paddle_customer_id = $2 where id = $1 and paddle_customer_id is null", userID, id); err != nil {
		return "", err
	}
	return id, nil
}

// --- transactions -----------------------------------------------------

// Transaction is the subset of a Paddle transaction the api reads.
type Transaction struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	CustomerID     string `json:"customer_id"`
	SubscriptionID string `json:"subscription_id"`
	CurrencyCode   string `json:"currency_code"`
	Origin         string `json:"origin"`
	InvoiceID      string `json:"invoice_id"`
	InvoiceNumber  string `json:"invoice_number"`
	CreatedAt      string `json:"created_at"`
	BilledAt       string `json:"billed_at"`
	BillingPeriod  *struct {
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
	} `json:"billing_period"`
	Details *struct {
		Totals struct {
			Subtotal   string `json:"subtotal"`
			Tax        string `json:"tax"`
			Total      string `json:"total"`
			GrandTotal string `json:"grand_total"`
		} `json:"totals"`
	} `json:"details"`
	Items []struct {
		Price struct {
			ID        string `json:"id"`
			ProductID string `json:"product_id"`
		} `json:"price"`
		Quantity int `json:"quantity"`
	} `json:"items"`
	Checkout *struct {
		URL string `json:"url"`
	} `json:"checkout"`
	CustomData map[string]any `json:"custom_data"`
}

// CreateCheckoutTransaction creates the transaction the dashboard opens
// with Paddle.js: one plan price, quantity one, the customer, the user id
// in custom_data so the webhook can find the account, collected
// automatically (card at checkout). A non-empty discountID applies that
// discount; Paddle carries a recurring one onto the subscription and
// starts it when the trial ends (DECISIONS I-497).
func (p *Paddle) CreateCheckoutTransaction(ctx context.Context, customerID, priceID, discountID string, userID uuid.UUID) (string, error) {
	var out Transaction
	body := map[string]any{
		"items":           []map[string]any{{"price_id": priceID, "quantity": 1}},
		"customer_id":     customerID,
		"custom_data":     map[string]any{"user_id": userID.String()},
		"collection_mode": "automatic",
	}
	if discountID != "" {
		body["discount_id"] = discountID
	}
	err := p.do(ctx, http.MethodPost, "/transactions", body, &out)
	if err != nil {
		return "", err
	}
	return out.ID, nil
}

// ListTransactions lists the customer's billed, completed and past-due
// transactions, newest first, up to 24 (GET /billing/invoices).
func (p *Paddle) ListTransactions(ctx context.Context, customerID string) ([]Transaction, error) {
	var out []Transaction
	q := url.Values{"customer_id": {customerID}, "status": {"billed,completed,past_due"}, "per_page": {"24"}, "order_by": {"created_at[DESC]"}}
	if err := p.do(ctx, http.MethodGet, "/transactions?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InvoicePDF returns the URL of a transaction's invoice PDF.
func (p *Paddle) InvoicePDF(ctx context.Context, transactionID string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := p.do(ctx, http.MethodGet, "/transactions/"+url.PathEscape(transactionID)+"/invoice", nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// --- subscriptions ----------------------------------------------------

// Subscription is the subset of a Paddle subscription the api reads,
// shared by GET /subscriptions/{id} and the subscription.* webhooks.
type Subscription struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	CustomerID   string `json:"customer_id"`
	CurrencyCode string `json:"currency_code"`
	CreatedAt    string `json:"created_at"`
	StartedAt    string `json:"started_at"`
	NextBilledAt string `json:"next_billed_at"`
	CanceledAt   string `json:"canceled_at"`
	PausedAt     string `json:"paused_at"`
	// CurrentBillingPeriod is null on a subscription that has not billed.
	CurrentBillingPeriod *struct {
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
	} `json:"current_billing_period"`
	ScheduledChange *struct {
		Action      string `json:"action"` // cancel | pause | resume
		EffectiveAt string `json:"effective_at"`
		ResumeAt    string `json:"resume_at"`
	} `json:"scheduled_change"`
	Items []struct {
		Status     string `json:"status"`
		Quantity   int    `json:"quantity"`
		TrialDates *struct {
			StartsAt string `json:"starts_at"`
			EndsAt   string `json:"ends_at"`
		} `json:"trial_dates"`
		Price struct {
			ID        string `json:"id"`
			ProductID string `json:"product_id"`
		} `json:"price"`
	} `json:"items"`
	CustomData     map[string]any `json:"custom_data"`
	ManagementURLs *struct {
		UpdatePaymentMethod string `json:"update_payment_method"`
		Cancel              string `json:"cancel"`
	} `json:"management_urls"`
	// Discount is the recurring discount on the subscription, if any;
	// EndsAt is null while Paddle has not fixed its end.
	Discount *struct {
		ID       string `json:"id"`
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
	} `json:"discount"`
	// ImmediateTransaction is set on a charge made immediately.
	ImmediateTransaction *struct {
		ID string `json:"id"`
	} `json:"immediate_transaction"`
}

// PriceID is the first item's price, which is the plan.
func (s *Subscription) PriceID() string {
	if len(s.Items) == 0 {
		return ""
	}
	return s.Items[0].Price.ID
}

// TrialEnd is the first item's trial end, if any.
func (s *Subscription) TrialEnd() *time.Time {
	if len(s.Items) == 0 || s.Items[0].TrialDates == nil {
		return nil
	}
	return paddleTime(s.Items[0].TrialDates.EndsAt)
}

// UserID is custom_data.user_id when the checkout stamped it.
func (s *Subscription) UserID() (uuid.UUID, bool) {
	if s.CustomData == nil {
		return uuid.Nil, false
	}
	v, _ := s.CustomData["user_id"].(string)
	id, err := uuid.Parse(v)
	return id, err == nil
}

// paddleTime parses Paddle's RFC 3339 timestamps; nil for "".
func paddleTime(s string) *time.Time {
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

// GetSubscription reads one subscription.
func (p *Paddle) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	var out Subscription
	if err := p.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Proration modes for a plan change (PRICING.md "Cancelling and changing
// plans"): an upgrade at once, a downgrade at the next renewal.
const (
	ProrateImmediately      = "prorated_immediately"
	ProrateNextBillingCycle = "prorated_next_billing_period"
)

// UpdateSubscriptionItems moves the subscription to another plan's price.
func (p *Paddle) UpdateSubscriptionItems(ctx context.Context, id, priceID, prorationMode string) (*Subscription, error) {
	var out Subscription
	err := p.do(ctx, http.MethodPatch, "/subscriptions/"+url.PathEscape(id), map[string]any{
		"items":                  []map[string]any{{"price_id": priceID, "quantity": 1}},
		"proration_billing_mode": prorationMode,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// When a cancellation takes effect.
const (
	EffectiveNextBillingPeriod = "next_billing_period"
	EffectiveImmediately       = "immediately"
)

// CancelSubscription schedules or performs a cancellation.
func (p *Paddle) CancelSubscription(ctx context.Context, id, effectiveFrom string) (*Subscription, error) {
	var out Subscription
	err := p.do(ctx, http.MethodPost, "/subscriptions/"+url.PathEscape(id)+"/cancel", map[string]any{"effective_from": effectiveFrom}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ResumeScheduledChange removes a scheduled cancellation before it takes
// effect (POST /billing/resume).
func (p *Paddle) ResumeScheduledChange(ctx context.Context, id string) (*Subscription, error) {
	var out Subscription
	err := p.do(ctx, http.MethodPatch, "/subscriptions/"+url.PathEscape(id), map[string]any{"scheduled_change": nil}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateOneTimeCharge adds a non-catalog line to the subscription's next
// transaction: the egress overage (PRICING.md "Egress"). It returns the
// transaction id when Paddle billed it at once, and "" when the line
// waits for the next invoice. effectiveFrom is next_billing_period in
// the hourly tick and immediately when the account is being deleted.
func (p *Paddle) CreateOneTimeCharge(ctx context.Context, subscriptionID string, cents int64, description, effectiveFrom string) (string, error) {
	var out Subscription
	err := p.do(ctx, http.MethodPost, "/subscriptions/"+url.PathEscape(subscriptionID)+"/charge", map[string]any{
		"effective_from": effectiveFrom,
		"items": []map[string]any{{
			"quantity": 1,
			"price": map[string]any{
				"description": description,
				"name":        "Egress overage",
				"product_id":  p.cfg.ProductOverage,
				"unit_price":  map[string]any{"amount": strconv.FormatInt(cents, 10), "currency_code": "USD"},
				"quantity":    map[string]any{"minimum": 1, "maximum": 1},
			},
		}},
	}, &out)
	if err != nil {
		return "", err
	}
	if out.ImmediateTransaction != nil {
		return out.ImmediateTransaction.ID, nil
	}
	return "", nil
}

// --- portal -----------------------------------------------------------

// PortalURLs is what a portal session gives: the overview page and, per
// subscription, the deep link that updates its payment method.
type PortalURLs struct {
	Overview            string
	UpdatePaymentMethod map[string]string
}

// PortalSession creates a customer portal session.
func (p *Paddle) PortalSession(ctx context.Context, customerID string, subscriptionIDs []string) (PortalURLs, error) {
	var out struct {
		URLs struct {
			General struct {
				Overview string `json:"overview"`
			} `json:"general"`
			Subscriptions []struct {
				ID                              string `json:"id"`
				UpdateSubscriptionPaymentMethod string `json:"update_subscription_payment_method"`
			} `json:"subscriptions"`
		} `json:"urls"`
	}
	body := map[string]any{}
	if len(subscriptionIDs) > 0 {
		body["subscription_ids"] = subscriptionIDs
	}
	if err := p.do(ctx, http.MethodPost, "/customers/"+url.PathEscape(customerID)+"/portal-sessions", body, &out); err != nil {
		return PortalURLs{}, err
	}
	res := PortalURLs{Overview: out.URLs.General.Overview, UpdatePaymentMethod: map[string]string{}}
	for _, s := range out.URLs.Subscriptions {
		res.UpdatePaymentMethod[s.ID] = s.UpdateSubscriptionPaymentMethod
	}
	return res, nil
}

// --- catalog (bootstrap) ----------------------------------------------

// Product is a Paddle product as the bootstrap reads it.
type Product struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	TaxCategory string         `json:"tax_category"`
	CustomData  map[string]any `json:"custom_data"`
}

// PaddlePrice is a Paddle price as the bootstrap reads it.
type PaddlePrice struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	Description string `json:"description"`
	Status      string `json:"status"`
	UnitPrice   struct {
		Amount       string `json:"amount"`
		CurrencyCode string `json:"currency_code"`
	} `json:"unit_price"`
	BillingCycle *struct {
		Interval  string `json:"interval"`
		Frequency int    `json:"frequency"`
	} `json:"billing_cycle"`
	TrialPeriod *struct {
		Interval  string `json:"interval"`
		Frequency int    `json:"frequency"`
	} `json:"trial_period"`
	CustomData map[string]any `json:"custom_data"`
}

// ListProducts lists active products.
func (p *Paddle) ListProducts(ctx context.Context) ([]Product, error) {
	var out []Product
	if err := p.do(ctx, http.MethodGet, "/products?status=active&per_page=200", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateProduct creates a product in the saas tax category.
func (p *Paddle) CreateProduct(ctx context.Context, name, description string, custom map[string]any) (Product, error) {
	var out Product
	err := p.do(ctx, http.MethodPost, "/products", map[string]any{
		"name": name, "description": description, "tax_category": "saas", "custom_data": custom,
	}, &out)
	return out, err
}

// ListPrices lists active prices of a product.
func (p *Paddle) ListPrices(ctx context.Context, productID string) ([]PaddlePrice, error) {
	var out []PaddlePrice
	q := url.Values{"product_id": {productID}, "status": {"active"}, "per_page": {"200"}}
	if err := p.do(ctx, http.MethodGet, "/prices?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreatePlanPrice creates a plan's monthly price with its trial.
func (p *Paddle) CreatePlanPrice(ctx context.Context, productID string, plan Plan) (PaddlePrice, error) {
	var out PaddlePrice
	err := p.do(ctx, http.MethodPost, "/prices", map[string]any{
		"description":   plan.Name + " monthly",
		"name":          plan.Name,
		"product_id":    productID,
		"unit_price":    map[string]any{"amount": strconv.FormatInt(plan.PriceCents, 10), "currency_code": plan.Currency},
		"billing_cycle": map[string]any{"interval": "month", "frequency": 1},
		"trial_period":  map[string]any{"interval": "day", "frequency": plan.TrialDays},
		"tax_mode":      "account_setting",
		"quantity":      map[string]any{"minimum": 1, "maximum": 1},
		"custom_data":   map[string]any{"repose": plan.ID},
	}, &out)
	return out, err
}

// Discount is a Paddle discount as the bootstrap reads it.
type Discount struct {
	ID                        string         `json:"id"`
	Status                    string         `json:"status"`
	Description               string         `json:"description"`
	Type                      string         `json:"type"`
	Amount                    string         `json:"amount"`
	CurrencyCode              string         `json:"currency_code"`
	Recur                     bool           `json:"recur"`
	MaximumRecurringIntervals *int           `json:"maximum_recurring_intervals"`
	RestrictTo                []string       `json:"restrict_to"`
	CustomData                map[string]any `json:"custom_data"`
}

// ListDiscounts lists active discounts.
func (p *Paddle) ListDiscounts(ctx context.Context) ([]Discount, error) {
	var out []Discount
	if err := p.do(ctx, http.MethodGet, "/discounts?status=active&per_page=200", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateIntroDiscount creates the plan's introductory discount: a flat
// amount off each of the first IntroMonths charges of that plan's price.
// It has no published code; the api applies it by id at checkout. It is
// enabled for checkout because Paddle applies only such a discount to a
// checkout, and its generated code is never shown to anyone.
func (p *Paddle) CreateIntroDiscount(ctx context.Context, plan Plan, priceID string) (Discount, error) {
	var out Discount
	err := p.do(ctx, http.MethodPost, "/discounts", map[string]any{
		"description":                 fmt.Sprintf("%s introductory price: $%d a month for the first %d months", plan.Name, plan.IntroCents/100, plan.IntroMonths),
		"type":                        "flat",
		"amount":                      strconv.FormatInt(plan.IntroDiscountCents(), 10),
		"currency_code":               plan.Currency,
		"enabled_for_checkout":        true,
		"recur":                       true,
		"maximum_recurring_intervals": plan.IntroMonths,
		"restrict_to":                 []string{priceID},
		"custom_data":                 map[string]any{"repose": introDiscountKey(plan)},
	}, &out)
	return out, err
}

// introDiscountKey is the custom_data.repose value of a plan's
// introductory discount; it names the amount and months, so a changed
// introductory price makes a new discount rather than reusing the old.
func introDiscountKey(plan Plan) string {
	return fmt.Sprintf("intro-%s-%d-%d", plan.ID, plan.IntroCents, plan.IntroMonths)
}

// NotificationSetting is a webhook destination.
type NotificationSetting struct {
	ID                string   `json:"id"`
	Description       string   `json:"description"`
	Type              string   `json:"type"`
	Destination       string   `json:"destination"`
	Active            bool     `json:"active"`
	EndpointSecretKey string   `json:"endpoint_secret_key"`
	TrafficSource     string   `json:"traffic_source"`
	SubscribedEvents  []string `json:"-"`
	RawEvents         []struct {
		Name string `json:"name"`
	} `json:"subscribed_events"`
}

// ListNotificationSettings lists the account's webhook destinations.
func (p *Paddle) ListNotificationSettings(ctx context.Context) ([]NotificationSetting, error) {
	var out []NotificationSetting
	if err := p.do(ctx, http.MethodGet, "/notification-settings", nil, &out); err != nil {
		return nil, err
	}
	for i := range out {
		for _, e := range out[i].RawEvents {
			out[i].SubscribedEvents = append(out[i].SubscribedEvents, e.Name)
		}
	}
	return out, nil
}

// TrafficSource is the traffic_source a destination gets: in the sandbox
// it also receives simulated events, which docs/ops/M4-GATE.md sends to
// prove the payment states (Paddle refuses a simulation for a "platform"
// destination); live gets real events only.
func (p *Paddle) TrafficSource() string {
	if p.Environment() == EnvSandbox {
		return "all"
	}
	return "platform"
}

// CreateNotificationSetting creates a webhook destination for the events.
func (p *Paddle) CreateNotificationSetting(ctx context.Context, destination string, events []string) (NotificationSetting, error) {
	var out NotificationSetting
	err := p.do(ctx, http.MethodPost, "/notification-settings", map[string]any{
		"description":              "repose api",
		"destination":              destination,
		"type":                     "url",
		"subscribed_events":        events,
		"api_version":              1,
		"include_sensitive_fields": false,
		"traffic_source":           p.TrafficSource(),
	}, &out)
	for _, e := range out.RawEvents {
		out.SubscribedEvents = append(out.SubscribedEvents, e.Name)
	}
	return out, err
}

// SetNotificationTrafficSource changes a destination's traffic_source.
func (p *Paddle) SetNotificationTrafficSource(ctx context.Context, id, source string) error {
	return p.do(ctx, http.MethodPatch, "/notification-settings/"+url.PathEscape(id), map[string]any{"traffic_source": source}, nil)
}
