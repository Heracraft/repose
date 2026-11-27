package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
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
	u := macUserOf(m)
	return platform{
		noHostFiles: true,
		noSlice:     true,
		prepare:     prepareMacNet,
		volumes: func(r shell.Runner) lvm.LVM {
			return &apfsvol.Volumes{Dir: m.VolumesDir, R: r, Mke2fs: m.Mke2fs, E2fsck: m.E2fsck, Chown: u.chown}
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
			return adaptMac(ctx, o, m, u, r, log, cfg, d)
		},
		// Parked machines held for the user's unlocked session (I-1666)
		// are restored when it comes.
		loop: func(ctx context.Context, mgr *guest.Manager, log *slog.Logger) {
			macMgr.Store(mgr)
			// The control socket is the user's, so `repose host restart`
			// parks without sudo (hostd, root, still connects).
			go func() {
				for i := 0; i < 100 && ctx.Err() == nil; i++ {
					if _, err := os.Stat(o.ControlSock); err == nil {
						if err := u.chown(o.ControlSock); err != nil {
							log.Warn("control socket not given to the Mac user", "event", "control", "err", err.Error())
						}
						return
					}
					time.Sleep(100 * time.Millisecond)
				}
			}()
			// Every second: the session's lock is followed as it
			// changes (a restore still works for a few seconds after the
			// lock, never later: lockWatch); held parks are tried every
			// third tick.
			t := time.NewTicker(time.Second)
			defer t.Stop()
			var swept time.Time
			var lw lockWatch
			awake := newKeepAwake()
			defer awake.set(false, log)
			for tick := 0; ; tick++ {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
				awake.set(mgr.AnyRunning(), log)
				if r, ok := macSession.Load().(*sessionReader); ok && r != nil {
					lw.step(ctx, r.read(ctx), time.Now(), mgr, log)
				}
				if tick%3 != 0 {
					continue
				}
				if time.Since(swept) > 10*time.Minute && o.SnapshotDir != "" {
					swept = time.Now()
					mgr.SweepKeptClones(ctx, newestPerProject(storedSnapshots(o.SnapshotDir), keptPerProject))
				}
				mgr.ExpireRestSaves(ctx)
				if _, err := mgr.RestoreHeld(ctx); err != nil {
					log.Warn("held parked machines not restored", "event", "guest_unpark", "err", err.Error())
				}
			}
		},
	}
}

// macUser is who the VMs run as: --mac-user under a root hostd (a
// LaunchDaemon), else hostd's own user.
type macUser struct {
	name     string
	uid, gid int
	err      error
}

func macUserOf(m MacOptions) macUser {
	if m.User == "" {
		u, err := user.Current()
		if err != nil {
			return macUser{err: err}
		}
		return macUser{name: u.Username, uid: os.Getuid(), gid: os.Getgid()}
	}
	uid, gid, err := guest.LookupUser(m.User)
	return macUser{name: m.User, uid: uid, gid: gid, err: err}
}

// chown gives a file hostd made to the user; nothing to do when hostd is
// that user.
func (u macUser) chown(p string) error {
	if os.Getuid() == u.uid {
		return nil
	}
	return os.Chown(p, u.uid, u.gid)
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
		m.NetMTU = 65520 // I-1668: 27-38 Gbit/s to the Mac against 4.9 at 1500
	}
	return m
}

