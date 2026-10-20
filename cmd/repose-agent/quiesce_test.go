package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestQuiesceKillsTheSession: the session is the project's slug, matched
// exactly, as nix/guest/base/tmux.nix names it.
func TestQuiesceKillsTheSession(t *testing.T) {
	f := newFixture(t)
	mkdir(t, liveDir(f.env.Store, "todo-app"))
	f.sessions["=todo-app"] = true

	if code := f.run("quiesce", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	want := []string{"has-session -t =todo-app", "kill-session -t =todo-app"}
	if !slices.Equal(f.tmux, want) {
		t.Errorf("tmux calls %v, want %v", f.tmux, want)
	}
	if f.sessions["=todo-app"] {
		t.Error("the session is still there")
	}
	if got, want := f.stdout(), `{"stopped":["tmux:todo-app"],"busy":[]}`; got != want {
		t.Errorf("stdout %s, want %s", got, want)
	}
}

// TestQuiesceWithNothingRunning: nothing to stop is not a failure, and tmux
// is asked once.
func TestQuiesceWithNothingRunning(t *testing.T) {
	f := newFixture(t)
	mkdir(t, liveDir(f.env.Store, "todo-app"))

	if code := f.run("quiesce", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	if want := `{"stopped":[],"busy":[]}`; f.stdout() != want {
		t.Errorf("stdout %s, want %s", f.stdout(), want)
	}
	if len(f.tmux) != 1 {
		t.Errorf("tmux calls %v, want has-session alone", f.tmux)
	}
	if len(f.signals) != 0 {
		t.Errorf("signals %v, want none", f.signals)
	}
}

// TestQuiesceSigkillsWhatSurvivesSigterm: SIGTERM first, SIGKILL after the
// grace period, to that pid and in that order.
func TestQuiesceSigkillsWhatSurvivesSigterm(t *testing.T) {
	f := newFixture(t)
	mkdir(t, liveDir(f.env.Store, "todo-app"))
	f.holds("todo-app", 4242, "node")
	f.diesOn[killOf(4242)] = true

	if code := f.run("quiesce", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	want := []string{termOf(4242), killOf(4242)}
	if !slices.Equal(f.signals, want) {
		t.Errorf("signals %v, want %v", f.signals, want)
	}
	if got, want := f.stdout(), `{"stopped":["node"],"busy":[]}`; got != want {
		t.Errorf("stdout %s, want %s", got, want)
	}
}

// TestQuiesceReportsWhatItCouldNotStop: a process that answers neither
// signal is reported, not hidden. A rewind over it loses its last writes
// (docs/research/wrapper-undo.md).
func TestQuiesceReportsWhatItCouldNotStop(t *testing.T) {
	f := newFixture(t)
	mkdir(t, liveDir(f.env.Store, "todo-app"))
	f.holds("todo-app", 4242, "node")
	f.holds("todo-app", 4243, "claude")
	f.diesOn[termOf(4242)] = true

	if code := f.run("quiesce", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	var got wireQuiesce
	if err := json.Unmarshal([]byte(f.stdout()), &got); err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if !slices.Equal(got.Stopped, []string{"node"}) {
		t.Errorf("stopped %v, want [node]", got.Stopped)
	}
	if !slices.Equal(got.Busy, []string{"claude"}) {
		t.Errorf("busy %v, want [claude]", got.Busy)
	}
	// Both were asked to stop before either was killed.
	want := []string{termOf(4242), termOf(4243), killOf(4243)}
	if !slices.Equal(f.signals, want) {
		t.Errorf("signals %v, want %v", f.signals, want)
	}
}

// TestQuiesceOnlyTouchesItsOwnProject: another project's processes are not
// this project's holders, and nothing signals them.
func TestQuiesceOnlyTouchesItsOwnProject(t *testing.T) {
	f := newFixture(t)
	mkdir(t, liveDir(f.env.Store, "todo-app"))
	f.holds("todo-app-2", 4242, "node")

	if code := f.run("quiesce", "--project", "todo-app"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	if want := `{"stopped":[],"busy":[]}`; f.stdout() != want {
		t.Errorf("stdout %s, want %s", f.stdout(), want)
	}
	if len(f.signals) != 0 {
		t.Errorf("signals %v, want none", f.signals)
	}
}

// TestQuiesceOnAnUnknownProject: not_found, and nothing is signalled.
func TestQuiesceOnAnUnknownProject(t *testing.T) {
	f := newFixture(t)
	if code := f.run("quiesce", "--project", "todo-app"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(f.stdout(), codeNotFound) {
		t.Errorf("stdout %s", f.stdout())
	}
	if len(f.signals) != 0 {
		t.Errorf("signals %v, want none", f.signals)
	}
}

// TestQuiesceWhenTheProcessesCannotBeRead: the store's own failure is the
// agent's, and nothing is signalled on a guess.
func TestQuiesceWhenTheProcessesCannotBeRead(t *testing.T) {
	f := newFixture(t)
	mkdir(t, liveDir(f.env.Store, "todo-app"))
	f.backend.errs["Holders"] = errors.New("read /proc: permission denied")

	if code := f.run("quiesce", "--project", "todo-app"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(f.stdout(), codeInternal) {
		t.Errorf("stdout %s", f.stdout())
	}
	if len(f.signals) != 0 {
		t.Errorf("signals %v, want none", f.signals)
	}
}
