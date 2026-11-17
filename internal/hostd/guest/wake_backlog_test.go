package guest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

type countingDialer struct {
	inner vsockclient.Dialer
	n     atomic.Int64
}

func (d *countingDialer) Dial(ctx context.Context, t vsockclient.Target) (vsockclient.Session, error) {
	d.n.Add(1)
	return d.inner.Dial(ctx, t)
}

// A monitor with no session dials nothing while the guest rests, and
// dials the moment a wake clears the mark (DECISIONS I-1228). A paused
// VM does not refuse a connect: it waits in the hypervisor's socket
// backlog and lands on guestd at the resume, where each one replaces the
// live connection. hostd restarted under three resting guests on
// 2026-11-16 and every wake after it failed with EOF.
func TestNoDialWhileResting(t *testing.T) {
	h := newHarness(t, func(cfg *Config) {
		// Long enough that a wake served by the retry alone, and not by
		// the poke, is visibly slow.
		cfg.GuestdRetry = 3 * time.Second
	})
	h.create(gid1)
	h.pauseOK(gid1)
	cd := &countingDialer{inner: h.m.d.Guestd}
	h.m.d.Guestd = cd
	// The restart is what leaves a resting guest's monitor without a
	// session; the fake guestd keeps listening, as the real one's socket
	// keeps accepting.
	h.restart()
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !h.m.Resting(gid1) {
		t.Fatal("the mark did not survive the restart")
	}
	time.Sleep(600 * time.Millisecond) // a dozen boot-rate dials' worth
	if n := cd.n.Load(); n != 0 {
		t.Fatalf("%d dials to a resting guest", n)
	}
	start := time.Now()
	res := h.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: WakeReasonConnect}))
	took := time.Since(start)
	if w := res.GetWake(); w == nil || !w.WasResting {
		t.Fatalf("wake: %+v", res)
	}
	if cd.n.Load() == 0 {
		t.Fatal("the wake did not dial")
	}
	if took > 1500*time.Millisecond {
		t.Fatalf("the wake took %s: the cleared mark did not poke the dial, the retry did", took)
	}
	if h.m.Resting(gid1) {
		t.Fatal("the mark survived the wake")
	}
}

// A session that ends under the wake is not a guest that does not
// answer: the wake takes the next session and tries once more
// (DECISIONS I-1228). The fake drops the connection on the clock step's
// Exec, which is where the replaced connection failed on 2026-11-16.
func TestWakeSurvivesAReplacedSession(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.pauseOK(gid1)
	h.guestd(gid1).DropNext("Exec")
	res := h.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: WakeReasonConnect}))
	if w := res.GetWake(); w == nil || !w.WasResting {
		t.Fatalf("wake: %+v", res)
	}
	if s := h.scrape(t); s.has(`repose_host_guest_wake_failed_total{reason="guestd"} 1`) {
		t.Fatal("a replaced session counted as a failed wake")
	}
	execs := 0
	for _, k := range h.guestd(gid1).Kinds() {
		if k == "Exec" {
			execs++
		}
	}
	// The pause's write-back and timer read, the dropped clock step and
	// the one that landed.
	if execs < 2 {
		t.Fatalf("%d Execs; the wake did not retry the clock step on the next session", execs)
	}
}
