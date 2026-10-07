// Package guestd is the daemon inside every repose guest. It serves the
// protocol of docs/interfaces/vsock-guestd.md on vsock port 5000 and has no
// network listener of any kind.
package guestd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/exec"
	"github.com/heracraft/repose/internal/guestd/freeze"
	"github.com/heracraft/repose/internal/guestd/fs"
	"github.com/heracraft/repose/internal/guestd/hooks"
	"github.com/heracraft/repose/internal/guestd/project"
	"github.com/heracraft/repose/internal/guestd/questions"
	"github.com/heracraft/repose/internal/guestd/sample"
	"github.com/heracraft/repose/internal/guestd/secrets"
	"github.com/heracraft/repose/internal/guestd/ssh"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/guestd/system"
	"github.com/heracraft/repose/internal/guestd/warn"
	"github.com/heracraft/repose/internal/obs"
	"github.com/heracraft/repose/internal/vsockrpc"
)

// ProtocolVersion is what Ping reports. docs/workstreams/04-guestd.md §8:
// hostd refuses to send a request a guest's version does not know, so a new
// hostd with old guests degrades per request instead of failing outright.
const ProtocolVersion = "1"

// NotifyQueue is the depth of the notification buffer. Past it, notifications
// are dropped and counted rather than blocking the guest
// (docs/workstreams/04-guestd.md §5).
const NotifyQueue = 256

// DefaultDeadline bounds a request handler that does not set its own.
const DefaultDeadline = 30 * time.Second

// ReadyPollInterval is how often the readiness check looks for sshd. It
// reads /proc/net/tcp and stops at the first hit, so a short step costs
// nothing and takes up to 0.4 s off every boot's Ready (DECISIONS I-161).
const ReadyPollInterval = 100 * time.Millisecond

// Config builds a Server. The zero value is a real guest; tests fill in Root,
// the dev socket and the fakes.
type Config struct {
	// Root prefixes every path, for tests. Empty on a real guest.
	Root string
	// DevSocket, when set, serves the protocol on that unix socket instead of
	// vsock: the --dev-socket mode for machines without vsock.
	DevSocket string
	// VsockPort defaults to 5000.
	VsockPort uint32
	// HookSocket overrides the hook socket path; empty uses the convention.
	HookSocket string
	// HerdrSocket overrides herdr's socket path (tests: sun_path is short);
	// empty uses /home/dev/.config/herdr/herdr.sock under Root. HerdrUID,
	// when above 0, is the peer uid that socket must have in place of
	// dev's.
	HerdrSocket string
	HerdrUID    int

	FreezeTimeout time.Duration
	SwitchTimeout time.Duration

	Log *slog.Logger

	// Dependencies. Nil means the real implementation.
	Runner  sysdep.Runner
	Freezer sysdep.Freezer
	Docker  sysdep.Docker
	Now     func() time.Time

	// SkipReadyProbe makes Ready fire immediately instead of waiting for
	// sshd. Tests set it; a guest never does.
	SkipReadyProbe bool
}

// Server is the daemon.
type Server struct {
	cfg   Config
	paths sysdep.Paths
	log   *slog.Logger
	now   func() time.Time

	freeze  *freeze.Handler
	system  *system.Handler
	fs      *fs.Handler
	secrets *secrets.Handler
	ssh     *ssh.Handler
	project *project.Handler
	sampler *sample.Handler
	watcher *sample.Watcher
	exec    *exec.Handler
	warn    *warn.Checker
	hooks   *hooks.Server
	asks    *questions.Store

	notify  chan *guestdv1.Envelope
	dropped atomic.Uint64

	started time.Time
	bootID  string

	mu    sync.Mutex
	conn  *vsockrpc.Conn
	ready bool
}

