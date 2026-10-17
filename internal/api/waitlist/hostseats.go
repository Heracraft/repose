package waitlist

import (
	"math"

	"github.com/heracraft/repose/internal/api/scheduler"
)

// How many seats one host can deliver (DECISIONS I-739). A seat is 8 GB
// of running memory, and every class is 2 GB per guest vCPU (R3-2), so a
// seat is also 4 guest vCPUs. The memory ceiling is what the host's
// placeable memory divides into; the CPU ceiling is what its physical
// cores can serve while typing still feels immediate, measured on prod
// on 2026-10-14 (docs/proposals/2026-10-14-host-cpu-capacity.md §1 and
// §3: past roughly one busy guest vCPU per physical core, keystroke
// latency in every guest on the host goes from 2.7 ms to 17-45 ms). The
// host delivers the smaller of the two, and the memory ceiling alone --
// what this derivation used until I-739 -- oversold a `D64s_v7` by 19
// seats.
//
// The CPU ceiling is a peak, not a mean: seats are sold to accounts that
// use them in bursts, so the question is how many may be sold before the
// chance that more than the host can serve are busy at the same moment
// passes 5 percent. With n seats each busy an independent DutyCycle of
// the time, the busy count is Binomial(n, DutyCycle), and the host can
// serve UsableCoreFraction of its cores divided by a seat's 4 vCPUs.
//
// A seat whose machine placement would refuse is not a seat, so the CPU
// ceiling is also never more than scheduler.PlacementVCPUPct allows --
// 1.5 committed guest vCPUs per physical core (I-734, enforced by
// placement in I-736), which is cores * 1.5 / 4 seats; capping the one by
// the other is what keeps the two from disagreeing about the same host.
// The burst figure binds on small hosts and the placement ratio on large
// ones, so a `D248ls_v7` sells 46 seats where the proposal's table, which
// has no placement term, says 53.

const (
	// SeatVCPUs is a seat's guest vCPUs: 8 GB at 2 GB per vCPU (R3-2).
	SeatVCPUs = 4

	// DutyCycle is the share of its running time a seat is actually
	// consuming its allocation. Measured on prod over 14 days to
	// 2026-10-11: 48 percent for the one active account over 918
	// machine-hours and 33 percent fleet-wide over 73,059 running
	// minutes (2026-10-11-pause-idle-machines.md §5). The product's
	// pitch -- the agent keeps working after the laptop closes --
	// selects for the high end, so the planning figure is the prudent
	// 0.4 rather than the fleet's own 0.33.
	DutyCycle = 0.4

	// UsableCoreFraction is the share of a host's cores guests may have:
	// the rest is the host's own work (WireGuard crypt, virtiofsd,
	// hostd, Fluent Bit), measured at about a tenth of the host.
	UsableCoreFraction = 0.9

	// SeatTail is the chance the host is asked for more than it can
	// serve that the seat count accepts.
	SeatTail = 0.05
)

// HostSeats is how many seats one ready host delivers: the smaller of
// its memory and CPU ceilings. memBytes is the host's total memory and
// cores its physical cores. cores <= 0 is a host that has not reported
// them (a hostd older than I-735) and is bounded by memory alone, which
// is what the derivation did before I-739; the bridge for such a host is
// in the write path, where Register, Rotate and migration 0027 record
// vcpus / 2 (DESIGN.md §4: every host is Intel with two threads per
// core), so this reads one real number and the rule lives in one place.
func HostSeats(memBytes int64, cores int) int {
	mem, cpu := hostCeilings(memBytes, cores, DutyCycle)
	return min(mem, cpu)
}

// HostCeilings is the two ceilings HostSeats takes the smaller of, for
// `repose-admin seats` to report which one bound the fleet. A host that
// has not reported cores has no CPU ceiling and is reported at its
// memory ceiling, since memory alone bounds it.
func HostCeilings(memBytes int64, cores int) (mem, cpu int) {
	return hostCeilings(memBytes, cores, DutyCycle)
}

// hostCeilings is HostCeilings at a duty cycle p (the table test pins
// the published figures at 0.3, 0.4 and 0.5).
func hostCeilings(memBytes int64, cores int, p float64) (mem, cpu int) {
	mem = int(max(0, memBytes-scheduler.HostReserve(memBytes)) / SeatBytes)
	if cores <= 0 {
		return mem, mem
	}
	return mem, cpuSeats(cores, p)
}

// cpuSeats is the CPU ceiling: what the host's cores can serve in a
// burst, never more than placement will accept for them.
func cpuSeats(cores int, p float64) int {
	if cores <= 0 {
		return 0
	}
	// What placement will accept on the host (I-736), in whole seats.
	placeable := cores * scheduler.PlacementVCPUPct / 100 / SeatVCPUs
	return min(placeable, burstSeats(cores, p))
}

// burstSeats is the largest n for which the chance that more seats are
// busy at once than the host's usable cores can serve is at most
// SeatTail.
func burstSeats(cores int, p float64) int {
	// The most seats that may be busy together, in whole seats.
	busy := int(UsableCoreFraction * float64(cores) / SeatVCPUs)
	if busy < 1 {
		// A host with fewer cores than one seat needs still sells the
		// one seat it can almost serve; below that it sells none.
		if UsableCoreFraction*float64(cores) >= 1 {
			return 1
		}
		return 0
	}
	// P(Binomial(n, p) > busy) rises with n, so the first n that fails
	// bounds the answer.
	for n := busy; ; n++ {
		if binomialTailAbove(n+1, busy, p) > SeatTail {
			return n
		}
	}
}

// binomialTailAbove is P(X > k) for X ~ Binomial(n, p), summed term by
// term from k+1 upward so every term is positive. Not 1 - CDF(k): the
// tightest margin the seat table turns on is 0.0018 absolute (a
// `D128ls_v7` is 25 seats at a tail of 0.0344 and 26 at 0.0518), and
// cancellation against 1 is the wrong way to spend that. n stays in the
// low hundreds (the memory ceiling of the biggest Intel host Azure sells
// is 184 seats), so the pmf's recurrence in float64 is exact enough and
// nothing underflows.
func binomialTailAbove(n, k int, p float64) float64 {
	if k >= n {
		return 0
	}
	if k < 0 {
		return 1
	}
	// pmf(0) = (1-p)^n, then pmf(i+1) = pmf(i) * (n-i)/(i+1) * p/(1-p).
	q := 1 - p
	pmf := math.Pow(q, float64(n))
	var tail float64
	for i := 0; i < n; i++ {
		pmf *= float64(n-i) / float64(i+1) * p / q
		if i+1 > k {
			tail += pmf
		}
	}
	return tail
}
