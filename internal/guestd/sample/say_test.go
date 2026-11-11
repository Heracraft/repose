package sample

import (
	"context"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// sayFixture is one claude window the watcher has seen, with the tmux
// submit script recorded rather than run.
func sayFixture(t *testing.T) (*Watcher, *sysdep.FakeRunner, *recorder, *clock) {
	t.Helper()
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash"},
		{pid: 101, ppid: 100, comm: "claude", ticks: 50},
	}
	w, run, rec, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	w.Refresh(context.Background())
	return w, run, rec, clk
}

// submitted returns the submit scripts the runner was asked to run, in
// order. Each is one `sh -c <script>` as dev.
func submitted(run *sysdep.FakeRunner) []string {
	var out []string
	for _, c := range run.Calls() {
		if len(c.Argv) == 3 && c.Argv[0] == "sh" && c.Argv[1] == "-c" && strings.Contains(c.Argv[2], "repose_pp=") {
			out = append(out, c.Argv[2])
		}
	}
	return out
}

// I-1144: an agent at its prompt gets the message at once, through the
// submit script of I-752, into its own window, and the delivery is an
// agent_prompted event carrying the sender and never the text.
func TestSayReachesAnIdleAgentAtOnce(t *testing.T) {
	w, run, rec, _ := sayFixture(t)
	out, err := w.Say(context.Background(), SayRequest{ID: "s1", Text: "run the tests", Sender: "heracraft", FromOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != SayDelivered || out.Window != "claude" || out.Replaced {
		t.Fatalf("outcome = %+v, want delivered in claude", out)
	}
	scripts := submitted(run)
	if len(scripts) != 1 {
		t.Fatalf("%d submit scripts, want 1", len(scripts))
	}
	s := scripts[0]
	for _, want := range []string{
		"repose_pp='run the tests'",
		"tmux send-keys -t '=todo-app:=claude' -l \"$repose_pp\"",
		"tmux send-keys -t '=todo-app:=claude' Enter",
		"tmux capture-pane -p -t '=todo-app:=claude'",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("script has no %q:\n%s", want, s)
		}
	}
	_, events, _ := rec.snapshot()
	if len(events) != 1 || events[0] != "claude/claude=agent_prompted:heracraft" {
		t.Fatalf("events = %v, want one agent_prompted carrying the sender", events)
	}
	for _, e := range events {
		if strings.Contains(e, "run the tests") {
			t.Fatalf("the message is in an event: %q", e)
		}
	}
	if n := w.PendingSays(); n != 0 {
		t.Fatalf("%d messages still waiting", n)
	}
}

// I-1144: a working agent is not typed into. The message waits and goes
// in at the refresh that sees the turn end.
func TestSayWaitsForTheEndOfTheTurn(t *testing.T) {
	w, run, rec, clk := sayFixture(t)
	ctx := context.Background()
	w.RecordHook("claude", KindPrompt, clk.now())

	out, err := w.Say(ctx, SayRequest{ID: "s1", Text: "and squash the fixups", Sender: "heracraft", FromOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != SayWaiting || out.Window != "claude" {
		t.Fatalf("outcome = %+v, want waiting", out)
	}
	if n := len(submitted(run)); n != 0 {
		t.Fatalf("%d submit scripts while the agent works, want none", n)
	}
	// A refresh while it still works changes nothing.
	clk.advance(Interval)
	w.Refresh(ctx)
	if n := len(submitted(run)); n != 0 {
		t.Fatalf("%d submit scripts after a refresh mid-turn, want none", n)
	}
	if _, events, _ := rec.snapshot(); len(events) != 0 {
		t.Fatalf("events mid-turn = %v, want none", events)
	}

	// The turn ends; the next refresh delivers.
	w.RecordHook("claude", KindCompleted, clk.now())
	clk.advance(Interval)
	w.Refresh(ctx)
	scripts := submitted(run)
	if len(scripts) != 1 || !strings.Contains(scripts[0], "repose_pp='and squash the fixups'") {
		t.Fatalf("after the turn: %d scripts %v", len(scripts), scripts)
	}
	_, events, _ := rec.snapshot()
	if len(events) != 1 || events[0] != "claude/claude=agent_prompted:heracraft" {
		t.Fatalf("events = %v, want one agent_prompted", events)
	}
	if n := w.PendingSays(); n != 0 {
		t.Fatalf("%d messages still waiting", n)
	}
}

// I-1144: one message waits per window. A second replaces it, says so,
// and only the newer one is typed in.
func TestASecondMessageReplacesTheWaitingOne(t *testing.T) {
	w, run, _, clk := sayFixture(t)
	ctx := context.Background()
	w.RecordHook("claude", KindPrompt, clk.now())

	if _, err := w.Say(ctx, SayRequest{ID: "s1", Text: "first", Sender: "heracraft", FromOwner: true}); err != nil {
		t.Fatal(err)
	}
	out, err := w.Say(ctx, SayRequest{ID: "s2", Text: "second", Sender: "heracraft", FromOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Replaced || out.State != SayWaiting {
		t.Fatalf("outcome = %+v, want waiting and replaced", out)
	}
	if n := w.PendingSays(); n != 1 {
		t.Fatalf("%d messages waiting, want 1", n)
	}
	w.RecordHook("claude", KindCompleted, clk.now())
	clk.advance(Interval)
	w.Refresh(ctx)
	scripts := submitted(run)
	if len(scripts) != 1 {
		t.Fatalf("%d submit scripts, want 1", len(scripts))
	}
	if strings.Contains(scripts[0], "repose_pp='first'") || !strings.Contains(scripts[0], "repose_pp='second'") {
		t.Fatalf("the older message was typed in:\n%s", scripts[0])
	}
}

// I-1144: a say_id the guest has already answered is answered from the
// record, so a command hostd or the api resends types nothing twice.
func TestARepeatedSayIdIsNotTypedTwice(t *testing.T) {
	w, run, _, _ := sayFixture(t)
	ctx := context.Background()
	first, err := w.Say(ctx, SayRequest{ID: "s1", Text: "run the tests", Sender: "heracraft", FromOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	again, err := w.Say(ctx, SayRequest{ID: "s1", Text: "run the tests", Sender: "heracraft", FromOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatalf("repeat answered %+v, want the stored %+v", again, first)
	}
	if n := len(submitted(run)); n != 1 {
		t.Fatalf("%d submit scripts for one say_id, want 1", n)
	}
}

// I-1144: the window. One agent needs no name; a name that is not there
// and a machine with several both refuse by naming the windows.
func TestSayPicksTheWindowOrNamesThem(t *testing.T) {
	ctx := context.Background()

	t.Run("no agent", func(t *testing.T) {
		w, _, _, _, _ := newWatcherFixture(t, []fakeProc{{pid: 100, ppid: 1, comm: "bash"}})
		w.Refresh(ctx)
		_, err := w.Say(ctx, SayRequest{ID: "s1", Text: "hi", FromOwner: true})
		if err == nil || sysdep.CodeOf(err) != "not_found" {
			t.Fatalf("err = %v (%s), want not_found", err, sysdep.CodeOf(err))
		}
	})

	t.Run("a window that is not there", func(t *testing.T) {
		w, _, _, _ := sayFixture(t)
		_, err := w.Say(ctx, SayRequest{ID: "s1", Window: "codex", Text: "hi", FromOwner: true})
		if err == nil || !strings.Contains(err.Error(), "claude") {
			t.Fatalf("err = %v, want the refusal to name claude", err)
		}
	})

	t.Run("several agents", func(t *testing.T) {
		procs := []fakeProc{
			{pid: 100, ppid: 1, comm: "bash"},
			{pid: 101, ppid: 100, comm: "claude", ticks: 50},
			{pid: 201, ppid: 100, comm: "codex", ticks: 50},
		}
		w, run, _, _, _ := newWatcherFixture(t, procs)
		run.Match["list-windows"] = tmuxOutput(
			[4]string{"claude", "101", "claude", "0"},
			[4]string{"codex", "201", "codex", "0"},
		)
		w.Refresh(ctx)
		_, err := w.Say(ctx, SayRequest{ID: "s1", Text: "hi", FromOwner: true})
		if err == nil || sysdep.CodeOf(err) != "invalid_argument" {
			t.Fatalf("err = %v (%s), want invalid_argument", err, sysdep.CodeOf(err))
		}
		if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "codex") {
			t.Fatalf("refusal %q names neither window", err)
		}
		// Named, it goes to that one.
		out, err := w.Say(ctx, SayRequest{ID: "s2", Window: "codex", Text: "hi", FromOwner: true})
		if err != nil || out.Window != "codex" {
			t.Fatalf("out = %+v, err = %v", out, err)
		}
		scripts := submitted(run)
		if len(scripts) != 1 || !strings.Contains(scripts[0], "=todo-app:=codex") {
			t.Fatalf("scripts = %v", scripts)
		}
	})
}

// I-1144: the sender is named to the agent only when it is not the
// project's owner. Today it always is, so nothing is prefixed.
func TestOnlyAnotherSenderIsNamedToTheAgent(t *testing.T) {
	owner := SayText(SayRequest{Text: "ship it", Sender: "heracraft", FromOwner: true})
	if owner != "ship it" {
		t.Fatalf("the owner's own message = %q", owner)
	}
	other := SayText(SayRequest{Text: "ship it", Sender: "dana", FromOwner: false})
	if other != "[from dana] ship it" {
		t.Fatalf("another sender's message = %q", other)
	}
}

// I-1144: a message is tenant content. A refusal of an empty or
// oversized one names no text, and nothing of the message is typed.
func TestSayRefusesAnEmptyOrOversizedMessage(t *testing.T) {
	w, run, _, _ := sayFixture(t)
	ctx := context.Background()
	if _, err := w.Say(ctx, SayRequest{ID: "s1", Text: "   ", FromOwner: true}); err == nil {
		t.Fatal("an empty message was taken")
	}
	long := strings.Repeat("x", SayTextCap+1)
	_, err := w.Say(ctx, SayRequest{ID: "s2", Text: long, FromOwner: true})
	if err == nil || sysdep.CodeOf(err) != "invalid_argument" {
		t.Fatalf("err = %v (%s), want invalid_argument", err, sysdep.CodeOf(err))
	}
	if strings.Contains(err.Error(), long[:64]) {
		t.Fatalf("the refusal quotes the message: %v", err)
	}
	if n := len(submitted(run)); n != 0 {
		t.Fatalf("%d submit scripts for refused messages", n)
	}
}

// I-1144: a window that closed with a message still waiting drops it
// rather than keeping it for whatever opens with that name next.
func TestAWaitingMessageGoesWithItsWindow(t *testing.T) {
	w, run, _, clk := sayFixture(t)
	ctx := context.Background()
	w.RecordHook("claude", KindPrompt, clk.now())
	if _, err := w.Say(ctx, SayRequest{ID: "s1", Text: "first", FromOwner: true}); err != nil {
		t.Fatal(err)
	}
	run.Match["list-windows"] = tmuxOutput()
	clk.advance(Interval)
	w.Refresh(ctx)
	if n := w.PendingSays(); n != 0 {
		t.Fatalf("%d messages waiting after the window closed", n)
	}
	if n := len(submitted(run)); n != 0 {
		t.Fatalf("%d submit scripts after the window closed", n)
	}
}