// New builds the server and its handlers.
func New(cfg Config) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = obs.Nop(obs.ComponentGuestd)
	}
	log := cfg.Log
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.VsockPort == 0 {
		cfg.VsockPort = vsockrpc.Port
	}
	paths := sysdep.Paths{Root: cfg.Root}

	runner := cfg.Runner
	if runner == nil {
		runner = sysdep.ExecRunner{}
	}
	freezer := cfg.Freezer
	if freezer == nil {
		freezer = sysdep.IoctlFreezer{}
	}
	docker := cfg.Docker
	if docker == nil {
		docker = sysdep.NewSocketDocker(paths.DockerSock(), 2*time.Second)
	}

	s := &Server{
		cfg:     cfg,
		paths:   paths,
		log:     log,
		now:     cfg.Now,
		notify:  make(chan *guestdv1.Envelope, NotifyQueue),
		started: cfg.Now(),
	}
	s.bootID = readBootID(paths)

	s.freeze = freeze.New(freezer, paths.RootMount(), cfg.FreezeTimeout, s.Warn, log)
	s.system = system.New(paths, runner, cfg.SwitchTimeout, log)
	s.fs = fs.New(paths, runner, 0, log)
	s.secrets = secrets.New(paths, runner, log)
	s.ssh = ssh.New(paths, runner, log)
	s.project = project.New(paths, runner, log)
	s.watcher = sample.NewWatcher(paths, runner, docker, s.project, s, log, cfg.Now)
	if cfg.HerdrSocket != "" {
		s.watcher.SetHerdrSocket(cfg.HerdrSocket, cfg.HerdrUID)
	}
	s.sampler = sample.NewHandler(paths, s.watcher, log, cfg.Now)
	s.exec = exec.New(paths, runner, log)
	s.warn = warn.New(paths, s.Warn, log, cfg.Now)

	hookPath := cfg.HookSocket
	if hookPath == "" {
		hookPath = paths.HooksSock()
	}
	_, devGID := sysdep.DevIdentity()
	s.hooks = hooks.NewServer(hookPath, devGID, s.onHook, s.sampler.WindowOfPane, log)
	s.hooks.SetHerdrResolver(s.watcher.ResolveHerdr)
	s.asks = questions.New(paths.QuestionsDir(), s.emitQuestion, log, cfg.Now)
	s.hooks.EnableAsk(s.asks, s.AgentEvent)
	return s, nil
}

// HookSocketPath is where agents POST, after Run has created it.
func (s *Server) HookSocketPath() string { return s.hooks.Addr() }

// Run listens and serves until ctx is done. It returns the first error that
// stops it; a connection ending is not one.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := os.MkdirAll(s.paths.RunDir(), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", s.paths.RunDir(), err)
	}
	// Before serving, so hostd's first command finds the secrets loadable
	// (DECISIONS I-475). A failure is logged; WriteSecrets rewrites it all.
	if err := s.secrets.Restore(ctx); err != nil {
		s.log.Warn("could not rebuild the secrets refresh file", "event", "write_secrets", "error_code", sysdep.CodeOf(err))
	}
	if err := s.hooks.Listen(); err != nil {
		return err
	}
	go func() {
		if err := s.hooks.Serve(); err != nil {
			s.log.Error("the hook socket stopped serving", "event", "hook_bad_payload", "error", err.Error())
		}
	}()
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		if err := s.hooks.Close(shutCtx); err != nil {
			s.log.Warn("could not close the hook socket", "event", "shutdown")
		}
	}()

	l, err := s.listen()
	if err != nil {
		return err
	}
	defer l.Close() //nolint:errcheck // the listener is torn down with the process
	if s.cfg.DevSocket != "" {
		s.log.Info("listening on the dev unix socket", "event", "ready", "transport", "unix")
	} else {
		s.log.Info(fmt.Sprintf("listening on vsock port %d", s.cfg.VsockPort),
			"event", "ready", "transport", "vsock", "port", s.cfg.VsockPort)
	}

	go s.watcher.Run(ctx)
	go s.asks.Run(ctx)
	go s.warn.Run(ctx)
	go s.readyProbe(ctx)
	go func() {
		<-ctx.Done()
		_ = l.Close()
		if err := s.freeze.Close(); err != nil {
			s.log.Error("could not thaw at shutdown", "event", "shutdown")
		}
	}()

	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept on %s: %w", s.transport(), err)
		}
		// One connection at a time: a new hostd replaces the old, which gets
		// EOF, so a hostd restart reconnects cleanly.
		s.serve(ctx, c)
	}
}

