package guest

import (
	"slices"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// netOps is a copy of the fake network's operations so far.
func (h *harness) netOps() []string {
	return slices.Clone(h.net.Ops)
}

// A guest address freed by a destroy and handed to the next guest must
// not carry the old guest's MAC in the host's neighbour table: the entry
// is deleted when the destroy has removed the tap, and again when the
// address is given to a guest, before it boots (DECISIONS I-1530; staging
// saw `10.64.0.3 lladdr 52:54:cc:56:95:84 STALE` and 30 to 45 s of dial
// timeouts to the fork that took the address).
func TestNeighbourEntryFlushedOnDestroyAndAssign(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	ip := h.guest(gid1).IP
	tap := h.guest(gid1).Tap
	ops := h.netOps()
	flush, boot := slices.Index(ops, "neigh-"+ip), slices.Index(ops, "rules+"+gid1)
	if flush < 0 || flush > boot {
		t.Fatalf("create: no neighbour flush for %s before the guest's rules: %v", ip, ops)
	}
	if i := slices.Index(h.sd.Ops, "run "+GuestUnit(gid1)); i < 0 {
		t.Fatal("guest never started")
	}

	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	ops = h.netOps()[len(ops):]
	delTap, flush := slices.Index(ops, "tap-"+tap), slices.Index(ops, "neigh-"+ip)
	if delTap < 0 || flush < 0 || flush < delTap {
		t.Fatalf("destroy: want the tap removed, then the neighbour entry: %v", ops)
	}

	// The next guest takes the freed address, and it is flushed again
	// before that guest boots.
	h.create(gid2)
	if got := h.guest(gid2).IP; got != ip {
		t.Fatalf("second guest got %s, want the freed %s", got, ip)
	}
	n := 0
	for _, o := range h.netOps() {
		if o == "neigh-"+ip {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("the reused address was flushed %d times, want 3 (create, destroy, create): %v", n, h.netOps())
	}

	// A restore (a fork's first step) assigns an address, and flushes it.
	snap := h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid2, Reason: "manual"})).GetSnapshot()
	const gid3 = "0192f0a3-3333-7000-8000-000000000003"
	h.mustOK(cmd(&hostdv1.Restore{ProjectId: "proj-0192f0a3", GuestId: gid3, BlobPath: snap.BlobPath, Sha256: snap.Sha256, Class: "large", VolumeBytes: 40 << 30, SystemClosure: h.closure}))
	if ip3 := h.guest(gid3).IP; !slices.Contains(h.netOps(), "neigh-"+ip3) {
		t.Fatalf("restore did not flush %s: %v", ip3, h.netOps())
	}
}
