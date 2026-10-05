// Package hostinfo collects what Register reports about a host: hostname,
// Azure SKU from IMDS (reachable from the host, blocked from guests),
// memory, vCPUs, the running NixOS system, the Cloud Hypervisor version
// and the thin pool size.
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
	info := &hostdv1.HostInfo{Vcpus: uint32(runtime.NumCPU())}
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
