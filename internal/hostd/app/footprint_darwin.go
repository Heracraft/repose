package app

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/launchd"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// vmFootprint is a machine's VM's phys_footprint on a Mac: the memory its
// VZ service holds, which is about what a save writes (DECISIONS I-1666).
// The VZ service is an XPC process whose responsible process is the
// machine's repose-vm, which is its own launchd job.
func vmFootprint(r shell.Runner, ld *launchd.Launchd) func(ctx context.Context, id string) (uint64, bool) {
	return func(ctx context.Context, id string) (uint64, bool) {
		props, err := ld.Show(ctx, guest.GuestUnit(id), "MainPID")
		if err != nil || props["MainPID"] == "" || props["MainPID"] == "0" {
			return 0, false
		}
		res, err := r.Run(ctx, "/usr/bin/pgrep", "-f", "com.apple.Virtualization.VirtualMachine")
		if err != nil {
			return 0, false
		}
		for _, p := range strings.Fields(string(res.Stdout)) {
			info, err := r.Run(ctx, "/bin/launchctl", "procinfo", p)
			if err != nil || responsiblePID(info.Stdout) != props["MainPID"] {
				continue
			}
			fp, err := r.Run(ctx, "/usr/bin/footprint", "-p", p)
			if err != nil {
				return 0, false
			}
			return physFootprint(fp.Stdout)
		}
		return 0, false
	}
}

var responsibleRe = regexp.MustCompile(`(?m)responsible pid = (\d+)`)

func responsiblePID(out []byte) string {
	if m := responsibleRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

var footprintRe = regexp.MustCompile(`phys_footprint:\s+([\d.]+)\s*([KMGT]?B)`)

// physFootprint reads footprint(1)'s "phys_footprint: 632 MB".
func physFootprint(out []byte) (uint64, bool) {
	m := footprintRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(m[1]), 64)
	if err != nil {
		return 0, false
	}
	mul := map[string]float64{"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40}[string(m[2])]
	return uint64(v * mul), true
}
