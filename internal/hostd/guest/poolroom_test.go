package guest

import (
	"context"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// The thin pool is guarded by what it holds, not by the sizes of its
// volumes (DECISIONS I-586, replacing I-449's bound on their sum): sizes
// are ceilings and the pool is overcommitted by design. At 85 percent
// used, data or metadata, a create, a restore and a grow are refused; at
// 95 a start is; a running guest is never touched.
func TestPoolRoom(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolSize, h.lvm.PoolFree = 200<<30, 200<<30
	h.create(gid1) // 40 GB

	// Sizes far past the pool are allowed while it has room: a 250 GB
	// grow and a second 40 GB volume put 290 GB of sizes on 200.
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 250 << 30}))
	h.create(gid2)

	// 86 percent used: no grow, no create; a start still goes.
	h.lvm.PoolFree = 28 << 30
	res := h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 251 << 30}), CodeInsufficientCapacity)
	t.Log(res.Error.Message)
	if h.lvm.Volumes["g-"+gid1].Size != 250<<30 || h.guest(gid1).VolumeBytes != 250<<30 {
		t.Fatal("refused resize changed the volume")
	}
	c := createReq("0192f0a3-3333-7000-8000-000000000003")
	c.SystemClosure = h.closure
	h.mustFail(cmd(c), CodeInsufficientCapacity)
	if _, ok := h.lvm.Volumes["g-"+c.GuestId]; ok {
		t.Fatal("refused create made a volume")
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))

	// Metadata counts as data does: 10 percent data, 86 metadata.
	h.lvm.PoolFree, h.lvm.PoolMetaPct = 180<<30, 86
	h.mustFail(cmd(c), CodeInsufficientCapacity)
	h.lvm.PoolMetaPct = 0

	// 84 percent: the create goes.
	h.lvm.PoolFree = 32 << 30
	h.mustOK(cmd(c))

	// 95 percent: a start is refused, and the running guest runs on.
	h.lvm.PoolFree = 10 << 30
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid2}))
	res = h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid2}), CodeInsufficientCapacity)
	t.Log(res.Error.Message)
	if g := h.guest(gid1); g.State != StateRunning {
		t.Fatalf("running guest is %s", g.State)
	}
	h.lvm.PoolFree = 100 << 30
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid2}))
}

// Hello and the heartbeat carry the pool's size, so the api places by
// how full it is as autoextend grows it (DECISIONS I-586).
func TestHeartbeatCarriesPoolSize(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolSize, h.lvm.PoolFree = 500<<30, 120<<30
	if hb := h.m.Heartbeat(); hb.PoolBytes != 500<<30 || hb.PoolFreeBytes != 120<<30 {
		t.Fatalf("heartbeat pool %d free %d", hb.PoolBytes, hb.PoolFreeBytes)
	}
	if he := h.m.Hello(); he.PoolBytes != 500<<30 {
		t.Fatalf("hello pool %d", he.PoolBytes)
	}
}

// pool_high goes out at 70 percent, where the api stops placing new
// projects on the host, and not below.
func TestPoolHighAtPlacementThreshold(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolFree = h.lvm.PoolSize * 31 / 100
	h.m.CollectSamples(context.Background())
	for _, w := range h.rec.warnings() {
		if w == "pool_high" {
			t.Fatal("pool_high at 69 percent")
		}
	}
	h.lvm.PoolFree = h.lvm.PoolSize * 29 / 100
	h.m.CollectSamples(context.Background())
	for _, w := range h.rec.warnings() {
		if w == "pool_high" {
			return
		}
	}
	t.Fatal("no pool_high at 71 percent")
}
