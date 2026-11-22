package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/baseimg"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/storeview"
	"github.com/heracraft/repose/internal/hostd/virtiofs"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

// Create steps, numbered as in docs/workstreams/03-hostd.md §5.5 so a
// failure names the step and --fail-at-step can target it.
const (
	stepValidate  = 1
	stepAllocate  = 2
	stepVolume    = 3
	stepGCRoot    = 4
	stepRunner    = 5
	stepNetwork   = 6
	stepSecrets   = 7
	stepVirtiofsd = 8
	stepHypervisr = 9
	stepReady     = 10
)

var stepNames = map[int]string{
	stepValidate: "validate", stepAllocate: "allocate", stepVolume: "volume", stepGCRoot: "gcroot",
	stepRunner: "runner", stepNetwork: "network", stepSecrets: "secrets", stepVirtiofsd: "virtiofsd",
	stepHypervisr: "cloud-hypervisor", stepReady: "ready",
}

func stepErr(step int, err error) *Error {
	if e, ok := err.(*Error); ok && e.Code != CodeInternal {
		return e
	}
	if errors.Is(err, lvm.ErrPoolUnavailable) {
		return errf(CodeVolumeUnavailable, "create: step %d (%s) failed: %s", step, stepNames[step], err.Error())
	}
	code := CodeInternal
	if step == stepReady {
		code = CodeGuestUnresponsive
	}
	return errf(code, "create: step %d (%s) failed: %s", step, stepNames[step], err.Error())
}

func (m *Manager) injected(step int) error {
	if m.cfg.FailAtStep == step {
		return fmt.Errorf("injected failure at step %d", step)
	}
	return nil
}

func marshalGuest(g *state.Guest) ([]byte, error) {
	return json.MarshalIndent(g, "", "  ")
}

// capacityCheck applies the host-level refusals shared by Create and
// Restore: draining, pool nearly full, the pool's allocation budget,
// memory for the class.
func (m *Manager) capacityCheck(guestID, class string, volumeBytes uint64, needMem bool) *Error {
	if m.Draining() {
		return errf(CodeInsufficientCapacity, "host draining")
	}
	c, ok := Classes[class]
	if !ok {
		return errf(CodeInvalidArgument, "class %q must be tiny, small, large or xl", class)
	}
	if volumeBytes < m.cfg.MinVolumeBytes || volumeBytes > m.cfg.MaxVolumeBytes {
		return errf(CodeInvalidArgument, "volume_bytes %d outside %d..%d", volumeBytes, m.cfg.MinVolumeBytes, m.cfg.MaxVolumeBytes)
	}
	if err := m.poolRoom(); err != nil {
		return err
	}
	if needMem && m.FreeMemBytes() < (c.MemMiB+OverheadMiB)<<20 {
		return errf(CodeInsufficientCapacity, "not enough free memory for a %s guest", class)
	}
	return nil
}

// poolRoom refuses an operation that would put more data in the thin
// pool (a create, a restore, growing a volume) once the pool is
// PoolRefusePct used (DECISIONS I-586). Volumes are counted by what they
// hold, not their sizes (I-585, which replaced I-449's bound on the sum
// of the sizes), so the pool's real use is the measure.
func (m *Manager) poolRoom() *Error {
	pct, err := m.poolUsedPct()
	if err != nil {
		return lvmErr("thin pool", err)
	}
	if pct >= m.cfg.PoolRefusePct {
		return errf(CodeInsufficientCapacity, "thin pool %.0f%% full", pct)
	}
	return nil
}

