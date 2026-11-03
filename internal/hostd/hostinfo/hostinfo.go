// Package hostinfo collects what Register reports about a host: hostname,
// Azure SKU from IMDS (reachable from the host, blocked from guests),
// memory, vCPUs, physical cores, the running NixOS system, the Cloud
// Hypervisor version and the thin pool size.
package hostinfo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/shell"
	"github.com/heracraft/repose/internal/psi"
)

// IMDSURL is Azure's instance metadata endpoint.
const IMDSURL = "http://169.254.169.254/metadata/instance/compute/vmSize?api-version=2021-02-01&format=text"

// cgroupRoot is where the unified cgroup hierarchy is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// CgroupCPUPressure returns the cpu.pressure "some" total, in microseconds,
// of a cgroup as systemd's ControlGroup property names it
// ("/system.slice/guest@g-x.service"): for a guest's hypervisor unit, the
// time its vCPU threads waited for a host CPU (DECISIONS I-493).
func CgroupCPUPressure(cgroup string) (uint64, error) {
	return cgroupCPUPressureFrom(cgroupRoot, cgroup)
}

func cgroupCPUPressureFrom(root, cgroup string) (uint64, error) {
	if !strings.HasPrefix(cgroup, "/") || strings.Contains(cgroup, "..") {
		return 0, fmt.Errorf("hostinfo: cgroup %q is not an absolute path", cgroup)
	}
	return psi.ReadSomeTotal(filepath.Join(root, cgroup, "cpu.pressure"))
}

// BootIDPath is the kernel's random id of the current boot.
const BootIDPath = "/proc/sys/kernel/random/boot_id"

// BootID returns the current boot's id: a different value at hostd's next
// start means the host restarted in between (DECISIONS I-645).
func BootID() (string, error) { return bootIDFrom(BootIDPath) }

func bootIDFrom(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", fmt.Errorf("hostinfo: %s is empty", path)
	}
	return id, nil
}

// TaintedPath is the kernel's own latched verdict on this host, and
// PanicOnOopsPath the sysctl that decides whether an oops is survivable
// (DECISIONS I-984, I-988).
const (
	TaintedPath     = "/proc/sys/kernel/tainted"
	PanicOnOopsPath = "/proc/sys/kernel/panic_on_oops"
)

// TaintReasons is the taint bits a host is watched for, as
// Documentation/admin-guide/tainted-kernels.rst numbers them. Not the
// whole word: bits 0, 12 and 13 (a proprietary, out-of-tree or unsigned
// module) are set on healthy hosts and never clear, so a host that reads
// the word as one number reads tainted for ever. The decomposition is
// here rather than in PromQL because PromQL has no bitwise AND.
var TaintReasons = []string{"die", "machine_check", "bad_page", "soft_lockup", "warn"}

// AlertingTaintReasons is the subset HostKernelTainted pages on: the
// hard-fault family, the one a kernel does not recover from. soft_lockup
// and warn are exported as series and alert on nothing -- a soft lockup is
// load, which HostCPUSaturated covers, and a kernel warning is a condition
// the kernel recovered from (the same division KernelOops draws, I-980).
var AlertingTaintReasons = []string{"die", "machine_check", "bad_page"}

// taintBits is each reason's bit in the kernel's taint word.
var taintBits = map[string]uint64{
	"machine_check": 1 << 4,  // M, a machine check exception
	"bad_page":      1 << 5,  // B, a bad page was found
	"die":           1 << 7,  // D, the kernel has oopsed or hit a BUG
	"warn":          1 << 9,  // W, a kernel warning
	"soft_lockup":   1 << 14, // L, a soft lockup
}

// Tainted reports, per reason in TaintReasons, whether the kernel has
// latched that taint on this boot.
//
// On any failure it returns every reason true along with the error, and
// that is the contract, not a convenience: a caller that pre-creates the
// gauges at 0 and skips the Set on error publishes green for ever on a
// host whose taint word it could never read -- exactly the host the guard
// exists for. So the fail-closed value is produced here, where it cannot
// be forgotten, and the gauge means "this taint is latched, or cannot be
// ruled out" (DECISIONS I-1010).
func Tainted() (map[string]bool, error) { return taintedFrom(TaintedPath) }

func taintedFrom(path string) (map[string]bool, error) {
	unknown := func() map[string]bool {
		out := make(map[string]bool, len(TaintReasons))
		for _, r := range TaintReasons {
			out[r] = true
		}
		return out
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return unknown(), err
	}
	word, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return unknown(), fmt.Errorf("hostinfo: %s is not a taint word: %w", path, err)
	}
	out := make(map[string]bool, len(TaintReasons))
	for _, r := range TaintReasons {
		out[r] = word&taintBits[r] != 0
	}
	return out, nil
}

