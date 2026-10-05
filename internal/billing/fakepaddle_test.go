package billing_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/billing"
)

// fakePaddle is an httptest.Server that speaks enough of the Paddle
// Billing API for every call the api makes: customers, transactions,
// subscriptions (charge, cancel, patch), portal sessions, the transaction
// list, invoice PDFs, and the catalog and notification settings the
// bootstrap creates. It signs webhooks with the same scheme the api
// verifies, so tests post events through the real route. Every request
// is recorded for assertions on what was sent.
type fakePaddle struct {
	srv    *httptest.Server
	secret string
	mu     sync.Mutex
	seq    int
	// Requests is every call as "METHOD /path".
	Requests []string
	// Bodies is the decoded JSON body of each request, by "METHOD /path".
	Bodies    map[string][]map[string]any
	customers map[string]map[string]any
	products  map[string]map[string]any
	prices    map[string]map[string]any
	subs      map[string]map[string]any
	txns      map[string]map[string]any
	settings  map[string]map[string]any
	discounts map[string]map[string]any
	// Fail makes the next request matching "METHOD /path" answer status
	// with a Paddle error, that many times.
	Fail      map[string]int
	FailCode  int
	FailTimes int
	// RetryAfter is put on 429 answers.
	RetryAfter string
	// KeySeen is the last Authorization header's token.
	KeySeen string
	Version string
}

func newFakePaddle() *fakePaddle {
	f := &fakePaddle{secret: "pdl_ntfset_secret_test", Bodies: map[string][]map[string]any{}, customers: map[string]map[string]any{},
		products: map[string]map[string]any{}, prices: map[string]map[string]any{}, subs: map[string]map[string]any{}, txns: map[string]map[string]any{},
		settings: map[string]map[string]any{}, discounts: map[string]map[string]any{}, Fail: map[string]int{}, FailCode: 500}
	// The discount testConfig names, as an operator's earlier bootstrap
	// left it; no custom_data, so a bootstrap here makes its own.
	f.discounts["dsc_intro_test"] = map[string]any{"id": "dsc_intro_test", "status": "active"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakePaddle) Close() { f.srv.Close() }

// URL is the base URL for Config.BaseURL.
func (f *fakePaddle) URL() string { return f.srv.URL }

func (f *fakePaddle) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s_%08d", prefix, f.seq)
}

// Sign signs a webhook body as Paddle would, at ts.
func (f *fakePaddle) Sign(body []byte, ts time.Time) string { return billing.Sign(f.secret, ts, body) }

// Secret is the endpoint secret the api must be configured with.
func (f *fakePaddle) Secret() string { return f.secret }

// Count is how many requests matched "METHOD /path".
func (f *fakePaddle) Count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.Requests {
		if r == key {
			n++
		}
	}
	return n
}

// Sub returns a subscription as the fake holds it.
func (f *fakePaddle) Sub(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[id]
}

// AddSubscription seeds a subscription so PATCH/cancel/charge have a
// target; returns its id.
func (f *fakePaddle) AddSubscription(customerID, priceID, status string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("sub")
	f.subs[id] = map[string]any{"id": id, "status": status, "customer_id": customerID, "currency_code": "USD",
		"next_billed_at": "2026-11-01T00:00:00Z", "current_billing_period": map[string]any{"starts_at": "2026-10-01T00:00:00Z", "ends_at": "2026-11-01T00:00:00Z"},
		"scheduled_change": nil, "items": []any{map[string]any{"status": "active", "quantity": 1, "price": map[string]any{"id": priceID, "product_id": "pro_plan"}}}}
	return id
}

// AddTransaction seeds a transaction for the list.
func (f *fakePaddle) AddTransaction(customerID, subID, status string, total, tax int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("txn")
	f.txns[id] = map[string]any{"id": id, "status": status, "customer_id": customerID, "subscription_id": subID, "currency_code": "USD", "origin": "subscription_recurring",
		"invoice_id": "inv_" + id, "invoice_number": "1234-" + id[len(id)-4:], "created_at": "2026-10-01T00:00:00Z", "billed_at": "2026-10-01T00:00:00Z",
		"billing_period": map[string]any{"starts_at": "2026-10-01T00:00:00Z", "ends_at": "2026-11-01T00:00:00Z"},
		"details":        map[string]any{"totals": map[string]any{"subtotal": fmt.Sprint(total - tax), "tax": fmt.Sprint(tax), "total": fmt.Sprint(total), "grand_total": fmt.Sprint(total)}}}
	return id
}

