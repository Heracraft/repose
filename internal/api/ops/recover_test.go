package ops_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// killGuestd strands a running project the way the 2026-09-21 base
// switch stranded age-calculator: the unit runs, guestd does not answer,
// and the api has the project in `error`.
func killGuestd(t *testing.T, h *apitest.Harness, pid uuid.UUID, apiState string) *store.Project {
	t.Helper()
	p := h.Project(pid)
	h.Fake.SetGuestdDead(p.GuestID.String(), true)
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = $2 where id = $1", pid, apiState); err != nil {
		t.Fatal(err)
	}
	return h.Project(pid)
}

func commandsSince(h *apitest.Harness, n int) []*hostdv1.Command { return h.Fake.Commands()[n:] }

func kinds(cmds []*hostdv1.Command) string {
	var out []string
	for _, c := range cmds {
		switch {
		case c.GetStopGuest() != nil:
			if c.GetStopGuest().SnapshotFirst {
				out = append(out, "StopGuest+snap")
			} else {
				out = append(out, "StopGuest")
			}
		case c.GetSnapshot() != nil:
			out = append(out, "Snapshot")
		case c.GetDestroyGuest() != nil:
			out = append(out, "DestroyGuest")
		case c.GetStartGuest() != nil:
			out = append(out, "StartGuest")
		case c.GetApplyConfig() != nil:
			out = append(out, "ApplyConfig")
		case c.GetResizeVolume() != nil:
			out = append(out, "ResizeVolume")
		}
	}
	return strings.Join(out, ",")
}

// TestDestroyWithDeadGuestdReachesDone is I-156: every destroy of
// age-calculator failed at step 0 with guest_unresponsive and left the
// project in error. The op stops the unit without guestd, snapshots the
// stopped volume, deletes the guest and ends done. Since I-165 that is
// every destroy's plan, so a dead guestd needs no recovery at all.
func TestDestroyWithDeadGuestdReachesDone(t *testing.T) {
	for _, state := range []string{"error", "running"} {
		t.Run(state, func(t *testing.T) {
			h := apitest.New(t, apitest.Options{})
			u := h.NewUser("ada-" + state)
			p := h.CreateRunning(u, "age-calculator")
			pid := p.ID
			p = killGuestd(t, h, pid, state)
			n := len(h.Fake.Commands())
			op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}))
			if op.State != "done" {
				t.Fatalf("destroy: state %s error %+v", op.State, op.Error)
			}
			got := kinds(commandsSince(h, n))
			if want := "StopGuest,Snapshot,DestroyGuest"; got != want {
				t.Fatalf("commands %s, want %s", got, want)
			}
			if p := h.Project(pid); p.State != "destroyed" || p.DestroyedAt == nil {
				t.Fatalf("after destroy: %s", p.State)
			}
			if len(h.Fake.Guests()) != 0 {
				t.Fatal("guest still on the host")
			}
			snaps, _ := store.ListSnapshots(h.Ctx, h.Pool, pid)
			if len(snaps) != 1 || snaps[0].ExpiresAt == nil || time.Until(*snaps[0].ExpiresAt) < 29*24*time.Hour {
				t.Fatalf("final snapshot: %+v", snaps)
			}
			if r, ok := op.Params["recovered"]; ok {
				t.Fatalf("the ordinary plan needed a recovery: %v", r)
			}
			if snaps[0].Reason != "stop" {
				t.Fatalf("final snapshot reason %q, want stop", snaps[0].Reason)
			}
		})
	}
}

// A destroy whose final snapshot cannot be taken even of the stopped
// volume skips it with a snapshot_failed event and still ends done.
func TestDestroySkipsAnImpossibleFinalSnapshot(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("bea")
	p := h.CreateRunning(u, "gone")
	pid := p.ID
	p = killGuestd(t, h, pid, "error")
	h.Fake.SetFail("Snapshot", "guest_unresponsive")
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}))
	if op.State != "done" {
		t.Fatalf("destroy: %+v", op.Error)
	}
	if h.Project(pid).State != "destroyed" {
		t.Fatal("not destroyed")
	}
	evs, _ := store.ListEvents(h.Ctx, h.Pool, pid, time.Time{}, 50)
	found := false
	for _, e := range evs {
		found = found || (e.Kind == "snapshot_failed" && strings.Contains(e.Summary, "skipped"))
	}
	if !found {
		t.Fatalf("no snapshot_failed event for the skipped snapshot: %+v", evs)
	}
}

