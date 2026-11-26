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

	"github.com/heracraft/repose/internal/agentproc"
	"github.com/heracraft/repose/internal/guestd/questions"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/multiplexer"
	"golang.org/x/sys/unix"
)

// WriteWait is the hook socket's write deadline for a route that does
// not set its own. The two ask routes that can outlast it do set their
// own: the long poll (its wait and 5 s) and the open (OpenWait).
var WriteWait = 10 * time.Second

// OpenWait is how long guestd may spend opening a question before the
// write deadline cuts the answer off. Opening one is real work -- the
// window lookup alone is capped at 2 s -- and on a loaded single-core
// guest WriteWait is not certainly longer than it. A cut-off answer is
// ENG-105 from guestd's side: guestd holds a question the owner will
// answer and the asker is never told its id (DECISIONS I-1404).
var OpenWait = 30 * time.Second

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

// KindSessionStart and KindSessionEnd are Claude Code's SessionStart and
// SessionEnd (DECISIONS I-1676): an agent with no terminal enters the
// agent registry at its start and leaves it at its end. They never leave
// the guest, and a hook from a tmux window or herdr pane of either kind
// changes nothing, since the window is the record there.
const (
	KindSessionStart = "session_start"
	KindSessionEnd   = "session_end"
)

// Kinds an agent may report.
var kinds = map[string]bool{"completed": true, "needs_input": true, "error": true, KindPrompt: true, KindResume: true, KindSessionStart: true, KindSessionEnd: true}

// agents that may report. A wrapper for an agent not in this list is a
// packaging mistake, and saying so is more useful than accepting it.
var agents = map[string]bool{"claude": true, "opencode": true, "codex": true, "gemini": true, "pi": true}

