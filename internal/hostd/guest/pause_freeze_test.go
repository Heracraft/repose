package guest

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// S7, the failure this feature could cause: a guest the idle pause has
// paused is one the host-restart freeze then has to snapshot. Cloud
// Hypervisor refuses a second vm.pause, and the freeze's refusal path
// discards the image, counts the guest unfrozen and leaves it to
// cold-boot and lose every process -- I-796's own incident, caused by
// the pause. The freeze must take an already-paused guest as paused.
//
// ch.Fake refuses the second pause the way the hypervisor does, so this
// test can see it at all.
func TestFreezeOfARestingGuestKeepsItsMemory(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.pauseOK(gid1)
	res := fh.freezeOne(gid1)
	if !res.OK || res.Outcome != "frozen" {
		t.Fatalf("freeze of a resting guest: %+v", res)
	}
	if res.ImageBytes == 0 {
		t.Fatal("no image was written")
	}
	if n := testutil.ToFloat64(fh.metrics.GuestFreezeUnfrozen.WithLabelValues("failed")); n != 0 {
		t.Fatalf("freeze_unfrozen_total{failed} = %v, want 0", n)
	}
	if rec := fh.record(gid1); rec == nil || rec.State != "complete" {
		t.Fatalf("freeze record: %+v", rec)
	}
	// A frozen guest is not resting: its memory is in the image, and a
	// wake cannot bring it back in milliseconds.
	if g := fh.guest(gid1); g.Resting != nil {
		t.Fatalf("the freeze left the resting mark: %+v", g.Resting)
	}
}

// The freeze's pre-freeze write-back skips a resting guest: it synced at
// its pause and cannot answer now, and the 10 s spent waiting comes off
// the freeze's deadline, which is what makes a later guest fail the
// estimate and lose its image.
func TestFreezeDoesNotWaitOnARestingGuestsWriteBack(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.pauseOK(gid1)
	gd := fh.guestd(gid1)
	before := 0
	for _, k := range gd.Kinds() {
		if k == "Exec" {
			before++
		}
	}
	fh.freezeOne(gid1)
	after := 0
	for _, k := range gd.Kinds() {
		if k == "Exec" {
			after++
		}
	}
	if after != before {
		t.Fatalf("the freeze ran %d Execs in a paused guest", after-before)
	}
}

// A guest frozen while it rested comes back plain running at the thaw,
// with no mark: the restore resumed it, so nothing may say its vCPUs are
// stopped.
func TestThawOfAGuestFrozenWhileRestingClearsTheMark(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.pauseOK(gid1)
	if res := fh.freezeOne(gid1); !res.OK {
		t.Fatalf("freeze: %+v", res)
	}
	fh.nextStart("boot-b")
	fh.waitState(gid1, StateRunning)
	if g := fh.guest(gid1); g.Resting != nil {
		t.Fatalf("the thaw left a resting mark: %+v", g.Resting)
	}
	if fh.m.Resting(gid1) {
		t.Fatal("the manager's map kept the mark across the thaw")
	}
	var found *hostdv1.GuestStatus
	for _, gs := range fh.m.Hello().Guests {
		if gs.GuestId == gid1 {
			found = gs
		}
	}
	if found == nil || found.RestingAt != 0 {
		t.Fatalf("Hello after the thaw: %+v", found)
	}
}
