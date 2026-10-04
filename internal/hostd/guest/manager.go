// Package guest is hostd's state machine: it turns Commands from the api
// into guests, serialising commands per guest, bounding concurrency per
// host, writing every transition to bbolt before acting on it, and
// answering a repeated command_id with the stored result.
package guest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/gcroot"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/metrics"
	hnet "github.com/heracraft/repose/internal/hostd/net"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/snapshot"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/systemd"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
	"github.com/heracraft/repose/internal/obs"
)

// Guest states, the enum in docs/interfaces/README.md.
const (
	StateCreating   = "creating"
	StateBuilding   = "building"
	StateStarting   = "starting"
	StateRunning    = "running"
	StateStopping   = "stopping"
	StateStopped    = "stopped"
	StateRestoring  = "restoring"
	StateDestroying = "destroying"
	StateDestroyed  = "destroyed"
	StateError      = "error"
)

// Error codes from docs/interfaces/grpc-hostd.md.
const (
	CodeInvalidArgument      = "invalid_argument"
	CodeNotFound             = "not_found"
	CodeAlreadyExists        = "already_exists"
	CodeInsufficientCapacity = "insufficient_capacity"
	CodeBuildFailed          = "build_failed"
	CodeBuildTimeout         = "build_timeout"
	CodeEvalFailed           = "eval_failed"
	CodeClosureTooLarge      = "closure_too_large"
	CodeGuestUnresponsive    = "guest_unresponsive"
	CodeInternal             = "internal"
)

// Reserved secret names (DECISIONS I-10).
const (
	SecretHostKey  = "ssh_host_ed25519_key"
	SecretHostCert = "ssh_host_ed25519_key-cert.pub"
	SecretUserCA   = "user_ca.pub"
)

// Class is a size class. DiskIOPS and DiskMBps cap the guest's disk, read
// and write together, so one guest cannot take the shared data disk from
// the others (DECISIONS I-450).
type Class struct {
	VCPUs    uint32
	MemMiB   uint64
	DiskIOPS uint64
	DiskMBps uint64
}

// Classes are the three size classes from DESIGN.md §5. The disk caps are
// each at most a quarter of a host data disk's 16,000 IOPS and 600 MB/s.
var Classes = map[string]Class{
	"small": {VCPUs: 2, MemMiB: 4096, DiskIOPS: 2000, DiskMBps: 80},
	"large": {VCPUs: 4, MemMiB: 8192, DiskIOPS: 3000, DiskMBps: 120},
	"xl":    {VCPUs: 8, MemMiB: 16384, DiskIOPS: 4000, DiskMBps: 150},
}

// OverheadMiB is what guest@<id> may hold beyond the guest's RAM. The RAM
// is a shared memfd (virtio-fs needs shared=on), charged to the unit as
// shmem and never reclaimable, so everything else Cloud Hypervisor holds
// has to fit here: its own heap, page tables and KVM's second-level ones
// (about 4 MiB per GiB of RAM), io_uring and slab, and the page cache of
// the kernel and initrd it read. With the disk opened O_DIRECT that is
// under 150 MiB for xl (DECISIONS I-230); the rest is margin. virtiofsd is
// its own unit (virtiofsd@<id>, MemoryMax=1G), as is the login share
// (virtiofsd-auth@<id>, MemoryMax=64M, I-278); neither is in this number.
// Placement (FreeMemBytes) and the api count class RAM plus this.
const OverheadMiB = 512

// HighMarginMiB puts MemoryHigh this far below MemoryMax: past it the
// kernel reclaims the unit's page cache on every allocation and throttles
// the allocating thread instead of invoking the OOM killer, which at
// MemoryMax has only the hypervisor to kill (DECISIONS I-230).
const HighMarginMiB = 128

