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

// fakePolar is an httptest.Server that speaks enough of the Polar API for
// every call the api makes: checkouts, subscriptions (patch, revoke),
// event ingestion, customer sessions, orders and their invoices, and the
// organization, meters, products, discounts and webhook endpoints the
// bootstrap reads and creates. It signs webhooks with the same scheme the
// api verifies, so tests post events through the real handler. Every
// request is recorded for assertions on what was sent.
type fakePolar struct {
	srv    *httptest.Server
	secret string
	mu     sync.Mutex
	seq    int
	// Requests is every call as "METHOD /path".
	Requests []string
	// Bodies is the decoded JSON body of each request, by "METHOD /path".
	Bodies    map[string][]map[string]any
	org       map[string]any
	meters    map[string]map[string]any
	products  map[string]map[string]any
	discounts map[string]map[string]any
	endpoints map[string]map[string]any
	subs      map[string]map[string]any
	orders    map[string]map[string]any
	customers map[string]bool
	events    map[string]map[string]any
	checkouts map[string]map[string]any
	// Fail makes the next request matching "METHOD /path" answer FailCode,
	// that many times.
	Fail     map[string]int
	FailCode int
	// RetryAfter is put on 429 answers.
	RetryAfter string
	// TokenSeen is the last Authorization header's token, Version the last
	// Polar-Version header.
	TokenSeen string
	Version   string
}

func newFakePolar() *fakePolar {
	f := &fakePolar{secret: "whsec_" + "c2VjcmV0LWZvci10aGUtZmFrZS1wb2xhcg==", Bodies: map[string][]map[string]any{},
		meters: map[string]map[string]any{}, products: map[string]map[string]any{}, discounts: map[string]map[string]any{},
		endpoints: map[string]map[string]any{}, subs: map[string]map[string]any{}, orders: map[string]map[string]any{},
		customers: map[string]bool{}, events: map[string]map[string]any{}, checkouts: map[string]map[string]any{},
		Fail: map[string]int{}, FailCode: 500}
	f.org = map[string]any{"id": "org_fake", "slug": "repose-fake",
		"subscription_settings":    map[string]any{"allow_multiple_subscriptions": true, "proration_behavior": "prorate", "benefit_revocation_grace_period": 0, "prevent_trial_abuse": false, "allow_customer_updates": true},
		"customer_portal_settings": map[string]any{"usage": map[string]any{"show": true}, "subscription": map[string]any{"update_seats": true, "update_plan": true}, "customer": map[string]any{"allow_email_change": false}},
		"customer_email_settings":  map[string]any{"order_confirmation": true, "subscription_past_due": true, "subscription_trial_conversion_reminder": true, "subscription_cancellation": true, "subscription_revoked": true, "subscription_updated": true, "subscription_renewal_reminder": true}}
	// The discount testConfig names, as an operator's earlier bootstrap
	// left it; no metadata, so a bootstrap here makes its own.
	f.discounts["dsc_intro_test"] = map[string]any{"id": "dsc_intro_test", "type": "fixed", "products": []any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakePolar) Close() { f.srv.Close() }

// URL is the base URL for Config.BaseURL.
func (f *fakePolar) URL() string { return f.srv.URL }

func (f *fakePolar) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s_%08d", prefix, f.seq)
}

// Sign signs a webhook body as Polar would, at ts, under event id id.
func (f *fakePolar) Sign(id string, body []byte, ts time.Time) billing.WebhookHeaders {
	return billing.Sign(f.secret, id, ts, body)
}

// Secret is the endpoint secret the api must be configured with.
func (f *fakePolar) Secret() string { return f.secret }

// Count is how many requests matched "METHOD /path".
func (f *fakePolar) Count(key string) int {
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
func (f *fakePolar) Sub(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[id]
}

// AddSubscription seeds a subscription so PATCH and DELETE have a target.
func (f *fakePolar) AddSubscription(id, customerID, productID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id] = map[string]any{"id": id, "status": status, "customer_id": customerID, "product_id": productID, "currency": "usd",
		"current_period_start": "2026-10-01T00:00:00Z", "current_period_end": "2026-11-01T00:00:00Z", "cancel_at_period_end": false,
		"ends_at": nil, "pending_update": nil, "metadata": map[string]any{}}
}

// AddCustomer makes a customer known by external id, as a completed
// checkout does.
func (f *fakePolar) AddCustomer(externalID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.customers[externalID] = true
}

