package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// The Polar webhook (09-billing.md §5.11, api.md POST /billing/webhook,
// DECISIONS I-604). Polar signs with Standard Webhooks: webhook-id,
// webhook-timestamp and webhook-signature (`v1,<base64>`, space
// separated), an HMAC-SHA256 of `id.timestamp.body`, refused past five
// minutes of skew. Every event is deduped on webhook-id, the primary key
// of billing_events: a duplicate delivery is a no-op and a replay
// reproduces the same rows.

// ErrBadSignature is returned when the signature does not verify. The
// route answers 400 and logs the event type only; the body is never
// logged (it carries the customer's details).
var ErrBadSignature = errors.New("polar webhook signature is invalid")

// ErrDuplicate means the event id was already recorded.
var ErrDuplicate = errors.New("polar event already processed")

// The event types the endpoint subscribes to and handles.
var WebhookEvents = []string{
	"subscription.created", "subscription.updated", "subscription.active", "subscription.canceled",
	"subscription.uncanceled", "subscription.revoked", "subscription.past_due", "order.paid",
}

// SignatureSkew is how far a webhook's timestamp may be from now.
const SignatureSkew = 5 * time.Minute

// WebhookHeaders are the three Standard Webhooks headers.
type WebhookHeaders struct {
	ID        string
	Timestamp string
	Signature string
}

// HeadersFrom reads the Standard Webhooks headers off a request.
func HeadersFrom(h http.Header) WebhookHeaders {
	return WebhookHeaders{ID: h.Get("webhook-id"), Timestamp: h.Get("webhook-timestamp"), Signature: h.Get("webhook-signature")}
}

// Webhooks applies Polar events to the database.
type Webhooks struct {
	pool *db.Pool
	cfg  Config
	// secrets are the endpoint secrets accepted: the current one and, during
	// a rotation, the previous one.
	secrets []string
	log     *slog.Logger
	m       *metrics.M
	Now     func() time.Time
	// Stop stops a user's machines when the subscription ends; nil skips
	// the stop (tests of the row logic alone).
	Stop Stopper
	// Seats is told when a subscription arrives (waitlist.Seats.Converted);
	// nil skips it.
	Seats waitlist.Seats
}

// NewWebhooks builds the handler. Extra secrets are accepted alongside
// cfg.WebhookSecret while an endpoint is rotated.
func NewWebhooks(pool *db.Pool, cfg Config, m *metrics.M, log *slog.Logger, extraSecrets ...string) *Webhooks {
	secrets := append([]string{cfg.WebhookSecret}, extraSecrets...)
	return &Webhooks{pool: pool, cfg: cfg, secrets: secrets, log: log.With("component", obs.ComponentAPI), m: m, Now: time.Now}
}

// signingKeys are the HMAC keys a secret may stand for: the base64 after
// `whsec_` (Standard Webhooks, Polar's secrets from 2026-09-08) and the
// bytes of the whole string (Polar's older secrets).
func signingKeys(secret string) [][]byte {
	var keys [][]byte
	if rest, ok := strings.CutPrefix(secret, "whsec_"); ok {
		if k, err := base64.StdEncoding.DecodeString(rest); err == nil && len(k) > 0 {
			keys = append(keys, k)
		}
	}
	return append(keys, []byte(secret))
}