// Error is a command failure with its documented code.
type Error struct {
	Code         string
	Message      string
	FragmentLine int32
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Config is the host-level tuning.
type Config struct {
	HostID           string
	GuestsDir        string
	GuestCIDR        string
	TotalMemBytes    uint64
	HostReserveBytes uint64
	MaxOps           int
	MaxBuilds        int
	MaxBuildQueue    int
	ReadyTimeout     time.Duration
	// VirtiofsSocketWait bounds how long step 8 waits for virtiofsd to create
	// its socket before Cloud Hypervisor is started; zero skips the wait
	// (unit tests with a fake systemd). A virtiofsd that exits first fails
	// the create at step 8 with its unit named, instead of step 10 a minute
	// later (DECISIONS I-62).
	VirtiofsSocketWait time.Duration
	StopTimeoutS       uint32
	EgressMbit         int
	// DownloadMbit limits what a guest receives from outside the host
	// (DECISIONS I-451); EgressMbit what it sends (I-217).
	DownloadMbit   int
	StoreTag       string
	StoreExport    string
	VirtiofsUser   string
	VirtiofsBinary string
	// The Claude login share (DECISIONS I-278): UsersDir/<user_id>/claude-auth
	// holds one user's .credentials.json, served into each of that user's
	// guests as AuthTag by a virtiofsd running as AuthUser.
	UsersDir string
	AuthTag  string
	AuthUser string
	// NoAuthShare starts no login share: guests boot as before I-278.
	NoAuthShare bool
	// AuthKeep is how long a user's login share outlives their last guest
	// on this host; zero means 30 days, the snapshot retention.
	AuthKeep time.Duration
	// GuestUser is the unprivileged user guest@<id> (Cloud Hypervisor) runs
	// as (I-51). It owns the taps and is in group kvm; the guest volumes
	// are group-owned by it through the host's udev rule.
	GuestUser string
	// Lookup resolves a user name to uid and primary gid; nil means the
	// system user database. Tests point it at their own ids.
	Lookup         func(name string) (uid, gid int, err error)
	MinVolumeBytes uint64
	MaxVolumeBytes uint64
	PoolRefusePct  float64
	PoolWarnPct    float64
	// PoolOvercommit bounds the virtual sizes of the pool's thin volumes,
	// as a multiple of the pool's size; VolumeMaxPoolPct bounds one volume,
	// as a percentage of it (DECISIONS I-449).
	PoolOvercommit   float64
	VolumeMaxPoolPct float64
	StoreHighPct     float64
	GuestdRetry      time.Duration
	// GuestdBootRetry is the dial interval until a monitor's first guestd
	// session: a booting guest's guestd starts listening at an unknown
	// moment on every start's critical path, and a 2 s retry cost a
	// second of each start on average (DECISIONS I-225; 50 ms since I-232).
	GuestdBootRetry time.Duration
	GuestdLostAfter time.Duration
	UnitPoll        time.Duration
	// FailAtStep injects a failure into CreateGuest at that step (tests).
	FailAtStep int
}

// Defaults fills documented values into zero fields.
func (c Config) Defaults() Config {
	def := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.MaxOps, 8)
	def(&c.MaxBuilds, 2)
	def(&c.MaxBuildQueue, 20)
	def(&c.EgressMbit, 200)
	def(&c.DownloadMbit, 1000)
	if c.ReadyTimeout == 0 {
		c.ReadyTimeout = 60 * time.Second
	}
	if c.StopTimeoutS == 0 {
		c.StopTimeoutS = 60
	}
	if c.StoreTag == "" {
		c.StoreTag = "ro-store"
	}
	if c.StoreExport == "" {
		c.StoreExport = "/run/repose/store-export"
	}
	if c.VirtiofsUser == "" {
		c.VirtiofsUser = "virtiofsd"
	}
	if c.UsersDir == "" {
		c.UsersDir = "/var/lib/repose/users"
	}
	if c.AuthTag == "" {
		c.AuthTag = "claude-auth"
	}
	if c.AuthUser == "" {
		c.AuthUser = "repose-auth"
	}
	if c.AuthKeep == 0 {
		c.AuthKeep = 30 * 24 * time.Hour
	}
	if c.GuestUser == "" {
		c.GuestUser = "hostd"
	}
	if c.MinVolumeBytes == 0 {
		c.MinVolumeBytes = 10 << 30
	}
	if c.MaxVolumeBytes == 0 {
		c.MaxVolumeBytes = 2 << 40
	}
	if c.PoolRefusePct == 0 {
		c.PoolRefusePct = 90
	}
	if c.PoolWarnPct == 0 {
		c.PoolWarnPct = 80
	}
	if c.PoolOvercommit == 0 {
		c.PoolOvercommit = 1.5
	}
	if c.VolumeMaxPoolPct == 0 {
		c.VolumeMaxPoolPct = 50
	}
	if c.StoreHighPct == 0 {
		c.StoreHighPct = 80
	}
	if c.GuestdRetry == 0 {
		c.GuestdRetry = 2 * time.Second
	}
	if c.GuestdBootRetry == 0 {
		// A failed dial is a connect to Cloud Hypervisor's socket and one
		// line; at 200 ms the wait after guestd listened averaged 100 ms of
		// every start (DECISIONS I-232).
		c.GuestdBootRetry = 50 * time.Millisecond
	}
	if c.GuestdBootRetry > c.GuestdRetry {
		c.GuestdBootRetry = c.GuestdRetry
	}
	if c.GuestdLostAfter == 0 {
		c.GuestdLostAfter = 60 * time.Second
	}
	if c.UnitPoll == 0 {
		c.UnitPoll = 5 * time.Second
	}
	if c.HostReserveBytes == 0 {
		if c.TotalMemBytes > 128<<30 {
			c.HostReserveBytes = 16 << 30
		} else {
			c.HostReserveBytes = 8 << 30
		}
	}
	return c
}

