package waitlist

import (
	"testing"

	"github.com/heracraft/repose/internal/api/scheduler"
)

// The seats one host delivers (DECISIONS I-739). The memory and the
// published columns are the measured table in
// docs/proposals/2026-10-14-host-cpu-capacity.md §3, written out here as
// figures rather than taken from the code: these numbers are the price
// floor (docs/PRICING.md "The cost floor"), so moving a constant has to
// fail a test that names what was published.
//
// `want` is what the code sells, which is the published figure capped by
// what placement will accept for the host (scheduler.PlacementVCPUPct,
// 1.5 committed guest vCPUs per core = cores * 1.5 / 4 seats). The
// proposal's table has no placement term, so from 32 cores up `want` is
// deliberately below it: a seat whose machine placement would refuse is
// not a seat.
func TestHostSeatsTable(t *testing.T) {
	for _, c := range []struct {
		sku   string
		cores int
		gb    int64
		mem   int    // "seats memory allows"
		pub   [3]int // the proposal's p=.3, .4, .5 columns
		want  [3]int // those capped by the placement ratio
	}{
		{"D16s_v7", 8, 64, 7, [3]int{1, 1, 1}, [3]int{1, 1, 1}},
		{"D32ls_v7", 16, 64, 7, [3]int{5, 4, 3}, [3]int{5, 4, 3}},
		{"D64ls_v7", 32, 128, 14, [3]int{14, 11, 9}, [3]int{12, 11, 9}},
		{"D64s_v7", 32, 256, 30, [3]int{14, 11, 9}, [3]int{12, 11, 9}},
		{"D128ls_v7", 64, 256, 30, [3]int{30, 25, 21}, [3]int{24, 24, 21}},
		{"D192ls_v7", 96, 384, 46, [3]int{46, 40, 33}, [3]int{36, 36, 33}},
		{"D248ls_v7", 124, 496, 60, [3]int{60, 53, 44}, [3]int{46, 46, 44}},
		{"D372s_v7", 186, 1488, 184, [3]int{111, 85, 69}, [3]int{69, 69, 69}},
	} {
		memBytes := c.gb << 30
		for i, p := range []float64{0.3, 0.4, 0.5} {
			mem, cpu := hostCeilings(memBytes, c.cores, p)
			if mem != c.mem {
				t.Errorf("%s: memory allows %d, the table says %d", c.sku, mem, c.mem)
			}
			// The published figure is the memory ceiling against the
			// burst figure alone, with no placement term.
			if pub := min(mem, burstSeats(c.cores, p)); pub != c.pub[i] {
				t.Errorf("%s at p=%g: %d seats, the table says %d", c.sku, p, pub, c.pub[i])
			}
			if got := min(mem, cpu); got != c.want[i] {
				t.Errorf("%s at p=%g: sells %d seats, want %d", c.sku, p, got, c.want[i])
			}
		}
	}
}

// The placement ratio is what binds on a big host, and it is the reason
// the biggest hosts sell fewer seats than the proposal's table says.
func TestHostSeatsPlacementRatioBinds(t *testing.T) {
	for _, c := range []struct {
		cores             int
		burst, ratio, cpu int
	}{
		{8, 1, 3, 1},     // the burst figure binds
		{32, 11, 12, 11}, // still the burst figure, by one seat
		{64, 25, 24, 24}, // the ratio, from here up
		{124, 53, 46, 46},
	} {
		if got := burstSeats(c.cores, DutyCycle); got != c.burst {
			t.Errorf("%d cores: burst %d, want %d", c.cores, got, c.burst)
		}
		if got := c.cores * scheduler.PlacementVCPUPct / 100 / SeatVCPUs; got != c.ratio {
			t.Errorf("%d cores: placement allows %d seats, want %d", c.cores, got, c.ratio)
		}
		if got := cpuSeats(c.cores, DutyCycle); got != c.cpu {
			t.Errorf("%d cores: CPU ceiling %d, want %d", c.cores, got, c.cpu)
		}
	}
}

// The edges: a host that has not reported cores is bounded by memory
// alone (the write path records vcpus / 2, so this is the host that has
// not registered since I-735), a host whose memory is all reserve
// delivers nothing, and host-01 today -- a little under 64 GB and 8
// physical cores -- delivers one seat, not the six its memory allows.
func TestHostSeatsEdges(t *testing.T) {
	for _, c := range []struct {
		name            string
		memBytes        int64
		cores           int
		mem, cpu, seats int
	}{
		{"cores unknown", 64 << 30, 0, 7, 7, 7},
		{"all reserve", 8 << 30, 8, 0, 1, 0},
		{"nothing reported", 0, 0, 0, 0, 0},
		{"host-01", 63 << 30, 8, 6, 1, 1},
		{"xl-sized host", 128 << 30, 32, 14, 11, 11},
	} {
		mem, cpu := HostCeilings(c.memBytes, c.cores)
		if mem != c.mem || cpu != c.cpu {
			t.Errorf("%s: ceilings %d memory, %d cores; want %d and %d", c.name, mem, cpu, c.mem, c.cpu)
		}
		if got := HostSeats(c.memBytes, c.cores); got != c.seats {
			t.Errorf("%s: %d seats, want %d", c.name, got, c.seats)
		}
	}
}

// The tail is summed from the top term up, never as 1 - CDF: the seat
// count turns on margins of 0.0018 (a `D128ls_v7` is 25 seats at a tail
// of 0.03439 and 26 at 0.05176, either side of the 5 percent budget).
func TestBinomialTailAbove(t *testing.T) {
	for _, c := range []struct {
		n, k int
		want float64
	}{
		{2, 1, 0.16},          // D16s_v7's two seats, by hand
		{4, 3, 0.0256},        // 0.4^4
		{5, 3, 0.08704},       // 5*0.4^4*0.6 + 0.4^5
		{25, 14, 0.034391518}, // D128ls_v7 at 25 seats: inside the budget
		{26, 14, 0.051755336}, // and outside it at 26
		{53, 27, 0.039805382}, // D248ls_v7, the other 0.0018 margin
		{54, 27, 0.051770989}, // and the seat it refuses
		{1, 1, 0},             // nothing above n
		{0, 0, 0},             // no seats, no tail
	} {
		got := binomialTailAbove(c.n, c.k, 0.4)
		if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("P(Bin(%d, 0.4) > %d) = %.12f, want %.12f", c.n, c.k, got, c.want)
		}
	}
	if got := binomialTailAbove(3, -1, 0.4); got != 1 {
		t.Errorf("P(Bin(3, 0.4) > -1) = %v, want 1", got)
	}
}
