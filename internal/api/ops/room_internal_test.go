package ops

import (
	"testing"

	"github.com/heracraft/repose/internal/api/scheduler"
)

// TestRoomWhyNamesTheFigure: a ready host of the user's own that refused
// a machine says which figure refused it, memory before disk, with what
// it has and what is needed (DECISIONS I-1586). Since I-1630 the memory
// figure is what the host reports free against OwnedStartFreeBytes,
// and no class is named: a machine there has no size. A host whose
// hostd does not share keeps the class figure.
func TestRoomWhyNamesTheFigure(t *testing.T) {
	for _, c := range []struct {
		r     scheduler.Room
		short bool
		want  string
	}{
		// A hostd that does not share yet places by class (I-1586).
		{scheduler.Room{FreeBytes: 5 << 30, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false,
			"is full. Stop a machine there"},
		{scheduler.Room{Shares: true, FreeBytes: 1 << 29, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false,
			"is full. Stop a machine there"},
		{scheduler.Room{Shares: true, FreeBytes: 0, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false,
			"is full. Stop a machine there"},
		{scheduler.Room{Shares: true, FreeBytes: 3 << 28, PoolFreeBytes: 0, PoolNeedBytes: 1}, true, "is full"},
		// 5 GB free took a large before I-1630; now it is room.
		{scheduler.Room{Shares: true, FreeBytes: 5 << 30, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false, ""},
		{scheduler.Room{Shares: true, FreeBytes: 20 << 30, PoolFreeBytes: 40 << 30, PoolNeedBytes: 61 << 30}, false,
			"is out of disk space: 40 GB free, 61 GB needed. `repose rm` a machine there to free some"},
		{scheduler.Room{Shares: true, FreeBytes: 20 << 30, PoolFreeBytes: 40 << 30, PoolNeedBytes: 61 << 30}, true, "is out of disk space"},
		{scheduler.Room{Shares: true, FreeBytes: 20 << 30, PoolUnavailable: true}, false, "cannot use its disk right now"},
		{scheduler.Room{Shares: true, FreeBytes: 20 << 30, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false, ""},
	} {
		if got := roomWhy(c.r, "large", c.short); got != c.want {
			t.Errorf("roomWhy(%+v, %v) = %q, want %q", c.r, c.short, got, c.want)
		}
	}
}
