package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

func newHookIngest(t *testing.T) (*HookIngest, *fakeapi.Fake, *obsmetrics.GatewayMetrics, string) {
	t.Helper()
	api := fakeapi.New(fakeapi.Options{})
	t.Cleanup(api.Close)
	p, err := api.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(api.URL(), nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	m := obsmetrics.NewGatewayMetrics(obsmetrics.New(obs.ComponentGateway))
	return NewHookIngest(client, nil, m), api, m, p.GuestIP
}

func post(t *testing.T, h *HookIngest, remoteAddr, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/hooks", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHookIngestForwardsFromGuestSource(t *testing.T) {
	h, _, m, guestIP := newHookIngest(t)
	rec := post(t, h, guestIP+":40000", `{"agent":"claude","kind":"completed","summary":"done"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("guest hook = %d (%s)", rec.Code, rec.Body.String())
	}
	if counterValue(t, m.HookEventsTotal.WithLabelValues(HookForwarded)) != 1 {
		t.Fatal("forwarded not counted")
	}
}

func TestHookIngestRejectsNonGuestSource(t *testing.T) {
	h, _, m, _ := newHookIngest(t)
	rec := post(t, h, "203.0.113.9:1234", `{"kind":"completed"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-guest source = %d, want 403", rec.Code)
	}
	if counterValue(t, m.HookEventsTotal.WithLabelValues(HookRejected)) != 1 {
		t.Fatal("rejected not counted")
	}
}

func TestHookIngestUnknownAddress(t *testing.T) {
	h, _, m, _ := newHookIngest(t)
	rec := post(t, h, "10.64.9.9:2000", `{"kind":"completed"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown guest = %d, want 404", rec.Code)
	}
	if counterValue(t, m.HookEventsTotal.WithLabelValues(HookNotFound)) != 1 {
		t.Fatal("not_found not counted")
	}
}

func TestHookIngestRateLimit(t *testing.T) {
	h, _, m, guestIP := newHookIngest(t)
	now := time.Unix(1_700_000_000, 0)
	h.clock = func() time.Time { return now }
	ok := 0
	for i := 0; i < 15; i++ {
		if post(t, h, guestIP+":40000", `{"kind":"completed"}`).Code == http.StatusAccepted {
			ok++
		}
	}
	if ok != hookRatePerSec {
		t.Fatalf("accepted %d in one second, want %d", ok, hookRatePerSec)
	}
	if counterValue(t, m.HookEventsTotal.WithLabelValues(HookRateLimited)) != float64(15-hookRatePerSec) {
		t.Fatalf("rate_limited count %v", counterValue(t, m.HookEventsTotal.WithLabelValues(HookRateLimited)))
	}
	// The next second the budget resets.
	now = now.Add(time.Second)
	if post(t, h, guestIP+":40000", `{"kind":"completed"}`).Code != http.StatusAccepted {
		t.Fatal("budget did not reset after a second")
	}
}

func TestHookIngestBodyCapAndValidation(t *testing.T) {
	h, _, _, guestIP := newHookIngest(t)
	if rec := post(t, h, guestIP+":40000", `{"kind":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty kind = %d, want 400", rec.Code)
	}
	// Platform kinds come from the api, never from a guest.
	for _, kind := range []string{"billing_stopped", "abuse_stopped", "notifications_paused", "agent_question", "agent.waiting"} {
		if rec := post(t, h, guestIP+":40000", `{"agent":"claude","kind":"`+kind+`"}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("kind %s = %d, want 400", kind, rec.Code)
		}
	}
	if rec := post(t, h, guestIP+":40000", `{"agent":"claude\r\nx","kind":"completed"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown agent = %d, want 400", rec.Code)
	}
	big := `{"kind":"completed","summary":"` + strings.Repeat("x", hookBodyCap*2) + `"}`
	if rec := post(t, h, guestIP+":40000", big); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d, want 400", rec.Code)
	}
}