// PanicOnOops reports whether kernel.panic_on_oops is on, so an oops
// reboots the host instead of leaving it serving tenants with a kernel it
// cannot trust. A failed read reports false, which is the value
// HostPanicOnOopsOff warns on, for the same reason Tainted fails closed.
func PanicOnOops() (bool, error) { return panicOnOopsFrom(PanicOnOopsPath) }

func panicOnOopsFrom(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return false, fmt.Errorf("hostinfo: %s is not a number: %w", path, err)
	}
	return v != 0, nil
}

// BootTime returns when the current boot began, from the btime line of
// /proc/stat.
func BootTime() (time.Time, error) { return bootTimeFrom("/proc/stat") }

func bootTimeFrom(path string) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = f.Close() }() // read only
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[0] == "btime" {
			s, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(s, 0).UTC(), nil
		}
	}
	if err := sc.Err(); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, fmt.Errorf("hostinfo: no btime in %s", path)
}

// MemInfo returns MemTotal and MemAvailable in bytes from /proc/meminfo.
func MemInfo() (total, avail uint64, err error) {
	return memInfoFrom("/proc/meminfo")
}

func memInfoFrom(path string) (uint64, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }() // read only
	var total, avail uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = kb << 10
		case "MemAvailable:":
			avail = kb << 10
		}
	}
	return total, avail, sc.Err()
}

// StoreStat returns the size and used bytes of the filesystem holding
// /nix/store.
func StoreStat() (total, used uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs("/nix/store", &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize)
	total = st.Blocks * bs
	used = (st.Blocks - st.Bfree) * bs
	return total, used, nil
}

// CPUTopologyRoot is where the kernel publishes each online CPU's
// topology.
const CPUTopologyRoot = "/sys/devices/system/cpu"

// PhysicalCores returns the host's physical cores: the number of distinct
// (physical_package_id, core_id) pairs across the online CPUs.
// runtime.NumCPU() counts SMT threads, twice that on every host repose
// runs on, and placement counts committed guest vCPUs against cores
// (DECISIONS I-735). A host whose topology cannot be read reports the
// thread count, which is the number this field held before it existed:
// 0 would read as "unknown" and leave the host unbounded.
func PhysicalCores() uint32 { return physicalCoresFrom(CPUTopologyRoot) }

func physicalCoresFrom(root string) uint32 {
	dirs, err := filepath.Glob(filepath.Join(root, "cpu[0-9]*"))
	if err != nil {
		return uint32(runtime.NumCPU())
	}
	type core struct{ pkg, id string }
	seen := make(map[core]struct{}, len(dirs))
	for _, d := range dirs {
		pkg, err := os.ReadFile(filepath.Join(d, "topology", "physical_package_id"))
		if err != nil {
			continue // an offline or hotplugged CPU publishes no topology
		}
		id, err := os.ReadFile(filepath.Join(d, "topology", "core_id"))
		if err != nil {
			continue
		}
		seen[core{strings.TrimSpace(string(pkg)), strings.TrimSpace(string(id))}] = struct{}{}
	}
	if len(seen) == 0 {
		return uint32(runtime.NumCPU())
	}
	return uint32(len(seen))
}

// Load1 reads the one-minute load average.
func Load1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// SKU asks IMDS for the VM size with a short timeout; empty when absent.
func SKU(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, IMDSURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Metadata", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }() // body read below
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Collect builds the HostInfo message.
func Collect(ctx context.Context, r shell.Runner, l lvm.LVM) *hostdv1.HostInfo {
	info := &hostdv1.HostInfo{Vcpus: uint32(runtime.NumCPU()), Cores: PhysicalCores()}
	info.Hostname, _ = os.Hostname() // an unreadable hostname is reported as empty
	info.MemBytes, _, _ = MemInfo()  // same
	info.Sku = SKU(ctx)
	if sys, err := os.Readlink("/run/current-system"); err == nil {
		info.NixosSystem = sys
	}
	if res, err := r.Run(ctx, "cloud-hypervisor", "--version"); err == nil {
		info.ChVersion = strings.TrimSpace(string(res.Stdout))
	}
	if l != nil {
		if size, _, err := l.PoolStats(ctx); err == nil {
			info.PoolBytes = size
		}
	}
	return info
}