func (m *Manager) create(ctx context.Context, c *hostdv1.CreateGuest) (*hostdv1.CreateResult, *Error) {
	// Step 1: validate.
	if err := m.injected(stepValidate); err != nil {
		return nil, stepErr(stepValidate, err)
	}
	if c.GuestId == "" || c.ProjectId == "" {
		return nil, errf(CodeInvalidArgument, "guest_id and project_id required")
	}
	if _, err := hexPrefix(c.GuestId); err != nil {
		return nil, err.(*Error)
	}
	existing, gerr := m.getGuest(c.GuestId)
	if gerr != nil && gerr.Code != CodeNotFound {
		return nil, gerr
	}
	if existing != nil {
		switch existing.State {
		case StateRunning:
			return &hostdv1.CreateResult{GuestIp: existing.IP, VsockCid: existing.CID}, nil
		case StateCreating, StateError:
			// A re-run after a crash or a failed attempt: continue with the
			// same record; every step below tolerates what already exists.
		case StateStopped:
			// A create a host shutdown cut off before Ready: the next boot's
			// reconcile made it stopped and the api resends it (I-650). A
			// guest that ever reached Ready has a last good closure and is
			// refused below, as is another project's create.
			if existing.LastGoodClosure != "" || existing.ProjectID != c.ProjectId {
				return nil, errf(CodeAlreadyExists, "guest %s exists in state %s", c.GuestId, existing.State)
			}
		default:
			return nil, errf(CodeAlreadyExists, "guest %s exists in state %s", c.GuestId, existing.State)
		}
	}
	// An erofs guest (base_image) or a Nix guest (system_closure), never
	// both (DECISIONS I-1458).
	erofs := c.BaseImage != nil
	switch {
	case erofs && c.SystemClosure != "":
		return nil, errf(CodeInvalidArgument, "base_image and system_closure are exclusive")
	case !erofs && c.SystemClosure == "":
		return nil, errf(CodeInvalidArgument, "system_closure or base_image required")
	case !erofs && !m.NixOn():
		return nil, m.nixUnsupported("create")
	}
	if existing != nil && (existing.BaseVersion != "") != erofs {
		return nil, errf(CodeAlreadyExists, "guest %s exists as another kind of guest", c.GuestId)
	}
	if err := validateSecretNames(c.Secrets); err != nil {
		return nil, err
	}
	var base baseimg.Spec
	if erofs {
		var perr *Error
		if base, perr = parseBase(c.BaseImage); perr != nil {
			return nil, perr
		}
		if m.d.Images == nil {
			return nil, errf(CodeInvalidArgument, "this host boots no erofs guests (no --images-dir)")
		}
	} else if ok, err := m.d.Nix.PathExists(ctx, c.SystemClosure); err != nil {
		return nil, errf(CodeInternal, "nix path-info: %v", err)
	} else if !ok {
		return nil, errf(CodeNotFound, "system closure %s is not in the host store", c.SystemClosure)
	}
	if err := m.capacityCheck(c.GuestId, c.Class, c.VolumeBytes, existing == nil || existing.State != StateCreating); err != nil {
		return nil, err
	}
	// The bundle before anything is allocated: a fetch that fails leaves
	// no record, no address and no volume (I-1482).
	if erofs {
		if err := m.ensureBase(ctx, base); err != nil {
			return nil, err
		}
	}

	// Step 2: allocate address and record.
	if err := m.injected(stepAllocate); err != nil {
		return nil, stepErr(stepAllocate, err)
	}
	idx, err := m.d.State.AllocIndex(c.GuestId, m.maxIndex)
	if err != nil {
		return nil, errf(CodeInsufficientCapacity, "%v", err)
	}
	tap, _ := TapName(c.GuestId)
	mac, _ := MACAddr(c.GuestId)
	// A tap or MAC another guest on this host already holds is refused
	// rather than shared (DECISIONS I-120); a replay of this same create
	// finds its own record and passes.
	if others, err := m.d.State.ListGuests(); err == nil {
		for _, o := range others {
			if o.GuestID != c.GuestId && (o.Tap == tap || o.MAC == mac) {
				return nil, errf(CodeAlreadyExists, "tap %s or mac %s already belongs to guest %s", tap, mac, o.GuestID)
			}
		}
	}
	g := &state.Guest{
		GuestID: c.GuestId, ProjectID: c.ProjectId, UserID: c.UserId, ProjectSlug: c.ProjectSlug, RemoteURL: c.RemoteUrl,
		Class: c.Class, VolumeBytes: c.VolumeBytes, SystemClosure: c.SystemClosure,
		IP: m.ipForIndex(idx), MAC: mac, Tap: tap, CID: cidForIndex(idx), IPIndex: idx,
		Env: c.Env, Principals: c.Principals, SSHCAPub: c.SshCaPub, HooksConfig: c.HooksConfig, ProjectJSON: c.ProjectJson,
	}
	if erofs {
		g.BaseVersion = base.Version
		recordMenu(g, c.Menu)
	}
	if existing != nil {
		g.CreatedAt = existing.CreatedAt
	}
	if err := m.setState(g, StateCreating, ""); err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	m.log(g).Info("creating guest", "event", "guest_create", "class", g.Class, "volume_bytes", g.VolumeBytes)

	// Step 3: volume. Never rolled back.
	if err := m.injected(stepVolume); err != nil {
		return nil, m.fail(g, stepVolume, err)
	}
	volStart := m.d.Now()
	if err := m.d.LVM.CreateVolume(ctx, VolumeName(g.GuestID), g.VolumeBytes); err != nil {
		return nil, m.fail(g, stepVolume, err)
	}
	// An erofs guest's volume is vdb, ext4 labelled repose-rw; its
	// initramfs lays it out on the first boot (I-1480).
	mkfs := m.d.LVM.Mkfs
	if erofs {
		mkfs = m.d.LVM.MkfsRW
	}
	if err := mkfs(ctx, VolumeName(g.GuestID)); err != nil {
		return nil, m.fail(g, stepVolume, err)
	}
	volume := m.d.Now().Sub(volStart)

	// Step 4: GC root. An erofs guest has no closure to root.
	if !erofs {
		if err := m.injected(stepGCRoot); err != nil {
			return nil, m.fail(g, stepGCRoot, err)
		}
		if err := m.d.Roots.Set(g.GuestID, g.SystemClosure); err != nil {
			return nil, m.fail(g, stepGCRoot, err)
		}
	}

	// Step 7 comes before the boot so secrets are ready when Ready arrives.
	if err := m.injected(stepSecrets); err != nil {
		return nil, m.fail(g, stepSecrets, err)
	}
	m.cacheTriggers(g.GuestID, c.Triggers)
	if err := m.cacheSecrets(g.GuestID, c.Secrets, c.SshCaPub, c.HostKey, c.HostCert); err != nil {
		return nil, m.fail(g, stepSecrets, err)
	}

	if err := m.boot(ctx, g, stepRunner, "create", volume); err != nil {
		return nil, err
	}
	return &hostdv1.CreateResult{GuestIp: g.IP, VsockCid: g.CID, Boot: m.lastBoot(g.GuestID), MenuError: m.lastMenuErr(g.GuestID)}, nil
}

// fail records a create or start failure, rolling back everything but the
// volume, the address and the record.
func (m *Manager) fail(g *state.Guest, step int, err error) *Error {
	if m.HostStopping(context.Background()) {
		// Leave the record as it is (starting, or creating): the guest's
		// units are going down with the host, and the next boot's
		// reconcile settles it (I-650). No error is written.
		m.log(g).Warn("boot interrupted by the host shutdown", "event", "guest_interrupted", "step", step)
		return interrupted("create: step " + stepNames[step])
	}
	e := stepErr(step, err)
	m.teardown(context.Background(), g)
	m.log(g).Error("guest failed", "event", "guest_state", "step", step, "reason", e.Message)
	_ = m.setState(g, StateError, e.Message) // the error being reported is e; a state write failure is logged inside setState's caller path
	return e
}

