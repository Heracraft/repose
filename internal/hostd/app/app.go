// Package app wires hostd together: identity, state, the guest Manager,
// the api stream, the samples loop, certificate rotation, metrics, console
// capture and the operator control socket.
package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/google/uuid"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"golang.org/x/crypto/ssh"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/console"
	"github.com/heracraft/repose/internal/hostd/control"
	"github.com/heracraft/repose/internal/hostd/gcroot"
	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/hostinfo"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/metrics"
	hnet "github.com/heracraft/repose/internal/hostd/net"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/register"
	"github.com/heracraft/repose/internal/hostd/shell"
	"github.com/heracraft/repose/internal/hostd/snapshot"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/stream"
	"github.com/heracraft/repose/internal/hostd/systemd"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
	"github.com/heracraft/repose/internal/obs"
	"github.com/heracraft/repose/internal/obs/instrument"
)

// Options are the daemon's flags.
type Options struct {
	Version       string
	StateDir      string
	GuestsDir     string
	BuildsDir     string
	BaseDir       string
	BaseRepoURL   string
	BaseSSHKey    string
	BuildUser     string
	GCRootsDir    string
	APIAddr       string
	APIServerName string
	APICA         string
	TokenPath     string
	MetricsAddr   string
	ControlSock   string
	GuestdUnix    bool
	SnapshotDir   string
	BlobURL       string
	BlobContainer string
	BlobIdentity  string
	StoreExport   string
	VirtiofsUser  string
	GuestUser     string
	// ClaudeLoginShare off starts no login share at guest start (I-278).
	ClaudeLoginShare bool
	VG               string
	Pool             string
	MaxOps           int
	MaxBuilds        int
	FailAtStep       int
	NoWG             bool
	Substituters     string
	LogLevel         string
}

// Logger builds hostd's logger from internal/obs, which puts ts, level,
// component and the redaction floor on every line
// (docs/workstreams/10-observability.md §5). level is a --log-level value;
// an unknown one falls back to info and says so on the first line.
func Logger(level string) *slog.Logger {
	lv, err := obs.ParseLevel(level)
	log := obs.NewLogger(obs.LogOptions{Component: obs.ComponentHostd, Level: lv})
	if err != nil {
		log.Warn("unknown log level; using info", "event", "start", "reason", "bad_log_level")
	}
	return log
}

// HostNetUnit renders the bridge, sshd and exporter addresses from
// host.json (workstream 01); hostd restarts it after registering.
const HostNetUnit = "repose-host-net.service"

// TokenError is returned by EnsureIdentity when the token is refused.
var TokenError = register.ErrTokenUsed

// registerRetry is the wait between registration attempts; a variable so
// the test of the loop does not take half a minute.
var registerRetry = 30 * time.Second