// AddOrder seeds an order for the listing.
func (f *fakePolar) AddOrder(externalID, subID, status string, total, tax int64, generated bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("ord")
	f.orders[id] = map[string]any{"id": id, "status": status, "paid": status == "paid", "external_customer_id": externalID, "subscription_id": subID,
		"currency": "usd", "billing_reason": "subscription_cycle", "invoice_number": "REPOSE-" + id[len(id)-4:], "is_invoice_generated": generated,
		"created_at": "2026-10-01T00:00:00Z", "subtotal_amount": total - tax, "discount_amount": 0, "net_amount": total - tax, "tax_amount": tax, "total_amount": total}
	return id
}

// Events is every ingested event, by external id.
func (f *fakePolar) Events() map[string]map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]map[string]any{}
	for k, v := range f.events {
		out[k] = v
	}
	return out
}

func (f *fakePolar) write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (f *fakePolar) writeErr(w http.ResponseWriter, status int, typ, detail string) {
	f.write(w, status, map[string]any{"error": typ, "detail": detail})
}

func listOf(items []any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"items": items, "pagination": map[string]any{"total_count": len(items), "max_page": 1}}
}

func (f *fakePolar) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TokenSeen = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.Version = r.Header.Get("Polar-Version")
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
		f.writeErr(w, f.FailCode, "FakeFailure", "the fake was told to fail")
		return
	}
	if f.TokenSeen == "" {
		f.writeErr(w, 401, "Unauthorized", "no bearer token")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	q := r.URL.Query()
	switch {
	case r.Method == "POST" && r.URL.Path == "/checkouts/":
		products, _ := body["products"].([]any)
		if len(products) != 1 {
			f.write(w, 422, map[string]any{"detail": []any{map[string]any{"loc": []any{"body", "products"}, "msg": "one product", "type": "value_error"}}})
			return
		}
		if d, ok := body["discount_id"].(string); ok {
			if _, known := f.discounts[d]; !known {
				f.write(w, 422, map[string]any{"detail": []any{map[string]any{"loc": []any{"body", "discount_id"}, "msg": "Discount does not exist.", "type": "value_error"}}})
				return
			}
		}
		c := map[string]any{"id": f.id("chk"), "status": "open"}
		c["url"] = "https://sandbox.polar.sh/checkout/" + c["id"].(string)
		for k, v := range body {
			c[k] = v
		}
		f.checkouts[c["id"].(string)] = c
		f.write(w, 201, c)
	case len(parts) == 2 && parts[0] == "subscriptions":
		s, ok := f.subs[parts[1]]
		if !ok {
			f.writeErr(w, 404, "ResourceNotFound", "Not found")
			return
		}
		switch r.Method {
		case "GET":
			f.write(w, 200, s)
		case "DELETE":
			if s["status"] == "canceled" {
				f.writeErr(w, 403, "AlreadyCanceledSubscription", "This subscription is already canceled.")
				return
			}
			s["status"] = "canceled"
			s["ended_at"] = time.Now().UTC().Format(time.RFC3339)
			f.write(w, 200, s)
		case "PATCH":
			if pid, ok := body["product_id"].(string); ok {
				if body["proration_behavior"] == "next_period" {
					s["pending_update"] = map[string]any{"product_id": pid, "applies_at": s["current_period_end"]}
				} else {
					s["product_id"] = pid
					s["pending_update"] = nil
				}
			}
			if d, present := body["discount_id"]; present && d == nil {
				s["discount_id"] = nil
			}
			if pu, present := body["pending_update"]; present && pu == nil {
				s["pending_update"] = nil
			}
			if c, ok := body["cancel_at_period_end"].(bool); ok {
				if s["status"] == "canceled" {
					f.writeErr(w, 403, "AlreadyCanceledSubscription", "This subscription is already canceled.")
					return
				}
				s["cancel_at_period_end"] = c
				if c {
					s["ends_at"] = s["current_period_end"]
				} else {
					s["ends_at"] = nil
				}
			}
			f.write(w, 200, s)
		default:
			f.writeErr(w, 405, "MethodNotAllowed", "no")
		}
	case r.Method == "POST" && r.URL.Path == "/events/ingest":
		evs, _ := body["events"].([]any)
		ins, dup := 0, 0
		for _, e := range evs {
			ev := e.(map[string]any)
			id, _ := ev["external_id"].(string)
			if _, seen := f.events[id]; seen && id != "" {
				dup++
				continue
			}
			f.events[id] = ev
			ins++
		}
		f.write(w, 200, map[string]any{"inserted": ins, "duplicates": dup})
	case r.Method == "POST" && r.URL.Path == "/customer-sessions/":
		ext, _ := body["external_customer_id"].(string)
		if !f.customers[ext] {
			f.writeErr(w, 404, "ResourceNotFound", "Customer does not exist.")
			return
		}
		f.write(w, 201, map[string]any{"token": "polar_cst_fake", "customer_portal_url": "https://sandbox.polar.sh/repose-fake/portal?customer_session_token=polar_cst_fake"})
	case r.Method == "GET" && r.URL.Path == "/orders/":
		var out []any
		for _, o := range f.orders {
			if ext := q.Get("external_customer_id"); ext != "" && o["external_customer_id"] != ext {
				continue
			}
			out = append(out, o)
		}
		f.write(w, 200, listOf(out))
	case len(parts) == 3 && parts[0] == "orders" && parts[2] == "invoice":
		o, ok := f.orders[parts[1]]
		if !ok {
			f.writeErr(w, 404, "ResourceNotFound", "Not found")
			return
		}
		if r.Method == "POST" {
			o["is_invoice_generated"] = true
			f.write(w, 202, nil)
			return
		}
		if o["is_invoice_generated"] != true {
			f.writeErr(w, 404, "InvoiceDoesNotExist", "Invoice does not exist.")
			return
		}
		f.write(w, 200, map[string]any{"url": "https://invoices.fake/" + parts[1] + ".pdf"})
	case r.Method == "GET" && r.URL.Path == "/organizations/":
		f.write(w, 200, listOf([]any{f.org}))
	case r.Method == "PATCH" && len(parts) == 2 && parts[0] == "organizations":
		for _, k := range []string{"subscription_settings", "customer_portal_settings", "customer_email_settings", "default_tax_behavior"} {
			if v, ok := body[k]; ok {
				f.org[k] = v
			}
		}
		f.write(w, 200, f.org)
	case r.Method == "GET" && r.URL.Path == "/meters/":
		var out []any
		for _, m := range f.meters {
			out = append(out, m)
		}
		f.write(w, 200, listOf(out))
	case r.Method == "POST" && r.URL.Path == "/meters/":
		m := map[string]any{"id": f.id("mtr")}
		for k, v := range body {
			m[k] = v
		}
		f.meters[m["id"].(string)] = m
		f.write(w, 201, m)
	case r.Method == "GET" && r.URL.Path == "/products/":
		var out []any
		for _, p := range f.products {
			out = append(out, p)
		}
		f.write(w, 200, listOf(out))
	case r.Method == "POST" && r.URL.Path == "/products/":
		p := map[string]any{"id": f.id("prod"), "is_archived": false}
		for k, v := range body {
			p[k] = v
		}
		var prices []any
		for _, pr := range body["prices"].([]any) {
			price := map[string]any{"id": f.id("price"), "is_archived": false}
			for k, v := range pr.(map[string]any) {
				price[k] = v
			}
			if u, ok := price["unit_amount"].(string); ok {
				price["unit_amount"] = u + ".000000000000"
			}
			prices = append(prices, price)
		}
		p["prices"] = prices
		f.products[p["id"].(string)] = p
		f.write(w, 201, p)
	case r.Method == "GET" && r.URL.Path == "/discounts/":
		var out []any
		for _, d := range f.discounts {
			out = append(out, d)
		}
		f.write(w, 200, listOf(out))
	case r.Method == "POST" && r.URL.Path == "/discounts/":
		d := map[string]any{"id": f.id("dsc")}
		for k, v := range body {
			d[k] = v
		}
		var prods []any
		for _, id := range body["products"].([]any) {
			prods = append(prods, map[string]any{"id": id})
		}
		d["products"] = prods
		f.discounts[d["id"].(string)] = d
		f.write(w, 201, d)
	case r.Method == "GET" && r.URL.Path == "/webhooks/endpoints":
		var out []any
		for _, e := range f.endpoints {
			out = append(out, e)
		}
		f.write(w, 200, listOf(out))
	case r.Method == "POST" && r.URL.Path == "/webhooks/endpoints":
		e := map[string]any{"id": f.id("whe"), "secret": f.secret, "enabled": true}
		for k, v := range body {
			e[k] = v
		}
		f.endpoints[e["id"].(string)] = e
		f.write(w, 201, e)
	case r.Method == "PATCH" && len(parts) == 3 && parts[0] == "webhooks" && parts[1] == "endpoints":
		e, ok := f.endpoints[parts[2]]
		if !ok {
			f.writeErr(w, 404, "ResourceNotFound", "Not found")
			return
		}
		for k, v := range body {
			e[k] = v
		}
		f.write(w, 200, e)
	default:
		f.writeErr(w, 404, "NotFound", "no such route: "+key)
	}
}
