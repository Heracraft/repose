package guest

import (
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/lvm"
)

const gid3 = "0192f0a3-3333-7000-8000-000000000003"

// One tenant cannot take a volume past half the pool, and the host's
// volumes together cannot pass 1.5 times it, by create or by resize
// (DECISIONS I-449). A 200 GB pool: 100 GB per volume, 300 GB in all.
func TestPoolBudget(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolSize, h.lvm.PoolFree = 200<<30, 200<<30
	h.create(gid1) // 40 GB

	// One volume over half the pool: the resize that made one tenant's
	// volume larger than the whole pool is refused, and nothing changed.
	res := h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 101 << 30}), CodeInsufficientCapacity)
	t.Log(res.Error.Message)
	if h.lvm.Volumes["g-"+gid1].Size != 40<<30 || h.guest(gid1).VolumeBytes != 40<<30 {
		t.Fatal("refused resize changed the volume")
	}
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 100 << 30}))

	// The caches' volume counts; snapshots do not.
	h.lvm.Volumes["repose-cache"] = &lvm.FakeVolume{Size: 64 << 30}
	h.lvm.Volumes["snap-"+gid1+"-1"] = &lvm.FakeVolume{Size: 100 << 30}

	// 100 + 64 + 100 = 264 fits under 300; another 40 does not.
	c := createReq(gid2)
	c.VolumeBytes = 100 << 30
	c.SystemClosure = h.closure
	h.mustOK(cmd(c))
	c = createReq(gid3)
	c.SystemClosure = h.closure
	res = h.mustFail(cmd(c), CodeInsufficientCapacity)
	t.Log(res.Error.Message)
	if _, ok := h.lvm.Volumes["g-"+gid3]; ok {
		t.Fatal("refused create made a volume")
	}
	// Exactly at the budget fits; one byte more, by a resize, does not.
	c.VolumeBytes = 36 << 30
	h.mustOK(cmd(c))
	h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid3, NewBytes: 36<<30 + 1}), CodeInsufficientCapacity)
}
