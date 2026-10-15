package ops_test

import (
	"testing"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
)

// A create and a start keep hostd's boot timings in ops.result, so a slow
// boot is read from the ops table instead of the host's journal
// (DECISIONS I-707).
func TestCreateAndStartKeepBootTimings(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("bt")
	p := h.NewProject(u, "bt", "large")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	if op.State != "done" {
		t.Fatalf("create: %s %+v", op.State, op.Error)
	}
	b, ok := op.Result["boot"].(map[string]any)
	if !ok || b["total_ms"] == nil || b["ready_ms"] == nil || b["volume_ms"] == nil {
		t.Fatalf("create result %v has no boot timings", op.Result)
	}
	if op.Result["guest_ip"] == nil {
		t.Fatalf("create result lost guest_ip: %v", op.Result)
	}
	if stop := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); stop.State != "done" {
		t.Fatalf("stop: %s %+v", stop.State, stop.Error)
	}
	start := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)}))
	if start.State != "done" {
		t.Fatalf("start: %s %+v", start.State, start.Error)
	}
	if b, ok := start.Result["boot"].(map[string]any); !ok || b["total_ms"] == nil {
		t.Fatalf("start result %v has no boot timings", start.Result)
	}
}
