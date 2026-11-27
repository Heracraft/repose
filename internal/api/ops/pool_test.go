package ops_test

import (
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// DECISIONS I-644: a start refused because the host's pool is down booted
// nothing and the disk is intact: the project is stopped, not error, so
// the next `repose start` is a start and not a restart, and the user reads
// the sentence.
func TestStartVolumeUnavailableLeavesProjectStopped(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("vic")
	p := h.CreateRunning(u, "todo-app")
	pid := p.ID
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatalf("stop: %+v", op.Error)
	}
	h.Fake.SetFail("StartGuest", "volume_unavailable")
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)}))
	if op.State != "error" || op.Error["code"] != "volume_unavailable" {
		t.Fatalf("start op %s %+v", op.State, op.Error)
	}
	msg, _ := op.Error["message"].(string)
	if !strings.Contains(msg, "disk is safe") || !strings.Contains(msg, "`repose start`") {
		t.Fatalf("message %q", msg)
	}
	p = h.Project(pid)
	if p.State != "stopped" || p.LastError == nil || !strings.HasPrefix(*p.LastError, "volume_unavailable: ") {
		t.Fatalf("project %s %v, want stopped with the error", p.State, p.LastError)
	}
	h.Fake.SetFail("StartGuest", "")
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)})); op.State != "done" {
		t.Fatalf("start after the pool came back: %+v", op.Error)
	}
}

// A create that fails the same way keeps today's error with its host:
// freeing the placement would orphan hostd's creating record.
func TestCreateVolumeUnavailableKeepsHost(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("wes")
	h.Fake.SetFail("CreateGuest", "volume_unavailable")
	p := h.NewProject(u, "todo-app", "large")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	if op.State != "error" || op.Error["code"] != "volume_unavailable" {
		t.Fatalf("create op %s %+v", op.State, op.Error)
	}
	p = h.Project(pid)
	if p.State != "error" || p.HostID == nil {
		t.Fatalf("project %s host %v, want error with the host kept", p.State, p.HostID)
	}
}

// ENG-265: a start the host refuses for room booted nothing (hostd's
// commit guard, its free-memory and CPU checks and its pool check all
// answer before the boot), so the project stays stopped and only the
// command fails. It used to be left in error, and `repose status` said
// "error: the host has no room for this project right now." (d2-lab on
// e2e-d2n, 2026-11-27 00:28Z). The op keeps the code and the sentence, so
// the CLI's exit 8 and its message are unchanged.
func TestStartRefusedForRoomLeavesProjectStopped(t *testing.T) {
	for name, msg := range map[string]string{
		"mac commit": "the machines running on this host hold 22528 of the 24576 MiB it gives its machines, and one starts with at least 2048 MiB: stop one first",
		"cpu":        "not enough free CPU: 8 of 8 vCPUs taken by running machines on this host, a machine starts with 2",
		"memory":     "not enough free memory for a large guest",
		"pool":       "thin pool 96% full",
	} {
		t.Run(name, func(t *testing.T) {
			h := apitest.New(t, apitest.Options{})
			u := h.NewUser("vic")
			p := h.CreateRunning(u, "todo-app")
			pid := p.ID
			if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); op.State != "done" {
				t.Fatalf("stop: %+v", op.Error)
			}
			h.Fake.SetFail("StartGuest", "insufficient_capacity")
			h.Fake.SetFailMessage("StartGuest", msg)
			op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)}))
			if op.State != "error" || op.Error["code"] != "insufficient_capacity" {
				t.Fatalf("start op %s %+v", op.State, op.Error)
			}
			p = h.Project(pid)
			if p.State != "stopped" || p.LastError == nil || !strings.HasPrefix(*p.LastError, "insufficient_capacity: ") || p.HostID == nil {
				t.Fatalf("project %s %v host %v, want stopped on its host with the error", p.State, p.LastError, p.HostID)
			}
			h.Fake.SetFail("StartGuest", "")
			if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)})); op.State != "done" {
				t.Fatalf("start with room: %+v", op.Error)
			}
			if p = h.Project(pid); p.State != "running" {
				t.Fatalf("project %s after the start with room", p.State)
			}
		})
	}
}

// ENG-264 (DECISIONS I-1680): a host that sizes each start (a Mac) says
// what the VM got; the project keeps it while it runs, so `repose start`,
// `repose ls` and the dashboard can say a machine started smaller, and a
// stop clears it. A host that says nothing leaves none.
func TestStartRecordsTheSizeTheVMGot(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("vic")
	p := h.CreateRunning(u, "todo-app")
	pid := p.ID
	if p = h.Project(pid); p.VMSize != nil {
		t.Fatalf("vm_size %+v from a host that sizes nothing", p.VMSize)
	}
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatalf("stop: %+v", op.Error)
	}
	h.Fake.SetVMSize(&hostdv1.VmSize{Vcpus: 2, MemMib: 3968, FullVcpus: 5, FullMemMib: 8192})
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)})); op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}
	p = h.Project(pid)
	if p.State != "running" || p.VMSize == nil || !p.VMSize.SizedDown() || p.VMSize.MemMiB != 3968 || p.VMSize.FullVCPUs != 5 {
		t.Fatalf("project %s vm_size %+v, want 2 vCPUs / 3968 of 5 / 8192", p.State, p.VMSize)
	}
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatalf("stop: %+v", op.Error)
	}
	if p = h.Project(pid); p.VMSize != nil {
		t.Fatalf("stopped with vm_size %+v", p.VMSize)
	}
	h.Fake.SetVMSize(nil)
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)})); op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}
	if p = h.Project(pid); p.VMSize != nil {
		t.Fatalf("vm_size %+v after a start that reported none", p.VMSize)
	}
}