func (f *fakePaddle) write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": map[string]any{"request_id": "req_fake"}})
}

func (f *fakePaddle) writeErr(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "request_error", "code": code, "detail": detail}, "meta": map[string]any{"request_id": "req_fake"}})
}

func (f *fakePaddle) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.KeySeen = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.Version = r.Header.Get("Paddle-Version")
	key := r.Method + " " + r.URL.Path
	f.Requests = append(f.Requests, key)
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	f.Bodies[key] = append(f.Bodies[key], body)
	if n := f.Fail[key]; n > 0 {
		f.Fail[key] = n - 1
		if f.FailCode == 429 && f.RetryAfter != "" {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		f.writeErr(w, f.FailCode, "fake_failure", "the fake was told to fail")
		return
	}
	if f.KeySeen == "" {
		f.writeErr(w, 403, "authentication_missing", "no bearer token")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == "GET" && r.URL.Path == "/customers":
		var out []any
		for _, c := range f.customers {
			if e := r.URL.Query().Get("email"); e == "" || strings.EqualFold(e, c["email"].(string)) {
				out = append(out, c)
			}
		}
		f.write(w, 200, out)
	case r.Method == "POST" && r.URL.Path == "/customers":
		email, _ := body["email"].(string)
		for _, c := range f.customers {
			if strings.EqualFold(c["email"].(string), email) {
				f.writeErr(w, 409, "customer_already_exists", "customer email conflicts with customer of id "+c["id"].(string))
				return
			}
		}
		c := map[string]any{"id": f.id("ctm"), "email": email, "status": "active", "custom_data": body["custom_data"]}
		f.customers[c["id"].(string)] = c
		f.write(w, 201, c)
	case r.Method == "POST" && len(parts) == 3 && parts[0] == "customers" && parts[2] == "portal-sessions":
		if _, ok := f.customers[parts[1]]; !ok {
			f.writeErr(w, 404, "entity_not_found", "customer not found")
			return
		}
		var subs []any
		if ids, ok := body["subscription_ids"].([]any); ok {
			for _, id := range ids {
				subs = append(subs, map[string]any{"id": id, "cancel_subscription": "https://portal.fake/cancel/" + id.(string), "update_subscription_payment_method": "https://portal.fake/payment/" + id.(string)})
			}
		}
		f.write(w, 201, map[string]any{"id": f.id("pts"), "customer_id": parts[1], "urls": map[string]any{"general": map[string]any{"overview": "https://portal.fake/overview/" + parts[1]}, "subscriptions": subs}})
	case r.Method == "POST" && r.URL.Path == "/transactions":
		items, _ := body["items"].([]any)
		if len(items) == 0 {
			f.writeErr(w, 400, "bad_request", "items required")
			return
		}
		t := map[string]any{"id": f.id("txn"), "status": "ready", "customer_id": body["customer_id"], "custom_data": body["custom_data"], "items": items, "currency_code": "USD", "created_at": time.Now().UTC().Format(time.RFC3339)}
		if d, ok := body["discount_id"].(string); ok {
			if _, known := f.discounts[d]; !known {
				f.writeErr(w, 400, "transaction_discount_not_found", "no such discount")
				return
			}
			t["discount_id"] = d
		}
		f.txns[t["id"].(string)] = t
		f.write(w, 201, t)
	case r.Method == "GET" && r.URL.Path == "/transactions":
		var out []any
		statuses := strings.Split(r.URL.Query().Get("status"), ",")
		for _, t := range f.txns {
			if c := r.URL.Query().Get("customer_id"); c != "" && t["customer_id"] != c {
				continue
			}
			ok := r.URL.Query().Get("status") == ""
			for _, s := range statuses {
				if s == t["status"] {
					ok = true
				}
			}
			if ok {
				out = append(out, t)
			}
		}
		f.write(w, 200, out)
	case r.Method == "GET" && len(parts) == 3 && parts[0] == "transactions" && parts[2] == "invoice":
		if _, ok := f.txns[parts[1]]; !ok {
			f.writeErr(w, 404, "entity_not_found", "transaction not found")
			return
		}
		f.write(w, 200, map[string]any{"url": "https://invoices.fake/" + parts[1] + ".pdf"})
	case len(parts) >= 2 && parts[0] == "subscriptions":
		s, ok := f.subs[parts[1]]
		if !ok {
			f.writeErr(w, 404, "entity_not_found", "subscription not found")
			return
		}
		switch {
		case r.Method == "GET" && len(parts) == 2:
			f.write(w, 200, s)
		case r.Method == "PATCH" && len(parts) == 2:
			if items, ok := body["items"].([]any); ok && len(items) > 0 {
				pid, _ := items[0].(map[string]any)["price_id"].(string)
				s["items"] = []any{map[string]any{"status": "active", "quantity": 1, "price": map[string]any{"id": pid, "product_id": "pro_plan"}}}
			}
			if sc, present := body["scheduled_change"]; present && sc == nil {
				s["scheduled_change"] = nil
			}
			f.write(w, 200, s)
		case r.Method == "POST" && len(parts) == 3 && parts[2] == "cancel":
			if body["effective_from"] == "immediately" {
				s["status"] = "canceled"
				s["canceled_at"] = time.Now().UTC().Format(time.RFC3339)
			} else {
				s["scheduled_change"] = map[string]any{"action": "cancel", "effective_at": "2026-11-01T00:00:00Z"}
			}
			f.write(w, 200, s)
		case r.Method == "POST" && len(parts) == 3 && parts[2] == "charge":
			if s["status"] == "canceled" {
				f.writeErr(w, 400, "subscription_locked_processing", "cannot charge a canceled subscription")
				return
			}
			out := map[string]any{}
			for k, v := range s {
				out[k] = v
			}
			if body["effective_from"] == "immediately" {
				t := map[string]any{"id": f.id("txn"), "status": "completed", "customer_id": s["customer_id"], "subscription_id": s["id"], "currency_code": "USD", "origin": "subscription_charge", "items": body["items"]}
				f.txns[t["id"].(string)] = t
				out["immediate_transaction"] = map[string]any{"id": t["id"]}
			} else {
				out["next_transaction"] = map[string]any{"billing_period": s["current_billing_period"]}
			}
			f.write(w, 200, out)
		default:
			f.writeErr(w, 404, "not_found", "no such route")
		}
	case r.Method == "GET" && r.URL.Path == "/products":
		var out []any
		for _, p := range f.products {
			out = append(out, p)
		}
		f.write(w, 200, out)
	case r.Method == "POST" && r.URL.Path == "/products":
		p := map[string]any{"id": f.id("pro"), "name": body["name"], "status": "active", "tax_category": body["tax_category"], "custom_data": body["custom_data"]}
		f.products[p["id"].(string)] = p
		f.write(w, 201, p)
	case r.Method == "GET" && r.URL.Path == "/prices":
		var out []any
		for _, p := range f.prices {
			if pid := r.URL.Query().Get("product_id"); pid == "" || p["product_id"] == pid {
				out = append(out, p)
			}
		}
		f.write(w, 200, out)
	case r.Method == "POST" && r.URL.Path == "/prices":
		p := map[string]any{"id": f.id("pri"), "product_id": body["product_id"], "description": body["description"], "status": "active", "unit_price": body["unit_price"],
			"billing_cycle": body["billing_cycle"], "trial_period": body["trial_period"], "custom_data": body["custom_data"]}
		f.prices[p["id"].(string)] = p
		f.write(w, 201, p)
	case r.Method == "GET" && r.URL.Path == "/discounts":
		var out []any
		for _, d := range f.discounts {
			out = append(out, d)
		}
		f.write(w, 200, out)
	case r.Method == "POST" && r.URL.Path == "/discounts":
		d := map[string]any{"id": f.id("dsc"), "status": "active"}
		for _, k := range []string{"description", "type", "amount", "currency_code", "enabled_for_checkout", "recur", "maximum_recurring_intervals", "restrict_to", "custom_data"} {
			d[k] = body[k]
		}
		f.discounts[d["id"].(string)] = d
		f.write(w, 201, d)
	case r.Method == "GET" && r.URL.Path == "/notification-settings":
		var out []any
		for _, s := range f.settings {
			out = append(out, s)
		}
		f.write(w, 200, out)
	case r.Method == "POST" && r.URL.Path == "/notification-settings":
		var evs []any
		if names, ok := body["subscribed_events"].([]any); ok {
			for _, n := range names {
				evs = append(evs, map[string]any{"name": n})
			}
		}
		s := map[string]any{"id": f.id("ntfset"), "description": body["description"], "type": body["type"], "destination": body["destination"], "active": true,
			"endpoint_secret_key": f.secret, "subscribed_events": evs}
		f.settings[s["id"].(string)] = s
		f.write(w, 201, s)
	default:
		f.writeErr(w, 404, "not_found", "no such route: "+key)
	}
}
