package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/api/waitlist"
)

// Plans, seats and the waitlist as docs/interfaces/api.md "Usage and
// billing" describes them (DECISIONS I-289, I-290). The numbers are
// docs/PRICING.md's; the fake keeps them here rather than importing
// internal/billing so that a change to the real table is a deliberate
// change to the contract the dashboard tests run against.

// Billing modes for SetBilling. "off" is the api without PADDLE_API_KEY:
// every billing route answers 503 billing_disabled and the account is
// exempt, so compute is allowed (the default, which is what every test
// that is not about billing wants). The other modes have billing on.
const (
	BillingOff       = "off"
	BillingNone      = "none"      // an account with no plan yet
	BillingTrial     = "trial"     // a trialing subscription
	BillingActive    = "active"    // a paid subscription
	BillingPastDue   = "past_due"  // the last payment failed, day 0 to 3
	BillingSuspended = "suspended" // three days past due; machines were stopped
	BillingExempt    = "exempt"    // billing on, this account is exempt
)

var billingModes = map[string]bool{
	BillingOff: true, BillingNone: true, BillingTrial: true, BillingActive: true,
	BillingPastDue: true, BillingSuspended: true, BillingExempt: true,
}

// PlanDef is one row of GET /billing's plans (docs/PRICING.md).
type PlanDef struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PriceCents   int64  `json:"price_cents"`
	Currency     string `json:"currency"`
	TrialDays    int    `json:"trial_days"`
	Seats        int    `json:"seats"`
	MemoryGB     int    `json:"memory_gb"`
	DiskGB       int    `json:"disk_gb"`
	EgressGB     int    `json:"egress_gb"`
	ProjectLimit int    `json:"project_limit"`
	// IntroPriceCents and IntroMonths are the introductory price; 0 for
	// none (DECISIONS I-497).
	IntroPriceCents int64 `json:"intro_price_cents"`
	IntroMonths     int   `json:"intro_months"`
	IntroEgressGB   int   `json:"intro_egress_gb"`
}

// Plans is docs/PRICING.md's table.
var Plans = []PlanDef{
	{ID: "solo", Name: "Solo", PriceCents: 2900, Currency: "USD", TrialDays: 7, Seats: 1, MemoryGB: 8, DiskGB: 100, EgressGB: 250, ProjectLimit: 10, IntroPriceCents: 2000, IntroMonths: 3, IntroEgressGB: 100},
	{ID: "plus", Name: "Plus", PriceCents: 5900, Currency: "USD", TrialDays: 7, Seats: 2, MemoryGB: 16, DiskGB: 250, EgressGB: 500, ProjectLimit: 25},
	{ID: "pro", Name: "Pro", PriceCents: 9900, Currency: "USD", TrialDays: 7, Seats: 4, MemoryGB: 32, DiskGB: 500, EgressGB: 1000, ProjectLimit: 50},
}

// classGB is each size class's memory, the unit a plan counts
// (docs/interfaces/README.md).
var classGB = map[string]int{"small": 4, "large": 8, "xl": 16}

// overageCentsPerGB is the egress price past the allowance.
const overageCentsPerGB = 5

// egressStopFactor is the multiple of the allowance at which machines stop.
const egressStopFactor = 4

// FakeClientToken and FakeEnvironment are what GET /billing and the
// checkout answer under paddle; "fake" tells the dashboard to use its
// window.__reposePaddleStub instead of loading Paddle.js.
const (
	FakeClientToken = "test_fake_client_token"
	FakeEnvironment = "fake"
	portalURL       = "https://customer-portal.paddle.com/cpl_fake"
)

// Invoice is one row of GET /billing/invoices (a Paddle transaction).
type Invoice struct {
	ID            string    `json:"id"`
	Number        string    `json:"number"`
	Status        string    `json:"status"`
	Currency      string    `json:"currency"`
	AmountCents   int64     `json:"amount_cents"`
	SubtotalCents int64     `json:"subtotal_cents"`
	TaxCents      int64     `json:"tax_cents"`
	CreatedAt     time.Time `json:"created_at"`
	PeriodStart   time.Time `json:"period_start"`
	PeriodEnd     time.Time `json:"period_end"`
	HostedURL     *string   `json:"hosted_url"`
	PDFURL        *string   `json:"pdf_url"`
}

