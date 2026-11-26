package hostinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// What an owned host's hostd reads and writes beyond the fleet's
// (DECISIONS I-1626): the memory the guests hold together, the CPUs the
// guests may have, and each hypervisor's place in the OOM killer's order.

// GuestsSlice is the cgroup every guest unit runs in.
const GuestsSlice = "guests.slice"

// GuestsSliceMemory returns guests.slice's memory.current in bytes: what
// every guest's hypervisor and virtiofsd hold together.
func GuestsSliceMemory() (uint64, error) {
	return cgroupMemoryFrom(cgroupRoot, GuestsSlice)
}

// GuestMemory returns a guest unit's cgroup memory.current in bytes
// (guest@<id>.service under guests.slice): what the memory guard reads to
// find the guest that grew (DECISIONS I-1650).
func GuestMemory(id string) (uint64, error) {
	return cgroupMemoryFrom(cgroupRoot, filepath.Join(GuestsSlice, "guest@"+id+".service"))
}

// SliceOOMKills returns guests.slice's memory.events oom_kill: the kernel's
// OOM kills of the guests' processes, counted where the dead guest's own
// cgroup cannot be read any more (DECISIONS I-1650).
func SliceOOMKills() (uint64, error) {
	return oomKillsFrom(cgroupRoot, GuestsSlice)
}

func oomKillsFrom(root, cgroup string) (uint64, error) {
	b, err := os.ReadFile(filepath.Join(root, cgroup, "memory.events"))
	if err != nil {
		return 0, err
	}
	for line := range strings.Lines(string(b)) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), " "); ok && k == "oom_kill" {
			return strconv.ParseUint(v, 10, 64)
		}
	}
	return 0, fmt.Errorf("%s/memory.events: no oom_kill", cgroup)
}

func cgroupMemoryFrom(root, cgroup string) (uint64, error) {
	b, err := os.ReadFile(filepath.Join(root, cgroup, "memory.current"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

// VCPUs is the CPUs this process may run on, which on a host is the CPUs
// its kernel has online: the most vCPUs a guest there may boot with.
func VCPUs() uint32 { return uint32(runtime.NumCPU()) }

// procRoot is /proc; a var so a test writes into a directory.
var procRoot = "/proc"

// SetOOMScoreAdj writes a process's oom_score_adj (-1000 to 1000).
func SetOOMScoreAdj(pid, adj int) error {
	if pid <= 0 {
		return fmt.Errorf("hostinfo: pid %d", pid)
	}
	if adj < -1000 || adj > 1000 {
		return fmt.Errorf("hostinfo: oom_score_adj %d outside -1000..1000", adj)
	}
	return os.WriteFile(filepath.Join(procRoot, strconv.Itoa(pid), "oom_score_adj"), []byte(strconv.Itoa(adj)+"\n"), 0o644)
}

// sysRoot is /sys; a var so a test writes into a directory.
var sysRoot = "/sys"

// UnderHypervisor reports whether this host's kernel itself runs in a
// virtual machine, so its guests are nested: the Mac appliance under
// Apple's Virtualization framework, or an x86 VM whose CPU flags name
// "hypervisor". A nested guest's vCPUs cost far more as their number
// grows (DECISIONS I-1640).
func UnderHypervisor() bool { return underHypervisorFrom(sysRoot, procRoot) }

func underHypervisorFrom(sys, proc string) bool {
	if b, err := os.ReadFile(filepath.Join(sys, "class", "dmi", "id", "product_name")); err == nil &&
		strings.HasPrefix(strings.TrimSpace(string(b)), "Apple Virtualization") {
		return true
	}
	b, err := os.ReadFile(filepath.Join(proc, "cpuinfo"))
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(b)) {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "flags" {
			if slices.Contains(strings.Fields(v), "hypervisor") {
				return true
			}
		}
	}
	return false
}
