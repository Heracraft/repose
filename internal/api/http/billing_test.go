package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/obs"
)

func errDetail(r resp, key string) string {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["detail"].(map[string]any)
	s, _ := d[key].(string)
	return s
}

func errMessage(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	s, _ := e["message"].(string)
	return s
}

// The compute gate at every call site (api.md's payment_required table):
// start, create, restore, fork, snapshot restore, class change and volume
// growth; each refusal carries the reason and the whole sentence, and an
// exempt account passes everything (I-16).
func TestBillingGateBlocksCompute(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tok := e.signIn(t, "sub-gate", "gate-dev")
	e.subscribe(t, "sub-gate", "solo")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "gated", "class": "large"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	// Running large on Solo: a second machine is plan_limit, naming it.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "second", "class": "small"})
	if r.status != 402 || errDetail(r, "reason") != "plan_limit" || errMessage(r) != "Your Solo plan runs 8 GB at once and gated is using it. Stop it, or upgrade at https://repose.herakraft.co/billing." {
		t.Fatalf("plan_limit on create: %d %s", r.status, r.raw)
	}
	stop := e.do(t, tok, "POST", "/projects/"+pid+"/stop", map[string]any{"snapshot": true})
	if stop.status != 202 && stop.status != 200 {
		t.Fatalf("stop: %d %s", stop.status, stop.raw)
	}
	e.waitOp(t, stop)
	// Class change to xl on Solo: plan_limit (xl needs Plus).
	r = e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"class": "xl"})
	if r.status != 402 || errDetail(r, "reason") != "plan_limit" || !strings.Contains(errMessage(r), "an xl machine needs 16 GB. Upgrade to Plus") {
		t.Fatalf("xl on solo: %d %s", r.status, r.raw)
	}
	// Growing the volume past 100 GB: disk_limit.
	r = e.do(t, tok, "POST", "/projects/"+pid+"/resize", map[string]any{"volume_bytes": int64(200) << 30})
	if r.status != 402 || errDetail(r, "reason") != "disk_limit" || !strings.Contains(errMessage(r), "allocates up to 100 GB of disk and your projects use 40 GB; this needs 160 GB more") {
		t.Fatalf("disk_limit on resize: %d %s", r.status, r.raw)
	}
	// A second project whose volume would pass the plan: disk_limit on create.
	if _, err := e.h.Pool.Exec(ctx, "update projects set volume_bytes = $2 where id = $1", pid, int64(90)<<30); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "fat", "class": "small"})
	if r.status != 402 || errDetail(r, "reason") != "disk_limit" {
		t.Fatalf("disk_limit on create: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set volume_bytes = $2 where id = $1", pid, int64(40)<<30); err != nil {
		t.Fatal(err)
	}
	// Egress past four times the allowance: egress_limit until period_end.
	var periodStart, periodEnd time.Time
	if err := e.h.Pool.QueryRow(ctx, "select period_start, period_end from subscriptions where id = 'sub_sub-gate'").Scan(&periodStart, &periodEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, `insert into usage_hours (project_id, hour, class, running_seconds, gb_alloc, egress_bytes, cost_cents, period_start, period_end, price_version)
		values ($1, $2, 'large', 3600, 40, $3, 0, $2, $4, 'plan-v1')`, pid, periodStart, int64(1000)<<30, periodEnd); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	if r.status != 402 || errDetail(r, "reason") != "egress_limit" || !strings.Contains(errMessage(r), "four times the Solo plan's 250 GB allowance") {
		t.Fatalf("egress_limit: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "delete from usage_hours where project_id = $1", pid); err != nil {
		t.Fatal(err)
	}
	// past_due, suspended, and no subscription at all.
	for _, c := range []struct {
		name, update, reason, message string
	}{
		{"past due", "billing_status = 'past_due'", "past_due", "Your last payment failed. Update your card at https://repose.herakraft.co/billing to start machines again."},
		{"suspended", "billing_status = 'suspended', suspended_at = now(), suspended_reason = 'billing'", "suspended", "Your account is suspended. Pay at https://repose.herakraft.co/billing to lift it, or email support."},
	} {
		if _, err := e.h.Pool.Exec(ctx, "update users set "+c.update+" where logto_sub = 'sub-gate'"); err != nil {
			t.Fatal(err)
		}
		r := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
		if c.reason == "suspended" {
			// A suspended account is forbidden everywhere but /me, /billing
			// and the portal (api.md).
			if r.status != http.StatusForbidden {
				t.Errorf("%s on start: %d %s", c.name, r.status, r.raw)
			}
			if r := e.do(t, tok, "GET", "/me", nil); r.status != 200 {
				t.Errorf("suspended GET /me: %d", r.status)
			}
			if r := e.do(t, tok, "GET", "/billing", nil); r.status != 503 || errCode(r) != "billing_disabled" {
				t.Errorf("suspended GET /billing reaches the route: %d %s", r.status, r.raw)
			}
			if r := e.do(t, tok, "POST", "/billing/portal", nil); r.status != 503 {
				t.Errorf("suspended POST /billing/portal reaches the route: %d %s", r.status, r.raw)
			}
			if r := e.do(t, tok, "GET", "/projects", nil); r.status != 403 {
				t.Errorf("suspended GET /projects: %d", r.status)
			}
			continue
		}
		if r.status != http.StatusPaymentRequired || errCode(r) != "payment_required" || errDetail(r, "reason") != c.reason || errMessage(r) != c.message {
			t.Errorf("%s on start: %d %s", c.name, r.status, r.raw)
		}
	}
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'active', suspended_at = null, suspended_reason = null where logto_sub = 'sub-gate'"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "sub-gate", "")
	for _, call := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/projects/" + pid + "/start", nil},
		{"POST", "/projects/restore", map[string]any{"slug": "gated"}},
		{"POST", "/projects/" + pid + "/resize", map[string]any{"volume_bytes": int64(60) << 30}},
	} {
		r := e.do(t, tok, call.method, call.path, call.body)
		if r.status != 402 || errDetail(r, "reason") != "subscription_required" {
			t.Errorf("%s %s without a plan: %d %s", call.method, call.path, r.status, r.raw)
		}
	}
	// An exempt account passes every check (DECISIONS I-16).
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'exempt', xl_limit = 1 where logto_sub = 'sub-gate'"); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"class": "xl"}); r.status != 200 {
		t.Fatalf("exempt class change: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil); r.status != 202 && r.status != 200 {
		t.Fatalf("exempt start: %d %s", r.status, r.raw)
	}
	if !strings.Contains(e.logs.String(), obs.EventBillingGateRefused) {
		t.Error("refusals were not logged")
	}
}

