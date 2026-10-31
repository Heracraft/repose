package guest

import (
	"context"
	"testing"

	"github.com/heracraft/repose/internal/hostd/lvm"
)

// A hostd that starts finds no snapshot in flight, so every snap-* volume
// is an orphan of an interrupted Snapshot (DECISIONS I-68).
func TestReconcileRemovesStaleSnapshotVolumes(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.Volumes["snap-g1-1"] = &lvm.FakeVolume{}
	h.lvm.Volumes["g-g1"] = &lvm.FakeVolume{}
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.lvm.Volumes["snap-g1-1"]; ok {
		t.Fatal("stale snapshot volume survived reconcile")
	}
	if _, ok := h.lvm.Volumes["g-g1"]; !ok {
		t.Fatal("guest volume was removed by the sweep")
	}
}

// A snapshot this hostd is taking or uploading is not an orphan, and the
// sweep leaves it alone (I-906). Reconcile is reachable while one is in
// flight — the operator's `POST /reconcile`, and the one after a thaw —
// and the sweep used to pull the volume out from under its own upload.
func TestReconcileLeavesAnInFlightSnapshotAlone(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.Volumes["snap-g1-busy"] = &lvm.FakeVolume{}
	h.lvm.Volumes["snap-g1-orphan"] = &lvm.FakeVolume{}
	h.lvm.Volumes["g-g1"] = &lvm.FakeVolume{}
	h.m.snapStarted("snap-g1-busy")
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.lvm.Volumes["snap-g1-busy"]; !ok {
		t.Fatal("the sweep took a snapshot that was still being uploaded")
	}
	if _, ok := h.lvm.Volumes["snap-g1-orphan"]; ok {
		t.Fatal("an orphan beside it survived the sweep")
	}
	// Once the upload is done the next reconcile takes it, so nothing
	// leaks: the sweep no longer runs only at start.
	h.m.snapDone("snap-g1-busy")
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.lvm.Volumes["snap-g1-busy"]; ok {
		t.Fatal("a released snapshot volume was not swept by a later reconcile")
	}
	if _, ok := h.lvm.Volumes["g-g1"]; !ok {
		t.Fatal("guest volume was removed by the sweep")
	}
}
