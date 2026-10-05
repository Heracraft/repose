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

func TestAttachToStoppedGuestExitsFive(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	// The fake starts a freshly created project running at once (its own
	// doc: "operations complete at once"); stop it to get to the state
	// this test wants.
	if _, err := e.Client.StopProject(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	e.Cache.ByDir[e.Cwd] = p.ID

	err = runRun(ctx, e, RunOptions{}, true)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitGuestNotRunning {
		t.Fatalf("err = %v, want a guest-not-running exitError", err)
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

// A stop names the agents it interrupted mid-turn or waiting for an
// answer, and no command (features/stop-start-destroy.md, DECISIONS
// I-500); an idle agent and a stop with none busy add no line.
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
	if !strings.HasSuffix(out, "Disk is still billed.\nInterrupted claude (working) and claude-2 (needs input).\n") {
		t.Errorf("stop with two busy agents printed %q", out)
	}
	if strings.Contains(out, "codex") || strings.Contains(out, "resume") {
		t.Errorf("stop named an idle agent or a command: %q", out)
	}

	if out := stop([]fakeapi.AgentSignal{{Agent: "codex", Window: "codex", State: "idle"}}); strings.Contains(out, "Interrupted") {
		t.Errorf("stop with only an idle agent printed %q", out)
	}
}
