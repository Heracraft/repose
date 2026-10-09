package billing_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/billing"
)

// The bootstrap creates every object once and finds it on every rerun;
// a live key needs --live; the block names every id and never the key.
func TestBootstrapIsIdempotent(t *testing.T) {
	f := newFakePaddle()
	defer f.Close()
	cfg := testConfig(f)
	p := billing.NewPaddle(cfg, quiet())
	ctx := context.Background()
	var progress bytes.Buffer
	res, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{WebhookURL: "https://api.repose.test/v1/billing/webhook", Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 9 || len(res.Found) != 0 {
		t.Fatalf("first run: created %v found %v", res.Created, res.Found)
	}
	if res.PriceSolo == "" || res.PricePlus == "" || res.PricePro == "" || res.ProductOverage == "" || res.WebhookSecret != f.Secret() || res.Environment != "sandbox" {
		t.Fatalf("result: %+v", res)
	}
	prices := f.Bodies["POST /prices"]
	if len(prices) != 3 {
		t.Fatalf("%d prices created", len(prices))
	}
	for _, pr := range prices {
		tp := pr["trial_period"].(map[string]any)
		bc := pr["billing_cycle"].(map[string]any)
		up := pr["unit_price"].(map[string]any)
		if tp["interval"] != "day" || tp["frequency"] != float64(7) || bc["interval"] != "month" || bc["frequency"] != float64(1) || up["currency_code"] != "USD" {
			t.Fatalf("price body: %v", pr)
		}
		if up["amount"] != "2900" && up["amount"] != "5900" && up["amount"] != "9900" {
			t.Fatalf("price amount %v", up["amount"])
		}
	}
	// The introductory discount: $9 off Solo's price for three charges.
	ds := f.Bodies["POST /discounts"]
	if len(ds) != 1 || res.DiscountIntro == "" {
		t.Fatalf("discounts created: %v (%q)", ds, res.DiscountIntro)
	}
	d := ds[0]
	if d["type"] != "flat" || d["amount"] != "900" || d["currency_code"] != "USD" || d["recur"] != true || d["maximum_recurring_intervals"] != float64(3) ||
		len(d["restrict_to"].([]any)) != 1 || d["restrict_to"].([]any)[0] != res.PriceSolo || d["custom_data"].(map[string]any)["repose"] != "intro-solo-2000-3" {
		t.Fatalf("discount body: %v", d)
	}
	ns := f.Bodies["POST /notification-settings"][0]
	if ns["destination"] != "https://api.repose.test/v1/billing/webhook" || len(ns["subscribed_events"].([]any)) != len(billing.WebhookEvents) || ns["traffic_source"] != "all" {
		t.Fatalf("notification setting: %v", ns)
	}
	block := res.EnvBlock()
	for _, want := range []string{"PADDLE_PRICE_SOLO=" + res.PriceSolo, "PADDLE_PRICE_PLUS=" + res.PricePlus, "PADDLE_PRICE_PRO=" + res.PricePro, "PADDLE_PRODUCT_OVERAGE=" + res.ProductOverage, "PADDLE_DISCOUNT_INTRO=" + res.DiscountIntro, "PADDLE_WEBHOOK_SECRET=" + f.Secret()} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, cfg.APIKey) || strings.Contains(progress.String(), cfg.APIKey) {
		t.Fatal("the key was printed")
	}
	// Rerun: everything found, nothing created.
	before := len(f.Requests)
	again, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{WebhookURL: "https://api.repose.test/v1/billing/webhook"})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Created) != 0 || len(again.Found) != 9 || again.DiscountIntro != res.DiscountIntro || again.PriceSolo != res.PriceSolo || again.PricePlus != res.PricePlus || again.PricePro != res.PricePro || again.WebhookSecret != f.Secret() {
		t.Fatalf("rerun: created %v found %v", again.Created, again.Found)
	}
	for _, r := range f.Requests[before:] {
		if strings.HasPrefix(r, "POST ") || strings.HasPrefix(r, "PATCH ") {
			t.Fatalf("rerun made %s", r)
		}
	}
	// A live key is refused without --live, before any call.
	live := billing.NewPaddle(billing.Config{APIKey: "pdl_live_apikey_test", BaseURL: f.URL()}, quiet())
	calls := len(f.Requests)
	if _, err := billing.Bootstrap(ctx, live, billing.BootstrapOptions{}); !errors.Is(err, billing.ErrLiveKey) || len(f.Requests) != calls {
		t.Fatalf("live without --live: %v (%d calls)", err, len(f.Requests)-calls)
	}
	if _, err := billing.Bootstrap(ctx, live, billing.BootstrapOptions{Live: true}); err != nil {
		t.Fatalf("live with --live: %v", err)
	}
}

// A sandbox destination an earlier bootstrap made with traffic_source
// platform cannot receive simulations; a rerun switches it to all. A live
// destination stays platform.
func TestBootstrapTrafficSource(t *testing.T) {
	f := newFakePaddle()
	defer f.Close()
	ctx := context.Background()
	const hook = "https://api.repose.test/v1/billing/webhook"
	f.settings["ntfset_old"] = map[string]any{"id": "ntfset_old", "type": "url", "destination": hook, "active": true,
		"endpoint_secret_key": f.Secret(), "traffic_source": "platform"}
	p := billing.NewPaddle(testConfig(f), quiet())
	var progress bytes.Buffer
	res, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{WebhookURL: hook, Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}
	if res.WebhookID != "ntfset_old" || f.settings["ntfset_old"]["traffic_source"] != "all" || !strings.Contains(progress.String(), "traffic_source all") {
		t.Fatalf("sandbox destination: %v; progress:\n%s", f.settings["ntfset_old"], progress.String())
	}
	live := billing.NewPaddle(billing.Config{APIKey: "pdl_live_apikey_test", BaseURL: f.URL()}, quiet())
	if _, err := billing.Bootstrap(ctx, live, billing.BootstrapOptions{WebhookURL: "https://api.repose.test/live", Live: true}); err != nil {
		t.Fatal(err)
	}
	bodies := f.Bodies["POST /notification-settings"]
	if last := bodies[len(bodies)-1]; last["traffic_source"] != "platform" {
		t.Fatalf("live destination: %v", last)
	}
}
