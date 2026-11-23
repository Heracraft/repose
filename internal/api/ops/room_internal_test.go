package ops

import (
	"testing"

	"github.com/heracraft/repose/internal/api/scheduler"
)

// TestRoomWhyNamesTheFigure: a ready host of the user's own that refused
// a machine says which figure refused it, memory before disk, with what
// it has and what is needed (DECISIONS I-1586).
func TestRoomWhyNamesTheFigure(t *testing.T) {
	for _, c := range []struct {
		r     scheduler.Room
		class string
		short bool
		want  string
	}{
		{scheduler.Room{FreeBytes: 5 << 30, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, "large", false,
			"has 5 GB of memory free for machines, and a large machine needs 8 GB. A machine holds its memory while it runs, so stopping one frees room for it"},
		{scheduler.Room{FreeBytes: 3 << 29, PoolFreeBytes: 0, PoolNeedBytes: 1}, "tiny", true, "has 1.5 GB free"},
		{scheduler.Room{FreeBytes: 20 << 30, PoolFreeBytes: 40 << 30, PoolNeedBytes: 61 << 30}, "small", false,
			"has 40 GB of disk free, and a new machine needs 61 GB free"},
		{scheduler.Room{FreeBytes: 20 << 30, PoolFreeBytes: 40 << 30, PoolNeedBytes: 61 << 30}, "small", true, "has 40 GB of disk free, 61 GB needed"},
		{scheduler.Room{FreeBytes: 20 << 30, PoolUnavailable: true}, "small", false, "cannot use its disk for machines"},
		{scheduler.Room{FreeBytes: 20 << 30, PoolFreeBytes: 900 << 30, PoolNeedBytes: 300 << 30}, "small", false, ""},
	} {
		if got := roomWhy(c.r, c.class, c.short); got != c.want {
			t.Errorf("roomWhy(%+v, %s, %v) = %q, want %q", c.r, c.class, c.short, got, c.want)
		}
	}
}
