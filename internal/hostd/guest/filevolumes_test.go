package guest

import (
	"context"
	"strings"
	"testing"
	"time"

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

// With SnapshotClones (a Mac) a snapshot keeps its frozen volume as a
// clone, a restore of it is a clone of that with nothing read from the
// store, and the sweep removes a clone whose snapshot the store no longer
// lists. d2-lab measured every restore --as writing 4.4 GB again.
func TestSnapshotClonesRestoreWithoutTheStore(t *testing.T) {
	h := newHarness(t, nil)
	h.m.d.SnapshotClones = true
	h.create(gid1)
	h.lvm.SetData("g-"+gid1, []byte("the tenant's filesystem"))
	sr := h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"})).GetSnapshot()
	kept := KeptCloneName(sr.BlobPath)
	if _, ok := h.lvm.Volumes[kept]; !ok {
		t.Fatalf("no kept clone %s", kept)
	}
	h.blob.BeforeDownload = func(string) { t.Error("the restore read the store") }
	rq := &hostdv1.Restore{ProjectId: "proj-0192f0a1", GuestId: gid2, BlobPath: sr.BlobPath, Class: "large", VolumeBytes: 40 << 30,
		SystemClosure: h.closure, UserId: "user-1", ProjectSlug: "todo-app", HostKey: []byte("hk")}
	h.mustOK(cmd(rq))
	if string(h.lvm.GetData("g-"+gid2)) != "the tenant's filesystem" {
		t.Fatal("restored bytes differ")
	}
	h.m.SweepKeptClones(context.Background(), []string{sr.BlobPath})
	if _, ok := h.lvm.Volumes[kept]; !ok {
		t.Fatal("a listed snapshot's clone was swept")
	}
	h.m.SweepKeptClones(context.Background(), nil)
	if _, ok := h.lvm.Volumes[kept]; ok {
		t.Fatal("an unlisted snapshot's clone was kept")
	}
}

// A destroy tells the platform, which sweeps the machine's kept snapshot
// clones at once (DECISIONS I-1712).
func TestDestroyRunsTheHook(t *testing.T) {
	h := newHarness(t, nil)
	done := make(chan struct{}, 1)
	h.m.SetOnDestroyed(func() { done <- struct{}{} })
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the destroy hook did not run")
	}
}