// Emitter is where results, events, samples and build logs go (the
// stream in production, a recorder in tests).
type Emitter interface {
	Result(res *hostdv1.Result)
	Event(ev *hostdv1.Event)
	Samples(s *hostdv1.Samples)
	BuildLog(commandID string, seq uint64, line string)
}

// Deps are the host facilities, all behind interfaces with fakes.
type Deps struct {
	State   *state.DB
	LVM     lvm.LVM
	Net     hnet.Net
	Systemd systemd.Systemd
	CH      ch.Client
	Guestd  vsockclient.Dialer
	Nix     nixbuild.Builder
	Roots   gcroot.Roots
	Blob    snapshot.Blob
	Stream  snapshot.Streamer
	Emit    Emitter
	Metrics *metrics.M
	Log     *slog.Logger
	// MemInfo returns total and available host memory.
	MemInfo func() (total, avail uint64, err error)
	// Load1 returns the one-minute load average.
	Load1 func() float64
	// StoreStat returns the host store filesystem's size and used bytes.
	StoreStat func() (total, used uint64, err error)
	// ConsoleStart begins console capture for a guest dir; the returned
	// func stops it. Nil disables capture (tests).
	ConsoleStart func(guestID, dir string) (stop func())
	Now          func() time.Time
}

type job struct {
	cmd      *hostdv1.Command
	internal func(ctx context.Context)
}

type worker struct {
	ch chan job
}

// Manager owns the guests.
type Manager struct {
	cfg  Config
	d    Deps
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	// curMu guards the sample cursors and the last sample timestamp; a
	// tap's counters are read under it (advanceNet).
	curMu  sync.Mutex
	last   map[string]sampleCursor
	lastTs int64

	mu       sync.Mutex
	workers  map[string]*worker
	inflight map[string]bool
	secrets  map[string][]*guestdv1.Secret
	monitors map[string]*monitor
	blocked  *blockedTracker
	ops      chan struct{}
	buildCh  chan job
	buildRun int
	// buildBusy is the build workers holding a job (running it or waiting
	// for an op slot); buildSeq is the last BuildLog seq sent per build
	// command, so the queue's line and the build's own share one sequence.
	buildBusy int
	buildSeq  map[string]uint64

	cidr        *net.IPNet
	base        net.IP
	maxIndex    uint32
	poolWarned  time.Time
	storeWarned time.Time
}

// New builds a Manager; Run must be called before commands are dispatched.
func New(cfg Config, d Deps) (*Manager, error) {
	cfg = cfg.Defaults()
	_, ipn, err := net.ParseCIDR(cfg.GuestCIDR)
	if err != nil {
		return nil, fmt.Errorf("guest cidr %q: %w", cfg.GuestCIDR, err)
	}
	ones, bits := ipn.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	if size < 8 {
		return nil, fmt.Errorf("guest cidr %q too small", cfg.GuestCIDR)
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = obs.Nop(obs.ComponentHostd)
	}
	m := &Manager{
		cfg: cfg, d: d,
		workers: map[string]*worker{}, inflight: map[string]bool{},
		secrets: map[string][]*guestdv1.Secret{}, monitors: map[string]*monitor{},
		last:     map[string]sampleCursor{},
		buildSeq: map[string]uint64{},
		ops:      make(chan struct{}, cfg.MaxOps),
		buildCh:  make(chan job, cfg.MaxBuildQueue),
		cidr:     ipn, base: ipn.IP.To4(), maxIndex: size - 4,
	}
	m.ctx, m.stop = context.WithCancel(context.Background())
	return m, nil
}

// Run starts the build workers. It returns immediately.
func (m *Manager) Run() {
	for i := 0; i < m.cfg.MaxBuilds; i++ {
		m.wg.Add(1)
		go m.buildWorker()
	}
}

