package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/heracraft/repose/internal/hostd/apfsvol"
	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/launchd"
	"github.com/heracraft/repose/internal/hostd/lvm"
	hnet "github.com/heracraft/repose/internal/hostd/net"
	"github.com/heracraft/repose/internal/hostd/shell"
	"github.com/heracraft/repose/internal/hostd/snapshot"
	"github.com/heracraft/repose/internal/hostd/vzpark"
)

// NewMacNet builds the Mac's userspace network (hnet.Net on gVisor's
// netstack, d2-net's internal/hostd/unet). Nil, or an empty --mac-net-dir,
// leaves each VM on VZ's own NAT with no rules: a lab mode for the runtime
// alone, never a customer's host.
var NewMacNet func(ctx context.Context, o Options, log *slog.Logger) (hnet.Net, error)

// platformFor is hostd on macOS (design 2, DECISIONS I-1664): volumes are
// APFS files, units are launchd jobs in the user's session, each VM is
// repose-vm, and the park is VZ's own save file.
func platformFor(o Options) platform {
	m := macDefaults(o)
	return platform{
		noHostFiles: true,
		noSlice:     true,
		volumes: func(r shell.Runner) lvm.LVM {
			return &apfsvol.Volumes{Dir: m.VolumesDir, R: r, Mke2fs: m.Mke2fs, E2fsck: m.E2fsck}
		},
		park: func(o Options, log *slog.Logger) (guest.ParkConfig, guest.ParkDeps) {
			cfg := guest.ParkConfig{
				Enabled: o.Park.Enabled, RecordDir: filepath.Join(o.StateDir, "freeze"),
				Deadline: o.Park.Deadline, WriteRate: o.Park.WriteRateMBs * 1_000_000,
				RestoreTimeout: o.Park.RestoreTimeout,
			}
			return cfg, vzpark.Deps()
		},
		adapt: func(ctx context.Context, r shell.Runner, log *slog.Logger, cfg *guest.Config, d *guest.Deps) error {
			return adaptMac(ctx, o, m, r, log, cfg, d)
		},
		// A restore needs the user's session unlocked (I-1662): hostd
		// waits for it rather than let a parked machine start cold.
		beforeReconcile: func(ctx context.Context, r shell.Runner, log *slog.Logger) {
			vzpark.WaitUnlocked(ctx, r, os.Getuid(), filepath.Join(o.StateDir, "freeze"), 2*time.Second, log)
		},
	}
}

// macDefaults fills the Mac flags left empty from the state directory.
func macDefaults(o Options) MacOptions {
	m := o.Mac
	if m.VolumesDir == "" {
		m.VolumesDir = filepath.Join(o.StateDir, "volumes")
	}
	if m.LaunchdDir == "" {
		m.LaunchdDir = filepath.Join(o.StateDir, "launchd")
	}
	if m.Launcher == "" {
		if exe, err := os.Executable(); err == nil {
			m.Launcher = filepath.Join(filepath.Dir(exe), "repose-vm")
		}
	}
	if m.NetMTU == 0 {
		m.NetMTU = 1500
	}
	return m
}

