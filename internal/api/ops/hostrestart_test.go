package ops_test

import (
	"slices"
	"testing"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
)

// A host restart (DECISIONS I-1690) is one RestartHost: done with the
// parked count when hostd restarts, an error naming the machines by slug
// when one could not be parked and the restart was not forced, and the
// hostd refusal as it is on a host that does not restart.
func TestHostRestartOp(t *testing.T) {
	h := apitest.New(t, apitest.Options{FakeOpts: fakehostd.Options{Restart: true}})
	u := h.NewUser("rita")
	h.CreateRunning(u, "rs")
	hid := h.HostID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindHostRestart, HostID: &hid, Phases: ops.PlanHostRestart()}))
	if op.State != "done" {
		t.Fatalf("restart: %s %+v", op.State, op.Error)
	}
	if op.Result["parked"] != float64(1) || op.Result["restarting"] != true {
		t.Fatalf("result %v", op.Result)
	}
}

func TestHostRestartOpRefusedNamesTheMachine(t *testing.T) {
	h := apitest.New(t, apitest.Options{FakeOpts: fakehostd.Options{Restart: true}})
	u := h.NewUser("rex")
	p := h.CreateRunning(u, "kept")
	h.Fake.SetNotParked([]string{p.GuestID.String()}, "")
	hid := h.HostID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindHostRestart, HostID: &hid, Phases: ops.PlanHostRestart()}))
	if op.State != "error" || op.Error["code"] != ops.CodeNotParked {
		t.Fatalf("restart with a machine it cannot park: %s %+v", op.State, op.Error)
	}
	want := "1 machine could not be parked and would start fresh (kept: started with nobody logged in). Nothing restarted, and the parked machines run again."
	if op.Error["message"] != want {
		t.Fatalf("message %q, want %q", op.Error["message"], want)
	}
	names, _ := op.Result["not_parked"].([]any)
	if !slices.Equal(names, []any{"kept"}) || op.Result["restarting"] != false {
		t.Fatalf("result %v", op.Result)
	}
	// A locked Mac parks nothing: the sentence says to unlock it.
	h.Fake.SetNotParked([]string{p.GuestID.String()}, ops.ReasonLocked)
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindHostRestart, HostID: &hid, Phases: ops.PlanHostRestart()}))
	if op.State != "error" || op.Error["message"] != "This Mac's screen is locked, and a locked Mac cannot save a machine's memory. Nothing restarted; unlock it and restart again." {
		t.Fatalf("locked: %s %+v", op.State, op.Error)
	}
	h.Fake.SetNotParked([]string{p.GuestID.String()}, "")
	// Forced: done, the machine named in the result.
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindHostRestart, HostID: &hid, Params: map[string]any{"force": true}, Phases: ops.PlanHostRestart()}))
	if op.State != "done" || op.Result["restarting"] != true {
		t.Fatalf("forced: %s %+v %v", op.State, op.Error, op.Result)
	}
}

// Every Linux host: hostd refuses, and so does the op.
func TestHostRestartOpOnAHostThatDoesNotRestart(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	hid := h.HostID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindHostRestart, HostID: &hid, Phases: ops.PlanHostRestart()}))
	if op.State != "error" || op.Error["code"] != "invalid_argument" {
		t.Fatalf("%s %+v", op.State, op.Error)
	}
}
