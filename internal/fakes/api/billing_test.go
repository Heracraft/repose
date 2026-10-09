package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A JSON body decoded loosely, for asserting on a few fields.
func decodeMap(t *testing.T, r resp) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
	return m
}

func detailOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	m := decodeMap(t, r)
	e, _ := m["error"].(map[string]any)
	d, _ := e["detail"].(map[string]any)
	if d == nil {
		t.Fatalf("no detail: %s", r.body)
	}
	return d
}

func mkPlanProject(t *testing.T, f *Fake, name, class string) map[string]any {
	t.Helper()
	r := call(t, f, "POST", "/v1/projects", tok, map[string]string{"name": name, "remote_url": "github.com/x/" + name, "class": class})
	want(t, r, 201)
	return decodeMap(t, r)
}

// GET /billing without a subscription: the plans with their availability,
// the seats, no waitlist.
func TestBillingNoSubscription(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetBilling(BillingNone)
	r := call(t, f, "GET", "/v1/billing", tok, nil)
	want(t, r, 200)
	var b struct {
		Subscription *json.RawMessage `json:"subscription"`
		Plans        []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
			MemoryGB  int    `json:"memory_gb"`
		} `json:"plans"`
		Seats  Seats `json:"seats"`
		Usage struct {
			MemoryGB int `json:"memory_gb"`
		} `json:"usage"`
	}
	r.json(t, &b)
	if b.Subscription != nil && string(*b.Subscription) != "null" {
		t.Fatalf("subscription: %s", *b.Subscription)
	}
	if len(b.Plans) != 3 || b.Plans[0].ID != "solo" || b.Plans[1].ID != "plus" || b.Plans[2].ID != "pro" ||
		!b.Plans[0].Available || !b.Plans[1].Available || !b.Plans[2].Available ||
		b.Plans[0].MemoryGB != 8 || b.Plans[1].MemoryGB != 16 || b.Plans[2].MemoryGB != 32 {
		t.Fatalf("plans: %s", r.body)
	}
	if b.Seats.Free != 18 || b.Usage.MemoryGB != 0 {
		t.Fatalf("billing: %s", r.body)
	}
	// Three seats free: Solo and Plus yes, Pro (four seats) no.
	f.SetSeats(30, 27, 0)
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	r.json(t, &b)
	if !b.Plans[0].Available || !b.Plans[1].Available || b.Plans[2].Available {
		t.Fatalf("plans with three seats: %s", r.body)
	}
	// One seat free: Solo yes, Plus and Pro no.
	f.SetSeats(30, 29, 0)
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	r.json(t, &b)
	if !b.Plans[0].Available || b.Plans[1].Available || b.Plans[2].Available {
		t.Fatalf("plans with one seat: %s", r.body)
	}
	// The GET /me projection.
	r = call(t, f, "GET", "/v1/me", tok, nil)
	if !strings.Contains(string(r.body), `"status":"none"`) || !strings.Contains(string(r.body), `"limits":{"projects":0,"xl":0,"memory_gb":0,"disk_gb":0,"egress_gb":0}`) {
		t.Fatalf("me: %s", r.body)
	}
}

// Checkout answers the hosted checkout's URL; completing it (the webhook)
// makes the account trialing on that plan, a week out from the first
// charge.
func TestBillingCheckoutToTrial(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetBilling(BillingNone)
	wantErr(t, call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "gold"}), 400, "invalid")
	r := call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "plus"})
	want(t, r, 200)
	m := decodeMap(t, r)
	url, _ := m["url"].(string)
	if !strings.HasPrefix(url, CheckoutBase+"chk_fake_") || len(m) != 1 {
		t.Fatalf("checkout: %s", r.body)
	}
	if _, err := f.CompleteCheckout("chk_nope"); err == nil {
		t.Fatal("completed an unknown checkout")
	}
	if back, err := f.CompleteCheckout(strings.TrimPrefix(url, CheckoutBase)); err != nil || back != "" {
		t.Fatalf("complete: %q %v", back, err)
	}
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	var b struct {
		Subscription struct {
			Plan     string     `json:"plan"`
			Status   string     `json:"status"`
			Seats    int        `json:"seats"`
			TrialEnd *time.Time `json:"trial_end"`
			NextBill *time.Time `json:"next_billed_at"`
		} `json:"subscription"`
		Seats Seats `json:"seats"`
	}
	r.json(t, &b)
	if b.Subscription.Plan != "plus" || b.Subscription.Status != "trialing" || b.Subscription.Seats != 2 || b.Subscription.TrialEnd == nil || b.Subscription.NextBill == nil {
		t.Fatalf("after checkout: %s", r.body)
	}
	if !b.Subscription.NextBill.Equal(*b.Subscription.TrialEnd) {
		t.Fatalf("first charge is at the trial's end: %s", r.body)
	}
	if b.Seats.Held != 14 {
		t.Fatalf("seats held after a Plus checkout: %d", b.Seats.Held)
	}
	// A second checkout is a conflict: change the plan instead.
	r = call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "solo"})
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "subscribed" {
		t.Fatalf("detail: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/me", tok, nil)
	if !strings.Contains(string(r.body), `"status":"trial"`) || !strings.Contains(string(r.body), `"plan":"plus"`) || !strings.Contains(string(r.body), `"xl":1`) {
		t.Fatalf("me: %s", r.body)
	}
}

