package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

// fakeNix is every process the environment commands start, behind
// undo.Runner — the seam internal/undo's backend uses for btrfs, and the
// pattern of internal/undo's FakeRunner: every argv is recorded in order,
// and a key in fail fails the invocation whose argv contains it.
type fakeNix struct {
	// calls is every argv, in order.
	calls [][]string
	// stdout answers the invocation whose joined argv contains the key.
	stdout map[string]string
	// fail fails the invocation whose joined argv contains the key, with
	// the value as its last stderr line.
	fail map[string]string
	// have is the commands a login shell finds, and version what a
	// runtime's own --version prints, by command.
	have    map[string]bool
	version map[string]string
	// provides are the commands an attribute's install puts on PATH, so a
	// `nix profile add` is followed by a `command -v` that finds them.
	provides map[string][]string
	// installed is every attribute `nix profile add` was given, in order,
	// and removed every one `nix profile remove` was given.
	installed []string
	removed   []string
}

func newFakeNix() *fakeNix {
	return &fakeNix{
		stdout:   map[string]string{},
		fail:     map[string]string{},
		have:     map[string]bool{},
		version:  map[string]string{},
		provides: map[string][]string{},
	}
}

// Run implements undo.Runner.
func (f *fakeNix) Run(_ context.Context, argv ...string) (undo.Result, error) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	joined := strings.Join(argv, " ")
	for key, stderr := range f.fail {
		if strings.Contains(joined, key) {
			res := undo.Result{Stderr: []byte(stderr + "\n"), ExitCode: 1}
			return res, &undo.ExitError{Argv: argv, Result: res}
		}
	}
	if argv[0] == "bash" && len(argv) == 3 {
		return f.shell(argv, argv[2])
	}
	if i := slices.Index(argv, "profile"); i >= 0 && i+1 < len(argv) {
		switch argv[i+1] {
		case "add", "install":
			if attr := strings.TrimPrefix(last(argv), "nixpkgs#"); attr != "--help" {
				f.add(attr)
			}
		case "remove":
			attr := last(argv)
			f.removed = append(f.removed, attr)
			f.installed = slices.DeleteFunc(f.installed, func(a string) bool { return a == attr })
		case "list":
			// A test that wants a listing of its own sets stdout; without
			// one the profile holds what it was asked to add.
			if out, ok := f.stdout["profile list"]; ok {
				return undo.Result{Stdout: []byte(out)}, nil
			}
			return undo.Result{Stdout: []byte(f.listing())}, nil
		}
	}
	return f.answer(joined), nil
}

// add is an install: the attribute is in the profile, its commands are on
// PATH, and a runtime's attribute is what a login shell then reports as
// that runtime's version, as it is on the box.
func (f *fakeNix) add(attr string) {
	f.installed = append(f.installed, attr)
	for _, bin := range f.provides[attr] {
		f.have[bin] = true
	}
	if lang, out, ok := runtimeOf(attr); ok {
		f.version[lang] = out
	}
}

func runtimeOf(attr string) (string, string, bool) {
	switch {
	case strings.HasPrefix(attr, "nodejs_"):
		return "node", "v" + strings.TrimPrefix(attr, "nodejs_") + ".1.0\n", true
	case strings.HasPrefix(attr, "ruby_"):
		v := strings.ReplaceAll(strings.TrimPrefix(attr, "ruby_"), "_", ".")
		return "ruby", "ruby " + v + ".1 (2026-01-01 revision abc) [x86_64-linux]\n", true
	case strings.HasPrefix(attr, "jdk") && strings.HasSuffix(attr, "_headless"):
		v := strings.TrimSuffix(strings.TrimPrefix(attr, "jdk"), "_headless")
		return "java", "openjdk version \"" + v + ".0.1\" 2026-01-20\n", true
	}
	return "", "", false
}

// listing is `nix profile list --json`, in the shape nix prints since it
// keyed the elements by name.
func (f *fakeNix) listing() string {
	parts := make([]string, 0, len(f.installed))
	for _, attr := range f.installed {
		parts = append(parts, `"`+attr+`":{"attrPath":"legacyPackages.x86_64-linux.`+attr+`"}`)
	}
	return `{"elements":{` + strings.Join(parts, ",") + `}}`
}

// shell is `bash -lc`: the probes the agent makes of the project's
// environment. Anything else a script asks for exits 0 with nothing.
func (f *fakeNix) shell(argv []string, script string) (undo.Result, error) {
	if _, rest, ok := strings.Cut(script, "command -v "); ok {
		bin := ""
		if fields := strings.Fields(rest); len(fields) > 0 {
			bin = strings.Trim(fields[0], "'")
		}
		if f.have[bin] {
			return undo.Result{Stdout: []byte("/usr/bin/" + bin + "\n")}, nil
		}
		res := undo.Result{ExitCode: 1}
		return res, &undo.ExitError{Argv: argv, Result: res}
	}
	for _, lang := range runtimePins {
		if !strings.Contains(script, lang+" -") {
			continue
		}
		out, ok := f.version[lang]
		if !ok {
			res := undo.Result{ExitCode: 127}
			return res, &undo.ExitError{Argv: argv, Result: res}
		}
		return undo.Result{Stdout: []byte(out)}, nil
	}
	// Whatever else a script installs — a laptop package manager, where
	// the box has no nix — puts its commands on PATH.
	for pkg, bins := range f.provides {
		if !strings.Contains(script, pkg) {
			continue
		}
		for _, bin := range bins {
			f.have[bin] = true
		}
	}
	return undo.Result{}, nil
}

func (f *fakeNix) answer(joined string) undo.Result {
	for key, out := range f.stdout {
		if strings.Contains(joined, key) {
			return undo.Result{Stdout: []byte(out)}
		}
	}
	return undo.Result{}
}

func last(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[len(argv)-1]
}

// fixture is an agent with a temporary store and home, and the fake store
// behind the interface.
type fixture struct {
	env     *Env
	out     *strings.Builder
	errOut  *strings.Builder
	backend *fakeUndo
	nix     *fakeNix

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
		nix:      newFakeNix(),
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
		Runner:      f.nix,
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

// writeRepoConfig puts a repose.nix at the project's checkout root, which
// is the live area the sync delivers it into.
func (f *fixture) writeRepoConfig(t *testing.T, project, body string) {
	t.Helper()
	live := liveDir(f.env.Store, project)
	mkdir(t, live)
	if err := os.WriteFile(filepath.Join(live, repoConfigName), []byte(body), 0o644); err != nil {
		t.Fatalf("write the project's %s: %v", repoConfigName, err)
	}
}

// writeWanted puts the carry's tool list where the laptop writes it.
func (f *fixture) writeWanted(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(f.env.Home, ".repose")
	mkdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "tools-wanted.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write the carry's tool list: %v", err)
	}
}

// termOf and killOf are the keys the fake Signal records, so a test says
// which signal a process answers.
func termOf(pid int) string { return signalKey(pid, syscall.SIGTERM) }
func killOf(pid int) string { return signalKey(pid, syscall.SIGKILL) }

func signalKey(pid int, sig os.Signal) string {
	return strconv.Itoa(pid) + " " + sig.String()
}