// WaitlistPlace is the user's place, as GET /me and GET /billing show it.
type WaitlistPlace struct {
	Position  int        `json:"position"`
	JoinedAt  time.Time  `json:"joined_at"`
	InvitedAt *time.Time `json:"invited_at"`
	HoldUntil *time.Time `json:"hold_until"`
}

// Seats is GET /public/seats plus held, as GET /billing shows it.
type Seats struct {
	Total   int `json:"total"`
	Held    int `json:"held"`
	Free    int `json:"free"`
	Waiting int `json:"waiting"`
}

// BillingState is every knob a test turns, in one struct: cmd/fakeapi's
// admin POST /billing takes it as JSON and SetBillingState applies the
// fields that are set.
type BillingState struct {
	// Mode is one of the Billing* constants.
	Mode *string `json:"mode,omitempty"`
	// Plan is solo, plus or pro; ignored unless the mode has a subscription.
	Plan *string `json:"plan,omitempty"`
	// ScheduledPlan is a downgrade waiting for the renewal; "" clears it.
	ScheduledPlan *string `json:"scheduled_plan,omitempty"`
	// Cancelled sets or clears cancel_at (the period's end).
	Cancelled *bool `json:"cancelled,omitempty"`
	// Seats sets total, held and waiting; free is total minus held.
	Seats *struct {
		Total   int `json:"total"`
		Held    int `json:"held"`
		Waiting int `json:"waiting"`
	} `json:"seats,omitempty"`
	// Waitlist puts the user on the list (or, with Position 0, takes them
	// off). Invited adds invited_at now and hold_until now + HoldHours
	// (72 when zero); a negative HoldHours is an expired hold.
	Waitlist *struct {
		Position  int  `json:"position"`
		Invited   bool `json:"invited"`
		HoldHours int  `json:"hold_hours"`
	} `json:"waitlist,omitempty"`
	// EgressGB is this period's egress.
	EgressGB *float64 `json:"egress_gb,omitempty"`
	// Invoices replaces the list; nil keeps it, an empty list empties it.
	Invoices *[]Invoice `json:"invoices,omitempty"`
	// IntroUsed marks the account as having had a subscription before, so
	// it gets no introductory price (DECISIONS I-497).
	IntroUsed *bool `json:"intro_used,omitempty"`
}

type billingState struct {
	mode          string
	plan          string
	scheduledPlan string
	cancelAt      *time.Time
	periodStart   time.Time
	periodEnd     time.Time
	trialEnd      *time.Time
	seatsTotal    int
	seatsHeld     int
	waiting       int
	waitlist      *WaitlistPlace
	egressGB      float64
	invoices      *[]Invoice
	introUsed     bool
	checkouts     map[string]string // transaction id -> plan
	txnSeq        int
}

func (f *Fake) initBilling() {
	f.bill = billingState{
		mode:       BillingOff,
		seatsTotal: 30,
		seatsHeld:  12,
		checkouts:  map[string]string{},
	}
	f.startPeriod()
}

// startPeriod dates a subscription from now: a week's trial, a month's period.
func (f *Fake) startPeriod() {
	now := f.now()
	f.bill.periodStart = now
	f.bill.periodEnd = now.AddDate(0, 1, 0)
	te := now.AddDate(0, 0, 7)
	f.bill.trialEnd = &te
}

// SetBilling switches the billing mode at run time, so one fake serves a
// browser test of every state of the billing page. The subscription modes
// default to Solo; SetPlan changes it.
func (f *Fake) SetBilling(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setMode(mode)
}

func (f *Fake) setMode(mode string) {
	if !billingModes[mode] {
		panic("fakes/api: unknown billing mode " + mode) // test fixture; a typo in a test is a bug in the test
	}
	f.bill.mode = mode
	if f.hasSubscription() {
		if f.bill.plan == "" {
			f.bill.plan = "solo"
		}
		f.bill.waitlist = nil
	} else {
		f.bill.plan = ""
		f.bill.scheduledPlan = ""
		f.bill.cancelAt = nil
	}
	switch mode {
	case BillingTrial:
		f.startPeriod()
	case BillingActive, BillingPastDue, BillingSuspended:
		f.startPeriod()
		f.bill.trialEnd = nil
	}
}

