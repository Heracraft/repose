package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose stop` returns once the api has the stop and the project reads
// stopping; the snapshot and the shutdown go on without the CLI
// (DECISIONS I-710). The owner's 2026-10-13 stops waited 20 and 23 s.
func TestStopReturnsOnceTheStopIsUnderWay(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StopDelay: 3 * time.Second, StopPickup: 300 * time.Millisecond})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "job")
	var out bytes.Buffer
	e.Out = &out
	begun := time.Now()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begun); took > 2*time.Second {
		t.Fatalf("stop took %v; it waited for the snapshot", took)
	}
	if out.String() != "Stopping job.\n" {
		t.Fatalf("out %q", out.String())
	}
	// It waited past the engine's pickup: whatever runs next sees the stop.
	if s := stateOf(t, e, p.ID); s != "stopping" {
		t.Fatalf("state right after the stop: %s", s)
	}

	// A second stop while it runs says so and sends nothing.
	out.Reset()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "job is already stopping.\n" {
		t.Fatalf("second stop: %q", out.String())
	}

	// --wait on a stop under way waits for it.
	out.Reset()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true, Wait: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Stopped job in ") || stateOf(t, e, p.ID) != "stopped" {
		t.Fatalf("--wait on a stopping project: %q, %s", out.String(), stateOf(t, e, p.ID))
	}
}

// --wait keeps the whole wait and the line that says what the stop did
// (I-570); a busy agent's line says Ended after it and Ending without it.
func TestStopWaitAndTheAgentsItEnds(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StopDelay: 500 * time.Millisecond})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "api")
	fake.SetAgents(p.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude", State: "working"}})
	var out bytes.Buffer
	e.Out = &out
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Stopping api.\nEnding claude (working).\n" {
		t.Fatalf("background: %q", out.String())
	}
	waitFor(t, func() bool { return stateOf(t, e, p.ID) == "stopped" })

	q := runningProject(t, fake, e, "web")
	fake.SetAgents(q.ID, []fakeapi.AgentSignal{{Agent: "codex", Window: "codex", State: "needs_input"}})
	out.Reset()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"web"}, Snapshot: true, Yes: true, Wait: true}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "Stopped web in ") || !strings.Contains(got, " snapshot.\nEnded codex (needs input).\n") {
		t.Fatalf("--wait: %q", got)
	}
}

// Several stop in parallel and each gets its line; --unused too (I-615).
func TestStopSeveralInTheBackground(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StopDelay: 2 * time.Second})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	runningProject(t, fake, e, "api")
	runningProject(t, fake, e, "web")
	var out bytes.Buffer
	e.Out = &out
	begun := time.Now()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api", "web"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(begun) > 1500*time.Millisecond || out.String() != "Stopping api.\nStopping web.\n" {
		t.Fatalf("out %q after %v", out.String(), time.Since(begun))
	}
}

// A start, attach or run during a stop waits for the stop and then
// starts: one command, not "wait and try again".
func TestStartWaitsForAStopUnderWay(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StopDelay: time.Second})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "job")
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e.Out = &out
	if err := StartCmd(ctx, e, p.ID); err != nil {
		t.Fatalf("start during the stop: %v", err)
	}
	if !strings.HasPrefix(out.String(), "job is running (") || stateOf(t, e, p.ID) != "running" {
		t.Fatalf("out %q, state %s", out.String(), stateOf(t, e, p.ID))
	}
}

// The line puts what the stop leaves after the slug, as the stopped line
// does (I-634): the user reads it before the machine is gone.
func TestStoppingLineSaysWhatItLeaves(t *testing.T) {
	n := func(i int) *int { return &i }
	rows := []gitRow{{Worktree: "job", Branch: "main", NotOnLaptop: n(0), Uncommitted: n(1)}}
	if l := stoppingLine("job", rows); l != "Stopping job; 1 file not committed." {
		t.Fatalf("line %q", l)
	}
	if l := stoppingLine("job", nil); l != "Stopping job." {
		t.Fatalf("clean %q", l)
	}
}
