package guest

import (
	"strings"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// On a Mac (VZ cannot resize an attached disk) a grow of a running
// machine is refused before anything changes: growing the file and failing
// the hypervisor's resize left hostd's size and the api's apart and every
// later snapshot unrestorable (e2e-pocketmail, 2026-11-26). Stopped, it
// grows as anywhere; the next start fills the filesystem out (I-1660).
func TestFileVolumesRefuseALiveGrowBeforeChangingAnything(t *testing.T) {
	h := newHarness(t, nil)
	h.m.d.FileVolumes = true
	h.create(gid1)
	before := h.lvm.Volumes["g-"+gid1].Size
	res := h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 80 << 30}), CodeInvalidArgument)
	if !strings.Contains(res.Error.Message, "while it is stopped") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if strings.Contains(res.Error.Message, "cloud-hypervisor") {
		t.Fatalf("the message names a hypervisor a Mac does not run: %q", res.Error.Message)
	}
	if h.lvm.Volumes["g-"+gid1].Size != before || h.guest(gid1).VolumeBytes != before {
		t.Fatal("the refused grow changed the volume or the record")
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 80 << 30}))
	if h.lvm.Volumes["g-"+gid1].Size != 80<<30 || h.guest(gid1).VolumeBytes != 80<<30 {
		t.Fatal("a stopped machine's disk did not grow")
	}
}

// A machine whose disk is gone answers a snapshot as unresponsive, which
// the api's destroy turns into a skipped final snapshot, so the machine
// can still be removed (e2e-pocketmail and e2e-pm-as1 could not be).
func TestFileVolumesSnapshotOfAMissingDiskIsUnresponsive(t *testing.T) {
	h := newHarness(t, nil)
	h.m.d.FileVolumes = true
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	delete(h.lvm.Volumes, "g-"+gid1)
	res := h.mustFail(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"}), CodeGuestUnresponsive)
	if strings.Contains(res.Error.Message, "lvcreate") {
		t.Fatalf("message %q", res.Error.Message)
	}
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
}