// SetPlan puts the subscription on solo, plus or pro.
func (f *Fake) SetPlan(plan string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if planByID(plan) == nil {
		panic("fakes/api: unknown plan " + plan) // test fixture
	}
	f.bill.plan = plan
}

// SetSeats sets the fleet's seats: total, held and the number waiting.
func (f *Fake) SetSeats(total, held, waiting int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bill.seatsTotal, f.bill.seatsHeld, f.bill.waiting = total, held, waiting
}

// SetWaitlist puts the user at a place on the waitlist; position 0 takes
// them off. A non-zero holdUntil marks them invited with that hold.
func (f *Fake) SetWaitlist(position int, holdUntil time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setWaitlist(position, holdUntil)
}

func (f *Fake) setWaitlist(position int, holdUntil time.Time) {
	if position <= 0 {
		f.bill.waitlist = nil
		return
	}
	place := &WaitlistPlace{Position: position, JoinedAt: f.now().Add(-time.Hour)}
	if !holdUntil.IsZero() {
		inv := holdUntil.Add(-72 * time.Hour)
		place.InvitedAt = &inv
		hu := holdUntil
		place.HoldUntil = &hu
	}
	f.bill.waitlist = place
}

// SetWaitlisted is the pre-I-290 knob, kept under its name: the user has no
// plan, the fleet has no free seat, and the user is at the given place on
// the waitlist, so a compute gate answers payment_required
// subscription_required with the place in detail.waitlist. 0 turns it off
// and makes the account exempt again.
func (f *Fake) SetWaitlisted(position int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if position <= 0 {
		f.setMode(BillingOff)
		f.bill.waitlist = nil
		return
	}
	f.setMode(BillingNone)
	f.bill.seatsHeld = f.bill.seatsTotal
	f.setWaitlist(position, time.Time{})
}

// SetEgress sets this period's egress in GB.
func (f *Fake) SetEgress(gb float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bill.egressGB = gb
}

// SetInvoices replaces GET /billing/invoices' list.
func (f *Fake) SetInvoices(list []Invoice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bill.invoices = &list
}

// SetBillingState applies every field of s that is set.
func (f *Fake) SetBillingState(s BillingState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.Mode != nil {
		if !billingModes[*s.Mode] {
			return fmt.Errorf("mode: one of off, none, trial, active, past_due, suspended, exempt")
		}
		f.setMode(*s.Mode)
	}
	if s.Plan != nil {
		if planByID(*s.Plan) == nil {
			return fmt.Errorf("plan: solo, plus or pro")
		}
		f.bill.plan = *s.Plan
	}
	if s.ScheduledPlan != nil {
		if *s.ScheduledPlan != "" && planByID(*s.ScheduledPlan) == nil {
			return fmt.Errorf("scheduled_plan: solo, plus, pro or empty")
		}
		f.bill.scheduledPlan = *s.ScheduledPlan
	}
	if s.Cancelled != nil {
		if *s.Cancelled {
			end := f.subscriptionEnd()
			f.bill.cancelAt = &end
		} else {
			f.bill.cancelAt = nil
		}
	}
	if s.Seats != nil {
		f.bill.seatsTotal, f.bill.seatsHeld, f.bill.waiting = s.Seats.Total, s.Seats.Held, s.Seats.Waiting
	}
	if s.Waitlist != nil {
		var hold time.Time
		if s.Waitlist.Invited {
			h := s.Waitlist.HoldHours
			if h == 0 {
				h = 72
			}
			hold = f.now().Add(time.Duration(h) * time.Hour)
		}
		f.setWaitlist(s.Waitlist.Position, hold)
	}
	if s.EgressGB != nil {
		f.bill.egressGB = *s.EgressGB
	}
	if s.IntroUsed != nil {
		f.bill.introUsed = *s.IntroUsed
	}
	if s.Invoices != nil {
		list := *s.Invoices
		f.bill.invoices = &list
	}
	return nil
}

