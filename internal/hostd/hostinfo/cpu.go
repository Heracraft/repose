package hostinfo

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The burst controller's three cgroup operations (DECISIONS I-1126): the
// host's own CPU time, one guest unit's CPU time, and the cap it may use.
// They are here rather than in internal/hostd/guest because every other
// read of /sys and /proc hostd does is here, and because a controller that
// takes them as function fields can be tested against a directory tree
// instead of a host.

// CPUPeriodUSec is the CFS period the cap is written against: systemd's
// own default, so a cap hostd writes and a CPUQuota systemd wrote are the
// same shape and a daemon reload that re-asserts the unit's property puts
// the guest back at its base rather than somewhere unreadable. The period
// is what bounds how long a throttled guest stalls, and the figure to
// shorten it is one of the measurements the gate is still waiting for
// (proposals/2026-11-10-burst-cores.md §6.2 c).
const CPUPeriodUSec = 100_000

// CgroupCPUUsage returns the cumulative CPU time, in microseconds, of a
// cgroup as systemd's ControlGroup property names it
// ("/guests.slice/guest@g-x.service"), from its cpu.stat usage_usec. The
// root cgroup is "/", whose figure is every process on the host: guests,
// virtiofsd, host builds, WireGuard and hostd. That is the quantity the
// typing latency depends on, so it is the one the gate is set against.
func CgroupCPUUsage(cgroup string) (uint64, error) {
	return cgroupCPUUsageFrom(cgroupRoot, cgroup)
}

func cgroupCPUUsageFrom(root, cgroup string) (uint64, error) {
	dir, err := cgroupDir(root, cgroup)
	if err != nil {
		return 0, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	return cpuStatUsage(b)
}

// cpuStatUsage parses the usage_usec line of a cgroup v2 cpu.stat.
func cpuStatUsage(data []byte) (uint64, error) {
	for _, line := range bytes.Split(data, []byte("\n")) {
		f := bytes.Fields(line)
		if len(f) == 2 && string(f[0]) == "usage_usec" {
			return strconv.ParseUint(string(f[1]), 10, 64)
		}
	}
	return 0, fmt.Errorf("hostinfo: no usage_usec in cpu.stat")
}

// SetCgroupCPUMax writes a cgroup's cpu.max: the CPU time, in
// microseconds, its tasks may use in each CPUPeriodUSec. millicores is
// thousandths of one core, so 2000 is two cores.
//
// The cap is written straight to the cgroup rather than set as a runtime
// CPUQuota over systemd's D-Bus, and the reason is which way each fails.
// A direct write is undone by a daemon reload, which re-asserts the unit's
// own CPUQuota and puts the guest back at the base its class sold: the
// failure is a guest that loses its burst. A runtime property that systemd
// keeps fails the other way -- a hostd that dies with a cap raised leaves
// the host oversubscribed until something notices -- and the thing this
// design must never do is leave the host above the gate. hostd writes
// every running guest's base at its start, before the first tick, so a
// crash costs at most that gap (DECISIONS I-1126).
func SetCgroupCPUMax(cgroup string, millicores uint64) error {
	return setCgroupCPUMaxIn(cgroupRoot, cgroup, millicores)
}

func setCgroupCPUMaxIn(root, cgroup string, millicores uint64) error {
	dir, err := cgroupDir(root, cgroup)
	if err != nil {
		return err
	}
	quota := millicores * CPUPeriodUSec / 1000
	v := fmt.Sprintf("%d %d", quota, CPUPeriodUSec)
	return os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(v), 0o644)
}

// cgroupDir joins a cgroup path under the hierarchy root, refusing
// anything that is not an absolute path inside it.
func cgroupDir(root, cgroup string) (string, error) {
	if !strings.HasPrefix(cgroup, "/") || strings.Contains(cgroup, "..") {
		return "", fmt.Errorf("hostinfo: cgroup %q is not an absolute path", cgroup)
	}
	return filepath.Join(root, cgroup), nil
}
