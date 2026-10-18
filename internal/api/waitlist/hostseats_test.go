package waitlist

import (
	"testing"

	"github.com/heracraft/repose/internal/api/scheduler"
)

// The seats one host delivers (DECISIONS I-739, I-784). The memory and
// the published columns are the measured table in
// docs/proposals/2026-10-14-host-cpu-capacity.md §3 at I-784's two guest
// vCPUs per seat -- the proposal's own table is at four, and its §3
// model is what these are derived from, not a measurement of a host
// packed with 2-vCPU guests, which nobody has run. They are written out
// here as figures rather than taken from the code: these numbers are the
// price floor (docs/PRICING.md "The cost floor"), so moving a constant
// has to fail a test that names what was published.
//
// `want` is what the code sells, which is the published figure capped by
// what placement will accept for the host (scheduler.PlacementVCPUPct,
// 1.5 committed guest vCPUs per core = cores * 1.5 / SeatVCPUs seats).
// The model has no placement term, so on the memory-rich `s` line `want`
// is deliberately below it: a seat whose machine placement would refuse
// is not a seat.
//
// Which ceiling binds flipped with I-784. At four vCPUs to a seat the
// CPU side bound nearly every shape; at two it is memory on the `ls`
// line, the placement ratio on the `s` line, and the burst figure only
// on the smallest hosts. The `D8s_v7` row is the one that does not move
// at all: four cores served one seat before and serve one now, so the
// change is not a uniform rise.
func TestHostSeatsTable(t *testing.T) {
	for _, c := range []struct {
		sku   string
		cores int
		gb    int64
		mem   int    // "seats memory allows"
		pub   [3]int // the proposal's p=.3, .4, .5 columns
		want  [3]int // those capped by the placement ratio
	}{
		{"D8s_v7", 4, 32, 3, [3]int{1, 1, 1}, [3]int{1, 1, 1}},
		{"D16s_v7", 8, 64, 7, [3]int{5, 4, 3}, [3]int{5, 4, 3}},
		{"D32ls_v7", 16, 64, 7, [3]int{7, 7, 7}, [3]int{7, 7, 7}},
		{"D64ls_v7", 32, 128, 14, [3]int{14, 14, 14}, [3]int{14, 14, 14}},
		{"D64s_v7", 32, 256, 30, [3]int{30, 25, 21}, [3]int{24, 24, 21}},
		{"D128ls_v7", 64, 256, 30, [3]int{30, 30, 30}, [3]int{30, 30, 30}},
		{"D192ls_v7", 96, 384, 46, [3]int{46, 46, 46}, [3]int{46, 46, 46}},
		{"D248ls_v7", 124, 496, 60, [3]int{60, 60, 60}, [3]int{60, 60, 60}},
		{"D372s_v7", 186, 1488, 184, [3]int{184, 181, 147}, [3]int{139, 139, 139}},
		// Hetzner's AX162-2, the second-provider shape of R3-20, at the
		// proposal's 240 placeable GB: CPU bound it at 17 seats before
		// I-784 and its memory binds it at 30 now.
		{"AX162-2", 48, 256, 30, [3]int{30, 30, 30}, [3]int{30, 30, 30}},
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
		{4, 1, 3, 1},     // the burst figure binds, and 4 cores still serve one seat
		{8, 4, 6, 4},     // still the burst figure
		{32, 25, 24, 24}, // the ratio, from here up
		{48, 40, 36, 36}, // an AX162-2's cores
		{124, 116, 93, 93},
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
// delivers nothing, and a host-01-shaped host -- a little under 64 GB
// and 8 physical cores -- delivers four seats where it delivered one
// before I-784, four being under the six its memory allows. The byte
// counts here are chosen, not prod's: host-01's usable memory is
// somewhat under 64 GiB and `repose-admin seats` is where its real
// figure is read.
func TestHostSeatsEdges(t *testing.T) {
	for _, c := range []struct {
		name            string
		memBytes        int64
		cores           int
		mem, cpu, seats int
	}{
		{"cores unknown", 64 << 30, 0, 7, 7, 7},
		{"all reserve", 8 << 30, 8, 0, 4, 0},
		{"nothing reported", 0, 0, 0, 0, 0},
		{"host-01 shape", 63 << 30, 8, 6, 4, 4},
		{"xl-sized host", 128 << 30, 32, 14, 24, 14},
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
// count turns on margins of 0.0018 (at a cap of 14 busy seats, 25 seats
// is a tail of 0.03439 and 26 is 0.05176, either side of the 5 percent
// budget -- that cap is a `D64s_v7`'s 32 cores since I-784 and was a
// `D128ls_v7`'s 64 before it).
func TestBinomialTailAbove(t *testing.T) {
	for _, c := range []struct {
		n, k int
		want float64
	}{
		{2, 1, 0.16},          // D16s_v7's two seats, by hand
		{4, 3, 0.0256},        // 0.4^4
		{5, 3, 0.08704},       // 5*0.4^4*0.6 + 0.4^5
		{25, 14, 0.034391518}, // a 32-core host at 25 seats: inside the budget
		{26, 14, 0.051755336}, // and outside it at 26
		{53, 27, 0.039805382}, // the other 0.0018 margin, at a cap of 27
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

// A seat is 8 GB of running memory, which is one `large`, so its vCPUs
// are that class's and the two cannot be set independently (DECISIONS
// I-739, I-784). SeatBytes says the same thing about the memory. Without
// this, halving the class table and leaving SeatVCPUs at 4 would have
// left every host's CPU ceiling at the old figure and the seat count
// quietly wrong in the direction that oversells.
func TestSeatVCPUsIsALarge(t *testing.T) {
	if SeatVCPUs != scheduler.ClassVCPU("large") {
		t.Errorf("SeatVCPUs %d, a large is %d vCPU", SeatVCPUs, scheduler.ClassVCPU("large"))
	}
	if SeatBytes != scheduler.ClassRAM("large") {
		t.Errorf("SeatBytes %d, a large is %d bytes", SeatBytes, scheduler.ClassRAM("large"))
	}
}
