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
	"github.com/heracraft/repose/internal/guestd/sample"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/multiplexer"
	"golang.org/x/sys/unix"
)

// SummaryCap is the cap on a hook summary (docs/interfaces/grpc-hostd.md caps
// the AgentEvent summary at 1 KB).
const SummaryCap = 1 << 10

// KindPrompt is the socket kind of a prompt-submit hook (DECISIONS I-690):
// the user sent the agent a prompt. It never leaves the guest, and the
// prompt's text is never read: guestd closes the open questions asked
// from that agent process and marks the agent working.
const KindPrompt = "prompt"

// KindResume is the socket kind of a turn the agent starts by itself:
// Claude Code's turn after a background task finished, whose prompt is
// its own <task-notification> (DECISIONS I-688, I-690). It never leaves
// the guest and closes no question; guestd marks the agent working.
const KindResume = "resume"

// Kinds an agent may report.
var kinds = map[string]bool{"completed": true, "needs_input": true, "error": true, KindPrompt: true, KindResume: true}

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
// window or herdr agent key, or "" when the payload named none (or a herdr
// pane no agent is in) and the caller is not in a tmux pane.
type Sink func(agent, window, kind, summary string)

// WindowResolver maps a tmux pane id to a window name.
type WindowResolver func(ctx context.Context, pane string) (string, error)

// HerdrResolver maps a herdr pane id (a hook's "herdr:<pane_id>" window,
// DECISIONS I-506) to the agent key guestd reports; ok is false when no
// agent is in that pane.
type HerdrResolver func(ctx context.Context, paneID string) (key string, ok bool)

// Server serves the hook socket.
type Server struct {
	path     string
	sink     Sink
	resolve  WindowResolver
	herdr    HerdrResolver
	log      *slog.Logger
	gid      int
	srv      *http.Server
	mux      *http.ServeMux
	listener net.Listener
	// questions backs /notify and /ask once EnableAsk is called (I-244).
	questions *questions.Store
	msgSink   Sink
	// procRoot is where the caller's process tree is read, /proc on a
	// guest.
	procRoot string
}

// NewServer builds the hook server. gid owns the socket's group, so the dev
// user can write to it and nothing else can.
func NewServer(path string, gid int, sink Sink, resolve WindowResolver, log *slog.Logger) *Server {
	s := &Server{path: path, sink: sink, resolve: resolve, log: log, gid: gid, procRoot: "/proc"}
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

// SetProcRoot reads callers' process trees under root instead of /proc
// (tests, and guestd's own Root).
func (s *Server) SetProcRoot(root string) { s.procRoot = root }

// SetHerdrResolver resolves "herdr:" windows; without it every such window
// is unresolved.
func (s *Server) SetHerdrResolver(r HerdrResolver) { s.herdr = r }

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

	if p.Kind == KindPrompt {
		s.handlePrompt(w, r, p)
		return
	}
	if p.Kind == KindResume {
		s.sink(p.Agent, s.windowOf(r, p.Window), KindResume, "")
		s.log.Info("agent resumed", "event", "agent_event", "agent", p.Agent, "kind", KindResume)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	summary := truncate(p.Summary, SummaryCap)
	// Empty when the caller is in no pane guestd can name (a script, a
	// herdr pane no agent is in): the sink relays the event under the
	// agent's name and leaves every window's state alone, since a window
	// named after the agent may be another agent entirely.
	window := s.windowOf(r, p.Window)

	s.sink(p.Agent, window, p.Kind, summary)
	s.log.Info("agent hook received",
		"event", "agent_event", "agent", p.Agent, "kind", p.Kind, "summary_bytes", len(summary))
	w.WriteHeader(http.StatusNoContent)
}

// handlePrompt closes the questions the prompted agent asked (reason
// terminal) and tells the sink, with no summary: whatever text the
// payload carries is dropped unread.
func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request, p Payload) {
	window := s.windowOf(r, p.Window)
	closed := 0
	if s.questions != nil {
		match := askedFrom(s.agentProcOf(r, p.Agent), window)
		closed = s.questions.CloseWhere(match, questions.ReasonTerminal)
	}
	s.sink(p.Agent, window, KindPrompt, "")
	s.log.Info("agent prompt received", "event", "agent_event", "agent", p.Agent, "kind", KindPrompt, "questions_closed", closed)
	w.WriteHeader(http.StatusNoContent)
}

// askedFrom matches the open questions asked from one agent process (proc,
// "pid:starttime"), and, where either side has no agent process, from
// one window. A question asked from no agent and no window matches
// nothing.
func askedFrom(proc, window string) func(questions.Question) bool {
	return func(q questions.Question) bool {
		if proc != "" && q.AgentProc == proc {
			return true
		}
		return (proc == "" || q.AgentProc == "") && window != "" && q.Window == window
	}
}

// agentProcOf is the request's caller's agent process key.
func (s *Server) agentProcOf(r *http.Request, agent string) string {
	pid, _ := r.Context().Value(peerKey{}).(int)
	return agentProc(s.procRoot, pid, agent)
}

// agentProc walks from pid up its parents to the nearest process that is
// agent's program and returns it as "pid:starttime", so a reused pid
// never matches; "" when there is none or agent is not one of the five.
// It reads stat and comm only: a process name, never arguments or
// environment.
func agentProc(root string, pid int, agent string) string {
	if !agents[agent] {
		return ""
	}
	for i := 0; i < 32 && pid > 1; i++ {
		b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
		if err != nil {
			return ""
		}
		st := string(b)
		open, end := strings.IndexByte(st, '('), strings.LastIndexByte(st, ')')
		if open < 0 || end < open {
			return ""
		}
		comm := st[open+1 : end]
		f := strings.Fields(st[end+1:])
		if len(f) < 20 {
			return ""
		}
		if sample.IsAgentCommand(agent, comm) || sample.IsAgentCommand(agent, exeName(root, pid)) {
			return strconv.Itoa(pid) + ":" + f[19]
		}
		pid, _ = strconv.Atoi(f[1])
	}
	return ""
}

// exeName is the basename of a process's exe link: Gemini CLI is node,
// whose main thread's comm is MainThread.
func exeName(root string, pid int) string {
	l, err := os.Readlink(filepath.Join(root, strconv.Itoa(pid), "exe"))
	if err != nil {
		return ""
	}
	return filepath.Base(l)
}

// windowOf is the window a hook, a notify or an ask is recorded under: a
// "herdr:<pane_id>" window resolved to its agent's key ("" when no agent
// is in that pane, I-506), another given window as is, and with none the
// caller's tmux window.
func (s *Server) windowOf(r *http.Request, given string) string {
	if ref, ok := strings.CutPrefix(given, multiplexer.HerdrWindowPrefix); ok {
		if s.herdr == nil {
			return ""
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		key, ok := s.herdr(ctx, ref)
		if !ok {
			s.log.Debug("a hook's herdr pane has no agent", "event", "agent_event")
			return ""
		}
		return key
	}
	if given != "" {
		return given
	}
	return s.windowOfCaller(r)
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