// EnsureIdentity loads the identity or registers with the join token,
// retrying every 30 s while the token is missing or the api unreachable.
func EnsureIdentity(ctx context.Context, o Options, log *slog.Logger, r shell.Runner, l lvm.LVM) (*register.Identity, error) {
	if id, err := register.Load(o.StateDir); err == nil {
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	roots, err := register.LoadRoots(o.APICA)
	if err != nil {
		return nil, err
	}
	cfg := register.Config{Dir: o.StateDir, TokenPath: o.TokenPath, APIAddr: o.APIAddr, ServerName: o.APIServerName, Roots: roots}
	for {
		cfg.Info = hostinfo.Collect(ctx, r, l)
		id, err := register.Register(ctx, cfg)
		switch {
		case err == nil:
			log.Info("registered", "event", "register", "host_id", id.Host.HostID)
			// host.json is the host's runtime network input; the renderer
			// that reads it must run again now (docs/interfaces/
			// host-conventions.md, DECISIONS I-18, I-40). When hostd
			// registers itself instead of repose-register.service, nothing
			// else would.
			if r != nil {
				if _, rerr := r.Run(ctx, "systemctl", "--no-block", "restart", HostNetUnit); rerr != nil {
					log.Warn("restart of the host network renderer failed", "event", "register", "unit", HostNetUnit, "err", rerr.Error())
				}
			}
			return id, nil
		case errors.Is(err, register.ErrTokenUsed):
			log.Error("register: join token already used", "event", "register")
			return nil, err
		case errors.Is(err, register.ErrNoToken):
			// The token may be gone because repose-register.service used it:
			// it writes the identity and exits, and nothing else tells a
			// hostd that started before the token arrived (DECISIONS I-76).
			if id, lerr := register.Load(o.StateDir); lerr == nil {
				log.Info("registered", "event", "register", "host_id", id.Host.HostID)
				return id, nil
			}
			log.Warn("waiting for join token", "event", "register")
		default:
			log.Warn("register failed; retrying", "event", "register", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(registerRetry):
		}
	}
}

// Daemon is the running hostd.
type Daemon struct {
	o       Options
	log     *slog.Logger
	st      *state.DB
	mgr     *guest.Manager
	strm    *stream.Stream
	metrics *metrics.M
	id      *register.Identity
	idMu    sync.Mutex
	start   time.Time
	dialer  *rotatingDialer
}

type rotatingDialer struct {
	mu    sync.Mutex
	inner stream.GRPCDialer
}

// lateHost lets the stream be built before the Manager.
type lateHost struct{ m *guest.Manager }

func (l *lateHost) Hello() *hello         { return l.m.Hello() }
func (l *lateHost) Heartbeat() *heartbeat { return l.m.Heartbeat() }
func (l *lateHost) Dispatch(c *command)   { l.m.Dispatch(c) }

// Run starts everything and blocks until ctx ends. Guests keep running
// across a return; only hostd's goroutines stop.
func Run(ctx context.Context, o Options, log *slog.Logger) error {
	// Tracing is wired and off: with no OTEL_EXPORTER_OTLP_ENDPOINT there is
	// no exporter and no connection, and the spans the gRPC stream starts go
	// to the noop provider (docs/workstreams/10-observability.md §5).
	_, shutdownTracing, err := instrument.SetupTracing(ctx, instrument.TraceOptions{Component: obs.ComponentHostd, Version: o.Version, Insecure: true})
	if err != nil {
		return fmt.Errorf("set up tracing: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(sctx); err != nil {
			log.Warn("tracer shutdown failed", "event", "shutdown", "err", err.Error())
		}
	}()
	if instrument.TracingEnabled() {
		log.Info("tracing enabled", "event", "start", "part", "tracing")
	}

	st, err := state.Open(filepath.Join(o.StateDir, "state.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }() // read-only at exit; a close error changes nothing
	r := shell.Exec{}
	l := &lvm.Real{VG: o.VG, Pool: o.Pool, R: r, Sandbox: true}
	id, err := EnsureIdentity(ctx, o, log, r, l)
	if err != nil {
		return err
	}
	roots, err := register.LoadRoots(o.APICA)
	if err != nil {
		return err
	}
	m := metrics.New()
	total, _, _ := hostinfo.MemInfo() // an unreadable meminfo means no memory accounting; Defaults() copes with zero
	d := &Daemon{o: o, log: log, st: st, metrics: m, id: id, start: time.Now()}
	d.dialer = &rotatingDialer{inner: stream.GRPCDialer{Addr: o.APIAddr, TLS: id.TLSConfig(roots, o.APIServerName)}}
	lh := &lateHost{}
	d.strm = stream.New(stream.Config{}, streamDialer{d.dialer}, lh, m, log)

	var blob snapshot.Blob
	switch {
	case o.SnapshotDir != "":
		blob = &snapshot.FileBlob{Dir: o.SnapshotDir}
	case o.BlobURL != "":
		blob, err = snapshot.NewAzureBlob(o.BlobURL, o.BlobContainer, o.BlobIdentity)
		if err != nil {
			return err
		}
	default:
		return errors.New("hostd: set --snapshot-dir or --blob-url")
	}
	var dialer vsockclient.Dialer = vsockclient.CHDialer{}
	if o.GuestdUnix {
		dialer = vsockclient.UnixDialer{}
	}
	nix := (&nixbuild.Real{R: r, BuildsDir: o.BuildsDir, BaseDir: o.BaseDir, BaseRepoURL: o.BaseRepoURL, BaseSSHKey: o.BaseSSHKey,
		Roots: gcroot.Roots{Dir: o.GCRootsDir}, UseScope: true, User: o.BuildUser, Timeout: "timeout", Substituters: o.Substituters}).Defaults()
	cfg := guest.Config{
		HostID: id.Host.HostID, GuestsDir: o.GuestsDir, GuestCIDR: id.Host.GuestCIDR, TotalMemBytes: total,
		MaxOps: o.MaxOps, MaxBuilds: o.MaxBuilds, StoreExport: o.StoreExport, VirtiofsUser: o.VirtiofsUser,
		VirtiofsSocketWait: 10 * time.Second,
		GuestUser:          o.GuestUser,
		NoAuthShare:        !o.ClaudeLoginShare,
	}
	if os.Getenv("REPOSE_HOSTD_TESTING") == "1" {
		cfg.FailAtStep = o.FailAtStep
		// The hostd and virtiofsd accounts exist on hosts only; under the
		// fakes the guest directory is chowned to hostd's own ids.
		cfg.Lookup = func(string) (int, int, error) { return os.Getuid(), os.Getgid(), nil }
	}
	consoles := &consoleSet{log: log}
	mgr, err := guest.New(cfg, guest.Deps{
		State: st, LVM: l, Net: hnet.NewReal(r), Systemd: systemd.NewReal(r), CH: &ch.HTTP{}, Guestd: dialer, Nix: nix,
		Roots: gcroot.Roots{Dir: o.GCRootsDir}, Blob: blob, Stream: &snapshot.Pipeline{R: r, Sandbox: true}, Emit: d.strm, Metrics: m, Log: log,
		MemInfo: hostinfo.MemInfo, Load1: hostinfo.Load1, StoreStat: hostinfo.StoreStat, ConsoleStart: consoles.start,
	})
	if err != nil {
		return err
	}
	lh.m = mgr
	d.mgr = mgr
	if err := mgr.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	mgr.Run()
	defer mgr.Close()

	var wg sync.WaitGroup
	run := func(name string, f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
		log.Info("started "+name, "event", "start", "part", name)
	}
	run("stream", func() { d.strm.Run(ctx) })
	run("samples", func() { d.samplesLoop(ctx) })
	run("rotation", func() { d.rotationLoop(ctx, roots) })
	run("prune", func() { d.pruneLoop(ctx) })
	run("metrics", func() { d.serveMetrics(ctx) })
	run("control", func() {
		if err := control.Serve(ctx, o.ControlSock, d); err != nil {
			log.Error("control socket failed", "event", "control", "err", err.Error())
		}
	})
	<-ctx.Done()
	wg.Wait()
	consoles.stopAll()
	return nil
}

func (d *Daemon) samplesLoop(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.strm.Samples(d.mgr.CollectSamples(ctx))
		}
	}
}

func (d *Daemon) rotationLoop(ctx context.Context, roots *x509.CertPool) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		d.idMu.Lock()
		cur := d.id
		d.idMu.Unlock()
		if !cur.ShouldRotate(time.Now()) {
			continue
		}
		cfg := register.Config{Dir: d.o.StateDir, APIAddr: d.o.APIAddr, ServerName: d.o.APIServerName, Roots: roots, Info: hostinfo.Collect(ctx, shell.Exec{}, nil)}
		next, err := register.Rotate(ctx, cfg, cur)
		if err != nil {
			d.log.Warn("certificate rotation failed", "event", "rotate", "err", err.Error())
			continue
		}
		d.idMu.Lock()
		d.id = next
		d.idMu.Unlock()
		// host.json's rendered fields (the Host CA sshd trusts, the Loki
		// Fluent Bit ships to) only reach /run/repose when the renderer runs
		// again; a rotate is the one time they change without a boot
		// (I-95, I-139).
		if next.Host.HostCAPub != cur.Host.HostCAPub || next.Host.LokiURL != cur.Host.LokiURL {
			if _, rerr := (shell.Exec{}).Run(ctx, "systemctl", "--no-block", "restart", HostNetUnit); rerr != nil {
				d.log.Warn("restart of the host network renderer failed", "event", "rotate", "unit", HostNetUnit, "err", rerr.Error())
			}
		}
		d.dialer.mu.Lock()
		d.dialer.inner = stream.GRPCDialer{Addr: d.o.APIAddr, TLS: next.TLSConfig(roots, d.o.APIServerName)}
		d.dialer.mu.Unlock()
		d.log.Info("certificate rotated", "event", "rotate", "not_after", next.NotAfter.Format(time.RFC3339))
		d.strm.Reconnect()
	}
}

