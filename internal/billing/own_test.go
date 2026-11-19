package billing_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

// The Own hardware plan (DECISIONS I-1364): $10 a month for one host the
// user owns, selling none of repose's compute.

// withOwn is testConfig with the Own hardware product configured, which
// is what turns the plan on for a deployment. testConfig leaves it out,
// as production does.
func withOwn(cfg billing.Config) billing.Config {
	cfg.ProductOwn = "prod_own_test"
	return cfg
}

// TestOwnHardwareStartsOnItsOwnTerms is the test the whole item turns on:
// an account on the Own hardware plan asks for the biggest machine there
// is, with more memory already running than any hosted plan sells, a disk
// larger than any plan's and a period's egress past every allowance, and
// the gate refuses nothing. The plan's MemoryGB, DiskGB and EgressGB are
// 0 because it sells none of ours, and every one of the gate's three
// resource checks reads a 0 as a ceiling: without the early return this
// account is refused `egress_limit` before it ever reaches the memory
// check (four times nothing is nothing, and it has sent some), and then
// `plan_limit` for any class and `disk_limit` for any volume. A user who
// bought the plan, installed the appliance and ran `repose run` would be
// refused three different ways with every other test green.
func TestOwnHardwareStartsOnItsOwnTerms(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	g := newGate(pool, f)
	ctx := context.Background()

	a := seedAccount(t, pool, "own", "active", "large", "running")
	// 32 GB running: more than Pro sells, on a host that has the memory.
	for _, slug := range []string{"one", "two", "three"} {
		id := addProject(t, pool, a, slug, "large", "running", int64(40)<<30)
		held(t, pool, id, int64(60)<<30)
	}
	held(t, pool, a.ProjectID, int64(60)<<30)
	// A period's egress well past Pro's hard stop.
	usageHour(t, pool, a.ProjectID, a.Period.Start.Add(time.Hour), "large", 3600, int64(5000)<<30, a.Period)

	u := user(t, pool, a)
	for _, req := range []billing.Request{
		{Class: "xl"},
		{Class: "large", Project: a.ProjectID},
		{Class: "tiny", Disk: true, AddHeldBytes: int64(80) << 30, VolumeBytes: int64(200) << 30},
		{Disk: true, VolumeBytes: int64(900) << 30},
	} {
		if err := g.Check(ctx, u, req); err != nil {
			t.Fatalf("own hardware refused %+v: %v", req, err)
		}
	}

	// What still refuses on this plan: a failed payment, and a suspension.
	if _, err := pool.Exec(ctx, "update subscriptions set status = 'past_due' where id = $1", a.SubID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update users set billing_status = 'past_due' where id = $1", a.UserID); err != nil {
		t.Fatal(err)
	}
	r := refusal(t, g.Check(ctx, user(t, pool, a), billing.Request{Class: "large"}))
	if r.Reason != "past_due" || r.Detail["plan"] != "own" {
		t.Fatalf("past due on own hardware: %+v", r)
	}
	// A resume after a host restart is let through, as on any plan.
	if err := g.Check(ctx, user(t, pool, a), billing.Request{Class: "large", Resume: true}); err != nil {
		t.Fatalf("resume on a past-due own-hardware account: %v", err)
	}
	if _, err := pool.Exec(ctx, "update users set billing_status = 'suspended', suspended_at = now(), suspended_reason = 'billing' where id = $1", a.UserID); err != nil {
		t.Fatal(err)
	}
	if r := refusal(t, g.Check(ctx, user(t, pool, a), billing.Request{Class: "tiny"})); r.Reason != "suspended" {
		t.Fatalf("suspended on own hardware: %+v", r)
	}
}

// withFleetMemoryOnOwn gives the own-hardware plan fleet memory, disk
// and egress for one test, and puts the table back afterwards. It is
// the plan nobody sells yet and the one the owner asks for first: a
// host of your own *and* some memory on repose's hosts. The plan id
// stays `own` because the subscriptions check constraint lists the four
// that exist (migration 0049), so a hypothetical fourth id could not be
// seeded.
func withFleetMemoryOnOwn(t *testing.T) billing.Plan {
	t.Helper()
	saved := append([]billing.Plan(nil), billing.Plans...)
	t.Cleanup(func() { billing.Plans = saved })
	var hybrid billing.Plan
	for i := range billing.Plans {
		if billing.Plans[i].ID != billing.Own.ID {
			continue
		}
		billing.Plans[i].MemoryGB = 16
		billing.Plans[i].DiskGB = 250
		billing.Plans[i].EgressGB = 500
		hybrid = billing.Plans[i]
	}
	if hybrid.ID == "" {
		t.Fatal("no own-hardware plan to give fleet memory to")
	}
	return hybrid
}

