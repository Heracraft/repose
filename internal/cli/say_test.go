package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose say` against the fake api (DECISIONS I-1144): the three lines
// a sender reads, and the refusals.

func sayTestEnv(t *testing.T) (*Env, *Project, *fakeapi.Fake, *discardWriter) {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	e, p := newRoundtripEnv(t, fake)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	return e, p, fake, out
}

// TestSayToAnIdleAgent: the message reaches the agent and the line names
// the window it went to, once, with nothing after it.
func TestSayToAnIdleAgent(t *testing.T) {
	e, p, fake, out := sayTestEnv(t)
	if err := SayCmd(context.Background(), e, p.Name, "", "run the tests before you commit"); err != nil {
		t.Fatalf("say: %v", err)
	}
	if got := out.buf.String(); got != "Sent to claude.\n" {
		t.Fatalf("output = %q", got)
	}
	says := fake.Says()
	if len(says) != 1 || says[0].Text != "run the tests before you commit" || says[0].Window != "claude" {
		t.Fatalf("the api got %+v", says)
	}
}

// TestSayToAWorkingAgent: the message waits, and the line says when it
// goes in rather than leaving the sender to guess.
func TestSayToAWorkingAgent(t *testing.T) {
	e, p, fake, out := sayTestEnv(t)
	fake.SetSayState("waiting", false)
	if err := SayCmd(context.Background(), e, p.Name, "", "and squash the fixups"); err != nil {
		t.Fatalf("say: %v", err)
	}
	if got := out.buf.String(); got != "claude is working. Your message goes in at the end of the turn.\n" {
		t.Fatalf("output = %q", got)
	}

	// A second one while the first waits says what became of the first.
	out.buf.Reset()
	fake.SetSayState("waiting", true)
	if err := SayCmd(context.Background(), e, p.Name, "", "no, leave them"); err != nil {
		t.Fatalf("say: %v", err)
	}
	want := "claude is working. Your message goes in at the end of the turn, in place of the one that was waiting.\n"
	if got := out.buf.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// TestSayNamesTheAgentsWhenThereIsMoreThanOne: the refusal is the
// machine's own sentence, which lists the windows, so the sender can
// pick one with --agent and does not have to go and look.
func TestSayNamesTheAgentsWhenThereIsMoreThanOne(t *testing.T) {
	e, p, fake, out := sayTestEnv(t)
	fake.SetSayAgents("claude", "codex-2")
	err := SayCmd(context.Background(), e, p.Name, "", "hello")
	if err == nil {
		t.Fatal("a machine with two agents took a message that named neither")
	}
	if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "codex-2") {
		t.Fatalf("refusal %q names neither window", err)
	}
	if out.buf.Len() != 0 {
		t.Fatalf("a refusal printed on stdout: %q", out.buf.String())
	}
	if n := len(fake.Says()); n != 0 {
		t.Fatalf("%d messages stored for a refused say", n)
	}

	// Named, it goes through.
	if err := SayCmd(context.Background(), e, p.Name, "codex-2", "hello"); err != nil {
		t.Fatalf("say --agent: %v", err)
	}
	if got := out.buf.String(); got != "Sent to codex-2.\n" {
		t.Fatalf("output = %q", got)
	}
}

// TestSayWithNoAgentOrNoMachine: a machine running no agent, and one
// that is not running, are both refused before anything is typed.
func TestSayWithNoAgentOrNoMachine(t *testing.T) {
	e, p, fake, _ := sayTestEnv(t)
	fake.SetSayAgents()
	if err := SayCmd(context.Background(), e, p.Name, "", "hello"); err == nil || !strings.Contains(err.Error(), "No agent is running") {
		t.Fatalf("with no agent: %v", err)
	}
	var usage cobraUsageError
	if err := SayCmd(context.Background(), e, p.Name, "", "  "); !errors.As(err, &usage) {
		t.Fatalf("an empty message: %v, want a usage error (exit 2)", err)
	}
}
