// Package billing is the api's money: the plan table from docs/PRICING.md,
// the Polar client, the webhook that keeps the subscriptions table in
// step with Polar, the compute gate a plan buys, the hourly rollup of
// meter samples into usage_hours (the internal record of hours, disk and
// egress), the egress overage line, dunning and the seat count
// (docs/workstreams/09-billing.md §5.11, DECISIONS I-289, I-604).
//
// Polar is the merchant of record and the ledger of what was charged;
// usage_hours is the ledger of what was used; the overage line is the one
// place the two meet.
package billing

import (
	"fmt"
	"math"
)

// Plan is one row of the table in docs/PRICING.md. The numbers live here
// and in that doc and nowhere else (PRICING.md "Changing prices");
// TestPlansMatchPricingDoc parses the doc's table and fails when they
// disagree.
type Plan struct {
	ID         string // solo | plus | pro | own
	Name       string
	PriceCents int64
	Currency   string
	TrialDays  int
	Seats      int
	MemoryGB   int // may run at once; 0 on an own-hardware plan
	DiskGB     int // may be allocated; 0 on an own-hardware plan
	EgressGB   int // a period; 0 on an own-hardware plan
	// Hosts is how many hosts of their own the plan lets the account add
	// (DECISIONS I-1364, I-1366): UnlimitedHosts on Own hardware since
	// I-1594, none on the plans that sell memory on repose's own fleet.
	// `POST /hosts` and /me's `limits.hosts` read this and nothing else
	// holds the figure, so a plan's host count is one number here.
	//
	// A plan with Hosts > 0 sells no fleet memory, disk or egress at all:
	// the host the user owns is the only limit, so MemoryGB, DiskGB and
	// EgressGB are 0 and none of the gate's three resource checks runs
	// (Gate.check). What keeps such an account off a host repose bought
	// is placement, through Gate.FleetAllowed, not the gate's refusals.
	Hosts int
	// BurstCoreHours is the CPU time above a machine's base cores that
	// the plan includes in a period (DECISIONS I-1128). Past it, with
	// overage off, the account's machines run on their base cores until
	// the period ends; nothing stops. STARTING NUMBERS: 20, 50 and 120
	// are the council's, to be set from the first two weeks of real
	// burst data, because the quota hides burst demand today.
	BurstCoreHours int
	// IntroCents and IntroMonths are the introductory price: a first
	// subscription pays IntroCents for its first IntroMonths charges after
	// the trial, then PriceCents. Zero is no introductory price. Polar
	// charges it as a repeating discount (DECISIONS I-497, I-604). IntroEgressGB
	// is the egress allowance a period has while the offer runs; zero
	// keeps EgressGB.
	IntroCents    int64
	IntroMonths   int
	IntroEgressGB int
	// IntroLimit is how many subscriptions may carry the introductory
	// price at once: the founding offer's first 30 (DECISIONS I-672). Zero
	// is no limit.
	IntroLimit int
}

// The three hosted plans (DECISIONS I-362) at ladder C's prices, Solo's
// founding price for the first 30 subscribers (DECISIONS I-672), and the
// plan for a host the user owns (DECISIONS I-1364).
var (
	Solo = Plan{ID: "solo", Name: "Solo", PriceCents: 4900, Currency: "USD", TrialDays: 7, Seats: 1, MemoryGB: 8, DiskGB: 100, EgressGB: 250, BurstCoreHours: 20, IntroCents: 3900, IntroMonths: 12, IntroLimit: 30}
	Plus = Plan{ID: "plus", Name: "Plus", PriceCents: 8900, Currency: "USD", TrialDays: 7, Seats: 2, MemoryGB: 16, DiskGB: 250, EgressGB: 500, BurstCoreHours: 50}
	Pro  = Plan{ID: "pro", Name: "Pro", PriceCents: 16900, Currency: "USD", TrialDays: 7, Seats: 4, MemoryGB: 32, DiskGB: 500, EgressGB: 1000, BurstCoreHours: 120}
	// Own is the Mac minis or x86 boxes the user owns: as many hosts as
	// they add (DECISIONS I-1594), no seat on the fleet, and no memory,
	// disk or egress of ours to sell, because the machines are theirs.
	// The hosted plans are untouched by it.
	Own = Plan{ID: "own", Name: "Own hardware", PriceCents: 1000, Currency: "USD", TrialDays: 7, Hosts: UnlimitedHosts}
)

// UnlimitedHosts is the Hosts figure of a plan that sells as many hosts
// as the account adds (DECISIONS I-1594). It is a finite number, not a
// sentinel such as -1 or 0, so that every reader that compares reads it
// right without knowing about it: `POST /hosts`'s count never reaches it,
// Plan.OwnHardware's `Hosts > 0` still says the plan sells hosts, and 0
// keeps its one meaning, a plan that sells none. Only a reader that
// prints the figure must know it, and says "any number" (HostsPhrase).
// It is math.MaxInt32 so that a 32-bit reader and JavaScript hold it
// exactly. apps/web/src/lib/plans.ts's UNLIMITED_HOSTS is the same
// number, and plans.test.ts holds the two equal.
const UnlimitedHosts = math.MaxInt32

