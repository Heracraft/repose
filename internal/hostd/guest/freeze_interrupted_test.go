package guest

import (
	"context"
	"testing"
)

// A host restart with the freeze on (I-652) and resume after a reboot
// (I-645) together: Reconcile restores the frozen guests before it lists
// the units, so a guest ThawAll brought back is running and is not marked
// interrupted (the api would otherwise start a machine that already runs,
// and tell its user it restarted). A guest whose image could not be used
// starts cold, and that one is marked, as after any host restart.
func TestThawedGuestIsNotMarkedInterrupted(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.create(gid2)
	b := withBoot(t, fh.harness, "boot-1")

	for _, r := range fh.m.FreezeAll(context.Background(), FreezeOperator) {
		if !r.OK || r.Outcome != "frozen" {
			t.Fatalf("freeze: %+v", r)
		}
	}
	// gid2's volume was written after its freeze: its image is stale and
	// is discarded, so gid2 comes back cold.
	fh.setFingerprint(gid2, "sb-2")

	m2 := hostRestart(t, fh.harness, b, "boot-2")

	if g := fh.guest(gid1); g.Interrupted != nil {
		t.Fatalf("a guest ThawAll restored was marked interrupted: %+v", g.Interrupted)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("frozen guest not restored by the reconcile after the restart")
	}
	fh.waitState(gid1, StateRunning)
	g2 := fh.guest(gid2)
	if g2.Interrupted == nil || g2.Interrupted.BootID != "boot-2" || g2.Interrupted.PrevState != StateRunning {
		t.Fatalf("the guest whose image was discarded was not marked: state %s, mark %+v", g2.State, g2.Interrupted)
	}
	for _, gs := range m2.Hello().Guests {
		switch gs.GuestId {
		case gid1:
			if gs.InterruptedBootId != "" || gs.State != StateRunning {
				t.Fatalf("Hello for the restored guest: %+v", gs)
			}
		case gid2:
			if gs.InterruptedBootId != "boot-2" {
				t.Fatalf("Hello for the cold guest: %+v", gs)
			}
		}
	}
	if n := gaugeValue(t, fh.metrics.GuestsInterrupted); n != 1 {
		t.Fatalf("repose_host_guests_interrupted = %v, want 1 (the cold guest only)", n)
	}
}