// Close stops workers and monitors; guests keep running.
func (m *Manager) Close() {
	m.stop()
	m.mu.Lock()
	for _, mon := range m.monitors {
		mon.stop()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Draining reports the host drain flag.
func (m *Manager) Draining() bool {
	d, _ := m.d.State.Draining() // a read error reads as not draining; SetDraining reports write errors
	return d
}

// --- addressing -------------------------------------------------------

func hexPrefix(guestID string) (string, error) {
	h := strings.ReplaceAll(guestID, "-", "")
	if len(h) < 8 {
		return "", errf(CodeInvalidArgument, "guest_id %q is not an id", guestID)
	}
	if _, err := hex.DecodeString(h[:8]); err != nil {
		return "", errf(CodeInvalidArgument, "guest_id %q is not an id", guestID)
	}
	return h[:8], nil
}

// netID is the first eight hex characters of sha256(guest id): the part
// of the id that names the tap and the MAC. The id's own first eight hex
// are the top of its UUIDv7 timestamp and are shared by every guest
// created within the same ~65 s, which put two tenants on one tap on
// host-01 (DECISIONS I-120). Records made before then keep the tap and
// MAC stored in them; only new guests are named this way.
func netID(guestID string) (string, error) {
	if _, err := hexPrefix(guestID); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(guestID))
	return hex.EncodeToString(sum[:4]), nil
}

// TapName is tap-<8 hex of sha256(guest id)>.
func TapName(guestID string) (string, error) {
	h, err := netID(guestID)
	if err != nil {
		return "", err
	}
	return "tap-" + h, nil
}

// MACAddr is 52:54: plus the first four bytes of sha256(guest id); 0x52
// has the locally-administered bit set and the multicast bit clear.
func MACAddr(guestID string) (string, error) {
	h, err := netID(guestID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("52:54:%s:%s:%s:%s", h[0:2], h[2:4], h[4:6], h[6:8]), nil
}

func (m *Manager) ipForIndex(i uint32) string {
	ip := make(net.IP, 4)
	copy(ip, m.base)
	n := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	n += 2 + i
	return fmt.Sprintf("%d.%d.%d.%d", n>>24, n>>16&255, n>>8&255, n&255)
}

func (m *Manager) gateway() string {
	ip := make(net.IP, 4)
	copy(ip, m.base)
	ip[3]++
	return ip.String()
}

func (m *Manager) netmask() string {
	mk := m.cidr.Mask
	return fmt.Sprintf("%d.%d.%d.%d", mk[0], mk[1], mk[2], mk[3])
}

func cidForIndex(i uint32) uint32 { return 1000 + i }

func (m *Manager) guestDir(id string) string { return filepath.Join(m.cfg.GuestsDir, id) }

// VolumeName is g-<guest_id>.
func VolumeName(guestID string) string { return "g-" + guestID }

// GuestUnit is guest@<guest_id>.
func GuestUnit(guestID string) string { return "guest@" + guestID }

// --- state helpers ----------------------------------------------------

func (m *Manager) log(g *state.Guest) *slog.Logger {
	l := m.d.Log
	if g != nil {
		l = l.With("guest_id", g.GuestID, "project_id", g.ProjectID)
	}
	return l
}

func (m *Manager) setState(g *state.Guest, st, reason string) error {
	prev := g.State
	g.State, g.Reason = st, reason
	if err := m.d.State.PutGuest(g); err != nil {
		return fmt.Errorf("state write: %w", err)
	}
	m.writeGuestJSON(g)
	m.log(g).Info("guest state changed", "event", "guest_state", "state", st, "prev", prev, "reason", reason)
	m.emitEvent(&hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{GuestId: g.GuestID, State: st, Reason: reason}})
	m.refreshGuestGauge()
	return nil
}

// writeGuestJSON keeps a non-secret copy of the record in the guest dir
// so `hostd reconcile` can rebuild a lost bbolt from disk.
func (m *Manager) writeGuestJSON(g *state.Guest) {
	dir := m.guestDir(g.GuestID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	b, err := marshalGuest(g)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "guest.json"), b, 0o600) // bbolt is authoritative; this copy is a recovery aid
}

func (m *Manager) refreshGuestGauge() {
	if m.d.Metrics == nil {
		return
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return
	}
	m.d.Metrics.Guests.Reset()
	// Every state and class is published, at zero when empty: a GaugeVec
	// with no children exposes no series at all, so a host with no guests
	// showed "no data" rather than 0 (m3 integration, DECISIONS I-116).
	for _, st := range GuestStates {
		for class := range Classes {
			m.d.Metrics.Guests.WithLabelValues(st, class).Set(0)
		}
	}
	for _, g := range gs {
		m.d.Metrics.Guests.WithLabelValues(g.State, g.Class).Inc()
	}
}

// GuestStates is the guest state enum of docs/interfaces/README.md, the
// label set repose_host_guests always carries.
var GuestStates = []string{
	StateCreating, StateBuilding, StateStarting, StateRunning, StateStopping,
	StateStopped, StateRestoring, StateDestroying, StateDestroyed, StateError,
}

