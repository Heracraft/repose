package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

// A command that needs the project settled, run straight after a stop
// that returned early, waits for the stop and then does its work: the
// api refuses another op while the stop runs (409) and a restore over a
// machine that is not stopped. Before I-710 the stop had already ended
// by then; `repose stop X; repose rm X` must still work.
func TestCommandsAfterABackgroundStopWaitForIt(t *testing.T) {
	ctx := context.Background()
	// A real stop outlasts the retry on a 409 (8 to 23 s on prod); the
	// stop here is 2 s, so the retry gets less.
	window := opConflictRetryWindow
	opConflictRetryWindow = 300 * time.Millisecond
	defer func() { opConflictRetryWindow = window }()
	cases := []struct {
		name string
		run  func(e *Env, p *Project) error
		want string // the state the project ends in
	}{
		{"rm", func(e *Env, p *Project) error {
			return DestroyProjectsCmd(ctx, e, []string{p.ID}, true, true, nil)
		}, "gone"},
		{"rm without --wait", func(e *Env, p *Project) error {
			return DestroyProjectsCmd(ctx, e, []string{p.ID}, true, false, nil)
		}, "gone"},
		{"fork", func(e *Env, p *Project) error {
			return ForkCmd(ctx, e, ForkOptions{ProjectArg: p.ID, Count: 1, NoStart: true})
		}, "stopped"},
		{"resize --size", func(e *Env, p *Project) error {
			return ResizeClassCmd(ctx, e, p.ID, "large", nil)
		}, "stopped"},
		{"resize disk", func(e *Env, p *Project) error {
			return ResizeCmd(ctx, e, p.ID, p.VolumeBytes*2)
		}, "stopped"},
		{"snapshots create", func(e *Env, p *Project) error {
			return SnapshotsCreateCmd(ctx, e, p.ID)
		}, "stopped"},
		{"snapshots restore", func(e *Env, p *Project) error {
			snaps, err := e.Client.ListSnapshots(ctx, p.ID)
			if err != nil || len(snaps) == 0 {
				t.Fatalf("snapshots: %v %v", snaps, err)
			}
			return SnapshotsRestoreCmd(ctx, e, p.ID, snaps[0].ID, "", nil)
		}, "stopped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := fakeapi.New(fakeapi.Options{StopDelay: 2 * time.Second})
			defer fake.Close()
			e := newLifecycleEnv(t, fake)
			p := runningProject(t, fake, e, "job")
			if c.name == "snapshots restore" {
				if err := SnapshotsCreateCmd(ctx, e, p.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
				t.Fatal(err)
			}
			if s := stateOf(t, e, p.ID); s != "stopping" {
				t.Fatalf("state after the stop: %s", s)
			}
			fresh, err := e.Client.GetProject(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.run(e, fresh); err != nil {
				t.Fatalf("%s right after a background stop: %v", c.name, err)
			}
			got, err := e.Client.GetProject(ctx, p.ID)
			switch {
			case c.want == "gone":
				if err == nil && got.State != "destroyed" && got.State != "destroying" {
					t.Fatalf("after rm: %s", got.State)
				}
			case err != nil:
				t.Fatal(err)
			case got.State != c.want:
				t.Fatalf("after %s: %s", c.name, got.State)
			}
		})
	}
}

// A stop that fails inside the pickup wait says so and exits 1 with the
// command that tries again, as --wait does.
func TestBackgroundStopThatFails(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "job")
	fake.SetStopFailure(p.ID, "the host did not answer")
	err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true})
	if err == nil || exitCode(err) != ExitGeneric || !strings.Contains(err.Error(), "the host did not answer") || !strings.Contains(err.Error(), "`repose stop job` tries again") {
		t.Fatalf("err %v (exit %d)", err, exitCode(err))
	}
}

// A stop that ended inside the pickup wait with its snapshot failed says
// so (I-158), as the --wait line does.
func TestBackgroundStopNotesAFailedSnapshot(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "job")
	fake.SetStopSnapshotFailure(p.ID, "the snapshot upload failed")
	var out, errOut bytes.Buffer
	e.Out, e.ErrOut = &out, &errOut
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Stopped job in ") || !strings.Contains(errOut.String(), "Note: ") || !strings.Contains(errOut.String(), "snapshot upload failed") {
		t.Fatalf("out %q, err %q", out.String(), errOut.String())
	}
}

// --json prints the project as the stop left it: stopping (cli.md).
func TestBackgroundStopJSON(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StopDelay: 2 * time.Second})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	runningProject(t, fake, e, "job")
	var out bytes.Buffer
	e.Out, e.JSON = &out, true
	if err := stopWithJSON(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	var got Project
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Slug != "job" || got.State != "stopping" {
		t.Fatalf("json %q (%v)", out.String(), err)
	}
}
