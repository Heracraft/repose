// Package fakeguestd is the unix-socket stand-in for a guest's guestd, per
// docs/interfaces/vsock-guestd.md. hostd's tests point the vsock dialer at
// it; it records every request, answers with canned results, sends Ready
// on connect, and runs the 10 s freeze watchdog like the real daemon.
package fakeguestd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/vsockrpc"
)

// Call is one recorded request.
type Call struct {
	Kind string
	Req  *guestdv1.Request
}

// Options tune the fake.
type Options struct {
	// ReadyDelay is how long after a connection Ready is sent; a negative
	// value never sends it (a guest that never boots).
	ReadyDelay time.Duration
	// FreezeWatchdog is the self-thaw timeout; zero means the real 10 s.
	FreezeWatchdog time.Duration
	// NeedsReboot lists closures whose Switch answers needs_reboot.
	NeedsReboot map[string]bool
	// Fail makes a request kind answer with the given error.
	Fail map[string]*guestdv1.Error
	// DropOnce closes the connection instead of answering the first request
	// of that kind (the shape of guestd restarted by an activation, I-148);
	// the next connection answers normally.
	DropOnce map[string]bool
	// Delay holds a request kind's answer back for that long (a guest
	// whose SetupProject waits on a slow home-manager activation).
	Delay map[string]time.Duration
	// Sample is the canned Sample reply; nil gives a small default.
	Sample *guestdv1.SampleResult
	// Version and BootID for Ping.
	Version string
	BootID  string
	// OnFreeze and OnThaw run when Freeze and Thaw arrive (a host check
	// freezes a real filesystem with them); an error fails the request.
	OnFreeze, OnThaw func() error
}

// Server is one fake guest listening on a unix socket.
type Server struct {
	path string
	opts Options
	ln   net.Listener

	mu       sync.Mutex
	calls    []Call
	dropped  map[string]bool
	secrets  map[string][]byte
	princ    []string
	frozen   bool
	conn     *vsockrpc.ServerConn
	closed   bool
	stopped  chan struct{}
	shutdown func()
	wg       sync.WaitGroup
}

// Listen starts a fake guestd on path.
func Listen(path string, opts Options) (*Server, error) {
	_ = os.Remove(path) // a stale socket from a previous run; Listen reports the real error if it is not one
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("fake guestd listen: %w", err)
	}
	if opts.FreezeWatchdog == 0 {
		opts.FreezeWatchdog = 10 * time.Second
	}
	if opts.Version == "" {
		opts.Version = "fake"
	}
	if opts.BootID == "" {
		opts.BootID = "boot-1"
	}
	s := &Server{path: path, opts: opts, ln: ln, secrets: map[string][]byte{}, stopped: make(chan struct{})}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Path is the socket path.
func (s *Server) Path() string { return s.path }

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.conn != nil {
			_ = s.conn.Close() // one connection at a time; the older one is dropped, as the contract says
		}
		sc := vsockrpc.Serve(context.Background(), c, s)
		s.conn = sc
		s.mu.Unlock()
		if s.opts.ReadyDelay >= 0 {
			go func() {
				time.Sleep(s.opts.ReadyDelay)
				_ = sc.Notify(&guestdv1.Notify{N: &guestdv1.Notify_Ready{Ready: &guestdv1.Ready{BootId: s.opts.BootID}}}) // the peer may be gone; nothing to do
			}()
		}
	}
}

// Close stops the fake and removes the socket.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	s.mu.Unlock()
	err := s.ln.Close()
	if conn != nil {
		_ = conn.Close() // ending the fake; the peer sees EOF, which is the point
	}
	s.wg.Wait()
	_ = os.Remove(s.path) // best effort cleanup of the socket file
	return err
}

// Notify pushes an unsolicited message, as a real guestd would for agent
// events and warnings.
func (s *Server) Notify(n *guestdv1.Notify) error {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		return errors.New("fake guestd: no connection")
	}
	return c.Notify(n)
}