// CompleteCheckout stands for Paddle's subscription.created webhook after
// the transaction a checkout opened was paid: the account is trialing on
// the plan the checkout named. Unknown transaction ids are an error, so a
// test that completes the wrong one finds out.
func (f *Fake) CompleteCheckout(transactionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	plan, ok := f.bill.checkouts[transactionID]
	if !ok {
		return fmt.Errorf("no open checkout %q", transactionID)
	}
	delete(f.bill.checkouts, transactionID)
	// The invitation's held seat becomes the subscription's; a waiting
	// user converts and leaves the count.
	held := planByID(plan).Seats
	if f.holdActive() {
		held--
	}
	if f.bill.waitlist != nil && f.bill.waiting > 0 {
		f.bill.waiting--
	}
	f.setMode(BillingTrial)
	f.bill.plan = plan
	f.bill.seatsHeld += held
	f.bill.waitlist = nil
	return nil
}

func planByID(id string) *PlanDef {
	for i := range Plans {
		if Plans[i].ID == id {
			return &Plans[i]
		}
	}
	return nil
}

func (f *Fake) billingOn() bool { return f.bill.mode != BillingOff }

func (f *Fake) hasSubscription() bool {
	switch f.bill.mode {
	case BillingTrial, BillingActive, BillingPastDue, BillingSuspended:
		return true
	}
	return false
}

// currentPlan is the subscription's plan as it applies to this period:
// with the introductory egress allowance while the offer runs (I-497).
func (f *Fake) currentPlan() *PlanDef {
	if !f.hasSubscription() {
		return nil
	}
	p := planByID(f.bill.plan)
	if p == nil {
		return nil
	}
	cp := *p
	if f.introRuns(&cp) && cp.IntroEgressGB > 0 {
		cp.EgressGB = cp.IntroEgressGB
	}
	return &cp
}

// introRuns reports whether the subscription is inside its introductory
// offer: a plan with one, on an account that had no subscription before.
// The fake's period always starts inside the offer's months.
func (f *Fake) introRuns(p *PlanDef) bool {
	return p.IntroMonths > 0 && !f.bill.introUsed
}

func (f *Fake) subscriptionEnd() time.Time {
	if f.bill.mode == BillingTrial && f.bill.trialEnd != nil {
		return *f.bill.trialEnd
	}
	return f.bill.periodEnd
}

func (f *Fake) seatsFree() int {
	free := f.bill.seatsTotal - f.bill.seatsHeld
	if free < 0 {
		return 0
	}
	return free
}

// holdActive is whether the user's waitlist invitation still holds a seat.
func (f *Fake) holdActive() bool {
	w := f.bill.waitlist
	return w != nil && w.HoldUntil != nil && w.HoldUntil.After(f.now())
}

// seatsFor is how many free seats this user can count on for a checkout:
// the fleet's free seats plus the one their own invitation holds.
func (f *Fake) seatsFor() int {
	n := f.seatsFree()
	if f.holdActive() {
		n++
	}
	return n
}

// Usage of the plan by this user's projects.

func (f *Fake) runningGB(u *userRec, exclude *project) int {
	n := 0
	for _, p := range f.userProjects(u) {
		if p == exclude {
			continue
		}
		switch p.State {
		case "running", "starting", "restoring", "creating", "building":
			n += classGB[p.Class]
		}
	}
	return n
}

func (f *Fake) diskAllocatedGB(u *userRec) float64 {
	var b int64
	for _, p := range f.userProjects(u) {
		b += p.VolumeBytes
	}
	return float64(b) / (1 << 30)
}

func (f *Fake) runningSlugs(u *userRec, exclude *project) []string {
	var out []string
	for _, p := range f.userProjects(u) {
		if p == exclude {
			continue
		}
		switch p.State {
		case "running", "starting", "restoring", "creating", "building":
			out = append(out, p.Slug)
		}
	}
	sort.Strings(out)
	return out
}

func (f *Fake) overageCents(plan *PlanDef) int64 {
	if plan == nil {
		return 0
	}
	over := f.bill.egressGB - float64(plan.EgressGB)
	if over <= 0 {
		return 0
	}
	return int64(math.Ceil(over * overageCentsPerGB))
}