func adaptMac(ctx context.Context, o Options, m MacOptions, u macUser, r shell.Runner, log *slog.Logger, cfg *guest.Config, d *guest.Deps) error {
	if _, err := os.Stat(m.Launcher); err != nil {
		return fmt.Errorf("hostd: the VM launcher: %w", err)
	}
	// Every VM runs as the user, never as root: a VM saved as root dies
	// (I-1662). hostd itself may be root (a LaunchDaemon, I-1664): then
	// --mac-user names the user, and with nobody logged in a VM starts in
	// the system domain as that user, without a park.
	if u.err != nil {
		return fmt.Errorf("hostd: the Mac user: %w", u.err)
	}
	if u.uid == 0 {
		return errors.New("hostd: VMs never run as root on a Mac; run hostd as the user, or as root with --mac-user (DECISIONS I-1662, I-1664)")
	}
	uid, gid := u.uid, u.gid
	if os.Getuid() != uid {
		// A root hostd: every VM runs as the user and opens its volume,
		// its log and its sockets under these directories, so the user
		// must be able to pass through them. hostd's own state, volume and
		// switch directories are made 0711 (every file in them stays 0600,
		// root's or the user's own); a VM whose
		// volume sat under a 0700 one died at once with "Permission
		// denied" (lab Mac, 2026-11-27).
		for _, d := range []string{o.StateDir, m.VolumesDir, m.NetDir} {
			if d == "" {
				continue
			}
			if err := os.MkdirAll(d, 0o700); err != nil {
				return fmt.Errorf("hostd: %s: %w", d, err)
			}
			if err := os.Chmod(d, 0o711); err != nil {
				return fmt.Errorf("hostd: %s: %w", d, err)
			}
		}
		for _, d := range []string{cfg.GuestsDir, m.VolumesDir, o.ImagesDir, m.NetDir} {
			if blocked := unreachable(d, uid, gid); blocked != "" {
				return fmt.Errorf("hostd: %s, where the machines' files are, is out of reach of the user %s: %s does not let them pass (it needs o+x, or the user or its group as owner)", d, u.name, blocked)
			}
		}
	}
	cfg.Lookup = func(string) (int, int, error) { return uid, gid, nil }
	cfg.GuestUser, cfg.VirtiofsUser = u.name, u.name
	// The login share is a directory VZ shares into each guest of the
	// user: every file the guest sees is its own uid, mode kept, so no
	// uid mapping and no virtiofsd (I-1666).
	d.DirectAuthShare = true
	cfg.AuthUser = u.name
	if cfg.UsersDir == "" {
		cfg.UsersDir = filepath.Join(o.StateDir, "users")
	}
	// VZ's balloon has no free page reporting (proposal section 2.5).
	cfg.FreePageReporting = false
	// macOS itself, its compressor and the user's apps keep more than
	// the 512 MiB a Linux owned host keeps (OwnedReserveBytes).
	cfg.HostReserveBytes = macReserveBytes
	cfg.OwnedGuestMemMiB = macGuestMemMiB(m.GuestMemMiB, cfg.TotalMemBytes)
	// The machines' memory together stays within the Mac's less the
	// reserve (times --mac-memory-commit-pct): VZ keeps what a guest
	// touches, and three 12 GiB machines swapped a 24 GB Mac (I-1666).
	if cfg.TotalMemBytes > cfg.HostReserveBytes && m.MemoryCommitPct > 0 {
		cfg.CommitMemMiB = (cfg.TotalMemBytes - cfg.HostReserveBytes) >> 20 * uint64(m.MemoryCommitPct) / 100
	}
	// A VM is one process: one that ends under a running machine is
	// booted again (I-1664).
	d.RestartOnExit = true
	// Half the Mac's cores a machine by default (--owned-vcpus to change
	// it): one machine builds Go in 29.3-29.6 s at 5 vCPUs on the 10-core
	// M4 against ~25 s at 10, and two fit the cores (I-1666).
	cfg.HostVCPUs = macGuestVCPUs(o.OwnedVCPUs, uint32(cfg.Cores))
	// The machines' vCPUs together stay near the Mac's cores: thirty on
	// ten made three Go builds take 503-519 s, twelve took 55-58 s, no
	// swap either way (I-1666). Each start gets its vCPUs or what is left
	// of the budget, at least two.
	if m.VCPUCommit > 0 {
		cfg.CommitVCPUs = uint32(m.VCPUCommit)
	} else {
		cfg.CommitVCPUs = uint32(cfg.Cores) + 2
	}
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
		// The guest's eth0 takes the NIC's MTU from its kernel line
		// (I-1668: 65520 is what moves the file-handle NIC from 4 to
		// tens of Gbit/s; segments are cut by the stack, not the wire).
		cfg.GuestMTU = m.NetMTU
	} else {
		log.Warn("no switch: each VM is on VZ's NAT, without isolation or egress rules (lab only)", "event", "start", "part", "network")
	}
	if m.NetDir != "" && os.Getuid() != u.uid {
		// A root hostd binds each machine's port socket; the launcher,
		// the user's, must be able to send to it.
		net = ownedPorts{Net: net, dir: m.NetDir, u: u}
	}
	d.Net = net
	d.Mount = macMount{r: r}
	if os.Getuid() == 0 {
		// Attaching a disk image at the share needs root (I-1666).
		d.BoundAuthShare = boundShare(r)
	}
	session := func(ctx context.Context) vzpark.Session {
		s, err := vzpark.ReadSession(ctx, r, uid)
		if err != nil {
			// Unreadable is taken as present: the start or restore then
			// tries, as before this check existed.
			return vzpark.Session{LoggedIn: true, OnConsole: true}
		}
		return s
	}
	ld := &launchd.Launchd{
		R: r, UID: uid, Dir: m.LaunchdDir, Launcher: m.Launcher,
		HasSession: func(ctx context.Context) bool { return session(ctx).LoggedIn },
		ChownLog:   u.chown,
		// launchd opens a job's log as the user, and a root hostd's
		// launchd directory is under its 0700 state directory: a VM's log
		// goes in its guest directory, which the VM already reaches.
		LogFor: func(unit string) string {
			if id, ok := strings.CutPrefix(unit, "guest@"); ok && id != "" && !strings.ContainsAny(id, "/.") {
				return filepath.Join(cfg.GuestsDir, id, "vm.log")
			}
			return ""
		},
		ExitTimeOut: 60 * time.Second,
		ExtraArgs: func(unit string, _ []string) []string {
			// The volume's VZ caching and synchronization modes (I-1666).
			args := []string{"--disk-cache", m.DiskCache, "--disk-sync", m.DiskSync}
			if m.NetDir == "" {
				return args
			}
			return append(args, "--net-dir", m.NetDir, "--net-mtu", fmt.Sprint(m.NetMTU))
		},
	}
	if os.Getuid() == 0 {
		ld.UserName = u.name
	}
	d.Systemd = ld
	d.Park.CanRestore = func(ctx context.Context) bool { return session(ctx).Unlocked() }
	// A park image is the guest's touched memory: asked of the guest
	// first, since after a restore the VM's footprint is its whole
	// configured memory (ENG-263); the footprint when the guest does not
	// answer.
	d.Park.GuestTouched = true
	d.Park.ImageEstimate = vmFootprint(r, ld)
	// An idle machine rests by save, giving all its memory back to
	// macOS, wherever the save can be read back at the wake: an agent job
	// in an unlocked session (DECISIONS I-1682, the owner's ruling on
	// ENG-266: idle machines give memory back). The lock is handled where
	// it happens: the moment the screen locks, every machine resting as a
	// save is restored and rests by pause through the lock (loop below,
	// Manager.UnsaveRests), and a while after the unlock it is saved again.
	// --mac-rest-by-save, off by default: the owner's call on the secrets
	// promise (conductor 03:50Z); without it a rest is a pause.
	if m.RestBySave {
		d.RestBySave = func(ctx context.Context, id string) bool {
			return ld.Domain(ctx, guest.GuestUnit(id)) == "gui" && session(ctx).Unlocked()
		}
	}
	d.SessionState = func(ctx context.Context) string {
		switch s := session(ctx); {
		case !s.LoggedIn:
			return guest.SessionNone
		case !s.Unlocked():
			return guest.SessionLocked
		}
		return guest.SessionUnlocked
	}
	macSession.Store(&sessionReader{read: session})
	// A restart through repose parks every machine, then restarts the
	// Mac (I-1690).
	d.RestartHost = func(ctx context.Context) error {
		return restartMac(ctx, r, os.Getuid() == 0, session(ctx).LoggedIn, uid, u.name)
	}
	d.Park.CanPark = func(ctx context.Context, id string) (bool, string) {
		// A save while the screen is locked fails, and it can end the VM:
		// "The virtual machine stopped unexpectedly" on the lab Mac's
		// `repose host restart`, its memory lost (2026-11-27, I-1682).
		if !session(ctx).Unlocked() {
			return false, guest.SkipLocked
		}
		if ld.Domain(ctx, guest.GuestUnit(id)) == launchd.SystemDomain {
			return false, "started with nobody logged in"
		}
		return true, ""
	}
	// e2fsprogs from --mac-mke2fs's directory, zstd and dd from PATH; no
	// systemd-run sandbox on macOS.
	d.Stream = &snapshot.Pipeline{R: r, ReplayJournal: true, GrowToSnapshot: true, AllCores: true}
	// VZ cannot resize an attached disk (I-1666).
	d.FileVolumes = true
	// Each snapshot's frozen clone is kept beside its stream, and a
	// restore of it is a clone (I-1666).
	d.SnapshotClones = true
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

