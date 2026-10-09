package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db/testdb"
)

// The overage line: egress over the allowance within three hours of the
// period's end is one egress_overage event, to the GB; a retry sends
// none; a period under the allowance sends none but is marked; a failure
// leaves the row unsent and the next run sends it under the same external
// id, brought up to the period's egress so far.
func TestOverageChargeOnce(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	p := billing.NewPolar(cfg, quiet())
	stop := &stopRecorder{}
	o := billing.NewOverage(pool, p, cfg, stop, nop(), quiet())
	ctx := context.Background()

	// Solo, 300 GB this period (50 over), the period ends in two hours.
	a := seedAccount(t, pool, "solo", "active", "large", "stopped")
	for h := 0; h < 30; h++ {
		usageHour(t, pool, a.ProjectID, a.Period.Start.Add(time.Duration(h)*time.Hour), "large", 3600, 10<<30, a.Period)
	}
	o.Now = at(a.Period.End.Add(-2 * time.Hour))
	charges, stopped, err := o.Run(ctx)
	if err != nil || len(stopped) != 0 {
		t.Fatalf("run: %v %v", err, stopped)
	}
	ref := billing.OverageExternalID(a.SubID, a.Period.Start)
	if len(charges) != 1 || !charges[0].Sent || charges[0].Cents != 250 || charges[0].EgressGB != 50 || charges[0].Ref != ref {
		t.Fatalf("charges: %+v", charges)
	}
	ev := f.Events()[ref]
	md, _ := ev["metadata"].(map[string]any)
	if ev["name"] != billing.OverageEvent || ev["external_customer_id"] != a.UserID.String() || md["gb"] != float64(50) {
		t.Fatalf("event: %v", ev)
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from overage_charges where subscription_id = $1 and cents = 250 and sent_ref = $2", a.SubID, ref).Scan(&n); err != nil || n != 1 {
		t.Fatalf("overage_charges rows: %d %v", n, err)
	}
	sub, _ := billing.GetSubscription(ctx, pool, a.SubID)
	if sub.OverageChargedFor == nil || !sub.OverageChargedFor.Equal(a.Period.Start) {
		t.Fatalf("overage_charged_for: %v", sub.OverageChargedFor)
	}
	// A retry sends nothing.
	charges, _, err = o.Run(ctx)
	if err != nil || len(charges) != 0 || f.Count("POST /events/ingest") != 1 {
		t.Fatalf("retry: %+v %v (%d sends)", charges, err, f.Count("POST /events/ingest"))
	}
	// Even the direct call (overage-now, account deletion) finds the line
	// and does not send it again.
	c, err := o.ChargePeriod(ctx, sub)
	if err != nil || c.Sent || c.Ref != ref || f.Count("POST /events/ingest") != 1 {
		t.Fatalf("ChargePeriod on a charged period: %+v %v", c, err)
	}

	// Under the allowance: marked, nothing sent, no row.
	b := seedAccount(t, pool, "plus", "active", "large", "stopped")
	usageHour(t, pool, b.ProjectID, b.Period.Start, "large", 3600, 100<<30, b.Period)
	charges, _, err = o.Run(ctx)
	if err != nil || len(charges) != 1 || charges[0].Cents != 0 || charges[0].Sent {
		t.Fatalf("under allowance: %+v %v", charges, err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from overage_charges where subscription_id = $1", b.SubID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a zero line was recorded: %d", n)
	}
	sub, _ = billing.GetSubscription(ctx, pool, b.SubID)
	if sub.OverageChargedFor == nil {
		t.Fatal("period not marked")
	}

	// Not yet within the window: nothing.
	c2 := seedAccount(t, pool, "solo", "active", "large", "stopped")
	usageHour(t, pool, c2.ProjectID, c2.Period.Start, "large", 3600, 400<<30, c2.Period)
	o.Now = at(c2.Period.End.Add(-5 * time.Hour))
	if charges, _, err = o.Run(ctx); err != nil || len(charges) != 0 {
		t.Fatalf("outside the window: %+v %v", charges, err)
	}

	// Polar refuses: the row stays unsent, the period is not marked, and
	// the next run sends it, with the egress that came since.
	o.Now = at(c2.Period.End.Add(-2 * time.Hour))
	f.Fail["POST /events/ingest"] = 3
	f.FailCode = 500
	p.Sleep = func(time.Duration) {}
	charges, _, err = o.Run(ctx)
	if err != nil || len(charges) != 0 {
		t.Fatalf("a failed send is logged, not returned: %+v %v", charges, err)
	}
	var sent *string
	if err := pool.QueryRow(ctx, "select sent_ref from overage_charges where subscription_id = $1", c2.SubID).Scan(&sent); err != nil || sent != nil {
		t.Fatalf("row after failure: %v %v", sent, err)
	}
	sub, _ = billing.GetSubscription(ctx, pool, c2.SubID)
	if sub.OverageChargedFor != nil {
		t.Fatal("a failed period was marked charged")
	}
	usageHour(t, pool, c2.ProjectID, c2.Period.Start.Add(time.Hour), "large", 3600, 10<<30, c2.Period)
	charges, _, err = o.Run(ctx)
	ref2 := billing.OverageExternalID(c2.SubID, c2.Period.Start)
	if err != nil || len(charges) != 1 || !charges[0].Sent || charges[0].EgressGB != 160 || charges[0].Cents != 800 || charges[0].Ref != ref2 {
		t.Fatalf("the resend: %+v %v", charges, err)
	}
	if md := f.Events()[ref2]["metadata"].(map[string]any); md["gb"] != float64(160) {
		t.Fatalf("resent event: %v", md)
	}
	sub, _ = billing.GetSubscription(ctx, pool, c2.SubID)
	if sub.OverageChargedFor == nil {
		t.Fatal("the period is marked once Polar has the line")
	}
}

