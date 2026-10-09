package billing_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

// fullSeats refuses every reservation with a place, the way the real
// Seats does when the fleet is full.
type fullSeats struct{}

func (fullSeats) Reserve(_ context.Context, _ string, _ int) (bool, *waitlist.Place, error) {
	return false, &waitlist.Place{Position: 3, JoinedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Email: "who@example.test"}, nil
}
func (fullSeats) Converted(context.Context, string) error { return nil }
func (fullSeats) Count(context.Context) (waitlist.Count, error) {
	return waitlist.Count{Total: 30, Held: 30, Free: 0, Waiting: 3}, nil
}

func newService(t *testing.T, pool *db.Pool, f *fakePolar, seats waitlist.Seats) (*billing.Service, *billing.Overage) {
	t.Helper()
	cfg := testConfig(f)
	p := billing.NewPolar(cfg, quiet())
	if seats == nil {
		seats = &billing.SubscriptionSeats{Pool: pool, Total: 30}
	}
	o := billing.NewOverage(pool, p, cfg, &stopRecorder{}, nop(), quiet())
	o.Now = at(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	s := billing.NewService(pool, p, cfg, seats, o, quiet())
	s.Now = o.Now
	return s, o
}

func TestCheckout(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	s, _ := newService(t, pool, f, nil)
	ctx := context.Background()
	a := seedAccount(t, pool, "", "none", "", "")

	if _, err := s.Checkout(ctx, user(t, pool, a), "gold", ""); !errors.Is(err, billing.ErrUnknownPlan) {
		t.Fatalf("unknown plan: %v", err)
	}
	url, err := s.Checkout(ctx, user(t, pool, a), "solo", "203.0.113.9")
	if err != nil || !strings.HasPrefix(url, "https://sandbox.polar.sh/checkout/") {
		t.Fatalf("checkout: %s %v", url, err)
	}
	body := f.Bodies["POST /checkouts/"][0]
	if body["products"].([]any)[0] != "prod_solo_test" || body["external_customer_id"] != a.UserID.String() || body["metadata"].(map[string]any)["user_id"] != a.UserID.String() ||
		body["customer_email"] != a.Email || body["allow_discount_codes"] != false || body["customer_ip_address"] != "203.0.113.9" ||
		body["success_url"] != "https://repose.herakraft.co/billing?checkout=done" || body["return_url"] != "https://repose.herakraft.co/billing" {
		t.Fatalf("checkout body: %v", body)
	}
	if body["discount_id"] != "dsc_intro_test" {
		t.Fatalf("a first Solo checkout carries the introductory discount: %v", body)
	}
	// Plus has no introductory price.
	if _, err := s.Checkout(ctx, user(t, pool, a), "plus", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Bodies["POST /checkouts/"][1]["discount_id"]; ok {
		t.Fatal("a Plus checkout carried the discount")
	}
	// A user who had a subscription before pays the full price.
	again := seedAccount(t, pool, "solo", "active", "", "")
	if _, err := pool.Exec(ctx, "update subscriptions set status = 'canceled' where id = $1", again.SubID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkout(ctx, user(t, pool, again), "solo", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Bodies["POST /checkouts/"][2]["discount_id"]; ok {
		t.Fatal("a returning subscriber got the introductory discount")
	}
	if ov, err := s.Overview(ctx, user(t, pool, again)); err != nil || ov["intro_eligible"] != false {
		t.Fatalf("returning subscriber's overview: %v %v", ov["intro_eligible"], err)
	}
	// A subscriber is refused with subscribed.
	b := seedAccount(t, pool, "solo", "active", "", "")
	if _, err := s.Checkout(ctx, user(t, pool, b), "plus", ""); !errors.Is(err, billing.ErrSubscribed) {
		t.Fatalf("subscribed: %v", err)
	}
	// No seat: waitlisted with the place, no checkout.
	full, _ := newService(t, pool, f, fullSeats{})
	before := f.Count("POST /checkouts/")
	_, err = full.Checkout(ctx, user(t, pool, a), "solo", "")
	var wl *billing.WaitlistedError
	if !errors.As(err, &wl) || wl.Place.Position != 3 || wl.Message() != "repose is full right now. You're number 3 on the waitlist; we'll email who@example.test when there's a seat." {
		t.Fatalf("waitlisted: %v", err)
	}
	if f.Count("POST /checkouts/") != before {
		t.Fatal("a checkout was created for a waitlisted user")
	}
	// Overview marks plans unavailable when full and lists the seats.
	ov, err := full.Overview(ctx, user(t, pool, a))
	if err != nil {
		t.Fatal(err)
	}
	plans := ov["plans"].([]map[string]any)
	if plans[0]["intro_price_cents"] != int64(2000) || plans[0]["intro_months"] != 3 || plans[0]["intro_egress_gb"] != 100 || plans[1]["intro_months"] != 0 || ov["intro_eligible"] != true {
		t.Fatalf("introductory price in the overview: %v %v", plans, ov["intro_eligible"])
	}
	if len(plans) != 3 || plans[0]["available"] != false || plans[0]["price_cents"] != int64(2900) || plans[1]["id"] != "plus" || plans[2]["id"] != "pro" || plans[2]["seats"] != 4 {
		t.Fatalf("plans: %v", plans)
	}
	seats := ov["seats"].(map[string]any)
	if seats["total"] != 30 || seats["free"] != 0 || seats["waiting"] != 3 {
		t.Fatalf("seats: %v", seats)
	}
	if _, has := ov["paddle"]; ov["subscription"] != nil || has {
		t.Fatalf("overview: %v", ov)
	}
	usage := ov["usage"].(map[string]any)
	if usage["memory_gb"] != 8 || usage["project_limit"] != billing.ProjectCap {
		t.Fatalf("usage without a plan shows Solo's limits: %v", usage)
	}
}

// ChargeCents and PlanFor follow the introductory offer: $20 and 100 GB
// of egress while it runs, before its end is known too, and the
// plan's own price and allowance after it, without it and on another
// plan (DECISIONS I-497).
func TestIntroOffer(t *testing.T) {
	ends := time.Date(2027, 1, 8, 0, 0, 0, 0, time.UTC)
	before, after := ends.Add(-time.Hour), ends
	for _, c := range []struct {
		name   string
		sub    *billing.Sub
		at     time.Time
		cents  int64
		egress int
	}{
		{"no subscription", nil, before, 2900, 250},
		{"no offer", &billing.Sub{Plan: "solo"}, before, 2900, 250},
		{"offer, end not fixed", &billing.Sub{Plan: "solo", Intro: true}, before, 2000, 100},
		{"offer, before its end", &billing.Sub{Plan: "solo", Intro: true, IntroUntil: &ends}, before, 2000, 100},
		{"offer, at its end", &billing.Sub{Plan: "solo", Intro: true, IntroUntil: &ends}, after, 2900, 250},
		{"plus", &billing.Sub{Plan: "plus", Intro: true}, before, 5900, 500},
	} {
		if got := c.sub.ChargeCents(c.at); got != c.cents {
			t.Errorf("%s: %d cents, want %d", c.name, got, c.cents)
		}
		if got := c.sub.PlanFor(c.at).EgressGB; got != c.egress {
			t.Errorf("%s: %d GB egress, want %d", c.name, got, c.egress)
		}
	}
	// The allowance sets the overage and the hard stop: 150 GB is 50 GB
	// over during the offer and nothing after it; the stop is 400 GB.
	sub := &billing.Sub{Plan: "solo", Intro: true, IntroUntil: &ends}
	gb := int64(1) << 30
	if over, cents := billing.OverageCents(sub.PlanFor(before), 150*gb); over != 50 || cents != 250 {
		t.Errorf("overage during the offer: %d GB %d cents", over, cents)
	}
	if over, _ := billing.OverageCents(sub.PlanFor(after), 150*gb); over != 0 {
		t.Errorf("overage after the offer: %d GB", over)
	}
	if stop := sub.PlanFor(before).EgressHardStopBytes(); stop != 400*gb {
		t.Errorf("hard stop during the offer: %d GB", stop/gb)
	}
	next := before
	j := billing.SubJSON(&billing.Sub{Plan: "solo", Intro: true, IntroUntil: &ends, NextBilledAt: &next}).(map[string]any)
	if j["next_charge_cents"] != int64(2000) || j["intro_until"] != &ends {
		t.Errorf("SubJSON: %v", j)
	}
}

func TestPlanChangesCancelResume(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	s, _ := newService(t, pool, f, nil)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	f.AddSubscription(a.SubID, "ctm_"+a.Handle, "prod_solo_test", "active")
	f.Sub(a.SubID)["discount_id"] = "dsc_intro_test"
	if _, err := pool.Exec(ctx, "update subscriptions set intro = true, intro_until = '2027-01-01' where id = $1", a.SubID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ChangePlan(ctx, user(t, pool, a), "solo"); !errors.Is(err, billing.ErrSamePlan) {
		t.Fatalf("same plan: %v", err)
	}
	// Upgrade: at once, prorated, plan_changed email.
	ch, err := s.ChangePlan(ctx, user(t, pool, a), "plus")
	if err != nil || ch.Plan != "plus" || ch.ScheduledPlan != nil {
		t.Fatalf("upgrade: %+v %v", ch, err)
	}
	patch := f.Bodies["PATCH /subscriptions/"+a.SubID][0]
	if d, present := patch["discount_id"]; patch["proration_behavior"] != "invoice" || patch["product_id"] != "prod_plus_test" || !present || d != nil {
		t.Fatalf("upgrade body: %v", patch)
	}
	// Leaving Solo ends the introductory offer, at Polar and in the row.
	sub, _ := billing.GetSubscription(ctx, pool, a.SubID)
	if sub.Plan != "plus" || sub.Seats != 2 || sub.Intro || f.Sub(a.SubID)["discount_id"] != nil {
		t.Fatalf("row after upgrade: %+v", sub)
	}
	if k := eventKinds(t, pool, a); len(k) != 1 || k[0] != "plan_changed" {
		t.Fatalf("events %v", k)
	}
	if p := accountEmail(t, pool, a, "plan_changed", "from Solo to Plus"); p["from_plan"] != "solo" || p["to_plan"] != "plus" || p["effective_at"] == nil {
		t.Fatalf("plan_changed payload %v", p)
	}
	// Downgrade refused while two large run (16 GB > 8).
	addProject(t, pool, a, "second", "large", "running", 40<<30)
	_, err = s.ChangePlan(ctx, user(t, pool, a), "solo")
	var over *billing.OverPlanError
	if !errors.As(err, &over) || over.RunningGB != 16 || over.DiskHeldBytes != 80<<30 {
		t.Fatalf("over plan: %v", err)
	}
	// Stopped but holding 120 GB of Solo's 100: refused for the disk
	// alone (I-585).
	if _, err := pool.Exec(ctx, "update projects set state = 'stopped', disk_held_bytes = 60::bigint<<30 where user_id = $1", a.UserID); err != nil {
		t.Fatal(err)
	}
	_, err = s.ChangePlan(ctx, user(t, pool, a), "solo")
	if !errors.As(err, &over) || over.RunningGB != 0 || over.DiskHeldBytes != 120<<30 {
		t.Fatalf("over plan on disk: %v", err)
	}
	if _, err := pool.Exec(ctx, "update projects set disk_held_bytes = 10::bigint<<30 where user_id = $1", a.UserID); err != nil {
		t.Fatal(err)
	}
	// Stopped, it is scheduled for period_end.
	if _, err := pool.Exec(ctx, "update projects set state = 'stopped' where user_id = $1", a.UserID); err != nil {
		t.Fatal(err)
	}
	ch, err = s.ChangePlan(ctx, user(t, pool, a), "solo")
	if err != nil || ch.Plan != "plus" || ch.ScheduledPlan == nil || *ch.ScheduledPlan != "solo" || !ch.EffectiveAt.Equal(a.Period.End) {
		t.Fatalf("downgrade: %+v %v", ch, err)
	}
	if patches := f.Bodies["PATCH /subscriptions/"+a.SubID]; patches[len(patches)-1]["proration_behavior"] != "next_period" || patches[len(patches)-1]["product_id"] != "prod_solo_test" {
		t.Fatalf("downgrade body: %v", patches[len(patches)-1])
	}
	sub, _ = billing.GetSubscription(ctx, pool, a.SubID)
	if sub.ScheduledPlan == nil || *sub.ScheduledPlan != "solo" {
		t.Fatal("scheduled_plan not stored")
	}
	// A webhook carrying Polar's pending update keeps the scheduled
	// downgrade.
	w := newHooks(t, pool, f, nil, nil)
	if err := post(t, w, f, event("subscription.updated", subData(a.SubID, a, "prod_plus_test", "active", map[string]any{"pending_update": f.Sub(a.SubID)["pending_update"]}))); err != nil {
		t.Fatal(err)
	}
	sub, _ = billing.GetSubscription(ctx, pool, a.SubID)
	if sub.ScheduledPlan == nil || *sub.ScheduledPlan != "solo" {
		t.Fatal("the webhook dropped the scheduled downgrade")
	}
	// Choosing plus again undoes it.
	if ch, err = s.ChangePlan(ctx, user(t, pool, a), "plus"); err != nil || ch.ScheduledPlan != nil {
		t.Fatalf("undo: %+v %v", ch, err)
	}
	if patches := f.Bodies["PATCH /subscriptions/"+a.SubID]; len(patches[len(patches)-1]) != 1 || f.Sub(a.SubID)["pending_update"] != nil {
		t.Fatalf("undo body: %v", patches[len(patches)-1])
	}
	sub, _ = billing.GetSubscription(ctx, pool, a.SubID)
	if sub.ScheduledPlan != nil {
		t.Fatal("scheduled_plan not cleared")
	}
	// Cancel, twice, resume, resume.
	if _, err := s.Resume(ctx, user(t, pool, a)); !errors.Is(err, billing.ErrNotCancelled) {
		t.Fatalf("resume with nothing scheduled: %v", err)
	}
	cancelAt, err := s.Cancel(ctx, user(t, pool, a))
	if err != nil || !cancelAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cancel: %v %v", cancelAt, err)
	}
	if patches := f.Bodies["PATCH /subscriptions/"+a.SubID]; patches[len(patches)-1]["cancel_at_period_end"] != true {
		t.Fatal("cancel is at the period's end")
	}
	if _, err := s.Cancel(ctx, user(t, pool, a)); !errors.Is(err, billing.ErrAlreadyCancelled) {
		t.Fatalf("second cancel: %v", err)
	}
	if k := eventKinds(t, pool, a); len(k) != 2 || k[1] != "subscription_cancelled" {
		t.Fatalf("events %v", k)
	}
	if p := accountEmail(t, pool, a, "subscription_cancelled", "Plus", "1 November 2026 at 00:00 UTC"); p["plan"] != "plus" {
		t.Fatalf("subscription_cancelled payload %v", p)
	}
	resumed, err := s.Resume(ctx, user(t, pool, a))
	if err != nil || resumed.CancelAt != nil {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	if patches := f.Bodies["PATCH /subscriptions/"+a.SubID]; patches[len(patches)-1]["cancel_at_period_end"] != false {
		t.Fatal("resume PATCHes cancel_at_period_end false")
	}
	// No subscription: ErrNoSubscription.
	n := seedAccount(t, pool, "", "none", "", "")
	if _, err := s.ChangePlan(ctx, user(t, pool, n), "plus"); !errors.Is(err, billing.ErrNoSubscription) {
		t.Fatalf("no subscription: %v", err)
	}
	// Upgrade needs a free seat.
	fullS, _ := newService(t, pool, f, fullSeats{})
	b := seedAccount(t, pool, "solo", "active", "", "")
	if _, err := fullS.ChangePlan(ctx, user(t, pool, b), "plus"); !errors.Is(err, billing.ErrNoSeat) {
		t.Fatalf("no seat: %v", err)
	}
}

// TestPlanChangePlusPro moves between the two upper plans: Plus to Pro
// takes two more seats at once; Pro to Plus is refused while three large
// machines run (24 GB > 16) and scheduled once one stops.
func TestPlanChangePlusPro(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	s, _ := newService(t, pool, f, nil)
	ctx := context.Background()
	a := seedAccount(t, pool, "plus", "active", "large", "running")
	f.AddSubscription(a.SubID, "ctm_"+a.Handle, "prod_plus_test", "active")

	ch, err := s.ChangePlan(ctx, user(t, pool, a), "pro")
	if err != nil || ch.Plan != "pro" || ch.ScheduledPlan != nil {
		t.Fatalf("upgrade: %+v %v", ch, err)
	}
	patch := f.Bodies["PATCH /subscriptions/"+a.SubID][0]
	if patch["proration_behavior"] != "invoice" || patch["product_id"] != "prod_pro_test" {
		t.Fatalf("upgrade body: %v", patch)
	}
	sub, _ := billing.GetSubscription(ctx, pool, a.SubID)
	if sub.Plan != "pro" || sub.Seats != 4 {
		t.Fatalf("row after upgrade: %+v", sub)
	}
	if p := accountEmail(t, pool, a, "plan_changed", "from Plus to Pro"); p["to_plan"] != "pro" {
		t.Fatalf("plan_changed payload %v", p)
	}
	addProject(t, pool, a, "api", "large", "running", 40<<30)
	third := addProject(t, pool, a, "web", "large", "running", 40<<30)
	_, err = s.ChangePlan(ctx, user(t, pool, a), "plus")
	var over *billing.OverPlanError
	if !errors.As(err, &over) || over.RunningGB != 24 {
		t.Fatalf("over plan: %v", err)
	}
	if _, err := pool.Exec(ctx, "update projects set state = 'stopped' where id = $1", third); err != nil {
		t.Fatal(err)
	}
	ch, err = s.ChangePlan(ctx, user(t, pool, a), "plus")
	if err != nil || ch.Plan != "pro" || ch.ScheduledPlan == nil || *ch.ScheduledPlan != "plus" {
		t.Fatalf("downgrade: %+v %v", ch, err)
	}
	if patches := f.Bodies["PATCH /subscriptions/"+a.SubID]; patches[len(patches)-1]["product_id"] != "prod_plus_test" {
		t.Fatalf("downgrade body: %v", patches[len(patches)-1])
	}
}

func TestPortalAndInvoices(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	s, _ := newService(t, pool, f, nil)
	ctx := context.Background()
	a := seedAccount(t, pool, "plus", "active", "", "")
	f.AddCustomer(a.UserID.String())
	url, err := s.Portal(ctx, user(t, pool, a), "")
	if err != nil || !strings.Contains(url, "/portal?customer_session_token=") {
		t.Fatalf("portal: %s %v", url, err)
	}
	if b := f.Bodies["POST /customer-sessions/"][0]; b["external_customer_id"] != a.UserID.String() || b["return_url"] != "https://repose.herakraft.co/billing" {
		t.Fatalf("customer session body: %v", b)
	}
	// Polar has no payment-method deep link: the same portal.
	if pm, err := s.Portal(ctx, user(t, pool, a), "payment_method"); err != nil || pm != url {
		t.Fatalf("payment method link: %s %v", pm, err)
	}
	// Never subscribed: no portal.
	n := seedAccount(t, pool, "", "none", "", "")
	if _, err := s.Portal(ctx, user(t, pool, n), ""); !errors.Is(err, billing.ErrNoSubscription) {
		t.Fatalf("portal without a subscription: %v", err)
	}
	paid := f.AddOrder(a.UserID.String(), a.SubID, "paid", 6490, 590, true)
	fresh := f.AddOrder(a.UserID.String(), a.SubID, "paid", 2380, 380, false)
	f.AddOrder(a.UserID.String(), a.SubID, "draft", 100, 0, false)
	f.AddOrder("someone-else", "sub_x", "paid", 999, 0, true)
	inv, err := s.Invoices(ctx, user(t, pool, a))
	if err != nil || len(inv) != 2 {
		t.Fatalf("invoices: %v %v", inv, err)
	}
	byID := map[string]map[string]any{}
	for _, i := range inv {
		byID[i["id"].(string)] = i
		for _, k := range []string{"id", "number", "status", "currency", "amount_cents", "subtotal_cents", "tax_cents", "created_at", "period_start", "period_end", "hosted_url", "pdf_url"} {
			if _, ok := i[k]; !ok {
				t.Errorf("invoice lacks %s", k)
			}
		}
	}
	i := byID[paid]
	if i["amount_cents"] != int64(6490) || i["tax_cents"] != int64(590) || i["subtotal_cents"] != int64(5900) || i["status"] != "paid" || i["currency"] != "USD" || !strings.HasSuffix(i["pdf_url"].(string), ".pdf") {
		t.Fatalf("invoice shape: %v", i)
	}
	// An invoice Polar has not generated has no link yet and is asked for.
	if byID[fresh]["pdf_url"] != nil || f.Count("POST /orders/"+fresh+"/invoice") != 1 {
		t.Fatalf("ungenerated invoice: %v (%d requests)", byID[fresh], f.Count("POST /orders/"+fresh+"/invoice"))
	}
	inv, _ = s.Invoices(ctx, user(t, pool, a))
	for _, i := range inv {
		if i["id"] == fresh && i["pdf_url"] == nil {
			t.Fatal("the generated invoice has no link on the next listing")
		}
	}
	// No customer yet: an empty list, not an error.
	if inv, err := s.Invoices(ctx, user(t, pool, n)); err != nil || len(inv) != 0 {
		t.Fatalf("no customer: %v %v", inv, err)
	}
}

// Account deletion: the pending overage is sent first; with some owed the
// subscription is cancelled at the period's end so Polar bills it, with
// none it is revoked at once.
func TestCloseAccount(t *testing.T) {
	pool := testdb.Open(t)
	f := newFakePolar()
	defer f.Close()
	s, _ := newService(t, pool, f, nil)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	f.AddSubscription(a.SubID, "ctm_"+a.Handle, "prod_solo_test", "active")
	usageHour(t, pool, a.ProjectID, a.Period.Start, "large", 3600, 270<<30, a.Period)

	if err := s.CloseAccount(ctx, a.UserID); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range f.Requests {
		if strings.HasPrefix(r, "POST /events/") || strings.Contains(r, "/subscriptions/") {
			order = append(order, r)
		}
	}
	if len(order) != 2 || order[0] != "POST /events/ingest" || order[1] != "PATCH /subscriptions/"+a.SubID {
		t.Fatalf("order: %v", order)
	}
	if f.Bodies["PATCH /subscriptions/"+a.SubID][0]["cancel_at_period_end"] != true {
		t.Fatal("with overage owed the subscription ends at the period's end")
	}
	var cents int64
	var ref *string
	if err := pool.QueryRow(ctx, "select cents, sent_ref from overage_charges where subscription_id = $1", a.SubID).Scan(&cents, &ref); err != nil || cents != 100 || ref == nil {
		t.Fatalf("overage line: %d %v %v", cents, ref, err)
	}
	sub, _ := billing.GetSubscription(ctx, pool, a.SubID)
	if sub.CancelAt == nil || !sub.CancelAt.Equal(a.Period.End) {
		t.Fatalf("after close with overage: %+v", sub)
	}
	// Nothing owed: revoked at once, the account has no plan.
	b := seedAccount(t, pool, "solo", "active", "large", "running")
	f.AddSubscription(b.SubID, "ctm_"+b.Handle, "prod_solo_test", "active")
	if err := s.CloseAccount(ctx, b.UserID); err != nil {
		t.Fatal(err)
	}
	if f.Count("DELETE /subscriptions/"+b.SubID) != 1 || f.Count("PATCH /subscriptions/"+b.SubID) != 0 {
		t.Fatalf("revoke: %v", f.Requests)
	}
	sub, _ = billing.GetSubscription(ctx, pool, b.SubID)
	if sub.Status != "canceled" || userField(t, pool, b, "billing_status") != "none" {
		t.Fatalf("after close: %s %s", sub.Status, userField(t, pool, b, "billing_status"))
	}
	// A user without a subscription: nothing to do.
	n := seedAccount(t, pool, "", "none", "", "")
	if err := s.CloseAccount(ctx, n.UserID); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionSeatsStub(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	seedAccount(t, pool, "plus", "active", "", "")
	seedAccount(t, pool, "solo", "trial", "", "")
	c := seedAccount(t, pool, "solo", "active", "", "")
	if _, err := pool.Exec(ctx, "update subscriptions set status = 'canceled' where id = $1", c.SubID); err != nil {
		t.Fatal(err)
	}
	s := &billing.SubscriptionSeats{Pool: pool, Total: 4}
	count, err := s.Count(ctx)
	if err != nil || count.Total != 4 || count.Held != 3 || count.Free != 1 {
		t.Fatalf("count: %+v %v", count, err)
	}
	if ok, place, err := s.Reserve(ctx, "x", 1); err != nil || !ok || place != nil {
		t.Fatalf("one seat free: %v %v %v", ok, place, err)
	}
	if ok, _, err := s.Reserve(ctx, "x", 2); err != nil || ok {
		t.Fatalf("two seats: %v %v", ok, err)
	}
	unlimited := &billing.SubscriptionSeats{Pool: pool}
	if ok, _, err := unlimited.Reserve(ctx, "x", 2); err != nil || !ok {
		t.Fatalf("SEATS_TOTAL=0 is unlimited: %v %v", ok, err)
	}
	if err := s.Converted(ctx, "x"); err != nil {
		t.Fatal(err)
	}
}
