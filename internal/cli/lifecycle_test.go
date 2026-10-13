package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func newLifecycleEnv(t *testing.T, fake *fakeapi.Fake) *Env {
	t.Helper()
	dir := t.TempDir()
	return &Env{
		Dir: dir, Cfg: defaultConfig(), Cache: newProjectsCache(), Cwd: t.TempDir(),
		Client: newClient(fake.URL()+"/v1", staticToken("tok")),
		Out:    &discardWriter{}, ErrOut: &discardWriter{},
	}
}

func TestStartStopLifecycle(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()

	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}

	if err := StartCmd(ctx, e, p.ID); err != nil {
		t.Fatalf("StartCmd: %v", err)
	}
	got, err := e.Client.GetProject(ctx, p.ID)
	if err != nil || got.State != "running" {
		t.Fatalf("state = %q err=%v, want running", got.State, err)
	}

	if err := StopCmd(ctx, e, p.ID, true); err != nil {
		t.Fatalf("StopCmd: %v", err)
	}
	got, err = e.Client.GetProject(ctx, p.ID)
	if err != nil || got.State != "stopped" {
		t.Fatalf("state = %q err=%v, want stopped", got.State, err)
	}
}

func TestDestroyRequiresConfirmation(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}

	err = DestroyCmd(ctx, e, p.ID, false, false, nil)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitUsage {
		t.Fatalf("err = %v, want a usage exitError", err)
	}

	if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
		t.Fatalf("DestroyCmd --yes: %v", err)
	}
	// A destroyed project drops out of GET /projects/:id entirely (the
	// fake's docs: destroyed projects are hidden from the normal lookup,
	// visible only where the api explicitly keeps them for retention).
	if _, err := e.Client.GetProject(ctx, p.ID); err == nil {
		t.Fatal("expected a destroyed project to 404 on GetProject")
	}
}

