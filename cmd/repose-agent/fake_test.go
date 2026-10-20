package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/undo"
)

// takenPoint is what the fake's Take answers with, and the point the golden
// shapes carry.
var takenPoint = undo.Point{
	Name:    "r-0199a1c2",
	Project: "todo-app",
	Reason:  "run",
	TakenAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
}

// fakeUndo is the store of SPINE.md "Contract 1" with no box under it. Every
// method reads errs first, so a test names the failure it wants by method
// name.
type fakeUndo struct {
	store   string
	points  map[string][]undo.Point
	holders map[string][]undo.Holder
	space   undo.Space
	ready   bool
	errs    map[string]error

	// safety is the point Rewind answers with: the one the real store takes
	// of the state it replaces.
	safety string

	sizeBytes uint64        // what Provision was asked for
	highWater int           // what GC was asked for
	gc        undo.GCResult // the counts GC answers with, beside the points
	calls     []string      // method names, in order
}

var _ undoStore = (*fakeUndo)(nil)

func newFakeUndo(store string) *fakeUndo {
	return &fakeUndo{
		store:   store,
		points:  map[string][]undo.Point{},
		holders: map[string][]undo.Holder{},
		errs:    map[string]error{},
		space:   undo.Space{Total: 42949672960, Used: 13314424832, Free: 29635248128},
		safety:  "r-0199a2ff",
	}
}

func (f *fakeUndo) call(name string) error {
	f.calls = append(f.calls, name)
	return f.errs[name]
}

// dropHolder is a process leaving the live area, which is what a signal it
// answers looks like from here.
func (f *fakeUndo) dropHolder(pid int) {
	for project, hs := range f.holders {
		kept := make([]undo.Holder, 0, len(hs))
		for _, h := range hs {
			if h.PID != pid {
				kept = append(kept, h)
			}
		}
		f.holders[project] = kept
	}
}

func (f *fakeUndo) Ready(_ context.Context) (bool, error) {
	if err := f.call("Ready"); err != nil {
		return false, err
	}
	return f.ready, nil
}

func (f *fakeUndo) Provision(_ context.Context, sizeBytes uint64) error {
	f.sizeBytes = sizeBytes
	if err := f.call("Provision"); err != nil {
		return err
	}
	f.ready = true
	return os.MkdirAll(filepath.Join(f.store, "projects"), 0o755)
}

func (f *fakeUndo) Init(_ context.Context, project string) (string, error) {
	if err := f.call("Init"); err != nil {
		return "", err
	}
	path := liveDir(f.store, project)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	if _, ok := f.points[project]; !ok {
		f.points[project] = nil
	}
	return path, nil
}

func (f *fakeUndo) Take(_ context.Context, project, name, reason string) (undo.Point, error) {
	if err := f.call("Take"); err != nil {
		return undo.Point{}, err
	}
	p := undo.Point{
		Name:    name,
		Project: project,
		Reason:  reason,
		TakenAt: takenPoint.TakenAt,
	}
	f.points[project] = append([]undo.Point{p}, f.points[project]...)
	return p, nil
}

func (f *fakeUndo) List(_ context.Context, project string) ([]undo.Point, error) {
	if err := f.call("List"); err != nil {
		return nil, err
	}
	return f.points[project], nil
}

func (f *fakeUndo) Rewind(_ context.Context, project, name string) (string, error) {
	if err := f.call("Rewind"); err != nil {
		return "", err
	}
	safety := undo.Point{Name: f.safety, Project: project, Reason: undo.ReasonRewind, TakenAt: takenPoint.TakenAt}
	f.points[project] = append([]undo.Point{safety}, f.points[project]...)
	return f.safety, nil
}

func (f *fakeUndo) Fork(_ context.Context, srcProject, name, newProject string) error {
	if err := f.call("Fork"); err != nil {
		return err
	}
	return os.MkdirAll(liveDir(f.store, newProject), 0o755)
}

func (f *fakeUndo) Delete(_ context.Context, project, name string) error {
	return f.call("Delete")
}

