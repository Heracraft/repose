package ops

import (
	"testing"

	"github.com/heracraft/repose/internal/api/scheduler"
)

// TestRoomWhyNamesTheFigure: a ready host of the user's own that refused
// a machine says which figure refused it, memory before disk, with what
// it has and what is needed (DECISIONS I-1586). Since I-1630 the memory
// figure is what the host reports free against OwnedStartFreeBytes,
// and no class is named: a machine there has no size.
func TestRoomWhyNamesTheFigure(t *testing.T) {
	for _, c := range []struct {
		r     scheduler.Room
		short bool
		want  string
	}{
		{scheduler.Room{FreeBytes: 1 << 29, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false,
			"has 0.5 GB of memory free, and a machine starts there when it has at least 1 GB free. Stopping a machine frees the memory it uses"},
		{scheduler.Room{FreeBytes: 0, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false,
			"has 0 GB of memory free, and a machine starts there when it has at least 1 GB free. Stopping a machine frees the memory it uses"},
		{scheduler.Room{FreeBytes: 3 << 28, PoolFreeBytes: 0, PoolNeedBytes: 1}, true, "has 0.8 GB free"},
		// 5 GB free took a large before I-1630; now it is room.
		{scheduler.Room{FreeBytes: 5 << 30, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false, ""},
		{scheduler.Room{FreeBytes: 20 << 30, PoolFreeBytes: 40 << 30, PoolNeedBytes: 61 << 30}, false,
			"has 40 GB of disk free, and a new machine needs 61 GB free"},
		{scheduler.Room{FreeBytes: 20 << 30, PoolFreeBytes: 40 << 30, PoolNeedBytes: 61 << 30}, true, "has 40 GB of disk free, 61 GB needed"},
		{scheduler.Room{FreeBytes: 20 << 30, PoolUnavailable: true}, false, "cannot use its disk for machines"},
		{scheduler.Room{FreeBytes: 20 << 30, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, false, ""},
	} {
		if got := roomWhy(c.r, c.short); got != c.want {
			t.Errorf("roomWhy(%+v, %v) = %q, want %q", c.r, c.short, got, c.want)
		}
	}
}