// boot runs create steps 5, 6, 8, 9 and 10 for a guest whose volume, root
// and secrets are in place. Start reuses it. op ("create" or "start") and
// volume (the create's volume step) go into the "boot timings" line a boot
// that reaches running logs (DECISIONS I-707).
func (m *Manager) boot(ctx context.Context, g *state.Guest, firstStep int, op string, volume time.Duration) *Error {
	bootStart := m.d.Now()
	m.setMenuErr(g.GuestID, nil)
	class := Classes[g.Class]
	dir := m.guestDir(g.GuestID)
	if err := m.prepareGuestDir(dir); err != nil {
		return m.fail(g, firstStep, err)
	}

	// Step 5: render the hypervisor invocation from the closure.
	if err := m.injected(stepRunner); err != nil {
		return m.fail(g, stepRunner, err)
	}
	erofs := isErofs(g)
	spec := ch.Spec{
		GuestDir: dir,
		IP:       g.IP, Gateway: m.gateway(), Netmask: m.netmask(), Tap: g.Tap, MAC: g.MAC, CID: g.CID,
		VolumeDev: m.d.LVM.DevPath(VolumeName(g.GuestID)), VCPUs: m.bootVCPUs(class), MemMiB: class.MemMiB, StoreTag: m.cfg.StoreTag,
		DiskIOPS: class.DiskIOPS, DiskBytesPerSec: class.DiskMBps * 1_000_000,
		Hostname:          ch.Hostname(g.ProjectSlug),
		FreePageReporting: m.cfg.FreePageReporting,
	}
	if erofs {
		// The bundle from the image cache: vda the rootfs.erofs, the
		// kernel and initramfs beside it (I-1480).
		b, err := m.bundleFor(g)
		if err != nil {
			return m.fail(g, stepRunner, err)
		}
		spec.Kernel, spec.Initrd, spec.RootImage = b.Kernel, b.Initrd, b.Rootfs
	} else {
		if !m.NixOn() {
			return m.fail(g, stepRunner, m.nixUnsupported("boot"))
		}
		info, err := nixbuild.ClosureInfo(g.SystemClosure)
		if err != nil {
			return m.fail(g, stepRunner, err)
		}
		spec.Kernel, spec.Initrd, spec.Init, spec.KernelParams, spec.StoreDisk = info.Kernel, info.Initrd, info.Init, info.KernelParams, info.StoreDisk
	}
	argv := spec.Args()
	if err := os.WriteFile(filepath.Join(dir, "ch.args"), []byte(strings.Join(argv, "\n")+"\n"), 0o640); err != nil {
		return m.fail(g, stepRunner, err)
	}
	g.Kernel, g.Initrd = spec.Kernel, spec.Initrd

	// Step 6: network.
	if err := m.injected(stepNetwork); err != nil {
		return m.fail(g, stepNetwork, err)
	}
	if err := m.d.Net.AddTap(ctx, g.Tap); err != nil {
		return m.fail(g, stepNetwork, err)
	}
	m.resetCursor(g.GuestID)
	if err := m.d.Net.AddGuestRules(ctx, g.GuestID, g.IP, g.MAC, g.Tap); err != nil {
		return m.fail(g, stepNetwork, err)
	}
	m.baselineEgress(ctx, g.GuestID)
	if err := m.d.Net.Shape(ctx, g.Tap, m.cfg.EgressMbit, m.cfg.DownloadMbit); err != nil {
		return m.fail(g, stepNetwork, err)
	}

	// Step 8: virtiofsd.
	if err := m.injected(stepVirtiofsd); err != nil {
		return m.fail(g, stepVirtiofsd, err)
	}
	// An erofs guest has no store share and no store view (I-1480).
	if !erofs {
		vcfg := virtiofs.Config{SharedDir: m.cfg.StoreExport, User: m.cfg.VirtiofsUser, Group: m.cfg.VirtiofsUser, SocketGroup: m.cfg.GuestUser, Binary: m.cfg.VirtiofsBinary}
		if err := virtiofs.Start(ctx, m.d.Systemd, vcfg, g.GuestID, ch.VirtiofsSocket(dir)); err != nil {
			return m.fail(g, stepVirtiofsd, err)
		}
		if err := m.waitSocket(ctx, virtiofs.Unit(g.GuestID), ch.VirtiofsSocket(dir)); err != nil {
			return m.fail(g, stepVirtiofsd, err)
		}
		if err := m.extendView(ctx, g.GuestID, m.viewClosures(g)); err != nil {
			return m.fail(g, stepVirtiofsd, err)
		}
	}
	// The user's Claude login share (DECISIONS I-278), attached only when
	// its virtiofsd is up; the hypervisor would otherwise wait on its socket.
	if m.startAuthShare(ctx, g, dir) {
		spec.AuthTag = m.cfg.AuthTag
		argv = spec.Args()
		if err := os.WriteFile(filepath.Join(dir, "ch.args"), []byte(strings.Join(argv, "\n")+"\n"), 0o640); err != nil {
			return m.fail(g, stepVirtiofsd, err)
		}
	}

	// Step 9: Cloud Hypervisor.
	if err := m.injected(stepHypervisr); err != nil {
		return m.fail(g, stepHypervisr, err)
	}
	if err := m.setState(g, StateStarting, ""); err != nil {
		return m.fail(g, stepHypervisr, err)
	}
	props := GuestUnitProps(class, m.cfg.GuestUser, m.cfg.GuestsDir, dir, spec.VolumeDev, m.unitBundle(g))
	// This boot's console starts here in the log, which earlier boots
	// wrote too (I-592).
	consoleFrom := m.consoleSize(g.GuestID)
	if err := m.d.Systemd.Run(ctx, GuestUnit(g.GuestID), props, argv); err != nil {
		return m.fail(g, stepHypervisr, err)
	}
	chStart := m.d.Now()
	// The registration RegisterPaths sends is read while the guest boots,
	// not after its guestd answers: `nix-store -qR` and `--dump-db` took
	// 70 ms of every start's critical path on host-01 (DECISIONS I-232).
	dump := make(chan dumpResult, 1)
	if erofs {
		// Nothing to register: the erofs guest's packages are its own.
		dump <- dumpResult{}
	} else {
		go func(closure string) {
			b, err := m.d.Nix.DumpDB(ctx, closure)
			dump <- dumpResult{b, err}
		}(g.SystemClosure)
	}

	// Step 10: wait for guestd, deliver, mark running.
	if err := m.injected(stepReady); err != nil {
		return m.fail(g, stepReady, err)
	}
	mon := m.startMonitor(g)
	sess, err := mon.waitReady(ctx, m.cfg.ReadyTimeout)
	if err != nil {
		// The console is read before capture ends; what a boot that
		// failed early printed is in the log long before the wait ends.
		rf := m.readyFailure(g, consoleFrom, err)
		mon.stop()
		m.removeMonitor(g.GuestID)
		return m.fail(g, stepReady, rf)
	}
	readyAt := m.d.Now()
	dt, err := m.deliver(ctx, g, sess, dump)
	if err != nil {
		mon.stop()
		m.removeMonitor(g.GuestID)
		// Where the time went, on a boot that failed as much as on one
		// that reached running. Without this line nobody could say which
		// phase a failed start had run out of, which is how nine starts
		// on a Mac appliance produced two error sentences and no
		// evidence (ENG-80).
		m.log(g).Warn("boot timings", "event", "guest_start", "op", op, "result", "failed", "phase", dt.phase,
			"volume_ms", ms(volume), "prepare_ms", ms(chStart.Sub(bootStart)), "ready_ms", ms(readyAt.Sub(chStart)),
			"identity_ms", ms(dt.identity), "paths_ms", ms(dt.paths), "setup_ms", ms(dt.setup),
			"limit_ms", ms(dt.limit), "total_ms", ms(volume+m.d.Now().Sub(bootStart)))
		return m.fail(g, stepReady, m.deliverFailure(g, consoleFrom, dt, err))
	}
	// The closure reached Ready and took its delivery: it is what a later
	// boot that fails falls back to (I-590). setState writes the record.
	// An erofs guest has no closure; its menu is replayed instead, after
	// SetupProject, and a replay that fails leaves the guest running with
	// the failure in the result (I-1482).
	if erofs {
		g.BaseNeedsFetch = false
		m.setMenuErr(g.GuestID, m.replayMenu(ctx, g, sess))
	} else {
		m.markGood(g, g.SystemClosure)
	}
	if err := m.setState(g, StateRunning, ""); err != nil {
		return m.fail(g, stepReady, err)
	}
	done := m.d.Now()
	// SetupMs is the set-up phase as deliver measured it, not what is
	// left of the delivery after the registration: since I-1408 the ssh
	// identity is delivered first, and the rest of this window is the
	// record write.
	bt := &hostdv1.BootTimings{
		VolumeMs: ms(volume), PrepareMs: ms(chStart.Sub(bootStart)), ReadyMs: ms(readyAt.Sub(chStart)),
		PathsMs: ms(dt.paths), SetupMs: ms(dt.setup), TotalMs: ms(volume + done.Sub(bootStart)),
	}
	m.mu.Lock()
	m.boots[g.GuestID] = bt
	m.mu.Unlock()
	m.log(g).Info("guest running", "event", "guest_start", "class", g.Class)
	// Where the boot's time went, as "stop timings" says for a stop
	// (DECISIONS I-707); the same numbers go back in the result.
	m.log(g).Info("boot timings", "event", "guest_start", "op", op, "result", "running", "identity_ms", ms(dt.identity),
		"volume_ms", bt.VolumeMs, "prepare_ms", bt.PrepareMs,
		"ready_ms", bt.ReadyMs, "paths_ms", bt.PathsMs, "setup_ms", bt.SetupMs, "total_ms", bt.TotalMs)
	return nil
}

