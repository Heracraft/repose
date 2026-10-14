package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
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
	// This env has no ssh: each connection burns its whole wait.
	defer func(l, s time.Duration) { sshWaitTimeout, sshWaitShort = l, s }(sshWaitTimeout, sshWaitShort)
	sshWaitTimeout, sshWaitShort = 300*time.Millisecond, 300*time.Millisecond
	state := func() *Project {
		t.Helper()
		got, err := e.Client.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// The attach itself needs ssh, which this env has not; what this
	// test is about is the state it leaves. A machine on its way
	// somewhere gets there once the attach shows it is waiting (then),
	// as the api would move it when the op it waits on ends.
	for _, tc := range []struct {
		name, state, lastError string
		waiting, then          string
	}{
		{name: "stopped", state: "stopped"},
		{name: "error", state: "error", lastError: "boot_failed: the machine did not boot"},
		{name: "stopping", state: "stopping", waiting: "Stopping todo-app", then: "stopped"},
		{name: "starting", state: "starting", waiting: "Booting todo-app", then: "running"},
		{name: "restoring", state: "restoring", waiting: "Restoring todo-app", then: "stopped"},
	} {
		fake.SetState(p.ID, tc.state)
		fake.SetLastError(p.ID, tc.lastError)
		errOut := &syncWriter{}
		e.ErrOut = errOut
		moved := false
		if tc.then != "" {
			fake.OnGet(p.ID, func(fp *fakeapi.Project) bool {
				if !errOut.Contains(tc.waiting) {
					return false
				}
				fp.State, fp.GuestIP, moved = tc.then, "10.64.4.99", true
				return true
			})
		}
		_ = runRun(ctx, e, RunOptions{}, true)
		fake.OnGet(p.ID, nil)
		if got := state(); got.State != "running" {
			t.Fatalf("attach from %s left it %s; stderr %q", tc.name, got.State, errOut.String())
		}
		if tc.then != "" && !moved {
			t.Fatalf("attach from %s never showed %q; stderr %q", tc.name, tc.waiting, errOut.String())
		}
	}

	// Frozen across its server's restart (I-652): running, with the
	// host unreachable. The attach waits for the host, and starts
	// nothing.
	const waiting = "Waiting for todo-app's server to come back"
	backOnceWaiting := func(errOut *syncWriter, back *bool) {
		fake.OnGet(p.ID, func(fp *fakeapi.Project) bool {
			if !errOut.Contains(waiting) {
				return false
			}
			fp.HostUnreachable, *back = false, true
			return true
		})
	}
	fake.SetLastError(p.ID, "")
	fake.SetHostUnreachable(p.ID, true)
	ops := len(fake.OpKinds(p.ID))
	errOut := &syncWriter{}
	e.ErrOut = errOut
	back := false
	backOnceWaiting(errOut, &back)
	_ = runRun(ctx, e, RunOptions{}, true)
	if !back {
		t.Fatalf("attach did not wait for the server; stderr %q", errOut.String())
	}
	if n := len(fake.OpKinds(p.ID)); n != ops {
		t.Fatalf("attach to a frozen machine sent %d ops", n-ops)
	}
	// Stopped on that server: the start is refused while it is away (the
	// fake refuses it as the api does), so the attach waits for it and
	// then starts the machine.
	fake.SetState(p.ID, "stopped")
	fake.SetHostUnreachable(p.ID, true)
	errOut = &syncWriter{}
	e.ErrOut = errOut
	back = false
	backOnceWaiting(errOut, &back)
	_ = runRun(ctx, e, RunOptions{}, true)
	if got := state(); got.State != "running" || !back {
		t.Fatalf("attach to a stopped machine on a server that came back left it %s (waited %v); stderr %q", got.State, back, errOut.String())
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

// syncWriter is a stderr a test reads while the command still writes it.
type syncWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *syncWriter) Contains(s string) bool { return strings.Contains(w.String(), s) }

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
	defer func(l, s time.Duration) { sshWaitTimeout, sshWaitShort = l, s }(sshWaitTimeout, sshWaitShort)
	sshWaitTimeout, sshWaitShort = 300*time.Millisecond, 300*time.Millisecond
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
	// This env has no ssh: each connection burns its whole wait.
	defer func(l, s time.Duration) { sshWaitTimeout, sshWaitShort = l, s }(sshWaitTimeout, sshWaitShort)
	sshWaitTimeout, sshWaitShort = 300*time.Millisecond, 300*time.Millisecond

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
	errOut := &syncWriter{}
	e.ErrOut = errOut
	restored := false
	fake.OnGet(p.ID, func(fp *fakeapi.Project) bool {
		if !errOut.Contains("Restoring todo-app") {
			return false
		}
		fp.State, fp.GuestIP, restored = "stopped", "", true
		return true
	})
	_ = SSHCmd(ctx, e, "")
	if got, err := e.Client.GetProject(ctx, p.ID); err != nil || got.State != "running" || !restored {
		t.Fatalf("after ssh while restoring (waited %v): %+v %v; stderr %q", restored, got, err, errOut.String())
	}
	// Frozen across its server's restart (I-652): ssh waits for the
	// server, as attach does, and sends no op.
	fake.SetHostUnreachable(p.ID, true)
	ops := len(fake.OpKinds(p.ID))
	errOut = &syncWriter{}
	e.ErrOut = errOut
	back := false
	fake.OnGet(p.ID, func(fp *fakeapi.Project) bool {
		if !errOut.Contains("Waiting for todo-app's server to come back") {
			return false
		}
		fp.HostUnreachable, back = false, true
		return true
	})
	_ = SSHCmd(ctx, e, "")
	if !back {
		t.Fatalf("ssh did not wait for the server; stderr %q", errOut.String())
	}
	if n := len(fake.OpKinds(p.ID)); n != ops {
		t.Fatalf("ssh to a frozen machine sent %d ops", n-ops)
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

// `repose start` on a machine frozen across its server's restart waits
// for the server, as attach does, sends no op, and says it is running
// (I-709).
func TestStartWaitsForARestartingServer(t *testing.T) {
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
	ops := len(fake.OpKinds(p.ID))
	var out bytes.Buffer
	e.Out = &out
	errOut := &syncWriter{}
	e.ErrOut = errOut
	back := false
	fake.OnGet(p.ID, func(fp *fakeapi.Project) bool {
		if !errOut.Contains("Waiting for todo-app's server to come back") {
			return false
		}
		fp.HostUnreachable, back = false, true
		return true
	})
	if err := StartCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if !back {
		t.Fatalf("start did not wait for the server; stderr %q", errOut.String())
	}
	if n := len(fake.OpKinds(p.ID)); n != ops {
		t.Fatalf("start on a frozen machine sent %d ops", n-ops)
	}
	if !strings.Contains(out.String(), "todo-app is running (large)") {
		t.Fatalf("out %q", out.String())
	}
}

// A machine that a wait leaves running (a restore that booted it, a
// start someone else sent) gets the early boot probe, as one this
// command started does (I-237, I-709).
func TestWaitThatEndsRunningCallsGuestUp(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"restoring", "starting"} {
		fake.SetState(p.ID, from)
		go func() {
			time.Sleep(700 * time.Millisecond)
			fake.SetState(p.ID, "running")
		}()
		called := 0
		e.guestUp = func(*Project) { called++ }
		project, err := e.Client.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := ensureRunningFrom(ctx, e, project, e.newProgress(), true); err != nil {
			t.Fatal(err)
		}
		if project.State != "running" || called != 1 {
			t.Fatalf("from %s: state %s, guestUp called %d times", from, project.State, called)
		}
	}
}
