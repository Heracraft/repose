package admin_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/store"
)

// docs/workstreams/09-billing.md §9 (I-289): `repose-admin billing
// show|rollup|explain|suspend|unsuspend|overage-now|polar-bootstrap`
// exist, print what the runbook reads, and write audit_log.
func TestBillingSubcommands(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	ctx := h.Ctx
	fake := newFakePolar(t)
	admin.PolarBaseURL = fake.URL
	t.Cleanup(func() { admin.PolarBaseURL = "" })
	t.Setenv("POLAR_ACCESS_TOKEN", "polar_oat_admin_test")
	t.Setenv("POLAR_ENVIRONMENT", "sandbox")
	t.Setenv("POLAR_WEBHOOK_SECRET", "whsec_dGVzdA==")
	t.Setenv("POLAR_PRODUCT_SOLO", "prod_solo_test")
	t.Setenv("POLAR_PRODUCT_PLUS", "prod_plus_test")
	t.Setenv("POLAR_PRODUCT_PRO", "prod_pro_test")
	t.Setenv("POLAR_DISCOUNT_INTRO", "dsc_intro_test")

	// A Solo account with a project, an hour of usage and 260 GB egress.
	uid, pid, gid := store.NewID(), store.NewID(), store.NewID()
	hour := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	periodStart := hour.AddDate(0, 0, -10)
	periodEnd := periodStart.AddDate(0, 1, 0)
	if _, err := h.Pool.Exec(ctx, `insert into users (id, handle, email, billing_status, has_card, billing_customer_id, created_at)
		values ($1, 'payer', 'payer@example.test', 'active', true, 'cus_payer', $2)`, uid, periodStart); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `insert into subscriptions (id, user_id, customer_id, plan, status, seats, period_start, period_end, next_billed_at)
		values ('sub_payer', $1, 'cus_payer', 'solo', 'active', 1, $2, $3, $3)`, uid, periodStart, periodEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes, guest_id, created_at)
		values ($1, $2, 'ledger', 'ledger', 'large', 'running', $3, $4, $5)`, pid, uid, int64(40)<<30, gid, periodStart); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, "select 1"); err != nil {
		t.Fatal(err)
	}
	for m := 0; m < 60; m++ {
		if _, err := h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, host_id, state, class, disk_alloc, net_tx, guestd_ok)
			values ($1, $2, $3, 'running', 'large', $4, $5, true)`, hour.Add(time.Duration(m)*time.Minute), pid, store.NewID(), int64(40)<<30, (int64(260)<<30)/60); err != nil {
			t.Fatal(err)
		}
	}

	// rollup --hour writes the row and prints the table (no cents column).
	out, err := run(t, e, "billing", "rollup", "--hour", hour.Format("2006-01-02T15"))
	if err != nil || !strings.Contains(out, pid.String()) || !strings.Contains(out, "EGRESS") || strings.Contains(out, "CREDIT") {
		t.Fatalf("rollup --hour: %s %v", out, err)
	}
	// show prints the subscription, the period and the overage arithmetic.
	out, err = run(t, e, "billing", "show", "payer")
	if err != nil || !strings.Contains(out, "sub_payer solo active") || !strings.Contains(out, "8 of 8 GB: ledger (large)") || !strings.Contains(out, "= 10 GB x 5 cents = 50 cents") {
		t.Fatalf("show: %s %v", out, err)
	}
	// explain prints the row's inputs and the period's overage.
	out, err = run(t, e, "billing", "explain", "ledger", hour.Format("2006-01-02T15"))
	if err != nil || !strings.Contains(out, "60 running samples x 60") || !strings.Contains(out, "no hourly price") || !strings.Contains(out, "= 10 GB x 5 cents = 50 cents") {
		t.Fatalf("explain: %s %v", out, err)
	}
	// overage-now sends the line once.
	out, err = run(t, e, "billing", "overage-now", "payer")
	if err != nil || !strings.Contains(out, "sent 10 GB over = 50 cents to Polar") || !strings.Contains(out, "(event overage:sub_payer:") {
		t.Fatalf("overage-now: %s %v", out, err)
	}
	if fake.events != 1 || fake.lastGB != 10 || fake.lastCustomer != uid.String() {
		t.Fatalf("%d events sent (%v GB for %s)", fake.events, fake.lastGB, fake.lastCustomer)
	}
	out, err = run(t, e, "billing", "overage-now", "payer")
	if err != nil || !strings.Contains(out, "already has its line") || fake.events != 1 {
		t.Fatalf("overage-now again: %s %v (%d events)", out, err, fake.events)
	}
	// suspend and unsuspend are the users commands with reason billing.
	if out, err := run(t, e, "billing", "suspend", "payer"); err != nil {
		t.Fatalf("suspend: %s %v", out, err)
	}
	var status, reason string
	if err := h.Pool.QueryRow(ctx, "select billing_status, coalesce(suspended_reason, '') from users where id = $1", uid).Scan(&status, &reason); err != nil || status != "suspended" || reason != "billing" {
		t.Fatalf("after suspend: %s %s %v", status, reason, err)
	}
	if out, err := run(t, e, "billing", "unsuspend", "payer"); err != nil {
		t.Fatalf("unsuspend: %s %v", out, err)
	}
	// polar-bootstrap creates the catalog and prints the block, twice
	// the same; production is refused without --production; a missing
	// environment is a usage error.
	out, err = run(t, e, "billing", "polar-bootstrap", "--webhook-url", "https://api.test/v1/billing/webhook")
	if err != nil || !strings.Contains(out, "POLAR_PRODUCT_SOLO=prod_") || !strings.Contains(out, "POLAR_DISCOUNT_INTRO=dsc_") || !strings.Contains(out, "POLAR_WEBHOOK_SECRET=whsec_made") || strings.Contains(out, "polar_oat_admin_test") {
		t.Fatalf("bootstrap: %s %v", out, err)
	}
	again, err := run(t, e, "billing", "polar-bootstrap", "--webhook-url", "https://api.test/v1/billing/webhook")
	if err != nil || !strings.Contains(again, "created 0 object(s), found 7") {
		t.Fatalf("bootstrap rerun: %s %v", again, err)
	}
	t.Setenv("POLAR_ENVIRONMENT", "production")
	if out, err := run(t, e, "billing", "polar-bootstrap", "--no-webhook"); err == nil || !strings.Contains(err.Error(), "--production") {
		t.Fatalf("production without --production: %s %v", out, err)
	}
	t.Setenv("POLAR_ENVIRONMENT", "")
	if out, err := run(t, e, "billing", "polar-bootstrap", "--no-webhook"); !errors.Is(err, admin.ErrUsage) {
		t.Fatalf("no environment: %s %v", out, err)
	}
	// Every command wrote its audit row.
	for _, action := range []string{"billing_rollup", "billing_overage_now", "user_suspend", "user_unsuspend"} {
		var n int
		if err := h.Pool.QueryRow(ctx, "select count(*) from audit_log where action = $1", action).Scan(&n); err != nil || n == 0 {
			t.Errorf("no audit row for %s (%v)", action, err)
		}
	}
	// Removed commands are usage errors.
	for _, gone := range []string{"credit", "reconcile", "resync", "cycle-now"} {
		if _, err := run(t, e, "billing", gone); err == nil {
			t.Errorf("billing %s still exists", gone)
		}
	}
}