// A plan that sells fleet memory is gated on it, whatever else it sells.
// The gate skipped its three resource checks for any plan with a host
// (Plan.OwnHardware), while placement let an account onto repose's
// hosts whenever its plan sold memory there (Gate.FleetAllowed). The two
// agree on every plan that exists today and diverge on the first plan
// that sells both, which placement would put on the fleet and the gate
// would meter with nothing. Both now read Plan.SellsFleetMemory.
func TestAPlanThatSellsFleetMemoryIsGatedOnIt(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	ctx := context.Background()
	hybrid := withFleetMemoryOnOwn(t)
	if !hybrid.OwnHardware() || !hybrid.SellsFleetMemory() {
		t.Fatalf("the test plan sells a host and fleet memory: %+v", hybrid)
	}
	g := newGate(pool, f)

	a := seedAccount(t, pool, "own", "active", "xl", "running")
	u := user(t, pool, a)
	// Memory: an xl is running, so a second one passes the plan's 16 GB.
	r := refusal(t, g.Check(ctx, u, billing.Request{Class: "xl"}))
	if r.Reason != "plan_limit" || r.Detail["limit_gb"] != 16 {
		t.Fatalf("memory on a plan that sells it: %+v", r)
	}
	// Disk: one volume larger than the plan's whole disk.
	r = refusal(t, g.Check(ctx, u, billing.Request{Disk: true, VolumeBytes: int64(300) << 30}))
	if r.Reason != "disk_limit" {
		t.Fatalf("disk on a plan that sells it: %+v", r)
	}
	// Egress: past four times the plan's allowance.
	usageHour(t, pool, a.ProjectID, a.Period.Start.Add(time.Hour), "xl", 3600, int64(2100)<<30, a.Period)
	r = refusal(t, g.Check(ctx, u, billing.Request{Class: "tiny"}))
	if r.Reason != "egress_limit" {
		t.Fatalf("egress on a plan that sells it: %+v", r)
	}
	// And the rest of the fleet machinery comes back with it: the disk
	// email, the overage line and the hard stop are all the plan's again.
	held(t, pool, a.ProjectID, int64(300)<<30)
	o := billing.NewOverage(pool, billing.NewPolar(testConfig(f), quiet()), testConfig(f), &stopRecorder{}, nop(), quiet())
	o.Now = at(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	told, err := o.DiskOverPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 || told[0] != a.UserID {
		t.Fatalf("the disk email on a plan that sells disk: %v", told)
	}
	sub, err := billing.LiveSubscription(ctx, pool, a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := billing.LoadUsage(ctx, pool, a.UserID, sub.PlanOrSolo(), a.Period)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.DiskOver() || !usage.EgressStopped() || usage.OverageCents == 0 {
		t.Fatalf("usage on a plan that sells disk and egress: over %v stopped %v overage %d",
			usage.DiskOver(), usage.EgressStopped(), usage.OverageCents)
	}
	// The product half of the plan is untouched: it is still the Own
	// hardware product, still one host, still no change across the kinds.
	if !billing.SameKind(hybrid, hybrid) || billing.SameKind(hybrid, billing.Solo) {
		t.Fatal("a plan with a host is still the own-hardware kind")
	}
	if hybrid.Hosts != 1 {
		t.Fatalf("hosts %d", hybrid.Hosts)
	}
}

// The usage block and the limits of an own-hardware account: zeros that
// mean "the plan caps nothing", one host, every class allowed, and no
// overage however much egress the account's machines sent.
func TestOwnHardwareUsageAndLimits(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "own", "active", "xl", "running")
	held(t, pool, a.ProjectID, int64(700)<<30)
	usageHour(t, pool, a.ProjectID, a.Period.Start.Add(time.Hour), "xl", 3600, int64(4000)<<30, a.Period)

	sub, err := billing.LiveSubscription(ctx, pool, a.UserID)
	if err != nil || sub == nil {
		t.Fatalf("subscription: %v", err)
	}
	if sub.Plan != "own" || sub.Seats != 0 {
		t.Fatalf("an own-hardware subscription holds no seat: %+v", sub)
	}
	usage, err := billing.LoadUsage(ctx, pool, a.UserID, sub.PlanOrSolo(), a.Period)
	if err != nil {
		t.Fatal(err)
	}
	if usage.RunningGB != 16 || usage.DiskHeldBytes != int64(700)<<30 {
		t.Fatalf("usage reads what the account holds: %+v", usage)
	}
	if usage.OverageCents != 0 || usage.OverageGB != 0 || usage.EgressIncludedGB != 0 {
		t.Fatalf("no egress is counted against this plan: %+v", usage)
	}
	if usage.DiskOver() || usage.EgressStopped() {
		t.Fatalf("nothing is over a plan that sells nothing: over disk %v, stopped %v", usage.DiskOver(), usage.EgressStopped())
	}
	j := usage.JSON()
	if j["memory_gb"] != 0 || j["disk_gb"] != 0 || j["egress_included_gb"] != 0 {
		t.Fatalf("usage JSON: %v", j)
	}

	u := user(t, pool, a)
	l := billing.LimitsFor(u, sub)
	if l.Hosts != 1 || l.XL != 1 || l.MemoryGB != 0 || l.DiskGB != 0 || l.EgressGB != 0 {
		t.Fatalf("own-hardware limits: %+v", l)
	}
	if l.JSON()["hosts"] != 1 {
		t.Fatalf("limits JSON: %v", l.JSON())
	}
	// /me's limits.hosts is the figure POST /hosts refuses past
	// (Gate.HostsAllowed), for every kind of account: one implementation,
	// so the page and the route cannot disagree.
	g := newGate(pool, newFakePolar())
	hosts := func(a account) int {
		t.Helper()
		n, err := g.HostsAllowed(ctx, pool, user(t, pool, a))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := hosts(a); n != 1 || l.Hosts != n {
		t.Fatalf("own hardware: the route allows %d hosts, /me says %d", n, l.Hosts)
	}
	solo := seedAccount(t, pool, "solo", "active", "", "")
	soloSub, err := billing.LiveSubscription(ctx, pool, solo.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if n, ml := hosts(solo), billing.LimitsFor(user(t, pool, solo), soloSub).Hosts; n != 0 || ml != 0 {
		t.Fatalf("a Solo account: the route allows %d hosts of its own, /me says %d", n, ml)
	}
	// An exempt account gets one without buying a plan from itself.
	ex := seedAccount(t, pool, "", "exempt", "", "")
	if n, ml := hosts(ex), billing.LimitsFor(user(t, pool, ex), nil).Hosts; n != billing.ExemptHosts || ml != billing.ExemptHosts {
		t.Fatalf("an exempt account: the route allows %d, /me says %d", n, ml)
	}
	none := seedAccount(t, pool, "", "none", "", "")
	if n, ml := hosts(none), billing.LimitsFor(user(t, pool, none), nil).Hosts; n != 0 || ml != 0 {
		t.Fatalf("an account with no plan: the route allows %d, /me says %d", n, ml)
	}
}

// The plan is listed and sold only where its Polar product id is set:
// that one variable is the switch, so a deployment with no appliance
// behind it offers nothing it cannot keep. A subscription that already
// exists on the plan is honoured either way.
func TestOwnPlanIsSoldOnlyWhereItsProductIs(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	ctx := context.Background()
	p := billing.NewPolar(testConfig(f), quiet())
	seats := &billing.SubscriptionSeats{Pool: pool, Total: 30}
	o := billing.NewOverage(pool, p, testConfig(f), &stopRecorder{}, nop(), quiet())

	off := billing.NewService(pool, p, testConfig(f), seats, o, quiet())
	on := billing.NewService(pool, p, withOwn(testConfig(f)), seats, o, quiet())

	a := seedAccount(t, pool, "", "none", "", "")
	u := user(t, pool, a)

	out, err := off.Overview(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if ids := planIDs(out); strings.Join(ids, ",") != "solo,plus,pro" {
		t.Fatalf("with no product id the plan is not listed: %v", ids)
	}
	if _, err := off.Checkout(ctx, u, "own", ""); !errors.Is(err, billing.ErrNotForSale) {
		t.Fatalf("checkout where the plan is not sold: %v", err)
	}
	if got := billing.PlanChoices(off.PlansForSale()); got != "solo, plus and pro" {
		t.Fatalf("the refusal lists %q", got)
	}

	out, err = on.Overview(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	ids := planIDs(out)
	if strings.Join(ids, ",") != "solo,plus,pro,own" {
		t.Fatalf("with the product id the plan is listed last: %v", ids)
	}
	own := planRow(out, "own")
	if own["price_cents"] != int64(1000) || own["hosts"] != 1 || own["seats"] != 0 || own["trial_days"] != 7 ||
		own["memory_gb"] != 0 || own["disk_gb"] != 0 || own["egress_gb"] != 0 || own["available"] != true {
		t.Fatalf("the own-hardware row: %v", own)
	}
	url, err := on.Checkout(ctx, u, "own", "")
	if err != nil || !strings.HasPrefix(url, "https://sandbox.polar.sh/checkout/") {
		t.Fatalf("checkout: %q %v", url, err)
	}
	body := f.Bodies["POST /checkouts/"][0]
	if body["products"].([]any)[0] != "prod_own_test" {
		t.Fatalf("the checkout names %v", body["products"])
	}
	// No founding discount on it: the offer is Solo's (I-672).
	if body["discount_id"] != nil {
		t.Fatalf("the own-hardware checkout carries a discount: %v", body["discount_id"])
	}
}

// A full fleet does not block the plan: it holds no seat, so the
// checkout never asks the seat service at all. Asking would both refuse
// it and put the user on the waitlist, because the real
// waitlist.Service reads a request for 0 seats as a request for 1
// (fits clamps seats < 1), which no stub in this package does: the
// refusing stand-in below is the one that catches it.
func TestOwnPlanNeedsNoSeat(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	ctx := context.Background()
	p := billing.NewPolar(withOwn(testConfig(f)), quiet())
	o := billing.NewOverage(pool, p, withOwn(testConfig(f)), &stopRecorder{}, nop(), quiet())
	// fullSeats refuses every Reserve and every Fits, whatever the count.
	s := billing.NewService(pool, p, withOwn(testConfig(f)), fullSeats{}, o, quiet())

	a := seedAccount(t, pool, "", "none", "", "")
	u := user(t, pool, a)
	var wl *billing.WaitlistedError
	if _, err := s.Checkout(ctx, u, "solo", ""); !errors.As(err, &wl) {
		t.Fatalf("a hosted plan with no seat free: %v", err)
	}
	if _, err := s.Checkout(ctx, u, "own", ""); err != nil {
		t.Fatalf("own hardware with no seat free: %v", err)
	}
	// And the page offers it: available, where the hosted three are not.
	out, err := s.Overview(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if planRow(out, "own")["available"] != true {
		t.Fatalf("the own-hardware row on a full fleet: %v", planRow(out, "own"))
	}
	if planRow(out, "solo")["available"] != false {
		t.Fatalf("Solo is available on a full fleet: %v", planRow(out, "solo"))
	}
	// The subscription a webhook writes for it holds no seat, so the
	// fleet's count does not move and the user keeps any place they hold
	// in the queue for one: they reserved nothing to convert.
	before := seatsHeld(t, pool)
	w := billing.NewWebhooks(pool, withOwn(testConfig(f)), nop(), quiet())
	w.Now = at(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	seatRec := &seatsRecorder{}
	w.Seats = seatRec
	ev := event("subscription.created", subData("sub_own_seat", a, "prod_own_test", "trialing", nil))
	if err := post(t, w, f, ev); err != nil {
		t.Fatalf("the webhook refused the own-hardware product: %v", err)
	}
	if after := seatsHeld(t, pool); after != before {
		t.Fatalf("the seat count moved from %d to %d", before, after)
	}
	if len(seatRec.converted) != 0 {
		t.Fatalf("an own-hardware subscription converted a waitlist place: %v", seatRec.converted)
	}
	// A hosted plan still converts, so the seat an invitation held is
	// released at the checkout as before.
	b := seedAccount(t, pool, "", "none", "", "")
	if err := post(t, w, f, event("subscription.created", subData("sub_solo_seat", b, "prod_solo_test", "trialing", nil))); err != nil {
		t.Fatal(err)
	}
	if len(seatRec.converted) != 1 || seatRec.converted[0] != b.UserID.String() {
		t.Fatalf("a hosted subscription did not convert: %v", seatRec.converted)
	}
}

// The webhook accepts the Own hardware product and writes the plan, with
// no seat and no founding discount; the account becomes trial and then
// active as on any plan.
func TestOwnHardwareWebhookAccepted(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	ctx := context.Background()
	cfg := withOwn(testConfig(f))
	w := billing.NewWebhooks(pool, cfg, nop(), quiet())
	w.Now = at(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	a := seedAccount(t, pool, "", "none", "", "")

	if err := post(t, w, f, event("subscription.created", subData("sub_own", a, "prod_own_test", "trialing", nil))); err != nil {
		t.Fatalf("subscription.created: %v", err)
	}
	sub, err := billing.GetSubscription(ctx, pool, "sub_own")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Plan != "own" || sub.Seats != 0 || sub.Intro {
		t.Fatalf("the row: %+v", sub)
	}
	if got := userField(t, pool, a, "billing_status"); got != "trial" {
		t.Fatalf("billing_status %q", got)
	}
	if err := post(t, w, f, event("order.paid", orderData(a, "sub_own", 1000, "subscription_cycle"))); err != nil {
		t.Fatalf("order.paid: %v", err)
	}
	if err := post(t, w, f, event("subscription.active", subData("sub_own", a, "prod_own_test", "active", nil))); err != nil {
		t.Fatalf("subscription.active: %v", err)
	}
	if got := userField(t, pool, a, "billing_status"); got != "active" {
		t.Fatalf("billing_status after the first charge: %q", got)
	}
	// Where the product is not configured the same event is foreign, and
	// the row the api holds is untouched: the webhook never guesses a
	// plan from a product it does not know.
	plain := billing.NewWebhooks(pool, testConfig(f), nop(), quiet())
	plain.Now = w.Now
	err = post(t, plain, f, event("subscription.updated", subData("sub_own2", a, "prod_own_test", "active", nil)))
	if !errors.Is(err, billing.ErrForeign) {
		t.Fatalf("an unconfigured product: %v", err)
	}
}

// A plan cannot be changed between the hosted plans and Own hardware, in
// either direction, and the refusal says what to do instead.
func TestPlanKindRefusal(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	ctx := context.Background()
	p := billing.NewPolar(withOwn(testConfig(f)), quiet())
	o := billing.NewOverage(pool, p, withOwn(testConfig(f)), &stopRecorder{}, nop(), quiet())
	s := billing.NewService(pool, p, withOwn(testConfig(f)), &billing.SubscriptionSeats{Pool: pool, Total: 30}, o, quiet())

	owner := seedAccount(t, pool, "own", "active", "large", "running")
	if _, err := s.ChangePlan(ctx, user(t, pool, owner), "solo"); !errors.Is(err, billing.ErrPlanKind) {
		t.Fatalf("own to solo: %v", err)
	}
	if _, err := s.ChangePlan(ctx, user(t, pool, owner), "pro"); !errors.Is(err, billing.ErrPlanKind) {
		t.Fatalf("own to pro: %v", err)
	}
	hosted := seedAccount(t, pool, "solo", "active", "", "")
	f.AddSubscription(hosted.SubID, "ctm_"+hosted.Handle, "prod_solo_test", "active")
	if _, err := s.ChangePlan(ctx, user(t, pool, hosted), "own"); !errors.Is(err, billing.ErrPlanKind) {
		t.Fatalf("solo to own: %v", err)
	}
	// Nothing was sent to Polar for any of them.
	for _, r := range f.Requests {
		if strings.HasPrefix(r, "PATCH /subscriptions/") {
			t.Fatalf("a refused change still reached Polar: %s", r)
		}
	}
	// Within a kind the change still works, and so does choosing the
	// current plan again.
	if _, err := s.ChangePlan(ctx, user(t, pool, hosted), "pro"); err != nil {
		t.Fatalf("solo to pro: %v", err)
	}
	if _, err := s.ChangePlan(ctx, user(t, pool, owner), "own"); !errors.Is(err, billing.ErrSamePlan) {
		t.Fatalf("own to own: %v", err)
	}
}

// The jobs that stop machines and charge for egress pass an own-hardware
// account over: no overage line, no hard stop, no disk email, whatever
// its machines did, because none of it is ours to charge for.
func TestOwnHardwareHasNoOverageStopOrDiskEmail(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	ctx := context.Background()
	cfg := withOwn(testConfig(f))
	p := billing.NewPolar(cfg, quiet())
	stop := &stopRecorder{}
	o := billing.NewOverage(pool, p, cfg, stop, nop(), quiet())
	o.Now = at(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))

	a := seedAccount(t, pool, "own", "active", "large", "running")
	held(t, pool, a.ProjectID, int64(900)<<30)
	usageHour(t, pool, a.ProjectID, a.Period.Start.Add(time.Hour), "large", 3600, int64(6000)<<30, a.Period)

	sub, err := billing.LiveSubscription(ctx, pool, a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := o.ChargePeriod(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.Cents != 0 || c.EgressGB != 0 {
		t.Fatalf("an own-hardware period is charged %+v", c)
	}
	var lines int
	if err := pool.QueryRow(ctx, "select count(*) from overage_charges where subscription_id = $1", sub.ID).Scan(&lines); err != nil || lines != 0 {
		t.Fatalf("%d overage lines recorded (%v)", lines, err)
	}
	// Run is the tick: the overage lines and the egress hard stops.
	charges, stopped, err := o.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(charges) != 0 || len(stopped) != 0 || len(stop.calls) != 0 {
		t.Fatalf("the tick charged %v and stopped %v (%d ops)", charges, stopped, len(stop.calls))
	}
	told, err := o.DiskOverPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(told) != 0 {
		t.Fatalf("a disk email for a plan that sells no disk: %v", told)
	}
	// Nothing was sent to Polar's meter either.
	if n := f.Count("POST /events/ingest"); n != 0 {
		t.Fatalf("%d meter events for an own-hardware account", n)
	}
}

// Validate keeps the three hosted products required and the Own hardware
// one optional, and refuses two plans sharing a product.
func TestOwnProductIsOptionalAndDistinct(t *testing.T) {
	f := newFakePolar()
	defer f.Close()
	cfg := testConfig(f)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a configuration without POLAR_PRODUCT_OWN is refused: %v", err)
	}
	if cfg.ForSale("own") || !cfg.ForSale("solo") {
		t.Fatalf("for sale: own %v, solo %v", cfg.ForSale("own"), cfg.ForSale("solo"))
	}
	on := withOwn(cfg)
	if err := on.Validate(); err != nil {
		t.Fatalf("with POLAR_PRODUCT_OWN: %v", err)
	}
	if !on.ForSale("own") || on.PlanForProduct("prod_own_test") != "own" {
		t.Fatalf("the own product maps to %q", on.PlanForProduct("prod_own_test"))
	}
	if cfg.PlanForProduct("prod_own_test") != "" {
		t.Fatal("an unconfigured product maps to a plan")
	}
	clash := cfg
	clash.ProductOwn = clash.ProductPro
	err := clash.Validate()
	if err == nil || !strings.Contains(err.Error(), "POLAR_PRODUCT_PRO") || !strings.Contains(err.Error(), "POLAR_PRODUCT_OWN") {
		t.Fatalf("two plans on one product: %v", err)
	}
}

// planIDs is the ids of GET /billing's plans, in order.
func planIDs(out map[string]any) []string {
	var ids []string
	for _, p := range out["plans"].([]map[string]any) {
		ids = append(ids, p["id"].(string))
	}
	return ids
}

// planRow is one plan of GET /billing by id.
func planRow(out map[string]any, id string) map[string]any {
	for _, p := range out["plans"].([]map[string]any) {
		if p["id"] == id {
			return p
		}
	}
	return nil
}

// seatsHeld is the seats live subscriptions hold.
func seatsHeld(t *testing.T, pool *db.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "select coalesce(sum(seats), 0)::integer from subscriptions where status in ('trialing','active','past_due')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