// The project cap is the account's, the same on every plan, and counts
// stopped projects (I-569): a Solo account with 100 stopped projects is
// refused a create with 400 project_limit, an upgrade does not raise it,
// and a destroy frees a slot.
func TestProjectCap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tok := e.signIn(t, "sub-count", "count-dev")
	e.subscribe(t, "sub-count", "solo")
	var uid string
	if err := e.h.Pool.QueryRow(ctx, "select id from users where logto_sub = 'sub-count'").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	// 1 MiB volumes keep the plan's disk out of it.
	for i := 0; i < billing.ProjectCap; i++ {
		if _, err := e.h.Pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values (gen_random_uuid(), $1, $2, $2, 'small', 'stopped', 1048576)", uid, fmt.Sprintf("p%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	want := fmt.Sprintf("you have %d of the %d projects an account can have, running or stopped; destroy one first", billing.ProjectCap, billing.ProjectCap)
	for _, plan := range []string{"solo", "pro"} {
		e.subscribe(t, "sub-count", plan)
		r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "one-more", "class": "small"})
		if r.status != 400 || errCode(r) != "invalid" || errDetail(r, "reason") != "project_limit" || r.body["error"].(map[string]any)["detail"].(map[string]any)["limit"] != float64(billing.ProjectCap) || errMessage(r) != want {
			t.Fatalf("one more on %s: %d %s", plan, r.status, r.raw)
		}
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'destroying' where user_id = $1 and slug = 'p0'", uid); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "one-more", "class": "small"}); r.status != 201 {
		t.Fatalf("after a destroy: %d %s", r.status, r.raw)
	}
}