func (m *Manager) emitEvent(ev any) {
	e := &hostdv1.Event{EventId: uuid.Must(uuid.NewV7()).String(), Ts: m.d.Now().Unix()}
	switch v := ev.(type) {
	case *hostdv1.Event_GuestStateChanged:
		e.Ev = v
	case *hostdv1.Event_AgentEvent:
		e.Ev = v
	case *hostdv1.Event_SnapshotDone:
		e.Ev = v
	case *hostdv1.Event_HostWarning:
		e.Ev = v
	case *hostdv1.Event_AgentQuestion:
		e.Ev = v
	}
	m.d.Emit.Event(e)
}

// Warn emits a host_warning event.
func (m *Manager) Warn(kind, detail string) {
	m.d.Log.Warn("host warning", "event", "host_warning", "kind", kind)
	m.emitEvent(&hostdv1.Event_HostWarning{HostWarning: &hostdv1.HostWarning{Kind: kind, Detail: detail}})
}

func (m *Manager) getGuest(id string) (*state.Guest, *Error) {
	if id == "" {
		return nil, errf(CodeInvalidArgument, "guest_id required")
	}
	g, err := m.d.State.GetGuest(id)
	if errors.Is(err, state.ErrNotFound) {
		return nil, errf(CodeNotFound, "guest %s not on this host", id)
	}
	if err != nil {
		return nil, errf(CodeInternal, "state read: %v", err)
	}
	return g, nil
}

// FreeMemBytes is total minus reserve minus what running guests hold.
func (m *Manager) FreeMemBytes() uint64 {
	total := m.cfg.TotalMemBytes
	if m.d.MemInfo != nil {
		if t, _, err := m.d.MemInfo(); err == nil && t > 0 {
			total = t
		}
	}
	reserved := m.reservedBytes()
	if total < m.cfg.HostReserveBytes+reserved {
		return 0
	}
	return total - m.cfg.HostReserveBytes - reserved
}

func (m *Manager) reservedBytes() uint64 {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return 0
	}
	var reserved uint64
	for _, g := range gs {
		switch g.State {
		case StateRunning, StateStarting, StateCreating, StateStopping:
			if c, ok := Classes[g.Class]; ok {
				reserved += (c.MemMiB + OverheadMiB) << 20
			}
		}
	}
	return reserved
}

// PoolFreeBytes reads the thin pool.
func (m *Manager) PoolFreeBytes() uint64 {
	_, free, err := m.d.LVM.PoolStats(m.ctx)
	if err != nil {
		return 0
	}
	return free
}

func (m *Manager) poolUsedPct() (float64, error) {
	size, free, err := m.d.LVM.PoolStats(m.ctx)
	if err != nil || size == 0 {
		return 0, err
	}
	return float64(size-free) / float64(size) * 100, nil
}

// Hello builds the reconciliation message for the stream.
func (m *Manager) Hello() *hostdv1.Hello {
	h := &hostdv1.Hello{HostId: m.cfg.HostID, FreeMemBytes: m.FreeMemBytes(), PoolFreeBytes: m.PoolFreeBytes()}
	gs, _ := m.d.State.ListGuests() // an unreadable table yields an empty Hello; the api reconciles from later samples
	for _, g := range gs {
		h.Guests = append(h.Guests, &hostdv1.GuestStatus{GuestId: g.GuestID, State: g.State, Ip: g.IP, VsockCid: g.CID, SystemClosure: g.SystemClosure})
	}
	return h
}

// Heartbeat builds the periodic heartbeat.
func (m *Manager) Heartbeat() *hostdv1.Heartbeat {
	hb := &hostdv1.Heartbeat{FreeMemBytes: m.FreeMemBytes(), PoolFreeBytes: m.PoolFreeBytes(), Draining: m.Draining()}
	if m.d.Load1 != nil {
		hb.Load1 = m.d.Load1()
	}
	gs, _ := m.d.State.ListGuests() // see Hello
	for _, g := range gs {
		if g.State == StateRunning {
			hb.RunningGuests++
		}
	}
	return hb
}

// --- secrets cache ----------------------------------------------------

func validateSecretNames(secrets []*hostdv1.Secret) *Error {
	for _, s := range secrets {
		switch s.Name {
		case SecretHostKey, SecretHostCert, SecretUserCA:
			return errf(CodeInvalidArgument, "secret name %s is reserved", s.Name)
		case "":
			return errf(CodeInvalidArgument, "secret name required")
		}
	}
	return nil
}

