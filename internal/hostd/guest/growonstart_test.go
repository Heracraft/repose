package guest

import (
	"slices"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// A disk grown while its machine is stopped is filled out at the next
// start (DECISIONS I-1660): the resize of a stopped guest only extends the
// volume, and nothing else ran resize2fs, so the guest kept its old
// filesystem (pass2-dash N1 on e2e-mac, 2026-11-26: lsblk 80G, df 40G
// after two starts). After the start reaches running, hostd asks guestd to
// grow; resize2fs on a filesystem that already fills its device does
// nothing, and it never shrinks.
func TestADiskGrownWhileStoppedIsFilledOutAtTheNextStart(t *testing.T) {
	h := newHarness(t, nil)
	if r := h.create(gid1); r.Error != nil {
		t.Fatalf("create: %v", r.Error)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 80 << 30}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	deadline := time.Now().Add(3 * time.Second)
	for !slices.Contains(h.guestd(gid1).Kinds(), "GrowFs") {
		if time.Now().After(deadline) {
			t.Fatalf("no GrowFs after the start: %v", h.guestd(gid1).Kinds())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if g := h.guest(gid1); g.State != StateRunning || g.VolumeBytes != 80<<30 {
		t.Fatalf("guest %s volume %d", g.State, g.VolumeBytes)
	}
}