func (d *Daemon) pruneLoop(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n, err := d.st.PruneCommands(time.Now().Add(-7 * 24 * time.Hour)); err == nil && n > 0 {
			d.log.Info("pruned command results", "event", "prune", "count", n)
		}
		d.mgr.SweepAuthShares(ctx) // login shares of users gone from this host (I-278)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (d *Daemon) serveMetrics(ctx context.Context) {
	addr := d.o.MetricsAddr
	if addr == "" {
		addr = "127.0.0.1:9101"
		if a := d.id.Host.WG.Address; a != "" {
			if ip, _, err := net.ParseCIDR(a); err == nil {
				addr = ip.String() + ":9101"
			}
		}
	}
	d.log.Info("metrics listening", "event", "metrics", "addr", addr)
	if err := d.metrics.Serve(ctx, addr); err != nil {
		d.log.Error("metrics server failed", "event", "metrics", "err", err.Error())
	}
}

// --- control.Backend ----------------------------------------------------

func (d *Daemon) Status(ctx context.Context) (*control.StatusReply, error) {
	gs, err := d.mgr.Guests(ctx)
	if err != nil {
		return nil, err
	}
	by := map[string]int{}
	for _, g := range gs {
		by[g.State]++
	}
	return &control.StatusReply{
		HostID: d.id.Host.HostID, Version: d.o.Version, Connected: d.strm.Connected(), Draining: d.mgr.Draining(),
		Guests: by, FreeMem: d.mgr.FreeMemBytes(), PoolFree: d.mgr.PoolFreeBytes(), Uptime: time.Since(d.start).Round(time.Second).String(),
	}, nil
}