// A destroy whose guest the host no longer has is done, not error.
func TestDestroyOfAGuestTheHostLostIsDone(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("cal")
	p := h.CreateRunning(u, "lost")
	pid := p.ID
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'error', guest_id = $2 where id = $1", pid, store.NewID()); err != nil {
		t.Fatal(err)
	}
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(h.Project(pid))}))
	if op.State != "done" || h.Project(pid).State != "destroyed" {
		t.Fatalf("destroy of a lost guest: %s %+v", op.State, op.Error)
	}
}

// TestStartFromErrorRestarts is I-157: `repose start` on age-calculator
// sent StartGuest (ok, the unit ran) and ApplyConfig (guest_unresponsive)
// four times in two minutes. A start from error is a restart: stop the
// unit, adopt the newest built revision while stopped, boot.
func TestStartFromErrorRestarts(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("dan")
	p := h.CreateRunning(u, "age-calculator")
	pid := p.ID
	newer := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure) values ($1, $2, '{}', 'built', '/nix/store/new-system')", newer, pid); err != nil {
		t.Fatal(err)
	}
	p = killGuestd(t, h, pid, "error")
	pending, err := ops.PendingRevision(h.Ctx, h.Pool, p)
	if err != nil || !pending {
		t.Fatalf("pending %v %v", pending, err)
	}
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Params: ops.RestartParams(), Phases: ops.PlanRestart(pending)}))
	if op.State != "done" {
		t.Fatalf("restart: %+v", op.Error)
	}
	if got := kinds(commandsSince(h, n)); got != "StopGuest,ApplyConfig,StartGuest" {
		t.Fatalf("commands %s", got)
	}
	p = h.Project(pid)
	if p.State != "running" || p.ConfigRevisionID == nil || *p.ConfigRevisionID != newer {
		t.Fatalf("after restart: state %s revision %v", p.State, p.ConfigRevisionID)
	}
	g := h.Fake.Guests()[0]
	if g.GuestdDead || g.Closure != "/nix/store/new-system" || g.State != "running" {
		t.Fatalf("guest after restart: %+v", g)
	}
}

// The old start plan on a stranded guest (StartGuest ok, ApplyConfig
// guest_unresponsive) turns into the restart instead of failing.
func TestStartWhoseApplyLosesGuestdRestarts(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("eve")
	p := h.CreateRunning(u, "strand")
	pid := p.ID
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure) values ($1, $2, '{}', 'built', '/nix/store/new-system')", store.NewID(), pid); err != nil {
		t.Fatal(err)
	}
	killGuestd(t, h, pid, "error")
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(true)}))
	if op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}
	if got := kinds(commandsSince(h, n)); got != "StartGuest,ApplyConfig,StopGuest,ApplyConfig,StartGuest" {
		t.Fatalf("commands %s", got)
	}
	if h.Project(pid).State != "running" || h.Fake.Guests()[0].GuestdDead {
		t.Fatal("not recovered")
	}
}

// A stop with a snapshot on a dead guestd stops without guestd and
// snapshots the stopped volume (I-158).
func TestStopWithDeadGuestdStopsThenSnapshots(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("fay")
	p := h.CreateRunning(u, "stp")
	pid := p.ID
	killGuestd(t, h, pid, "running")
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": true}, Phases: ops.PlanStop()}))
	if op.State != "done" || op.SnapshotID == nil {
		t.Fatalf("stop: %s %+v", op.State, op.Error)
	}
	if got := kinds(commandsSince(h, n)); got != "StopGuest+snap,StopGuest,Snapshot" {
		t.Fatalf("commands %s", got)
	}
	if h.Project(pid).State != "stopped" {
		t.Fatal("not stopped")
	}
	snaps, _ := store.ListSnapshots(h.Ctx, h.Pool, pid)
	if len(snaps) != 1 || snaps[0].Reason != "stop" {
		t.Fatalf("snapshots %+v", snaps)
	}
}