// No free seat: the checkout puts the user on the waitlist and a second
// call answers the same place; an invited hold counts toward the checkout.
func TestBillingWaitlist(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetBilling(BillingNone)
	f.SetSeats(30, 30, 40)
	r := call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "solo"})
	wantErr(t, r, 503, "waitlisted")
	d := detailOf(t, r)
	if d["position"] != float64(41) || d["email"] != CannedUser.Email || d["joined_at"] == nil {
		t.Fatalf("waitlisted detail: %s", r.body)
	}
	if !strings.Contains(string(r.body), "You're number 41 on the waitlist") {
		t.Fatalf("message: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/billing/waitlist", tok, nil)
	want(t, r, 200)
	if decodeMap(t, r)["position"] != float64(41) {
		t.Fatalf("second join: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/public/seats", "", nil)
	if !strings.Contains(string(r.body), `"free":0,"waiting":41`) {
		t.Fatalf("public seats: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/me", tok, nil)
	if !strings.Contains(string(r.body), `"waitlist":{"position":41,`) {
		t.Fatalf("me waitlist: %s", r.body)
	}
	// Invited: the hold is the user's seat.
	f.SetWaitlist(1, time.Now().Add(70*time.Hour))
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"invited_at":"`) || !strings.Contains(string(r.body), `"hold_until":"`) || !strings.Contains(string(r.body), `"id":"solo","name":"Solo","price_cents":2900,"currency":"USD","trial_days":7,"seats":1,"memory_gb":8,"disk_gb":100,"egress_gb":250,"project_limit":100,"intro_price_cents":2000,"intro_months":3,"intro_egress_gb":100,"available":true`) || !strings.Contains(string(r.body), `"intro_eligible":true`) {
		t.Fatalf("invited billing: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "solo"})
	want(t, r, 200)
	url, _ := decodeMap(t, r)["url"].(string)
	if _, err := f.CompleteCheckout(strings.TrimPrefix(url, CheckoutBase)); err != nil {
		t.Fatal(err)
	}
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"waitlist":null`) || !strings.Contains(string(r.body), `"waiting":40`) {
		t.Fatalf("after conversion: %s", r.body)
	}
	// An expired hold does not count.
	f.SetBilling(BillingNone)
	f.SetSeats(30, 30, 40)
	f.SetWaitlist(1, time.Now().Add(-time.Hour))
	wantErr(t, call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "solo"}), 503, "waitlisted")
}