// HostsPhrase is a hosts figure as the product says it: "one host",
// "3 hosts", or "any number of hosts" for UnlimitedHosts.
func HostsPhrase(n int) string {
	switch {
	case n >= UnlimitedHosts:
		return "any number of hosts"
	case n == 1:
		return "one host"
	}
	return fmt.Sprintf("%d hosts", n)
}

// Plans lists the plans in the order the dashboard shows them: the three
// hosted ones, then Own hardware.
var Plans = []Plan{Solo, Plus, Pro, Own}

// HostedPlans are the plans that sell memory on repose's own fleet: every
// plan but the own-hardware ones. The seat count, the waitlist and the
// landing's three cards are theirs.
func HostedPlans() []Plan {
	out := make([]Plan, 0, len(Plans))
	for _, p := range Plans {
		if !p.OwnHardware() {
			out = append(out, p)
		}
	}
	return out
}

// OwnHardware reports whether the plan sells a host the user owns
// (DECISIONS I-1364). It answers "which product is this", and nothing
// else: the Polar product and its description, the proration kind
// (SameKind), and which plan the three cards leave out. It is the wrong
// question for "does this plan sell repose's compute", which is
// SellsFleetMemory, and keying a resource gate off it is what let a
// plan with a host and fleet memory skip the gate while placement let
// it onto the fleet.
func (p Plan) OwnHardware() bool { return p.Hosts > 0 }

// SellsFleetMemory reports whether the plan sells memory on the hosts
// repose runs. It is the one predicate for the fleet, asked twice: the
// gate skips its memory, disk and egress checks when it is false
// (Gate.check), and placement refuses the fleet when it is false
// (Gate.FleetAllowed, DECISIONS I-1366). The two must answer the same
// question, because the gate's skip is only safe while placement keeps
// such an account off the fleet; a plan that sold both a host and fleet
// memory would otherwise be placed on repose's hosts and metered by
// nothing.
func (p Plan) SellsFleetMemory() bool { return p.MemoryGB > 0 }

// OwnPlan is the own-hardware plan, if the table has one.
func OwnPlan() (Plan, bool) {
	for _, p := range Plans {
		if p.OwnHardware() {
			return p, true
		}
	}
	return Plan{}, false
}

// SameKind reports whether two plans sell the same kind of thing: both
// hosted, or both own-hardware. A change across the two is refused
// (service.ErrPlanKind), because Polar would prorate between products
// that buy nothing alike (DECISIONS I-1364).
func SameKind(a, b Plan) bool { return a.OwnHardware() == b.OwnHardware() }

// ProjectCap is how many live projects, running or stopped, an account
// may have, on every plan and without one (DECISIONS I-569). It is an
// abuse bound, not a price: memory caps what runs and disk caps what is
// kept, so a plan sells no project count. users.project_limit can raise
// it for one account and never lowers it (AccountProjectCap).
const ProjectCap = 100

// AccountProjectCap is the account's project cap: ProjectCap, or the
// operator-set users.project_limit when that is higher. Values below
// ProjectCap were set when the limit was a plan's and counted stopped
// projects (6 for the owner, 3 and 10 for early accounts), so they no
// longer bind.
func AccountProjectCap(userLimit int) int {
	return max(ProjectCap, userLimit)
}

const (
	// EgressHardStopMultiplier is how far past the allowance a period's
	// egress may go before the user's machines stop for the rest of it
	// (PRICING.md "Egress": the stolen-card ceiling, not a price).
	EgressHardStopMultiplier = 4
	// OveragePerGBCents is the price of a GB of egress past the allowance,
	// added to the next invoice as one line.
	OveragePerGBCents int64 = 5
	// SeatGB is the memory a seat stands for (DECISIONS I-290).
	SeatGB = 8
	// DefaultPeriodHours is a period's length when nothing better is known.
	DefaultPeriodHours = 720
)

// PriceVersion stamps every usage_hours row. Rows carry no compute price
// from plan-v1 on: cost_cents and egress_cents are 0, and the overage is
// computed per period from the rows (PRICING.md "Changing prices"; rows
// are never repriced).
const PriceVersion = "plan-v1"

// PlanByID finds a plan; ok is false for anything but solo, plus, pro
// and own.
func PlanByID(id string) (Plan, bool) {
	for _, p := range Plans {
		if p.ID == id {
			return p, true
		}
	}
	return Plan{}, false
}

// HasIntro reports whether the plan has an introductory price.
func (p Plan) HasIntro() bool { return p.IntroCents > 0 && p.IntroMonths > 0 }

// IntroDiscountCents is the flat amount the introductory discount takes
// off each of the first IntroMonths charges.
func (p Plan) IntroDiscountCents() int64 {
	if !p.HasIntro() {
		return 0
	}
	return p.PriceCents - p.IntroCents
}

