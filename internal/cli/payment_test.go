package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// payment_required prints the api's sentence verbatim and exits 7; an
// older api's fragment (or a card-era reason) gets the plan sentence
// (DECISIONS I-289).
func TestPaymentRequiredMessage(t *testing.T) {
	const fallback = "Choose a plan at https://repose.herakraft.co/billing first."
	for _, c := range []struct {
		name string
		err  *APIError
		want string
	}{
		{"plan_limit verbatim", &APIError{Code: "payment_required", Message: "Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.", Detail: map[string]any{"reason": "plan_limit"}},
			"Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing."},
		{"plan_limit names the stop", &APIError{Code: "payment_required", Message: "Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at https://repose.herakraft.co/billing.", Detail: map[string]any{"reason": "plan_limit", "projects": []any{"todo-app"}}},
			"Your Solo plan runs 8 GB at once and todo-app is using it. `repose stop todo-app` frees it, or upgrade at https://repose.herakraft.co/billing."},
		{"plan_limit with two machines names both", &APIError{Code: "payment_required", Message: "Your Plus plan runs 16 GB at once and a and b are using it. Stop one, or upgrade at https://repose.herakraft.co/billing.", Detail: map[string]any{"reason": "plan_limit", "projects": []any{"a", "b"}}},
			"Your Plus plan runs 16 GB at once and a and b are using it. `repose stop a b` frees it, or upgrade at https://repose.herakraft.co/billing."},
		{"plan_limit from another api verbatim", &APIError{Code: "payment_required", Message: "A large (8 GB) would pass the 8 GB of memory Solo gives running machines; todo-app is using it. Stop one or upgrade.", Detail: map[string]any{"reason": "plan_limit", "projects": []any{"todo-app"}}},
			"A large (8 GB) would pass the 8 GB of memory Solo gives running machines; todo-app is using it. Stop one or upgrade."},
		{"egress_limit verbatim", &APIError{Code: "payment_required", Message: "Your machines are stopped until 1 November: this period's egress passed 1000 GB.", Detail: map[string]any{"reason": "egress_limit"}},
			"Your machines are stopped until 1 November: this period's egress passed 1000 GB."},
		{"subscription_required verbatim", &APIError{Code: "payment_required", Message: fallback, Detail: map[string]any{"reason": "subscription_required", "waitlist": nil}}, fallback},
		{"old api card_required", &APIError{Code: "payment_required", Message: "add a card before starting a guest", Detail: map[string]any{"reason": "card_required"}}, fallback},
		{"old api no detail", &APIError{Code: "payment_required", Message: "your trial credit is used up"}, fallback},
		{"new reason, empty message", &APIError{Code: "payment_required", Detail: map[string]any{"reason": "past_due"}}, fallback},
	} {
		if got := paymentRequiredMessage(c.err); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// The generic handler prints it and exits 7.
	var stderr strings.Builder
	code := exitCodeFor(&APIError{Code: "payment_required", Message: "Your last payment failed. Update your card at https://repose.herakraft.co/billing to start machines again.", Detail: map[string]any{"reason": "past_due"}}, &stderr)
	if code != ExitPaymentRequired || !strings.Contains(stderr.String(), "Your last payment failed.") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

// The project cap refusal reads the same from every command (I-569):
// run, restore and the rest print the CLI's sentence for the api's 400,
// not "invalid: ..."; an api from before I-569 (no detail.reason) is
// recognised by its numbers; another invalid is the api's sentence with
// its code after it. The cap exits 7, as the other plan limits (I-623).
func TestProjectLimitMessage(t *testing.T) {
	for _, c := range []struct {
		name string
		err  *APIError
		want string
		code int
	}{
		{"one", &APIError{Code: "invalid", Message: "you have 100 of the 100 projects an account can have, running or stopped; destroy one first",
			Detail: map[string]any{"reason": "project_limit", "limit": float64(100), "projects": float64(100)}},
			"You have 100 of the 100 projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`.\n", ExitPaymentRequired},
		{"several", &APIError{Code: "invalid", Message: "…",
			Detail: map[string]any{"reason": "project_limit", "limit": float64(100), "projects": float64(98), "requested": float64(5)}},
			"You have 98 of the 100 projects an account can have, running or stopped, and 5 more would make 103. Destroy some first with `repose rm PROJECT`.\n", ExitPaymentRequired},
		{"older api", &APIError{Code: "invalid", Message: "you have 6 of 6 projects; destroy one, or upgrade your plan at https://repose.herakraft.co/billing",
			Detail: map[string]any{"limit": float64(6), "projects": float64(6)}},
			"You have 6 of the 6 projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`.\n", ExitPaymentRequired},
		{"other invalid", &APIError{Code: "invalid", Message: "name must match [A-Za-z0-9._-]{1,64}"},
			"Name must match [A-Za-z0-9._-]{1,64} (invalid).\n", ExitGeneric},
		{"other reason", &APIError{Code: "invalid", Message: "something else", Detail: map[string]any{"reason": "other", "limit": float64(1), "projects": float64(1)}},
			"Something else (invalid).\n", ExitGeneric},
	} {
		var buf bytes.Buffer
		if code := exitCodeFor(c.err, &buf); code != c.code || buf.String() != c.want {
			t.Errorf("%s: exit %d, printed %q, want %q", c.name, code, buf.String(), c.want)
		}
	}
}

// I-634: a run on an account with no plan opens the billing page, waits
// for the checkout, and goes on with the same create; without a person
// at the terminal it exits 7 as before.
func TestRunWaitsForAPlan(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	fake.SetBilling(fakeapi.BillingNone)
	e := newLifecycleEnv(t, fake)
	var errOut strings.Builder
	e.ErrOut = &errOut
	ctx := context.Background()

	oldW, oldP, oldO := planWaitable, planPoll, openForPlan
	defer func() { planWaitable, planPoll, openForPlan = oldW, oldP, oldO }()
	planPoll = 10 * time.Millisecond

	planWaitable = func(*Env) bool { return false }
	_, err := createProjectForRun(ctx, e, "", RunOptions{Name: "todo-app"}, nil)
	var out strings.Builder
	if code := exitCodeFor(err, &out); code != ExitPaymentRequired {
		t.Fatalf("off a terminal: exit %d, want 7 (%s)", code, out.String())
	}

	planWaitable = func(*Env) bool { return true }
	t.Setenv(envNoBrowser, "")
	t.Setenv(envDisplay, ":0")
	t.Setenv(envInGuest, "")
	var opened []string
	openForPlan = func(u string) error { opened = append(opened, u); return nil }
	go func() {
		time.Sleep(40 * time.Millisecond)
		fake.SetBilling(fakeapi.BillingTrial)
	}()
	p, err := createProjectForRun(ctx, e, "", RunOptions{Name: "todo-app"}, nil)
	if err != nil {
		t.Fatalf("after the checkout: %v (%s)", err, errOut.String())
	}
	if p.Slug != "todo-app" {
		t.Fatalf("created %q", p.Slug)
	}
	if want := "Waiting for a plan at " + billingURL + "...\n"; errOut.String() != want {
		t.Fatalf("stderr %q, want %q", errOut.String(), want)
	}
	if len(opened) != 1 || opened[0] != billingURL {
		t.Fatalf("opened %v", opened)
	}
}

// On Solo a second large machine is refused for memory; on a terminal
// the run asks stop's question, stops the machine in the way on a yes,
// and creates or starts in the same command. A no and no terminal exit
// 7 with the refusal (I-637).
func TestRunStopsWhatIsInTheWayOfThePlan(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	fake.SetBilling(fakeapi.BillingActive)
	fake.SetPlan("solo")
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	api, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "api", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if err := StartCmd(ctx, e, api.ID); err != nil {
		t.Fatal(err)
	}
	fake.SetAgents(api.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude", State: "working"}})

	oldW, oldA := planWaitable, planStopAsk
	defer func() { planWaitable, planStopAsk = oldW, oldA }()
	var prompts []string
	answer := false
	planWaitable = func(*Env) bool { return true }
	planStopAsk = func(_ context.Context, _ *Env, prompt string) (bool, bool, error) {
		prompts = append(prompts, prompt)
		return true, answer, nil
	}

	// No: the refusal, exit 7, nothing stopped.
	_, err = createProjectForRun(ctx, e, "", RunOptions{Name: "todo-app", Size: "large"}, nil)
	var msg strings.Builder
	if code := exitCodeFor(err, &msg); code != ExitPaymentRequired || !strings.Contains(msg.String(), "`repose stop api` frees it") {
		t.Fatalf("no: exit %d %q", code, msg.String())
	}
	if want := "api has claude (working). Stopping ends it. Stop api to start todo-app? [y/N] "; len(prompts) != 1 || prompts[0] != want {
		t.Fatalf("prompts %q, want %q", prompts, want)
	}
	if s := stateOf(t, e, api.ID); s != "running" {
		t.Fatalf("api %s after a no", s)
	}

	// Off a terminal: exit 7 and no question.
	planWaitable = func(*Env) bool { return false }
	_, err = createProjectForRun(ctx, e, "", RunOptions{Name: "todo-app", Size: "large"}, nil)
	if code := exitCodeFor(err, &msg); code != ExitPaymentRequired || len(prompts) != 1 {
		t.Fatalf("off a terminal: exit %d, %d questions", code, len(prompts))
	}

	// Yes: api stops, todo-app is created, one command.
	planWaitable = func(*Env) bool { return true }
	answer = true
	var out strings.Builder
	e.Out = &out
	p, err := createProjectForRun(ctx, e, "", RunOptions{Name: "todo-app", Size: "large"}, nil)
	if err != nil {
		t.Fatalf("yes: %v", err)
	}
	if p.Slug != "todo-app" || stateOf(t, e, api.ID) != "stopped" {
		t.Fatalf("created %q, api %s", p.Slug, stateOf(t, e, api.ID))
	}
	if !strings.HasPrefix(out.String(), "Stopped api in ") || strings.Contains(out.String(), "Ended") {
		t.Fatalf("out %q", out.String())
	}

	// A start of a stopped machine asks the same, naming the machine.
	if err := StopCmd(ctx, e, p.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := StartCmd(ctx, e, api.ID); err != nil {
		t.Fatal(err)
	}
	if err := StartCmd(ctx, e, p.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if last := prompts[len(prompts)-1]; last != "Stop api to start todo-app? [y/N] " {
		t.Fatalf("start's question %q", last)
	}
	if stateOf(t, e, api.ID) != "stopped" || stateOf(t, e, p.ID) != "running" {
		t.Fatal("start did not swap the machines")
	}
}

// The fewest machines that make room, idle ones before busy ones.
func TestMachinesToStop(t *testing.T) {
	plan := "plus"
	me := &Me{}
	me.Billing.Plan = &plan
	me.Limits.MemoryGB = 16
	busy := &Signals{Agents: []AgentSignal{{Agent: "claude", State: "working"}}}
	ps := []Project{
		{ID: "1", Slug: "a", State: "running", Class: "large", Signals: busy},
		{ID: "2", Slug: "b", State: "running", Class: "small"},
		{ID: "3", Slug: "c", State: "running", Class: "small"},
		{ID: "4", Slug: "d", State: "stopped", Class: "xl"},
	}
	got := slugsOf(machinesToStop(me, ps, "new", "large"))
	if strings.Join(got, " ") != "b c" {
		t.Fatalf("large on plus: %v, want b c", got)
	}
	if got := machinesToStop(me, ps, "new", "small"); len(got) != 1 || got[0].Slug != "b" {
		t.Fatalf("small: %v", slugsOf(got))
	}
	me.Limits.MemoryGB = 8
	if got := machinesToStop(me, ps, "new", "xl"); got != nil {
		t.Fatalf("an xl never fits 8 GB: %v", slugsOf(got))
	}
}
