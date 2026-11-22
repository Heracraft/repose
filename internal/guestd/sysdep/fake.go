package sysdep

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// FakeRunner records every command and replays canned results. Handlers are
// tested against it so no test needs a guest.
type FakeRunner struct {
	mu    sync.Mutex
	calls []RunSpec
	// Results maps a command name (argv[0], basename after any setpriv
	// wrapper) to what it returns. A command with no entry succeeds silently.
	Results map[string]RunResult
	// Match maps a substring of the joined argv to a result and is consulted
	// before Results. It is how two invocations of the same binary, such as
	// `systemctl is-active` and `systemctl start`, are told apart. The longest
	// matching key wins.
	Match map[string]RunResult
	// Errs maps the same key to an error the runner returns instead.
	Errs map[string]error
	// Hook, when set, is called before the canned result is chosen.
	Hook func(RunSpec)
}

// NewFakeRunner builds an empty FakeRunner.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		Results: map[string]RunResult{},
		Match:   map[string]RunResult{},
		Errs:    map[string]error{},
	}
}

// Run records the call and returns the canned result for its command.
func (f *FakeRunner) Run(_ context.Context, spec RunSpec) (RunResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	hook := f.Hook
	f.mu.Unlock()
	if hook != nil {
		hook(spec)
	}
	if len(spec.Argv) == 0 {
		return RunResult{}, errors.New("run: empty argv")
	}
	key := f.key(spec)
	joined := strings.Join(spec.Argv, " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.Errs[key]; ok {
		return RunResult{}, err
	}
	best, found := "", false
	for pattern := range f.Match {
		if strings.Contains(joined, pattern) && len(pattern) > len(best) {
			best, found = pattern, true
		}
	}
	if found {
		return f.Match[best], nil
	}
	return f.Results[key], nil
}

func (f *FakeRunner) key(spec RunSpec) string {
	argv := spec.Argv
	// Skip a synchronous systemd-run wrapper (the switch runs as a transient
	// unit, I-143) so tests key on the real command: the program is the
	// first argument that is neither an option nor an option's value.
	if argv[0] == "systemd-run" && len(argv) > 1 && argv[1] == "--wait" {
		for i := 1; i < len(argv); i++ {
			a := argv[i]
			if strings.HasPrefix(a, "-") {
				if (a == "--unit" || a == "--setenv" || a == "-p" || a == "--property") && i+1 < len(argv) {
					i++
				}
				continue
			}
			argv = argv[i:]
			break
		}
	}
	// Skip a setpriv wrapper so tests key on the real command.
	if strings.HasSuffix(argv[0], "setpriv") {
		for i, a := range argv {
			if a == "--" && i+1 < len(argv) {
				argv = argv[i+1:]
				break
			}
		}
	}
	name := argv[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// Calls returns a copy of every recorded command.
func (f *FakeRunner) Calls() []RunSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RunSpec, len(f.calls))
	copy(out, f.calls)
	return out
}

// Ran reports whether a command whose argv joined by spaces contains want was
// run, and returns the first such call.
func (f *FakeRunner) Ran(want string) (RunSpec, bool) {
	for _, c := range f.Calls() {
		if strings.Contains(strings.Join(c.Argv, " "), want) {
			return c, true
		}
	}
	return RunSpec{}, false
}

// Reset drops the recorded calls.
func (f *FakeRunner) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// FakeFreezer records freeze and thaw without touching a filesystem.
type FakeFreezer struct {
	mu        sync.Mutex
	frozen    bool
	freezes   int
	thaws     int
	paths     []string
	FreezeErr error
	ThawErr   error
}

// Freeze marks the fake filesystem frozen.
func (f *FakeFreezer) Freeze(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, "freeze "+path)
	if f.FreezeErr != nil {
		return f.FreezeErr
	}
	f.frozen = true
	f.freezes++
	return nil
}

// Thaw marks it thawed.
func (f *FakeFreezer) Thaw(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, "thaw "+path)
	if f.ThawErr != nil {
		return f.ThawErr
	}
	f.frozen = false
	f.thaws++
	return nil
}

// Paths is every call in order, as "freeze <path>" or "thaw <path>".
func (f *FakeFreezer) Paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// Frozen reports the current state, and how many times each side was called.
func (f *FakeFreezer) Frozen() (frozen bool, freezes, thaws int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frozen, f.freezes, f.thaws
}

// FakeDocker answers Ping and RunningContainers from fields.
type FakeDocker struct {
	mu         sync.Mutex
	Up         bool
	Containers int
	Err        error
}

// Ping reports the configured health.
func (d *FakeDocker) Ping(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Err != nil {
		return d.Err
	}
	if !d.Up {
		return errors.New("docker socket: connection refused")
	}
	return nil
}

// RunningContainers reports the configured count.
func (d *FakeDocker) RunningContainers(context.Context) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Err != nil {
		return 0, d.Err
	}
	if !d.Up {
		return 0, errors.New("docker socket: connection refused")
	}
	return d.Containers, nil
}

// Set changes the fake daemon's health under lock.
func (d *FakeDocker) Set(up bool, containers int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Up, d.Containers = up, containers
}