// Calls returns the recorded requests.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Kinds returns just the recorded request kinds, in order.
func (s *Server) Kinds() []string {
	var out []string
	for _, c := range s.Calls() {
		out = append(out, c.Kind)
	}
	return out
}

// reservedName is guestd's sshd material (internal/guestd/secrets), which a
// WriteSecrets without it does not remove.
var reservedName = map[string]bool{"ssh_host_ed25519_key": true, "ssh_host_ed25519_key-cert.pub": true, "user_ca.pub": true}

// Secrets returns what WriteSecrets delivered.
func (s *Server) Secrets() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range s.secrets {
		out[k] = v
	}
	return out
}

// Principals returns what SetPrincipals delivered.
func (s *Server) Principals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.princ...)
}

// Frozen reports whether the fake filesystem is frozen.
func (s *Server) Frozen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frozen
}

// ShutdownCalled is closed when a Shutdown request arrived.
func (s *Server) ShutdownCalled() <-chan struct{} { return s.stopped }

// OnShutdown registers a callback run when Shutdown arrives (tests use it
// to mark the fake unit as exited).
func (s *Server) OnShutdown(f func()) {
	s.mu.Lock()
	s.shutdown = f
	s.mu.Unlock()
}

func kindOf(req *guestdv1.Request) string {
	switch req.Req.(type) {
	case *guestdv1.Request_Ping:
		return "Ping"
	case *guestdv1.Request_Freeze:
		return "Freeze"
	case *guestdv1.Request_Thaw:
		return "Thaw"
	case *guestdv1.Request_Switch:
		return "Switch"
	case *guestdv1.Request_RegisterPaths:
		return "RegisterPaths"
	case *guestdv1.Request_AnswerQuestion:
		return "AnswerQuestion"
	case *guestdv1.Request_GrowFs:
		return "GrowFs"
	case *guestdv1.Request_WriteSecrets:
		return "WriteSecrets"
	case *guestdv1.Request_SetPrincipals:
		return "SetPrincipals"
	case *guestdv1.Request_SetupProject:
		return "SetupProject"
	case *guestdv1.Request_Sample:
		return "Sample"
	case *guestdv1.Request_Exec:
		return "Exec"
	case *guestdv1.Request_Shutdown:
		return "Shutdown"
	}
	return "unknown"
}

func fail(code, msg string) *guestdv1.Response {
	return &guestdv1.Response{Ok: false, Error: &guestdv1.Error{Code: code, Message: msg}}
}