// gate is every compute gate (api.md "payment_required"): the class about
// to run (or "" when nothing starts) and the disk about to be allocated.
// exclude is the project whose own class is being counted, so a restart
// of a running project does not count itself twice.
func (f *Fake) gate(u *userRec, class string, addDiskBytes int64, exclude *project) *apiError {
	switch f.bill.mode {
	case BillingOff, BillingExempt:
		return nil
	case BillingNone:
		detail := map[string]any{"reason": "subscription_required", "waitlist": nil}
		msg := "Choose a plan at https://repose.herakraft.co/billing before a machine can start."
		if w := f.bill.waitlist; w != nil {
			detail["waitlist"] = map[string]any{"position": w.Position, "joined_at": w.JoinedAt}
			// The api's gate says the same while the user waits (billing/gate.go).
			msg = waitlist.Message(w.Position, u.Email)
		}
		return errf("payment_required", "%s", msg).withDetail(detail)
	case BillingPastDue:
		return errf("payment_required", "Your last payment failed. Update your card at https://repose.herakraft.co/billing; machines already running keep running.").
			withDetail(map[string]any{"reason": "past_due"})
	case BillingSuspended:
		return errf("payment_required", "Your account is suspended: the payment failed three days ago and your machines were stopped. Pay the invoice at https://repose.herakraft.co/billing to start them again.").
			withDetail(map[string]any{"reason": "suspended"})
	}
	plan := f.currentPlan()
	if plan == nil {
		return nil
	}
	if f.bill.egressGB >= float64(egressStopFactor*plan.EgressGB) {
		return errf("payment_required", "Your machines are stopped until %s: egress this period is %.0f GB, four times %s's %d GB allowance.",
			f.bill.periodEnd.Format("2006-01-02"), f.bill.egressGB, plan.Name, plan.EgressGB).
			withDetail(map[string]any{"reason": "egress_limit", "plan": plan.ID, "limit_gb": egressStopFactor * plan.EgressGB,
				"used_gb": f.bill.egressGB, "until": f.bill.periodEnd})
	}
	if class != "" {
		used := f.runningGB(u, exclude)
		if used+classGB[class] > plan.MemoryGB {
			slugs := f.runningSlugs(u, exclude)
			using := "nothing else is running"
			if len(slugs) > 0 {
				using = strings.Join(slugs, ", ") + " " + pluralIs(len(slugs)) + " using it"
			}
			return errf("payment_required", "A %s (%d GB) would pass the %d GB of memory %s gives running machines; %s. Stop one or upgrade.",
				class, classGB[class], plan.MemoryGB, plan.Name, using).
				withDetail(map[string]any{"reason": "plan_limit", "plan": plan.ID, "limit_gb": plan.MemoryGB, "used_gb": used, "projects": slugs})
		}
	}
	if addDiskBytes > 0 {
		used := f.diskAllocatedGB(u)
		add := float64(addDiskBytes) / (1 << 30)
		if used+add > float64(plan.DiskGB) {
			return errf("payment_required", "Another %.0f GB of disk would pass %s's %d GB (%.0f GB allocated). Destroy a project, shrink the request or upgrade.",
				add, plan.Name, plan.DiskGB, used).
				withDetail(map[string]any{"reason": "disk_limit", "plan": plan.ID, "limit_gb": plan.DiskGB, "used_gb": used})
		}
	}
	return nil
}

func pluralIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// Views.

type subscriptionView struct {
	Plan          string     `json:"plan"`
	Status        string     `json:"status"`
	Seats         int        `json:"seats"`
	PeriodStart   time.Time  `json:"period_start"`
	PeriodEnd     time.Time  `json:"period_end"`
	NextBilledAt  *time.Time `json:"next_billed_at"`
	TrialEnd      *time.Time `json:"trial_end"`
	CancelAt      *time.Time `json:"cancel_at"`
	ScheduledPlan *string    `json:"scheduled_plan"`
	// NextChargeCents and IntroUntil as the api answers them (I-497).
	NextChargeCents *int64     `json:"next_charge_cents"`
	IntroUntil      *time.Time `json:"intro_until"`
}