func (s *Server) transport() string {
	if s.cfg.DevSocket != "" {
		return "unix"
	}
	return "vsock"
}

func (s *Server) listen() (net.Listener, error) {
	if s.cfg.DevSocket != "" {
		return vsockrpc.ListenUnix(s.cfg.DevSocket)
	}
	return vsockrpc.Listen(s.cfg.VsockPort)
}

// serve installs c as the current connection, replacing any earlier one, and
// starts its reader and notification writer.
func (s *Server) serve(ctx context.Context, c net.Conn) {
	conn := vsockrpc.NewConn(c)

	s.mu.Lock()
	old := s.conn
	s.conn = conn
	s.mu.Unlock()
	if old != nil {
		s.log.Info("replacing the previous hostd connection", "event", "ready")
		_ = old.Close()
	}

	connCtx, cancel := context.WithCancel(ctx)
	go s.writeNotifications(connCtx, conn)
	go func() {
		defer cancel()
		s.readRequests(connCtx, conn)
	}()

	// Ready is sent on every new connection, so a hostd that restarted learns
	// the guest is up without waiting for a boot.
	if s.isReady() {
		s.emitReady()
	}
	// Every question still waiting is announced again: the hostd that
	// carried the first announcement may have restarted before the api
	// acked it, and the api inserts by question_id (DECISIONS I-244).
	for _, q := range s.asks.Pending() {
		s.emitQuestion(q)
	}
}

// ServeConn serves one already-accepted connection. Tests use it; Run uses
// serve.
func (s *Server) ServeConn(ctx context.Context, c net.Conn) {
	s.serve(ctx, c)
	<-ctx.Done()
}

func (s *Server) readRequests(ctx context.Context, conn *vsockrpc.Conn) {
	for {
		env, err := conn.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
				s.log.Warn("connection ended", "event", "ready", "reason", "read_error")
			}
			s.clearConn(conn)
			return
		}
		req := env.GetRequest()
		if req == nil {
			continue
		}
		go s.dispatch(ctx, conn, env.GetRequestId(), req)
	}
}

func (s *Server) clearConn(conn *vsockrpc.Conn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
	_ = conn.Close()
}

// writeNotifications drains the queue onto conn.
func (s *Server) writeNotifications(ctx context.Context, conn *vsockrpc.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case env := <-s.notify:
			if err := conn.Send(env); err != nil {
				// Put it back if there is room; otherwise it is counted as a
				// drop like any other.
				select {
				case s.notify <- env:
				default:
					s.dropped.Add(1)
				}
				return
			}
		}
	}
}

// enqueue puts a notification on the queue, dropping and counting when full.
func (s *Server) enqueue(n *guestdv1.Notify) {
	env := &guestdv1.Envelope{Body: &guestdv1.Envelope_Notify{Notify: n}}
	select {
	case s.notify <- env:
	default:
		dropped := s.dropped.Add(1)
		if dropped == 1 || dropped%NotifyQueue == 0 {
			s.log.Warn("notification queue is full; notifications are being dropped",
				"event", "ready", "dropped", dropped)
		}
	}
}

// Dropped is how many notifications have been dropped for want of a hostd.
func (s *Server) Dropped() uint64 { return s.dropped.Load() }

// Warn queues a Warning notification. It never blocks.
func (s *Server) Warn(kind, detail string) {
	s.enqueue(&guestdv1.Notify{N: &guestdv1.Notify_Warning{
		Warning: &guestdv1.Warning{Kind: kind, Detail: detail},
	}})
}

