package billing_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db/testdb"
)

func TestPaddleEnvironmentFromKey(t *testing.T) {
	if billing.Environment("pdl_sdbx_apikey_test") != billing.EnvSandbox || billing.Environment("pdl_live_apikey_test") != billing.EnvLive {
		t.Fatal("the key prefix tells sandbox from live")
	}
	cfg := billing.Config{APIKey: "pdl_sdbx_apikey_test"}
	if cfg.Environment() != "sandbox" {
		t.Fatal("config environment")
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "PADDLE_PRICE_PRO") || !strings.Contains(err.Error(), "PADDLE_PRICE_PLUS") || !strings.Contains(err.Error(), "PADDLE_WEBHOOK_SECRET") {
		t.Fatalf("a key without the rest is refused, naming what is missing: %v", err)
	}
	if err := (billing.Config{}).Validate(); err != nil {
		t.Fatalf("no key is a valid (disabled) configuration: %v", err)
	}
	full := billing.Config{APIKey: "k", WebhookSecret: "s", PriceSolo: "a", PricePlus: "c", PricePro: "b", ProductOverage: "p", DiscountIntro: "d"}
	if err := (billing.Config{APIKey: "k", WebhookSecret: "s", PriceSolo: "a", PricePlus: "c", PricePro: "b", ProductOverage: "p"}).Validate(); err == nil || !strings.Contains(err.Error(), "PADDLE_DISCOUNT_INTRO") {
		t.Fatalf("no introductory discount is refused: %v", err)
	}
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	if full.PlanForPrice("a") != "solo" || full.PlanForPrice("c") != "plus" || full.PlanForPrice("b") != "pro" || full.PlanForPrice("zzz") != "" || full.PlanPrice("plus") != "c" || full.PlanPrice("pro") != "b" {
		t.Fatal("price <-> plan mapping")
	}
	full.PricePro = "c"
	if err := full.Validate(); err == nil {
		t.Fatal("two plans on one price is refused")
	}
	full.PricePro = "a"
	if full.PlanForPrice("a") != "solo" || full.PlanForPrice("zzz") != "" || full.PlanPrice("pro") != "a" {
		t.Fatal("price <-> plan mapping")
	}
	if (billing.Config{}).BillingURL() != "https://repose.herakraft.co/billing" {
		t.Fatal("default billing url")
	}
}

// The client: headers, retries on 429 with Retry-After and on 5xx, three
// tries at most, and errors that carry Paddle's code and never the key.
func TestPaddleClientRetriesAndErrors(t *testing.T) {
	f := newFakePaddle()
	defer f.Close()
	cfg := testConfig(f)
	p := billing.NewPaddle(cfg, quiet())
	var slept []time.Duration
	p.Sleep = func(d time.Duration) { slept = append(slept, d) }
	ctx := context.Background()

	id, err := p.CreateCustomer(ctx, "a@example.test", store.NewID())
	if err != nil || !strings.HasPrefix(id, "ctm_") {
		t.Fatalf("create customer: %s %v", id, err)
	}
	if f.KeySeen != cfg.APIKey || f.Version != "1" {
		t.Fatalf("headers: key seen %q version %q", f.KeySeen, f.Version)
	}

	// 429 twice with Retry-After, then success: two sleeps of 2 s.
	f.Fail["GET /customers"] = 2
	f.FailCode = 429
	f.RetryAfter = "2"
	got, err := p.FindCustomerByEmail(ctx, "a@example.test")
	if err != nil || got != id {
		t.Fatalf("after retries: %q %v", got, err)
	}
	if len(slept) != 2 || slept[0] != 2*time.Second || slept[1] != 2*time.Second {
		t.Fatalf("Retry-After honoured: slept %v", slept)
	}

	// 5xx every time: three tries, then the error.
	slept = nil
	f.Fail["GET /customers"] = 10
	f.FailCode = 503
	f.RetryAfter = ""
	before := f.Count("GET /customers")
	_, err = p.FindCustomerByEmail(ctx, "a@example.test")
	var pe *billing.PaddleError
	if !errors.As(err, &pe) || pe.Status != 503 || pe.Code != "fake_failure" {
		t.Fatalf("error after three tries: %v", err)
	}
	if n := f.Count("GET /customers") - before; n != 3 {
		t.Fatalf("%d tries, want 3", n)
	}
	if len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
		t.Fatalf("backoff doubles: %v", slept)
	}
	if strings.Contains(err.Error(), cfg.APIKey) {
		t.Fatal("the error carries the key")
	}
	f.Fail["GET /customers"] = 0

	// A 4xx is not retried and carries the code.
	_, err = p.GetSubscription(ctx, "sub_missing")
	if !billing.IsPaddleCode(err, "entity_not_found") {
		t.Fatalf("404: %v", err)
	}
	if f.Count("GET /subscriptions/sub_missing") != 1 {
		t.Fatal("a 404 was retried")
	}
}

// EnsureCustomer finds by email, else creates, and stores the id once.
func TestEnsureCustomer(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePaddle()
	defer f.Close()
	p := billing.NewPaddle(testConfig(f), quiet())
	ctx := context.Background()
	a := seedAccount(t, pool, "", "none", "", "")

	id, err := p.EnsureCustomer(ctx, pool, a.UserID)
	if err != nil || !strings.HasPrefix(id, "ctm_") {
		t.Fatalf("ensure: %s %v", id, err)
	}
	if got := userField(t, pool, a, "paddle_customer_id"); got != id {
		t.Fatalf("stored %q, want %q", got, id)
	}
	again, err := p.EnsureCustomer(ctx, pool, a.UserID)
	if err != nil || again != id {
		t.Fatalf("second ensure: %s %v", again, err)
	}
	if f.Count("POST /customers") != 1 {
		t.Fatal("a second customer was created")
	}
	// A customer Paddle already has for the email is adopted, not doubled.
	b := seedAccount(t, pool, "", "none", "", "")
	if _, err := p.CreateCustomer(ctx, b.Email, store.NewID()); err != nil {
		t.Fatal(err)
	}
	idB, err := p.EnsureCustomer(ctx, pool, b.UserID)
	if err != nil || idB == "" || f.Count("POST /customers") != 2 {
		t.Fatalf("adopt: %s %v (%d creates)", idB, err, f.Count("POST /customers"))
	}
	if bodies := f.Bodies["POST /customers"]; bodies[0]["custom_data"].(map[string]any)["user_id"] != a.UserID.String() {
		t.Fatal("custom_data.user_id was not stamped")
	}
}
