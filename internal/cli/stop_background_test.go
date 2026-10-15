package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

	// --no-snapshot cannot change the stop under way, and says so.
	out.Reset()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "job is already stopping; --no-snapshot does not change the stop under way.\n" {
		t.Fatalf("--no-snapshot on a stopping project: %q", out.String())
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
	if l := stoppingLine("job", guestStatus{git: rows}); l != "Stopping job; 1 file not committed." {
		t.Fatalf("line %q", l)
	}
	if l := stoppingLine("job", guestStatus{}); l != "Stopping job." {
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
		{"config add", func(e *Env, p *Project) error {
			return ConfigAddCmd(ctx, e, p.ID, []string{"jq"})
		}, "stopped"},
		{"config apply", func(e *Env, p *Project) error {
			path := filepath.Join(t.TempDir(), "repose.nix")
			if err := os.WriteFile(path, []byte("{ pkgs, ... }: { home.packages = [ pkgs.jq ]; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return ConfigApplyCmd(ctx, e, p.ID, path)
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

// A fork right after a background stop waits the stop out and then
// forks, whichever way the copies are made: cloned on the project's
// server (I-701), restored from a fork snapshot when the server cannot
// hold them, or from a snapshot an api before clones needs (I-703). The
// api refuses both a clone and a snapshot while the stop is open.
func TestForkWaitsOutABackgroundStop(t *testing.T) {
	ctx := context.Background()
	window := opConflictRetryWindow
	opConflictRetryWindow = 300 * time.Millisecond
	defer func() { opConflictRetryWindow = window }()
	for _, c := range []struct {
		name         string
		noClone, old bool
	}{
		{name: "clone"},
		{name: "snapshot fallback", noClone: true},
		{name: "api before clones", old: true},
	} {
		for _, noStart := range []bool{true, false} {
			name := c.name
			if noStart {
				name += " --no-start"
			}
			t.Run(name, func(t *testing.T) {
				fake := fakeapi.New(fakeapi.Options{StopDelay: 1500 * time.Millisecond, StopPickup: 200 * time.Millisecond})
				defer fake.Close()
				fake.SetForkClone(c.noClone, c.old)
				e := newLifecycleEnv(t, fake)
				p := runningProject(t, fake, e, "job")
				if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
					t.Fatal(err)
				}
				if s := stateOf(t, e, p.ID); s != "stopping" {
					t.Fatalf("state after the stop: %s", s)
				}
				errOut := &syncWriter{}
				e.ErrOut = errOut
				if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "job", Count: 1, NoStart: noStart}); err != nil {
					t.Fatalf("fork right after a background stop: %v; stderr %q", err, errOut.String())
				}
				if s := stateOf(t, e, p.ID); s != "stopped" {
					t.Fatalf("source after the fork: %s", s)
				}
				projects, err := e.Client.ListProjects(ctx)
				if err != nil {
					t.Fatal(err)
				}
				want := "running"
				if noStart {
					want = "stopped"
				}
				forks := 0
				for _, q := range projects {
					if q.ID == p.ID {
						continue
					}
					forks++
					if q.State != want {
						t.Fatalf("fork %s is %s, want %s", q.Slug, q.State, want)
					}
				}
				if forks != 1 {
					t.Fatalf("%d forks; stderr %q", forks, errOut.String())
				}
			})
		}
	}
}

// `repose attach` on a machine whose background stop is under way waits
// for the stop and then starts it (I-709 on I-710): one step, no
// "wait for it to settle".
func TestAttachAfterABackgroundStopStartsIt(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{StopDelay: 1500 * time.Millisecond, StopPickup: 200 * time.Millisecond})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "job")
	e.Cache.ByDir[e.Cwd] = p.ID
	defer func(l, s time.Duration) { sshWaitTimeout, sshWaitShort = l, s }(sshWaitTimeout, sshWaitShort)
	sshWaitTimeout, sshWaitShort = 300*time.Millisecond, 300*time.Millisecond
	startsBefore := 0
	for _, k := range fake.OpKinds(p.ID) {
		if k == "start" {
			startsBefore++
		}
	}
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if s := stateOf(t, e, p.ID); s != "stopping" {
		t.Fatalf("state after the stop: %s", s)
	}
	errOut := &syncWriter{}
	e.ErrOut = errOut
	_ = runRun(ctx, e, RunOptions{}, true) // the attach itself needs ssh
	if s := stateOf(t, e, p.ID); s != "running" {
		t.Fatalf("attach during a background stop left it %s; stderr %q", s, errOut.String())
	}
	if strings.Contains(errOut.String(), "settle") {
		t.Fatalf("attach passed the api's refusal on: %q", errOut.String())
	}
	count := map[string]int{}
	for _, k := range fake.OpKinds(p.ID) {
		count[k]++
	}
	if count["stop"] != 1 || count["start"] != startsBefore+1 {
		t.Fatalf("ops %v after %d starts, want one stop and one start after it", count, startsBefore)
	}
}

// A background stop that fails while attach or start waits for it is
// the stop's failure, not theirs: the command says so in one line and
// starts the machine from error, as it does any machine in error (the
// api restarts it, I-157). It used to read "Could not start job".
func TestStartAfterABackgroundStopThatFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		// noOpID stands in for an api before I-722, whose project read
		// does not name the stop op: the wait follows the state instead.
		noOpID bool
	}{{"op named", false}, {"api before I-722", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fake := fakeapi.New(fakeapi.Options{StopDelay: 1500 * time.Millisecond, StopPickup: 200 * time.Millisecond, NoProjectOpID: tc.noOpID})
			defer fake.Close()
			e := newLifecycleEnv(t, fake)
			p := runningProject(t, fake, e, "job")
			fake.SetStopFailure(p.ID, "the host did not answer")
			if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
				t.Fatal(err)
			}
			read, err := e.Client.GetProject(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if read.State != "stopping" {
				t.Fatalf("state after the stop: %s", read.State)
			}
			if named := read.OpID != ""; named == tc.noOpID {
				t.Fatalf("project read op_id %q", read.OpID)
			}
			var out bytes.Buffer
			errOut := &syncWriter{}
			e.Out, e.ErrOut = &out, errOut
			if err := StartCmd(ctx, e, p.ID); err != nil {
				t.Fatalf("start after a stop that failed: %v; stderr %q", err, errOut.String())
			}
			if s := stateOf(t, e, p.ID); s != "running" {
				t.Fatalf("state %s; stderr %q", s, errOut.String())
			}
			if !strings.Contains(errOut.String(), "Stopping job failed: ") || !strings.Contains(errOut.String(), "the host did not answer") {
				t.Fatalf("stderr %q", errOut.String())
			}
			if !strings.HasPrefix(out.String(), "job is running (") {
				t.Fatalf("out %q", out.String())
			}
		})
	}
}