// lastBoot is the timings of id's last boot that reached running, or nil.
func (m *Manager) lastBoot(id string) *hostdv1.BootTimings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.boots[id]
}

// ms is d in whole milliseconds, 0 for a negative d.
func ms(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	return uint32(d.Milliseconds())
}

// dumpResult is a `nix-store --dump-db` read ahead of Ready.
type dumpResult struct {
	b   []byte
	err error
}

// The phases of a delivery, as the boot timings line and the error name
// them. A start that runs out of time runs out in one of these, and until
// ENG-80 nothing said which: the api turned every one of them into the
// same sentence, and a failed boot logged no timings at all, so five
// failed starts on a Mac appliance could not be told apart from each
// other or from a guest that had hung (DECISIONS I-1408).
const (
	phaseIdentity = "identity"
	phasePaths    = "paths"
	phaseSetup    = "setup"
)

// deliverTimings is where a delivery's time went and, when it failed,
// which phase failed and what deadline that phase had.
type deliverTimings struct {
	identity, paths, setup time.Duration
	phase                  string
	limit                  time.Duration
}

// deliver sends the machine's ssh identity, the closure's registration,
// the trigger set and the project setup after Ready. dump delivers the
// closure's registration, read while the guest booted. It returns where
// the time went, and on a failure which phase failed and the deadline it
// had.
//
// The ssh identity goes first, ahead of the registration, which is the
// slowest call of a start (2 min 6 s on the Mac appliance, against 16 s
// with the store on a disk). The user CA and the principals file both
// live on a tmpfs and sshd recreates them empty on every boot, so until
// this order a start that ran out of time in either later phase left a
// machine that trusted nobody: "Permission denied (publickey)" for the
// user, at the one moment they most need to get in and read the logs
// (ENG-80, DECISIONS I-1408). Both calls are idempotent -- the api sends
// the same material on every start -- and neither needs the nix database
// the registration loads, which SetupProject's home-manager does.
func (m *Manager) deliver(ctx context.Context, g *state.Guest, sess vsockclient.Session, dump <-chan dumpResult) (deliverTimings, error) {
	var t deliverTimings
	start := m.d.Now()
	if secrets := m.cachedSecrets(g.GuestID); len(secrets) > 0 {
		if err := sess.WriteSecrets(ctx, secrets); err != nil {
			t.phase, t.limit = phaseIdentity, vsockclient.DefaultTimeout
			return t, fmt.Errorf("WriteSecrets: %w", err)
		}
	}
	if len(g.Principals) > 0 {
		if err := sess.SetPrincipals(ctx, g.Principals); err != nil {
			t.phase, t.limit = phaseIdentity, vsockclient.DefaultTimeout
			return t, fmt.Errorf("SetPrincipals: %w", err)
		}
	}
	t.identity = m.d.Now().Sub(start)

	// The booted closure is on disk in the guest but unknown to its nix
	// database until registered (DECISIONS I-67).
	pathsStart := m.d.Now()
	var d dumpResult
	select {
	case d = <-dump:
	case <-ctx.Done():
		t.phase, t.limit = phasePaths, m.cfg.PathsTimeout
		return t, ctx.Err()
	}
	reg, err := d.b, d.err
	if err != nil {
		t.phase = phasePaths
		return t, fmt.Errorf("nix-store --dump-db: %w", err)
	}
	if len(reg) > 0 {
		if err := sess.RegisterPaths(ctx, reg); err != nil {
			t.paths = m.d.Now().Sub(pathsStart)
			t.phase, t.limit = phasePaths, m.cfg.PathsTimeout
			return t, fmt.Errorf("RegisterPaths: %w", err)
		}
	}
	t.paths = m.d.Now().Sub(pathsStart)

	setupStart := m.d.Now()
	// The trigger set, before SetupProject starts the session: a
	// delivery that arrives on a machine whose receiver has no set is a
	// 404 the provider reads as "no such route" (I-1248).
	if set := m.cachedTriggers(g.GuestID); len(set) > 0 {
		if err := sess.SetTriggers(ctx, &guestdv1.SetTriggers{Triggers: set}); err != nil {
			t.setup = m.d.Now().Sub(setupStart)
			t.phase, t.limit = phaseSetup, vsockclient.DefaultTimeout
			return t, fmt.Errorf("SetTriggers: %w", err)
		}
	}
	slug := g.ProjectSlug
	if slug == "" {
		slug = g.ProjectID
	}
	sp := &guestdv1.SetupProject{ProjectSlug: slug, RemoteUrl: g.RemoteURL, Tz: g.Env["TZ"], Lang: g.Env["LANG"], ProjectJson: g.ProjectJSON}
	if err := sess.SetupProject(ctx, sp); err != nil {
		t.setup = m.d.Now().Sub(setupStart)
		t.phase, t.limit = phaseSetup, m.cfg.SetupTimeout
		return t, fmt.Errorf("SetupProject: %w", err)
	}
	t.setup = m.d.Now().Sub(setupStart)
	return t, nil
}

// teardown removes tap, tc, nft membership, the virtiofsd and guest units
// and console capture. The volume, GC root, counter and address remain.
// The tap's counters are read once more after the hypervisor is gone and
// before the tap is (finalSample).
func (m *Manager) teardown(ctx context.Context, g *state.Guest) {
	if mon := m.removeMonitor(g.GuestID); mon != nil {
		mon.stop()
	}
	_ = m.d.Systemd.Stop(ctx, GuestUnit(g.GuestID)) // best effort in reverse order; each step's absence is fine
	m.finalSample(g)
	_ = virtiofs.Stop(ctx, m.d.Systemd, g.GuestID)                // same
	_ = virtiofs.StopAuth(ctx, m.d.Systemd, g.GuestID)            // same
	_ = m.d.Net.Unshape(ctx, g.Tap)                               // same
	_ = m.d.Net.DelGuestRules(ctx, g.GuestID, g.IP, g.MAC, g.Tap) // same
	_ = m.d.Net.DelTap(ctx, g.Tap)                                // same
	for _, s := range []string{"ch.sock", "vsock.sock", "console.sock", filepath.Join("virtiofsd", "virtiofsd.sock"), filepath.Join("virtiofsd-auth", "virtiofsd.sock")} {
		_ = os.Remove(filepath.Join(m.guestDir(g.GuestID), s)) // stale sockets confuse the next boot only if left behind
	}
}

