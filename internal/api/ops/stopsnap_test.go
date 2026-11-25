package ops_test

import (
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
)

// DECISIONS I-1636: a stop whose snapshot the host could not write (a
// full store) stopped the machine; hostd says so in snapshot_error. The
// project is stopped, not error, with the reason, the user is told, and
// no snapshot is recorded. The next start is a start.
func TestStopWithoutItsSnapshotLeavesProjectStopped(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("ines")
	p := h.CreateRunning(u, "todo-app")
	pid := p.ID
	before, _ := store.ListSnapshots(h.Ctx, h.Pool, pid)
	h.Fake.SetStopSnapshotError("internal: snapshot upload failed: write x.part: no space left on device")
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}))
	if op.State != "done" {
		t.Fatalf("stop op %s %+v", op.State, op.Error)
	}
	p = h.Project(pid)
	if p.State != "stopped" || p.LastError == nil || !strings.HasPrefix(*p.LastError, "snapshot_failed: the host's snapshot space is full. Repose deletes snapshots older than seven days") {
		t.Fatalf("project %s %v, want stopped with the snapshot's error", p.State, p.LastError)
	}
	after, _ := store.ListSnapshots(h.Ctx, h.Pool, pid)
	if len(after) != len(before) {
		t.Fatalf("%d snapshots, want %d: none recorded for a stop without one", len(after), len(before))
	}
	evs, _ := store.ListEvents(h.Ctx, h.Pool, pid, time.Time{}, 50)
	found := false
	for _, e := range evs {
		found = found || (e.Kind == "snapshot_failed" && e.Summary == "todo-app stopped, but its snapshot was not saved: the host's snapshot space is full. Repose deletes snapshots older than seven days, and all but a project's newest seven, once a day.")
	}
	if !found {
		t.Fatalf("no snapshot_failed event: %+v", evs)
	}
	h.Fake.SetStopSnapshotError("")
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)})); op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}
}
