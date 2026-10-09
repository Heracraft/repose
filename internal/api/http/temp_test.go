package httpapi_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/temp"
	"github.com/heracraft/repose/internal/db"
)

// tempEnv signs a user in and creates a running temporary project.
func tempEnv(t *testing.T, name string) (*env, string, uuid.UUID) {
	t.Helper()
	e := newEnv(t)
	tok := e.signIn(t, "sub-"+name, name)
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": name, "class": "small", "expires_in_s": 3600})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	if r.body["expires_at"] == nil || r.body["remote_url"] != nil {
		t.Fatalf("created: %s", r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	return e, tok, uuid.MustParse(r.body["id"].(string))
}

func (e *env) sample(t *testing.T, id uuid.UUID, ts time.Time, ssh, tmux int, agents string) {
	t.Helper()
	if err := db.EnsurePartitions(e.h.Ctx, e.h.Pool, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, `insert into meter_samples (ts, project_id, state, class, ssh_sessions, tmux_clients, agents)
		values ($1, $2, 'running', 'small', $3, $4, $5::jsonb)`, ts, id, ssh, tmux, agents); err != nil {
		t.Fatal(err)
	}
}

func (e *env) destroyOp(t *testing.T, id uuid.UUID) *store.Op {
	t.Helper()
	var opID uuid.UUID
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select id from ops where project_id = $1 and kind = 'destroy' order by created_at desc limit 1", id).Scan(&opID); err != nil {
		t.Fatal(err)
	}
	return e.h.WaitOp(opID)
}

// The create and keep contract (api.md): expires_in_s in 600..86400 and
// never with a remote; PATCH expires_at only to null.
func TestTempCreateAndKeepContract(t *testing.T) {
	e, tok, id := tempEnv(t, "tmp-abcd")
	for _, body := range []map[string]any{
		{"name": "short", "class": "small", "expires_in_s": 60},
		{"name": "long", "class": "small", "expires_in_s": 86401},
		{"name": "remote", "class": "small", "expires_in_s": 3600, "remote_url": "github.com/a/b"},
	} {
		if r := e.do(t, tok, "POST", "/projects", body); r.status != 400 || errCode(r) != "invalid" {
			t.Fatalf("create %v: %d %s", body, r.status, r.raw)
		}
	}
	path := "/projects/" + id.String()
	if r := e.do(t, tok, "PATCH", path, map[string]any{"expires_at": "2030-01-01T00:00:00Z"}); r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("expires_at set to a time: %d %s", r.status, r.raw)
	}
	// A PATCH without the field leaves it alone.
	if r := e.do(t, tok, "PATCH", path, map[string]any{"hold_base_updates": true}); r.status != 200 || r.body["expires_at"] == nil {
		t.Fatalf("patch without expires_at: %d %s", r.status, r.raw)
	}
	r := e.do(t, tok, "PATCH", path, map[string]any{"expires_at": nil})
	if r.status != 200 {
		t.Fatalf("keep: %d %s", r.status, r.raw)
	}
	if _, ok := r.body["expires_at"]; ok {
		t.Fatalf("kept project still has expires_at: %s", r.raw)
	}
	if p := e.h.Project(id); p.ExpiresAt != nil {
		t.Fatalf("row still temporary: %v", p.ExpiresAt)
	}
	// A kept project is never reaped.
	reaper := &temp.Reaper{Pool: e.h.Pool, Engine: e.h.Engine, Events: e.h.Events}
	if res, err := reaper.Run(e.h.Ctx, time.Now().Add(48*time.Hour)); err != nil || res.Destroyed != 0 {
		t.Fatalf("reaper on a kept project: %+v %v", res, err)
	}
}