func (m *Manager) cacheSecrets(guestID string, secrets []*hostdv1.Secret, sshCAPub string, hostKey, hostCert []byte) *Error {
	if err := validateSecretNames(secrets); err != nil {
		return err
	}
	var out []*guestdv1.Secret
	for _, s := range secrets {
		out = append(out, &guestdv1.Secret{Name: s.Name, Value: s.Value})
	}
	if len(hostKey) > 0 {
		out = append(out, &guestdv1.Secret{Name: SecretHostKey, Value: hostKey})
	}
	if len(hostCert) > 0 {
		out = append(out, &guestdv1.Secret{Name: SecretHostCert, Value: hostCert})
	}
	if sshCAPub != "" {
		out = append(out, &guestdv1.Secret{Name: SecretUserCA, Value: []byte(sshCAPub)})
	}
	m.mu.Lock()
	if len(out) > 0 {
		m.secrets[guestID] = out
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) cachedSecrets(guestID string) []*guestdv1.Secret {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secrets[guestID]
}

func (m *Manager) forgetSecrets(guestID string) {
	m.mu.Lock()
	delete(m.secrets, guestID)
	m.mu.Unlock()
}

// --- dispatch ---------------------------------------------------------

// Kind names a command for logs, metrics and the idempotency table.
func Kind(cmd *hostdv1.Command) string {
	switch cmd.Cmd.(type) {
	case *hostdv1.Command_CreateGuest:
		return "CreateGuest"
	case *hostdv1.Command_StartGuest:
		return "StartGuest"
	case *hostdv1.Command_StopGuest:
		return "StopGuest"
	case *hostdv1.Command_DestroyGuest:
		return "DestroyGuest"
	case *hostdv1.Command_ResizeVolume:
		return "ResizeVolume"
	case *hostdv1.Command_Build:
		return "Build"
	case *hostdv1.Command_ApplyConfig:
		return "ApplyConfig"
	case *hostdv1.Command_Snapshot:
		return "Snapshot"
	case *hostdv1.Command_Restore:
		return "Restore"
	case *hostdv1.Command_UpdateSecrets:
		return "UpdateSecrets"
	case *hostdv1.Command_SetPrincipals:
		return "SetPrincipals"
	case *hostdv1.Command_Exec:
		return "Exec"
	case *hostdv1.Command_Drain:
		return "Drain"
	case *hostdv1.Command_AnswerQuestion:
		return "AnswerQuestion"
	}
	return ""
}

// Target is the guest a command addresses ("" for host-level commands).
func Target(cmd *hostdv1.Command) string {
	switch c := cmd.Cmd.(type) {
	case *hostdv1.Command_CreateGuest:
		return c.CreateGuest.GuestId
	case *hostdv1.Command_StartGuest:
		return c.StartGuest.GuestId
	case *hostdv1.Command_StopGuest:
		return c.StopGuest.GuestId
	case *hostdv1.Command_DestroyGuest:
		return c.DestroyGuest.GuestId
	case *hostdv1.Command_ResizeVolume:
		return c.ResizeVolume.GuestId
	case *hostdv1.Command_ApplyConfig:
		return c.ApplyConfig.GuestId
	case *hostdv1.Command_Snapshot:
		return c.Snapshot.GuestId
	case *hostdv1.Command_Restore:
		return c.Restore.GuestId
	case *hostdv1.Command_UpdateSecrets:
		return c.UpdateSecrets.GuestId
	case *hostdv1.Command_SetPrincipals:
		return c.SetPrincipals.GuestId
	case *hostdv1.Command_Exec:
		return c.Exec.GuestId
	case *hostdv1.Command_AnswerQuestion:
		return c.AnswerQuestion.GuestId
	}
	return ""
}

// Dispatch handles idempotency and queues the command; the result reaches
// the Emitter. It is safe to call from the stream goroutine.
func (m *Manager) Dispatch(cmd *hostdv1.Command) {
	res, proceed := m.admit(cmd)
	if res != nil {
		m.d.Emit.Result(res)
		return
	}
	if !proceed {
		return // still executing; the result is sent when it finishes
	}
	kind := Kind(cmd)
	if kind == "Build" {
		m.mu.Lock()
		queued := m.buildBusy+len(m.buildCh) >= m.cfg.MaxBuilds
		m.mu.Unlock()
		if queued {
			// The client shows this as its own step rather than an
			// evaluation that seems to take minutes (DECISIONS I-320).
			m.buildLog(cmd.CommandId, "waiting for a build slot")
		}
		select {
		case m.buildCh <- job{cmd: cmd}:
			m.setQueueDepth()
		default:
			res := errResult(cmd.CommandId, errf(CodeInsufficientCapacity, "build queue full"))
			m.finish(cmd, res, time.Time{})
			m.clearInflight(cmd.CommandId)
			m.d.Emit.Result(res)
		}
		return
	}
	key := Target(cmd)
	if key == "" {
		key = "host"
	}
	m.enqueue(key, job{cmd: cmd})
}

// admit records the command. It returns a stored result for a repeat, or
// proceed=false when the same command_id is executing now, else it claims
// the command (marks it in flight) and returns proceed=true. A
// started-but-unfinished record (hostd died mid-command) is re-executed
// except for Exec, which is not idempotent.
func (m *Manager) admit(cmd *hostdv1.Command) (*hostdv1.Result, bool) {
	if cmd.CommandId == "" {
		return errResult("", errf(CodeInvalidArgument, "command_id required")), false
	}
	kind := Kind(cmd)
	if kind == "" {
		return errResult(cmd.CommandId, errf(CodeInvalidArgument, "unknown command")), false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inflight[cmd.CommandId] {
		return nil, false
	}
	existing, err := m.d.State.StartCommand(&state.Command{CommandID: cmd.CommandId, Kind: kind, GuestID: Target(cmd)})
	if err != nil {
		return errResult(cmd.CommandId, errf(CodeInternal, "state write: %v", err)), false
	}
	if existing != nil {
		if existing.Status == "done" {
			res := &hostdv1.Result{}
			if err := proto.Unmarshal(existing.Result, res); err != nil {
				return errResult(cmd.CommandId, errf(CodeInternal, "stored result unreadable: %v", err)), false
			}
			return res, false
		}
		if kind == "Exec" {
			res := errResult(cmd.CommandId, errf(CodeInternal, "command interrupted"))
			m.finish(cmd, res, time.Time{})
			return res, false
		}
		_ = m.d.State.RestartCommand(cmd.CommandId) // the record exists (StartCommand found it); a failed touch changes nothing
	}
	m.inflight[cmd.CommandId] = true
	return nil, true
}

func (m *Manager) clearInflight(id string) {
	m.mu.Lock()
	delete(m.inflight, id)
	m.mu.Unlock()
}

func (m *Manager) enqueue(key string, j job) {
	m.mu.Lock()
	w, ok := m.workers[key]
	if !ok {
		w = &worker{ch: make(chan job, 64)}
		m.workers[key] = w
		m.wg.Add(1)
		go m.guestWorker(w)
	}
	m.mu.Unlock()
	select {
	case w.ch <- j:
	case <-m.ctx.Done():
	}
}

func (m *Manager) guestWorker(w *worker) {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case j := <-w.ch:
			m.runJob(j)
		}
	}
}

