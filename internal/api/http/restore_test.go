package httpapi_test

import (
	"testing"

	"github.com/google/uuid"
)

// TestRestoreByName is I-167: after a destroy, the project is listed
// under /projects/destroyed with its snapshot and expiry, and
// POST /projects/restore {slug} brings it back under the same name, with
// its remote; a taken name is a 409 the CLI can ask about; a project with
// nothing left to restore is a 404 that says so.
func TestRestoreByName(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-rita", "rita")
	if _, err := e.h.Pool.Exec(ctx, "update users set has_card = true where logto_sub = 'sub-rita'"); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "izma", "class": "large", "remote_url": "github.com/rita/izma"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}

	// Nothing destroyed yet.
	if r := e.do(t, tok, "GET", "/projects/destroyed", nil); r.status != 200 || len(r.list) != 0 {
		t.Fatalf("destroyed list before a destroy: %d %s", r.status, r.raw)
	}

	r = e.do(t, tok, "DELETE", "/projects/"+pid, nil)
	if r.status != 202 {
		t.Fatalf("destroy: %d %s", r.status, r.raw)
	}
	// The project reads destroying from the moment the DELETE answers
	// (or is already gone): never running while the CLI has moved on.
	if g := e.do(t, tok, "GET", "/projects/"+pid, nil); g.status == 200 && g.body["state"] != "destroying" {
		t.Fatalf("state right after DELETE: %v", g.body["state"])
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("destroy op: %+v", op.Error)
	}

	r = e.do(t, tok, "GET", "/projects/destroyed", nil)
	if r.status != 200 || len(r.list) != 1 {
		t.Fatalf("destroyed list: %d %s", r.status, r.raw)
	}
	d := r.list[0].(map[string]any)
	snap, _ := d["snapshot"].(map[string]any)
	if d["slug"] != "izma" || d["name_free"] != true || d["restorable_until"] == nil || snap == nil || snap["reason"] != "stop" {
		t.Fatalf("destroyed entry: %s", r.raw)
	}

	// Unknown name, bad body.
	if r := e.do(t, tok, "POST", "/projects/restore", map[string]any{"slug": "nope"}); r.status != 404 {
		t.Fatalf("unknown slug: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects/restore", map[string]any{}); r.status != 400 {
		t.Fatalf("empty body: %d %s", r.status, r.raw)
	}

	// By name, same name, same remote.
	r = e.do(t, tok, "POST", "/projects/restore", map[string]any{"slug": "IZMA"})
	if r.status != 202 || r.body["slug"] != "izma" || r.body["snapshot_id"] != snap["id"] || r.body["from_project_id"] != pid {
		t.Fatalf("restore by name: %d %s", r.status, r.raw)
	}
	// The CLI's restore line names the snapshot's size (I-595).
	if r.body["snapshot_bytes"] == nil || r.body["snapshot_bytes"] != snap["bytes"] {
		t.Fatalf("restore answer's snapshot_bytes %v, the snapshot's bytes %v", r.body["snapshot_bytes"], snap["bytes"])
	}
	newID := r.body["project_id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("restore op: %+v", op.Error)
	}
	g := e.do(t, tok, "GET", "/projects/"+newID, nil)
	if g.status != 200 || g.body["state"] != "running" || g.body["remote_url"] != "github.com/rita/izma" {
		t.Fatalf("restored project: %d %s", g.status, g.raw)
	}
	// The destroyed entry now says its name is taken.
	r = e.do(t, tok, "GET", "/projects/destroyed", nil)
	if len(r.list) != 1 || r.list[0].(map[string]any)["name_free"] != false {
		t.Fatalf("destroyed list after the restore: %s", r.raw)
	}

	// izma is live again, so `restore izma` means the live one: its newest
	// snapshot, and the name is taken.
	if r := e.do(t, tok, "POST", "/projects/"+newID+"/snapshots", nil); r.status != 202 {
		t.Fatalf("snapshot: %d %s", r.status, r.raw)
	} else if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("snapshot op: %+v", op.Error)
	}
	r = e.do(t, tok, "POST", "/projects/restore", map[string]any{"slug": "izma"})
	if r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("restore onto a taken name: %d %s", r.status, r.raw)
	}
	if det, _ := r.body["error"].(map[string]any)["detail"].(map[string]any); det["reason"] != "name_taken" || det["name"] != "izma" {
		t.Fatalf("taken-name detail: %s", r.raw)
	}
	// An older snapshot of the destroyed one, under another name; the
	// remote stays with the live project.
	r = e.do(t, tok, "POST", "/projects/restore", map[string]any{"snapshot_id": snap["id"], "name": "izma-old"})
	if r.status != 202 || r.body["from_project_id"] != pid {
		t.Fatalf("restore a named snapshot as another name: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("restore op: %+v", op.Error)
	}
	if g := e.do(t, tok, "GET", "/projects/"+r.body["project_id"].(string), nil); g.body["remote_url"] != nil {
		t.Fatalf("a second project took the live one's remote: %s", g.raw)
	}

	// Another user's snapshot id is not found.
	other := e.signIn(t, "sub-otto", "otto")
	if r := e.do(t, other, "POST", "/projects/restore", map[string]any{"snapshot_id": snap["id"]}); r.status != 404 {
		t.Fatalf("someone else's snapshot: %d %s", r.status, r.raw)
	}

	// A destroyed project whose snapshots are gone cannot be restored.
	if _, err := e.h.Pool.Exec(ctx, "update snapshots set expires_at = now() - interval '1 day' where project_id = $1", pid); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "GET", "/projects/destroyed", nil); len(r.list) != 0 {
		t.Fatalf("an expired destroy is still listed: %s", r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects/restore", map[string]any{"project_id": pid}); r.status != 404 {
		t.Fatalf("restore of an expired destroy: %d %s", r.status, r.raw)
	}
}