func adaptMac(ctx context.Context, o Options, m MacOptions, r shell.Runner, log *slog.Logger, cfg *guest.Config, d *guest.Deps) error {
	if _, err := os.Stat(m.Launcher); err != nil {
		return fmt.Errorf("hostd: the VM launcher: %w", err)
	}
	// Every unit runs as hostd's own user, in its GUI session: VZ's save
	// file is sealed with that session's Secure Enclave key (I-1662).
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		return errors.New("hostd: on macOS hostd runs as the logged-in user, not root (a VM saved as root dies, DECISIONS I-1662)")
	}
	cfg.Lookup = func(string) (int, int, error) { return uid, gid, nil }
	// The login share is a VZ shared directory on a Mac, not virtiofsd;
	// not built yet.
	cfg.NoAuthShare = true
	// VZ's balloon has no free page reporting (proposal section 2.5).
	cfg.FreePageReporting = false
	// macOS itself, its compressor and the user's apps keep more than
	// the 512 MiB a Linux owned host keeps (OwnedReserveBytes).
	cfg.HostReserveBytes = macReserveBytes
	cfg.OwnedGuestMemMiB = m.GuestMemMiB
	cfg.HostVCPUs = ownedVCPUs(o.OwnedVCPUs, uint32(cfg.Cores), false)
	cfg.OwnedMaxVCPUs = ownedBound(o.OwnedMaxVCPUs, false, cfg.Cores)

	var net hnet.Net = noNet{}
	if m.NetDir != "" {
		if NewMacNet == nil {
			return errors.New("hostd: --mac-net-dir is set but this build has no userspace network")
		}
		n, err := NewMacNet(ctx, o, log)
		if err != nil {
			return fmt.Errorf("hostd: network: %w", err)
		}
		net = n
	} else {
		log.Warn("no switch: each VM is on VZ's NAT, without isolation or egress rules (lab only)", "event", "start", "part", "network")
	}
	d.Net = net
	d.Mount = noMount{}
	d.Systemd = &launchd.Launchd{
		R: r, UID: uid, Dir: m.LaunchdDir, Launcher: m.Launcher,
		ExitTimeOut: 60 * time.Second,
		ExtraArgs: func(unit string, _ []string) []string {
			if m.NetDir == "" {
				return nil
			}
			return []string{"--net-dir", m.NetDir, "--net-mtu", fmt.Sprint(m.NetMTU)}
		},
	}
	// e2fsprogs from --mac-mke2fs's directory, zstd and dd from PATH; no
	// systemd-run sandbox on macOS.
	d.Stream = &snapshot.Pipeline{R: r, ReplayJournal: true}
	vols := m.VolumesDir
	d.StoreStat = func() (uint64, uint64, error) {
		size, free, err := (&apfsvol.Volumes{Dir: vols}).PoolStats(context.Background())
		return size, size - free, err
	}
	d.CgroupCPUPressure, d.CgroupCPUUsage, d.SetCgroupCPUMax = nil, nil, nil
	d.GuestsSliceMemory, d.GuestMemory, d.SliceOOMKills, d.SetOOMScoreAdj = nil, nil, nil, nil
	d.SystemStopping = nil
	d.AfterHypervisorExit = nil
	if m.Mke2fs != "" {
		// dumpe2fs and e2fsck by name for the snapshot pipeline.
		if dir := filepath.Dir(m.Mke2fs); dir != "." {
			_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}
	return nil
}

// macReserveBytes is what an owned Mac keeps for macOS (item 8 of the
// proposal measures the right figure; this is the skeleton's).
const macReserveBytes = 4 << 30

// noNet is the network of a lab host with no switch: nothing to add, count
// or shape.
type noNet struct{}

func (noNet) TapExists(context.Context, string) (bool, error)                     { return true, nil }
func (noNet) AddTap(context.Context, string) error                                { return nil }
func (noNet) DelTap(context.Context, string) error                                { return nil }
func (noNet) AddGuestRules(context.Context, string, string, string, string) error { return nil }
func (noNet) DelGuestRules(context.Context, string, string, string, string) error { return nil }
func (noNet) Shape(context.Context, string, int, int) error                       { return nil }
func (noNet) Unshape(context.Context, string) error                               { return nil }
func (noNet) CounterBytes(context.Context, string) (uint64, error)                { return 0, nil }
func (noNet) EgressBytes(context.Context) (map[string]uint64, error)              { return map[string]uint64{}, nil }
func (noNet) BlockedPackets(context.Context) (map[string]map[string]uint64, error) {
	return map[string]map[string]uint64{}, nil
}
func (noNet) DelCounter(context.Context, string) error   { return nil }
func (noNet) TapStats(string) (uint64, uint64, error)    { return 0, 0, nil }
func (noNet) ListTaps(context.Context) ([]string, error) { return nil, nil }
func (noNet) FlushNeigh(context.Context, string) error   { return nil }

// noMount: nothing on a Mac host mounts a guest's filesystem (the login
// share and the park image are directories).
type noMount struct{}

func (noMount) IsMounted(string) (bool, error) { return false, nil }
func (noMount) Mount(context.Context, string, string) error {
	return errors.New("mount: not on macOS")
}
func (noMount) MountType(context.Context, string, string, string, string) error {
	return errors.New("mount: not on macOS")
}
func (noMount) Unmount(context.Context, string) error { return nil }
