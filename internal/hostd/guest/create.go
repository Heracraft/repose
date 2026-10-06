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
	"github.com/heracraft/repose/internal/hostd/ch"
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
		return errf(CodeInvalidArgument, "class %q must be small, large or xl", class)
	}
	if volumeBytes < m.cfg.MinVolumeBytes || volumeBytes > m.cfg.MaxVolumeBytes {
		return errf(CodeInvalidArgument, "volume_bytes %d outside %d..%d", volumeBytes, m.cfg.MinVolumeBytes, m.cfg.MaxVolumeBytes)
	}
	pct, err := m.poolUsedPct()
	if err != nil {
		return errf(CodeInternal, "thin pool: %v", err)
	}
	if pct >= m.cfg.PoolRefusePct {
		return errf(CodeInsufficientCapacity, "thin pool %.0f%% full", pct)
	}
	if err := m.poolBudget(VolumeName(guestID), volumeBytes); err != nil {
		return err
	}
	if needMem && m.FreeMemBytes() < (c.MemMiB+OverheadMiB)<<20 {
		return errf(CodeInsufficientCapacity, "not enough free memory for a %s guest", class)
	}
	return nil
}

// poolBudget refuses a volume of bytes, created or grown to that size,
// that would take the shared thin pool's allocation past its budget
// (DECISIONS I-449). A thin volume takes pool space only as it is
// written, so the pool is sold more than once: the virtual sizes of every
// thin volume in the pool (guests and the caches' volume; not snapshots,
// not name itself) together with this one may not pass PoolOvercommit
// times the pool. There is no bound on one volume: a plan's whole disk
// may be one project's (the owner's choice in I-449). Volumes already over
// the budget keep running and starting; they cannot grow.
func (m *Manager) poolBudget(name string, bytes uint64) *Error {
	size, _, err := m.d.LVM.PoolStats(m.ctx)
	if err != nil {
		return errf(CodeInternal, "thin pool: %v", err)
	}
	alloc, err := m.d.LVM.Allocated(m.ctx, name)
	if err != nil {
		return errf(CodeInternal, "lvs: %v", err)
	}
	if budget := float64(size) * m.cfg.PoolOvercommit; float64(alloc+bytes) > budget {
		return errf(CodeInsufficientCapacity, "thin pool allocation budget: %d GB allocated, %d GB more would pass %d GB", alloc>>30, bytes>>30, uint64(budget)>>30)
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
		default:
			return nil, errf(CodeAlreadyExists, "guest %s exists in state %s", c.GuestId, existing.State)
		}
	}
	if c.SystemClosure == "" {
		return nil, errf(CodeInvalidArgument, "system_closure required")
	}
	if err := validateSecretNames(c.Secrets); err != nil {
		return nil, err
	}
	if ok, err := m.d.Nix.PathExists(ctx, c.SystemClosure); err != nil {
		return nil, errf(CodeInternal, "nix path-info: %v", err)
	} else if !ok {
		return nil, errf(CodeNotFound, "system closure %s is not in the host store", c.SystemClosure)
	}
	if err := m.capacityCheck(c.GuestId, c.Class, c.VolumeBytes, existing == nil || existing.State != StateCreating); err != nil {
		return nil, err
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
	if err := m.d.LVM.CreateVolume(ctx, VolumeName(g.GuestID), g.VolumeBytes); err != nil {
		return nil, m.fail(g, stepVolume, err)
	}
	if err := m.d.LVM.Mkfs(ctx, VolumeName(g.GuestID)); err != nil {
		return nil, m.fail(g, stepVolume, err)
	}

	// Step 4: GC root.
	if err := m.injected(stepGCRoot); err != nil {
		return nil, m.fail(g, stepGCRoot, err)
	}
	if err := m.d.Roots.Set(g.GuestID, g.SystemClosure); err != nil {
		return nil, m.fail(g, stepGCRoot, err)
	}

	// Step 7 comes before the boot so secrets are ready when Ready arrives.
	if err := m.injected(stepSecrets); err != nil {
		return nil, m.fail(g, stepSecrets, err)
	}
	if err := m.cacheSecrets(g.GuestID, c.Secrets, c.SshCaPub, c.HostKey, c.HostCert); err != nil {
		return nil, m.fail(g, stepSecrets, err)
	}

	if err := m.boot(ctx, g, stepRunner); err != nil {
		return nil, err
	}
	return &hostdv1.CreateResult{GuestIp: g.IP, VsockCid: g.CID}, nil
}

