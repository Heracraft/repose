package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/undo"
)

// TestErrorCodes is one case per code of SPINE.md "Contract 3". The envelope
// is the whole of stdout and the exit is non-zero.
func TestErrorCodes(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, f *fixture)
		args     []string
		wantCode string
		wantExit int
	}{
		{
			name:     "invalid_argument",
			args:     []string{"point", "take", "--project", "todo-app", "--name", "r-1", "--reason", "whenever"},
			wantCode: codeInvalidArgument,
			wantExit: 2,
		},
		{
			name: "not_found",
			setup: func(_ *testing.T, f *fixture) {
				// Wrapped, as a backend reports it from the call that failed.
				f.backend.errs["Rewind"] = fmt.Errorf("snapshot r-1: %w", undo.ErrNotFound)
			},
			args:     []string{"point", "rewind", "--project", "todo-app", "--name", "r-1"},
			wantCode: codeNotFound,
			wantExit: 1,
		},
		{
			name: "busy",
			setup: func(_ *testing.T, f *fixture) {
				f.holds("todo-app", 4242, "claude")
			},
			args:     []string{"point", "rewind", "--project", "todo-app", "--name", "r-1"},
			wantCode: codeBusy,
			wantExit: 1,
		},
		{
			name: "no_space",
			setup: func(_ *testing.T, f *fixture) {
				f.backend.errs["Take"] = undo.ErrNoSpace
			},
			args:     []string{"point", "take", "--project", "todo-app", "--name", "r-1", "--reason", "run"},
			wantCode: codeNoSpace,
			wantExit: 1,
		},
		{
			name: "exists",
			setup: func(_ *testing.T, f *fixture) {
				f.backend.errs["Fork"] = undo.ErrExists
			},
			args:     []string{"point", "fork", "--project", "todo-app", "--name", "r-1", "--as", "todo-app-2"},
			wantCode: codeExists,
			wantExit: 1,
		},
		{
			name: "internal",
			setup: func(_ *testing.T, f *fixture) {
				f.backend.errs["Space"] = errors.New("btrfs filesystem usage: exit status 1")
			},
			args:     []string{"space"},
			wantCode: codeInternal,
			wantExit: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.setup != nil {
				c.setup(t, f)
			}
			code := f.run(c.args...)
			if code != c.wantExit {
				t.Errorf("exit %d, want %d", code, c.wantExit)
			}
			var ae errorEnvelope
			if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
				t.Fatalf("stdout is not the error envelope: %v: %s", err, f.stdout())
			}
			if ae.Error.Code != c.wantCode {
				t.Errorf("code %q, want %q", ae.Error.Code, c.wantCode)
			}
			if ae.Error.Message == "" {
				t.Error("the envelope carries no message")
			}
			if n := strings.Count(f.out.String(), "\n"); n != 1 {
				t.Errorf("stdout has %d lines, want 1: %q", n, f.out.String())
			}
		})
	}
}

// A full store is the one failure a user can do something about, so the
// refusal names the store and the command that frees it. A bare
// "no_space" leaves them nowhere to start.
func TestNoSpaceNamesTheStore(t *testing.T) {
	f := newFixture(t)
	f.backend.errs["Take"] = undo.ErrNoSpace
	if code := f.run("point", "take", "--project", "todo-app", "--name", "r-1", "--reason", "run"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var ae errorEnvelope
	if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
		t.Fatalf("stdout is not the error envelope: %v", err)
	}
	if ae.Error.Code != codeNoSpace {
		t.Fatalf("code %q", ae.Error.Code)
	}
	for _, want := range []string{f.env.Store, "repose rewind --rm POINT"} {
		if !strings.Contains(ae.Error.Message, want) {
			t.Errorf("the message does not name %q: %q", want, ae.Error.Message)
		}
	}
}

// TestBusyNamesTheProcesses: the refusal says what is still running, by
// process name and pid, which is what a user needs to act on it and the
// most about it the agent may report.
func TestBusyNamesTheProcesses(t *testing.T) {
	f := newFixture(t)
	f.holds("todo-app", 4242, "claude")
	f.holds("todo-app", 4243, "node")

	if code := f.run("point", "rewind", "--project", "todo-app", "--name", "r-1"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var ae errorEnvelope
	if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
		t.Fatalf("stdout is not the error envelope: %v", err)
	}
	for _, want := range []string{"claude (pid 4242)", "node (pid 4243)"} {
		if !strings.Contains(ae.Error.Message, want) {
			t.Errorf("the message does not name %s: %q", want, ae.Error.Message)
		}
	}
	// Nothing was rewound: the backend was never asked.
	for _, call := range f.backend.calls {
		if call == "Rewind" {
			t.Error("Rewind ran on a project that is not quiesced")
		}
	}
	// No path leaves the agent, in the message or anywhere else.
	if strings.Contains(ae.Error.Message, f.env.Store) {
		t.Errorf("the message carries a path: %q", ae.Error.Message)
	}
}

// TestUnknownCommand: the usage goes to stderr, where help belongs, and
// stdout stays the one JSON object.
func TestUnknownCommand(t *testing.T) {
	f := newFixture(t)
	if code := f.run("rewindy"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	var ae errorEnvelope
	if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
		t.Fatalf("stdout is not the error envelope: %v", err)
	}
	if ae.Error.Code != codeInvalidArgument {
		t.Errorf("code %q, want %q", ae.Error.Code, codeInvalidArgument)
	}
	if !strings.Contains(f.errOut.String(), "usage: repose-agent") {
		t.Errorf("stderr carries no usage: %q", f.errOut.String())
	}
}

// TestNoCommand: run with nothing, the agent refuses the same way.
func TestNoCommand(t *testing.T) {
	f := newFixture(t)
	if code := f.run(); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(f.stdout(), codeInvalidArgument) {
		t.Errorf("stdout %s", f.stdout())
	}
}

// TestStatusWithoutTheMachineRecord: a box the installer has not finished.
func TestStatusWithoutTheMachineRecord(t *testing.T) {
	f := newFixture(t)
	if code := f.run("status"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var ae errorEnvelope
	if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
		t.Fatalf("stdout is not the error envelope: %v", err)
	}
	if ae.Error.Code != codeNotFound {
		t.Errorf("code %q, want %q", ae.Error.Code, codeNotFound)
	}
}

// TestProjectInitRefusesAnOccupiedHome: ~/<slug> that is the box owner's own
// file is left alone.
func TestProjectInitRefusesAnOccupiedHome(t *testing.T) {
	f := newFixture(t)
	mkdir(t, filepath.Join(f.env.Home, "todo-app"))
	if code := f.run("project", "init", "--project", "todo-app"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var ae errorEnvelope
	if err := json.Unmarshal([]byte(f.stdout()), &ae); err != nil {
		t.Fatalf("stdout is not the error envelope: %v", err)
	}
	if ae.Error.Code != codeExists {
		t.Errorf("code %q, want %q", ae.Error.Code, codeExists)
	}
}