// §8: with BILLING_ENFORCE=false nothing is blocked.
func TestBillingEnforceFalseLetsStartsThrough(t *testing.T) {
	e := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		d.BillingEnforce = false
	})
	tok := e.signIn(t, "sub-noenf", "noenf-dev")
	e.subscribe(t, "sub-noenf", "")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "unblocked", "class": "xl"})
	if r.status != 201 {
		t.Fatalf("create with enforcement off and no plan: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
}

// Without PADDLE_API_KEY every /billing route is 503 billing_disabled and
// /me still answers.
func TestBillingDisabledRoutes(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-dis", "dis-dev")
	for _, call := range []struct{ method, path string }{
		{"GET", "/billing"}, {"POST", "/billing/checkout"}, {"POST", "/billing/plan"}, {"POST", "/billing/cancel"},
		{"POST", "/billing/resume"}, {"POST", "/billing/portal"}, {"GET", "/billing/invoices"},
	} {
		r := e.do(t, tok, call.method, call.path, map[string]any{"plan": "solo"})
		if r.status != 503 || errCode(r) != "billing_disabled" {
			t.Errorf("%s %s: %d %s", call.method, call.path, r.status, r.raw)
		}
	}
	if r := e.doRaw(t, "POST", "/billing/webhook", []byte("{}"), nil); r.status != 503 {
		t.Errorf("webhook: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/me", nil); r.status != 200 || r.body["billing"].(map[string]any)["status"] != "active" {
		t.Errorf("/me: %d %s", r.status, r.raw)
	}
}

// The /billing routes against the fake Paddle: the overview's shape,
// checkout (and its refusals), plan change, cancel, resume, portal and
// invoices, and the webhook route (no bearer; a bad signature is 400 with
// the type logged and nothing else; a duplicate is 200).
func TestBillingRoutes(t *testing.T) {
	f := newFakePaddle(t)
	var svc *billing.Service
	e := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		cfg := f.cfg
		p := billing.NewPaddle(cfg, d.Log)
		seats := &billing.SubscriptionSeats{Pool: d.Pool, Total: 30}
		overage := billing.NewOverage(d.Pool, p, cfg, d.Engine, d.Metrics, d.Log)
		svc = billing.NewService(d.Pool, p, cfg, seats, overage, d.Log)
		d.Billing = svc
		d.Webhooks = billing.NewWebhooks(d.Pool, cfg, d.Metrics, d.Log)
		d.Webhooks.Stop = d.Engine
		d.Gate = billing.NewGate(d.Pool, cfg, d.Metrics, d.Log)
	})
	ctx := context.Background()
	tok := e.signIn(t, "sub-bill", "bill-dev")
	e.subscribe(t, "sub-bill", "")

	// Overview without a plan.
	r := e.do(t, tok, "GET", "/billing", nil)
	if r.status != 200 || r.body["subscription"] != nil {
		t.Fatalf("overview: %d %s", r.status, r.raw)
	}
	for _, k := range []string{"subscription", "usage", "plans", "seats", "waitlist", "paddle"} {
		if _, ok := r.body[k]; !ok {
			t.Errorf("overview lacks %s", k)
		}
	}
	plans := r.body["plans"].([]any)
	if len(plans) != 3 || plans[0].(map[string]any)["available"] != true || plans[1].(map[string]any)["price_cents"] != float64(5900) || plans[2].(map[string]any)["price_cents"] != float64(9900) {
		t.Fatalf("plans: %v", plans)
	}
	if r.body["paddle"].(map[string]any)["client_token"] != "test_client_token" || r.body["paddle"].(map[string]any)["environment"] != "sandbox" {
		t.Fatalf("paddle block: %v", r.body["paddle"])
	}
	// Checkout: unknown plan, then a transaction.
	if r := e.do(t, tok, "POST", "/billing/checkout", map[string]any{"plan": "gold"}); r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("unknown plan: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/billing/checkout", map[string]any{"plan": "solo"})
	if r.status != 200 || !strings.HasPrefix(r.body["transaction_id"].(string), "txn_") || r.body["client_token"] != "test_client_token" || r.body["environment"] != "sandbox" {
		t.Fatalf("checkout: %d %s", r.status, r.raw)
	}
	// Plan change and cancel before a subscription: 409 no_subscription.
	if r := e.do(t, tok, "POST", "/billing/plan", map[string]any{"plan": "plus"}); r.status != 409 || errDetail(r, "reason") != "no_subscription" {
		t.Fatalf("plan without subscription: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/billing/cancel", nil); r.status != 409 {
		t.Fatalf("cancel without subscription: %d %s", r.status, r.raw)
	}
	// The webhook brings the subscription: no bearer, signature is the auth.
	var uid, customer string
	if err := e.h.Pool.QueryRow(ctx, "select id, paddle_customer_id from users where logto_sub = 'sub-bill'").Scan(&uid, &customer); err != nil {
		t.Fatal(err)
	}
	subID := f.addSubscription(customer, "pri_solo_test", "trialing")
	body := f.event("subscription.created", f.subData(subID, uid, customer, "pri_solo_test", "trialing"))
	r = e.doRaw(t, "POST", "/billing/webhook", body, map[string]string{"Paddle-Signature": f.sign(body)})
	if r.status != 200 || r.body["received"] != true {
		t.Fatalf("webhook: %d %s", r.status, r.raw)
	}
	r = e.doRaw(t, "POST", "/billing/webhook", body, map[string]string{"Paddle-Signature": f.sign(body)})
	if r.status != 200 || r.body["duplicate"] != true {
		t.Fatalf("duplicate: %d %s", r.status, r.raw)
	}
	r = e.doRaw(t, "POST", "/billing/webhook", body, map[string]string{"Paddle-Signature": "ts=1;h1=00"})
	if r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("bad signature: %d %s", r.status, r.raw)
	}
	if !strings.Contains(e.logs.String(), `"kind":"subscription.created"`) && !strings.Contains(e.logs.String(), "kind=subscription.created") {
		t.Error("the rejection did not log the event type")
	}
	if strings.Contains(e.logs.String(), customer) {
		t.Error("the webhook body reached the log")
	}
	// /me now says trial on solo; a second checkout is 409 subscribed.
	r = e.do(t, tok, "GET", "/me", nil)
	b := r.body["billing"].(map[string]any)
	if b["status"] != "trial" || b["plan"] != "solo" || b["trial_end"] == nil || b["has_card"] != true {
		t.Fatalf("/me after webhook: %s", r.raw)
	}
	if r := e.do(t, tok, "POST", "/billing/checkout", map[string]any{"plan": "plus"}); r.status != 409 || errDetail(r, "reason") != "subscribed" {
		t.Fatalf("second checkout: %d %s", r.status, r.raw)
	}
	// Overview with a plan.
	r = e.do(t, tok, "GET", "/billing", nil)
	sub := r.body["subscription"].(map[string]any)
	if sub["plan"] != "solo" || sub["status"] != "trialing" || sub["seats"] != float64(1) {
		t.Fatalf("overview subscription: %v", sub)
	}
	usage := r.body["usage"].(map[string]any)
	if usage["memory_gb"] != float64(8) || usage["egress_included_gb"] != float64(250) || usage["project_limit"] != float64(billing.ProjectCap) {
		t.Fatalf("usage: %v", usage)
	}
	// Upgrade to plus, then cancel and resume, then the portal and invoices.
	r = e.do(t, tok, "POST", "/billing/plan", map[string]any{"plan": "plus"})
	if r.status != 200 || r.body["plan"] != "plus" || r.body["scheduled_plan"] != nil {
		t.Fatalf("upgrade: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/billing/cancel", nil)
	if r.status != 200 || r.body["cancel_at"] == nil {
		t.Fatalf("cancel: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/billing/cancel", nil); r.status != 409 || errDetail(r, "reason") != "already_cancelled" {
		t.Fatalf("second cancel: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/billing/resume", nil)
	if r.status != 200 || r.body["plan"] != "plus" {
		t.Fatalf("resume: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/billing/resume", nil); r.status != 409 || errDetail(r, "reason") != "not_cancelled" {
		t.Fatalf("second resume: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/billing/portal", nil)
	if r.status != 200 || !strings.HasPrefix(r.body["url"].(string), "https://portal.fake/overview/") {
		t.Fatalf("portal: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/billing/portal", map[string]any{"for": "payment_method"})
	if r.status != 200 || !strings.HasPrefix(r.body["url"].(string), "https://portal.fake/payment/") {
		t.Fatalf("portal payment link: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/billing/portal", map[string]any{"for": "cancel"}); r.status != 400 {
		t.Fatalf("portal bad for: %d", r.status)
	}
	f.addTransaction(customer, subID, "completed", 2900, 0)
	r = e.do(t, tok, "GET", "/billing/invoices", nil)
	if r.status != 200 || len(r.list) != 1 || r.list[0].(map[string]any)["amount_cents"] != float64(2900) {
		t.Fatalf("invoices: %d %s", r.status, r.raw)
	}
	// Deleting the account cancels the subscription at once.
	r = e.do(t, tok, "DELETE", "/me", nil)
	if r.status != 202 {
		t.Fatalf("delete me: %d %s", r.status, r.raw)
	}
	var status string
	if err := e.h.Pool.QueryRow(ctx, "select status from subscriptions where id = $1", subID).Scan(&status); err != nil || status != "canceled" {
		t.Fatalf("subscription after delete: %s %v", status, err)
	}
	if f.body("POST /subscriptions/" + subID + "/cancel")["effective_from"] != "immediately" {
		t.Fatal("account deletion cancels immediately")
	}
	// A waitlisted checkout: 503 with the place and the sentence.
	full := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		p := billing.NewPaddle(f.cfg, d.Log)
		d.Billing = billing.NewService(d.Pool, p, f.cfg, fullSeats{}, nil, d.Log)
	})
	tok2 := full.signIn(t, "sub-full", "full-dev")
	full.subscribe(t, "sub-full", "")
	r = full.do(t, tok2, "POST", "/billing/checkout", map[string]any{"plan": "solo"})
	if r.status != 503 || errCode(r) != "waitlisted" || errMessage(r) != "repose is full right now. You're number 3 on the waitlist; we'll email full-dev@example.com when there's a seat." {
		t.Fatalf("waitlisted: %d %s", r.status, r.raw)
	}
	d := r.body["error"].(map[string]any)["detail"].(map[string]any)
	if d["position"] != float64(3) || d["email"] != "full-dev@example.com" || d["joined_at"] == nil {
		t.Fatalf("waitlisted detail: %v", d)
	}
}

// fullSeats refuses every reservation with a place.
type fullSeats struct{}

func (fullSeats) Reserve(_ context.Context, _ string, _ int) (bool, *waitlist.Place, error) {
	return false, &waitlist.Place{Position: 3, JoinedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}, nil
}
func (fullSeats) Converted(context.Context, string) error { return nil }
func (fullSeats) Count(context.Context) (waitlist.Count, error) {
	return waitlist.Count{Total: 30, Held: 30, Free: 0, Waiting: 3}, nil
}

// Project carries running_seconds_today and _month (and cost 0) since I-289.
func TestProjectCarriesRunningSeconds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tok := e.signIn(t, "sub-hours", "hours-dev")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "clock", "class": "small"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	now := time.Now().UTC().Truncate(time.Hour)
	if _, err := e.h.Pool.Exec(ctx, `insert into usage_hours (project_id, hour, class, running_seconds, gb_alloc, egress_bytes, cost_cents, period_start, period_end, price_version)
		values ($1, $2, 'small', 1800, 20, 0, 0, date_trunc('month', now()), date_trunc('month', now()) + interval '1 month', 'plan-v1')`, pid, now); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "GET", "/projects/"+pid, nil)
	if r.status != 200 || r.body["running_seconds_today"] != float64(1800) || r.body["running_seconds_month"] != float64(1800) || r.body["cost_today_cents"] != float64(0) {
		t.Fatalf("project: %s", r.raw)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.raw), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"running_seconds_today", "running_seconds_month", "cost_today_cents", "cost_month_cents"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("Project lacks %s", k)
		}
	}
}