// Payload is what a wrapper POSTs. session_id and inbox are what
// repose-hook read from the agent's own payload and environment
// (DECISIONS I-1676); an older repose-hook sends neither.
type Payload struct {
	Agent     string `json:"agent"`
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
	Window    string `json:"window,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Inbox     string `json:"inbox,omitempty"`
}

// AgentHook is a hook from an agent the socket found no terminal for
// (DECISIONS I-1676): Proc is its process as "pid:starttime". Summary is
// set for the kinds that are events (completed, needs_input, error) and
// empty for the rest.
type AgentHook struct {
	Agent, Kind, Summary, Proc, SessionID, Inbox string
}

// AgentSink takes a hook from an agent with no terminal.
type AgentSink func(AgentHook)

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
	// secretRelay says whether the connected hostd relays a secret
	// question's name (I-929); nil, or false, refuses `--secret`.
	secretRelay func() bool
	// linkSink takes a notify with a link (I-931).
	linkSink LinkSink
	// procRoot is where the caller's process tree is read, /proc on a
	// guest.
	procRoot string
	// presence backs /presence once SetPresence is called (I-1098).
	presence Presence
	// agentSink takes the hooks of agents with no terminal once
	// SetAgentSink is called (I-1676); without it they are relayed as
	// before, under the agent's name.
	agentSink AgentSink
}

// SetAgentSink routes the hooks of agents with no terminal to f.
func (s *Server) SetAgentSink(f AgentSink) { s.agentSink = f }

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
		WriteTimeout:      WriteWait,
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

	if p.Window == "" && s.agentSink != nil {
		// Resolved once: the paths below take the window as given.
		if p.Window = s.windowOfCaller(r); p.Window == "" {
			pid, _ := r.Context().Value(peerKey{}).(int)
			if proc := agentOrAncestor(s.procRoot, pid, p.Agent); proc != "" {
				s.handleWindowless(w, p, proc)
				return
			}
		}
	}
	if p.Kind == KindSessionStart || p.Kind == KindSessionEnd {
		// A window's agent is its window; a session hook changes nothing.
		w.WriteHeader(http.StatusNoContent)
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

// handleWindowless takes a hook from an agent with no terminal: a prompt
// closes the questions asked from its process, and the hook goes to the
// agent sink. The prompt's text is dropped unread, as handlePrompt drops
// it.
func (s *Server) handleWindowless(w http.ResponseWriter, p Payload, proc string) {
	h := AgentHook{Agent: p.Agent, Kind: p.Kind, Proc: proc, SessionID: p.SessionID, Inbox: p.Inbox}
	closed := 0
	switch p.Kind {
	case KindPrompt:
		if s.questions != nil {
			closed = s.questions.CloseWhere(askedFrom(proc, ""), questions.ReasonTerminal)
		}
	case KindResume, KindSessionStart, KindSessionEnd:
	default:
		h.Summary = truncate(p.Summary, SummaryCap)
	}
	s.agentSink(h)
	s.log.Info("agent hook received", "event", "agent_event", "agent", p.Agent, "kind", p.Kind,
		"windowless", true, "summary_bytes", len(h.Summary), "questions_closed", closed)
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
		if agentproc.IsAgentCommand(agent, comm) || agentproc.IsAgentCommand(agent, exeName(root, pid)) {
			return strconv.Itoa(pid) + ":" + f[19]
		}
		pid, _ = strconv.Atoi(f[1])
	}
	return ""
}

// passThrough are the process names between an agent and its hook that
// are not the agent: the shell Claude Code runs a hook command with
// (`sh -c`, which does not exec; measured on Claude Code 2.1.331), and
// the wrappers a user may put around a command.
var passThrough = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true, "ash": true, "ksh": true,
	"env": true, "nice": true, "nohup": true, "timeout": true, "setsid": true, "stdbuf": true, "flock": true,
	"repose-hook": true, ".repose-hook-wr": true, "repose-notify": true, "repose-ask": true,
}

// stopAt are the process names that are never an agent and above which
// none is: a hook run from a login shell, a tmux server, or a service is
// no agent's.
var stopAt = map[string]bool{
	"sshd": true, "sshd-session": true, "systemd": true, "init": true, "(sd-pam)": true, "login": true,
	"su": true, "sudo": true, "runuser": true, "tmux: server": true, "tmux": true, "herdr": true,
	"guestd": true, "cron": true, "crond": true,
}

// agentOrAncestor is agentProc, and, when no ancestor is the agent's
// program, the nearest ancestor above the caller that is neither a
// shell nor a wrapper: that is how Claude Desktop's CLI is found, whose
// binary is named for its version (`ccd-cli/2.1.330-3cc8a8679c34`,
// ENG-255). "" when the walk meets a login, a multiplexer or a service
// first. It reads stat only.
func agentOrAncestor(root string, pid int, agent string) string {
	if p := agentProc(root, pid, agent); p != "" {
		return p
	}
	if !agents[agent] || pid <= 1 {
		return ""
	}
	_, ppid, _, ok := readProcStat(root, pid)
	if !ok {
		return ""
	}
	pid = ppid // the caller itself is the hook command
	for i := 0; i < 32 && pid > 1; i++ {
		comm, ppid, start, ok := readProcStat(root, pid)
		if !ok || stopAt[comm] {
			return ""
		}
		if !passThrough[comm] {
			return strconv.Itoa(pid) + ":" + start
		}
		pid = ppid
	}
	return ""
}

// readProcStat is a process's comm, parent and start time from its stat
// file.
func readProcStat(root string, pid int) (comm string, ppid int, start string, ok bool) {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, "", false
	}
	st := string(b)
	open, end := strings.IndexByte(st, '('), strings.LastIndexByte(st, ')')
	if open < 0 || end < open {
		return "", 0, "", false
	}
	f := strings.Fields(st[end+1:])
	if len(f) < 20 {
		return "", 0, "", false
	}
	ppid, err = strconv.Atoi(f[1])
	if err != nil {
		return "", 0, "", false
	}
	return st[open+1 : end], ppid, f[19], true
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
