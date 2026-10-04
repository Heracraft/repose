package guest

import (
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/lvm"
)

const gid3 = "0192f0a3-3333-7000-8000-000000000003"

// The host's thin volumes together cannot pass 1.5 times the pool, by
// create or by resize (DECISIONS I-449). One volume has no bound of its
// own: a plan's whole disk may be one project's. A 200 GB pool: 300 GB in
// all.
func TestPoolBudget(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolSize, h.lvm.PoolFree = 200<<30, 200<<30
	h.create(gid1) // 40 GB

	// One volume larger than the whole pool is allowed while the budget
	// holds.
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 250 << 30}))

	// The caches' volume counts; snapshots do not. 250 + 64 = 314 is past
	// 300, so the next grow is refused and nothing changes.
	h.lvm.Volumes["repose-cache"] = &lvm.FakeVolume{Size: 64 << 30}
	h.lvm.Volumes["snap-"+gid1+"-1"] = &lvm.FakeVolume{Size: 250 << 30}
	res := h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 251 << 30}), CodeInsufficientCapacity)
	t.Log(res.Error.Message)
	if h.lvm.Volumes["g-"+gid1].Size != 250<<30 || h.guest(gid1).VolumeBytes != 250<<30 {
		t.Fatal("refused resize changed the volume")
	}
	// The guest already over the budget keeps starting.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))

	// With a smaller cache volume, 250 + 14 = 264: a 40 GB create would
	// pass 300, a 36 GB one reaches it exactly.
	h.lvm.Volumes["repose-cache"] = &lvm.FakeVolume{Size: 14 << 30}
	c := createReq(gid2)
	c.SystemClosure = h.closure
	res = h.mustFail(cmd(c), CodeInsufficientCapacity) // 250 + 14 + 40 = 304
	t.Log(res.Error.Message)
	if _, ok := h.lvm.Volumes["g-"+gid2]; ok {
		t.Fatal("refused create made a volume")
	}
	// Exactly at the budget fits; one byte more, by a resize, does not.
	// The user's login-share volume (I-464) is a thin volume of the pool
	// too, so it counts.
	exact := uint64(36<<30) - h.lvm.Volumes[AuthVolumeName(h.guest(gid1).UserID)].Size
	c.VolumeBytes = exact
	h.mustOK(cmd(c))
	h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid2, NewBytes: exact + 1}), CodeInsufficientCapacity)
}
