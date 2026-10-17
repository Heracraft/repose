package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose ps --project NAME WINDOW` and `repose attach --project NAME
// WINDOW` were refused with "name two projects": projectFrom read the
// one positional as a project and ran before orWindow, whose whole job
// is that a word naming no project is a window. With --project the pair
// form never fires, so the refusal came before any resolution — it
// reproduced with names that exist nowhere.
func TestWindowArgs(t *testing.T) {
	for _, c := range []struct {
		args            []string
		window, project string
		wantArgs        string
		wantWindow      string
		wantErr         bool
	}{
		{args: []string{"todo-app", "claude-2"}, wantArgs: "todo-app", wantWindow: "claude-2"},
		{args: []string{"claude-2"}, project: "todo-app", wantWindow: "claude-2"},
		{args: []string{"claude-2"}, project: "todo-app", window: "claude-2", wantWindow: "claude-2"},
		{args: []string{"claude-2"}, project: "todo-app", window: "shell", wantErr: true},
		{args: []string{"todo-app", "claude-2"}, window: "shell", wantErr: true},
		{args: []string{"todo-app"}, wantArgs: "todo-app"},
		{args: []string{"claude-2"}, wantArgs: "claude-2"}, // orWindow decides this one
		{window: "2", wantWindow: "2"},
	} {
		args, window, err := windowArgs(c.args, c.window, c.project)
		if c.wantErr {
			if !isUsage(err) {
				t.Errorf("%v -w %q --project %q: err = %v, want a usage error", c.args, c.window, c.project, err)
			}
			continue
		}
		if err != nil || strings.Join(args, " ") != c.wantArgs || window != c.wantWindow {
			t.Errorf("%v -w %q --project %q = %v %q (%v), want %q %q", c.args, c.window, c.project, args, window, err, c.wantArgs, c.wantWindow)
		}
	}
}

// The pair form stays PROJECT WINDOW: a first word that names no
// project is not retried as a window, or the not-found would be hidden.
func TestOrWindowKeepsTheNotFoundOfAPositionalPair(t *testing.T) {
	calls := 0
	err := orWindow(context.Background(), &Env{}, []string{"no-such-proj"}, "some-window", "", func(project, window string) error {
		calls++
		if project != "no-such-proj" || window != "some-window" {
			t.Errorf("fn(%q, %q)", project, window)
		}
		return errNoSuchProject(project)
	})
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != ExitProjectNotFound {
		t.Fatalf("err = %v, want exit %d", err, ExitProjectNotFound)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times", calls)
	}
}

// Through the command tree: with --project the window is accepted and
// the command goes on to the api, and `repose events PROJECT:CHECKOUT`
// is the usage error `repose logs` gives, not a project not found.
func TestWindowAndCheckoutArgsThroughTheTree(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("REPOSE_API_URL", fake.URL()+"/v1")
	t.Setenv("REPOSE_PROJECT", "")
	if err := os.MkdirAll(filepath.Join(cfg, "repose"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		root := newRootCmd("test")
		root.SetArgs(args)
		root.SetOut(&strings.Builder{})
		root.SetErr(&strings.Builder{})
		return root.ExecuteContext(context.Background())
	}
	// Past the argument checks: not logged in, so each stops at the api.
	for _, args := range [][]string{
		{"ps", "--project", "no-such-proj", "some-window"},
		{"attach", "--project", "no-such-proj", "some-window"},
		{"ps", "no-such-proj", "some-window"},
	} {
		err := run(args...)
		var ue cobraUsageError
		var ee *exitError
		if err == nil || errors.As(err, &ue) || (errors.As(err, &ee) && ee.code == ExitUsage) {
			t.Errorf("repose %s: err = %v, want past the argument checks", strings.Join(args, " "), err)
		}
	}
	// A checkout on a command that acts on the whole machine: exit 2
	// naming the syntax, as logs does (I-618), not exit 4.
	for _, cmd := range []string{"events", "logs"} {
		err := run(cmd, "todo-app:api")
		var ee *exitError
		if !errors.As(err, &ee) || ee.code != ExitUsage || !strings.Contains(err.Error(), "acts on the whole machine") {
			t.Errorf("repose %s todo-app:api: err = %v, want exit 2 naming the whole machine", cmd, err)
		}
	}
}