// Handle implements vsockrpc.Handler.
func (s *Server) Handle(_ context.Context, req *guestdv1.Request) *guestdv1.Response {
	kind := kindOf(req)
	s.mu.Lock()
	s.calls = append(s.calls, Call{Kind: kind, Req: req})
	if e := s.opts.Fail[kind]; e != nil {
		s.mu.Unlock()
		return &guestdv1.Response{Ok: false, Error: e}
	}
	if s.opts.DropOnce[kind] && !s.dropped[kind] {
		if s.dropped == nil {
			s.dropped = map[string]bool{}
		}
		s.dropped[kind] = true
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close() // the peer sees EOF mid-call, as with a restarted guestd
		}
		return fail("dropped", "connection closed by the fake")
	}
	d := s.opts.Delay[kind]
	s.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	ok := &guestdv1.Response{Ok: true}
	switch r := req.Req.(type) {
	case *guestdv1.Request_Ping:
		ok.Result = &guestdv1.Response_Ping{Ping: &guestdv1.PingResult{Version: s.opts.Version, UptimeS: 1, BootId: s.opts.BootID}}
	case *guestdv1.Request_Freeze:
		if s.opts.OnFreeze != nil {
			if err := s.opts.OnFreeze(); err != nil {
				return fail("internal", err.Error())
			}
		}
		s.mu.Lock()
		s.frozen = true
		s.mu.Unlock()
		go func() {
			time.Sleep(s.opts.FreezeWatchdog)
			s.mu.Lock()
			wasFrozen := s.frozen
			s.frozen = false
			c := s.conn
			s.mu.Unlock()
			if wasFrozen && c != nil {
				_ = c.Notify(&guestdv1.Notify{N: &guestdv1.Notify_Warning{Warning: &guestdv1.Warning{Kind: "freeze_timeout", Detail: "thawed by watchdog"}}}) // peer may be gone
			}
		}()
	case *guestdv1.Request_Thaw:
		if s.opts.OnThaw != nil {
			if err := s.opts.OnThaw(); err != nil {
				return fail("internal", err.Error())
			}
		}
		s.mu.Lock()
		s.frozen = false
		s.mu.Unlock()
	case *guestdv1.Request_Switch:
		if s.opts.NeedsReboot[r.Switch.SystemClosure] && !r.Switch.ForceReboot {
			ok.Result = &guestdv1.Response_Switch{Switch: &guestdv1.SwitchResult{NeedsReboot: true, Output: []byte("kernel changed")}}
		} else {
			ok.Result = &guestdv1.Response_Switch{Switch: &guestdv1.SwitchResult{Rebooted: r.Switch.ForceReboot, Output: []byte("activating the configuration...\n")}}
		}
	case *guestdv1.Request_RegisterPaths:
		// nothing to record beyond the call itself
	case *guestdv1.Request_GrowFs:
		ok.Result = &guestdv1.Response_GrowFs{GrowFs: &guestdv1.GrowFsResult{NewBytes: 1 << 30}}
	case *guestdv1.Request_WriteSecrets:
		// The list is the whole named set, as in guestd: a name not in it
		// goes, the sshd material stays until replaced.
		s.mu.Lock()
		for name := range s.secrets {
			if !reservedName[name] {
				delete(s.secrets, name)
			}
		}
		for _, sec := range r.WriteSecrets.Secrets {
			s.secrets[sec.Name] = sec.Value
		}
		s.mu.Unlock()
	case *guestdv1.Request_SetPrincipals:
		s.mu.Lock()
		s.princ = append([]string(nil), r.SetPrincipals.Principals...)
		s.mu.Unlock()
	case *guestdv1.Request_SetupProject:
		if r.SetupProject.ProjectSlug == "" {
			return fail("invalid_argument", "project_slug required")
		}
	case *guestdv1.Request_Sample:
		sm := s.opts.Sample
		if sm == nil {
			sm = &guestdv1.SampleResult{
				Signals: &hostdv1.GuestSignals{SshSessions: 1, TmuxClients: 1, DockerContainers: 0, GuestdOk: true,
					Agents: []*hostdv1.AgentProc{{Agent: "claude", TmuxWindow: "claude", State: "working"}}},
				Procs: []*hostdv1.ProcSample{{Comm: "claude", CpuNsDelta: 500_000_000, RssBytes: 200 << 20}},
			}
		}
		ok.Result = &guestdv1.Response_Sample{Sample: sm}
	case *guestdv1.Request_Exec:
		if len(r.Exec.Argv) == 0 {
			return fail("invalid_argument", "argv required")
		}
		ok.Result = &guestdv1.Response_Exec{Exec: &guestdv1.ExecResult{ExitCode: 0, Stdout: []byte("fake exec: " + r.Exec.Argv[0] + "\n")}}
	case *guestdv1.Request_Shutdown:
		s.mu.Lock()
		f := s.shutdown
		select {
		case <-s.stopped:
		default:
			close(s.stopped)
		}
		s.mu.Unlock()
		if f != nil {
			go f()
		}
	case *guestdv1.Request_AnswerQuestion:
		if r.AnswerQuestion.QuestionId == "" {
			return fail("invalid_argument", "question_id required")
		}
	default:
		return fail("invalid_argument", "unknown request")
	}
	return ok
}