// Plan changes: up at once when a seat is free, down at the renewal
// unless the running memory or disk would not fit.
func TestBillingPlanChange(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetBilling(BillingNone)
	r := call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "plus"})
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "no_subscription" {
		t.Fatalf("detail: %s", r.body)
	}
	f.SetBilling(BillingActive)
	f.SetSeats(30, 30, 0)
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "plus"})
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "no_seat" {
		t.Fatalf("detail: %s", r.body)
	}
	f.SetSeats(30, 29, 0)
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "plus"})
	want(t, r, 200)
	m := decodeMap(t, r)
	if m["plan"] != "plus" || m["scheduled_plan"] != nil {
		t.Fatalf("upgrade: %s", r.body)
	}
	// Two large machines fit Plus and not Solo: the downgrade is refused.
	mkPlanProject(t, f, "a", "large")
	mkPlanProject(t, f, "b", "large")
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "solo"})
	wantErr(t, r, 409, "conflict")
	d := detailOf(t, r)
	if d["reason"] != "over_plan" || d["running_gb"] != float64(16) || d["disk_held_gb"] == nil || d["disk_allocated_gb"] == nil {
		t.Fatalf("over_plan detail: %s", r.body)
	}
	// Stop one and the downgrade is scheduled for the period's end.
	var list []struct{ ID string }
	call(t, f, "GET", "/v1/projects", tok, nil).json(t, &list)
	want(t, call(t, f, "POST", "/v1/projects/"+list[0].ID+"/stop", tok, map[string]bool{"snapshot": false}), 202)
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "solo"})
	want(t, r, 200)
	m = decodeMap(t, r)
	if m["plan"] != "plus" || m["scheduled_plan"] != "solo" || m["effective_at"] == nil {
		t.Fatalf("downgrade: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"scheduled_plan":"solo"`) {
		t.Fatalf("billing after downgrade: %s", r.body)
	}
	// Choosing the current plan again undoes the schedule.
	want(t, call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "plus"}), 200)
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"scheduled_plan":null`) {
		t.Fatalf("billing after undo: %s", r.body)
	}
}

// Pro holds four seats: the checkout needs four free, and the trial
// carries Pro's limits (32 GB, an xl) and the account cap of 100 projects.
func TestBillingCheckoutPro(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetBilling(BillingNone)
	f.SetSeats(30, 27, 0)
	wantErr(t, call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "pro"}), 503, "waitlisted")
	f.SetBilling(BillingNone)
	f.SetWaitlist(0, time.Time{})
	f.SetSeats(30, 26, 0)
	r := call(t, f, "POST", "/v1/billing/checkout", tok, map[string]string{"plan": "pro"})
	want(t, r, 200)
	url, _ := decodeMap(t, r)["url"].(string)
	if _, err := f.CompleteCheckout(strings.TrimPrefix(url, CheckoutBase)); err != nil {
		t.Fatal(err)
	}
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	var b struct {
		Subscription struct {
			Plan  string `json:"plan"`
			Seats int    `json:"seats"`
		} `json:"subscription"`
		Seats Seats `json:"seats"`
	}
	r.json(t, &b)
	if b.Subscription.Plan != "pro" || b.Subscription.Seats != 4 || b.Seats.Held != 30 {
		t.Fatalf("after a Pro checkout: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/me", tok, nil)
	if !strings.Contains(string(r.body), `"limits":{"projects":100,"xl":1,"memory_gb":32,"disk_gb":500,"egress_gb":1000}`) {
		t.Fatalf("me: %s", r.body)
	}
}

// With three plans a change can skip one: Solo straight to Pro takes the
// three extra seats at once, and Pro down to Plus waits for the renewal
// unless what runs does not fit Plus's 16 GB.
func TestBillingPlanChangePro(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetBilling(BillingActive)
	f.SetPlan("solo")
	f.SetSeats(30, 28, 0)
	r := call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "pro"})
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "no_seat" {
		t.Fatalf("detail: %s", r.body)
	}
	f.SetSeats(30, 27, 0)
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "pro"})
	want(t, r, 200)
	if m := decodeMap(t, r); m["plan"] != "pro" || m["scheduled_plan"] != nil {
		t.Fatalf("upgrade: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"held":30`) {
		t.Fatalf("seats after Solo to Pro: %s", r.body)
	}
	// Three large machines (24 GB) fit Pro and not Plus.
	mkPlanProject(t, f, "a", "large")
	mkPlanProject(t, f, "b", "large")
	mkPlanProject(t, f, "c", "large")
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "plus"})
	wantErr(t, r, 409, "conflict")
	d := detailOf(t, r)
	if d["reason"] != "over_plan" || d["running_gb"] != float64(24) {
		t.Fatalf("over_plan detail: %s", r.body)
	}
	// Stop one: 16 GB fits Plus, and the downgrade waits for the renewal.
	var list []struct{ ID string }
	call(t, f, "GET", "/v1/projects", tok, nil).json(t, &list)
	want(t, call(t, f, "POST", "/v1/projects/"+list[0].ID+"/stop", tok, map[string]bool{"snapshot": false}), 202)
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "plus"})
	want(t, r, 200)
	if m := decodeMap(t, r); m["plan"] != "pro" || m["scheduled_plan"] != "plus" || m["effective_at"] == nil {
		t.Fatalf("downgrade: %s", r.body)
	}
	// Solo is still out of reach with 16 GB running.
	r = call(t, f, "POST", "/v1/billing/plan", tok, map[string]string{"plan": "solo"})
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "over_plan" {
		t.Fatalf("Pro to Solo: %s", r.body)
	}
}

