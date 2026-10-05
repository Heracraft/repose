package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// The Paddle webhook (09-billing.md §5.11, api.md POST /billing/webhook).
// Paddle-Signature is the authentication: `ts=...;h1=...`, an HMAC-SHA256
// of `ts:body` with the endpoint secret, refused past five minutes of
// skew. Every event is deduped on event_id, the primary key of
// paddle_events: a duplicate delivery is a no-op and a replay reproduces
// the same rows.

// ErrBadSignature is returned when Paddle-Signature does not verify. The
// route answers 400 and logs the event type only; the body is never
// logged (it carries the customer's details).
var ErrBadSignature = errors.New("paddle webhook signature is invalid")

// ErrDuplicate means the event id was already recorded.
var ErrDuplicate = errors.New("paddle event already processed")

// The event types the endpoint subscribes to and handles.
var WebhookEvents = []string{
	"subscription.created", "subscription.activated", "subscription.trialing", "subscription.updated",
	"subscription.past_due", "subscription.paused", "subscription.resumed", "subscription.canceled",
	"transaction.completed", "transaction.payment_failed",
}

// SignatureSkew is how far a webhook's ts may be from now.
const SignatureSkew = 5 * time.Minute

// Webhooks applies Paddle events to the database.
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

// Sign produces a Paddle-Signature header for a body, which is what the
// fake Paddle and the tests use to post events.
func Sign(secret string, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10) + ":"))
	mac.Write(body)
	return "ts=" + strconv.FormatInt(ts.Unix(), 10) + ";h1=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a Paddle-Signature header against the body: any h1 with
// any accepted secret, constant-time, within the skew.
func (w *Webhooks) Verify(header string, body []byte) error {
	var ts string
	var h1s []string
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "ts":
			ts = v
		case "h1":
			h1s = append(h1s, v)
		}
	}
	if ts == "" || len(h1s) == 0 {
		return fmt.Errorf("%w: header has no ts or h1", ErrBadSignature)
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: ts is not a number", ErrBadSignature)
	}
	if skew := w.Now().Sub(time.Unix(unix, 0)); skew > SignatureSkew || skew < -SignatureSkew {
		return fmt.Errorf("%w: ts is %s from now", ErrBadSignature, skew.Round(time.Second))
	}
	for _, secret := range w.secrets {
		if secret == "" {
			continue
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + ":"))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		for _, got := range h1s {
			if subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(got))) == 1 {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no h1 matched", ErrBadSignature)
}

// Event is Paddle's notification envelope.
type Event struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// Handle verifies the signature, records the event and applies it. A
// duplicate returns ErrDuplicate, which the route answers 200 to, because
// Paddle retries anything else.
func (w *Webhooks) Handle(ctx context.Context, payload []byte, sigHeader string) (kind string, err error) {
	if len(w.secrets) == 0 || w.secrets[0] == "" {
		return "", ErrDisabled
	}
	var ev Event
	if err := json.Unmarshal(payload, &ev); err == nil {
		kind = ev.EventType
	}
	if err := w.Verify(sigHeader, payload); err != nil {
		return kind, err
	}
	if kind == "" || ev.EventID == "" {
		return kind, fmt.Errorf("%w: body has no event_id or event_type", ErrBadSignature)
	}
	occurred := w.Now().UTC()
	if t := paddleTime(ev.OccurredAt); t != nil {
		occurred = *t
	}
	tag, err := w.pool.Exec(ctx, "insert into paddle_events (id, type, occurred_at) values ($1, $2, $3) on conflict (id) do nothing", ev.EventID, kind, occurred)
	if err != nil {
		return kind, err
	}
	if tag.RowsAffected() == 0 {
		return kind, ErrDuplicate
	}
	applyErr := w.apply(ctx, &ev)
	if applyErr != nil {
		if _, err := w.pool.Exec(ctx, "update paddle_events set error = $2 where id = $1", ev.EventID, applyErr.Error()); err != nil {
			return kind, err
		}
		return kind, applyErr
	}
	if _, err := w.pool.Exec(ctx, "update paddle_events set processed_at = now() where id = $1", ev.EventID); err != nil {
		return kind, err
	}
	w.log.Info("paddle webhook applied", "event", obs.EventBillingWebhook, "kind", kind, "result", "ok")
	return kind, nil
}

func (w *Webhooks) apply(ctx context.Context, ev *Event) error {
	switch {
	case strings.HasPrefix(ev.EventType, "subscription."):
		return w.subscription(ctx, ev)
	case ev.EventType == "transaction.completed":
		return w.transactionCompleted(ctx, ev)
	case ev.EventType == "transaction.payment_failed":
		return w.transactionFailed(ctx, ev)
	}
	// Anything else the endpoint is subscribed to is recorded and ignored.
	return nil
}

