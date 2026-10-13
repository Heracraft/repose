package guest

import (
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// TestBootLogsItsSteps: a create and a start each log one "boot timings"
// line and carry the same numbers in their result, so a slow boot can be
// read from the ops row and the journal without piecing timestamps
// together (DECISIONS I-707; the 55 s create of 2026-10-12 had to be).
func TestBootLogsItsSteps(t *testing.T) {
	h := newHarness(t, nil)
	res := h.create(gid1)
	b := res.GetCreate().GetBoot()
	if b == nil {
		t.Fatal("CreateResult has no boot timings")
	}
	lines := logLines(t, h, "boot timings")
	if len(lines) != 1 {
		t.Fatalf("%d \"boot timings\" lines after a create", len(lines))
	}
	l := lines[0]
	if l["event"] != "guest_start" || l["op"] != "create" {
		t.Fatalf("boot timings: %v", l)
	}
	for _, k := range []string{"volume_ms", "prepare_ms", "ready_ms", "paths_ms", "setup_ms", "total_ms"} {
		if _, ok := l[k].(float64); !ok {
			t.Fatalf("boot timings has no %s: %v", k, l)
		}
	}
	if uint32(l["total_ms"].(float64)) != b.TotalMs || uint32(l["ready_ms"].(float64)) != b.ReadyMs {
		t.Fatalf("log %v and result %v disagree", l, b)
	}
	if sum := b.VolumeMs + b.PrepareMs + b.ReadyMs + b.PathsMs + b.SetupMs; sum > b.TotalMs {
		t.Fatalf("steps add up to %d ms, over the total %d: %v", sum, b.TotalMs, b)
	}

	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	st := h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if st.GetStart().GetBoot() == nil {
		t.Fatal("StartResult has no boot timings")
	}
	lines = logLines(t, h, "boot timings")
	if len(lines) != 2 || lines[1]["op"] != "start" {
		t.Fatalf("after a start: %v", lines)
	}
	if lines[1]["volume_ms"].(float64) != 0 {
		t.Fatalf("a start made no volume: %v", lines[1])
	}
}