// Past its expiry, a temporary project somebody is attached to waits; the
// next minute with nobody on it (an agent at its prompt is nobody), or a
// day past the expiry, it goes (I-347, I-350).
func TestTempExpiryWaitsWhileAttached(t *testing.T) {
	e, _, id := tempEnv(t, "tmp-wait")
	ctx := e.h.Ctx
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := e.h.Pool.Exec(ctx, "update projects set expires_at = $2 where id = $1", id, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	reaper := &temp.Reaper{Pool: e.h.Pool, Engine: e.h.Engine, Events: e.h.Events}
	e.sample(t, id, now.Add(-30*time.Second), 1, 1, `[]`)
	if res, err := reaper.Run(ctx, now); err != nil || res.Destroyed != 0 || res.Waiting != 1 {
		t.Fatalf("attached: %+v %v", res, err)
	}
	if st := e.h.Project(id).State; st != "running" {
		t.Fatalf("state %s while attached", st)
	}
	// An agent working with nobody attached holds it too.
	e.sample(t, id, now.Add(-20*time.Second), 0, 0, `[{"agent":"claude","window":"claude","state":"working"}]`)
	if res, err := reaper.Run(ctx, now); err != nil || res.Waiting != 1 {
		t.Fatalf("agent working: %+v %v", res, err)
	}
	// A day past the expiry it goes regardless.
	if res, err := reaper.Run(ctx, now.Add(temp.Grace)); err != nil || res.Destroyed != 1 {
		t.Fatalf("past the grace: %+v %v", res, err)
	}
	if op := e.destroyOp(t, id); op.State != "done" {
		t.Fatalf("destroy: %+v", op.Error)
	}
}

// On herdr an SSH session does not hold an expired temporary project: a
// laptop herdr's sidebar keeps one open to every machine in it (I-511).
// A working agent still does (I-602).
func TestTempHerdrExpiryIgnoresSessions(t *testing.T) {
	e, _, id := tempEnv(t, "tmp-herd")
	ctx := e.h.Ctx
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := e.h.Pool.Exec(ctx, "update projects set expires_at = $2, multiplexer = 'herdr' where id = $1", id, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	reaper := &temp.Reaper{Pool: e.h.Pool, Engine: e.h.Engine, Events: e.h.Events}
	e.sample(t, id, now.Add(-30*time.Second), 1, 0, `[{"agent":"claude","window":"herdr:p1","state":"working"}]`)
	if res, err := reaper.Run(ctx, now); err != nil || res.Destroyed != 0 || res.Waiting != 1 {
		t.Fatalf("agent working: %+v %v", res, err)
	}
	e.sample(t, id, now.Add(-20*time.Second), 1, 0, `[{"agent":"claude","window":"herdr:p1","state":"idle"}]`)
	if res, err := reaper.Run(ctx, now); err != nil || res.Destroyed != 1 {
		t.Fatalf("sidebar session only: %+v %v", res, err)
	}
	if op := e.destroyOp(t, id); op.State != "done" {
		t.Fatalf("destroy: %+v", op.Error)
	}
}

// At expiry with nobody on it: [destroy_guest], no snapshot, the nightly
// one expired at once, a temp_destroyed event, nothing to restore
// (I-347).
func TestTempExpiryDestroysWithoutSnapshot(t *testing.T) {
	e, tok, id := tempEnv(t, "tmp-gone")
	ctx := e.h.Ctx
	now := time.Now().UTC().Truncate(time.Second)
	p := e.h.Project(id)
	// It lived through 03:00: the nightly snapshot.
	if _, err := e.h.Pool.Exec(ctx, `insert into snapshots (id, project_id, host_id, blob_path, bytes, reason, taken_at) values ($1, $2, $3, $4, 1, 'scheduled', $5)`,
		store.NewID(), id, p.HostID, "snap/"+id.String()+"/nightly", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A warning an hour before, once; none for a lifetime of an hour or
	// less (this one was created with 3600 s).
	reaper := &temp.Reaper{Pool: e.h.Pool, Engine: e.h.Engine, Events: e.h.Events}
	if res, err := reaper.Run(ctx, now.Add(10*time.Minute)); err != nil || res.Warned != 0 {
		t.Fatalf("warned a one-hour machine: %+v %v", res, err)
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set created_at = $2, expires_at = $3 where id = $1", id, now.Add(-23*time.Hour), now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{1, 0} {
		if res, err := reaper.Run(ctx, now); err != nil || res.Warned != want || res.Destroyed != 0 {
			t.Fatalf("warn run %d: %+v %v", i, res, err)
		}
	}
	// Expired, idle agent only, sample fresh: it goes.
	e.sample(t, id, now.Add(time.Hour-time.Minute), 0, 0, `[{"agent":"claude","window":"claude","state":"idle"}]`)
	res, err := reaper.Run(ctx, now.Add(time.Hour))
	if err != nil || res.Destroyed != 1 {
		t.Fatalf("expiry: %+v %v", res, err)
	}
	if st := e.h.Project(id).State; st != "destroying" && st != "destroyed" {
		t.Fatalf("state %s after the reaper enqueued", st)
	}
	// A second run while the destroy is open does nothing.
	if res, err := reaper.Run(ctx, now.Add(time.Hour)); err != nil || res.Destroyed != 0 {
		t.Fatalf("second run: %+v %v", res, err)
	}
	op := e.destroyOp(t, id)
	if op.State != "done" {
		t.Fatalf("destroy: %+v", op.Error)
	}
	if ph, _ := op.Params["phases"].([]any); len(ph) != 1 || ph[0] != "destroy_guest" {
		t.Fatalf("phases %v, want [destroy_guest]", op.Params["phases"])
	}
	gone := e.h.Project(id)
	if gone.State != "destroyed" || gone.DestroyedAt == nil {
		t.Fatalf("project %s", gone.State)
	}
	var restorable, stopSnaps, destroyedEvents int
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from snapshots s where s.project_id = $1 and "+store.RestorableSnapshotWhere, id).Scan(&restorable); err != nil {
		t.Fatal(err)
	}
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from snapshots where project_id = $1 and reason = 'stop'", id).Scan(&stopSnaps); err != nil {
		t.Fatal(err)
	}
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = 'temp_destroyed'", id).Scan(&destroyedEvents); err != nil {
		t.Fatal(err)
	}
	if restorable != 0 || stopSnaps != 0 || destroyedEvents != 1 {
		t.Fatalf("restorable %d, final snapshots %d, temp_destroyed events %d", restorable, stopSnaps, destroyedEvents)
	}
	if r := e.do(t, tok, "GET", "/projects/destroyed", nil); r.status != 200 || len(r.list) != 0 {
		t.Fatalf("destroyed list: %d %s", r.status, r.raw)
	}
}

// `repose rm` of a temporary project uses the same plan: no snapshot, and
// no temp_destroyed (the user asked; nothing ran out).
func TestTempDeleteKeepsNoSnapshot(t *testing.T) {
	e, tok, id := tempEnv(t, "tmp-rm")
	r := e.do(t, tok, "DELETE", "/projects/"+id.String(), nil)
	if r.status != 202 {
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	op := e.waitOp(t, r)
	if ph, _ := op.Params["phases"].([]any); op.State != "done" || len(ph) != 1 || ph[0] != "destroy_guest" {
		t.Fatalf("op %s phases %v", op.State, op.Params["phases"])
	}
	var snaps, events int
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select (select count(*) from snapshots where project_id = $1), (select count(*) from events where project_id = $1 and kind = 'temp_destroyed')", id).Scan(&snaps, &events); err != nil {
		t.Fatal(err)
	}
	if snaps != 0 || events != 0 {
		t.Fatalf("snapshots %d, temp_destroyed %d", snaps, events)
	}
}