// resolveUser finds the account a Paddle object belongs to: custom_data's
// user_id first, then the customer id. An unknown one is an error the
// paddle_events row keeps.
func (w *Webhooks) resolveUser(ctx context.Context, custom map[string]any, customerID string) (*store.User, error) {
	if custom != nil {
		if v, _ := custom["user_id"].(string); v != "" {
			if id, err := uuid.Parse(v); err == nil {
				u, err := store.GetUser(ctx, w.pool, id)
				if err == nil {
					return u, nil
				}
				if !errors.Is(err, db.ErrNotFound) {
					return nil, err
				}
			}
		}
	}
	if customerID == "" {
		return nil, errors.New("event names no user and no customer")
	}
	var id uuid.UUID
	err := w.pool.QueryRow(ctx, "select id from users where paddle_customer_id = $1", customerID).Scan(&id)
	if db.IsNoRows(err) {
		// Not ours: a customer made in Paddle's dashboard, or another
		// product on the same account.
		return nil, fmt.Errorf("no user for Paddle customer %s", customerID)
	}
	if err != nil {
		return nil, err
	}
	return store.GetUser(ctx, w.pool, id)
}

// subscription upserts the row from Paddle's view and projects it onto the
// account: status, has_card, the seat conversion and the account events.
func (w *Webhooks) subscription(ctx context.Context, ev *Event) error {
	var ps Subscription
	if err := json.Unmarshal(ev.Data, &ps); err != nil {
		return fmt.Errorf("decode the %s subscription: %w", ev.EventType, err)
	}
	if ps.ID == "" {
		return errors.New("subscription event without an id")
	}
	u, err := w.resolveUser(ctx, ps.CustomData, ps.CustomerID)
	if err != nil {
		return err
	}
	planID := w.cfg.PlanForPrice(ps.PriceID())
	if planID == "" {
		return fmt.Errorf("subscription %s has no repose plan price (price %q)", ps.ID, ps.PriceID())
	}
	plan, _ := PlanByID(planID)
	now := w.Now().UTC()
	row := Sub{ID: ps.ID, UserID: u.ID, CustomerID: ps.CustomerID, Plan: plan.ID, Status: ps.Status, Seats: plan.Seats,
		NextBilledAt: paddleTime(ps.NextBilledAt), TrialEnd: ps.TrialEnd()}
	if ps.CurrentBillingPeriod != nil {
		row.PeriodStart = paddleTime(ps.CurrentBillingPeriod.StartsAt)
		row.PeriodEnd = paddleTime(ps.CurrentBillingPeriod.EndsAt)
	}
	if ps.Discount != nil && w.cfg.DiscountIntro != "" && ps.Discount.ID == w.cfg.DiscountIntro {
		row.Intro, row.IntroUntil = true, paddleTime(ps.Discount.EndsAt)
	}
	if ps.ScheduledChange != nil && ps.ScheduledChange.Action == "cancel" {
		row.CancelAt = paddleTime(ps.ScheduledChange.EffectiveAt)
	}
	if !IsLive(ps.Status) && ps.Status != StatusCanceled && ps.Status != StatusPaused {
		return fmt.Errorf("subscription %s has unknown status %q", ps.ID, ps.Status)
	}
	var prev *Sub
	err = db.InTx(ctx, w.pool, func(tx db.Tx) error {
		// The downgrade a scheduled plan change is waiting for is ours to
		// keep: Paddle reports the current price until it takes effect.
		if cur, err := GetSubscription(ctx, tx, ps.ID); err == nil && cur.ScheduledPlan != nil && *cur.ScheduledPlan != plan.ID && ps.Status != StatusCanceled {
			row.ScheduledPlan = cur.ScheduledPlan
		}
		prev, err = upsertSubscription(ctx, tx, row)
		if err != nil {
			return err
		}
		if ps.CustomerID != "" {
			if _, err := tx.Exec(ctx, "update users set paddle_customer_id = $2 where id = $1 and paddle_customer_id is null", u.ID, ps.CustomerID); err != nil {
				return err
			}
		}
		if err := projectStatus(ctx, tx, u.ID, ps.Status, now); err != nil {
			return err
		}
		return w.subscriptionEvents(ctx, tx, u, prev, &row, plan, now)
	})
	if err != nil {
		return err
	}
	if w.m != nil {
		w.m.BillingSubscriptions.WithLabelValues(plan.ID, ps.Status).Inc()
	}
	if prev == nil && IsLive(ps.Status) && w.Seats != nil {
		if err := w.Seats.Converted(ctx, u.ID.String()); err != nil {
			return fmt.Errorf("record the seat conversion: %w", err)
		}
	}
	if ps.Status == StatusCanceled && (prev == nil || prev.Status != StatusCanceled) && w.Stop != nil {
		if _, err := stopUserMachines(ctx, w.pool, w.Stop, w.m, w.log, u.ID, StopReasonEnded); err != nil {
			return err
		}
	}
	return nil
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

// transactionObject is the subset of a transaction the two handlers read.
type transactionObject struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	CustomerID     string         `json:"customer_id"`
	SubscriptionID string         `json:"subscription_id"`
	Origin         string         `json:"origin"`
	CustomData     map[string]any `json:"custom_data"`
	Items          []struct {
		Price struct {
			ID        string `json:"id"`
			ProductID string `json:"product_id"`
		} `json:"price"`
	} `json:"items"`
}

