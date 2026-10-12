package ops_test

import (
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
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
	if !strings.Contains(msg, "disk is intact") || !strings.Contains(msg, "`repose start`") {
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
