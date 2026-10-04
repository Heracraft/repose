package preview

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoute(t *testing.T) {
	cases := []struct {
		host  string
		ok    bool
		label string
		port  int
	}{
		{"3000-todo-app-heracraft.repose.herakraft.co", true, "todo-app-heracraft", 3000},
		{"8080-api-heracraft.repose.herakraft.co:443", true, "api-heracraft", 8080},
		{"3000-Api-Heracraft.repose.herakraft.co", true, "api-heracraft", 3000}, // hostnames are case-insensitive; lowercased
		{"todo-app.heracraft.repose.herakraft.co", false, "", 0},
		{"3000-todo-.repose.herakraft.co", false, "", 0},
		{"99999999-a-b.repose.herakraft.co", false, "", 0},
		{"0-a-b.repose.herakraft.co", false, "", 0},
		{"3000-a-b.example.com", false, "", 0},
	}
	for _, c := range cases {
		tgt, err := Route(c.host)
		if c.ok != (err == nil) {
			t.Errorf("%q: ok=%v err=%v", c.host, c.ok, err)
			continue
		}
		if c.ok && (tgt.Label != c.label || tgt.Port != c.port) {
			t.Errorf("%q: got %+v", c.host, tgt)
		}
	}
}

// TestRouteNeverSplitsSlugAndHandle pins I-438: two different projects,
// todo-app of hera-craft and todo-app-hera of craft, give the same host
// name under the old <slug>-<handle> form, so the parser must not claim
// either reading.
func TestRouteNeverSplitsSlugAndHandle(t *testing.T) {
	tgt, err := Route("3000-todo-app-hera-craft.repose.herakraft.co")
	if err != nil {
		t.Fatal(err)
	}
	if tgt != (Target{Port: 3000, Label: "todo-app-hera-craft"}) {
		t.Fatalf("got %+v", tgt)
	}
}

func TestAuthenticateNotImplemented(t *testing.T) {
	if _, err := Authenticate(httptest.NewRequest("GET", "/", nil)); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Authenticate err = %v, want ErrNotImplemented", err)
	}
}

func TestHandlerServesStubAndHealthz(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "3000-todo-app-heracraft.repose.herakraft.co"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("preview root = %d, want 503", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "not enabled yet") {
		t.Fatalf("stub page: %q", body)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