// fail records a create or start failure, rolling back everything but the
// volume, the address and the record.
func (m *Manager) fail(g *state.Guest, step int, err error) *Error {
	e := stepErr(step, err)
	m.teardown(context.Background(), g)
	m.log(g).Error("guest failed", "event", "guest_state", "step", step, "reason", e.Message)
	_ = m.setState(g, StateError, e.Message) // the error being reported is e; a state write failure is logged inside setState's caller path
	return e
}

// boot runs create steps 5, 6, 8, 9 and 10 for a guest whose volume, root
// and secrets are in place. Start reuses it.
func (m *Manager) boot(ctx context.Context, g *state.Guest, firstStep int) *Error {
	class := Classes[g.Class]
	dir := m.guestDir(g.GuestID)
	if err := m.prepareGuestDir(dir); err != nil {
		return m.fail(g, firstStep, err)
	}

	// Step 5: render the hypervisor invocation from the closure.
	if err := m.injected(stepRunner); err != nil {
		return m.fail(g, stepRunner, err)
	}
	info, err := nixbuild.ClosureInfo(g.SystemClosure)
	if err != nil {
		return m.fail(g, stepRunner, err)
	}
	spec := ch.Spec{
		GuestDir: dir, Kernel: info.Kernel, Initrd: info.Initrd, Init: info.Init, KernelParams: info.KernelParams,
		IP: g.IP, Gateway: m.gateway(), Netmask: m.netmask(), Tap: g.Tap, MAC: g.MAC, CID: g.CID,
		VolumeDev: m.d.LVM.DevPath(VolumeName(g.GuestID)), VCPUs: class.VCPUs, MemMiB: class.MemMiB, StoreTag: m.cfg.StoreTag,
		DiskIOPS: class.DiskIOPS, DiskBytesPerSec: class.DiskMBps * 1_000_000,
		Hostname: ch.Hostname(g.ProjectSlug),
	}
	argv := spec.Args()
	if err := os.WriteFile(filepath.Join(dir, "ch.args"), []byte(strings.Join(argv, "\n")+"\n"), 0o640); err != nil {
		return m.fail(g, stepRunner, err)
	}
	g.Kernel, g.Initrd = info.Kernel, info.Initrd

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
	if err := m.d.Net.Shape(ctx, g.Tap, m.cfg.EgressMbit, m.cfg.DownloadMbit); err != nil {
		return m.fail(g, stepNetwork, err)
	}

	// Step 8: virtiofsd.
	if err := m.injected(stepVirtiofsd); err != nil {
		return m.fail(g, stepVirtiofsd, err)
	}
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
	props := GuestUnitProps(class, m.cfg.GuestUser, m.cfg.GuestsDir, dir, spec.VolumeDev)
	if err := m.d.Systemd.Run(ctx, GuestUnit(g.GuestID), props, argv); err != nil {
		return m.fail(g, stepHypervisr, err)
	}
	// The registration RegisterPaths sends is read while the guest boots,
	// not after its guestd answers: `nix-store -qR` and `--dump-db` took
	// 70 ms of every start's critical path on host-01 (DECISIONS I-232).
	dump := make(chan dumpResult, 1)
	go func(closure string) {
		b, err := m.d.Nix.DumpDB(ctx, closure)
		dump <- dumpResult{b, err}
	}(g.SystemClosure)

	// Step 10: wait for guestd, deliver, mark running.
	if err := m.injected(stepReady); err != nil {
		return m.fail(g, stepReady, err)
	}
	mon := m.startMonitor(g)
	sess, err := mon.waitReady(ctx, m.cfg.ReadyTimeout)
	if err != nil {
		mon.stop()
		m.removeMonitor(g.GuestID)
		return m.fail(g, stepReady, fmt.Errorf("guest did not become ready: %w", err))
	}
	if err := m.deliver(ctx, g, sess, dump); err != nil {
		mon.stop()
		m.removeMonitor(g.GuestID)
		return m.fail(g, stepReady, err)
	}
	if err := m.setState(g, StateRunning, ""); err != nil {
		return m.fail(g, stepReady, err)
	}
	m.log(g).Info("guest running", "event", "guest_start", "class", g.Class)
	return nil
}

