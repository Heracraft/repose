package billing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

// seatsRecorder records Seats.Converted calls.
type seatsRecorder struct {
	converted []string
}

func (s *seatsRecorder) Reserve(context.Context, string, int) (bool, *waitlist.Place, error) {
	return true, nil, nil
}
func (s *seatsRecorder) Converted(_ context.Context, userID string) error {
	s.converted = append(s.converted, userID)
	return nil
}
func (s *seatsRecorder) Count(context.Context) (waitlist.Count, error) { return waitlist.Count{}, nil }

var evSeq int

// event builds a Paddle notification body.
func event(kind string, data map[string]any) []byte {
	evSeq++
	b, _ := json.Marshal(map[string]any{"event_id": fmt.Sprintf("evt_%06d", evSeq), "event_type": kind, "occurred_at": "2026-10-03T12:00:00Z", "notification_id": "ntf_x", "data": data})
	return b
}

// subData is a Paddle subscription object for a user on a plan.
func subData(id string, a account, priceID, status string, extra map[string]any) map[string]any {
	d := map[string]any{"id": id, "status": status, "customer_id": "ctm_" + a.Handle, "currency_code": "USD", "next_billed_at": "2026-11-01T00:00:00Z",
		"current_billing_period": map[string]any{"starts_at": "2026-10-01T00:00:00Z", "ends_at": "2026-11-01T00:00:00Z"},
		"custom_data":            map[string]any{"user_id": a.UserID.String()},
		"items": []any{map[string]any{"status": "active", "quantity": 1, "price": map[string]any{"id": priceID, "product_id": "pro_x"},
			"trial_dates": map[string]any{"starts_at": "2026-10-01T00:00:00Z", "ends_at": "2026-10-08T00:00:00Z"}}}}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func newHooks(t *testing.T, pool *db.Pool, f *fakePaddle, stop *stopRecorder, seats *seatsRecorder) *billing.Webhooks {
	t.Helper()
	w := billing.NewWebhooks(pool, testConfig(f), nop(), quiet())
	w.Now = at(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	if stop != nil {
		w.Stop = stop
	}
	if seats != nil {
		w.Seats = seats
	}
	return w
}

func post(t *testing.T, w *billing.Webhooks, f *fakePaddle, body []byte) error {
	t.Helper()
	_, err := w.Handle(context.Background(), body, f.Sign(body, w.Now()))
	return err
}

func TestWebhookSignatureSkewAndDedupe(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePaddle()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	a := seedAccount(t, pool, "", "none", "", "")
	body := event("subscription.created", subData("sub_sig", a, "pri_solo_test", "trialing", nil))

	// Bad secret, missing header, tampered body, stale ts: refused.
	for name, hdr := range map[string]string{
		"wrong secret": billing.Sign("other", w.Now(), body),
		"no header":    "",
		"garbage":      "ts=abc;h1=00",
		"stale":        billing.Sign(f.Secret(), w.Now().Add(-6*time.Minute), body),
		"future":       billing.Sign(f.Secret(), w.Now().Add(6*time.Minute), body),
	} {
		if _, err := w.Handle(context.Background(), body, hdr); !errors.Is(err, billing.ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tampered := append([]byte{}, body...)
	tampered[len(tampered)-2] = ' '
	if _, err := w.Handle(context.Background(), tampered, f.Sign(body, w.Now())); !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("tampered body: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), "select count(*) from paddle_events").Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused event was recorded: %d %v", n, err)
	}
	// Within the skew, and with a second h1 during a rotation: accepted.
	rotated := billing.NewWebhooks(pool, testConfig(f), nop(), quiet(), "old_secret")
	rotated.Now = w.Now
	hdr := billing.Sign("old_secret", w.Now().Add(4*time.Minute), body)
	hdr += ";h1=" + strings.TrimPrefix(strings.SplitN(billing.Sign("unrelated", w.Now().Add(4*time.Minute), body), ";h1=", 2)[1], "")
	if kind, err := rotated.Handle(context.Background(), body, hdr); err != nil || kind != "subscription.created" {
		t.Fatalf("rotation secret within skew: %s %v", kind, err)
	}
	// A duplicate is ErrDuplicate and changes nothing.
	if err := post(t, w, f, body); !errors.Is(err, billing.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := pool.QueryRow(context.Background(), "select count(*) from paddle_events where processed_at is not null").Scan(&n); err != nil || n != 1 {
		t.Fatalf("processed rows %d %v", n, err)
	}
	// An unknown type is recorded and ignored.
	if err := post(t, w, f, event("address.created", map[string]any{"id": "add_1"})); err != nil {
		t.Fatalf("unknown type: %v", err)
	}
	var typ string
	if err := pool.QueryRow(context.Background(), "select type from paddle_events where type = 'address.created'").Scan(&typ); err != nil {
		t.Fatal("the unknown event was not recorded")
	}
	// An event for a customer that is not ours records the error.
	if err := post(t, w, f, event("subscription.created", map[string]any{"id": "sub_alien", "status": "active", "customer_id": "ctm_alien",
		"items": []any{map[string]any{"price": map[string]any{"id": "pri_solo_test"}}}})); err == nil {
		t.Fatal("an unknown customer's subscription was applied")
	}
	var errText string
	if err := pool.QueryRow(context.Background(), "select error from paddle_events where type = 'subscription.created' and error is not null").Scan(&errText); err != nil || !strings.Contains(errText, "ctm_alien") {
		t.Fatalf("error recorded: %q %v", errText, err)
	}
}

// Every subscription event, in the order a life runs: created (trialing)
// makes the account trial and converts the seat; activated makes it
// active; updated with the other price is plan_changed; a scheduled
// cancel is subscription_cancelled; canceled is subscription_ended and
// stops the machines; past_due, paused and resumed project their status.
func TestWebhookSubscriptionLifecycle(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePaddle()
	defer f.Close()
	stop, seats := &stopRecorder{}, &seatsRecorder{}
	w := newHooks(t, pool, f, stop, seats)
	a := seedAccount(t, pool, "", "none", "large", "running")
	ctx := context.Background()

	must := func(kind string, data map[string]any) {
		t.Helper()
		if err := post(t, w, f, event(kind, data)); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	must("subscription.created", subData("sub_life", a, "pri_solo_test", "trialing", nil))
	sub, err := billing.GetSubscription(ctx, pool, "sub_life")
	if err != nil || sub.Plan != "solo" || sub.Status != "trialing" || sub.Seats != 1 || sub.UserID != a.UserID || sub.TrialEnd == nil || sub.PeriodEnd == nil || sub.NextBilledAt == nil {
		t.Fatalf("row after created: %+v %v", sub, err)
	}
	if userField(t, pool, a, "billing_status") != "trial" || userField(t, pool, a, "has_card") != "true" {
		t.Fatal("created did not project trial + has_card")
	}
	if len(seats.converted) != 1 || seats.converted[0] != a.UserID.String() {
		t.Fatalf("Seats.Converted once on the first live subscription: %v", seats.converted)
	}
	// The introductory discount is recorded with its end (DECISIONS I-497).
	must("subscription.trialing", subData("sub_life", a, "pri_solo_test", "trialing", map[string]any{"discount": map[string]any{"id": "dsc_intro_test", "starts_at": "2026-10-08T00:00:00Z", "ends_at": "2027-01-08T00:00:00Z"}}))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.DiscountID == nil || *sub.DiscountID != "dsc_intro_test" || sub.DiscountEndsAt == nil || !sub.DiscountEndsAt.Equal(time.Date(2027, 1, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("discount: %v %v", sub.DiscountID, sub.DiscountEndsAt)
	}
	must("subscription.trialing", subData("sub_life", a, "pri_solo_test", "trialing", nil))
	must("subscription.activated", subData("sub_life", a, "pri_solo_test", "active", map[string]any{"items": []any{map[string]any{"status": "active", "quantity": 1, "price": map[string]any{"id": "pri_solo_test"}}}}))
	if userField(t, pool, a, "billing_status") != "active" || len(seats.converted) != 1 {
		t.Fatal("activated: active, and no second conversion")
	}
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.TrialEnd != nil {
		t.Fatal("trial_end cleared once the item has no trial dates")
	}
	// Plan change.
	must("subscription.updated", subData("sub_life", a, "pri_plus_test", "active", nil))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.Plan != "plus" || sub.Seats != 2 {
		t.Fatalf("updated to plus: %+v", sub)
	}
	// Cancellation scheduled.
	must("subscription.updated", subData("sub_life", a, "pri_plus_test", "active", map[string]any{"scheduled_change": map[string]any{"action": "cancel", "effective_at": "2026-11-01T00:00:00Z"}}))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.CancelAt == nil || !sub.CancelAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cancel_at: %+v", sub.CancelAt)
	}
	// Resumed: the scheduled change is gone.
	must("subscription.resumed", subData("sub_life", a, "pri_plus_test", "active", nil))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.CancelAt != nil {
		t.Fatal("resume clears cancel_at")
	}
	// Past due, paused, then canceled.
	must("subscription.past_due", subData("sub_life", a, "pri_plus_test", "past_due", nil))
	if userField(t, pool, a, "billing_status") != "past_due" || userField(t, pool, a, "past_due_since") == "" {
		t.Fatal("past_due projected with past_due_since")
	}
	must("subscription.paused", subData("sub_life", a, "pri_plus_test", "paused", nil))
	if userField(t, pool, a, "billing_status") != "none" {
		t.Fatal("paused -> none")
	}
	if len(stop.calls) != 0 {
		t.Fatal("paused stops nothing itself")
	}
	must("subscription.canceled", subData("sub_life", a, "pri_plus_test", "canceled", nil))
	if userField(t, pool, a, "billing_status") != "none" {
		t.Fatal("canceled -> none")
	}
	if len(stop.calls) != 1 || *stop.calls[0].ProjectID != a.ProjectID || stop.calls[0].Params["reason"] != "billing" || stop.calls[0].Params["snapshot"] != true {
		t.Fatalf("canceled stops the running machine with a snapshot: %+v", stop.calls)
	}
	// A second canceled (replayed by Paddle with a new id) stops nothing more.
	must("subscription.canceled", subData("sub_life", a, "pri_plus_test", "canceled", nil))
	if len(stop.calls) != 1 {
		t.Fatal("canceled twice stopped twice")
	}
	kinds := eventKinds(t, pool, a)
	want := []string{"plan_changed", "subscription_cancelled", "subscription_ended"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("account events %v, want %v", kinds, want)
	}
	if outboxEmails(t, pool, a) != 3 {
		t.Fatalf("%d emails queued, want 3", outboxEmails(t, pool, a))
	}
	if p := accountEmail(t, pool, a, "plan_changed", "from Solo to Plus", "3 October 2026 at 12:00 UTC"); p["from_plan"] != "solo" || p["to_plan"] != "plus" {
		t.Fatalf("plan_changed payload %v", p)
	}
	if p := accountEmail(t, pool, a, "subscription_cancelled", "Plus", "1 November 2026 at 00:00 UTC"); p["plan"] != "plus" || p["ends_at"] != "2026-11-01T00:00:00Z" {
		t.Fatalf("subscription_cancelled payload %v", p)
	}
	if p := accountEmail(t, pool, a, "subscription_ended", "Plus", "3 October 2026 at 12:00 UTC", "2 November 2026 at 12:00 UTC"); p["plan"] != "plus" || p["retention_until"] != "2026-11-02T12:00:00Z" {
		t.Fatalf("subscription_ended payload %v", p)
	}
	var live *billing.Sub
	if live, err = billing.LiveSubscription(ctx, pool, a.UserID); err != nil || live != nil {
		t.Fatalf("no live subscription after cancel: %+v %v", live, err)
	}
}

// A subscription whose price is not one of ours is an error the
// paddle_events row keeps; an unknown status too.
func TestWebhookRefusesForeignPrices(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePaddle()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	a := seedAccount(t, pool, "", "none", "", "")
	if err := post(t, w, f, event("subscription.created", subData("sub_f", a, "pri_other", "active", nil))); err == nil || !strings.Contains(err.Error(), "pri_other") {
		t.Fatalf("foreign price: %v", err)
	}
	if userField(t, pool, a, "billing_status") != "none" {
		t.Fatal("a foreign subscription changed the account")
	}
}

// transaction.completed and transaction.payment_failed.
func TestWebhookTransactions(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePaddle()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	ctx := context.Background()

	// The checkout's $0 transaction on a trialing subscription leaves the
	// account on trial.
	a := seedAccount(t, pool, "solo", "trial", "", "")
	txn := func(sub string, a account, items ...string) map[string]any {
		d := map[string]any{"id": "txn_" + uuid.NewString()[:8], "status": "completed", "customer_id": "ctm_" + a.Handle, "subscription_id": sub, "origin": "web", "custom_data": map[string]any{"user_id": a.UserID.String()}}
		var its []any
		for _, p := range items {
			its = append(its, map[string]any{"price": map[string]any{"id": "pri_x", "product_id": p}})
		}
		d["items"] = its
		return d
	}
	if err := post(t, w, f, event("transaction.completed", txn(a.SubID, a))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, a, "billing_status") != "trial" {
		t.Fatal("the trial's $0 transaction made the account active")
	}

	// A checkout payment that fails has no subscription: nothing happens.
	if err := post(t, w, f, event("transaction.payment_failed", txn("", a))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, a, "billing_status") != "trial" || len(eventKinds(t, pool, a)) != 0 {
		t.Fatal("a failed checkout marked the account past due")
	}

	// A renewal that fails: past_due, past_due_since, one payment_failed
	// email; a second failure (Paddle's retry) sends no second email.
	b := seedAccount(t, pool, "plus", "active", "", "")
	if err := post(t, w, f, event("transaction.payment_failed", txn(b.SubID, b))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, b, "billing_status") != "past_due" || userField(t, pool, b, "past_due_since") == "" {
		t.Fatal("payment_failed -> past_due")
	}
	sub, _ := billing.GetSubscription(ctx, pool, b.SubID)
	if sub.Status != "past_due" {
		t.Fatal("subscription row past_due")
	}
	if err := post(t, w, f, event("transaction.payment_failed", txn(b.SubID, b))); err != nil {
		t.Fatal(err)
	}
	if k := eventKinds(t, pool, b); len(k) != 1 || k[0] != "payment_failed" {
		t.Fatalf("payment_failed events %v, want one", k)
	}
	// The payment goes through: active again, past_due_since cleared, and
	// an overage line the transaction carried gets its id.
	if _, err := pool.Exec(ctx, "insert into overage_charges (subscription_id, period_start, egress_gb, cents) values ($1, $2, 10, 50)", b.SubID, b.Period.Start); err != nil {
		t.Fatal(err)
	}
	paid := txn(b.SubID, b, "pro_plan", "pro_overage_test")
	if err := post(t, w, f, event("transaction.completed", paid)); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, b, "billing_status") != "active" || userField(t, pool, b, "past_due_since") != "" {
		t.Fatal("completed -> active with past_due_since cleared")
	}
	sub, _ = billing.GetSubscription(ctx, pool, b.SubID)
	if sub.Status != "active" {
		t.Fatal("subscription row active")
	}
	var txnID string
	if err := pool.QueryRow(ctx, "select paddle_transaction_id from overage_charges where subscription_id = $1", b.SubID).Scan(&txnID); err != nil || txnID != paid["id"] {
		t.Fatalf("overage line transaction id %q %v", txnID, err)
	}

	// A billing suspension is lifted by a payment; an operator's is not.
	c := seedAccount(t, pool, "solo", "past_due", "", "")
	if _, err := pool.Exec(ctx, "update users set billing_status = 'suspended', suspended_at = now(), suspended_reason = 'billing' where id = $1", c.UserID); err != nil {
		t.Fatal(err)
	}
	if err := post(t, w, f, event("transaction.completed", txn(c.SubID, c))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, c, "billing_status") != "active" || userField(t, pool, c, "suspended_at") != "" {
		t.Fatal("a billing suspension is cleared by a payment")
	}
	d := seedAccount(t, pool, "solo", "active", "", "")
	if _, err := pool.Exec(ctx, "update users set billing_status = 'suspended', suspended_at = now(), suspended_reason = 'abuse' where id = $1", d.UserID); err != nil {
		t.Fatal(err)
	}
	if err := post(t, w, f, event("transaction.completed", txn(d.SubID, d))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, d, "billing_status") != "suspended" {
		t.Fatal("an operator's suspension survives a payment")
	}
	// Likewise a subscription event never un-suspends.
	if err := post(t, w, f, event("subscription.updated", subData(d.SubID, d, "pri_solo_test", "active", nil))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, d, "billing_status") != "suspended" {
		t.Fatal("subscription.updated un-suspended the account")
	}
}

// A subscription found by customer id alone (no custom_data) still lands
// on the right account.
func TestWebhookResolvesByCustomer(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePaddle()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	a := seedAccount(t, pool, "", "none", "", "")
	if _, err := pool.Exec(context.Background(), "update users set paddle_customer_id = $2 where id = $1", a.UserID, "ctm_"+a.Handle); err != nil {
		t.Fatal(err)
	}
	d := subData("sub_byc", a, "pri_plus_test", "active", nil)
	delete(d, "custom_data")
	if err := post(t, w, f, event("subscription.activated", d)); err != nil {
		t.Fatal(err)
	}
	sub, err := billing.LiveSubscription(context.Background(), pool, a.UserID)
	if err != nil || sub == nil || sub.Plan != "plus" {
		t.Fatalf("resolved by customer: %+v %v", sub, err)
	}
	if userField(t, pool, a, "billing_status") != "active" {
		t.Fatal("status")
	}
}
