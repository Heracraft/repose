package httpapi_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// TestPatchCheckout is I-1610: a project's checkout is null until the
// CLI names it, PATCH stores a valid name or "" (no checkout) and
// refuses anything else without writing, and GET serves what was stored.
func TestPatchCheckout(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-chk", "chk")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(e.h.Ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.CreateRunning(u, "chk-box")
	path := "/projects/" + p.ID.String()
	get := func() any {
		t.Helper()
		res := e.do(t, tok, "GET", path, nil)
		if res.status != 200 {
			t.Fatalf("get: %d %s", res.status, res.raw)
		}
		v, ok := res.body["checkout"]
		if !ok {
			t.Fatalf("no checkout key in %s", res.raw)
		}
		return v
	}
	if v := get(); v != nil {
		t.Fatalf("checkout %v before any CLI said, want null", v)
	}
	res := e.do(t, tok, "PATCH", path, map[string]any{"checkout": "todo-app"})
	if res.status != 200 || res.body["checkout"] != "todo-app" {
		t.Fatalf("patch todo-app: %d %s", res.status, res.raw)
	}
	for _, bad := range []string{"..", "a/b", "has space", "x?q=1", "../etc"} {
		res := e.do(t, tok, "PATCH", path, map[string]any{"checkout": bad, "keep_awake": true})
		if res.status != 400 {
			t.Fatalf("patch %q: %d %s, want 400", bad, res.status, res.raw)
		}
	}
	if got := e.h.Project(p.ID); got.KeepAwake {
		t.Fatal("a refused checkout wrote keep_awake beside it")
	}
	if v := get(); v != "todo-app" {
		t.Fatalf("checkout %v after refused patches, want todo-app", v)
	}
	res = e.do(t, tok, "PATCH", path, map[string]any{"checkout": ""})
	if res.status != 200 {
		t.Fatalf("patch empty: %d %s", res.status, res.raw)
	}
	if v := get(); v != "" {
		t.Fatalf("checkout %v after the empty patch, want \"\"", v)
	}
}