func (f *fakeUndo) Space(_ context.Context) (undo.Space, error) {
	if err := f.call("Space"); err != nil {
		return undo.Space{}, err
	}
	return f.space, nil
}

func (f *fakeUndo) GC(_ context.Context, highWaterPercent int) (undo.GCResult, error) {
	f.highWater = highWaterPercent
	if err := f.call("GC"); err != nil {
		return undo.GCResult{}, err
	}
	res := f.gc
	res.Deleted = f.points["todo-app"]
	return res, nil
}

func (f *fakeUndo) Holders(_ context.Context, project string) ([]undo.Holder, error) {
	if err := f.call("Holders"); err != nil {
		return nil, err
	}
	return f.holders[project], nil
}

// fixture is an agent with a temporary store and home, and the fake store
// behind the interface.
type fixture struct {
	env     *Env
	out     *strings.Builder
	errOut  *strings.Builder
	backend *fakeUndo

	// tmux records the tmux command lines, and sessions names the sessions
	// the fake tmux has, by the target the agent asks for.
	tmux     []string
	sessions map[string]bool
	// signals records pid and signal; diesOn names the signals a process
	// answers by leaving the live area.
	signals []string
	diesOn  map[string]bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, home := t.TempDir(), t.TempDir()
	f := &fixture{
		out:      &strings.Builder{},
		errOut:   &strings.Builder{},
		backend:  newFakeUndo(store),
		sessions: map[string]bool{},
		diesOn:   map[string]bool{},
	}
	f.env = &Env{
		Stdout:      f.out,
		Stderr:      f.errOut,
		Store:       store,
		MachineFile: filepath.Join(t.TempDir(), "machine.json"),
		Home:        home,
		Open:        func(string) (undoStore, error) { return f.backend, nil },
		Tmux: func(_ context.Context, args ...string) bool {
			f.tmux = append(f.tmux, strings.Join(args, " "))
			switch args[0] {
			case "has-session":
				return f.sessions[args[2]]
			case "kill-session":
				delete(f.sessions, args[2])
				return true
			case "new-session":
				// new-session -d -s <slug> -c <dir>: the session an
				// agent window is opened in (project init).
				for i, a := range args {
					if a == "-s" && i+1 < len(args) {
						f.sessions[args[i+1]] = true
					}
				}
				return true
			}
			return false
		},
		Signal: func(pid int, sig os.Signal) error {
			key := signalKey(pid, sig)
			f.signals = append(f.signals, key)
			if f.diesOn[key] {
				f.backend.dropHolder(pid)
			}
			return nil
		},
		Grace: 0,
	}
	return f
}

// run dispatches one command line and returns the exit code.
func (f *fixture) run(args ...string) int {
	return run(context.Background(), f.env, args)
}

// stdout is what the command printed, without its newline.
func (f *fixture) stdout() string { return strings.TrimRight(f.out.String(), "\n") }

// holds puts a process in the project's live area.
func (f *fixture) holds(project string, pid int, comm string) {
	f.backend.holders[project] = append(f.backend.holders[project], undo.Holder{PID: pid, Comm: comm})
}

// writeMachine puts the installer's record where status reads it.
func (f *fixture) writeMachine(t *testing.T) {
	t.Helper()
	body := `{"name":"homebox","id":"0199a1c2-1f3b-7c4d-9e2a-5b6c7d8e9f01","store":"` + f.env.Store + `"}`
	if err := os.WriteFile(f.env.MachineFile, []byte(body), 0o600); err != nil {
		t.Fatalf("write the machine record: %v", err)
	}
}

// termOf and killOf are the keys the fake Signal records, so a test says
// which signal a process answers.
func termOf(pid int) string { return signalKey(pid, syscall.SIGTERM) }
func killOf(pid int) string { return signalKey(pid, syscall.SIGKILL) }

func signalKey(pid int, sig os.Signal) string {
	return strconv.Itoa(pid) + " " + sig.String()
}