// extendView adds the closures' paths to the guest's store view (I-463):
// at boot, before the hypervisor starts, and before an in-place switch.
// closures[0] is the one the guest runs; when the others cannot be listed
// (a path half removed from the host store) the view holds that one alone.
func (m *Manager) extendView(ctx context.Context, guestID string, closures []string) error {
	if m.cfg.StoreExport != storeview.Dir {
		return nil
	}
	if m.d.View == nil {
		return errors.New("store view: not configured")
	}
	paths, err := m.d.Nix.Requisites(ctx, closures...)
	if err != nil && len(closures) > 1 {
		m.d.Log.Warn("store view: earlier closures left out", "event", "store_view", "guest_id", guestID, "err", err.Error())
		paths, err = m.d.Nix.Requisites(ctx, closures[0])
	}
	if err != nil {
		return fmt.Errorf("store view: %w", err)
	}
	if err := m.d.View.Populate(ctx, virtiofs.Unit(guestID), paths); err != nil {
		return fmt.Errorf("store view: %w", err)
	}
	return nil
}

// viewClosures is what a guest's view holds at boot: the closure it runs,
// then every closure it ran before that hostd recorded and the project's
// kept revisions, those still on the host. The guest's nix database lists
// the paths of every closure it was ever given as valid, and nix never
// fetches a valid path again, so a user's profile can point into an
// earlier closure (a package the user installed that the system already
// had). The whole-store export showed those paths for as long as the host
// kept them; the view shows the same ones of this guest's own.
func (m *Manager) viewClosures(g *state.Guest) []string {
	out := []string{g.SystemClosure}
	seen := map[string]bool{g.SystemClosure: true}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	for _, p := range g.PastClosures {
		add(p)
	}
	if es, err := m.d.Roots.List(); err == nil {
		for _, e := range es {
			if strings.HasPrefix(e.Name, "rev-"+g.ProjectID+"-") {
				add(e.Target)
			}
		}
	}
	return out
}

// waitSocket waits for a virtiofsd unit to bind its socket, failing early
// when the unit has already exited. Cloud Hypervisor would otherwise retry
// the connection for a full minute and the create would fail at step 10
// with "guest did not become ready", which names the wrong step.
func (m *Manager) waitSocket(ctx context.Context, unit, socket string) error {
	if m.cfg.VirtiofsSocketWait <= 0 {
		return nil
	}
	deadline := time.Now().Add(m.cfg.VirtiofsSocketWait)
	for {
		if _, err := os.Stat(socket); err == nil {
			return nil
		}
		active, err := m.d.Systemd.IsActive(ctx, unit)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("virtiofsd exited before creating its socket; see journalctl -u %s", unit)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("virtiofsd did not create %s within %s", filepath.Base(socket), m.cfg.VirtiofsSocketWait)
		}
		// virtiofsd binds its socket a few ms after it starts; a 100 ms
		// step made every start wait most of one (DECISIONS I-232). The
		// unit is asked about every tenth step only.
		for i := 0; i < 10; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
			if _, err := os.Stat(socket); err == nil {
				return nil
			}
		}
	}
}

// start boots a stopped guest. commandID ties a boot fallback (I-590) to
// the command that made it: a resend of that command after a hostd
// restart answers with the same fallback, whether the second boot had
// finished (the guest runs) or not (it is booted again on the closure
// the fallback chose, which the record already names).
func (m *Manager) start(ctx context.Context, commandID string, c *hostdv1.StartGuest) (*hostdv1.StartResult, *Error) {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	var resent *state.BootFallback
	if fb := g.BootFallback; fb != nil && fb.CommandID == commandID {
		resent = fb
	}
	switch g.State {
	case StateRunning:
		// A resting guest (I-818) is woken: a start is the api, or a
		// user, asking for the machine, and the answer must not pretend
		// a boot happened to a machine whose processes never stopped.
		if err := m.wakeIfResting(ctx, g, WakeReasonCommand); err != nil {
			return nil, err
		}
		// Nothing booted for this command, so it carries no timings, as
		// create's running case does not (I-707).
		return startResult(g, resent, "", nil), nil
	case StateStopped, StateError, StateStarting:
	default:
		return nil, errf(CodeInvalidArgument, "guest is %s; start needs stopped", g.State)
	}
	// Which system the start boots: the erofs guest's base (a new one
	// when base_image names another version, fetched before anything is
	// touched, I-1482), or the Nix guest's rooted closure.
	var newBase string
	if isErofs(g) {
		switch {
		case c.BaseImage != nil:
			base, perr := parseBase(c.BaseImage)
			if perr != nil {
				return nil, perr
			}
			if err := m.ensureBase(ctx, base); err != nil {
				return nil, err
			}
			if base.Version != g.BaseVersion {
				newBase = base.Version
			}
		case m.d.Images == nil || !m.d.Images.Has(g.BaseVersion):
			return nil, errf(CodeNotFound, "base image %s missing from the host; api must resend base_image", g.BaseVersion)
		}
	} else {
		if c.BaseImage != nil {
			return nil, errf(CodeInvalidArgument, "base_image for a Nix guest")
		}
		if !m.NixOn() {
			return nil, m.nixUnsupported("start")
		}
		if g.SystemClosure == "" {
			return nil, errf(CodeNotFound, "system closure missing; api must rebuild")
		}
		if ok, err := m.d.Roots.Exists(g.GuestID); err != nil {
			return nil, errf(CodeInternal, "gcroot: %v", err)
		} else if !ok {
			return nil, errf(CodeNotFound, "system closure missing; api must rebuild")
		}
	}
	// The pool first (DECISIONS I-644): a start on a host whose pool is
	// down is refused before anything is booted, so it can never reach
	// bootOrFallBack and adopt the last good closure for a disk problem,
	// and the project stays stopped.
	if ok, p := m.poolAvailable(ctx); !ok {
		return nil, errf(CodeVolumeUnavailable, "the host's guest pool is unavailable (%s)", p.Reason())
	}
	if ok, err := m.d.LVM.VolumeExists(ctx, VolumeName(g.GuestID)); err != nil {
		return nil, errf(CodeVolumeUnavailable, "lvs: %v", err)
	} else if !ok {
		return nil, errf(CodeNotFound, "volume for guest %s is missing", g.GuestID)
	}
	if err := m.ensureActive(ctx, g, VolumeName(g.GuestID)); err != nil {
		return nil, err
	}
	if !m.cfg.NoAuthShare && hasAuthShare(g) {
		auth := AuthVolumeName(g.UserID)
		if ok, err := m.d.LVM.VolumeExists(ctx, auth); err != nil {
			return nil, errf(CodeVolumeUnavailable, "lvs: %v", err)
		} else if ok {
			if err := m.ensureActive(ctx, g, auth); err != nil {
				return nil, err
			}
		}
	}
	// DECISIONS I-260: the api sends the project's class on every start,
	// so a class changed while the project was stopped takes effect here:
	// the hypervisor's vCPUs and memory, the unit's MemoryMax and the
	// class the samples (and so billing) report. The record is written by
	// boot's first state change; a start refused below leaves it as it was.
	if c.Class != "" && c.Class != g.Class {
		if _, ok := Classes[c.Class]; !ok {
			return nil, errf(CodeInvalidArgument, "class %q must be tiny, small, large or xl", c.Class)
		}
		m.log(g).Info("guest class changed", "event", "guest_start", "class", c.Class, "prev", g.Class)
		g.Class = c.Class
	}
	if m.FreeMemBytes() < (Classes[g.Class].MemMiB+OverheadMiB)<<20 {
		return nil, errf(CodeInsufficientCapacity, "not enough free memory for a %s guest", g.Class)
	}
	// A start adds no bytes, but a running guest writes: past
	// PoolStartRefusePct the pool is minutes from full, and one more
	// writer risks every guest on it (DECISIONS I-586).
	if pct, err := m.poolUsedPct(); err != nil {
		return nil, lvmErr("thin pool", err)
	} else if pct >= m.cfg.PoolStartRefusePct {
		return nil, errf(CodeInsufficientCapacity, "thin pool %.0f%% full", pct)
	}
	// DECISIONS I-26: StartGuest may carry the delivery fields so a host
	// that restarted still has the guest's secrets and sshd material.
	if len(c.Secrets) > 0 || len(c.HostKey) > 0 || len(c.HostCert) > 0 || c.SshCaPub != "" {
		m.cacheTriggers(g.GuestID, c.Triggers)
		if err := m.cacheSecrets(g.GuestID, c.Secrets, c.SshCaPub, c.HostKey, c.HostCert); err != nil {
			return nil, err
		}
	}
	if len(c.Principals) > 0 {
		g.Principals = c.Principals
	}
	if len(c.Env) > 0 {
		g.Env = c.Env
	}
	if len(c.HooksConfig) > 0 {
		g.HooksConfig = c.HooksConfig
	}
	if len(c.ProjectJson) > 0 {
		g.ProjectJSON = c.ProjectJson
	}
	if isErofs(g) {
		recordMenu(g, c.Menu)
		if newBase != "" {
			// The record names the new base from this boot's first state
			// write; the initramfs rotates the overlay's upper directory
			// when it sees the version on vdb differ, and the replay after
			// SetupProject installs the menu again (I-1482).
			m.log(g).Info("guest base changed", "event", "guest_start", "base_version", newBase, "prev", g.BaseVersion)
			g.BaseVersion = newBase
		}
	}
	// An UpdateSecrets since hostd restarted brings the named set but no
	// sshd material, so the host key is what says the api sent a delivery.
	if !m.cachedHostKey(g.GuestID) {
		m.log(g).Warn("starting without secrets; api sent none since hostd restarted", "event", "guest_start", "reason", "no_secrets")
	}
	if resent != nil {
		// The record already names the closure the fallback chose; boot
		// it, and answer with the fallback this command made.
		if err := m.boot(ctx, g, stepRunner, "start", 0); err != nil {
			return nil, err
		}
		r := startResult(g, resent, "", m.lastBoot(g.GuestID))
		r.MenuError = m.lastMenuErr(g.GuestID)
		return r, nil
	}
	// A fallback another command made is over once this one boots.
	g.BootFallback = nil
	fb, tail, err := m.bootOrFallBack(ctx, g, commandID, "start")
	if err != nil {
		return nil, err
	}
	r := startResult(g, fb, tail, m.lastBoot(g.GuestID))
	r.MenuError = m.lastMenuErr(g.GuestID)
	return r, nil
}

