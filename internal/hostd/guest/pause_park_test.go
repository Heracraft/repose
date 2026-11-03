package guest

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// S7, the failure this feature could cause: a guest the idle pause has
// paused is one the host-restart park then has to snapshot. Cloud
// Hypervisor refuses a second vm.pause, and the park's refusal path
// discards the image, counts the guest skipped and leaves it to
// cold-boot and lose every process -- I-796's own incident, caused by
// the pause. The park must take an already-paused guest as paused.
//
// ch.Fake refuses the second pause the way the hypervisor does, so this
// test can see it at all.
func TestParkOfARestingGuestKeepsItsMemory(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.pauseOK(gid1)
	res := fh.parkOne(gid1)
	if !res.OK || res.Outcome != "parked" {
		t.Fatalf("park of a resting guest: %+v", res)
	}
	if res.ImageBytes == 0 {
		t.Fatal("no image was written")
	}
	if n := testutil.ToFloat64(fh.metrics.GuestParkSkipped.WithLabelValues("failed")); n != 0 {
		t.Fatalf("park_skipped_total{failed} = %v, want 0", n)
	}
	if rec := fh.record(gid1); rec == nil || rec.State != "complete" {
		t.Fatalf("park record: %+v", rec)
	}
	// A parked guest is not resting: its memory is in the image, and a
	// wake cannot bring it back in milliseconds.
	if g := fh.guest(gid1); g.Resting != nil {
		t.Fatalf("the park left the resting mark: %+v", g.Resting)
	}
}

// The park's pre-park write-back skips a resting guest: it synced at
// its pause and cannot answer now, and the 10 s spent waiting comes off
// the park's deadline, which is what makes a later guest fail the
// estimate and lose its image.
func TestParkDoesNotWaitOnARestingGuestsWriteBack(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.pauseOK(gid1)
	gd := fh.guestd(gid1)
	before := 0
	for _, k := range gd.Kinds() {
		if k == "Exec" {
			before++
		}
	}
	fh.parkOne(gid1)
	after := 0
	for _, k := range gd.Kinds() {
		if k == "Exec" {
			after++
		}
	}
	if after != before {
		t.Fatalf("the park ran %d Execs in a paused guest", after-before)
	}
}

// A guest parked while it rested comes back plain running at the unpark,
// with no mark: the restore resumed it, so nothing may say its vCPUs are
// stopped.
func TestUnparkOfAGuestParkedWhileRestingClearsTheMark(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.pauseOK(gid1)
	if res := fh.parkOne(gid1); !res.OK {
		t.Fatalf("park: %+v", res)
	}
	fh.nextStart("boot-b")
	fh.waitState(gid1, StateRunning)
	if g := fh.guest(gid1); g.Resting != nil {
		t.Fatalf("the unpark left a resting mark: %+v", g.Resting)
	}
	if fh.m.Resting(gid1) {
		t.Fatal("the manager's map kept the mark across the unpark")
	}
	var found *hostdv1.GuestStatus
	for _, gs := range fh.m.Hello().Guests {
		if gs.GuestId == gid1 {
			found = gs
		}
	}
	if found == nil || found.RestingAt != 0 {
		t.Fatalf("Hello after the unpark: %+v", found)
	}
}
