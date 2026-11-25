package hostinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