// AgentState queues an AgentState notification (the sample.Emitter interface).
func (s *Server) AgentState(agent, window, state string) {
	s.enqueue(&guestdv1.Notify{N: &guestdv1.Notify_AgentState{
		AgentState: &guestdv1.AgentState{Agent: agent, TmuxWindow: window, State: state},
	}})
}

// AgentEvent queues an AgentEvent notification (the sample.Emitter interface).
func (s *Server) AgentEvent(agent, window, kind, summary string) {
	s.enqueue(&guestdv1.Notify{N: &guestdv1.Notify_AgentEvent{
		AgentEvent: &guestdv1.AgentEvent{Agent: agent, TmuxWindow: window, Kind: kind, Summary: summary},
	}})
}

// emitQuestion queues a Question notification for an ask that opened, is
// re-announced, or was closed from this side.
func (s *Server) emitQuestion(q questions.Question) {
	state := q.State
	if state == questions.StateOpen {
		state = ""
	}
	s.enqueue(&guestdv1.Notify{N: &guestdv1.Notify_Question{Question: &guestdv1.Question{
		QuestionId: q.ID, Agent: q.Agent, TmuxWindow: q.Window, Text: q.Text,
		Options: q.Options, TimeoutS: q.TimeoutS, State: state,
	}}})
}

// Questions is the ask store (tests).
func (s *Server) Questions() *questions.Store { return s.asks }

// onHook is what the hook socket calls: relay to hostd and fold into the
// agent-state machine so the next Sample agrees with the notification. A
// hook with no window (outside tmux, or a herdr pane no agent is in,
// I-506) is relayed under the agent's name and changes no window's state:
// the tmux window named "claude" is not the Claude in a herdr pane.
func (s *Server) onHook(agent, window, kind, summary string) {
	if window == "" {
		s.AgentEvent(agent, agent, kind, summary)
		return
	}
	s.watcher.RecordHook(window, kind, s.now())
	s.AgentEvent(agent, window, kind, summary)
}

func (s *Server) emitReady() {
	s.enqueue(&guestdv1.Notify{N: &guestdv1.Notify_Ready{Ready: &guestdv1.Ready{BootId: s.bootID}}})
	s.log.Info("guest is ready", "event", "ready")
}

func (s *Server) isReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// readyProbe waits until sshd is listening, then declares the guest ready. A
// Ready sent before sshd accepts connections would make the CLI try to attach
// to a guest that refuses it.
func (s *Server) readyProbe(ctx context.Context) {
	if s.cfg.SkipReadyProbe {
		s.markReady()
		return
	}
	t := time.NewTicker(ReadyPollInterval)
	defer t.Stop()
	for {
		if sshdListening(s.paths) {
			s.markReady()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) markReady() {
	s.mu.Lock()
	already := s.ready
	s.ready = true
	s.mu.Unlock()
	if !already {
		s.emitReady()
	}
}

// sshdListening looks for a listening socket on port 22 in /proc/net/tcp.
func sshdListening(p sysdep.Paths) bool {
	for _, name := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile(p.Proc() + "/net/" + name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			// local_address is host:port in hex; state 0A is LISTEN.
			parts := strings.Split(fields[1], ":")
			if len(parts) != 2 || fields[3] != "0A" {
				continue
			}
			if port, err := strconv.ParseUint(parts[1], 16, 32); err == nil && port == 22 {
				return true
			}
		}
	}
	return false
}

func readBootID(p sysdep.Paths) string {
	b, err := os.ReadFile(p.BootID())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Server) uptimeSeconds() uint64 {
	b, err := os.ReadFile(s.paths.Uptime())
	if err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if secs, err := strconv.ParseFloat(f[0], 64); err == nil {
				return uint64(secs)
			}
		}
	}
	return uint64(s.now().Sub(s.started).Seconds())
}
