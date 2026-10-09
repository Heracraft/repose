package billing_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

// TestPolarSandbox runs the billing package against Polar's real sandbox
// (docs/ops/M4-GATE.md §1) when POLAR_ENVIRONMENT=sandbox and
// POLAR_ACCESS_TOKEN are set: the bootstrap finds the catalog and a rerun
// creates nothing, and a checkout is created for a fresh account with the
// introductory discount. It skips without the token, so CI without a
// sandbox organization is green; it refuses production.
//
// With REPOSE_POLAR_E2E_LISTEN (an address such as 127.0.0.1:8787) it
// goes on end to end: it serves the webhook there for `polar listen
// http://ADDR/webhook` (POLAR_WEBHOOK_SECRET is the listen secret),
// writes the checkout URL to REPOSE_POLAR_E2E_URLFILE and waits for a
// person or a browser to pay it with Stripe's test card, then ends the
// trial with a real charge and walks the plan changes, cancellation,
// the portal, the invoices, the overage event and account deletion, each
// confirmed by Polar's own webhook.
func TestPolarSandbox(t *testing.T) {
	if os.Getenv("POLAR_ACCESS_TOKEN") == "" {
		t.Skip("POLAR_ACCESS_TOKEN not set")
	}
	cfg, _ := billing.ConfigFromEnv()
	if cfg.Env != billing.EnvSandbox {
		t.Fatal("POLAR_ENVIRONMENT is not sandbox; the test never runs against production")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	p := billing.NewPolar(cfg, quiet())
	res, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{Progress: testWriter{t}})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	again, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{})
	if err != nil || len(again.Created) != 0 {
		t.Fatalf("rerun: created %v, %v", again.Created, err)
	}
	cfg.ProductSolo, cfg.ProductPlus, cfg.ProductPro, cfg.DiscountIntro = res.ProductSolo, res.ProductPlus, res.ProductPro, res.DiscountIntro
	if err := cfg.Validate(); err != nil && !strings.Contains(err.Error(), "POLAR_WEBHOOK_SECRET") {
		t.Fatalf("config: %v", err)
	}

	pool := testdb.Open(t)
	a := seedAccount(t, pool, "", "none", "large", "running")
	email := "repose-e2e-" + a.UserID.String()[:8] + "@herakraft.co"
	if _, err := pool.Exec(ctx, "update users set email = $2 where id = $1", a.UserID, email); err != nil {
		t.Fatal(err)
	}
	ov := billing.NewOverage(pool, p, cfg, &stopRecorder{}, nop(), quiet())
	svc := billing.NewService(pool, p, cfg, &billing.SubscriptionSeats{Pool: pool, Total: 30}, ov, quiet())
	u, err := store.GetUser(ctx, pool, a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	url, err := svc.Checkout(ctx, u, "solo", "")
	if err != nil || !strings.HasPrefix(url, "https://sandbox.polar.sh/") {
		t.Fatalf("checkout: %q %v", url, err)
	}
	t.Logf("checkout for %s: %s", email, url)

	listen := os.Getenv("REPOSE_POLAR_E2E_LISTEN")
	if listen == "" {
		return
	}
	if cfg.WebhookSecret == "" {
		t.Fatal("POLAR_WEBHOOK_SECRET (the `polar listen` secret) is not set")
	}
	stop := &stopRecorder{}
	hooks := billing.NewWebhooks(pool, cfg, nop(), quiet())
	hooks.Stop = stop
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		kind, err := hooks.Handle(r.Context(), body, billing.HeadersFrom(r.Header))
		t.Logf("webhook %s: %v", kind, err)
		if err != nil && err != billing.ErrDuplicate {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(200)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	if f := os.Getenv("REPOSE_POLAR_E2E_URLFILE"); f != "" {
		if err := os.WriteFile(f, []byte(url+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sub := waitSub(ctx, t, pool, a.UserID, "the checkout's subscription", func(s *billing.Sub) bool { return s != nil })
	t.Logf("subscription %s: %s %s, trial_end %v, intro %v until %v", sub.ID, sub.Plan, sub.Status, sub.TrialEnd, sub.Intro, sub.IntroUntil)
	if sub.Status != billing.StatusTrialing || sub.Plan != "solo" || !sub.Intro || sub.IntroUntil == nil || sub.TrialEnd == nil {
		t.Fatalf("after checkout: %+v", sub)
	}
	if got := userField(t, pool, a, "billing_status"); got != "trial" {
		t.Fatalf("billing_status after checkout = %q", got)
	}
	if got := userField(t, pool, a, "billing_customer_id"); got == "" || got != sub.CustomerID {
		t.Fatalf("billing_customer_id = %q, subscription customer %q", got, sub.CustomerID)
	}

	// The trial ends now: Polar charges the first period with the test
	// card, the introductory $20, and the subscription becomes active.
	if err := endTrialNow(ctx, p, sub.ID); err != nil {
		t.Fatalf("end the trial: %v", err)
	}
	sub = waitSub(ctx, t, pool, a.UserID, "active after the trial", func(s *billing.Sub) bool { return s != nil && s.Status == billing.StatusActive })
	waitUser(ctx, t, pool, a, "billing_status", "active")

	// Upgrade at once, then a downgrade waits for the period's end, then
	// it is undone.
	u, _ = store.GetUser(ctx, pool, a.UserID)
	if ch, err := svc.ChangePlan(ctx, u, "plus"); err != nil || ch.Plan != "plus" {
		t.Fatalf("upgrade: %+v %v", ch, err)
	}
	waitSub(ctx, t, pool, a.UserID, "plus from Polar", func(s *billing.Sub) bool { return s != nil && s.Plan == "plus" })
	// Leaving Solo ends the introductory offer: Plus renews at $59.
	if ps, err := p.GetSubscription(ctx, sub.ID); err != nil || ps.DiscountID != "" || ps.Amount != 5900 {
		t.Fatalf("Polar after the upgrade: discount %q, amount %d, %v", ps.DiscountID, ps.Amount, err)
	}
	if ch, err := svc.ChangePlan(ctx, u, "solo"); err != nil || ch.ScheduledPlan == nil || *ch.ScheduledPlan != "solo" {
		t.Fatalf("downgrade: %+v %v", ch, err)
	}
	waitSub(ctx, t, pool, a.UserID, "solo scheduled by Polar", func(s *billing.Sub) bool {
		return s != nil && s.Plan == "plus" && s.ScheduledPlan != nil && *s.ScheduledPlan == "solo"
	})
	if _, err := svc.ChangePlan(ctx, u, "plus"); err != nil {
		t.Fatalf("undo the downgrade: %v", err)
	}
	ps, err := p.GetSubscription(ctx, sub.ID)
	if err != nil || ps.PendingUpdate != nil || ps.ProductID != cfg.ProductPlus {
		t.Fatalf("after the undo Polar has %+v %v", ps, err)
	}

	// Cancel, then resume.
	at, err := svc.Cancel(ctx, u)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitSub(ctx, t, pool, a.UserID, "cancel_at from Polar", func(s *billing.Sub) bool { return s != nil && s.CancelAt != nil && s.CancelAt.Equal(at) })
	if _, err := svc.Resume(ctx, u); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitSub(ctx, t, pool, a.UserID, "no cancel_at from Polar", func(s *billing.Sub) bool { return s != nil && s.CancelAt == nil })

	// The portal and the invoices.
	u, _ = store.GetUser(ctx, pool, a.UserID)
	portal, err := svc.Portal(ctx, u, "payment_method")
	if err != nil || !strings.HasPrefix(portal, "https://") {
		t.Fatalf("portal: %q %v", portal, err)
	}
	inv, err := svc.Invoices(ctx, u)
	if err != nil || len(inv) == 0 {
		t.Fatalf("invoices: %v %v", inv, err)
	}
	b, _ := json.Marshal(inv)
	t.Logf("invoices: %s", b)

	// The period's egress is 3 GB over Plus's 500 GB: account deletion
	// sends the overage event and cancels at the period's end.
	sub, _ = billing.LiveSubscription(ctx, pool, a.UserID)
	usageHour(t, pool, a.ProjectID, sub.PeriodStart.Truncate(time.Hour).Add(time.Hour), "large", 3600, 503<<30, sub.Period(time.Now()))
	if err := svc.CloseAccount(ctx, a.UserID); err != nil {
		t.Fatalf("close the account: %v", err)
	}
	var ref *string
	if err := pool.QueryRow(ctx, "select sent_ref from overage_charges where subscription_id = $1", sub.ID).Scan(&ref); err != nil || ref == nil {
		t.Fatalf("overage line: %v %v", ref, err)
	}
	waitSub(ctx, t, pool, a.UserID, "cancel_at after the close", func(s *billing.Sub) bool { return s != nil && s.CancelAt != nil })

	// Clean up: revoke, which Polar reports as canceled; the machines stop.
	if _, err := p.RevokeSubscription(ctx, sub.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	waitUser(ctx, t, pool, a, "billing_status", "none")
	if len(stop.calls) == 0 {
		t.Fatal("the end of the subscription stopped no machine")
	}
	var errs int
	if err := pool.QueryRow(ctx, "select count(*) from billing_events where error is not null").Scan(&errs); err != nil || errs != 0 {
		rows, _ := pool.Query(ctx, "select type, error from billing_events where error is not null")
		for rows.Next() {
			var k, e string
			_ = rows.Scan(&k, &e)
			t.Logf("event %s failed: %s", k, e)
		}
		t.Fatalf("%d webhook events failed", errs)
	}
}

func endTrialNow(ctx context.Context, p *billing.Polar, id string) error {
	return billing.PatchSubscriptionForTest(ctx, p, id, map[string]any{"trial_end": "now"})
}

func waitSub(ctx context.Context, t *testing.T, pool *db.Pool, userID uuid.UUID, what string, ok func(*billing.Sub) bool) *billing.Sub {
	t.Helper()
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		s, err := billing.LatestSubscription(ctx, pool, userID)
		if err != nil {
			t.Fatal(err)
		}
		if ok(s) {
			return s
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for %s", what)
	return nil
}

func waitUser(ctx context.Context, t *testing.T, pool *db.Pool, a account, col, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if userField(t, pool, a, col) == want {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("users.%s never became %q (is %q)", col, want, userField(t, pool, a, col))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
