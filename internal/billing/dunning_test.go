package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db/testdb"
)

// PRICING.md "Failed payments": day 0 is the webhook's email; day 2 a
// second, once; day 3 the stop with a snapshot, billing_stopped, suspended;
// a payment returns the account to active with the machine left stopped.
func TestDunningDays(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	ev := events.New(pool, nop(), quiet())
	stop := &stopRecorder{}
	d := billing.NewDunning(pool, stop, ev, cfg, nop(), quiet())
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	t0 := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

	// Day 0 from the webhook.
	w := newHooks(t, pool, f, nil, nil)
	w.Now = at(t0)
	if err := post(t, w, f, event("subscription.past_due", subData(a.SubID, a, "prod_solo_test", "past_due", nil))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, a, "billing_status") != "past_due" || len(eventKinds(t, pool, a)) != 1 {
		t.Fatal("day 0")
	}
	// Day 1: nothing.
	d.Now = at(t0.Add(24 * time.Hour))
	res, err := d.Run(ctx)
	if err != nil || len(res.SecondNotices) != 0 || len(res.Suspended) != 0 {
		t.Fatalf("day 1: %+v %v", res, err)
	}
	// Day 2: the second email, once however often the tick runs.
	d.Now = at(t0.Add(49 * time.Hour))
	for i := 0; i < 3; i++ {
		res, err = d.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && (len(res.SecondNotices) != 1 || res.SecondNotices[0] != a.UserID) {
			t.Fatalf("day 2 first run: %+v", res)
		}
		if i > 0 && len(res.SecondNotices) != 0 {
			t.Fatalf("day 2 run %d sent again", i)
		}
	}
	if k := eventKinds(t, pool, a); len(k) != 2 || k[1] != "payment_failed" {
		t.Fatalf("events %v", k)
	}
	if p := accountEmail(t, pool, a, "payment_failed", "Solo", "$29.00", "https://repose.herakraft.co/billing"); p["plan"] != "solo" || p["amount_cents"] != float64(2900) {
		t.Fatalf("day 2 payload %v", p)
	}
	if outboxEmails(t, pool, a) != 2 {
		t.Fatalf("%d emails queued, want one per payment_failed", outboxEmails(t, pool, a))
	}
	if len(stop.calls) != 0 || userField(t, pool, a, "billing_status") != "past_due" {
		t.Fatal("day 2 stopped something")
	}
	// Day 3 plus an hour: stop with snapshot, billing_stopped, suspended.
	d.Now = at(t0.Add(73 * time.Hour))
	res, err = d.Run(ctx)
	if err != nil || len(res.Suspended) != 1 || len(res.Suspended[0].Projects) != 1 || res.Suspended[0].Projects[0] != a.ProjectID {
		t.Fatalf("day 3: %+v %v", res, err)
	}
	if len(stop.calls) != 1 || stop.calls[0].Params["snapshot"] != true || stop.calls[0].Params["reason"] != "billing" || stop.kicks != 1 {
		t.Fatalf("stop: %+v", stop.calls)
	}
	if userField(t, pool, a, "billing_status") != "suspended" || userField(t, pool, a, "suspended_reason") != "billing" {
		t.Fatal("not suspended for billing")
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = 'billing_stopped'", a.ProjectID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("billing_stopped events: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from audit_log where action = 'billing_suspend' and target = $1", a.Handle).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows: %d %v", n, err)
	}
	// Idempotent: nothing more.
	res, err = d.Run(ctx)
	if err != nil || len(res.Suspended) != 0 || len(stop.calls) != 1 {
		t.Fatalf("second day-3 run: %+v %v", res, err)
	}
	// A payment: active, unsuspended, the machine stays as it is.
	if _, err := pool.Exec(ctx, "update projects set state = 'stopped' where id = $1", a.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := post(t, w, f, event("order.paid", orderData(a, a.SubID, 2900, "subscription_cycle"))); err != nil {
		t.Fatal(err)
	}
	if userField(t, pool, a, "billing_status") != "active" || userField(t, pool, a, "suspended_at") != "" || userField(t, pool, a, "past_due_since") != "" {
		t.Fatal("payment did not reactivate")
	}
	var state string
	if err := pool.QueryRow(ctx, "select state from projects where id = $1", a.ProjectID).Scan(&state); err != nil || state != "stopped" {
		t.Fatalf("the machine started itself: %s", state)
	}
}

// BILLING_ENFORCE=false: day 3 stops nothing and suspends nobody, but the
// day-2 email still goes out.
func TestDunningEnforceFalse(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	cfg.Enforce = false
	stop := &stopRecorder{}
	d := billing.NewDunning(pool, stop, events.New(pool, nop(), quiet()), cfg, nop(), quiet())
	a := seedAccount(t, pool, "solo", "past_due", "large", "running")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "update users set past_due_since = now() - interval '4 days' where id = $1", a.UserID); err != nil {
		t.Fatal(err)
	}
	res, err := d.Run(ctx)
	if err != nil || len(stop.calls) != 0 || userField(t, pool, a, "billing_status") != "past_due" {
		t.Fatalf("enforce off stopped something: %+v %v", res, err)
	}
	if len(res.Suspended) != 1 || len(res.Suspended[0].Projects) != 0 {
		t.Fatalf("the account is reported, with no projects stopped: %+v", res)
	}
}

// trial_ending goes out once when a trial has 48 hours left.
func TestTrialEnding(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	d := billing.NewDunning(pool, &stopRecorder{}, nil, testConfig(f), nop(), quiet())
	ctx := context.Background()
	a := seedAccount(t, pool, "plus", "trial", "", "") // trial_end = period start + 7 days
	trialEnd := a.Period.Start.Add(7 * 24 * time.Hour)

	d.Now = at(trialEnd.Add(-3 * 24 * time.Hour))
	res, err := d.Run(ctx)
	if err != nil || len(res.TrialEnding) != 0 {
		t.Fatalf("three days out: %+v %v", res, err)
	}
	d.Now = at(trialEnd.Add(-47 * time.Hour))
	for i := 0; i < 3; i++ {
		res, err = d.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if (i == 0) != (len(res.TrialEnding) == 1) {
			t.Fatalf("run %d: %+v", i, res.TrialEnding)
		}
	}
	if k := eventKinds(t, pool, a); len(k) != 1 || k[0] != "trial_ending" {
		t.Fatalf("events %v", k)
	}
	if p := accountEmail(t, pool, a, "trial_ending", "Plus", "$59.00", "8 October 2026 at 00:00 UTC"); p["plan"] != "plus" || p["amount_cents"] != float64(5900) || p["charge_at"] != "2026-10-08T00:00:00Z" {
		t.Fatalf("payload %v", p)
	}
	if outboxEmails(t, pool, a) != 1 {
		t.Fatal("no email queued")
	}
	// After the trial ended: nothing (and still once).
	d.Now = at(trialEnd.Add(time.Hour))
	res, err = d.Run(ctx)
	if err != nil || len(res.TrialEnding) != 0 {
		t.Fatalf("after the end: %+v %v", res, err)
	}
}
