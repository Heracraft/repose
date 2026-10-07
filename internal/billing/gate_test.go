package billing_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

func newGate(pool *db.Pool, f *fakePaddle) *billing.Gate {
	g := billing.NewGate(pool, testConfig(f), nop(), quiet())
	g.Now = at(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	return g
}

func user(t *testing.T, pool *db.Pool, a account) *store.User {
	t.Helper()
	u, err := store.GetUser(context.Background(), pool, a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func refusal(t *testing.T, err error) *billing.Refusal {
	t.Helper()
	var r *billing.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("want a refusal, got %v", err)
	}
	return r
}

// Every reason of api.md's payment_required table, and the message that
// is the whole sentence.
func TestGateEveryReason(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePaddle()
	defer f.Close()
	g := newGate(pool, f)
	ctx := context.Background()
	const url = "https://repose.herakraft.co/billing"

	// subscription_required: no plan; with a waitlist place in detail.
	a := seedAccount(t, pool, "", "none", "", "")
	r := refusal(t, g.Check(ctx, user(t, pool, a), billing.Request{Class: "large"}))
	if r.Reason != "subscription_required" || r.Message != "Choose a plan at "+url+" first." || r.Detail["waitlist"] != nil {
		t.Fatalf("no plan: %+v", r)
	}
	if _, err := pool.Exec(ctx, "insert into waitlist (user_id, joined_at) values ($1, now())", a.UserID); err != nil {
		t.Fatal(err)
	}
	r = refusal(t, g.Check(ctx, user(t, pool, a), billing.Request{Class: "large"}))
	wl, _ := r.Detail["waitlist"].(map[string]any)
	if wl == nil || wl["position"] != 1 || !strings.Contains(r.Message, "number 1 on the waitlist") {
		t.Fatalf("waitlisted detail: %+v", r)
	}

	// plan_limit: one large running on Solo; the message names it.
	b := seedAccount(t, pool, "solo", "active", "large", "running")
	r = refusal(t, g.Check(ctx, user(t, pool, b), billing.Request{Class: "small"}))
	if r.Reason != "plan_limit" || r.Message != "Your Solo plan runs 8 GB at once and "+b.Slug+" is using it. Stop it, or upgrade at "+url+"." {
		t.Fatalf("plan_limit: %+v", r)
	}
	if r.Detail["limit_gb"] != 8 || r.Detail["used_gb"] != 8 || r.Detail["plan"] != "solo" || len(r.Detail["projects"].([]string)) != 1 {
		t.Fatalf("plan_limit detail: %+v", r.Detail)
	}
	// Restarting the running project itself is not counted twice.
	if err := g.Check(ctx, user(t, pool, b), billing.Request{Class: "large", Project: b.ProjectID}); err != nil {
		t.Fatalf("restart of the running machine: %v", err)
	}
	// Stopped, it fits again; xl never fits Solo.
	if _, err := pool.Exec(ctx, "update projects set state = 'stopped' where id = $1", b.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(ctx, user(t, pool, b), billing.Request{Class: "large", Project: b.ProjectID}); err != nil {
		t.Fatalf("start after stop: %v", err)
	}
	r = refusal(t, g.Check(ctx, user(t, pool, b), billing.Request{Class: "xl"}))
	if r.Message != "Your Solo plan runs 8 GB at once and an xl machine needs 16 GB. Upgrade to Plus at "+url+"." {
		t.Fatalf("xl on solo: %s", r.Message)
	}
	// Two machines: "x and y are using it. Stop one".
	c := seedAccount(t, pool, "plus", "active", "large", "running")
	addProject(t, pool, c, "api", "large", "starting", 40<<30)
	r = refusal(t, g.Check(ctx, user(t, pool, c), billing.Request{Class: "small"}))
	if !strings.Contains(r.Message, "api and "+c.Slug+" are using it. Stop one, or upgrade") || r.Detail["used_gb"] != 16 {
		t.Fatalf("two machines: %+v", r)
	}
	// Pro runs 32 GB: three large and a fourth fit, an xl beside them does not.
	pr := seedAccount(t, pool, "pro", "active", "large", "running")
	addProject(t, pool, pr, "api", "large", "running", 40<<30)
	addProject(t, pool, pr, "web", "large", "running", 40<<30)
	if err := g.Check(ctx, user(t, pool, pr), billing.Request{Class: "large"}); err != nil {
		t.Fatalf("a fourth large on Pro: %v", err)
	}
	r = refusal(t, g.Check(ctx, user(t, pool, pr), billing.Request{Class: "xl"}))
	if r.Reason != "plan_limit" || !strings.HasPrefix(r.Message, "Your Pro plan runs 32 GB at once and ") || r.Detail["limit_gb"] != 32 || r.Detail["used_gb"] != 24 {
		t.Fatalf("xl beside three large on Pro: %+v", r)
	}

	// disk_limit: 100 GB on Solo, 70 allocated, 40 more asked.
	d := seedAccount(t, pool, "solo", "active", "small", "stopped")
	addProject(t, pool, d, "big", "small", "stopped", 30<<30)
	r = refusal(t, g.Check(ctx, user(t, pool, d), billing.Request{AddDiskBytes: 40 << 30}))
	if r.Reason != "disk_limit" || r.Message != "Your Solo plan allocates up to 100 GB of disk and your projects use 70 GB; this needs 40 GB more. Destroy a project, or upgrade at "+url+"." {
		t.Fatalf("disk_limit: %+v", r)
	}
	if err := g.Check(ctx, user(t, pool, d), billing.Request{AddDiskBytes: 30 << 30}); err != nil {
		t.Fatalf("30 more fits: %v", err)
	}
	// Growing an existing volume counts the growth, not the whole volume.
	if err := g.Check(ctx, user(t, pool, d), billing.Request{AddDiskBytes: 30 << 30, Project: d.ProjectID}); err != nil {
		t.Fatalf("grow: %v", err)
	}

	// egress_limit: 1 TB on Solo this period.
	e := seedAccount(t, pool, "solo", "active", "large", "stopped")
	usageHour(t, pool, e.ProjectID, e.Period.Start.Add(time.Hour), "large", 3600, 1000<<30, e.Period)
	r = refusal(t, g.Check(ctx, user(t, pool, e), billing.Request{Class: "large"}))
	if r.Reason != "egress_limit" || !strings.Contains(r.Message, "stopped until 1 November") || !strings.Contains(r.Message, "passed 1000 GB, four times the Solo plan's 250 GB") {
		t.Fatalf("egress_limit: %+v", r)
	}
	if r.Detail["until"] != e.Period.End || r.Detail["used_gb"] != int64(1000) {
		t.Fatalf("egress detail: %+v", r.Detail)
	}
	// Egress from a past period does not count.
	usageHour(t, pool, e.ProjectID, e.Period.Start.Add(time.Hour), "large", 3600, 10<<30, e.Period)
	usageHour(t, pool, e.ProjectID, e.Period.Start.Add(-time.Hour), "large", 3600, 5000<<30, billing.Period{Start: e.Period.Start.AddDate(0, -1, 0), End: e.Period.Start})
	if err := g.Check(ctx, user(t, pool, e), billing.Request{Class: "large"}); err != nil {
		t.Fatalf("last period's egress counted: %v", err)
	}

	// past_due and suspended.
	p := seedAccount(t, pool, "solo", "past_due", "", "")
	r = refusal(t, g.Check(ctx, user(t, pool, p), billing.Request{Class: "small"}))
	if r.Reason != "past_due" || r.Message != "Your last payment failed. Update your card at "+url+" to start machines again." {
		t.Fatalf("past_due: %+v", r)
	}
	if _, err := pool.Exec(ctx, "update users set billing_status = 'suspended', suspended_at = now(), suspended_reason = 'billing' where id = $1", p.UserID); err != nil {
		t.Fatal(err)
	}
	r = refusal(t, g.Check(ctx, user(t, pool, p), billing.Request{Class: "small"}))
	if r.Reason != "suspended" {
		t.Fatalf("suspended: %+v", r)
	}

	// Exempt passes everything (I-16); BILLING_ENFORCE=false too.
	x := seedAccount(t, pool, "", "exempt", "xl", "running")
	if err := g.Check(ctx, user(t, pool, x), billing.Request{Class: "xl", AddDiskBytes: 5 << 40}); err != nil {
		t.Fatalf("exempt: %v", err)
	}
	g.Enforce = false
	if err := g.Check(ctx, user(t, pool, a), billing.Request{Class: "xl"}); err != nil {
		t.Fatalf("enforce off: %v", err)
	}
	g.Enforce = true
	// Paddle not configured: every non-exempt user is subscription_required,
	// even with a row.
	g.Enabled = false
	r = refusal(t, g.Check(ctx, user(t, pool, c), billing.Request{}))
	if r.Reason != "subscription_required" {
		t.Fatalf("disabled deploy: %+v", r)
	}
	// A request for nothing (no class, no disk) on a good account passes.
	g.Enabled = true
	if err := g.Check(ctx, user(t, pool, c), billing.Request{}); err != nil {
		t.Fatalf("nothing asked: %v", err)
	}
}

func TestLimitsFor(t *testing.T) {
	u := &store.User{ProjectLimit: 3, XLLimit: 0, BillingStatus: "none"}
	l := billing.LimitsFor(u, nil)
	// users.project_limit below the cap does not lower it (I-569).
	if l.Projects != billing.ProjectCap || l.XL != 0 || l.MemoryGB != 8 || l.Plan != nil {
		t.Fatalf("no plan: %+v", l)
	}
	l = billing.LimitsFor(u, &billing.Sub{Plan: "plus", Status: "active"})
	if l.Projects != billing.ProjectCap || l.XL != 1 || l.MemoryGB != 16 || l.DiskGB != 250 || l.EgressGB != 500 || l.Plan.ID != "plus" {
		t.Fatalf("plus: %+v", l)
	}
	l = billing.LimitsFor(u, &billing.Sub{Plan: "pro", Status: "active"})
	if l.Projects != billing.ProjectCap || l.XL != 1 || l.MemoryGB != 32 || l.DiskGB != 500 || l.EgressGB != 1000 || l.Plan.ID != "pro" {
		t.Fatalf("pro: %+v", l)
	}
	l = billing.LimitsFor(u, &billing.Sub{Plan: "solo", Status: "trialing"})
	if l.Projects != billing.ProjectCap || l.XL != 0 {
		t.Fatalf("solo: %+v", l)
	}
	// Above the cap, users.project_limit raises it, with a plan or without.
	high := &store.User{ProjectLimit: billing.ProjectCap + 50, BillingStatus: "exempt"}
	if l := billing.LimitsFor(high, nil); l.Projects != billing.ProjectCap+50 {
		t.Fatalf("raised, exempt: %+v", l)
	}
	if l := billing.LimitsFor(high, &billing.Sub{Plan: "solo", Status: "active"}); l.Projects != billing.ProjectCap+50 {
		t.Fatalf("raised, solo: %+v", l)
	}
	l = billing.LimitsFor(u, &billing.Sub{Plan: "plus", Status: "canceled"})
	if l.Plan != nil {
		t.Fatal("a canceled subscription grants nothing")
	}
	u.BillingStatus = "exempt"
	l = billing.LimitsFor(u, nil)
	if l.XL != 1 || l.MemoryGB != 32 {
		t.Fatalf("exempt: %+v", l)
	}
	j := l.JSON()
	for _, k := range []string{"projects", "xl", "memory_gb", "disk_gb", "egress_gb"} {
		if _, ok := j[k]; !ok {
			t.Errorf("limits JSON lacks %s", k)
		}
	}
	_ = uuid.Nil
}
