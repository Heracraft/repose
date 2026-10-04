// Package systemd starts and watches the transient units hostd owns:
// guest@<id> (Cloud Hypervisor) and virtiofsd@<id>.
package systemd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// Systemd is what the guest state machine needs from unit management.
type Systemd interface {
	// Run starts argv as a transient service unit; if the unit is already
	// active it does nothing.
	Run(ctx context.Context, unit string, props []string, argv []string) error
	IsActive(ctx context.Context, unit string) (bool, error)
	Stop(ctx context.Context, unit string) error
	Kill(ctx context.Context, unit string) error
	// Show returns the requested properties.
	Show(ctx context.Context, unit string, props ...string) (map[string]string, error)
	// ListUnits returns unit names matching a glob, e.g. "guest@*".
	ListUnits(ctx context.Context, pattern string) ([]string, error)
	// WaitInactive blocks until the unit is no longer active or ctx ends.
	WaitInactive(ctx context.Context, unit string) error
}

// Real drives systemctl and systemd-run.
type Real struct {
	R    shell.Runner
	Poll time.Duration
}

// NewReal returns a Real polling every second.
func NewReal(r shell.Runner) *Real { return &Real{R: r, Poll: time.Second} }

// Run implements Systemd.
func (s *Real) Run(ctx context.Context, unit string, props []string, argv []string) error {
	active, err := s.IsActive(ctx, unit)
	if err != nil {
		return err
	}
	if active {
		return nil
	}
	args := []string{"systemd-run", "--unit", unit, "--collect", "--property", "KillMode=mixed"}
	for _, p := range props {
		args = append(args, "--property", p)
	}
	args = append(args, "--")
	args = append(args, argv...)
	_, err = s.R.Run(ctx, args...)
	return err
}

// IsActive implements Systemd.
func (s *Real) IsActive(ctx context.Context, unit string) (bool, error) {
	res, err := s.R.Run(ctx, "systemctl", "is-active", unit)
	state := strings.TrimSpace(string(res.Stdout))
	var ee *shell.ExitError
	if errors.As(err, &ee) {
		return state == "activating" || state == "deactivating", nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Stop implements Systemd.
func (s *Real) Stop(ctx context.Context, unit string) error {
	_, err := s.R.Run(ctx, "systemctl", "stop", unit)
	var ee *shell.ExitError
	if errors.As(err, &ee) && strings.Contains(string(ee.Result.Stderr), "not loaded") {
		return nil
	}
	return err
}

// Kill implements Systemd.
func (s *Real) Kill(ctx context.Context, unit string) error {
	_, err := s.R.Run(ctx, "systemctl", "kill", "-s", "SIGKILL", unit)
	var ee *shell.ExitError
	if errors.As(err, &ee) && strings.Contains(string(ee.Result.Stderr), "not loaded") {
		return nil
	}
	return err
}

// Show implements Systemd.
func (s *Real) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	res, err := s.R.Run(ctx, "systemctl", "show", "-p", strings.Join(props, ","), unit)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out, nil
}

// ListUnits implements Systemd.
func (s *Real) ListUnits(ctx context.Context, pattern string) ([]string, error) {
	res, err := s.R.Run(ctx, "systemctl", "list-units", "--all", "--plain", "--no-legend", pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out, nil
}

// WaitInactive implements Systemd.
func (s *Real) WaitInactive(ctx context.Context, unit string) error {
	t := time.NewTicker(s.Poll)
	defer t.Stop()
	for {
		active, err := s.IsActive(ctx, unit)
		if ctxErr := ctx.Err(); ctxErr != nil {
			// systemctl killed by the deadline exits non-zero with nothing
			// on stdout, which IsActive reads as inactive: the stop's
			// timeout used to pass as a clean power-off and skip the
			// fallback (I-186).
			return ctxErr
		}
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// FakeUnit is a unit the Fake knows about.
type FakeUnit struct {
	Argv     []string
	Props    []string
	Active   bool
	ExitCode int
	CPUNSec  uint64
	MemBytes uint64
	MainPID  int
}

// Fake is the in-memory unit table for tests. OnRun runs before a unit is
// started and may fail it; a nil OnRun starts every unit.
type Fake struct {
	mu     sync.Mutex
	Units  map[string]*FakeUnit
	OnRun  func(unit string, argv []string) error
	OnStop func(unit string)
	Ops    []string
}

// NewFake returns an empty fake.
func NewFake() *Fake { return &Fake{Units: map[string]*FakeUnit{}} }

func (f *Fake) Run(_ context.Context, unit string, props []string, argv []string) error {
	f.mu.Lock()
	f.Ops = append(f.Ops, "run "+unit)
	if u, ok := f.Units[unit]; ok && u.Active {
		f.mu.Unlock()
		return nil
	}
	hook := f.OnRun
	f.mu.Unlock()
	if hook != nil {
		if err := hook(unit, argv); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.Units[unit] = &FakeUnit{Argv: argv, Props: props, Active: true}
	f.mu.Unlock()
	return nil
}

func (f *Fake) IsActive(_ context.Context, unit string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.Units[unit]
	return ok && u.Active, nil
}

func (f *Fake) Stop(_ context.Context, unit string) error {
	f.mu.Lock()
	f.Ops = append(f.Ops, "stop "+unit)
	if u, ok := f.Units[unit]; ok {
		u.Active = false
	}
	hook := f.OnStop
	f.mu.Unlock()
	if hook != nil {
		hook(unit)
	}
	return nil
}

func (f *Fake) Kill(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Ops = append(f.Ops, "kill "+unit)
	if u, ok := f.Units[unit]; ok {
		u.Active = false
		u.ExitCode = 137
	}
	return nil
}

func (f *Fake) Show(_ context.Context, unit string, props ...string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.Units[unit]
	if !ok {
		return nil, fmt.Errorf("unit %s not loaded", unit)
	}
	out := map[string]string{}
	for _, p := range props {
		switch p {
		case "ActiveState":
			if u.Active {
				out[p] = "active"
			} else {
				out[p] = "inactive"
			}
		case "ExecMainStatus":
			out[p] = fmt.Sprint(u.ExitCode)
		case "CPUUsageNSec":
			out[p] = fmt.Sprint(u.CPUNSec)
		case "MemoryCurrent":
			out[p] = fmt.Sprint(u.MemBytes)
		case "MainPID":
			out[p] = fmt.Sprint(u.MainPID)
		}
	}
	return out, nil
}

func (f *Fake) ListUnits(_ context.Context, pattern string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := strings.TrimSuffix(pattern, "*")
	var out []string
	for name, u := range f.Units {
		if strings.HasPrefix(name, prefix) && u.Active {
			out = append(out, name)
		}
	}
	return out, nil
}

func (f *Fake) WaitInactive(ctx context.Context, unit string) error {
	for {
		if ok, _ := f.IsActive(ctx, unit); !ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Exit marks a unit as exited with code, as if the process died.
func (f *Fake) Exit(unit string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.Units[unit]; ok {
		u.Active = false
		u.ExitCode = code
	}
}

// Set updates a unit's accounting for the sample tests.
func (f *Fake) Set(unit string, cpuNSec, memBytes uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.Units[unit]; ok {
		u.CPUNSec, u.MemBytes = cpuNSec, memBytes
	}
}