// macMgr is the running manager, for MacWake.
var macMgr atomic.Pointer[guest.Manager]

// MacWake wakes the resting machine behind a switch port and waits for
// it, up to a minute: the userspace switch's OnWake (d2-net) calls it when
// a connection arrives for a VM that rests as a save file.
func MacWake(tap string) {
	m := macMgr.Load()
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, _ = m.WakeTap(ctx, tap) // a failed wake is logged and counted by the manager
}

// ownedPorts gives each switch port socket a root hostd binds to the
// user its VM runs as (DECISIONS I-1664): a 0755 root socket refuses the
// launcher's datagrams, which left every NIC dead (lab Mac, 16:06Z).
type ownedPorts struct {
	hnet.Net
	dir string
	u   macUser
}

// The switch binds a port the first time any of these names it (a start,
// or a reconcile under a running VM): each hands its socket to the user.
func (p ownedPorts) AddTap(ctx context.Context, tap string) error {
	if err := p.Net.AddTap(ctx, tap); err != nil {
		return err
	}
	return p.own(tap)
}

func (p ownedPorts) AddGuestRules(ctx context.Context, guestID, ip, mac, tap string) error {
	if err := p.Net.AddGuestRules(ctx, guestID, ip, mac, tap); err != nil {
		return err
	}
	return p.own(tap)
}

