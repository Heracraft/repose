// Package billing is the api's money: the plan table from docs/PRICING.md,
// the Paddle client, the webhook that keeps the subscriptions table in
// step with Paddle, the compute gate a plan buys, the hourly rollup of
// meter samples into usage_hours (the internal record of hours, disk and
// egress), the egress overage line, dunning and the seat count
// (docs/workstreams/09-billing.md §5.11, DECISIONS I-289).
//
// Paddle is the merchant of record and the ledger of what was charged;
// usage_hours is the ledger of what was used; the overage line is the one
// place the two meet.
package billing

// Plan is one row of the table in docs/PRICING.md. The numbers live here
// and in that doc and nowhere else (PRICING.md "Changing prices");
// TestPlansMatchPricingDoc parses the doc's table and fails when they
// disagree.
type Plan struct {
	ID         string // solo | plus | pro
	Name       string
	PriceCents int64
	Currency   string
	TrialDays  int
	Seats      int
	MemoryGB   int // may run at once
	DiskGB     int // may be allocated
	EgressGB   int // a period
	// IntroCents and IntroMonths are the introductory price: a first
	// subscription pays IntroCents for its first IntroMonths charges after
	// the trial, then PriceCents. Zero is no introductory price. Paddle
	// charges it as a recurring discount (DECISIONS I-497). IntroEgressGB
	// is the egress allowance a period has while the offer runs; zero
	// keeps EgressGB.
	IntroCents    int64
	IntroMonths   int
	IntroEgressGB int
}

// The three plans (DECISIONS I-362), and Solo's introductory price
// (DECISIONS I-497).
var (
	Solo = Plan{ID: "solo", Name: "Solo", PriceCents: 2900, Currency: "USD", TrialDays: 7, Seats: 1, MemoryGB: 8, DiskGB: 100, EgressGB: 250, IntroCents: 2000, IntroMonths: 3, IntroEgressGB: 100}
	Plus = Plan{ID: "plus", Name: "Plus", PriceCents: 5900, Currency: "USD", TrialDays: 7, Seats: 2, MemoryGB: 16, DiskGB: 250, EgressGB: 500}
	Pro  = Plan{ID: "pro", Name: "Pro", PriceCents: 9900, Currency: "USD", TrialDays: 7, Seats: 4, MemoryGB: 32, DiskGB: 500, EgressGB: 1000}
)

// Plans lists the plans in the order the dashboard shows them.
var Plans = []Plan{Solo, Plus, Pro}

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

// PlanByID finds a plan; ok is false for anything but solo, plus and pro.
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
// one at most: Paddle's discount is restricted to that plan's price.
func IntroPlan() (Plan, bool) {
	for _, p := range Plans {
		if p.HasIntro() {
			return p, true
		}
	}
	return Plan{}, false
}

// EgressAllowanceBytes is the plan's egress allowance in bytes.
func (p Plan) EgressAllowanceBytes() int64 { return int64(p.EgressGB) << 30 }

// EgressHardStopBytes is where the machines stop for the period.
func (p Plan) EgressHardStopBytes() int64 {
	return p.EgressAllowanceBytes() * EgressHardStopMultiplier
}

// AllowsXL reports whether an xl machine (16 GB) can run on the plan at
// all, which is what /me's `limits.xl` says.
func (p Plan) AllowsXL() bool { return p.MemoryGB >= ClassMemoryGB("xl") }

// ClassMemoryGB is the memory a size class takes while it runs
// (docs/interfaces/README.md: small 2 vCPU 4 GB, large 4 vCPU 8 GB, xl 8
// vCPU 16 GB). An unknown class counts as large.
func ClassMemoryGB(class string) int {
	switch class {
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
// allowance.
func OverageCents(plan Plan, egressBytes int64) (overGB int64, cents int64) {
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
// carries a price: the plan is charged by Paddle and the egress overage
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

// SmallestFor is the cheapest plan whose memory holds a machine of the
// class, which is what "an xl needs ..." names; Pro when none does.
func SmallestFor(class string) Plan {
	for _, p := range Plans {
		if p.MemoryGB >= ClassMemoryGB(class) {
			return p
		}
	}
	return Pro
}
