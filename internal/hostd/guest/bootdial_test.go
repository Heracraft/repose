package guest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/fakeguestd"
)

// I-225: until its first guestd session a monitor dials every
// GuestdBootRetry, not every GuestdRetry, so a create (or start) is
// marked running within a fraction of a second of guestd listening, not
// up to GuestdRetry later.
func TestBootDialFindsGuestdSoon(t *testing.T) {
	// What is measured is how long after guestd began listening the
	// create returned, not the whole create: under -race on a loaded box
	// the create's own steps before the boot took over a second, and a
	// bound on the total failed with the retry working (2.93 s against
	// 1.8 s, d2-int 2026-11-27; the same test on main).
	for _, c := range []struct {
		boot   time.Duration
		maxLag time.Duration
	}{
		{0, 1000 * time.Millisecond}, // the default, 50 ms
		{2 * time.Second, 0},         // what every start paid before
	} {
		h := newHarness(t, func(cfg *Config) {
			cfg.GuestdRetry = 2 * time.Second
			cfg.GuestdBootRetry = c.boot
			cfg.ReadyTimeout = 10 * time.Second
		})
		const id = "01a0cfac-851e-7d29-83d0-2bad2804cb21"
		h.mu.Lock()
		h.noBoot[id] = true
		h.mu.Unlock()
		// guestd starts listening 800 ms into the boot: late enough that
		// the first dial misses it even when a loaded machine is slow to
		// get there (at 300 ms a busy CI box dialled after it was up and
		// the 2 s case measured nothing).
		listened := make(chan time.Time, 1)
		go func() {
			time.Sleep(800 * time.Millisecond)
			srv, err := fakeguestd.Listen(filepath.Join(h.sockDir, id+".sock"), h.gopts)
			if err != nil {
				t.Error(err)
				return
			}
			listened <- time.Now()
			h.mu.Lock()
			h.guestds[id] = srv
			h.mu.Unlock()
		}()
		start := time.Now()
		h.create(id)
		end := time.Now()
		at := <-listened
		lag := end.Sub(at)
		if c.boot == 0 && lag > c.maxLag {
			t.Fatalf("create returned %s after guestd listened, with the boot retry", lag)
		}
		if c.boot != 0 && end.Sub(start) < 1500*time.Millisecond {
			t.Fatalf("create took %s with a 2 s boot retry: the test does not measure the retry", end.Sub(start))
		}
		t.Logf("boot retry %v: create took %s, %s after guestd listened", c.boot, end.Sub(start).Round(10*time.Millisecond), lag.Round(10*time.Millisecond))
	}
}
