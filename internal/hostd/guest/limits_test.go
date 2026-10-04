package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// A guest boots with its class's disk rate limiter (DECISIONS I-450) and
// both directions of its tap shaped (I-217, I-451), and a class changed
// while stopped takes its new disk limits at the next start.
func TestGuestBootsWithClassLimits(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1) // large
	args := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(h.cfg.GuestsDir, gid1, "ch.args"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if a := args(); !strings.Contains(a, "direct=on,bw_size=120000000,bw_refill_time=1000,ops_size=3000,ops_refill_time=1000\n") {
		t.Fatalf("large guest's --disk has no 3000 IOPS / 120 MB/s limiter:\n%s", a)
	}
	tap := h.guest(gid1).Tap
	if h.net.Shaped[tap] != 200 || h.net.Down[tap] != 1000 {
		t.Fatalf("tap shaped up %d down %d, want 200 and 1000", h.net.Shaped[tap], h.net.Down[tap])
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1, Class: "small"}))
	if a := args(); !strings.Contains(a, "bw_size=80000000,bw_refill_time=1000,ops_size=2000,ops_refill_time=1000\n") {
		t.Fatalf("small guest's --disk limiter:\n%s", a)
	}
	for name, c := range Classes {
		if c.DiskIOPS == 0 || c.DiskMBps == 0 || c.DiskIOPS > 16000/4 || c.DiskMBps > 600/4 {
			t.Fatalf("class %s disk limits %d IOPS %d MB/s: each must be set and at most a quarter of the data disk", name, c.DiskIOPS, c.DiskMBps)
		}
	}
}
