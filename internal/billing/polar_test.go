package billing_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/billing"
)

func TestPolarConfig(t *testing.T) {
	cfg := billing.Config{AccessToken: "polar_oat_x", Env: billing.EnvSandbox}
	if cfg.Environment() != "sandbox" {
		t.Fatal("config environment")
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "POLAR_PRODUCT_PRO") || !strings.Contains(err.Error(), "POLAR_PRODUCT_PLUS") || !strings.Contains(err.Error(), "POLAR_WEBHOOK_SECRET") {
		t.Fatalf("a token without the rest is refused, naming what is missing: %v", err)
	}
	if err := (billing.Config{AccessToken: "polar_oat_x"}).Validate(); err == nil || !strings.Contains(err.Error(), "POLAR_ENVIRONMENT") {
		t.Fatalf("a token without an environment is refused: %v", err)
	}
	if err := (billing.Config{AccessToken: "polar_oat_x", Env: "live"}).Validate(); err == nil || !strings.Contains(err.Error(), "POLAR_ENVIRONMENT") {
		t.Fatalf("an unknown environment is refused: %v", err)
	}
	if err := (billing.Config{}).Validate(); err != nil {
		t.Fatalf("no token is a valid (disabled) configuration: %v", err)
	}
	full := billing.Config{AccessToken: "k", Env: billing.EnvProduction, WebhookSecret: "s", ProductSolo: "a", ProductPlus: "c", ProductPro: "b", DiscountIntro: "d"}
	noIntro := full
	noIntro.DiscountIntro = ""
	if err := noIntro.Validate(); err == nil || !strings.Contains(err.Error(), "POLAR_DISCOUNT_INTRO") {
		t.Fatalf("no introductory discount is refused: %v", err)
	}
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	if full.PlanForProduct("a") != "solo" || full.PlanForProduct("c") != "plus" || full.PlanForProduct("b") != "pro" || full.PlanForProduct("zzz") != "" || full.PlanForProduct("") != "" || full.PlanProduct("plus") != "c" || full.PlanProduct("pro") != "b" {
		t.Fatal("product <-> plan mapping")
	}
	full.ProductPro = "c"
	if err := full.Validate(); err == nil {
		t.Fatal("two plans on one product is refused")
	}
	if (billing.Config{}).BillingURL() != "https://repose.herakraft.co/billing" {
		t.Fatal("default billing url")
	}
	t.Setenv("POLAR_ACCESS_TOKEN", " polar_oat_env ")
	t.Setenv("POLAR_ENVIRONMENT", "sandbox")
	t.Setenv("POLAR_PRODUCT_SOLO", "ps")
	t.Setenv("DASHBOARD_URL", "https://dash.test")
	got, on := billing.ConfigFromEnv()
	if !on || got.AccessToken != "polar_oat_env" || got.Env != "sandbox" || got.ProductSolo != "ps" || got.PortalReturnURL != "https://dash.test/billing" {
		t.Fatalf("from the environment: %+v %v", got, on)
	}
}

// The client: headers, retries on 429 with Retry-After and on 5xx, three
// tries at most, and errors that carry Polar's type and detail and never
// the token.
func TestPolarClientRetriesAndErrors(t *testing.T) {
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	p := billing.NewPolar(cfg, quiet())
	var slept []time.Duration
	p.Sleep = func(d time.Duration) { slept = append(slept, d) }
	ctx := context.Background()
	f.AddSubscription("sub_a", "cus_a", cfg.ProductSolo, "active")

	s, err := p.GetSubscription(ctx, "sub_a")
	if err != nil || s.ProductID != cfg.ProductSolo {
		t.Fatalf("get: %+v %v", s, err)
	}
	if f.TokenSeen != cfg.AccessToken || f.Version != billing.APIVersion {
		t.Fatalf("headers: token %q version %q", f.TokenSeen, f.Version)
	}

	// 429 twice with Retry-After, then success: two sleeps of 2 s.
	f.Fail["GET /subscriptions/sub_a"] = 2
	f.FailCode = 429
	f.RetryAfter = "2"
	if _, err := p.GetSubscription(ctx, "sub_a"); err != nil {
		t.Fatalf("after retries: %v", err)
	}
	if len(slept) != 2 || slept[0] != 2*time.Second || slept[1] != 2*time.Second {
		t.Fatalf("Retry-After honoured: slept %v", slept)
	}

	// 5xx every time: three tries, then the error.
	slept = nil
	f.Fail["GET /subscriptions/sub_a"] = 10
	f.FailCode = 503
	f.RetryAfter = ""
	before := f.Count("GET /subscriptions/sub_a")
	_, err = p.GetSubscription(ctx, "sub_a")
	var pe *billing.PolarError
	if !errors.As(err, &pe) || pe.Status != 503 || pe.Type != "FakeFailure" || !strings.Contains(pe.Detail, "told to fail") {
		t.Fatalf("error after three tries: %v", err)
	}
	if n := f.Count("GET /subscriptions/sub_a") - before; n != 3 {
		t.Fatalf("%d tries, want 3", n)
	}
	if len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
		t.Fatalf("backoff doubles: %v", slept)
	}
	if strings.Contains(err.Error(), cfg.AccessToken) {
		t.Fatal("the error carries the token")
	}
	f.Fail["GET /subscriptions/sub_a"] = 0

	// A 4xx is not retried and carries the status.
	_, err = p.GetSubscription(ctx, "sub_missing")
	if !billing.IsPolarStatus(err, 404) {
		t.Fatalf("404: %v", err)
	}
	if f.Count("GET /subscriptions/sub_missing") != 1 {
		t.Fatal("a 404 was retried")
	}
	// A validation failure names the field.
	_, err = p.CreateCheckout(ctx, billing.CheckoutRequest{ProductID: cfg.ProductSolo, DiscountID: "dsc_unknown"})
	if !errors.As(err, &pe) || pe.Status != 422 || !strings.Contains(pe.Detail, "body.discount_id") {
		t.Fatalf("422: %v", err)
	}
}

// The overage event carries the external id, the GB and the user, and a
// resend is a duplicate Polar ignores.
func TestPolarSendOverage(t *testing.T) {
	f := newFakePolar()
	defer f.Close()
	p := billing.NewPolar(testConfig(f), quiet())
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	ref := billing.OverageExternalID("sub_a", start)
	if ref != "overage:sub_a:1790812800" {
		t.Fatalf("external id %q", ref)
	}
	uid := mustUUID(t)
	for range 2 {
		if err := p.SendOverage(ctx, uid, ref, 7, start); err != nil {
			t.Fatal(err)
		}
	}
	evs := f.Events()
	ev, ok := evs[ref]
	if len(evs) != 1 || !ok || ev["name"] != billing.OverageEvent || ev["external_customer_id"] != uid.String() || ev["metadata"].(map[string]any)["gb"] != float64(7) {
		t.Fatalf("events: %v", evs)
	}
}
