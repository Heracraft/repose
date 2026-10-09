package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/billing"
)

// fakePolar is the slice of Polar's API the /billing routes reach:
// checkouts, subscriptions (patch, revoke), event ingestion, customer
// sessions and orders, with a signer for webhooks. The billing package
// has the full fake; this one is enough to drive the routes.
type fakePolar struct {
	t      *testing.T
	srv    *httptest.Server
	cfg    billing.Config
	secret string
	mu     sync.Mutex
	seq    int
	bodies map[string]map[string]any
	counts map[string]int
	subs   map[string]map[string]any
	orders map[string]map[string]any
	custs  map[string]bool
	evSeq  int
}

func newFakePolar(t *testing.T) *fakePolar {
	t.Helper()
	f := &fakePolar{t: t, secret: "whsec_cm91dGUtdGVzdC1zZWNyZXQ=", bodies: map[string]map[string]any{}, counts: map[string]int{},
		subs: map[string]map[string]any{}, orders: map[string]map[string]any{}, custs: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	f.cfg = billing.Config{AccessToken: "polar_oat_route_test", Env: billing.EnvSandbox, WebhookSecret: f.secret,
		ProductSolo: "prod_solo_test", ProductPlus: "prod_plus_test", ProductPro: "prod_pro_test",
		DashboardURL: "https://repose.herakraft.co", BaseURL: f.srv.URL, Enforce: true}
	return f
}

func (f *fakePolar) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s_r%06d", prefix, f.seq)
}

// sign returns the Standard Webhooks headers for an event body.
func (f *fakePolar) sign(id string, body []byte) map[string]string {
	h := billing.Sign(f.secret, id, time.Now(), body)
	return map[string]string{"webhook-id": h.ID, "webhook-timestamp": h.Timestamp, "webhook-signature": h.Signature}
}

func (f *fakePolar) body(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func (f *fakePolar) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[key]
}

// addSubscription makes a subscription for a user, as a completed
// checkout does; Polar then knows the user as a customer.
func (f *fakePolar) addSubscription(userID, customer, product, status string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("sub")
	f.subs[id] = f.subDataLocked(id, userID, customer, product, status)
	f.custs[userID] = true
	return id
}

func (f *fakePolar) addOrder(userID, sub, status string, total, tax int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("ord")
	f.orders[id] = map[string]any{"id": id, "status": status, "external_customer_id": userID, "subscription_id": sub, "currency": "usd", "invoice_number": "R-1",
		"created_at": "2026-10-01T00:00:00Z", "is_invoice_generated": true, "net_amount": total - tax, "tax_amount": tax, "total_amount": total}
	return id
}

func (f *fakePolar) subData(id, userID, customer, product, status string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subDataLocked(id, userID, customer, product, status)
}

func (f *fakePolar) subDataLocked(id, userID, customer, product, status string) map[string]any {
	return map[string]any{"id": id, "status": status, "customer_id": customer, "product_id": product,
		"current_period_start": "2026-10-01T00:00:00Z", "current_period_end": "2026-11-01T00:00:00Z",
		"trial_start": "2026-10-01T00:00:00Z", "trial_end": "2026-10-08T00:00:00Z", "cancel_at_period_end": false,
		"metadata": map[string]any{"user_id": userID}, "customer": map[string]any{"id": customer, "external_id": userID}}
}

// event is a webhook body and the id it is delivered under.
func (f *fakePolar) event(kind string, data map[string]any) (string, []byte) {
	f.evSeq++
	b, _ := json.Marshal(map[string]any{"type": kind, "timestamp": time.Now().UTC().Format(time.RFC3339), "api_version": billing.APIVersion, "data": data})
	return fmt.Sprintf("msg_r%06d", f.evSeq), b
}

func (f *fakePolar) write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (f *fakePolar) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.bodies[key] = body
	f.counts[key]++
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == "POST" && r.URL.Path == "/checkouts/":
		id := f.id("chk")
		f.write(w, 201, map[string]any{"id": id, "status": "open", "url": "https://sandbox.polar.sh/checkout/" + id})
	case r.Method == "POST" && r.URL.Path == "/customer-sessions/":
		ext, _ := body["external_customer_id"].(string)
		if !f.custs[ext] {
			f.write(w, 404, map[string]any{"error": "ResourceNotFound", "detail": "Customer does not exist."})
			return
		}
		f.write(w, 201, map[string]any{"customer_portal_url": "https://sandbox.polar.sh/fake/portal?customer_session_token=polar_cst_r"})
	case r.Method == "POST" && r.URL.Path == "/events/ingest":
		f.write(w, 200, map[string]any{"inserted": 1, "duplicates": 0})
	case r.Method == "GET" && r.URL.Path == "/orders/":
		var out []any
		for _, o := range f.orders {
			if o["external_customer_id"] == r.URL.Query().Get("external_customer_id") {
				out = append(out, o)
			}
		}
		if out == nil {
			out = []any{}
		}
		f.write(w, 200, map[string]any{"items": out, "pagination": map[string]any{"total_count": len(out), "max_page": 1}})
	case r.Method == "GET" && len(parts) == 3 && parts[0] == "orders" && parts[2] == "invoice":
		f.write(w, 200, map[string]any{"url": "https://invoices.fake/" + parts[1] + ".pdf"})
	case len(parts) == 2 && parts[0] == "subscriptions":
		s, ok := f.subs[parts[1]]
		if !ok {
			f.write(w, 404, map[string]any{"error": "ResourceNotFound", "detail": "Not found"})
			return
		}
		switch r.Method {
		case "DELETE":
			s["status"] = "canceled"
		case "PATCH":
			if pid, ok := body["product_id"].(string); ok {
				if body["proration_behavior"] == "next_period" {
					s["pending_update"] = map[string]any{"product_id": pid}
				} else {
					s["product_id"] = pid
				}
			}
			if c, ok := body["cancel_at_period_end"].(bool); ok {
				s["cancel_at_period_end"] = c
				s["ends_at"] = nil
				if c {
					s["ends_at"] = s["current_period_end"]
				}
			}
		}
		f.write(w, 200, s)
	default:
		f.write(w, 404, map[string]any{"error": "NotFound", "detail": key})
	}
}