type usageView struct {
	RunningGB        int     `json:"running_gb"`
	MemoryGB         int     `json:"memory_gb"`
	DiskAllocatedGB  float64 `json:"disk_allocated_gb"`
	DiskGB           int     `json:"disk_gb"`
	EgressGB         float64 `json:"egress_gb"`
	EgressIncludedGB int     `json:"egress_included_gb"`
	OverageCents     int64   `json:"overage_cents"`
	Projects         int     `json:"projects"`
	ProjectLimit     int     `json:"project_limit"`
}

type planView struct {
	PlanDef
	Available bool `json:"available"`
}

type billingResp struct {
	Subscription  *subscriptionView `json:"subscription"`
	Usage         usageView         `json:"usage"`
	Plans         []planView        `json:"plans"`
	IntroEligible bool              `json:"intro_eligible"`
	Seats         Seats             `json:"seats"`
	Waitlist      *WaitlistPlace    `json:"waitlist"`
	Paddle        struct {
		Environment string `json:"environment"`
		ClientToken string `json:"client_token"`
	} `json:"paddle"`
}

func (f *Fake) subscriptionOf() *subscriptionView {
	plan := f.currentPlan()
	if plan == nil {
		return nil
	}
	status := map[string]string{BillingTrial: "trialing", BillingActive: "active", BillingPastDue: "past_due", BillingSuspended: "past_due"}[f.bill.mode]
	v := &subscriptionView{Plan: plan.ID, Status: status, Seats: plan.Seats,
		PeriodStart: f.bill.periodStart, PeriodEnd: f.bill.periodEnd, CancelAt: f.bill.cancelAt}
	if f.bill.cancelAt == nil {
		next := f.subscriptionEnd()
		v.NextBilledAt = &next
	}
	if f.bill.mode == BillingTrial {
		v.TrialEnd = f.bill.trialEnd
	}
	if f.bill.scheduledPlan != "" {
		s := f.bill.scheduledPlan
		v.ScheduledPlan = &s
	}
	// A Solo subscription on an account that had none before carries the
	// introductory price for IntroMonths charges after the trial.
	if v.NextBilledAt != nil {
		cents := plan.PriceCents
		if f.introRuns(plan) {
			start := f.bill.periodStart
			if f.bill.trialEnd != nil {
				start = *f.bill.trialEnd
			}
			until := start.AddDate(0, plan.IntroMonths, 0)
			v.IntroUntil = &until
			if v.NextBilledAt.Before(until) {
				cents = plan.IntroPriceCents
			}
		}
		v.NextChargeCents = &cents
	}
	return v
}

func (f *Fake) usageOf(u *userRec) usageView {
	v := usageView{RunningGB: f.runningGB(u, nil), DiskAllocatedGB: f.diskAllocatedGB(u), EgressGB: f.bill.egressGB, Projects: len(f.userProjects(u))}
	if plan := f.currentPlan(); plan != nil {
		v.MemoryGB, v.DiskGB, v.EgressIncludedGB, v.ProjectLimit = plan.MemoryGB, plan.DiskGB, plan.EgressGB, plan.ProjectLimit
		v.OverageCents = f.overageCents(plan)
	}
	return v
}

func (f *Fake) seatsOf() Seats {
	return Seats{Total: f.bill.seatsTotal, Held: f.bill.seatsHeld, Free: f.seatsFree(), Waiting: f.bill.waiting}
}

// billingStatus is GET /me's billing.status, a projection of the mode.
func (f *Fake) billingStatus() string {
	if f.bill.mode == BillingOff {
		return "exempt"
	}
	return f.bill.mode
}

func (f *Fake) meBilling() billingView {
	v := billingView{Status: f.billingStatus(), HasCard: f.hasSubscription()}
	if sub := f.subscriptionOf(); sub != nil {
		plan := sub.Plan
		v.Plan = &plan
		v.Seats = sub.Seats
		pe := sub.PeriodEnd
		v.PeriodEnd = &pe
		v.TrialEnd = sub.TrialEnd
		v.CancelAt = sub.CancelAt
	}
	return v
}