func TestBillingCancelResume(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetBilling(BillingTrial)
	r := call(t, f, "POST", "/v1/billing/resume", tok, nil)
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "not_cancelled" {
		t.Fatalf("detail: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/billing/cancel", tok, nil)
	want(t, r, 200)
	cancelAt, _ := decodeMap(t, r)["cancel_at"].(string)
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	// During the trial the plan ends at the trial's end (PRICING.md).
	if !strings.Contains(string(r.body), `"trial_end":"`+cancelAt+`"`) || !strings.Contains(string(r.body), `"cancel_at":"`+cancelAt+`"`) || !strings.Contains(string(r.body), `"next_billed_at":null`) {
		t.Fatalf("cancelled: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/billing/cancel", tok, nil)
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "already_cancelled" {
		t.Fatalf("detail: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/billing/resume", tok, nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"plan":"solo"`) {
		t.Fatalf("resume: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"cancel_at":null`) {
		t.Fatalf("resumed: %s", r.body)
	}
}

// Every payment_required reason of api.md, with its detail.
func TestBillingGates(t *testing.T) {
	f := New(Options{})
	defer f.Close()

	gate := func(mode string, reason string) resp {
		t.Helper()
		f.SetBilling(mode)
		r := call(t, f, "POST", "/v1/projects", tok, map[string]string{"name": "g-" + reason, "remote_url": "github.com/x/" + reason, "class": "large"})
		wantErr(t, r, 402, "payment_required")
		if got := detailOf(t, r)["reason"]; got != reason {
			t.Fatalf("reason %v, want %s: %s", got, reason, r.body)
		}
		return r
	}
	r := gate(BillingNone, "subscription_required")
	if detailOf(t, r)["waitlist"] != nil {
		t.Fatalf("waitlist while not waiting: %s", r.body)
	}
	f.SetWaitlisted(3)
	r = gate(BillingNone, "subscription_required")
	if w, _ := detailOf(t, r)["waitlist"].(map[string]any); w == nil || w["position"] != float64(3) {
		t.Fatalf("waitlist detail: %s", r.body)
	}
	f.SetWaitlisted(0)
	gate(BillingPastDue, "past_due")
	gate(BillingSuspended, "suspended")

	// plan_limit: Solo holds 8 GB; a large is running.
	f.SetBilling(BillingActive)
	a := mkPlanProject(t, f, "first", "large")
	r = call(t, f, "POST", "/v1/projects", tok, map[string]string{"name": "second", "remote_url": "github.com/x/second", "class": "small"})
	wantErr(t, r, 402, "payment_required")
	d := detailOf(t, r)
	if d["reason"] != "plan_limit" || d["plan"] != "solo" || d["limit_gb"] != float64(8) || d["used_gb"] != float64(8) {
		t.Fatalf("plan_limit detail: %s", r.body)
	}
	if projects, _ := d["projects"].([]any); len(projects) != 1 || projects[0] != "first" {
		t.Fatalf("plan_limit projects: %s", r.body)
	}
	if !strings.Contains(string(r.body), "first is using it. Stop one or upgrade.") {
		t.Fatalf("plan_limit message: %s", r.body)
	}
	// Stopped, the second one can be created; starting it again is the
	// same gate, and a restart of a running project is not counted twice.
	id, _ := a["id"].(string)
	want(t, call(t, f, "POST", "/v1/projects/"+id+"/start", tok, nil), 202)
	want(t, call(t, f, "POST", "/v1/projects/"+id+"/stop", tok, map[string]bool{"snapshot": false}), 202)
	b := mkPlanProject(t, f, "second", "small")
	r = call(t, f, "POST", "/v1/projects/"+id+"/start", tok, nil)
	wantErr(t, r, 402, "payment_required")
	if detailOf(t, r)["reason"] != "plan_limit" {
		t.Fatalf("start over the plan: %s", r.body)
	}
	// An xl needs Plus or Pro.
	bid, _ := b["id"].(string)
	want(t, call(t, f, "POST", "/v1/projects/"+bid+"/stop", tok, map[string]bool{"snapshot": false}), 202)
	r = call(t, f, "POST", "/v1/projects", tok, map[string]string{"name": "big", "remote_url": "github.com/x/big", "class": "xl"})
	wantErr(t, r, 402, "payment_required")
	if detailOf(t, r)["reason"] != "plan_limit" {
		t.Fatalf("xl on solo: %s", r.body)
	}

	// disk_limit: one disk past Solo's 100 GB.
	r = call(t, f, "POST", "/v1/projects/"+id+"/resize", tok, map[string]int64{"volume_bytes": 200 << 30})
	wantErr(t, r, 402, "payment_required")
	d = detailOf(t, r)
	if d["reason"] != "disk_limit" || d["limit_gb"] != float64(100) || d["plan"] != "solo" || d["volume_gb"] != float64(200) {
		t.Fatalf("disk_limit detail: %s", r.body)
	}
	// The plan's disk counts what the projects hold (I-585): two projects
	// of 40 and 20 GB hold 1 GB each, and a third fits.
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	var ov struct {
		Usage map[string]any `json:"usage"`
	}
	r.json(t, &ov)
	if u := ov.Usage; u["disk_held_gb"] != float64(2) || u["disk_allocated_gb"] != float64(2) {
		t.Fatalf("usage: %s", r.body)
	}
	// Holding 99.5 GB, a new project is refused, and so is growing a disk.
	held := func(gb float64) {
		t.Helper()
		if err := f.SetBillingState(BillingState{DiskHeldGB: &gb}); err != nil {
			t.Fatal(err)
		}
	}
	held(99.5)
	r = call(t, f, "POST", "/v1/projects", tok, map[string]string{"name": "third", "remote_url": "github.com/x/third", "class": "small"})
	wantErr(t, r, 402, "payment_required")
	if d := detailOf(t, r); d["reason"] != "disk_limit" || d["held_gb"] != 99.5 || !strings.Contains(string(r.body), "hold 99.5 GB and your Solo plan has 100 GB of disk; this needs about 1 GB more") {
		t.Fatalf("create over the disk: %s", r.body)
	}
	held(101)
	r = call(t, f, "POST", "/v1/projects/"+id+"/resize", tok, map[string]int64{"volume_bytes": 60 << 30})
	wantErr(t, r, 402, "payment_required")
	if detailOf(t, r)["reason"] != "disk_limit" {
		t.Fatalf("grow over the disk: %s", r.body)
	}
	held(-1)

	// egress_limit: four times the allowance stops everything. During
	// Solo's introductory offer the allowance is 100 GB (I-497).
	f.SetEgress(400)
	r = call(t, f, "POST", "/v1/projects/"+id+"/start", tok, nil)
	wantErr(t, r, 402, "payment_required")
	if d = detailOf(t, r); d["reason"] != "egress_limit" || d["limit_gb"] != float64(400) {
		t.Fatalf("egress_limit during the offer: %s", r.body)
	}
	used := true
	if err := f.SetBillingState(BillingState{IntroUsed: &used}); err != nil {
		t.Fatal(err)
	}
	f.SetEgress(1000)
	r = call(t, f, "POST", "/v1/projects/"+id+"/start", tok, nil)
	wantErr(t, r, 402, "payment_required")
	d = detailOf(t, r)
	if d["reason"] != "egress_limit" || d["limit_gb"] != float64(1000) || d["used_gb"] != float64(1000) || d["until"] == nil {
		t.Fatalf("egress_limit detail: %s", r.body)
	}
	// Under it, the overage is a line in usage: 300 GB on Solo is $2.50.
	f.SetEgress(300)
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"egress_gb":300,"egress_included_gb":250,"overage_cents":250,"projects":2,"project_limit":100`) {
		t.Fatalf("usage: %s", r.body)
	}
	// Exempt: no gate at all.
	f.SetBilling(BillingExempt)
	mkPlanProject(t, f, "free", "xl")
}

func TestBillingStateKnob(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	mode, plan := BillingActive, "pro"
	cancelled := true
	egress := 12.5
	inv := []Invoice{{ID: "txn_1", Number: "R-1", Status: "completed", Currency: "USD", AmountCents: 5900}}
	if err := f.SetBillingState(BillingState{Mode: &mode, Plan: &plan, Cancelled: &cancelled, EgressGB: &egress, Invoices: &inv}); err != nil {
		t.Fatal(err)
	}
	r := call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"plan":"pro","status":"active"`) || strings.Contains(string(r.body), `"cancel_at":null`) || !strings.Contains(string(r.body), `"egress_gb":12.5`) {
		t.Fatalf("state: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/billing/invoices", tok, nil)
	if !strings.Contains(string(r.body), `"number":"R-1"`) {
		t.Fatalf("invoices: %s", r.body)
	}
	bad := "gold"
	if err := f.SetBillingState(BillingState{Plan: &bad}); err == nil || err.Error() != "plan: solo, plus or pro" {
		t.Fatalf("accepted an unknown plan: %v", err)
	}
	if err := f.SetBillingState(BillingState{ScheduledPlan: &bad}); err == nil || err.Error() != "scheduled_plan: solo, plus, pro or empty" {
		t.Fatalf("accepted an unknown scheduled plan: %v", err)
	}
	plus := "plus"
	if err := f.SetBillingState(BillingState{ScheduledPlan: &plus}); err != nil {
		t.Fatal(err)
	}
	r = call(t, f, "GET", "/v1/billing", tok, nil)
	if !strings.Contains(string(r.body), `"scheduled_plan":"plus"`) {
		t.Fatalf("scheduled plus: %s", r.body)
	}
}