// dumpResult is a `nix-store --dump-db` read ahead of Ready.
type dumpResult struct {
	b   []byte
	err error
}

// deliver sends secrets, principals and the project setup after Ready.
// dump delivers the closure's registration, read while the guest booted.
func (m *Manager) deliver(ctx context.Context, g *state.Guest, sess vsockclient.Session, dump <-chan dumpResult) error {
	// The booted closure is on disk in the guest but unknown to its nix
	// database until registered (DECISIONS I-67).
	var d dumpResult
	select {
	case d = <-dump:
	case <-ctx.Done():
		return ctx.Err()
	}
	reg, err := d.b, d.err
	if err != nil {
		return fmt.Errorf("nix-store --dump-db: %w", err)
	}
	if len(reg) > 0 {
		if err := sess.RegisterPaths(ctx, reg); err != nil {
			return fmt.Errorf("RegisterPaths: %w", err)
		}
	}
	if secrets := m.cachedSecrets(g.GuestID); len(secrets) > 0 {
		if err := sess.WriteSecrets(ctx, secrets); err != nil {
			return fmt.Errorf("WriteSecrets: %w", err)
		}
	}
	if len(g.Principals) > 0 {
		if err := sess.SetPrincipals(ctx, g.Principals); err != nil {
			return fmt.Errorf("SetPrincipals: %w", err)
		}
	}
	slug := g.ProjectSlug
	if slug == "" {
		slug = g.ProjectID
	}
	sp := &guestdv1.SetupProject{ProjectSlug: slug, RemoteUrl: g.RemoteURL, Tz: g.Env["TZ"], Lang: g.Env["LANG"], ProjectJson: g.ProjectJSON}
	if err := sess.SetupProject(ctx, sp); err != nil {
		return fmt.Errorf("SetupProject: %w", err)
	}
	return nil
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

func (m *Manager) start(ctx context.Context, c *hostdv1.StartGuest) *Error {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	switch g.State {
	case StateRunning:
		return nil
	case StateStopped, StateError, StateStarting:
	default:
		return errf(CodeInvalidArgument, "guest is %s; start needs stopped", g.State)
	}
	if g.SystemClosure == "" {
		return errf(CodeNotFound, "system closure missing; api must rebuild")
	}
	if ok, err := m.d.Roots.Exists(g.GuestID); err != nil {
		return errf(CodeInternal, "gcroot: %v", err)
	} else if !ok {
		return errf(CodeNotFound, "system closure missing; api must rebuild")
	}
	if ok, err := m.d.LVM.VolumeExists(ctx, VolumeName(g.GuestID)); err != nil {
		return errf(CodeInternal, "lvs: %v", err)
	} else if !ok {
		return errf(CodeNotFound, "volume for guest %s is missing", g.GuestID)
	}
	// DECISIONS I-260: the api sends the project's class on every start,
	// so a class changed while the project was stopped takes effect here:
	// the hypervisor's vCPUs and memory, the unit's MemoryMax and the
	// class the samples (and so billing) report. The record is written by
	// boot's first state change; a start refused below leaves it as it was.
	if c.Class != "" && c.Class != g.Class {
		if _, ok := Classes[c.Class]; !ok {
			return errf(CodeInvalidArgument, "class %q must be small, large or xl", c.Class)
		}
		m.log(g).Info("guest class changed", "event", "guest_start", "class", c.Class, "prev", g.Class)
		g.Class = c.Class
	}
	if m.FreeMemBytes() < (Classes[g.Class].MemMiB+OverheadMiB)<<20 {
		return errf(CodeInsufficientCapacity, "not enough free memory for a %s guest", g.Class)
	}
	// DECISIONS I-26: StartGuest may carry the delivery fields so a host
	// that restarted still has the guest's secrets and sshd material.
	if len(c.Secrets) > 0 || len(c.HostKey) > 0 || len(c.HostCert) > 0 || c.SshCaPub != "" {
		if err := m.cacheSecrets(g.GuestID, c.Secrets, c.SshCaPub, c.HostKey, c.HostCert); err != nil {
			return err
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
	if m.cachedSecrets(g.GuestID) == nil {
		m.log(g).Warn("starting without secrets; api sent none since hostd restarted", "event", "guest_start", "reason", "no_secrets")
	}
	return m.boot(ctx, g, stepRunner)
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
	stopCon func()

	// notifyBucket limits the guest's notifications; only run() touches it.
	notifyBucket bucket
}

func (m *Manager) startMonitor(g *state.Guest) *monitor {
	m.mu.Lock()
	if old, ok := m.monitors[g.GuestID]; ok {
		m.mu.Unlock()
		return old
	}
	ctx, cancel := context.WithCancel(m.ctx)
	mon := &monitor{m: m, g: g, ctx: ctx, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	m.monitors[g.GuestID] = mon
	m.mu.Unlock()
	if m.d.ConsoleStart != nil {
		mon.stopCon = m.d.ConsoleStart(g.GuestID, m.guestDir(g.GuestID))
	}
	go mon.run()
	return mon
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

func (mon *monitor) markReady() {
	mon.mu.Lock()
	if !mon.readyOK {
		mon.readyOK = true
		close(mon.ready)
	}
	mon.mu.Unlock()
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

func (mon *monitor) regained() {
	m := mon.m
	if mon.lost {
		m.log(mon.g).Info("guestd regained", "event", "guestd_regained")
	}
	mon.lost = false
}

func (mon *monitor) handleNotify(n *guestdv1.Notify) {
	m := mon.m
	switch n.N.(type) {
	case *guestdv1.Notify_AgentEvent, *guestdv1.Notify_Question, *guestdv1.Notify_Warning:
		// The ones that become api events pass the guest's bucket, so one
		// guest cannot fill the host's stream (I-445).
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
		mon.markReady()
	case *guestdv1.Notify_AgentEvent:
		ae := v.AgentEvent
		ev, ok := cleanAgentEvent(mon.g.GuestID, ae.Agent, ae.Kind, ae.Summary, ae.TmuxWindow)
		if !ok {
			m.notifyDropped("invalid")
			return
		}
		m.log(mon.g).Info("agent event", "event", "agent_event", "agent", ev.Agent, "kind", ev.Kind)
		m.emitGuestEvent(&hostdv1.Event_AgentEvent{AgentEvent: ev})
	case *guestdv1.Notify_Question:
		q := v.Question
		aq, ok := cleanQuestion(mon.g.GuestID, q.QuestionId, q.Agent, q.TmuxWindow, q.Text, q.Options, q.TimeoutS, q.State)
		if !ok {
			m.notifyDropped("invalid")
			return
		}
		// The text is tenant content: counted, never logged.
		m.log(mon.g).Info("agent question", "event", "agent_question", "agent", aq.Agent, "question_id", aq.QuestionId, "state", aq.State, "text_bytes", len(q.Text))
		m.emitGuestEvent(&hostdv1.Event_AgentQuestion{AgentQuestion: aq})
	case *guestdv1.Notify_AgentState:
		m.log(mon.g).Debug("agent state", "event", "agent_state", "agent", cleanToken(v.AgentState.Agent, capAgent), "state", cleanToken(v.AgentState.State, capAgent))
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
	m.log(g).Error("guest died", "event", "guest_state", "reason", reason)
	m.teardown(ctx, g)
	_ = m.setState(g, StateError, reason) // the failure is already logged above; the state write is best effort here
}
