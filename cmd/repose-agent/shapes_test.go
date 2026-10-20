package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/undo"
)

// The JSON the CLI parses, as SPINE.md "Contract 3" writes it.
const (
	pointJSON = `{"name":"r-0199a1c2","project":"todo-app","reason":"run","taken_at":"2026-10-19T12:00:00Z","bytes":32768}`
	spaceJSON = `{"total":42949672960,"used":13314424832,"free":29635248128}`
)

// TestShapes is the golden shape of every subcommand's one JSON object.
func TestShapes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
		args  []string
		want  func(f *fixture) string
	}{
		{
			name: "provision",
			args: []string{"provision", "--size-gb", "40"},
			want: func(f *fixture) string {
				return `{"store":"` + f.env.Store + `","fs":"btrfs","created":true}`
			},
		},
		{
			name: "provision again",
			setup: func(_ *testing.T, f *fixture) {
				f.backend.ready = true
			},
			args: []string{"provision", "--size-gb", "40"},
			want: func(f *fixture) string {
				return `{"store":"` + f.env.Store + `","fs":"btrfs","created":false}`
			},
		},
		{
			name: "project init",
			args: []string{"project", "init", "--project", "todo-app"},
			want: func(f *fixture) string {
				return `{"path":"` + liveDir(f.env.Store, "todo-app") + `","created":true}`
			},
		},
		{
			name: "point take",
			args: []string{"point", "take", "--project", "todo-app", "--name", "r-0199a1c2", "--reason", "run"},
			want: func(*fixture) string { return `{"point":` + pointJSON + `}` },
		},
		{
			name:  "point list",
			setup: seedPoint,
			args:  []string{"point", "list", "--project", "todo-app"},
			want:  func(*fixture) string { return `{"points":[` + pointJSON + `]}` },
		},
		{
			name: "point list with no points",
			args: []string{"point", "list", "--project", "todo-app"},
			want: func(*fixture) string { return `{"points":[]}` },
		},
		{
			name:  "point rewind",
			setup: seedPoint,
			args:  []string{"point", "rewind", "--project", "todo-app", "--name", "r-0199a1c2"},
			want:  func(*fixture) string { return `{"rewound":true,"safety_point":"r-0199a2ff"}` },
		},
		{
			name:  "point fork",
			setup: seedPoint,
			args:  []string{"point", "fork", "--project", "todo-app", "--name", "r-0199a1c2", "--as", "todo-app-2"},
			want: func(f *fixture) string {
				return `{"path":"` + liveDir(f.env.Store, "todo-app-2") + `"}`
			},
		},
		{
			name: "point delete",
			args: []string{"point", "delete", "--project", "todo-app", "--name", "r-0199a1c2"},
			want: func(*fixture) string { return `{"deleted":true}` },
		},
		{
			name:  "gc",
			setup: seedPoint,
			args:  []string{"gc", "--high-water", "85"},
			want: func(*fixture) string {
				return `{"deleted":[` + pointJSON + `],"space":` + spaceJSON + `}`
			},
		},
		{
			name: "space",
			args: []string{"space"},
			want: func(*fixture) string { return spaceJSON },
		},
		{
			name: "quiesce",
			setup: func(t *testing.T, f *fixture) {
				mkdir(t, liveDir(f.env.Store, "todo-app"))
				f.sessions["=todo-app"] = true
				f.holds("todo-app", 4242, "node")
				f.diesOn[termOf(4242)] = true
			},
			args: []string{"quiesce", "--project", "todo-app"},
			want: func(*fixture) string { return `{"stopped":["tmux:todo-app","node"],"busy":[]}` },
		},
		{
			name: "status",
			setup: func(t *testing.T, f *fixture) {
				f.writeMachine(t)
				mkdir(t, liveDir(f.env.Store, "todo-app"))
				seedPoint(t, f)
			},
			args: []string{"status"},
			want: func(f *fixture) string {
				return `{"machine":{"name":"homebox","id":"0199a1c2-1f3b-7c4d-9e2a-5b6c7d8e9f01","store":"` +
					f.env.Store + `"},"projects":[{"slug":"todo-app","points":1}],"space":` + spaceJSON + `}`
			},
		},
		{
			name: "version",
			args: []string{"version"},
			want: func(*fixture) string { return `{"version":"dev"}` },
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.setup != nil {
				c.setup(t, f)
			}
			if code := f.run(c.args...); code != 0 {
				t.Fatalf("exit %d, stdout %s", code, f.stdout())
			}
			if got := f.stdout(); got != c.want(f) {
				t.Errorf("stdout\n got %s\nwant %s", got, c.want(f))
			}
			// One object and nothing else: the CLI parses this stream.
			if n := strings.Count(f.out.String(), "\n"); n != 1 {
				t.Errorf("stdout has %d lines, want 1: %q", n, f.out.String())
			}
		})
	}
}

