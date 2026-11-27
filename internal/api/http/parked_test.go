package httpapi_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/answering"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// DECISIONS I-1672: a machine parked on a Mac, waiting for its user to log
// in, reads `parked: {since, waits_for: "login"}` and never
// `not_answering`, though its guestd is silent; the warner says nothing;
// and a start does not restart it, which would lose the memory it keeps.
func TestParkedMachineWaitsAndIsNotCalledNotAnswering(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-parked", "parked")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "macbox", "large")
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'running', started_at = $2 where id = $1", p.ID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	parkedAt := now.Add(-10 * time.Minute)
	for i := 4; i >= 0; i-- {
		ts := now.Add(-time.Duration(i) * time.Minute)
		if err := db.EnsurePartitions(ctx, e.h.Pool, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, guestd_ok, parked_waiting_since) values ($1, $2, 'running', 'large', false, $3)`, ts, p.ID, parkedAt); err != nil {
			t.Fatal(err)
		}
	}
	r := e.do(t, tok, "GET", "/projects/"+p.ID.String(), nil)
	if r.status != 200 {
		t.Fatalf("get: %d %s", r.status, r.raw)
	}
	if _, ok := r.body["not_answering"]; ok {
		t.Fatalf("a parked machine reads not_answering: %s", r.raw)
	}
	pk, ok := r.body["parked"].(map[string]any)
	if !ok || pk["waits_for"] != "login" {
		t.Fatalf("parked %v in %s", r.body["parked"], r.raw)
	}
	if got, _ := time.Parse(time.RFC3339, pk["since"].(string)); !got.Equal(parkedAt) {
		t.Fatalf("parked.since %v, want %s", pk["since"], parkedAt)
	}
	warner := &answering.Warner{Pool: e.h.Pool, Events: e.h.Events}
	if n, err := warner.Run(ctx, now); err != nil || n != 0 {
		t.Fatalf("warner raised %d for a parked machine (err %v)", n, err)
	}
}

// The start of a parked machine is a conflict, as for any running one that
// answers: never the restart a silent guestd gets (I-1652), which would
// throw away the memory the park keeps.
func TestStartDoesNotRestartAParkedMachine(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-parked2", "parked2")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "macbox2", "class": "small"})
	if r.status/100 != 2 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	var createOp string
	if err := e.h.Pool.QueryRow(ctx, "select id::text from ops where project_id = $1 and kind = 'create'", pid).Scan(&createOp); err != nil {
		t.Fatal(err)
	}
	if op := e.h.WaitOp(uuid.MustParse(createOp)); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	e.h.WaitIdle(uuid.MustParse(pid))
	p := e.h.Project(uuid.MustParse(pid))
	e.h.Fake.SetGuestdDead(p.GuestID.String(), true)
	if _, err := e.h.Pool.Exec(ctx, "insert into meter_samples (ts, project_id, host_id, state, class, guestd_ok, parked_waiting_since) values (now(), $1, $2, 'running', 'small', false, now() - interval '5 minutes')", pid, p.HostID); err != nil {
		t.Fatal(err)
	}
	s := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	if s.status != 409 || s.body["restart"] == true {
		t.Fatalf("start of a parked machine: %d %s", s.status, s.raw)
	}
}

// DECISIONS I-1682: on a Mac logged in with its screen locked, a parked
// machine waits for the unlock, and one resting as a save says it wakes
// at the unlock: both from hostd's waits_for.
func TestLockedMacMachinesWaitForTheUnlock(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-locked", "locked")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	parked := e.h.NewProject(u, "macbox", "large")
	resting := e.h.NewProject(u, "macrest", "large")
	for _, p := range []*store.Project{parked, resting} {
		if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'running', started_at = $2 where id = $1", p.ID, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set resting_since = $2 where id = $1", resting.ID, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, e.h.Pool, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, guestd_ok, parked_waiting_since, waits_for) values ($1, $2, 'running', 'large', false, $3, 'unlock')`, now, parked.ID, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, guestd_ok, resting, waits_for) values ($1, $2, 'running', 'large', true, true, 'unlock')`, now, resting.ID); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "GET", "/projects/"+parked.ID.String(), nil)
	if pk, ok := r.body["parked"].(map[string]any); !ok || pk["waits_for"] != "unlock" {
		t.Fatalf("parked %v in %s", r.body["parked"], r.raw)
	}
	r = e.do(t, tok, "GET", "/projects/"+resting.ID.String(), nil)
	if rs, ok := r.body["resting"].(map[string]any); !ok || rs["waits_for"] != "unlock" {
		t.Fatalf("resting %v in %s", r.body["resting"], r.raw)
	}
}