// The hard stop: four times the allowance stops the running machines once
// per period with an egress_stopped email; the gate then refuses.
func TestEgressHardStop(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	stop := &stopRecorder{}
	o := billing.NewOverage(pool, nil, cfg, stop, nop(), quiet())
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	other := addProject(t, pool, a, "sleeping", "small", "stopped", 20<<30)
	o.Now = at(a.Period.Start.Add(10 * 24 * time.Hour))

	usageHour(t, pool, a.ProjectID, a.Period.Start.Add(time.Hour), "large", 3600, 999<<30, a.Period)
	_, stopped, err := o.Run(ctx)
	if err != nil || len(stopped) != 0 || len(stop.calls) != 0 {
		t.Fatalf("under the ceiling: %v %v", stopped, err)
	}
	usageHour(t, pool, a.ProjectID, a.Period.Start.Add(2*time.Hour), "large", 3600, 1<<30, a.Period)
	_, stopped, err = o.Run(ctx)
	if err != nil || len(stopped) != 1 || stopped[0] != a.UserID {
		t.Fatalf("at the ceiling: %v %v", stopped, err)
	}
	if len(stop.calls) != 1 || *stop.calls[0].ProjectID != a.ProjectID || stop.calls[0].Params["reason"] != "billing" || stop.calls[0].Params["snapshot"] != true || stop.kicks != 1 {
		t.Fatalf("stop calls: %+v", stop.calls)
	}
	if k := eventKinds(t, pool, a); len(k) != 1 || k[0] != "egress_stopped" {
		t.Fatalf("events %v", k)
	}
	if p := accountEmail(t, pool, a, "egress_stopped", "Solo", "1000 GB", "250 GB", "1 November 2026 at 00:00 UTC"); p["plan"] != "solo" || p["limit_gb"] != float64(250) || p["egress_gb"] != float64(1000) {
		t.Fatalf("payload %v", p)
	}
	if outboxEmails(t, pool, a) != 1 {
		t.Fatalf("%d emails queued, want 1", outboxEmails(t, pool, a))
	}
	// Once per period: a second run stops nothing and sends no second email.
	if _, err := pool.Exec(ctx, "update projects set state = 'running' where id = $1", a.ProjectID); err != nil {
		t.Fatal(err)
	}
	_, stopped, err = o.Run(ctx)
	if err != nil || len(stopped) != 0 || len(stop.calls) != 1 || len(eventKinds(t, pool, a)) != 1 {
		t.Fatalf("second run: %v %v %d", stopped, err, len(stop.calls))
	}
	// The gate refuses until period_end.
	g := billing.NewGate(pool, cfg, nop(), quiet())
	g.Now = o.Now
	r := refusal(t, g.Check(ctx, user(t, pool, a), billing.Request{Class: "small", Project: other}))
	if r.Reason != "egress_limit" {
		t.Fatalf("gate after the stop: %+v", r)
	}
	// BILLING_ENFORCE=false: the ceiling is logged, nothing stops.
	b := seedAccount(t, pool, "plus", "active", "large", "running")
	usageHour(t, pool, b.ProjectID, b.Period.Start.Add(time.Hour), "large", 3600, 2000<<30, b.Period)
	o.Enforce = false
	_, stopped, err = o.Run(ctx)
	if err != nil || len(stopped) != 0 || len(stop.calls) != 1 || len(eventKinds(t, pool, b)) != 0 {
		t.Fatalf("enforce off: %v %v", stopped, err)
	}
}

// Projects that come to hold more than the plan's disk get one
// disk_over_plan email a period, and nothing is stopped (DECISIONS I-585).
func TestDiskOverPlanEmail(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	stop := &stopRecorder{}
	o := billing.NewOverage(pool, nil, testConfig(f), stop, nop(), quiet())
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	o.Now = at(a.Period.Start.Add(10 * 24 * time.Hour))
	held(t, pool, a.ProjectID, 100<<30)
	if told, err := o.DiskOverPlan(ctx); err != nil || len(told) != 0 {
		t.Fatalf("at the plan's disk: %v %v", told, err)
	}
	other := addProject(t, pool, a, "other", "large", "stopped", 40<<30)
	held(t, pool, other, 12<<30+400<<20)
	if _, _, err := o.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if k := eventKinds(t, pool, a); len(k) != 1 || k[0] != "disk_over_plan" {
		t.Fatalf("events %v", k)
	}
	if p := accountEmail(t, pool, a, "disk_over_plan", "112.4 GB", "Solo has 100 GB of disk", "keep running and starting"); p["plan"] != "solo" || p["held_gb"] != 112.4 || p["limit_gb"] != float64(100) {
		t.Fatalf("payload %v", p)
	}
	if len(stop.calls) != 0 {
		t.Fatalf("a machine was stopped for the disk: %+v", stop.calls)
	}
	// Once a period.
	if told, err := o.DiskOverPlan(ctx); err != nil || len(told) != 0 || len(eventKinds(t, pool, a)) != 1 {
		t.Fatalf("second run: %v %v", told, err)
	}
}
