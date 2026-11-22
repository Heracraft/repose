package httpapi_test

import (
	"testing"

	"github.com/google/uuid"

	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/store"
)

// Two tabs (council 2026-11-22 finding 6). While a config op is open a
// second PUT is 409 conflict (Engine.Enqueue); after it lands, a PUT that
// names the revision it read (base_revision_id, DECISIONS I-1528) and is
// not the current one is 409 menu_stale; the current one, or none, is
// taken.
func TestConfigPutConcurrentAndStale(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-stale", "stale")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "tabs", "class": "small"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
	pid := r.body["id"].(string)
	path := "/projects/" + pid + "/config"
	seen := e.do(t, tok, "GET", path, nil).body["revision_id"].(string)

	// An op open on the project refuses a second change.
	opID := store.NewID()
	if _, err := e.h.Pool.Exec(ctx, "insert into ops (id, project_id, kind, state, params) values ($1, $2, 'build', 'pending', '{}')", opID, uuid.MustParse(pid)); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "PUT", path, map[string]any{"fragment": "{ pkgs, ... }: { }"}); r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("PUT while an op is open: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update ops set state = 'done' where id = $1", opID); err != nil {
		t.Fatal(err)
	}

	// Tab A changes the configuration from what it read.
	r = e.do(t, tok, "PUT", path, map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.jq ]; }", "base_revision_id": seen})
	if r.status != 202 {
		t.Fatalf("tab A: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
	// Tab B still holds the old revision.
	r = e.do(t, tok, "PUT", path, map[string]any{"fragment": "{ pkgs, ... }: { }", "base_revision_id": seen})
	if r.status != 409 || errCode(r) != "menu_stale" {
		t.Fatalf("tab B, stale: %d %s", r.status, r.raw)
	}
	errObj := r.body["error"].(map[string]any)
	cur := e.do(t, tok, "GET", path, nil).body["revision_id"].(string)
	if errObj["message"] != httpapi.MsgMenuStale || errObj["detail"].(map[string]any)["current_revision_id"] != cur {
		t.Fatalf("menu_stale body: %s", r.raw)
	}
	// Reloaded, it is taken; without the field, as before.
	if r := e.do(t, tok, "PUT", path, map[string]any{"fragment": "{ pkgs, ... }: { }", "base_revision_id": cur}); r.status != 202 {
		t.Fatalf("tab B reloaded: %d %s", r.status, r.raw)
	} else {
		e.waitOp(t, r)
	}
	if r := e.do(t, tok, "PUT", path, map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.ripgrep ]; }"}); r.status != 202 {
		t.Fatalf("no base_revision_id: %d %s", r.status, r.raw)
	}
}