// fakePolar answers the calls the admin commands make: the catalog for
// the bootstrap and the event ingestion for overage-now.
type fakePolar struct {
	URL          string
	mu           sync.Mutex
	seq          int
	org          map[string]any
	meters       []any
	products     []any
	discounts    []any
	endpoints    []any
	events       int
	lastGB       float64
	lastCustomer string
}

func newFakePolar(t *testing.T) *fakePolar {
	f := &fakePolar{org: map[string]any{"id": "org_admin", "slug": "admin-test"}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

func (f *fakePolar) write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func page(items []any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"items": items, "pagination": map[string]any{"total_count": len(items), "max_page": 1}}
}

func (f *fakePolar) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	id := func(prefix string) string { f.seq++; return fmt.Sprintf("%s_%04d", prefix, f.seq) }
	created := func(prefix string) map[string]any {
		o := map[string]any{"id": id(prefix)}
		for k, v := range body {
			o[k] = v
		}
		return o
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/organizations/":
		f.write(w, 200, page([]any{f.org}))
	case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/organizations/"):
		for k, v := range body {
			f.org[k] = v
		}
		f.write(w, 200, f.org)
	case r.Method == "GET" && r.URL.Path == "/meters/":
		f.write(w, 200, page(f.meters))
	case r.Method == "POST" && r.URL.Path == "/meters/":
		m := created("mtr")
		f.meters = append(f.meters, m)
		f.write(w, 201, m)
	case r.Method == "GET" && r.URL.Path == "/products/":
		f.write(w, 200, page(f.products))
	case r.Method == "POST" && r.URL.Path == "/products/":
		p := created("prod")
		var prices []any
		for _, pr := range body["prices"].([]any) {
			price := pr.(map[string]any)
			if u, ok := price["unit_amount"].(string); ok {
				price["unit_amount"] = u + ".000000000000"
			}
			prices = append(prices, price)
		}
		p["prices"] = prices
		f.products = append(f.products, p)
		f.write(w, 201, p)
	case r.Method == "GET" && r.URL.Path == "/discounts/":
		f.write(w, 200, page(f.discounts))
	case r.Method == "POST" && r.URL.Path == "/discounts/":
		d := created("dsc")
		var prods []any
		for _, p := range body["products"].([]any) {
			prods = append(prods, map[string]any{"id": p})
		}
		d["products"] = prods
		f.discounts = append(f.discounts, d)
		f.write(w, 201, d)
	case r.Method == "GET" && r.URL.Path == "/webhooks/endpoints":
		f.write(w, 200, page(f.endpoints))
	case r.Method == "POST" && r.URL.Path == "/webhooks/endpoints":
		e := created("whe")
		e["secret"], e["enabled"] = "whsec_made", true
		f.endpoints = append(f.endpoints, e)
		f.write(w, 201, e)
	case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/webhooks/endpoints/"):
		// A rerun brings an endpoint's events and api_version up to date.
		for _, e := range f.endpoints {
			if m := e.(map[string]any); m["id"] == strings.TrimPrefix(r.URL.Path, "/webhooks/endpoints/") {
				for k, v := range body {
					m[k] = v
				}
				f.write(w, 200, m)
				return
			}
		}
		f.write(w, 404, map[string]any{"error": "ResourceNotFound", "detail": "Not found"})
	case r.Method == "POST" && r.URL.Path == "/events/ingest":
		for _, ev := range body["events"].([]any) {
			e := ev.(map[string]any)
			f.events++
			f.lastGB, _ = e["metadata"].(map[string]any)["gb"].(float64)
			f.lastCustomer, _ = e["external_customer_id"].(string)
		}
		f.write(w, 200, map[string]any{"inserted": 1, "duplicates": 0})
	default:
		f.write(w, 404, map[string]any{"error": "NotFound", "detail": r.Method + " " + r.URL.Path})
	}
}
