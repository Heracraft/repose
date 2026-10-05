// Package hooks is the agent hook ingest: an HTTP-over-unix socket at
// /run/repose/hooks.sock that agent wrappers POST to, relayed to hostd as an
// AgentEvent.
//
// A hook body is agent output. It is carried to hostd because that is the
// product feature, and it is never written to a log line: the rejection path
// logs a reason, never the body.
package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/guestd/questions"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"golang.org/x/sys/unix"
)

// SummaryCap is the cap on a hook summary (docs/interfaces/grpc-hostd.md caps
// the AgentEvent summary at 1 KB).
const SummaryCap = 1 << 10

// Kinds an agent may report.
var kinds = map[string]bool{"completed": true, "needs_input": true, "error": true}

// agents that may report. A wrapper for an agent not in this list is a
// packaging mistake, and saying so is more useful than accepting it.
var agents = map[string]bool{"claude": true, "opencode": true, "codex": true, "gemini": true, "pi": true}

// Payload is what a wrapper POSTs.
type Payload struct {
	Agent   string `json:"agent"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Window  string `json:"window,omitempty"`
}

// Sink receives a validated hook event. window is the caller's tmux
// window, or "" when the payload named none and the caller is not in a
// tmux pane.
type Sink func(agent, window, kind, summary string)

// WindowResolver maps a tmux pane id to a window name.
type WindowResolver func(ctx context.Context, pane string) (string, error)

// Server serves the hook socket.
type Server struct {
	path     string
	sink     Sink
	resolve  WindowResolver
	log      *slog.Logger
	gid      int
	srv      *http.Server
	mux      *http.ServeMux
	listener net.Listener
	// questions backs /notify and /ask once EnableAsk is called (I-244).
	questions *questions.Store
	msgSink   Sink
}

// NewServer builds the hook server. gid owns the socket's group, so the dev
// user can write to it and nothing else can.
func NewServer(path string, gid int, sink Sink, resolve WindowResolver, log *slog.Logger) *Server {
	s := &Server{path: path, sink: sink, resolve: resolve, log: log, gid: gid}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.mux = mux
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		ConnContext:       s.connContext,
	}
	return s
}

// Listen creates the socket at 0660 root:dev.
func (s *Server) Listen() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create hook socket directory: %w", err)
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale hook socket: %w", err)
	}
	l, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("listen on the hook socket: %w", err)
	}
	if err := os.Chown(s.path, 0, s.gid); err != nil && !os.IsPermission(err) {
		_ = l.Close()
		return fmt.Errorf("chown the hook socket: %w", err)
	}
	if err := os.Chmod(s.path, 0o660); err != nil {
		_ = l.Close()
		return fmt.Errorf("chmod the hook socket: %w", err)
	}
	s.listener = l
	return nil
}

// Addr is the socket path, after Listen.
func (s *Server) Addr() string { return s.path }

// Serve runs until Close.
func (s *Server) Serve() error {
	if s.listener == nil {
		return errors.New("hook server: Listen was not called")
	}
	err := s.srv.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops the server and removes the socket.
func (s *Server) Close(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if rmErr := os.Remove(s.path); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
		err = fmt.Errorf("remove the hook socket: %w", rmErr)
	}
	return err
}

type peerKey struct{}

func (s *Server) connContext(ctx context.Context, c net.Conn) context.Context {
	pid := peerPID(c)
	return context.WithValue(ctx, peerKey{}, pid)
}

// peerPID reads the credentials of the process on the other end of the socket.
func peerPID(c net.Conn) int {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	var pid int
	err = raw.Control(func(fd uintptr) {
		cred, credErr := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if credErr == nil {
			pid = int(cred.Pid)
		}
	})
	if err != nil {
		return 0
	}
	return pid
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.reject(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	body := http.MaxBytesReader(w, r.Body, 64<<10)
	var p Payload
	if err := json.NewDecoder(body).Decode(&p); err != nil {
		s.reject(w, r, http.StatusBadRequest, "malformed_json")
		return
	}
	switch {
	case !agents[p.Agent]:
		s.reject(w, r, http.StatusBadRequest, "unknown_agent")
		return
	case !kinds[p.Kind]:
		s.reject(w, r, http.StatusBadRequest, "unknown_kind")
		return
	}

	summary := truncate(p.Summary, SummaryCap)
	window := p.Window
	if window == "" {
		// Empty when the caller is not in a tmux pane (a herdr pane, a
		// script): the sink relays the event under the agent's name and
		// leaves every tmux window's state alone, since a window named
		// after the agent may be another agent entirely.
		window = s.windowOfCaller(r)
	}

	s.sink(p.Agent, window, p.Kind, summary)
	s.log.Info("agent hook received",
		"event", "agent_event", "agent", p.Agent, "kind", p.Kind, "summary_bytes", len(summary))
	w.WriteHeader(http.StatusNoContent)
}

// windowOfCaller finds the caller's tmux window from $TMUX_PANE in its
// environment. This is the only environment guestd reads and the only variable
// it takes from it; docs/SECURITY.md records the exception.
func (s *Server) windowOfCaller(r *http.Request) string {
	pid, _ := r.Context().Value(peerKey{}).(int)
	if pid <= 0 || s.resolve == nil {
		return ""
	}
	pane := tmuxPaneOf(pid)
	if pane == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	name, err := s.resolve(ctx, pane)
	if err != nil {
		s.log.Debug("could not resolve the hook's tmux pane",
			"event", "agent_event", "error_code", sysdep.CodeOf(err))
		return ""
	}
	return name
}

// tmuxPaneOf reads TMUX_PANE, and only TMUX_PANE, from a process environment.
func tmuxPaneOf(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return ""
	}
	for _, entry := range strings.Split(string(b), "\x00") {
		if v, ok := strings.CutPrefix(entry, "TMUX_PANE="); ok {
			return v
		}
	}
	return ""
}

// reject answers the wrapper and logs why, never what.
func (s *Server) reject(w http.ResponseWriter, _ *http.Request, status int, reason string) {
	s.log.Warn("hook rejected", "event", "hook_bad_payload", "reason", reason)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(reason + "\n"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
