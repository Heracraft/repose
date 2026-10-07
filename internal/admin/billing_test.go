package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/store"
)

// docs/workstreams/09-billing.md §9 (I-289): `repose-admin billing
// show|rollup|explain|suspend|unsuspend|overage-now|paddle-bootstrap`
// exist, print what the runbook reads, and write audit_log.
func TestBillingSubcommands(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	ctx := h.Ctx
	fake := newFakePaddle(t)
	admin.PaddleBaseURL = fake.URL
	t.Cleanup(func() { admin.PaddleBaseURL = "" })
	t.Setenv("PADDLE_API_KEY", "pdl_sdbx_apikey_test")
	t.Setenv("PADDLE_WEBHOOK_SECRET", "pdl_ntfset_test")
	t.Setenv("PADDLE_PRICE_SOLO", "pri_solo_test")
	t.Setenv("PADDLE_PRICE_PLUS", "pri_plus_test")
	t.Setenv("PADDLE_PRICE_PRO", "pri_pro_test")
	t.Setenv("PADDLE_PRODUCT_OVERAGE", "pro_overage_test")
	t.Setenv("PADDLE_DISCOUNT_INTRO", "dsc_intro_test")

	// A Solo account with a project, an hour of usage and 260 GB egress.
	uid, pid, gid := store.NewID(), store.NewID(), store.NewID()
	hour := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	periodStart := hour.AddDate(0, 0, -10)
	periodEnd := periodStart.AddDate(0, 1, 0)
	if _, err := h.Pool.Exec(ctx, `insert into users (id, handle, email, billing_status, has_card, paddle_customer_id, created_at)
		values ($1, 'payer', 'payer@example.test', 'active', true, 'ctm_payer', $2)`, uid, periodStart); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `insert into subscriptions (id, user_id, paddle_customer_id, plan, status, seats, period_start, period_end, next_billed_at)
		values ('sub_payer', $1, 'ctm_payer', 'solo', 'active', 1, $2, $3, $3)`, uid, periodStart, periodEnd); err != nil {
		t.Fatal(err)
	}
	fake.subs["sub_payer"] = true
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
	if err != nil || !strings.Contains(out, "sent 10 GB over = 50 cents to Paddle") {
		t.Fatalf("overage-now: %s %v", out, err)
	}
	if fake.charges != 1 {
		t.Fatalf("%d charges sent", fake.charges)
	}
	out, err = run(t, e, "billing", "overage-now", "payer")
	if err != nil || !strings.Contains(out, "already has its line") || fake.charges != 1 {
		t.Fatalf("overage-now again: %s %v (%d charges)", out, err, fake.charges)
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
	// paddle-bootstrap creates the catalog and prints the block, twice
	// the same; a live key is refused.
	out, err = run(t, e, "billing", "paddle-bootstrap", "--webhook-url", "https://api.test/v1/billing/webhook")
	if err != nil || !strings.Contains(out, "PADDLE_PRICE_SOLO=pri_") || !strings.Contains(out, "PADDLE_DISCOUNT_INTRO=dsc_") || !strings.Contains(out, "PADDLE_WEBHOOK_SECRET=") || strings.Contains(out, "pdl_sdbx_apikey_test") {
		t.Fatalf("bootstrap: %s %v", out, err)
	}
	again, err := run(t, e, "billing", "paddle-bootstrap", "--webhook-url", "https://api.test/v1/billing/webhook")
	if err != nil || !strings.Contains(again, "created 0 object(s), found 9") {
		t.Fatalf("bootstrap rerun: %s %v", again, err)
	}
	t.Setenv("PADDLE_API_KEY", "pdl_live_apikey_test")
	if out, err := run(t, e, "billing", "paddle-bootstrap", "--no-webhook"); err == nil || !strings.Contains(err.Error(), "--live") {
		t.Fatalf("live key without --live: %s %v", out, err)
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

// fakePaddle answers the calls the admin commands make: the catalog for
// the bootstrap and the charge for overage-now.
type fakePaddle struct {
	URL      string
	subs     map[string]bool
	products map[string]map[string]any
	prices   map[string]map[string]any
	settings map[string]map[string]any
	discount map[string]map[string]any
	charges  int
	seq      int
}

func newFakePaddle(t *testing.T) *fakePaddle {
	f := &fakePaddle{subs: map[string]bool{}, products: map[string]map[string]any{}, prices: map[string]map[string]any{}, settings: map[string]map[string]any{}, discount: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

func (f *fakePaddle) write(w http.ResponseWriter, status int, data any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (f *fakePaddle) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.seq++
	id := func(p string) string { return fmt.Sprintf("%s_a%04d", p, f.seq) }
	list := func(m map[string]map[string]any) []any {
		out := []any{}
		for _, v := range m {
			out = append(out, v)
		}
		return out
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/products":
		f.write(w, 200, list(f.products))
	case r.Method == "POST" && r.URL.Path == "/products":
		p := map[string]any{"id": id("pro"), "name": body["name"], "status": "active", "custom_data": body["custom_data"]}
		f.products[p["id"].(string)] = p
		f.write(w, 201, p)
	case r.Method == "GET" && r.URL.Path == "/prices":
		out := []any{}
		for _, p := range f.prices {
			if p["product_id"] == r.URL.Query().Get("product_id") {
				out = append(out, p)
			}
		}
		f.write(w, 200, out)
	case r.Method == "POST" && r.URL.Path == "/prices":
		p := map[string]any{"id": id("pri"), "product_id": body["product_id"], "status": "active", "unit_price": body["unit_price"], "custom_data": body["custom_data"]}
		f.prices[p["id"].(string)] = p
		f.write(w, 201, p)
	case r.Method == "GET" && r.URL.Path == "/discounts":
		f.write(w, 200, list(f.discount))
	case r.Method == "POST" && r.URL.Path == "/discounts":
		d := map[string]any{"id": id("dsc"), "status": "active", "restrict_to": body["restrict_to"], "custom_data": body["custom_data"]}
		f.discount[d["id"].(string)] = d
		f.write(w, 201, d)
	case r.Method == "GET" && r.URL.Path == "/notification-settings":
		f.write(w, 200, list(f.settings))
	case r.Method == "POST" && r.URL.Path == "/notification-settings":
		s := map[string]any{"id": id("ntfset"), "type": "url", "destination": body["destination"], "endpoint_secret_key": "pdl_ntfset_made", "subscribed_events": []any{}}
		f.settings[s["id"].(string)] = s
		f.write(w, 201, s)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/subscriptions/") && strings.HasSuffix(r.URL.Path, "/charge"):
		sub := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/subscriptions/"), "/charge")
		if !f.subs[sub] {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "entity_not_found", "detail": "no subscription"}})
			return
		}
		f.charges++
		f.write(w, 200, map[string]any{"id": sub, "status": "active"})
	default:
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "not_found", "detail": r.Method + " " + r.URL.Path}})
	}
}
