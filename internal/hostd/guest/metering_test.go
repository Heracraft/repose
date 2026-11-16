package guest

import (
	"context"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// billed is the egress a sample bills, as the api's rollup reads it: the
// egress counter's delta, or the tap's from a hostd without one
// (DECISIONS I-1063).
func billed(g *hostdv1.GuestSample) uint64 {
	if g.NetEgressBytesDelta != nil {
		return g.GetNetEgressBytesDelta()
	}
	return g.NetTxBytesDelta
}

// meteredTx sums the egress billed across every sample a test saw: the
// ones CollectSamples returned and the ones hostd emitted on its own (the
// final reading at a stop). It fails on two samples of the guest in the
// same second, which the api would keep only one of.
func meteredTx(t *testing.T, h *harness, collected []*hostdv1.Samples) (tx uint64, running int) {
	t.Helper()
	h.rec.mu.Lock()
	all := append(append([]*hostdv1.Samples{}, h.rec.samples...), collected...)
	h.rec.mu.Unlock()
	seen := map[int64]bool{}
	for _, s := range all {
		for _, g := range s.Guests {
			if g.GuestId != gid1 {
				continue
			}
			if seen[s.Ts] {
				t.Fatalf("two samples of the guest at ts %d; the api drops one", s.Ts)
			}
			seen[s.Ts] = true
			tx += billed(g)
			if g.State == StateRunning {
				running++
			}
		}
	}
	return tx, running
}

// Every byte a guest sends is metered across starts and stops: before its
// first tick (the cursor starts at zero with the tap), after its last (the
// stop reads the tap before deleting it), and in a run between two ticks
// (DECISIONS I-448).
func TestEgressMeteredAcrossStartAndStop(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	ctx := context.Background()
	tap := h.guest(gid1).Tap
	var collected []*hostdv1.Samples
	var sent uint64
	send := func(n uint64) { h.net.Send(tap, 0, n); sent += n }

	send(3000) // before the first tick
	collected = append(collected, h.m.CollectSamples(ctx))
	send(500) // after the last tick
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))

	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	send(700) // a new tap, counting from zero again
	collected = append(collected, h.m.CollectSamples(ctx))
	send(100)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))

	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	send(20_000) // a whole run between two ticks
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	collected = append(collected, h.m.CollectSamples(ctx))

	tx, running := meteredTx(t, h, collected)
	if tx != sent {
		t.Fatalf("metered %d of %d bytes sent", tx, sent)
	}
	// The final readings are stopping samples: the api's hours count only
	// running ones, and two ticks saw the guest running.
	if running != 2 {
		t.Fatalf("%d running samples, want 2", running)
	}
}

// A counter below the cursor started again from zero: it counts in full
// instead of being taken as a new baseline.
func TestCounterResetCountsInFull(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	ctx := context.Background()
	tap := h.guest(gid1).Tap
	h.net.Send(tap, 0, 5000)
	h.m.CollectSamples(ctx)
	h.net.Counters[gid1] = 300 // the counter was recreated under hostd
	h.net.Stats[tap] = [2]uint64{0, 300} // and the tap
	s := h.m.CollectSamples(ctx)
	if got := s.Guests[0].NetTxBytesDelta; got != 300 || s.Guests[0].GetNetEgressBytesDelta() != 300 {
		t.Fatalf("delta after a reset %d, want 300", got)
	}
	if counterDelta(10, 4) != 4 || counterDelta(4, 10) != 6 {
		t.Fatal("counterDelta")
	}
}

// A destroy from running meters the last bytes once: at the stop, not
// again at the destroy.
func TestDestroyMetersOnce(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.net.Send(h.guest(gid1).Tap, 0, 4096)
	h.net.Counters[gid1] = 4096
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	tx, _ := meteredTx(t, h, nil)
	if tx != 4096 {
		t.Fatalf("metered %d, want 4096", tx)
	}
}

// What the guest sends the edge or the host (SSH output, port forwards,
// hooks, the caches) leaves through its tap but never reaches the internet,
// so it is not egress; what it receives is still read from the tap
// (DECISIONS I-1063).
func TestEgressExcludesTrafficToTheEdge(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	ctx := context.Background()
	tap := h.guest(gid1).Tap
	h.net.SendLocal(tap, 2000, 90_000) // a terminal session's output
	h.net.Send(tap, 0, 1200)           // a request to the internet
	s := h.m.CollectSamples(ctx)
	// The tap's figures still say what went through it, which the idle
	// rule reads; the egress field is what bills.
	if g := s.Guests[0]; g.GetNetEgressBytesDelta() != 1200 || g.NetTxBytesDelta != 91_200 || g.NetRxBytesDelta != 2000 {
		t.Fatalf("egress %d tx %d rx %d, want 1200, 91200 and 2000", g.GetNetEgressBytesDelta(), g.NetTxBytesDelta, g.NetRxBytesDelta)
	}
	h.net.SendLocal(tap, 0, 50_000)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	if tx, _ := meteredTx(t, h, []*hostdv1.Samples{s}); tx != 1200 {
		t.Fatalf("metered %d across the stop, want 1200", tx)
	}
}

// A tick that cannot read the counters moves no guest's egress, and the
// next one counts those bytes: none is lost or counted twice.
func TestEgressUnreadTickCountsLater(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	ctx := context.Background()
	tap := h.guest(gid1).Tap
	h.net.Send(tap, 0, 800)
	h.net.FailOn["egress"] = context.DeadlineExceeded
	a := h.m.CollectSamples(ctx)
	if g := a.Guests[0]; g.NetEgressBytesDelta == nil || *g.NetEgressBytesDelta != 0 {
		t.Fatalf("egress %v on an unread tick, want 0", g.NetEgressBytesDelta)
	}
	delete(h.net.FailOn, "egress")
	h.net.Send(tap, 0, 200)
	b := h.m.CollectSamples(ctx)
	if got := b.Guests[0].GetNetEgressBytesDelta(); got != 1000 {
		t.Fatalf("egress %d after an unread tick, want 1000", got)
	}
}