func (p ownedPorts) Shape(ctx context.Context, tap string, up, down int) error {
	if err := p.Net.Shape(ctx, tap, up, down); err != nil {
		return err
	}
	return p.own(tap)
}

func (p ownedPorts) own(tap string) error {
	err := p.u.chown(filepath.Join(p.dir, tap+".sock"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Detach passes the rest-by-save's detach through to the switch.
func (p ownedPorts) Detach(tap string) {
	if d, ok := p.Net.(interface{ Detach(tap string) }); ok {
		d.Detach(tap)
	}
}

// macGuestMemMiB is each machine's configured memory: the flag when set,
// else half the Mac's memory up to 8 GiB: restore (every wake from a rest
// by save, every unpark) 1.70 s at 8 GiB against 2.08 at 12, and three
// Go builds at once peaked at 3.4-4.9 GB a 12 GiB machine, so 8 holds
// them (DECISIONS I-1666). Measured on the M4 (24 GiB),
// with 1 GiB touched: 12 GiB costs 0.56 s more per wake (restore 2.07 s
// against 1.52 s at 6 GiB, n=5 each), 1.5 s more per save and about 126 MB
// more held idle, and doubles what one machine can use (DECISIONS I-1666).
func macGuestMemMiB(flag, totalBytes uint64) uint64 {
	if flag > 0 {
		return flag
	}
	half := totalBytes / 2 >> 20
	return min(half-half%128, 8192)
}

// storedSnapshots lists the store paths a local snapshot directory holds
// (snapshot.FileBlob's layout: Dir/<path>).
func storedSnapshots(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".img.zst") {
			return nil
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

// keptPerProject is how many of a project's snapshots keep their APFS
// clone: a kept clone costs every block its volume has changed since, and
// thirty-nine of them took the lab Mac's disk with the store (d2-lab,
// 2026-11-26). Older snapshots restore from their stream (I-1666).
const keptPerProject = 3

// newestPerProject keeps the newest n store paths of each directory (a
// project's snapshots, named by time so they sort by it).
func newestPerProject(paths []string, n int) []string {
	by := map[string][]string{}
	for _, p := range paths {
		d := path.Dir(p)
		by[d] = append(by[d], p)
	}
	var out []string
	for _, ps := range by {
		sort.Strings(ps)
		if len(ps) > n {
			ps = ps[len(ps)-n:]
		}
		out = append(out, ps...)
	}
	return out
}

// macGuestVCPUs is each machine's vCPUs: the flag when set, else half the
// Mac's cores, at least two.
func macGuestVCPUs(flag uint, cores uint32) uint32 {
	if flag > 0 {
		return uint32(flag)
	}
	return max(cores/2, 2)
}

// unreachable is the first directory on the way to path (path included,
// where it exists) that uid, whose group is gid, cannot pass through; ""
// when every one lets it, or path is empty. A directory not made yet is
// hostd's to make, and is not checked.
func unreachable(path string, uid, gid int) string {
	if path == "" {
		return ""
	}
	p := filepath.Clean(path)
	for {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			if !canPass(st, uid, gid) {
				return p
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
}