func (d *Daemon) Guests(ctx context.Context) ([]guest.Status, error) { return d.mgr.Guests(ctx) }

func (d *Daemon) SnapshotAll(reason string) ([]string, error) { return d.mgr.SnapshotAll(reason) }

func (d *Daemon) Drain(on bool) error {
	if on {
		return d.st.SetDraining(true)
	}
	return d.mgr.Undrain()
}

func (d *Daemon) Reconcile(ctx context.Context, rebuild bool) ([]string, error) {
	if rebuild {
		return d.mgr.Rebuild(ctx)
	}
	return nil, d.mgr.Reconcile(ctx)
}

func (d *Daemon) ExportState(w io.Writer) error { return d.st.Export(w) }

// --- console capture --------------------------------------------------

type consoleSet struct {
	log *slog.Logger
	mu  sync.Mutex
	run map[string]context.CancelFunc
}

func (c *consoleSet) start(guestID, dir string) func() {
	c.mu.Lock()
	if c.run == nil {
		c.run = map[string]context.CancelFunc{}
	}
	if cancel, ok := c.run[guestID]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.run[guestID] = cancel
	c.mu.Unlock()
	t := console.New(ch.ConsoleSocket(dir), filepath.Join(dir, "console.log"))
	go func() {
		if err := t.Run(ctx); err != nil {
			c.log.Warn("console capture ended", "event", "console", "guest_id", guestID, "err", err.Error())
		}
	}()
	return func() {
		cancel()
		c.mu.Lock()
		delete(c.run, guestID)
		c.mu.Unlock()
	}
}

func (c *consoleSet) stopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cancel := range c.run {
		cancel()
	}
}

// AuditLogin is the PAM hook's side of an operator login (DECISIONS
// I-140): the journald line, now with the certificate's key id and serial
// (or a plain key's fingerprint) taken from sshd's SSH_AUTH_INFO_0, and a
// report to the running daemon over the control socket so the api writes
// the audit_log row. Nothing here may block a login: the socket call has a
// short timeout and a daemon that is not running loses the row, not the
// session; the journal line is the record in that case.
func AuditLogin(log *slog.Logger, hostID, controlSock string) {
	user := os.Getenv("PAM_USER")
	kind := os.Getenv("PAM_TYPE")
	if kind == "" {
		kind = "unknown"
	}
	keyID, serial, fp := ParseAuthInfo(os.Getenv("SSH_AUTH_INFO_0"))
	log.Log(context.Background(), slog.Level(2), "operator login", "event", "operator_login", "host_id", hostID, "pam_type", kind, "user_present", user != "", "key_id", keyID, "cert_serial", serial, "key_fp", fp)
	if kind != "open_session" || controlSock == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rep := control.OperatorLoginReport{PAMType: kind, UserPresent: user != "", KeyID: keyID, Serial: serial, KeyFingerprint: fp}
	if err := control.NewClient(controlSock).OperatorLogin(ctx, rep); err != nil {
		log.Warn("operator login not handed to hostd; the journal line is the record", "event", "operator_login", "host_id", hostID, "err", err.Error())
	}
}

// ParseAuthInfo reads the identifiers out of sshd's SSH_AUTH_INFO_0 ("<method>
// <keytype> <base64>" per line): for a certificate its key id, serial and
// the fingerprint of the key inside it, for a plain key its fingerprint.
// The body never leaves this function.
func ParseAuthInfo(info string) (keyID string, serial uint64, fingerprint string) {
	for _, line := range strings.Split(info, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "publickey" {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(fields[1] + " " + fields[2]))
		if err != nil {
			continue
		}
		if cert, ok := pub.(*ssh.Certificate); ok {
			return cert.KeyId, cert.Serial, ssh.FingerprintSHA256(cert.Key)
		}
		return "", 0, ssh.FingerprintSHA256(pub)
	}
	return "", 0, ""
}

// OperatorLogin is the control-socket side: the report becomes a host
// Event the api turns into an audit_log row (I-140).
func (d *Daemon) OperatorLogin(r control.OperatorLoginReport) error {
	ev := &hostdv1.Event{EventId: uuid.Must(uuid.NewV7()).String(), Ts: time.Now().Unix(),
		Ev: &hostdv1.Event_OperatorLogin{OperatorLogin: &hostdv1.OperatorLogin{PamType: r.PAMType, UserPresent: r.UserPresent, KeyId: r.KeyID, Serial: r.Serial, KeyFingerprint: r.KeyFingerprint}}}
	d.strm.Event(ev)
	return nil
}
