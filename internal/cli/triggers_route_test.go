package cli

import (
	"context"
	"strconv"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// ENG-32: `repose triggers add NAME --route R` on a route that already
// forwards a provider's deliveries to a port moves who answers that URL,
// from the customer's app to an agent, and said nothing about it. One
// door with two receivers is the design (I-1360); a receiver changing
// under a live URL has to be said, and taking it over has to be the
// user's choice.
func TestTriggerAddOverARouteThatForwardsAsks(t *testing.T) {
	ctx := context.Background()

	// asker records the prompt and answers as told.
	type asker struct {
		prompts []string
		answer  bool
	}

	newEnv := func(t *testing.T) (*Env, *Project, *fakeapi.Fake, *discardWriter) {
		t.Helper()
		fake := fakeapi.New(fakeapi.Options{})
		t.Cleanup(fake.Close)
		e, p := newRoundtripEnv(t, fake)
		out := &discardWriter{}
		e.Out, e.ErrOut = out, out
		// The route the customer's provider already posts to.
		if err := WebhooksAddCmd(ctx, e, "", "council", "3000/hook", nil); err != nil {
			t.Fatal(err)
		}
		out.buf.Reset()
		return e, p, fake, out
	}

	target := func(t *testing.T, e *Env, p *Project) string {
		t.Helper()
		r, err := e.Client.GetWebhook(ctx, p.ID, "council")
		if err != nil {
			t.Fatal(err)
		}
		if r.Trigger != "" {
			return "trigger " + r.Trigger
		}
		return ":" + strconv.Itoa(r.Port) + r.Path
	}

	t.Run("a no leaves the route forwarding", func(t *testing.T) {
		e, p, _, out := newEnv(t)
		a := &asker{}
		o := TriggerAddOptions{Route: "council",
			Confirm: func(prompt string) (bool, error) { a.prompts = append(a.prompts, prompt); return a.answer, nil }}
		err := TriggersAddCmd(ctx, e, "", "onhook", o)
		if err == nil {
			t.Fatalf("a no made the trigger anyway; output %q", out.buf.String())
		}
		if len(a.prompts) != 1 {
			t.Fatalf("asked %d times, want 1: %q", len(a.prompts), a.prompts)
		}
		// The prompt has to name what answers that URL now.
		if !strings.Contains(a.prompts[0], "3000/hook") || !strings.Contains(a.prompts[0], "council") {
			t.Fatalf("the question does not say what it takes over: %q", a.prompts[0])
		}
		if got := target(t, e, p); got != ":3000/hook" {
			t.Fatalf("the route now answers with %q after a no", got)
		}
	})

	t.Run("a yes takes it over and says so", func(t *testing.T) {
		e, p, _, out := newEnv(t)
		a := &asker{answer: true}
		o := TriggerAddOptions{Route: "council",
			Confirm: func(prompt string) (bool, error) { a.prompts = append(a.prompts, prompt); return a.answer, nil }}
		if err := TriggersAddCmd(ctx, e, "", "onhook", o); err != nil {
			t.Fatal(err)
		}
		if len(a.prompts) != 1 {
			t.Fatalf("asked %d times, want 1", len(a.prompts))
		}
		if got := target(t, e, p); got != "trigger onhook" {
			t.Fatalf("the route answers with %q after a yes", got)
		}
		// Re-running the same add is not a takeover: the route already
		// points at this trigger, so it must not ask again.
		a.prompts = nil
		out.buf.Reset()
		if err := TriggersAddCmd(ctx, e, "", "onhook", o); err != nil {
			t.Fatal(err)
		}
		if len(a.prompts) != 0 {
			t.Fatalf("asked again for the trigger that already owns the route: %q", a.prompts)
		}
	})

	t.Run("a route that is not there is not a takeover", func(t *testing.T) {
		e, _, _, _ := newEnv(t)
		a := &asker{}
		o := TriggerAddOptions{Route: "fresh",
			Confirm: func(prompt string) (bool, error) { a.prompts = append(a.prompts, prompt); return a.answer, nil }}
		if err := TriggersAddCmd(ctx, e, "", "onfresh", o); err != nil {
			t.Fatal(err)
		}
		if len(a.prompts) != 0 {
			t.Fatalf("asked about a route it created: %q", a.prompts)
		}
	})

	t.Run("without a terminal it refuses and names --yes", func(t *testing.T) {
		e, p, _, _ := newEnv(t)
		o := TriggerAddOptions{Route: "council"} // no Confirm: no terminal
		err := TriggersAddCmd(ctx, e, "", "onhook", o)
		if err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("error %v, want one naming --yes", err)
		}
		if got := target(t, e, p); got != ":3000/hook" {
			t.Fatalf("the route answers with %q after a refusal", got)
		}
	})

	t.Run("--yes takes it over with no question", func(t *testing.T) {
		e, p, _, _ := newEnv(t)
		a := &asker{}
		o := TriggerAddOptions{Route: "council", Yes: true,
			Confirm: func(prompt string) (bool, error) { a.prompts = append(a.prompts, prompt); return false, nil }}
		if err := TriggersAddCmd(ctx, e, "", "onhook", o); err != nil {
			t.Fatal(err)
		}
		if len(a.prompts) != 0 {
			t.Fatalf("--yes still asked: %q", a.prompts)
		}
		if got := target(t, e, p); got != "trigger onhook" {
			t.Fatalf("--yes left the route at %q", got)
		}
	})
}