// TestRestoreOfADestroyingProjectSaysSo: restoring a project whose destroy
// has not taken its final snapshot yet answered "has no snapshot left to
// restore" (conductor, 2026-09-23). It is now 409 with reason destroying,
// which the CLI waits out (I-190).
func TestRestoreOfADestroyingProjectSaysSo(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-dora", "dora")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "busy", "class": "small"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update projects set state = 'destroying' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/restore", map[string]any{"slug": "busy"})
	envl, _ := r.body["error"].(map[string]any)
	detail, _ := envl["detail"].(map[string]any)
	if r.status != 409 || errCode(r) != "conflict" || detail["reason"] != "destroying" {
		t.Fatalf("restore of a destroying project: %d %s", r.status, r.raw)
	}
	t.Logf("%s", r.raw)
}

// I-461: an in-place restore that fails after the old guest is destroyed
// leaves the project in error without the old guest's address (the host
// has released it and may give it to the next guest). A start would boot
// an empty or half-written volume, so it is refused and names the
// restore; the restore run again brings the project back.
func TestFailedInPlaceRestoreReleasesTheGuest(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-rhea", "rhea")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "rhea", "class": "small"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/stop", nil); r.status != 202 {
		t.Fatalf("stop: %d %s", r.status, r.raw)
	} else if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("stop op: %+v", op.Error)
	}
	snaps := e.do(t, tok, "GET", "/projects/"+pid+"/snapshots", nil)
	if snaps.status != 200 || len(snaps.list) == 0 {
		t.Fatalf("snapshots: %d %s", snaps.status, snaps.raw)
	}
	sid := snaps.list[0].(map[string]any)["id"].(string)

	// A restore that fails in destroy_guest leaves the old guest and its
	// volume as they were: start is not refused.
	e.h.Fake.SetFail("DestroyGuest", "internal")
	r = e.do(t, tok, "POST", "/projects/"+pid+"/snapshots/"+sid+"/restore", map[string]any{"start": false})
	if r.status != 202 {
		t.Fatalf("restore: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "error" {
		t.Fatalf("restore op with the host failing its destroy: %s", op.State)
	}
	e.h.Fake.SetFail("DestroyGuest", "")
	if p := e.h.Project(uuid.MustParse(pid)); p.GuestIP == nil || p.GuestID == nil {
		t.Fatalf("a restore that failed in destroy_guest dropped the guest: %+v", p)
	}
	if st := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil); st.status != 202 {
		t.Fatalf("start after a restore that failed in destroy_guest: %d %s", st.status, st.raw)
	} else if op := e.waitOp(t, st); op.State != "done" {
		t.Fatalf("start op: %+v", op.Error)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/stop", nil); r.status != 202 {
		t.Fatalf("stop: %d %s", r.status, r.raw)
	} else if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("stop op: %+v", op.Error)
	}

	oldGuest := *e.h.Project(uuid.MustParse(pid)).GuestID
	e.h.Fake.SetFail("Restore", "internal")
	r = e.do(t, tok, "POST", "/projects/"+pid+"/snapshots/"+sid+"/restore", map[string]any{"start": false})
	if r.status != 202 {
		t.Fatalf("restore: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "error" {
		t.Fatalf("restore op with the host failing it: %s", op.State)
	}
	// The old guest is gone and so is its address; the guest id is the
	// failed restore's, which the next restore destroys first.
	p := e.h.Project(uuid.MustParse(pid))
	if p.State != "error" || p.GuestIP != nil || p.VsockCID != nil || p.GuestID == nil || *p.GuestID == oldGuest {
		t.Fatalf("after a failed in-place restore: state %s guest %v ip %v cid %v", p.State, p.GuestID, p.GuestIP, p.VsockCID)
	}
	st := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	errObj, _ := st.body["error"].(map[string]any)
	detail, _ := errObj["detail"].(map[string]any)
	if st.status != 409 || detail["reason"] != "restore_unfinished" {
		t.Fatalf("start after a failed restore: %d %s", st.status, st.raw)
	}

	e.h.Fake.SetFail("Restore", "")
	r = e.do(t, tok, "POST", "/projects/"+pid+"/snapshots/"+sid+"/restore", nil)
	if r.status != 202 {
		t.Fatalf("restore again: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("restore again: %+v", op.Error)
	}
	if p := e.h.Project(uuid.MustParse(pid)); p.State != "running" || p.GuestID == nil || p.GuestIP == nil {
		t.Fatalf("after the restore again: %+v", p)
	}
}