// --- monitor ----------------------------------------------------------

// monitor holds a running guest's guestd session, forwards its
// notifications, and watches its units.
type monitor struct {
	m      *Manager
	g      *state.Guest
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	sess    vsockclient.Session
	ready   chan struct{}
	readyOK bool
	lostAt  time.Time
	lost    bool
	// connected is whether this guest's guestd has ever answered. Before
	// it has, the guest is booting and not lost, however long that takes:
	// the boot's own ready window is what bounds it, and the start's
	// failure is what reports it. Counted from the monitor's start, the
	// lost window (60 s) is shorter than guestd's first answer on an
	// owned Mac (59.5 to 70 s), so every healthy boot there raised the
	// warning and bumped repose_host_guestd_lost (ENG-80, I-1408).
	connected bool
	stopCon   func()
	// wake is poked when the guest's resting mark is cleared, so the dial
	// the rest held back goes at once rather than on the next retry.
	wake chan struct{}

	// notifyBucket limits the guest's notifications; only run() touches it.
	notifyBucket bucket
	// opened holds the questions passed as open, so each one's close
	// passes without a token (I-690); only run() touches it.
	opened openQuestions
}

func (m *Manager) startMonitor(g *state.Guest) *monitor {
	m.mu.Lock()
	if old, ok := m.monitors[g.GuestID]; ok {
		m.mu.Unlock()
		return old
	}
	ctx, cancel := context.WithCancel(m.ctx)
	mon := &monitor{m: m, g: g, ctx: ctx, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), wake: make(chan struct{}, 1)}
	m.monitors[g.GuestID] = mon
	m.mu.Unlock()
	if m.d.ConsoleStart != nil {
		mon.stopCon = m.d.ConsoleStart(g.GuestID, m.guestDir(g.GuestID))
	}
	go mon.run()
	return mon
}

