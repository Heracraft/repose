package billing_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/billing"
)

// The bootstrap creates every object once and finds it on every rerun;
// production needs --production; the block names every id and never the
// token; the organization ends up set the way I-604 says.
func TestBootstrapIsIdempotent(t *testing.T) {
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	p := billing.NewPolar(cfg, quiet())
	ctx := context.Background()
	const hook = "https://api.repose.test/v1/billing/webhook"
	var progress bytes.Buffer
	res, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{WebhookURL: hook, Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}
	// meter, three products, the discount, the webhook endpoint.
	if len(res.Created) != 6 || len(res.Found) != 0 {
		t.Fatalf("first run: created %v found %v", res.Created, res.Found)
	}
	if res.ProductSolo == "" || res.ProductPlus == "" || res.ProductPro == "" || res.MeterOverage == "" || res.WebhookSecret != f.Secret() || res.Environment != "sandbox" || res.Organization != "repose-fake" {
		t.Fatalf("result: %+v", res)
	}
	org := f.Bodies["PATCH /organizations/org_fake"]
	if len(org) != 1 || org[0]["default_tax_behavior"] != "exclusive" {
		t.Fatalf("organization update: %v", org)
	}
	ss := org[0]["subscription_settings"].(map[string]any)
	ps := org[0]["customer_portal_settings"].(map[string]any)["subscription"].(map[string]any)
	if ss["allow_multiple_subscriptions"] != false || ss["prevent_trial_abuse"] != true || ps["update_plan"] != false || ps["update_seats"] != false {
		t.Fatalf("organization settings: %v", org[0])
	}
	m := f.Bodies["POST /meters/"][0]
	clause := m["filter"].(map[string]any)["clauses"].([]any)[0].(map[string]any)
	if clause["property"] != "name" || clause["operator"] != "eq" || clause["value"] != billing.OverageEvent || m["aggregation"].(map[string]any)["property"] != "gb" {
		t.Fatalf("meter body: %v", m)
	}
	products := f.Bodies["POST /products/"]
	if len(products) != 3 {
		t.Fatalf("%d products created", len(products))
	}
	for i, pr := range products {
		plan := billing.Plans[i]
		if pr["recurring_interval"] != "month" || pr["trial_interval"] != "day" || pr["trial_interval_count"] != float64(7) || pr["metadata"].(map[string]any)["repose"] != "plan-"+plan.ID+"-"+itoa(plan.PriceCents) {
			t.Fatalf("product body: %v", pr)
		}
		prices := pr["prices"].([]any)
		fixed, metered := prices[0].(map[string]any), prices[1].(map[string]any)
		if fixed["amount_type"] != "fixed" || fixed["price_amount"] != float64(plan.PriceCents) || fixed["price_currency"] != "usd" || fixed["tax_behavior"] != "exclusive" {
			t.Fatalf("fixed price: %v", fixed)
		}
		if metered["amount_type"] != "metered_unit" || metered["meter_id"] != res.MeterOverage || metered["unit_amount"] != "5" || metered["tax_behavior"] != "exclusive" {
			t.Fatalf("metered price: %v", metered)
		}
	}
	// The introductory discount: $9 off Solo for three months.
	ds := f.Bodies["POST /discounts/"]
	if len(ds) != 1 || res.DiscountIntro == "" {
		t.Fatalf("discounts created: %v (%q)", ds, res.DiscountIntro)
	}
	d := ds[0]
	if d["type"] != "fixed" || d["amount"] != float64(900) || d["currency"] != "usd" || d["duration"] != "repeating" || d["duration_in_months"] != float64(3) ||
		len(d["products"].([]any)) != 1 || d["products"].([]any)[0] != res.ProductSolo || d["metadata"].(map[string]any)["repose"] != "intro-solo-2000-3" || d["code"] != nil {
		t.Fatalf("discount body: %v", d)
	}
	we := f.Bodies["POST /webhooks/endpoints"][0]
	if we["url"] != hook || we["format"] != "raw" || we["api_version"] != billing.APIVersion || len(we["events"].([]any)) != len(billing.WebhookEvents) {
		t.Fatalf("webhook endpoint: %v", we)
	}
	block := res.EnvBlock()
	for _, want := range []string{"POLAR_ENVIRONMENT=sandbox", "POLAR_PRODUCT_SOLO=" + res.ProductSolo, "POLAR_PRODUCT_PLUS=" + res.ProductPlus, "POLAR_PRODUCT_PRO=" + res.ProductPro, "POLAR_DISCOUNT_INTRO=" + res.DiscountIntro, "POLAR_WEBHOOK_SECRET=" + f.Secret()} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, cfg.AccessToken) || strings.Contains(progress.String(), cfg.AccessToken) {
		t.Fatal("the token was printed")
	}
	if f.TokenSeen != cfg.AccessToken || f.Version != billing.APIVersion {
		t.Fatalf("headers: token %q, version %q", f.TokenSeen, f.Version)
	}
	// Rerun: everything found, nothing created or changed.
	before := len(f.Requests)
	again, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{WebhookURL: hook})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Created) != 0 || len(again.Found) != 7 || again.DiscountIntro != res.DiscountIntro || again.ProductSolo != res.ProductSolo || again.ProductPlus != res.ProductPlus || again.ProductPro != res.ProductPro || again.WebhookSecret != f.Secret() {
		t.Fatalf("rerun: created %v found %v", again.Created, again.Found)
	}
	for _, r := range f.Requests[before:] {
		if strings.HasPrefix(r, "POST ") || strings.HasPrefix(r, "PATCH ") {
			t.Fatalf("rerun made %s", r)
		}
	}
	// An endpoint missing an event is brought up to date.
	f.endpoints[again.WebhookID]["events"] = []any{"order.paid"}
	if _, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{WebhookURL: hook}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.Bodies["PATCH /webhooks/endpoints/"+again.WebhookID]); n != 1 {
		t.Fatalf("endpoint updated %d times", n)
	}
	// A product with another price is not reused: a changed price makes a
	// new product, and the introductory discount follows it.
	for id, pr := range f.products {
		if pr["metadata"].(map[string]any)["repose"] == "plan-solo-2900" {
			pr["prices"].([]any)[0].(map[string]any)["price_amount"] = float64(1900)
			delete(f.products, id)
			pr["metadata"] = map[string]any{"repose": "plan-solo-1900"}
			f.products[id] = pr
		}
	}
	third, err := billing.Bootstrap(ctx, p, billing.BootstrapOptions{})
	if err != nil || len(third.Created) != 2 || third.ProductSolo == res.ProductSolo || third.DiscountIntro == res.DiscountIntro {
		t.Fatalf("changed price: created %v, %v", third.Created, err)
	}
	// Production is refused without --production, before any call.
	prod := billing.NewPolar(billing.Config{AccessToken: "polar_oat_prod", Env: billing.EnvProduction, BaseURL: f.URL()}, quiet())
	calls := len(f.Requests)
	if _, err := billing.Bootstrap(ctx, prod, billing.BootstrapOptions{}); !errors.Is(err, billing.ErrProduction) || len(f.Requests) != calls {
		t.Fatalf("production without --production: %v (%d calls)", err, len(f.Requests)-calls)
	}
	if _, err := billing.Bootstrap(ctx, prod, billing.BootstrapOptions{Production: true}); err != nil {
		t.Fatalf("production with --production: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