// IntroPlan is the plan with an introductory price, if any. One plan has
// one at most: Polar's discount is restricted to that plan's product.
func IntroPlan() (Plan, bool) {
	for _, p := range Plans {
		if p.HasIntro() {
			return p, true
		}
	}
	return Plan{}, false
}

// BurstAllowanceNs is the plan's burst allowance in nanoseconds of CPU
// time above a machine's base cores.
func (p Plan) BurstAllowanceNs() int64 { return int64(p.BurstCoreHours) * 3600 * 1_000_000_000 }

// EgressMetered reports whether the plan counts egress at all. An
// own-hardware plan does not: a guest on the user's own host sends its
// traffic over the user's own connection, so there is no allowance, no
// overage line and no hard stop (DECISIONS I-1364).
func (p Plan) EgressMetered() bool { return p.EgressGB > 0 }

// EgressAllowanceBytes is the plan's egress allowance in bytes.
func (p Plan) EgressAllowanceBytes() int64 { return int64(p.EgressGB) << 30 }

// EgressHardStopBytes is where the machines stop for the period; 0 on a
// plan that meters no egress, where nothing stops (EgressMetered).
func (p Plan) EgressHardStopBytes() int64 {
	return p.EgressAllowanceBytes() * EgressHardStopMultiplier
}

// AllowsXL reports whether an xl machine (16 GB) can run on the plan at
// all, which is what /me's `limits.xl` says. A plan with a host of the
// user's own allows every class whatever its fleet memory, because what
// fits on that host is the host's memory; on a plan that sells both,
// the gate still refuses an xl that does not fit the fleet memory, so
// the generous answer here is right for either shape.
func (p Plan) AllowsXL() bool { return p.OwnHardware() || p.MemoryGB >= ClassMemoryGB("xl") }

// ClassMemoryGB is the memory a size class takes while it runs
// (docs/DESIGN.md §5: tiny 0.5 vCPU 2 GB, small 1 vCPU 4 GB, large 2 vCPU
// 8 GB, xl 4 vCPU 16 GB). An unknown class counts as large. The memory is
// what a plan sells and what I-784 left alone; the CPU is
// scheduler.ClassMilliVCPU's.
//
// A plan sells memory that may run at once and no machine count (I-289),
// so `tiny` needs no plan rule of its own: it takes its 2 GB as every
// class takes its own, and Solo's 8 GB therefore runs four of them
// (DECISIONS I-1168).
func ClassMemoryGB(class string) int {
	switch class {
	case "tiny":
		return 2
	case "small":
		return 4
	case "xl":
		return 16
	default:
		return 8
	}
}

// OverageCents is the egress line for a period: whole GB past the
// allowance, rounded up, at OveragePerGBCents. Nothing under the
// allowance, and nothing at all on a plan that meters no egress.
func OverageCents(plan Plan, egressBytes int64) (overGB int64, cents int64) {
	if !plan.EgressMetered() {
		return 0, 0
	}
	over := egressBytes - plan.EgressAllowanceBytes()
	if over <= 0 {
		return 0, 0
	}
	overGB = (over + (1<<30 - 1)) >> 30
	return overGB, overGB * OveragePerGBCents
}

// Inputs is one hour of one project as the rollup measured it.
type Inputs struct {
	Class          string
	RunningSeconds int
	GBAlloc        int64
	EgressBytes    int64
}

// Result is the hour as written to usage_hours. Since plan-v1 no hour
// carries a price: the plan is charged by Polar and the egress overage
// is computed per period from the egress_bytes column, so every cents
// field is zero and stays in the row only because the columns exist.
type Result struct {
	RunningSeconds int
	GBAlloc        int64
	EgressBytes    int64
	CostCents      int64
	GuestCents     int64
	StorageCents   int64
	EgressCents    int64
}

// Price normalises one hour: seconds clamped to the hour, negative bytes
// to zero, every cents field zero.
func Price(in Inputs) Result {
	r := Result{RunningSeconds: in.RunningSeconds, GBAlloc: in.GBAlloc, EgressBytes: in.EgressBytes}
	if r.RunningSeconds > 3600 {
		r.RunningSeconds = 3600
	}
	if r.RunningSeconds < 0 {
		r.RunningSeconds = 0
	}
	if r.GBAlloc < 0 {
		r.GBAlloc = 0
	}
	if r.EgressBytes < 0 {
		r.EgressBytes = 0
	}
	return r
}

// SmallestFor is the cheapest hosted plan whose memory holds a machine of
// the class, which is what "an xl needs ..." names; Pro when none does.
// An own-hardware plan is never the answer: it sells no memory.
func SmallestFor(class string) Plan {
	for _, p := range HostedPlans() {
		if p.MemoryGB >= ClassMemoryGB(class) {
			return p
		}
	}
	return Pro
}