// pokeMonitor makes a guest's monitor dial now if it has no session: the
// resting mark its retries stood down for has been cleared by a wake.
func (m *Manager) pokeMonitor(id string) {
	m.mu.Lock()
	mon := m.monitors[id]
	m.mu.Unlock()
	if mon == nil {
		return
	}
	select {
	case mon.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) removeMonitor(id string) *monitor {
	m.mu.Lock()
	defer m.mu.Unlock()
	mon := m.monitors[id]
	delete(m.monitors, id)
	return mon
}

func (m *Manager) monitorOf(id string) *monitor {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.monitors[id]
}

// session returns the live guestd session for a running guest.
func (m *Manager) session(id string) (vsockclient.Session, *Error) {
	mon := m.monitorOf(id)
	if mon == nil {
		return nil, errf(CodeGuestUnresponsive, "guest %s has no guestd connection", id)
	}
	s := mon.current()
	if s == nil {
		return nil, errf(CodeGuestUnresponsive, "guestd unreachable for guest %s", id)
	}
	return s, nil
}

// awaitSession waits up to max for a fresh guestd session of guest id,
// polling the monitor; the monitor's own retry timer paces reconnects.
// gone is the session that just failed: until the monitor has noticed the
// drop it is still the current one, and handing it back made the retry
// fail on the same dead connection ("vsockrpc: EOF", the TestBuildAndApply
// flake in CI).
func (m *Manager) awaitSession(ctx context.Context, id string, max time.Duration, gone vsockclient.Session) (vsockclient.Session, *Error) {
	deadline := time.Now().Add(max)
	for {
		if s, err := m.session(id); err == nil && s != gone {
			return s, nil
		}
		if time.Now().After(deadline) {
			return nil, errf(CodeGuestUnresponsive, "guestd unreachable for guest %s", id)
		}
		select {
		case <-ctx.Done():
			return nil, errf(CodeGuestUnresponsive, "guestd unreachable for guest %s", id)
		case <-time.After(min(m.cfg.GuestdRetry/2, 100*time.Millisecond)):
		}
	}
}

func (mon *monitor) stop() {
	if stopCon := mon.stopKeepConsole(); stopCon != nil {
		stopCon()
	}
}

// stopKeepConsole ends the guestd watch but leaves console capture running,
// returning the function that ends it (nil when there is none): a stop
// ends capture only once the hypervisor has exited (I-186).
func (mon *monitor) stopKeepConsole() func() {
	mon.cancel()
	<-mon.done
	stopCon := mon.stopCon
	mon.stopCon = nil
	return stopCon
}

func (mon *monitor) current() vsockclient.Session {
	mon.mu.Lock()
	defer mon.mu.Unlock()
	return mon.sess
}

// markReady reports whether this was the guest's first Ready. A second
// one is a guestd that restarted under a running guest.
func (mon *monitor) markReady() bool {
	mon.mu.Lock()
	defer mon.mu.Unlock()
	if mon.readyOK {
		return false
	}
	mon.readyOK = true
	close(mon.ready)
	return true
}

// resendTriggers hands a restarted guestd the trigger set hostd holds.
// It is best effort: the guest's own copy on the volume is what makes a
// restart invisible, and this covers the set that changed while guestd
// was down.
func (m *Manager) resendTriggers(mon *monitor) {
	set := m.cachedTriggers(mon.g.GuestID)
	if len(set) == 0 {
		return
	}
	sess := mon.current()
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sess.SetTriggers(ctx, &guestdv1.SetTriggers{Triggers: set}); err != nil {
		m.log(mon.g).Warn("the restarted guestd did not take the trigger set",
			"event", "trigger_run", "err", err.Error())
	}
}

// waitReady blocks until guestd reports Ready or answers a Ping.
func (mon *monitor) waitReady(ctx context.Context, timeout time.Duration) (vsockclient.Session, error) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-mon.ready:
		if s := mon.current(); s != nil {
			return s, nil
		}
		return nil, fmt.Errorf("guestd connection dropped after ready")
	case <-t.C:
		return nil, fmt.Errorf("no Ready from guestd within %s", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (mon *monitor) run() {
	defer close(mon.done)
	m := mon.m
	unitTick := time.NewTicker(m.cfg.UnitPoll)
	defer unitTick.Stop()
	target := vsockclient.Target{GuestID: mon.g.GuestID, Dir: m.guestDir(mon.g.GuestID), CID: mon.g.CID}
	var sess vsockclient.Session
	var notes <-chan *guestdv1.Notify
	var sessDone <-chan struct{}
	retry := time.NewTimer(0)
	defer retry.Stop()
	// Until the first session the guest is (most likely) booting: dial
	// often, since its guestd's first answer is on the start's path.
	everConnected := false
	mon.lostAt = m.d.Now()
	for {
		select {
		case <-mon.ctx.Done():
			if sess != nil {
				_ = sess.Close() // monitor ending; the guest keeps running
			}
			return
		case <-retry.C:
			if sess != nil {
				continue
			}
			// No dial while the guest rests (DECISIONS I-1228). A paused
			// VM does not refuse a connect: Cloud Hypervisor's vsock
			// device is paused with the vCPUs, so the connect and its
			// CONNECT line wait in vsock.sock's kernel backlog (4096 deep)
			// and are all delivered to guestd at the resume, each new
			// connection replacing the one before it. hostd restarted
			// under three resting guests on 2026-11-16 and dialled each
			// every 5 s for 2 h 22 min; at the wake guestd took about
			// 1,650 dead connections in one second and every wake's Ping
			// failed with EOF. The wake pokes the dial when it clears the
			// mark; until then the timer just turns.
			if m.restingMark(mon.g.GuestID) != nil {
				mon.lostAt = m.d.Now()
				retry.Reset(m.cfg.GuestdRetry)
				continue
			}
			dctx, cancel := context.WithTimeout(mon.ctx, 5*time.Second)
			s, err := m.d.Guestd.Dial(dctx, target)
			cancel()
			if err != nil {
				mon.checkLost()
				if everConnected {
					retry.Reset(m.cfg.GuestdRetry)
				} else {
					retry.Reset(m.cfg.GuestdBootRetry)
				}
				continue
			}
			everConnected = true
			sess, notes, sessDone = s, s.Notifications(), s.Done()
			mon.mu.Lock()
			mon.sess = s
			mon.connected = true
			mon.mu.Unlock()
			mon.regained()
			go func() {
				pctx, pcancel := context.WithTimeout(mon.ctx, 10*time.Second)
				defer pcancel()
				if _, err := s.Ping(pctx); err == nil {
					mon.markReady()
				}
			}()
		case n, ok := <-notes:
			if !ok {
				notes = nil
				continue
			}
			mon.handleNotify(n)
		case <-mon.wake:
			if sess == nil {
				retry.Reset(0)
			}
		case <-sessDone:
			mon.mu.Lock()
			mon.sess = nil
			mon.mu.Unlock()
			sess, notes, sessDone = nil, nil, nil
			mon.lostAt = m.d.Now()
			retry.Reset(m.cfg.GuestdRetry)
		case <-unitTick.C:
			mon.checkUnits()
		}
	}
}

func (mon *monitor) checkLost() {
	m := mon.m
	// A resting guest is paused, not lost (I-818). Its session normally
	// stays open, so this check is not reached for it at all; whether
	// Cloud Hypervisor closes the host side of vsock.sock on vm.pause is
	// not measured, so the mark is suppressed either way. Moving lostAt
	// forward also keeps a wake from being read as 60 s of silence.
	if m.restingMark(mon.g.GuestID) != nil {
		mon.lostAt = m.d.Now()
		return
	}
	if !mon.everConnected() {
		return
	}
	if !mon.lost && m.d.Now().Sub(mon.lostAt) >= m.cfg.GuestdLostAfter {
		mon.lost = true
		m.log(mon.g).Warn("guestd lost", "event", "guestd_lost")
		// The metric is the count, repose_host_guestd_lost, recomputed by the
		// samples loop: guest_id is never a Prometheus label
		// (docs/workstreams/10-observability.md §5). Which guest it is comes
		// from this line in Loki and from the Warning the api receives.
		m.Warn("guestd_lost", "guest "+mon.g.GuestID+": no vsock connection for "+m.cfg.GuestdLostAfter.String())
	}
}

// everConnected reports whether guestd has answered this monitor at
// least once.
func (mon *monitor) everConnected() bool {
	mon.mu.Lock()
	defer mon.mu.Unlock()
	return mon.connected
}

func (mon *monitor) regained() {
	m := mon.m
	if mon.lost {
		m.log(mon.g).Info("guestd regained", "event", "guestd_regained")
	}
	mon.lost = false
}

func (mon *monitor) handleNotify(n *guestdv1.Notify) {
	m := mon.m
	switch v := n.N.(type) {
	case *guestdv1.Notify_AgentEvent, *guestdv1.Notify_Question, *guestdv1.Notify_Warning, *guestdv1.Notify_TriggerRun:
		// The ones that become api events pass the guest's bucket, so one
		// guest cannot fill the host's stream (I-445). The first close of
		// a question hostd passed as open takes no token (I-690): a lost
		// one leaves the question pending on the dashboard, and such
		// closes are bounded by the opens the bucket counted. Any other
		// close (an id never seen open, a repeat, one opened before a
		// hostd restart) takes a token, so made-up closes cannot flood
		// the stream.
		if q, ok := v.(*guestdv1.Notify_Question); ok && q.Question.GetState() != "" && mon.opened.close(q.Question.GetQuestionId(), m.d.Now()) {
			break
		}
		if !mon.notifyBucket.take(m.d.Now()) {
			mon.notifyBucket.dropped++
			if mon.notifyBucket.dropped == 1 {
				m.log(mon.g).Warn("guest notifications over the rate; dropping", "event", "notify_dropped", "reason", "rate_limited")
			}
			m.notifyDropped("rate_limited")
			return
		}
		if d := mon.notifyBucket.dropped; d > 0 {
			m.log(mon.g).Info("guest notifications under the rate again", "event", "notify_dropped", "dropped", d)
			mon.notifyBucket.dropped = 0
		}
	}
	switch v := n.N.(type) {
	case *guestdv1.Notify_Ready:
		if !mon.markReady() {
			// A guestd that restarted under a running guest: it has
			// whatever it kept on the volume, and hostd hands it the
			// set it holds, so a set the api changed while guestd was
			// down is not stale until the next start (I-1248).
			//
			// It also fires once on an ordinary first boot, because the
			// Ping path marks the guest ready before this Ready
			// arrives. That is a repeat of the set the boot itself
			// delivers, from the same cache, so the guest writes the
			// same bytes twice and nothing changes.
			go m.resendTriggers(mon)
		}
	case *guestdv1.Notify_AgentEvent:
		ae := v.AgentEvent
		ev, ok := cleanAgentEvent(mon.g.GuestID, ae.Agent, ae.Kind, ae.Summary, ae.TmuxWindow)
		if !ok {
			m.notifyDropped("invalid")
			return
		}
		ev.LinkUrl, ev.LinkLabel = cleanLink(ae.LinkUrl, ae.LinkLabel)
		m.log(mon.g).Info("agent event", "event", "agent_event", "agent", ev.Agent, "kind", ev.Kind)
		m.emitGuestEvent(&hostdv1.Event_AgentEvent{AgentEvent: ev})
	case *guestdv1.Notify_Question:
		q := v.Question
		aq, ok := cleanQuestion(mon.g.GuestID, q.QuestionId, q.Agent, q.TmuxWindow, q.Text, q.Options, q.TimeoutS, q.State, q.Reason, q.Secret)
		if !ok {
			m.notifyDropped("invalid")
			return
		}
		aq.LinkUrl, aq.LinkLabel = cleanLink(q.LinkUrl, q.LinkLabel)
		if aq.State == "" {
			mon.opened.open(aq.QuestionId, m.d.Now())
		}
		// The text is tenant content: counted, never logged.
		m.log(mon.g).Info("agent question", "event", "agent_question", "agent", aq.Agent, "question_id", aq.QuestionId, "state", aq.State, "reason", aq.Reason, "text_bytes", len(q.Text))
		m.emitGuestEvent(&hostdv1.Event_AgentQuestion{AgentQuestion: aq})
	case *guestdv1.Notify_AgentState:
		m.log(mon.g).Debug("agent state", "event", "agent_state", "agent", cleanToken(v.AgentState.Agent, capAgent), "state", cleanToken(v.AgentState.State, capAgent))
	case *guestdv1.Notify_TriggerRun:
		tr, ok := cleanTriggerRun(mon.g.GuestID, v.TriggerRun)
		if !ok {
			m.notifyDropped("invalid")
			return
		}
		// The trigger's name is the user's word and the summary is the
		// agent's: counted, never logged (DECISIONS I-1248).
		m.log(mon.g).Info("trigger run", "event", "trigger_run", "run_id", tr.RunId,
			"state", tr.State, "source", tr.Source, "summary_bytes", len(tr.Summary))
		m.emitGuestEvent(&hostdv1.Event_TriggerRun{TriggerRun: tr})
	case *guestdv1.Notify_Warning:
		w := cleanWarning(mon.g.GuestID, v.Warning.Kind, v.Warning.Detail)
		m.log(mon.g).Warn("guest warning", "event", "host_warning", "kind", w.Kind)
		m.emitGuestEvent(&hostdv1.Event_HostWarning{HostWarning: w})
	}
}

// checkUnits notices a hypervisor or virtiofsd that died under a running
// guest and hands the cleanup to the guest's queue so it never races a
// command.
func (mon *monitor) checkUnits() {
	m := mon.m
	g, err := m.d.State.GetGuest(mon.g.GuestID)
	if err != nil || g.State != StateRunning {
		return
	}
	active, err := m.d.Systemd.IsActive(mon.ctx, GuestUnit(g.GuestID))
	if err != nil {
		return
	}
	if !active {
		code := "unknown"
		if props, err := m.d.Systemd.Show(mon.ctx, GuestUnit(g.GuestID), "ExecMainStatus"); err == nil {
			code = props["ExecMainStatus"]
		}
		mon.cancel()
		m.enqueue(g.GuestID, job{internal: func(ctx context.Context) { m.unexpectedExit(ctx, g.GuestID, "hypervisor exited "+code) }})
		return
	}
	if isErofs(g) {
		return // no store share to watch (I-1480)
	}
	vActive, err := m.d.Systemd.IsActive(mon.ctx, virtiofs.Unit(g.GuestID))
	if err == nil && !vActive {
		mon.cancel()
		m.enqueue(g.GuestID, job{internal: func(ctx context.Context) { m.unexpectedExit(ctx, g.GuestID, "virtiofsd exited") }})
	}
}

func (m *Manager) unexpectedExit(ctx context.Context, id, reason string) {
	g, err := m.d.State.GetGuest(id)
	if err != nil || g.State != StateRunning {
		return
	}
	if m.Stopping(ctx) {
		// The host is shutting down and stopped the guest's units; the
		// guest was running and stays recorded so, closure and all, for
		// the next boot to find (DECISIONS I-650, I-645).
		m.log(g).Warn("guest stopped by the host shutdown", "event", "guest_interrupted")
		return
	}
	m.log(g).Error("guest died", "event", "guest_state", "reason", reason)
	m.teardown(ctx, g)
	_ = m.setState(g, StateError, reason) // the failure is already logged above; the state write is best effort here
}
