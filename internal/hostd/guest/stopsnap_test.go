package guest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/snapshot"
)

// A stop whose snapshot cannot be written (the store full: "no space left
// on device" on a Mac appliance's root, 2026-11-25) still stops the guest,
// and says so: the stop is ok, the guest is stopped with its volume, and
// StopResult.snapshot_error carries why there is no snapshot (I-1636).
func TestStopWithAStoreThatCannotWriteStopsTheGuest(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.blob.Fail = errors.New("write snapshots/x.img.zst.part: no space left on device")
	res := h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1, SnapshotFirst: true}))
	sr := res.GetStop()
	if sr == nil || sr.BlobPath != "" || !strings.HasPrefix(sr.SnapshotError, CodeInternal+": snapshot upload failed: ") || !strings.Contains(sr.SnapshotError, "no space left on device") {
		t.Fatalf("stop result %v", sr)
	}
	g := h.guest(gid1)
	if g.State != StateStopped {
		t.Fatalf("state %s", g.State)
	}
	if ok, _ := h.lvm.VolumeExists(t.Context(), VolumeName(gid1)); !ok {
		t.Fatal("volume gone")
	}
	// The next stop's snapshot, with room again, is a snapshot.
	h.blob.Fail = nil
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if r := h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1, SnapshotFirst: true})).GetStop(); r.BlobPath == "" || r.SnapshotError != "" {
		t.Fatalf("second stop %v", r)
	}
}

// The api's retention deletes an owned host's expired snapshot through
// the host that keeps it (I-1636): the file and its metadata go, a second
// delete is ok, and a host that keeps no snapshots says so.
func TestDeleteSnapshotOnTheHostThatKeepsIt(t *testing.T) {
	dir := t.TempDir()
	h := newHarnessWith(t, nil, func(d *Deps) { d.Blob = &snapshot.FileBlob{Dir: dir} })
	h.create(gid1)
	sr := h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1, SnapshotFirst: true})).GetStop()
	if sr.BlobPath == "" {
		t.Fatalf("no snapshot: %v", sr)
	}
	full := filepath.Join(dir, filepath.FromSlash(sr.BlobPath))
	if _, err := os.Stat(full); err != nil {
		t.Fatal(err)
	}
	h.mustOK(cmd(&hostdv1.DeleteSnapshot{BlobPath: sr.BlobPath}))
	for _, p := range []string{full, full + ".meta"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still there: %v", p, err)
		}
	}
	h.mustOK(cmd(&hostdv1.DeleteSnapshot{BlobPath: sr.BlobPath}))
	h.mustFail(cmd(&hostdv1.DeleteSnapshot{BlobPath: "../etc/passwd"}), CodeInvalidArgument)

	mem := newHarness(t, nil) // a MemBlob stands in for a store that cannot delete
	mem.mustFail(cmd(&hostdv1.DeleteSnapshot{BlobPath: "a/b/1.img.zst"}), CodeInvalidArgument)
}