// transactionCompleted: a payment went through. The account is active,
// past_due_since is cleared, a billing suspension is lifted (an operator's
// is not), and an overage line the transaction carried gets its id.
func (w *Webhooks) transactionCompleted(ctx context.Context, ev *Event) error {
	var t transactionObject
	if err := json.Unmarshal(ev.Data, &t); err != nil {
		return fmt.Errorf("decode the transaction: %w", err)
	}
	u, err := w.resolveUser(ctx, t.CustomData, t.CustomerID)
	if err != nil {
		return err
	}
	return db.InTx(ctx, w.pool, func(tx db.Tx) error {
		subStatus := ""
		if t.SubscriptionID != "" {
			if cur, err := GetSubscription(ctx, tx, t.SubscriptionID); err == nil {
				subStatus = cur.Status
			} else if !errors.Is(err, db.ErrNotFound) {
				return err
			}
			if _, err := tx.Exec(ctx, "update subscriptions set status = 'active' where id = $1 and status = 'past_due'", t.SubscriptionID); err != nil {
				return err
			}
			for _, it := range t.Items {
				if w.cfg.ProductOverage != "" && it.Price.ProductID == w.cfg.ProductOverage {
					if _, err := tx.Exec(ctx, `update overage_charges set paddle_transaction_id = $2 where subscription_id = $1 and paddle_transaction_id is null
						and period_start = (select max(period_start) from overage_charges where subscription_id = $1 and paddle_transaction_id is null)`, t.SubscriptionID, t.ID); err != nil {
						return err
					}
					break
				}
			}
		}
		// A trialing subscription's completed transaction is the checkout's
		// $0 one: the account stays trial until the first real charge.
		if subStatus == StatusTrialing {
			return nil
		}
		// A payment returns the account to active: from past_due, and from a
		// suspension the 3-day stop made (suspended_reason billing). An
		// operator's suspension stays. A trial or plan-less account moves
		// only when the transaction's subscription is known to be past its
		// trial; otherwise the subscription event projects the status.
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

// transactionFailed: a payment failed. The account is past_due from now
// (day 0 of PRICING.md "Failed payments") and the payment_failed email
// goes out.
func (w *Webhooks) transactionFailed(ctx context.Context, ev *Event) error {
	var t transactionObject
	if err := json.Unmarshal(ev.Data, &t); err != nil {
		return fmt.Errorf("decode the transaction: %w", err)
	}
	u, err := w.resolveUser(ctx, t.CustomData, t.CustomerID)
	if err != nil {
		return err
	}
	now := w.Now().UTC()
	return db.InTx(ctx, w.pool, func(tx db.Tx) error {
		// Only a subscription's payment puts the account past due. A card
		// declined at checkout fails a transaction with no subscription;
		// the user simply has no plan yet.
		if t.SubscriptionID == "" {
			return nil
		}
		cur, err := GetSubscription(ctx, tx, t.SubscriptionID)
		if errors.Is(err, db.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !cur.Live() {
			return nil
		}
		if _, err := tx.Exec(ctx, "update subscriptions set status = 'past_due' where id = $1 and status in ('trialing','active')", t.SubscriptionID); err != nil {
			return err
		}
		// The first failure moves the account and sends day 0's email;
		// Paddle's retries that fail again change nothing (day 2's email is
		// the dunning tick's).
		tag, err := tx.Exec(ctx, `update users set billing_status = 'past_due', past_due_since = coalesce(past_due_since, $2)
			where id = $1 and billing_status in ('trial', 'active', 'none')`, u.ID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		_, err = events.InsertAccount(ctx, tx, u.ID, now, KindPaymentFailed, paymentFailed(cur, now))
		return err
	})
}
