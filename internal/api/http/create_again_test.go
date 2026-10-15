package httpapi_test

import (
	"testing"

	"github.com/google/uuid"
)

// TestStartCreatesAProjectWhoseCreateFailed is I-406: a create that failed
// at placement (no host with capacity) leaves the project in error with no
// guest. start used to enqueue a restart, which answered "project has no
// guest; create it first" for ever (dogfood 2026-10-01, recruiting-2). It
// now runs the create again ({op_id, create: true}) and the project ends
// running.
func TestStartCreatesAProjectWhoseCreateFailed(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-gus", "gus")
	// Full: no room for a large guest (8 GiB plus 512 MiB of overhead).
	// 8 GiB of host memory keeps 2 GiB for the host itself and leaves 6,
	// which a large guest cannot fit. 12 GiB used to be full too, when the
	// reserve was a flat 8 GiB below 128 GB; it is an eighth with a 2 GiB
	// floor since I-724, so 12 GiB now leaves 10 and fits one.
	if _, err := e.h.Pool.Exec(ctx, "update hosts set mem_bytes = 8::bigint<<30"); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "recruiting-2", "class": "large"})
	if r.status/100 != 2 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	var createOp string
	if err := e.h.Pool.QueryRow(ctx, "select id::text from ops where project_id = $1 and kind = 'create'", pid).Scan(&createOp); err != nil {
		t.Fatal(err)
	}
	if op := e.h.WaitOp(uuid.MustParse(createOp)); op.State != "error" || op.Error["code"] != "capacity" {
		t.Fatalf("create op: %s %+v", op.State, op.Error)
	}
	if p := e.h.Project(uuid.MustParse(pid)); p.State != "error" || p.GuestID != nil {
		t.Fatalf("after the failed create: %s guest %v", p.State, p.GuestID)
	}
	// Still full: start runs the create, which fails the same way and
	// says so, not "create it first".
	r = e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	if r.status != 202 || r.body["create"] != true || r.body["restart"] != false {
		t.Fatalf("start while full: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "error" || op.Error["code"] != "capacity" {
		t.Fatalf("create from start while full: %s %+v", op.State, op.Error)
	}
	// Room again: start creates it.
	if _, err := e.h.Pool.Exec(ctx, "update hosts set mem_bytes = 64::bigint<<30"); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	if r.status != 202 || r.body["create"] != true {
		t.Fatalf("start: %d %s", r.status, r.raw)
	}
	if st := e.h.Project(uuid.MustParse(pid)).State; st == "error" {
		t.Fatalf("project still reads error after the create was accepted")
	}
	if op := e.waitOp(t, r); op.State != "done" || op.Kind != "create" {
		t.Fatalf("create from start: %s %s %+v", op.Kind, op.State, op.Error)
	}
	p := e.h.Project(uuid.MustParse(pid))
	if p.State != "running" || p.GuestID == nil || p.HostID == nil || p.LastError != nil {
		t.Fatalf("after start: %+v", p)
	}
	// A running project with a guest: start is the conflict it always was.
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil); r.status != 409 {
		t.Fatalf("start while running: %d %s", r.status, r.raw)
	}
}