func signature(key []byte, id, ts string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Sign produces the Standard Webhooks headers for a body, which is what
// the fake Polar and the tests use to post events.
func Sign(secret, id string, ts time.Time, body []byte) WebhookHeaders {
	t := strconv.FormatInt(ts.Unix(), 10)
	return WebhookHeaders{ID: id, Timestamp: t, Signature: "v1," + signature(signingKeys(secret)[0], id, t, body)}
}

// Verify checks the headers against the body: any v1 signature with any
// accepted secret's key, constant-time, within the skew.
func (w *Webhooks) Verify(h WebhookHeaders, body []byte) error {
	if h.ID == "" || h.Timestamp == "" || h.Signature == "" {
		return fmt.Errorf("%w: webhook-id, webhook-timestamp or webhook-signature is missing", ErrBadSignature)
	}
	unix, err := strconv.ParseInt(h.Timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: webhook-timestamp is not a number", ErrBadSignature)
	}
	if skew := w.Now().Sub(time.Unix(unix, 0)); skew > SignatureSkew || skew < -SignatureSkew {
		return fmt.Errorf("%w: webhook-timestamp is %s from now", ErrBadSignature, skew.Round(time.Second))
	}
	var sigs []string
	for _, part := range strings.Fields(h.Signature) {
		if v, ok := strings.CutPrefix(part, "v1,"); ok {
			sigs = append(sigs, v)
		}
	}
	if len(sigs) == 0 {
		return fmt.Errorf("%w: no v1 signature", ErrBadSignature)
	}
	for _, secret := range w.secrets {
		if secret == "" {
			continue
		}
		for _, key := range signingKeys(secret) {
			want := signature(key, h.ID, h.Timestamp, body)
			for _, got := range sigs {
				if subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1 {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("%w: no signature matched", ErrBadSignature)
}

// Event is Polar's webhook payload.
type Event struct {
	Type       string          `json:"type"`
	Timestamp  string          `json:"timestamp"`
	APIVersion string          `json:"api_version"`
	Data       json.RawMessage `json:"data"`
}

// Handle verifies the signature, records the event and applies it. A
// duplicate returns ErrDuplicate, which the route answers 200 to, because
// Polar retries anything else.
func (w *Webhooks) Handle(ctx context.Context, payload []byte, h WebhookHeaders) (kind string, err error) {
	if len(w.secrets) == 0 || w.secrets[0] == "" {
		return "", ErrDisabled
	}
	var ev Event
	if err := json.Unmarshal(payload, &ev); err == nil {
		kind = ev.Type
	}
	if err := w.Verify(h, payload); err != nil {
		return kind, err
	}
	if kind == "" {
		return kind, fmt.Errorf("%w: body has no type", ErrBadSignature)
	}
	occurred := w.Now().UTC()
	if t := polarTime(ev.Timestamp); t != nil {
		occurred = *t
	}
	tag, err := w.pool.Exec(ctx, "insert into billing_events (id, type, occurred_at) values ($1, $2, $3) on conflict (id) do nothing", h.ID, kind, occurred)
	if err != nil {
		return kind, err
	}
	if tag.RowsAffected() == 0 {
		// Seen before: a duplicate once applied; an event whose apply
		// failed is applied again, which is what Polar's retries and a
		// redelivery from its dashboard are for.
		var processed *time.Time
		if err := w.pool.QueryRow(ctx, "select processed_at from billing_events where id = $1", h.ID).Scan(&processed); err != nil {
			return kind, err
		}
		if processed != nil {
			return kind, ErrDuplicate
		}
	}
	applyErr := w.apply(ctx, &ev)
	if applyErr != nil {
		if _, err := w.pool.Exec(ctx, "update billing_events set error = $2 where id = $1", h.ID, applyErr.Error()); err != nil {
			return kind, err
		}
		return kind, applyErr
	}
	if _, err := w.pool.Exec(ctx, "update billing_events set processed_at = now(), error = null where id = $1", h.ID); err != nil {
		return kind, err
	}
	w.log.Info("polar webhook applied", "event", obs.EventBillingWebhook, "kind", kind, "result", "ok")
	return kind, nil
}

func (w *Webhooks) apply(ctx context.Context, ev *Event) error {
	switch {
	case strings.HasPrefix(ev.Type, "subscription."):
		return w.subscription(ctx, ev)
	case ev.Type == "order.paid":
		return w.orderPaid(ctx, ev)
	}
	// Anything else the endpoint is subscribed to is recorded and ignored.
	return nil
}

// resolveUser finds the account a Polar object belongs to: the user id
// the checkout stamped (metadata, then the customer's external id), then
// the customer id. An unknown one is an error the billing_events row
// keeps.
func (w *Webhooks) resolveUser(ctx context.Context, userID uuid.UUID, customerID string) (*store.User, error) {
	if userID != uuid.Nil {
		u, err := store.GetUser(ctx, w.pool, userID)
		if err == nil {
			return u, nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
	}
	if customerID == "" {
		return nil, errors.New("event names no user and no customer")
	}
	var id uuid.UUID
	err := w.pool.QueryRow(ctx, "select id from users where billing_customer_id = $1", customerID).Scan(&id)
	if db.IsNoRows(err) {
		// Not ours: a customer made in Polar's dashboard, or another
		// product in the same organization.
		return nil, fmt.Errorf("no user for Polar customer %s", customerID)
	}
	if err != nil {
		return nil, err
	}
	return store.GetUser(ctx, w.pool, id)
}

// subscriptionStatus maps Polar's status onto the subscriptions table's:
// unpaid (retries exhausted) is canceled; incomplete and
// incomplete_expired are a checkout that never became a subscription,
// for which there is no row ("").
func subscriptionStatus(polar string) string {
	switch polar {
	case StatusTrialing, StatusActive, StatusPastDue, StatusPaused, StatusCanceled:
		return polar
	case "unpaid":
		return StatusCanceled
	}
	return ""
}

// subscriptionRow is the subscriptions row a Polar subscription stands
// for. Polar has no next billing date: a subscription that renews bills
// at its period's end. It names no end for the introductory discount
// either; Polar counts its months from the first charged period (the
// trial's end), which is what IntroUntil derives (DECISIONS I-604).
func (w *Webhooks) subscriptionRow(ps *Subscription, userID uuid.UUID, status string, plan Plan) Sub {
	row := Sub{ID: ps.ID, UserID: userID, CustomerID: ps.CustomerID, Plan: plan.ID, Status: status, Seats: plan.Seats,
		PeriodStart: polarTime(ps.CurrentPeriodStart), PeriodEnd: polarTime(ps.CurrentPeriodEnd), TrialEnd: polarTime(ps.TrialEnd), CancelAt: ps.CancelAt(),
		SourceModifiedAt: polarTime(ps.ModifiedAt)}
	if IsLive(status) && row.CancelAt == nil {
		row.NextBilledAt = row.PeriodEnd
	}
	if ps.PendingUpdate != nil {
		if sp := w.cfg.PlanForProduct(ps.PendingUpdate.ProductID); sp != "" && sp != plan.ID {
			row.ScheduledPlan = &sp
		}
	}
	if w.cfg.DiscountIntro != "" && ps.DiscountID == w.cfg.DiscountIntro {
		row.Intro = true
		from := polarTime(ps.TrialEnd)
		if from == nil {
			from = polarTime(ps.StartedAt)
		}
		if from == nil {
			from = polarTime(ps.CreatedAt)
		}
		if from != nil && plan.IntroMonths > 0 {
			until := from.AddDate(0, plan.IntroMonths, 0)
			row.IntroUntil = &until
		}
	}
	return row
}

// subscription upserts the row from Polar's view and projects it onto the
// account: status, has_card, the seat conversion, the first payment
// failure and the account events.
func (w *Webhooks) subscription(ctx context.Context, ev *Event) error {
	var ps Subscription
	if err := json.Unmarshal(ev.Data, &ps); err != nil {
		return fmt.Errorf("decode the %s subscription: %w", ev.Type, err)
	}
	if ps.ID == "" {
		return errors.New("subscription event without an id")
	}
	status := subscriptionStatus(ps.Status)
	if status == "" {
		if ps.Status == "incomplete" || ps.Status == "incomplete_expired" {
			return nil
		}
		return fmt.Errorf("subscription %s has unknown status %q", ps.ID, ps.Status)
	}
	uid, _ := ps.UserID()
	u, err := w.resolveUser(ctx, uid, ps.CustomerID)
	if err != nil {
		return err
	}
	planID := w.cfg.PlanForProduct(ps.ProductID)
	if planID == "" {
		return fmt.Errorf("subscription %s has no repose plan product (product %q)", ps.ID, ps.ProductID)
	}
	plan, _ := PlanByID(planID)
	now := w.Now().UTC()
	row := w.subscriptionRow(&ps, u.ID, status, plan)
	var prev *Sub
	stale := false
	err = db.InTx(ctx, w.pool, func(tx db.Tx) error {
		// Polar sends subscription.updated and a specific event for each
		// change, and a retry can arrive after a newer one: a payload older
		// than the one applied last changes nothing.
		if _, err := tx.Exec(ctx, "select 1 from subscriptions where id = $1 for update", ps.ID); err != nil {
			return err
		}
		if cur, err := GetSubscription(ctx, tx, ps.ID); err == nil && cur.SourceModifiedAt != nil && row.SourceModifiedAt != nil && row.SourceModifiedAt.Before(*cur.SourceModifiedAt) {
			stale = true
			return nil
		} else if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		prev, err = upsertSubscription(ctx, tx, row)
		if err != nil {
			return err
		}
		if ps.CustomerID != "" {
			if _, err := tx.Exec(ctx, "update users set billing_customer_id = $2 where id = $1 and billing_customer_id is null", u.ID, ps.CustomerID); err != nil {
				return err
			}
		}
		if status == StatusPastDue && (prev == nil || prev.Status != StatusPastDue) {
			if err := w.paymentFailed(ctx, tx, u.ID, &row, now); err != nil {
				return err
			}
		}
		if err := projectStatus(ctx, tx, u.ID, status, now); err != nil {
			return err
		}
		return w.subscriptionEvents(ctx, tx, u, prev, &row, plan, now)
	})
	if err != nil {
		return err
	}
	if stale {
		w.log.Info("polar webhook older than the subscription row", "event", obs.EventBillingWebhook, "kind", ev.Type, "result", "stale")
		return nil
	}
	if w.m != nil {
		w.m.BillingSubscriptions.WithLabelValues(plan.ID, status).Inc()
	}
	if prev == nil && IsLive(status) && w.Seats != nil {
		if err := w.Seats.Converted(ctx, u.ID.String()); err != nil {
			return fmt.Errorf("record the seat conversion: %w", err)
		}
	}
	if status == StatusCanceled && (prev == nil || prev.Status != StatusCanceled) && w.Stop != nil {
		if _, err := stopUserMachines(ctx, w.pool, w.Stop, w.m, w.log, u.ID, StopReasonEnded); err != nil {
			return err
		}
	}
	return nil
}

// paymentFailed: a renewal's payment failed and the subscription became
// past_due. The account is past_due from now (day 0 of PRICING.md
// "Failed payments") and the payment_failed email goes out. Polar's
// retries that fail again change nothing (day 2's email is the dunning
// tick's).
func (w *Webhooks) paymentFailed(ctx context.Context, tx db.Tx, userID uuid.UUID, sub *Sub, now time.Time) error {
	tag, err := tx.Exec(ctx, `update users set billing_status = 'past_due', past_due_since = coalesce(past_due_since, $2)
		where id = $1 and billing_status in ('trial', 'active', 'none')`, userID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	_, err = events.InsertAccount(ctx, tx, userID, now, KindPaymentFailed, paymentFailed(sub, now))
	return err
}

// subscriptionEvents emits the account events a change calls for
// (I-291): subscription_cancelled when a cancellation is scheduled,
// subscription_ended when the status becomes canceled, plan_changed when
// the plan changes.
func (w *Webhooks) subscriptionEvents(ctx context.Context, tx db.Tx, u *store.User, prev, cur *Sub, plan Plan, now time.Time) error {
	if cur.Status == StatusCanceled && (prev == nil || prev.Status != StatusCanceled) {
		_, err := events.InsertAccount(ctx, tx, u.ID, now, KindSubscriptionEnded, subscriptionEnded(plan, now))
		return err
	}
	if cur.CancelAt != nil && (prev == nil || prev.CancelAt == nil) {
		if _, err := events.InsertAccount(ctx, tx, u.ID, now, KindSubscriptionCancelled, SubscriptionCancelledPayload{Plan: plan.ID, EndsAt: cur.CancelAt.UTC()}); err != nil {
			return err
		}
	}
	if prev != nil && prev.Plan != cur.Plan {
		_, err := events.InsertAccount(ctx, tx, u.ID, now, KindPlanChanged, PlanChangedPayload{FromPlan: prev.PlanOrSolo().ID, ToPlan: plan.ID, EffectiveAt: now})
		return err
	}
	return nil
}

// orderPaid: a payment went through. The account is active,
// past_due_since is cleared and a billing suspension is lifted (an
// operator's is not). A trialing subscription's order is the checkout's
// $0 one: the account stays trial until the first real charge.
func (w *Webhooks) orderPaid(ctx context.Context, ev *Event) error {
	var o struct {
		ID             string         `json:"id"`
		CustomerID     string         `json:"customer_id"`
		SubscriptionID string         `json:"subscription_id"`
		TotalAmount    int64          `json:"total_amount"`
		Metadata       map[string]any `json:"metadata"`
		Customer       *struct {
			ExternalID string `json:"external_id"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(ev.Data, &o); err != nil {
		return fmt.Errorf("decode the order: %w", err)
	}
	// Only a subscription's order moves the account; anything else sold
	// in the organization is not repose's.
	if o.SubscriptionID == "" {
		return nil
	}
	uid := uuid.Nil
	if v, _ := o.Metadata["user_id"].(string); v != "" {
		uid, _ = uuid.Parse(v)
	}
	if uid == uuid.Nil && o.Customer != nil {
		uid, _ = uuid.Parse(o.Customer.ExternalID)
	}
	u, err := w.resolveUser(ctx, uid, o.CustomerID)
	if err != nil {
		return err
	}
	return db.InTx(ctx, w.pool, func(tx db.Tx) error {
		subStatus := ""
		if cur, err := GetSubscription(ctx, tx, o.SubscriptionID); err == nil {
			subStatus = cur.Status
		} else if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if _, err := tx.Exec(ctx, "update subscriptions set status = 'active' where id = $1 and status = 'past_due'", o.SubscriptionID); err != nil {
			return err
		}
		if subStatus == StatusTrialing || o.TotalAmount == 0 && subStatus == "" {
			return nil
		}
		// A payment returns the account to active: from past_due, and from a
		// suspension the 3-day stop made (suspended_reason billing). An
		// operator's suspension stays. A trial or plan-less account moves
		// only when the order's subscription is known to be past its trial;
		// otherwise the subscription event projects the status.
		fromTrial := subStatus == StatusActive || subStatus == StatusPastDue
		_, err := tx.Exec(ctx, `update users set billing_status = 'active', past_due_since = null, has_card = true,
			suspended_at = case when suspended_reason = 'billing' then null else suspended_at end,
			suspended_reason = case when suspended_reason = 'billing' then null else suspended_reason end
			where id = $1 and (billing_status in ('past_due', 'active')
			or (billing_status = 'suspended' and suspended_reason = 'billing')
			or ($2 and billing_status in ('trial', 'none')))`, u.ID, fromTrial)
		return err
	})
}