func (m *Manager) buildWorker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case j := <-m.buildCh:
			m.setQueueDepth()
			m.mu.Lock()
			m.buildBusy++
			m.mu.Unlock()
			m.runJob(j)
			m.mu.Lock()
			m.buildBusy--
			m.mu.Unlock()
		}
	}
}

// buildLog sends one BuildLog line for a build command, numbered after
// the last one sent for it.
func (m *Manager) buildLog(commandID, line string) {
	m.mu.Lock()
	m.buildSeq[commandID]++
	seq := m.buildSeq[commandID]
	m.mu.Unlock()
	m.d.Emit.BuildLog(commandID, seq, line)
}

func (m *Manager) setQueueDepth() {
	if m.d.Metrics != nil {
		m.d.Metrics.BuildQueueDepth.Set(float64(len(m.buildCh)))
		if len(m.buildCh) >= 10 {
			m.Warn("build_queue_deep", fmt.Sprintf("%d builds queued", len(m.buildCh)))
		}
	}
}

func (m *Manager) runJob(j job) {
	select {
	case m.ops <- struct{}{}:
	case <-m.ctx.Done():
		return
	}
	defer func() { <-m.ops }()
	if j.internal != nil {
		j.internal(m.ctx)
		return
	}
	res := m.run(m.ctx, j.cmd)
	m.d.Emit.Result(res)
}

// Execute admits and runs a command to completion, returning its result
// (nil when the same command_id is executing already). Dispatch is the
// asynchronous path; tests call Execute directly.
func (m *Manager) Execute(ctx context.Context, cmd *hostdv1.Command) *hostdv1.Result {
	res, proceed := m.admit(cmd)
	if res != nil {
		return res
	}
	if !proceed {
		return nil
	}
	return m.run(ctx, cmd)
}

// run executes an admitted command and releases its in-flight claim.
func (m *Manager) run(ctx context.Context, cmd *hostdv1.Command) *hostdv1.Result {
	defer m.clearInflight(cmd.CommandId)
	start := m.d.Now()
	kind := Kind(cmd)
	m.d.Log.Info("command start", "event", "command_start", "command_id", cmd.CommandId, "kind", kind, "guest_id", Target(cmd))
	res := m.execute(ctx, cmd)
	m.finish(cmd, res, start)
	return res
}