// `repose attach` brings the machine up in one step from every state a
// start can leave: stopped, in error, stopping, starting, restoring, and
// running on a server that is restarting (I-631, I-709). It waits on a
// machine on its way up and starts it after a restore. A machine stopped
// for abuse, or one being destroyed, still exits 5.
func TestAttachStartsAStoppedMachine(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	e.Cache.ByDir[e.Cwd] = p.ID
	state := func() *Project {
		t.Helper()
		got, err := e.Client.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// later changes the fake a moment after the attach began, as the
	// api would when an op it is waiting on ends.
	later := func(fn func()) {
		go func() {
			time.Sleep(700 * time.Millisecond)
			fn()
		}()
	}

	// The attach itself needs ssh, which this env has not; what this
	// test is about is the state it leaves.
	for _, tc := range []struct {
		name  string
		setup func()
	}{
		{"stopped", func() { fake.SetState(p.ID, "stopped") }},
		{"error", func() {
			fake.SetState(p.ID, "error")
			fake.SetLastError(p.ID, "boot_failed: the machine did not boot")
		}},
		{"stopping", func() {
			fake.SetState(p.ID, "stopping")
			later(func() { fake.SetState(p.ID, "stopped") })
		}},
		{"starting", func() {
			fake.SetState(p.ID, "starting")
			later(func() { fake.SetState(p.ID, "running") })
		}},
		{"restoring", func() {
			fake.SetState(p.ID, "restoring")
			later(func() { fake.SetState(p.ID, "stopped") })
		}},
	} {
		tc.setup()
		_ = runRun(ctx, e, RunOptions{}, true)
		if got := state(); got.State != "running" {
			t.Fatalf("attach from %s left it %s", tc.name, got.State)
		}
	}

	// Frozen across its server's restart (I-652): running, with the
	// host unreachable. The attach waits for the host, and starts
	// nothing.
	fake.SetHostUnreachable(p.ID, true)
	ops := len(fake.OpKinds(p.ID))
	waited := time.Now()
	later(func() { fake.SetHostUnreachable(p.ID, false) })
	errOut := &discardWriter{}
	e.ErrOut = errOut
	_ = runRun(ctx, e, RunOptions{}, true)
	if time.Since(waited) < 500*time.Millisecond {
		t.Fatalf("attach did not wait for the server (%s)", time.Since(waited))
	}
	if n := len(fake.OpKinds(p.ID)); n != ops {
		t.Fatalf("attach to a frozen machine sent %d ops", n-ops)
	}
	if !strings.Contains(errOut.buf.String(), "Waiting for todo-app's server to come back") {
		t.Fatalf("stderr %q", errOut.buf.String())
	}
	// Stopped on that server: the start would be refused while it is
	// away, so the attach waits for it and then starts the machine.
	fake.SetState(p.ID, "stopped")
	fake.SetHostUnreachable(p.ID, true)
	ops = len(fake.OpKinds(p.ID))
	later(func() {
		if n := len(fake.OpKinds(p.ID)); n != ops {
			t.Errorf("a start went out while the server was away (%d ops)", n-ops)
		}
		fake.SetHostUnreachable(p.ID, false)
	})
	_ = runRun(ctx, e, RunOptions{}, true)
	if got := state(); got.State != "running" {
		t.Fatalf("attach to a stopped machine on a server that came back left it %s", got.State)
	}

	for _, tc := range []struct {
		name, state, lastError, msg string
	}{
		{"abuse", "stopped", "abuse_stopped: stopped: a cryptocurrency miner (xmrig) was running", "todo-app is stopped: a cryptocurrency miner"},
		{"destroying", "destroying", "", "todo-app is being destroyed"},
	} {
		fake.SetState(p.ID, tc.state)
		fake.SetLastError(p.ID, tc.lastError)
		err = runRun(ctx, e, RunOptions{}, true)
		if ee, ok := err.(*exitError); !ok || ee.code != ExitGuestNotRunning || !strings.Contains(ee.msg, tc.msg) {
			t.Fatalf("attach while %s: %v, want exit 5 with %q", tc.name, err, tc.msg)
		}
	}
}

// A server that stays away is waited for up to hostWaitTimeout, then
// the attach exits 5 and says so.
func TestAttachGivesUpOnAServerThatStaysAway(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	e.Cache.ByDir[e.Cwd] = p.ID
	fake.SetHostUnreachable(p.ID, true)
	defer func(d time.Duration) { hostWaitTimeout = d }(hostWaitTimeout)
	hostWaitTimeout = time.Second
	err = runRun(ctx, e, RunOptions{}, true)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitGuestNotRunning || !strings.Contains(ee.msg, "todo-app's server has not answered for 1s") {
		t.Fatalf("err = %v", err)
	}
}

// ssh and code start a stopped machine as attach does; exec does not
// (I-641).
func TestSSHStartsAStoppedMachineAndExecDoesNot(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.StopProject(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	e.Cache.ByDir[e.Cwd] = p.ID

	err = ExecCmd(ctx, e, ExecOptions{Command: []string{"true"}}, nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitGuestNotRunning {
		t.Fatalf("exec on a stopped machine: %v, want exit 5", err)
	}
	if got, _ := e.Client.GetProject(ctx, p.ID); got.State != "stopped" {
		t.Fatalf("exec started it: %s", got.State)
	}
	// The connection itself needs ssh, which this env has not; the start
	// is what this test is about.
	_ = SSHCmd(ctx, e, "")
	if got, err := e.Client.GetProject(ctx, p.ID); err != nil || got.State != "running" {
		t.Fatalf("after ssh: %+v %v", got, err)
	}
	// A restore is waited for and the machine started after it (I-709);
	// a destroy is not.
	fake.SetState(p.ID, "restoring")
	go func() {
		time.Sleep(700 * time.Millisecond)
		fake.SetState(p.ID, "stopped")
	}()
	_ = SSHCmd(ctx, e, "")
	if got, err := e.Client.GetProject(ctx, p.ID); err != nil || got.State != "running" {
		t.Fatalf("after ssh while restoring: %+v %v", got, err)
	}
	fake.SetState(p.ID, "destroying")
	err = SSHCmd(ctx, e, "")
	if ee, ok := err.(*exitError); !ok || ee.code != ExitGuestNotRunning {
		t.Fatalf("ssh while destroying: %v, want exit 5", err)
	}
}

// TestRetryOnOpConflict is DECISIONS I-70: a stop/start/resize/destroy
// racing a queued update_secrets op sees 409 conflict "an operation is in
// progress" and must retry, not surface it, for up to 10s.
func TestRetryOnOpConflict(t *testing.T) {
	t.Run("retries the exact conflict and succeeds", func(t *testing.T) {
		calls := 0
		err := retryOnOpConflict(context.Background(), func() error {
			calls++
			if calls < 3 {
				return &APIError{Code: "conflict", Message: "an operation is in progress"}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if calls != 3 {
			t.Fatalf("calls = %d, want 3", calls)
		}
	})

	t.Run("does not retry a conflict that names its reason", func(t *testing.T) {
		calls := 0
		want := &APIError{Code: "conflict", Message: "todo has no usable volume", Detail: map[string]any{"reason": "restore_unfinished"}}
		err := retryOnOpConflict(context.Background(), func() error {
			calls++
			return want
		})
		if err != want || calls != 1 {
			t.Fatalf("err = %v calls = %d", err, calls)
		}
	})

	t.Run("does not retry a non-conflict error", func(t *testing.T) {
		calls := 0
		want := &APIError{Code: "not_found", Message: "project not found"}
		err := retryOnOpConflict(context.Background(), func() error {
			calls++
			return want
		})
		if err != want || calls != 1 {
			t.Fatalf("err = %v calls = %d", err, calls)
		}
	})

	t.Run("gives up after the retry window and surfaces the conflict", func(t *testing.T) {
		calls := 0
		conflict := &APIError{Code: "conflict", Message: "an operation is in progress"}
		start := time.Now()
		err := retryOnOpConflict(context.Background(), func() error {
			calls++
			return conflict
		})
		elapsed := time.Since(start)
		if err != conflict {
			t.Fatalf("err = %v, want the conflict surfaced", err)
		}
		if elapsed < opConflictRetryWindow {
			t.Fatalf("gave up after %v, want at least %v", elapsed, opConflictRetryWindow)
		}
		if calls < 2 {
			t.Fatalf("calls = %d, want more than one attempt", calls)
		}
	})
}

func TestStopRetriesOnOpConflictThenSucceeds(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}

	fake.FailNext("POST", "/projects/"+p.ID+"/stop", "conflict")
	if err := StopCmd(ctx, e, p.ID, true); err != nil {
		t.Fatalf("StopCmd: %v", err)
	}
	got, err := e.Client.GetProject(ctx, p.ID)
	if err != nil || got.State != "stopped" {
		t.Fatalf("state = %q err=%v, want stopped after the retry", got.State, err)
	}
}

// A stop with --yes names the agents it ended mid-turn or waiting for an
// answer, and no command (features/stop-start-destroy.md, DECISIONS
// I-500, I-614); an idle agent and a stop with none busy add no line.
func TestStopNamesInterruptedAgents(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	stop := func(agents []fakeapi.AgentSignal) string {
		t.Helper()
		e := newLifecycleEnv(t, fake)
		p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: fmt.Sprintf("app-%d", len(agents)), Class: "small"})
		if err != nil {
			t.Fatal(err)
		}
		if err := StartCmd(ctx, e, p.ID); err != nil {
			t.Fatal(err)
		}
		fake.SetAgents(p.ID, agents)
		var out bytes.Buffer
		e.Out = &out
		if err := StopCmd(ctx, e, p.ID, false); err != nil {
			t.Fatalf("StopCmd: %v", err)
		}
		return out.String()
	}

	out := stop([]fakeapi.AgentSignal{
		{Agent: "claude", Window: "claude", State: "working"},
		{Agent: "codex", Window: "codex", State: "idle"},
		{Agent: "claude", Window: "claude-2", State: "needs_input"},
	})
	if !strings.HasPrefix(out, "Stopped app-3 in ") || !strings.HasSuffix(out, "s.\nEnded claude (working) and claude-2 (needs input).\n") {
		t.Errorf("stop with two busy agents printed %q", out)
	}
	if strings.Contains(out, "codex") || strings.Contains(out, "resume") {
		t.Errorf("stop named an idle agent or a command: %q", out)
	}

	if out := stop([]fakeapi.AgentSignal{{Agent: "codex", Window: "codex", State: "idle"}}); strings.Contains(out, "Ended") {
		t.Errorf("stop with only an idle agent printed %q", out)
	}
}

// The stop line says how long the stop took and, when it took one, the
// snapshot's size, and nothing about billing: a stopped project costs
// nothing (DECISIONS I-570). The owner's 2026-10-07 stop printed "Disk is
// still billed." and the snapshot's 36-character id.
func TestStopLineSaysWhatTheStopDid(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "waterville", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	stop := func(snapshot bool) string {
		t.Helper()
		if err := StartCmd(ctx, e, p.ID); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		e.Out = &out
		if err := StopCmd(ctx, e, p.ID, snapshot); err != nil {
			t.Fatalf("StopCmd: %v", err)
		}
		return out.String()
	}
	snaps := func() []Snapshot {
		t.Helper()
		s, err := e.Client.ListSnapshots(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	out := stop(true)
	latest := newestSnapshot(snaps())
	if latest == nil {
		t.Fatal("the stop took no snapshot")
	}
	want := " with a " + humanBytes(latest.Bytes) + " snapshot.\n"
	if !strings.HasPrefix(out, "Stopped waterville in ") || !strings.HasSuffix(out, want) || strings.Count(out, "\n") != 1 {
		t.Errorf("stop printed %q, want one line ending %q", out, want)
	}
	if strings.Contains(out, latest.ID) || strings.Contains(out, "billed") {
		t.Errorf("stop printed the snapshot id or a billing claim: %q", out)
	}

	out = stop(false)
	if !strings.HasPrefix(out, "Stopped waterville in ") || !strings.HasSuffix(out, "s.\n") || strings.Contains(out, "snapshot") || strings.Count(out, "\n") != 1 {
		t.Errorf("stop --no-snapshot printed %q", out)
	}

	var out2 bytes.Buffer
	e.Out = &out2
	if err := StopCmd(ctx, e, p.ID, true); err != nil {
		t.Fatalf("StopCmd on a stopped project: %v", err)
	}
	if got := out2.String(); got != "waterville is already stopped.\n" {
		t.Errorf("already stopped printed %q", got)
	}
}
