package httpapi_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// TestStartCreatesAProjectWhoseCreateFailed is I-406: a create that failed
// at placement (no host with capacity) leaves the project in error with no
// guest. start used to enqueue a restart, which answered "project has no
// guest; create it first" for ever (dogfood 2026-10-01, recruiting-2). It
// now runs the create again ({op_id, create: true}) and the project ends
// running.
//
// Since I-1390 a create that no host can take is refused at the request
// and writes no row, so the first half of this test is that refusal, and
// the project whose create failed is written the way the old shape left
// it (a row in error with no host), which a start must still repair.
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
	if r.status != 503 || detailOf(t, r)["reason"] != "no_capacity" {
		t.Fatalf("create while full: %d %s, want 503 capacity no_capacity", r.status, r.raw)
	}
	var uid string
	if err := e.h.Pool.QueryRow(ctx, "select id from users where logto_sub = 'sub-gus'").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from projects where user_id = $1", uid).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a refused create left %d project row(s) behind", rows)
	}
	// The row the old shape left behind: in error, placed nowhere, no
	// guest. A start must still turn it into a create (I-406).
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(uid))
	if err != nil {
		t.Fatal(err)
	}
	pid := e.h.NewProject(u, "recruiting-2", "large").ID.String()
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'error', last_error = 'capacity: no host with capacity' where id = $1", pid); err != nil {
		t.Fatal(err)
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