func (m *Manager) finish(cmd *hostdv1.Command, res *hostdv1.Result, start time.Time) {
	kind := Kind(cmd)
	result := "ok"
	if !res.Ok {
		result = res.Error.GetCode()
	}
	if m.d.Metrics != nil {
		m.d.Metrics.CommandsTotal.WithLabelValues(kind, result).Inc()
		if !start.IsZero() {
			m.d.Metrics.CommandDuration.WithLabelValues(kind).Observe(m.d.Now().Sub(start).Seconds())
		}
	}
	b, err := proto.Marshal(res)
	if err == nil {
		err = m.d.State.FinishCommand(cmd.CommandId, b)
	}
	if err != nil {
		m.d.Log.Error("result not stored", "event", "command_result", "command_id", cmd.CommandId, "kind", kind, "err", err.Error())
	}
	m.d.Log.Info("command done", "event", "command_result", "command_id", cmd.CommandId, "kind", kind, "result", result, "guest_id", Target(cmd))
}

func (m *Manager) execute(ctx context.Context, cmd *hostdv1.Command) *hostdv1.Result {
	id := cmd.CommandId
	switch c := cmd.Cmd.(type) {
	case *hostdv1.Command_CreateGuest:
		r, err := m.create(ctx, c.CreateGuest)
		return payloadOrErr(id, err, func(res *hostdv1.Result) { res.Payload = &hostdv1.Result_Create{Create: r} })
	case *hostdv1.Command_StartGuest:
		err := m.start(ctx, c.StartGuest)
		return payloadOrErr(id, err, nil)
	case *hostdv1.Command_StopGuest:
		r, err := m.stopCmd(ctx, c.StopGuest)
		return payloadOrErr(id, err, func(res *hostdv1.Result) { res.Payload = &hostdv1.Result_Stop{Stop: r} })
	case *hostdv1.Command_DestroyGuest:
		err := m.destroy(ctx, c.DestroyGuest)
		return payloadOrErr(id, err, nil)
	case *hostdv1.Command_ResizeVolume:
		err := m.resize(ctx, c.ResizeVolume)
		return payloadOrErr(id, err, nil)
	case *hostdv1.Command_Build:
		r, err := m.build(ctx, id, c.Build)
		return payloadOrErr(id, err, func(res *hostdv1.Result) { res.Payload = &hostdv1.Result_Build{Build: r} })
	case *hostdv1.Command_ApplyConfig:
		r, err := m.apply(ctx, c.ApplyConfig)
		return payloadOrErr(id, err, func(res *hostdv1.Result) { res.Payload = &hostdv1.Result_Apply{Apply: r} })
	case *hostdv1.Command_Snapshot:
		r, err := m.snapshotCmd(ctx, c.Snapshot)
		return payloadOrErr(id, err, func(res *hostdv1.Result) { res.Payload = &hostdv1.Result_Snapshot{Snapshot: r} })
	case *hostdv1.Command_Restore:
		r, err := m.restore(ctx, c.Restore)
		return payloadOrErr(id, err, func(res *hostdv1.Result) { res.Payload = &hostdv1.Result_Create{Create: r} })
	case *hostdv1.Command_UpdateSecrets:
		err := m.updateSecrets(ctx, c.UpdateSecrets)
		return payloadOrErr(id, err, nil)
	case *hostdv1.Command_SetPrincipals:
		err := m.setPrincipals(ctx, c.SetPrincipals)
		return payloadOrErr(id, err, nil)
	case *hostdv1.Command_Exec:
		r, err := m.exec(ctx, c.Exec)
		return payloadOrErr(id, err, func(res *hostdv1.Result) { res.Payload = &hostdv1.Result_Exec{Exec: r} })
	case *hostdv1.Command_Drain:
		err := m.drain(ctx)
		return payloadOrErr(id, err, nil)
	case *hostdv1.Command_AnswerQuestion:
		err := m.answerQuestion(ctx, c.AnswerQuestion)
		return payloadOrErr(id, err, nil)
	}
	return errResult(id, errf(CodeInvalidArgument, "unknown command"))
}

func payloadOrErr(id string, err *Error, set func(*hostdv1.Result)) *hostdv1.Result {
	if err != nil {
		return errResult(id, err)
	}
	res := &hostdv1.Result{CommandId: id, Ok: true}
	if set != nil {
		set(res)
	}
	return res
}

func errResult(id string, e *Error) *hostdv1.Result {
	return &hostdv1.Result{CommandId: id, Ok: false, Error: &hostdv1.Error{Code: e.Code, Message: e.Message, FragmentLine: e.FragmentLine}}
}