// An in-place restore right after a background stop asks its question
// from the snapshots listed once the stop has ended, so it names the
// stop's snapshot as keeping the disk. Listed before, while the stop
// still ran, the question said the disk would be lost for good (review
// of the I-730 merge, run on prod 2026-10-14). The fake keeps
// started_at through the stop, as the api does.
func TestRestoreAfterABackgroundStopNamesTheStopSnapshot(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{StopDelay: 1500 * time.Millisecond, StopPickup: 200 * time.Millisecond})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "job")
	if err := SnapshotsCreateCmd(ctx, e, p.ID); err != nil {
		t.Fatal(err)
	}
	snaps, err := e.Client.ListSnapshots(ctx, p.ID)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots: %v %v", snaps, err)
	}
	manual := snaps[0].ID
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if s := stateOf(t, e, p.ID); s != "stopping" {
		t.Fatalf("state after the stop: %s", s)
	}
	var asked string
	err = SnapshotsRestoreCmd(ctx, e, p.ID, manual, "", func(q string) (bool, error) { asked = q; return false, nil })
	if exitCode(err) != ExitGeneric || err.Error() != "Not restored." {
		t.Fatalf("declined: %v", err)
	}
	if !strings.Contains(asked, "The stop snapshot of ") || !strings.Contains(asked, " keeps the disk as it is now.") {
		t.Fatalf("asked %q", asked)
	}
}