// TestUnresponsiveErrorsAreSentences is I-159: op.error.message is a
// sentence with the way out, the code is kept, the host's wording (with
// the guest id) is detail only.
func TestUnresponsiveErrorsAreSentences(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("gus")
	p := h.CreateRunning(u, "msg")
	pid := p.ID
	killGuestd(t, h, pid, "running")
	gid := h.Project(pid).GuestID.String()
	for _, n := range []ops.NewOp{
		{Kind: ops.KindSnapshot, ProjectID: &pid, Phases: ops.PlanSnapshot()},
		{Kind: ops.KindResize, ProjectID: &pid, Params: map[string]any{"volume_bytes": float64(80 << 30)}, Phases: ops.PlanResize()},
	} {
		op := h.WaitOp(h.Enqueue(n))
		msg, _ := op.Error["message"].(string)
		if op.State != "error" || op.Error["code"] != "guest_unresponsive" {
			t.Fatalf("%s: %+v", n.Kind, op.Error)
		}
		if strings.Contains(msg, gid) || !strings.Contains(msg, "`repose start`") {
			t.Fatalf("%s message %q", n.Kind, msg)
		}
		if d, _ := op.Error["detail"].(string); !strings.Contains(d, gid) {
			t.Fatalf("%s detail %q", n.Kind, d)
		}
		if le := h.Project(pid).LastError; le == nil || strings.Contains(*le, gid) {
			t.Fatalf("%s last_error %v", n.Kind, le)
		}
	}
	if h.Project(pid).State != "running" {
		t.Fatal("a failed snapshot or resize must not change the state")
	}
}

// TestDestroyStopsFirstAndReportsItsFailure is I-165: a running project's
// destroy stops without a snapshot, snapshots the stopped volume (reason
// stop, 30 days) and destroys; a destroy enqueued with the pre-I-165 plan
// still snapshots in its stop; and a destroy that fails leaves the project
// in error with its reason and a destroy_failed event, because the CLI no
// longer waits to say so (I-166).
func TestDestroyStopsFirstAndReportsItsFailure(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("cleo")

	p := h.CreateRunning(u, "izma")
	pid := p.ID
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}))
	if op.State != "done" {
		t.Fatalf("destroy: %+v", op.Error)
	}
	if got, want := kinds(commandsSince(h, n)), "StopGuest,Snapshot,DestroyGuest"; got != want {
		t.Fatalf("commands %s, want %s", got, want)
	}
	if op.SnapshotID == nil {
		t.Fatal("the destroy op does not name its final snapshot")
	}

	old := h.CreateRunning(u, "old-plan")
	oid := old.ID
	n = len(h.Fake.Commands())
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &oid, Phases: []string{ops.PhaseStopGuest, ops.PhaseDestroyGuest}}))
	if got, want := kinds(commandsSince(h, n)), "StopGuest+snap,DestroyGuest"; op.State != "done" || got != want {
		t.Fatalf("pre-I-165 plan: %s %s, want %s", op.State, got, want)
	}

	bad := h.CreateRunning(u, "stuck")
	bid := bad.ID
	h.Fake.SetFail("DestroyGuest", "internal")
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &bid, Phases: ops.PlanDestroy(bad)}))
	if op.State != "error" {
		t.Fatalf("destroy with a failing host: %s", op.State)
	}
	got := h.Project(bid)
	if got.State != "error" || got.LastError == nil || got.DestroyedAt != nil {
		t.Fatalf("after a failed destroy: state %s last_error %v", got.State, got.LastError)
	}
	evs, _ := store.ListEvents(h.Ctx, h.Pool, bid, time.Time{}, 50)
	found := false
	for _, e := range evs {
		found = found || (e.Kind == "destroy_failed" && strings.Contains(e.Summary, "repose rm stuck"))
	}
	if !found {
		t.Fatalf("no destroy_failed event: %+v", evs)
	}
}

// TestHelloCountsOnlyForTheHostsOwnGuests: a host listing another host's
// guest in its Hello does not move that project's state; the project's
// own host still reconciles it.
func TestHelloCountsOnlyForTheHostsOwnGuests(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("helloer")
	p := h.CreateRunning(u, "hello-scope")
	h.WaitIdle(p.ID)
	hello := &hostdv1.Hello{Guests: []*hostdv1.GuestStatus{{GuestId: p.GuestID.String(), State: "stopped"}}}
	h.Engine.OnHello(h.Ctx, store.NewID(), hello)
	if got := h.Project(p.ID).State; got != "running" {
		t.Fatalf("another host's Hello set the state to %s", got)
	}
	h.Engine.OnHello(h.Ctx, h.HostID, hello)
	if got := h.Project(p.ID).State; got != "stopped" {
		t.Fatalf("the project's own host did not reconcile it: %s", got)
	}
}