func (f *Fake) meLimits() limitsView {
	switch f.bill.mode {
	case BillingOff, BillingExempt:
		// The top plan's numbers (I-289).
		top := Plans[len(Plans)-1]
		return limitsView{Projects: top.ProjectLimit, XL: 1, MemoryGB: top.MemoryGB, DiskGB: top.DiskGB, EgressGB: top.EgressGB}
	}
	plan := f.currentPlan()
	if plan == nil {
		return limitsView{}
	}
	// An xl fits any plan that holds 16 GB of memory for running machines (Plus and Pro).
	xl := 0
	if plan.MemoryGB >= classGB["xl"] {
		xl = 1
	}
	return limitsView{Projects: plan.ProjectLimit, XL: xl, MemoryGB: plan.MemoryGB, DiskGB: plan.DiskGB, EgressGB: plan.EgressGB}
}

// Handlers.

func (f *Fake) billingDisabled() *apiError {
	if f.billingOn() {
		return nil
	}
	return errf("billing_disabled", "billing is not configured")
}

func (f *Fake) getBilling(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	u := userFrom(r)
	var out billingResp
	out.Subscription = f.subscriptionOf()
	out.Usage = f.usageOf(u)
	out.Seats = f.seatsOf()
	out.Waitlist = f.bill.waitlist
	out.IntroEligible = !f.hasSubscription() && !f.bill.introUsed
	out.Paddle.Environment = FakeEnvironment
	out.Paddle.ClientToken = FakeClientToken
	for _, p := range Plans {
		available := f.seatsFor() >= p.Seats
		if cur := f.currentPlan(); cur != nil {
			// A subscriber holds their seats already; another plan needs
			// only the difference.
			available = f.seatsFree() >= p.Seats-cur.Seats
		}
		out.Plans = append(out.Plans, planView{PlanDef: p, Available: available})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// joinWaitlist puts the user on the list if they are not on it; the place
// is the same on a second call.
func (f *Fake) joinWaitlist() *WaitlistPlace {
	if f.bill.waitlist == nil {
		f.bill.waiting++
		f.bill.waitlist = &WaitlistPlace{Position: f.bill.waiting, JoinedAt: f.now()}
	}
	return f.bill.waitlist
}

func (f *Fake) billingCheckout(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	var body struct {
		Plan string `json:"plan"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	plan := planByID(body.Plan)
	if plan == nil {
		return invalid("plan: solo, plus or pro")
	}
	if f.hasSubscription() {
		return errf("conflict", "you already have a plan; change it instead").withDetail(map[string]any{"reason": "subscribed"})
	}
	u := userFrom(r)
	if f.seatsFor() < plan.Seats {
		place := f.joinWaitlist()
		return errf("waitlisted", "%s", waitlist.Message(place.Position, u.Email)).
			withDetail(map[string]any{"position": place.Position, "joined_at": place.JoinedAt, "email": u.Email})
	}
	f.bill.txnSeq++
	txn := fmt.Sprintf("txn_fake_%06d", f.bill.txnSeq)
	f.bill.checkouts[txn] = plan.ID
	writeJSON(w, http.StatusOK, map[string]string{"transaction_id": txn, "client_token": FakeClientToken, "environment": FakeEnvironment})
	return nil
}

func (f *Fake) billingWaitlist(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	if f.hasSubscription() {
		return errf("conflict", "you already have a plan").withDetail(map[string]any{"reason": "subscribed"})
	}
	place := f.joinWaitlist()
	writeJSON(w, http.StatusOK, map[string]any{"position": place.Position, "joined_at": place.JoinedAt})
	return nil
}

func (f *Fake) billingPlan(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	var body struct {
		Plan string `json:"plan"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	to := planByID(body.Plan)
	if to == nil {
		return invalid("plan: solo, plus or pro")
	}
	cur := f.currentPlan()
	if cur == nil {
		return errf("conflict", "no plan to change; choose one first").withDetail(map[string]any{"reason": "no_subscription"})
	}
	u := userFrom(r)
	now := f.now()
	switch {
	case to.ID == cur.ID:
		// Choosing the current plan again cancels a scheduled downgrade.
		f.bill.scheduledPlan = ""
		writeJSON(w, http.StatusOK, map[string]any{"plan": cur.ID, "scheduled_plan": nil, "effective_at": now})
	case to.Seats > cur.Seats:
		if f.seatsFree() < to.Seats-cur.Seats {
			return errf("conflict", "no seat is free for %s right now; try again when one is", to.Name).withDetail(map[string]any{"reason": "no_seat"})
		}
		f.bill.seatsHeld += to.Seats - cur.Seats
		f.bill.plan = to.ID
		f.bill.scheduledPlan = ""
		writeJSON(w, http.StatusOK, map[string]any{"plan": to.ID, "scheduled_plan": nil, "effective_at": now})
	default:
		running, disk := f.runningGB(u, nil), f.diskAllocatedGB(u)
		if running > to.MemoryGB || disk > float64(to.DiskGB) {
			return errf("conflict", "%s holds %d GB of memory for running machines and %d GB of disk; you have %d GB of memory running and %.0f GB allocated. Stop machines or destroy projects first.",
				to.Name, to.MemoryGB, to.DiskGB, running, disk).
				withDetail(map[string]any{"reason": "over_plan", "running_gb": running, "disk_allocated_gb": disk})
		}
		f.bill.scheduledPlan = to.ID
		writeJSON(w, http.StatusOK, map[string]any{"plan": cur.ID, "scheduled_plan": to.ID, "effective_at": f.bill.periodEnd})
	}
	return nil
}

func (f *Fake) billingCancel(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	if !f.hasSubscription() {
		return errf("conflict", "no plan to cancel").withDetail(map[string]any{"reason": "no_subscription"})
	}
	if f.bill.cancelAt != nil {
		return errf("conflict", "the plan is already cancelled").withDetail(map[string]any{"reason": "already_cancelled"})
	}
	end := f.subscriptionEnd()
	f.bill.cancelAt = &end
	writeJSON(w, http.StatusOK, map[string]any{"cancel_at": end})
	return nil
}

func (f *Fake) billingResume(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	if !f.hasSubscription() || f.bill.cancelAt == nil {
		return errf("conflict", "the plan is not cancelled").withDetail(map[string]any{"reason": "not_cancelled"})
	}
	f.bill.cancelAt = nil
	writeJSON(w, http.StatusOK, map[string]any{"plan": f.bill.plan, "period_end": f.bill.periodEnd})
	return nil
}

func (f *Fake) billingPortal(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	var body struct {
		For string `json:"for"`
	}
	if e := decodeBody(r, &body, true); e != nil {
		return e
	}
	url := portalURL
	if body.For == "payment_method" {
		url += "/subscriptions/sub_fake/update-payment-method"
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
	return nil
}

func (f *Fake) billingInvoices(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	if f.bill.invoices != nil {
		writeJSON(w, http.StatusOK, *f.bill.invoices)
		return nil
	}
	if !f.hasSubscription() || f.bill.mode == BillingTrial {
		writeJSON(w, http.StatusOK, []Invoice{})
		return nil
	}
	plan := f.currentPlan()
	pdf := "https://checkout.paddle.com/invoice/txn_fake_inv_000001.pdf"
	writeJSON(w, http.StatusOK, []Invoice{{
		ID: "txn_fake_inv_000001", Number: "REPOSE-0001", Status: "completed", Currency: "USD",
		AmountCents: plan.PriceCents, SubtotalCents: plan.PriceCents, TaxCents: 0,
		CreatedAt: f.bill.periodStart, PeriodStart: f.bill.periodStart, PeriodEnd: f.bill.periodEnd,
		HostedURL: &pdf, PDFURL: &pdf,
	}})
	return nil
}

// billingWebhook stands in for Paddle's endpoint: it checks only that a
// signature header is present and answers what the real route answers.
func (f *Fake) billingWebhook(w http.ResponseWriter, r *http.Request) *apiError {
	if e := f.billingDisabled(); e != nil {
		return e
	}
	if r.Header.Get("Paddle-Signature") == "" {
		return errf("invalid", "paddle signature verification failed")
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": true})
	return nil
}

func (f *Fake) publicSeats(w http.ResponseWriter, r *http.Request) *apiError {
	s := f.seatsOf()
	writeJSON(w, http.StatusOK, struct {
		Total   int `json:"total"`
		Free    int `json:"free"`
		Waiting int `json:"waiting"`
	}{s.Total, s.Free, s.Waiting})
	return nil
}
