package app

import (
	"context"
	"log/slog"

	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// platform is what a host substrate other than Linux's changes in Run
// (design 2, DECISIONS I-1664). The zero value is a Linux host: every hook
// nil, so Run builds exactly the Linux facilities it always has.
type platform struct {
	// noHostFiles skips the Linux host files (hostd.env and the units
	// beside it), which a Mac does not have.
	noHostFiles bool
	// noSlice skips the owned host's guests.slice limits (systemd).
	noSlice bool
	// volumes replaces the LVM thin pool.
	volumes func(r shell.Runner) lvm.LVM
	// park replaces the park's key and image volume.
	park func(o Options, log *slog.Logger) (guest.ParkConfig, guest.ParkDeps)
	// adapt replaces the rest of the host facilities in the manager's
	// config and deps, after Run has filled them for Linux.
	adapt func(ctx context.Context, r shell.Runner, log *slog.Logger, cfg *guest.Config, d *guest.Deps) error
	// loop runs beside the manager for the life of hostd.
	loop func(ctx context.Context, mgr *guest.Manager, log *slog.Logger)
	// prepare runs before the identity is ensured, so registration can
	// travel over a network hostd itself brings up (the Mac's WireGuard
	// netstack, DECISIONS I-1670); it may set Options.APIDial.
	prepare func(ctx context.Context, o Options, log *slog.Logger) (Options, error)
}

// MacOptions are the flags of hostd on macOS (design 2, DECISIONS I-1664):
// where volumes and launchd jobs live, the VM launcher, the network
// switch's socket directory, and e2fsprogs, which macOS does not ship.
type MacOptions struct {
	VolumesDir string
	LaunchdDir string
	Launcher   string
	NetDir     string
	NetMTU     int
	Mke2fs     string
	E2fsck     string
	// User is who every VM runs as when hostd is root (a LaunchDaemon);
	// empty is hostd's own user.
	User string
	// GuestMemMiB caps each guest's memory below the host's placeable
	// memory; 0 is no cap (DECISIONS I-1666).
	GuestMemMiB uint64
	// DiskCache and DiskSync are each volume's VZ caching mode
	// (automatic, cached, uncached) and synchronization mode (full,
	// fsync, none), passed to repose-vm (DECISIONS I-1666).
	DiskCache string
	DiskSync  string
	// MemoryCommitPct is how much of the Mac's memory, less the reserve,
	// the machines may be started with together, in percent; 0 is no
	// bound (DECISIONS I-1666).
	MemoryCommitPct uint
	// VCPUCommit is the vCPUs the machines may be started with together;
	// 0 is the Mac's cores plus two (DECISIONS I-1666).
	VCPUCommit uint
	// RestBySave lets an idle machine rest as a save file, giving its
	// memory back, instead of by pause (DECISIONS I-1682). On by default
	// (the owner's ruling, ENG-273): the save is encrypted by VZ, removed
	// once read and after 6 hours, as the restart image is.
	RestBySave bool
}
