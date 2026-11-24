package api

import (
	"net/http"
	"testing"
)

// TestCheckoutField: the fake serves and refuses `checkout` as api.md
// documents it (I-1610), so the CLI's and the dashboard's tests can
// rely on it.
func TestCheckoutField(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "chk", "")
	if p.Checkout != nil {
		t.Fatalf("checkout %q before any CLI said", *p.Checkout)
	}
	for _, bad := range []string{"..", "a/b", "x y", "q?x"} {
		wantErr(t, call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"checkout": bad}), http.StatusBadRequest, "invalid")
	}
	if p = getProject(t, f, tok, p.ID); p.Checkout != nil {
		t.Fatalf("a refused checkout was stored: %q", *p.Checkout)
	}
	want(t, call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"checkout": "todo-app"}), http.StatusOK)
	if p = getProject(t, f, tok, p.ID); p.Checkout == nil || *p.Checkout != "todo-app" {
		t.Fatalf("checkout after patch: %v", p.Checkout)
	}
	want(t, call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"checkout": ""}), http.StatusOK)
	if p = getProject(t, f, tok, p.ID); p.Checkout == nil || *p.Checkout != "" {
		t.Fatalf("checkout after the empty patch: %v", p.Checkout)
	}
}
