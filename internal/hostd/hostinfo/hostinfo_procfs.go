//go:build !darwin

package hostinfo

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// BootID returns the current boot's id: a different value at hostd's next
// start means the host restarted in between (DECISIONS I-645).
func BootID() (string, error) { return bootIDFrom(BootIDPath) }

// BootTime returns when the current boot began, from the btime line of
// /proc/stat.
func BootTime() (time.Time, error) { return bootTimeFrom("/proc/stat") }

// MemInfo returns MemTotal and MemAvailable in bytes from /proc/meminfo.
func MemInfo() (total, avail uint64, err error) {
	return memInfoFrom("/proc/meminfo")
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