// TestProvisionSizeInBytes is the only place --size-gb becomes bytes.
func TestProvisionSizeInBytes(t *testing.T) {
	f := newFixture(t)
	if code := f.run("provision", "--size-gb", "40"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	if want := uint64(40) << 30; f.backend.sizeBytes != want {
		t.Errorf("Provision got %d bytes, want %d", f.backend.sizeBytes, want)
	}
}

// TestProvisionNeedsASizeOnlyForANewStore: a resend over SSH, or the
// installer's second run, carries no size and changes nothing.
func TestProvisionNeedsASizeOnlyForANewStore(t *testing.T) {
	f := newFixture(t)
	if code := f.run("provision"); code != 2 {
		t.Fatalf("exit %d on a new store with no size, want 2", code)
	}
	f.out.Reset()
	f.backend.ready = true
	if code := f.run("provision"); code != 0 {
		t.Fatalf("exit %d on a store already there, want 0: %s", code, f.stdout())
	}
	want := `{"store":"` + f.env.Store + `","fs":"btrfs","created":false}`
	if got := f.stdout(); got != want {
		t.Errorf("stdout\n got %s\nwant %s", got, want)
	}
}

// TestGCHighWaterDefault: the flag's default is the 85 percent of Contract 3.
func TestGCHighWaterDefault(t *testing.T) {
	f := newFixture(t)
	if code := f.run("gc"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stdout())
	}
	if f.backend.highWater != 85 {
		t.Errorf("GC got high-water %d, want 85", f.backend.highWater)
	}
}

// TestProjectInitIsIdempotent: a second init answers created:false, keeps the
// link it made and nothing else changes.
func TestProjectInitIsIdempotent(t *testing.T) {
	f := newFixture(t)
	if code := f.run("project", "init", "--project", "todo-app"); code != 0 {
		t.Fatalf("first init: exit %d: %s", code, f.stdout())
	}
	link := filepath.Join(f.env.Home, "todo-app")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("the home link is missing: %v", err)
	}
	if want := liveDir(f.env.Store, "todo-app"); target != want {
		t.Fatalf("the home link points at %s, want %s", target, want)
	}

	f.out.Reset()
	if code := f.run("project", "init", "--project", "todo-app"); code != 0 {
		t.Fatalf("second init: exit %d: %s", code, f.stdout())
	}
	want := `{"path":"` + liveDir(f.env.Store, "todo-app") + `","created":false}`
	if got := f.stdout(); got != want {
		t.Errorf("second init\n got %s\nwant %s", got, want)
	}
}

// seedPoint gives the project the one point the golden shapes carry.
func seedPoint(t *testing.T, f *fixture) {
	t.Helper()
	f.backend.points["todo-app"] = []undo.Point{takenPoint}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

// A project's tmux session is what an agent window is opened in and what
// an attach attaches to. On a guest a systemd user unit starts it; here
// nothing else would, so `project init` does, and a second init leaves
// the running one alone.
func TestProjectInitStartsTheTmuxSession(t *testing.T) {
	f := newFixture(t)
	if code := run(context.Background(), f.env, []string{"project", "init", "--project", "todo-app"}); code != 0 {
		t.Fatalf("exit %d: %s", code, f.out)
	}
	if !slices.Contains(f.tmux, "has-session -t todo-app") {
		t.Errorf("init did not ask whether the session runs: %v", f.tmux)
	}
	want := "new-session -d -s todo-app -c " + liveDir(f.env.Store, "todo-app")
	if !slices.Contains(f.tmux, want) {
		t.Errorf("init did not start the session: %v", f.tmux)
	}
	f.tmux = nil
	if code := run(context.Background(), f.env, []string{"project", "init", "--project", "todo-app"}); code != 0 {
		t.Fatalf("second init: exit %d: %s", code, f.out)
	}
	for _, c := range f.tmux {
		if strings.HasPrefix(c, "new-session") {
			t.Errorf("the second init started a second session: %v", f.tmux)
		}
	}
}
