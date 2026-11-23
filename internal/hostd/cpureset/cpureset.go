// Package cpureset takes each of the host's CPUs offline and back online
// after a guest's hypervisor has exited, on a host that is itself a
// virtual machine under Apple's Virtualization framework: the Mac
// appliance (DECISIONS I-1578).
//
// There, a guest that powers itself off (what `repose rm` and `repose
// stop` ask it to do) leaves every appliance CPU its vCPUs last ran on
// busy in macOS: the appliance's kernel counts that CPU idle, takes no
// interrupt on it, and runs no task on it, yet the Mac's Virtualization
// process burns a whole core for it until a vCPU of another guest runs
// there. Taking the CPU offline and online again (PSCI CPU_OFF and
// CPU_ON, a reset of that virtual CPU in the framework) ends it at once.
// Measured on an M4, macOS's Virtualization process: 200 % before, under
// 3 % after, for about 25 ms a CPU (DECISIONS I-1578 has the runs).
//
// The cycle is one CPU at a time, so the host never has fewer than all
// but one of its CPUs, and a running guest's vCPU threads move to another
// CPU for the few milliseconds theirs is away. A CPU that does not come
// back is retried and then logged; nothing here ever leaves one offline
// on purpose.
package cpureset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SysRoot is where sysfs is; a test points it at a directory tree.
const SysRoot = "/sys"

// AppleVirtualization reports whether the host is a VM under Apple's
// Virtualization framework, from its DMI product name ("Apple
// Virtualization Generic Platform" under vfkit on macOS 15 and 26).
func AppleVirtualization(sysRoot string) bool {
	b, err := os.ReadFile(filepath.Join(sysRoot, "class", "dmi", "id", "product_name"))
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(b)), "Apple Virtualization")
}

// Enabled resolves the --cpu-reset flag: "auto" is on under Apple's
// Virtualization framework and off elsewhere.
func Enabled(mode, sysRoot string) (bool, error) {
	switch mode {
	case "", "auto":
		return AppleVirtualization(sysRoot), nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("--cpu-reset %q: want auto, on or off", mode)
}

// Resetter cycles the CPUs when kicked. Kicks that arrive while a cycle
// runs or waits coalesce into one more cycle.
type Resetter struct {
	SysRoot string
	Log     *slog.Logger
	// Settle is how long after the last kick the cycle waits, so the stop
	// of several guests at once costs one cycle. Zero cycles at once.
	Settle time.Duration
	// Write writes a sysfs file; nil is os.WriteFile. A test records
	// the order of the writes through it.
	Write func(path, value string) error
	kick  chan struct{}
}

func (r *Resetter) write(path, value string) error {
	if r.Write != nil {
		return r.Write(path, value)
	}
	return os.WriteFile(path, []byte(value), 0)
}

// New returns a Resetter over sysRoot.
func New(sysRoot string, log *slog.Logger) *Resetter {
	return &Resetter{SysRoot: sysRoot, Log: log, Settle: 2 * time.Second, kick: make(chan struct{}, 1)}
}

// Kick asks for a cycle; it never blocks. A nil Resetter does nothing,
// which is a host the problem does not exist on.
func (r *Resetter) Kick() {
	if r == nil {
		return
	}
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run cycles on each kick until ctx ends.
func (r *Resetter) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		}
		if r.Settle > 0 {
			t := time.NewTimer(r.Settle)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
		start := time.Now()
		n, err := r.Cycle()
		if err != nil {
			r.Log.Error("cpu reset failed", "event", "cpu_reset", "cpus", n, "err", err)
			continue
		}
		r.Log.Info("cpus reset", "event", "cpu_reset", "cpus", n, "duration_ms", time.Since(start).Milliseconds())
	}
}

// Cycle takes each online CPU that can go offline offline and back
// online, one at a time, and returns how many it cycled. With one CPU
// online it does nothing.
func (r *Resetter) Cycle() (int, error) {
	cpuDir := filepath.Join(r.SysRoot, "devices", "system", "cpu")
	b, err := os.ReadFile(filepath.Join(cpuDir, "online"))
	if err != nil {
		return 0, err
	}
	cpus, err := ParseList(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, err
	}
	if len(cpus) < 2 {
		return 0, nil
	}
	n := 0
	var errs []error
	for _, c := range cpus {
		f := filepath.Join(cpuDir, "cpu"+strconv.Itoa(c), "online")
		if _, err := os.Stat(f); err != nil {
			continue // a CPU the kernel cannot take offline has no file
		}
		if err := r.write(f, "0"); err != nil {
			errs = append(errs, fmt.Errorf("cpu%d offline: %w", c, err))
			continue // still online: nothing to undo
		}
		if err := r.online(f); err != nil {
			// The one state this must never leave: a CPU the host has
			// lost. Stop here rather than take another.
			return n, errors.Join(append(errs, fmt.Errorf("cpu%d stays offline: %w", c, err))...)
		}
		n++
	}
	return n, errors.Join(errs...)
}

// online brings a CPU back, retrying: a CPU_ON the hypervisor answers
// late is not a CPU to give up on.
func (r *Resetter) online(f string) error {
	var err error
	for i := 0; i < 5; i++ {
		if err = r.write(f, "1"); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
	}
	return err
}

// ParseList reads a kernel CPU list ("0-3,6,8-9").
func ParseList(s string) ([]int, error) {
	var out []int
	if s == "" {
		return nil, nil
	}
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return nil, fmt.Errorf("cpu list %q: %w", s, err)
		}
		bnd := a
		if isRange {
			if bnd, err = strconv.Atoi(hi); err != nil || bnd < a {
				return nil, fmt.Errorf("cpu list %q: bad range %q", s, part)
			}
		}
		for c := a; c <= bnd; c++ {
			out = append(out, c)
		}
	}
	sort.Ints(out)
	return out, nil
}
