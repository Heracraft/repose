package billing_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// event builds a Polar webhook body; its id travels in webhook-id.
type hookEvent struct {
	id   string
	body []byte
}

func event(kind string, data map[string]any) hookEvent {
	evSeq++
	b, _ := json.Marshal(map[string]any{"type": kind, "timestamp": "2026-10-03T12:00:00Z", "api_version": billing.APIVersion, "data": data})
	return hookEvent{id: fmt.Sprintf("msg_%06d", evSeq), body: b}
}

// subData is a Polar subscription object for a user on a plan.
func subData(id string, a account, productID, status string, extra map[string]any) map[string]any {
	d := map[string]any{"id": id, "status": status, "customer_id": "cus_" + a.Handle, "product_id": productID, "currency": "usd",
		"current_period_start": "2026-10-01T00:00:00Z", "current_period_end": "2026-11-01T00:00:00Z",
		"trial_start": "2026-10-01T00:00:00Z", "trial_end": "2026-10-08T00:00:00Z", "started_at": "2026-10-01T00:00:00Z",
		"cancel_at_period_end": false, "ends_at": nil, "discount_id": nil, "pending_update": nil,
		"metadata": map[string]any{"user_id": a.UserID.String()},
		"customer": map[string]any{"id": "cus_" + a.Handle, "external_id": a.UserID.String(), "email": a.Email}}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// orderData is a Polar order for a subscription.
func orderData(a account, subID string, total int64, reason string) map[string]any {
	return map[string]any{"id": "ord_" + uuid.NewString()[:8], "status": "paid", "paid": true, "customer_id": "cus_" + a.Handle, "subscription_id": subID,
		"total_amount": total, "billing_reason": reason, "metadata": map[string]any{}, "customer": map[string]any{"external_id": a.UserID.String()}}
}

func newHooks(t *testing.T, pool *db.Pool, f *fakePolar, stop *stopRecorder, seats *seatsRecorder) *billing.Webhooks {
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

func post(t *testing.T, w *billing.Webhooks, f *fakePolar, ev hookEvent) error {
	t.Helper()
	_, err := w.Handle(context.Background(), ev.body, f.Sign(ev.id, ev.body, w.Now()))
	return err
}

func TestWebhookSignatureSkewAndDedupe(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	a := seedAccount(t, pool, "", "none", "", "")
	ev := event("subscription.created", subData("sub_sig", a, "prod_solo_test", "trialing", nil))
	body := ev.body
	good := f.Sign(ev.id, body, w.Now())

	// Bad secret, missing headers, garbage, another event's id, stale or
	// future timestamp: refused.
	noSig := good
	noSig.Signature = ""
	noID := good
	noID.ID = ""
	otherID := good
	otherID.ID = "msg_other"
	for name, hdr := range map[string]billing.WebhookHeaders{
		"wrong secret": billing.Sign("whsec_b3RoZXI=", ev.id, w.Now(), body),
		"no signature": noSig,
		"no id":        noID,
		"garbage":      {ID: ev.id, Timestamp: "abc", Signature: "v1,AAAA"},
		"another id":   otherID,
		"stale":        f.Sign(ev.id, body, w.Now().Add(-6*time.Minute)),
		"future":       f.Sign(ev.id, body, w.Now().Add(6*time.Minute)),
	} {
		if _, err := w.Handle(context.Background(), body, hdr); !errors.Is(err, billing.ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tampered := append([]byte{}, body...)
	tampered[len(tampered)-2] = ' '
	if _, err := w.Handle(context.Background(), tampered, good); !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("tampered body: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), "select count(*) from billing_events").Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused event was recorded: %d %v", n, err)
	}
	// Within the skew, with the previous secret during a rotation, among
	// several signatures: accepted.
	rotated := billing.NewWebhooks(pool, testConfig(f), nop(), quiet(), "whsec_b2xkLXNlY3JldA==")
	rotated.Now = w.Now
	hdr := billing.Sign("whsec_b2xkLXNlY3JldA==", ev.id, w.Now().Add(4*time.Minute), body)
	hdr.Signature = "v1,bm90LXRoaXMtb25l " + hdr.Signature
	if kind, err := rotated.Handle(context.Background(), body, hdr); err != nil || kind != "subscription.created" {
		t.Fatalf("rotation secret within skew: %s %v", kind, err)
	}
	// A duplicate is ErrDuplicate and changes nothing.
	if err := post(t, w, f, ev); !errors.Is(err, billing.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := pool.QueryRow(context.Background(), "select count(*) from billing_events where processed_at is not null").Scan(&n); err != nil || n != 1 {
		t.Fatalf("processed rows %d %v", n, err)
	}
	// An unknown type is recorded and ignored.
	if err := post(t, w, f, event("benefit.created", map[string]any{"id": "ben_1"})); err != nil {
		t.Fatalf("unknown type: %v", err)
	}
	var typ string
	if err := pool.QueryRow(context.Background(), "select type from billing_events where type = 'benefit.created'").Scan(&typ); err != nil {
		t.Fatal("the unknown event was not recorded")
	}
	// An event for a customer that is not ours records the error.
	if err := post(t, w, f, event("subscription.created", map[string]any{"id": "sub_alien", "status": "active", "customer_id": "cus_alien", "product_id": "prod_solo_test"})); err == nil {
		t.Fatal("an unknown customer's subscription was applied")
	}
	var errText string
	if err := pool.QueryRow(context.Background(), "select error from billing_events where type = 'subscription.created' and error is not null").Scan(&errText); err != nil || !strings.Contains(errText, "cus_alien") {
		t.Fatalf("error recorded: %q %v", errText, err)
	}
}

// Polar's older secrets key the HMAC with the bytes of the whole string,
// and `polar listen` signs with a plain hex secret the same way.
func TestWebhookLegacySecret(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	cfg.WebhookSecret = "6t3c8ce2247c493a3ade20uea4484d64"
	w := billing.NewWebhooks(pool, cfg, nop(), quiet())
	a := seedAccount(t, pool, "", "none", "", "")
	ev := event("subscription.created", subData("sub_legacy", a, "prod_solo_test", "trialing", nil))
	ts := strconv.FormatInt(w.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(cfg.WebhookSecret))
	mac.Write([]byte(ev.id + "." + ts + "."))
	mac.Write(ev.body)
	hdr := billing.WebhookHeaders{ID: ev.id, Timestamp: ts, Signature: "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))}
	if _, err := w.Handle(context.Background(), ev.body, hdr); err != nil {
		t.Fatalf("legacy key: %v", err)
	}
}

// Every subscription event, in the order a life runs: created (trialing)
// makes the account trial and converts the seat; active makes it active;
// another product is plan_changed; a pending update is the scheduled
// downgrade; cancel_at_period_end is subscription_cancelled; past_due is
// the first payment failure; canceled is subscription_ended and stops the
// machines.
func TestWebhookSubscriptionLifecycle(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
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
	// A checkout that never paid is no subscription yet.
	must("subscription.created", subData("sub_life", a, "prod_solo_test", "incomplete", nil))
	if s, _ := billing.LatestSubscription(ctx, pool, a.UserID); s != nil {
		t.Fatalf("an incomplete subscription made a row: %+v", s)
	}
	must("subscription.created", subData("sub_life", a, "prod_solo_test", "trialing", map[string]any{"discount_id": "dsc_intro_test"}))
	sub, err := billing.GetSubscription(ctx, pool, "sub_life")
	if err != nil || sub.Plan != "solo" || sub.Status != "trialing" || sub.Seats != 1 || sub.UserID != a.UserID || sub.TrialEnd == nil || sub.PeriodEnd == nil || sub.NextBilledAt == nil || !sub.NextBilledAt.Equal(*sub.PeriodEnd) || sub.CustomerID != "cus_"+a.Handle {
		t.Fatalf("row after created: %+v %v", sub, err)
	}
	if userField(t, pool, a, "billing_status") != "trial" || userField(t, pool, a, "has_card") != "true" || userField(t, pool, a, "billing_customer_id") != "cus_"+a.Handle {
		t.Fatal("created did not project trial, has_card and the customer")
	}
	if len(seats.converted) != 1 || seats.converted[0] != a.UserID.String() {
		t.Fatalf("Seats.Converted once on the first live subscription: %v", seats.converted)
	}
	// The introductory discount runs three months from the trial's end
	// (DECISIONS I-497, I-604).
	if !sub.Intro || sub.IntroUntil == nil || !sub.IntroUntil.Equal(time.Date(2027, 1, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("intro: %v %v", sub.Intro, sub.IntroUntil)
	}
	// Another discount is not the introductory offer.
	must("subscription.updated", subData("sub_life", a, "prod_solo_test", "trialing", map[string]any{"discount_id": "dsc_other"}))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.Intro || sub.IntroUntil != nil {
		t.Fatalf("another discount marked the offer: %v %v", sub.Intro, sub.IntroUntil)
	}
	must("subscription.active", subData("sub_life", a, "prod_solo_test", "active", map[string]any{"trial_start": nil, "trial_end": nil}))
	if userField(t, pool, a, "billing_status") != "active" || len(seats.converted) != 1 {
		t.Fatal("active: active, and no second conversion")
	}
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.TrialEnd != nil {
		t.Fatal("trial_end cleared once Polar has none")
	}
	// Plan change.
	must("subscription.updated", subData("sub_life", a, "prod_plus_test", "active", nil))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.Plan != "plus" || sub.Seats != 2 {
		t.Fatalf("updated to plus: %+v", sub)
	}
	// A pending update is the scheduled downgrade; without one there is
	// none.
	must("subscription.updated", subData("sub_life", a, "prod_plus_test", "active", map[string]any{"pending_update": map[string]any{"product_id": "prod_solo_test", "applies_at": "2026-11-01T00:00:00Z"}}))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.ScheduledPlan == nil || *sub.ScheduledPlan != "solo" {
		t.Fatalf("scheduled_plan: %v", sub.ScheduledPlan)
	}
	must("subscription.updated", subData("sub_life", a, "prod_plus_test", "active", nil))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.ScheduledPlan != nil {
		t.Fatal("scheduled_plan kept after Polar dropped the pending update")
	}
	// Cancellation scheduled.
	must("subscription.canceled", subData("sub_life", a, "prod_plus_test", "active", map[string]any{"cancel_at_period_end": true, "ends_at": "2026-11-01T00:00:00Z"}))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.CancelAt == nil || !sub.CancelAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || sub.NextBilledAt != nil {
		t.Fatalf("cancel_at: %+v, next_billed_at %v", sub.CancelAt, sub.NextBilledAt)
	}
	// Uncanceled: the scheduled cancellation is gone.
	must("subscription.uncanceled", subData("sub_life", a, "prod_plus_test", "active", nil))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.CancelAt != nil {
		t.Fatal("uncancel clears cancel_at")
	}
	// The renewal fails: past_due from now, one payment_failed email; the
	// next update while still past due sends no second one.
	must("subscription.past_due", subData("sub_life", a, "prod_plus_test", "past_due", nil))
	if userField(t, pool, a, "billing_status") != "past_due" || userField(t, pool, a, "past_due_since") == "" {
		t.Fatal("past_due projected with past_due_since")
	}
	must("subscription.updated", subData("sub_life", a, "prod_plus_test", "past_due", nil))
	// Retries exhausted: unpaid counts as canceled.
	must("subscription.updated", subData("sub_life", a, "prod_plus_test", "unpaid", nil))
	sub, _ = billing.GetSubscription(ctx, pool, "sub_life")
	if sub.Status != "canceled" || userField(t, pool, a, "billing_status") != "none" {
		t.Fatalf("unpaid -> canceled, none: %s %s", sub.Status, userField(t, pool, a, "billing_status"))
	}
	if len(stop.calls) != 1 || *stop.calls[0].ProjectID != a.ProjectID || stop.calls[0].Params["reason"] != "billing" || stop.calls[0].Params["snapshot"] != true {
		t.Fatalf("the end stops the running machine with a snapshot: %+v", stop.calls)
	}
	// Revoked after it (Polar sends both): stops nothing more.
	must("subscription.revoked", subData("sub_life", a, "prod_plus_test", "canceled", nil))
	if len(stop.calls) != 1 {
		t.Fatal("ended twice stopped twice")
	}
	kinds := eventKinds(t, pool, a)
	want := []string{"plan_changed", "subscription_cancelled", "payment_failed", "subscription_ended"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("account events %v, want %v", kinds, want)
	}
	if outboxEmails(t, pool, a) != 4 {
		t.Fatalf("%d emails queued, want 4", outboxEmails(t, pool, a))
	}
	if p := accountEmail(t, pool, a, "plan_changed", "from Solo to Plus", "3 October 2026 at 12:00 UTC"); p["from_plan"] != "solo" || p["to_plan"] != "plus" {
		t.Fatalf("plan_changed payload %v", p)
	}
	if p := accountEmail(t, pool, a, "subscription_cancelled", "Plus", "1 November 2026 at 00:00 UTC"); p["plan"] != "plus" || p["ends_at"] != "2026-11-01T00:00:00Z" {
		t.Fatalf("subscription_cancelled payload %v", p)
	}
	if p := accountEmail(t, pool, a, "payment_failed", "Plus"); p["plan"] != "plus" || p["amount_cents"] != float64(5900) {
		t.Fatalf("payment_failed payload %v", p)
	}
	if p := accountEmail(t, pool, a, "subscription_ended", "Plus", "3 October 2026 at 12:00 UTC", "2 November 2026 at 12:00 UTC"); p["plan"] != "plus" || p["retention_until"] != "2026-11-02T12:00:00Z" {
		t.Fatalf("subscription_ended payload %v", p)
	}
	var live *billing.Sub
	if live, err = billing.LiveSubscription(ctx, pool, a.UserID); err != nil || live != nil {
		t.Fatalf("no live subscription after the end: %+v %v", live, err)
	}
}

// A subscription whose product is not one of ours is an error the
// billing_events row keeps; an unknown status too.
func TestWebhookRefusesForeignProducts(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	a := seedAccount(t, pool, "", "none", "", "")
	if err := post(t, w, f, event("subscription.created", subData("sub_f", a, "prod_other", "active", nil))); err == nil || !strings.Contains(err.Error(), "prod_other") {
		t.Fatalf("foreign product: %v", err)
	}
	if err := post(t, w, f, event("subscription.created", subData("sub_g", a, "prod_solo_test", "weird", nil))); err == nil || !strings.Contains(err.Error(), "weird") {
		t.Fatalf("unknown status: %v", err)
	}
	if userField(t, pool, a, "billing_status") != "none" {
		t.Fatal("a foreign subscription changed the account")
	}
}

// order.paid: the payment that ends past_due and lifts a billing
// suspension; the checkout's $0 order on a trial changes nothing.
func TestWebhookOrderPaid(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	ctx := context.Background()

	a := seedAccount(t, pool, "solo", "trial", "", "")
	if err := post(t, w, f, event("order.paid", orderData(a, a.SubID, 0, "subscription_create"))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, a, "billing_status") != "trial" {
		t.Fatal("the trial's $0 order made the account active")
	}
	// An order with no subscription is not repose's.
	if err := post(t, w, f, event("order.paid", orderData(a, "", 500, "purchase"))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, a, "billing_status") != "trial" {
		t.Fatal("a one-off order moved the account")
	}

	b := seedAccount(t, pool, "plus", "past_due", "", "")
	if _, err := pool.Exec(ctx, "update users set past_due_since = now() where id = $1", b.UserID); err != nil {
		t.Fatal(err)
	}
	if err := post(t, w, f, event("order.paid", orderData(b, b.SubID, 5900, "subscription_cycle"))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, b, "billing_status") != "active" || userField(t, pool, b, "past_due_since") != "" {
		t.Fatal("paid -> active with past_due_since cleared")
	}
	sub, _ := billing.GetSubscription(ctx, pool, b.SubID)
	if sub.Status != "active" {
		t.Fatal("subscription row active")
	}

	// A billing suspension is lifted by a payment; an operator's is not.
	c := seedAccount(t, pool, "solo", "past_due", "", "")
	if _, err := pool.Exec(ctx, "update users set billing_status = 'suspended', suspended_at = now(), suspended_reason = 'billing' where id = $1", c.UserID); err != nil {
		t.Fatal(err)
	}
	if err := post(t, w, f, event("order.paid", orderData(c, c.SubID, 2900, "subscription_cycle"))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, c, "billing_status") != "active" || userField(t, pool, c, "suspended_at") != "" {
		t.Fatal("a billing suspension is cleared by a payment")
	}
	d := seedAccount(t, pool, "solo", "active", "", "")
	if _, err := pool.Exec(ctx, "update users set billing_status = 'suspended', suspended_at = now(), suspended_reason = 'abuse' where id = $1", d.UserID); err != nil {
		t.Fatal(err)
	}
	if err := post(t, w, f, event("order.paid", orderData(d, d.SubID, 2900, "subscription_cycle"))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, d, "billing_status") != "suspended" {
		t.Fatal("an operator's suspension survives a payment")
	}
	// Likewise a subscription event never un-suspends.
	if err := post(t, w, f, event("subscription.updated", subData(d.SubID, d, "prod_solo_test", "active", nil))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, d, "billing_status") != "suspended" {
		t.Fatal("subscription.updated un-suspended the account")
	}
}

// A subscription found by customer id alone (no metadata, no external id)
// still lands on the right account.
func TestWebhookResolvesByCustomer(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	w := newHooks(t, pool, f, nil, nil)
	a := seedAccount(t, pool, "", "none", "", "")
	if _, err := pool.Exec(context.Background(), "update users set billing_customer_id = $2 where id = $1", a.UserID, "cus_"+a.Handle); err != nil {
		t.Fatal(err)
	}
	d := subData("sub_byc", a, "prod_plus_test", "active", nil)
	delete(d, "metadata")
	delete(d, "customer")
	if err := post(t, w, f, event("subscription.active", d)); err != nil {
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
