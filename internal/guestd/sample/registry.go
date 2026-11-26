package sample

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/multiplexer"
)

// The agent registry (DECISIONS I-1676): the agents that run with no
// terminal repose can see, Claude Desktop's "Open in Claude" first among
// them (ENG-255). A record is one agent process, keyed "pid:starttime",
// made by the agent's own hooks and removed by its SessionEnd hook or
// when its process is gone. tmux windows and herdr panes stay the
// watcher's windows; a record whose process turns out to sit in an agent
// window is dropped, since the window already lists it.

// Hook kinds that only the registry reads. They never leave the guest.
const (
	// KindSessionStart is Claude Code's SessionStart: the agent is up
	// and waits at its prompt.
	KindSessionStart = "session_start"
	// KindSessionEnd is Claude Code's SessionEnd: the agent is gone.
	KindSessionEnd = "session_end"
)

// agentsFile keeps the registry across a guestd restart, beside
// hooksFile in the run directory (a tmpfs).
const agentsFile = "agents.json"

// MaxAgentRecords caps the registry. A guest that reports more agents
// than this is reporting noise; the oldest records stay.
const MaxAgentRecords = 32

// WindowlessQuietAfter is how long a working agent with no terminal may
// go without a hook and without its process tree using CPU before it
// counts as idle. A window's agent has its pane's output for this
// (PromptQuietAfter); a windowless one has only its CPU, which a turn
// waiting on the model barely moves, so the wait is longer.
const WindowlessQuietAfter = 10 * time.Minute

// procKeyShape is "pid:starttime" as the hook socket makes it.
var procKeyShape = regexp.MustCompile(`^[1-9][0-9]{0,9}:[0-9]{1,20}$`)

// sessionShape is an agent's own conversation id (a UUID, Codex's thread
// id), the shape repose-hook already accepts (I-636).
var sessionShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// AgentHook is one hook from an agent the hook socket found no terminal
// for: its process key, its agent, the hook kind, and what the hook's
// payload said about the session. Never the prompt, never the summary.
type AgentHook struct {
	Agent string
	Kind  string
	// Proc is the agent's process as "pid:starttime".
	Proc string
	// SessionID is the agent's own conversation id, or "".
	SessionID string
	// Inbox is Claude Code's per-session socket path from its hook
	// environment (CLAUDE_CODE_MESSAGING_SOCKET), or "". Kept for `say`
	// (proposal item 7); never sent off the guest.
	Inbox string
}

type agentRecord struct {
	ID        string    `json:"id"`
	Agent     string    `json:"agent"`
	SessionID string    `json:"session_id,omitempty"`
	Inbox     string    `json:"inbox,omitempty"`
	Kind      string    `json:"kind"`
	At        time.Time `json:"at"`

	pid        int
	start      string
	state      string
	stateSince time.Time
	announced  string
	lastCPU    uint64
	lastActive time.Time
}

// AgentIDEmitter is an Emitter that also carries an agent's id with its
// state (DECISIONS I-1676). guestd's server is one; an Emitter without
// it gets the windowless agent's state with an empty window.
type AgentIDEmitter interface {
	AgentStateOf(agent, window, agentID, state string)
}

func splitProcKey(id string) (int, string, bool) {
	if !procKeyShape.MatchString(id) {
		return 0, "", false
	}
	pidS, start, _ := strings.Cut(id, ":")
	pid, err := strconv.Atoi(pidS)
	if err != nil {
		return 0, "", false
	}
	return pid, start, true
}

// loadAgents reads the registry a previous guestd kept. Each record is
// checked against /proc at the next refresh, so a process that ended
// while guestd was down is dropped there.
func (w *Watcher) loadAgents() {
	b, err := os.ReadFile(filepath.Join(w.paths.RunDir(), agentsFile))
	if err != nil {
		return
	}
	var recs []*agentRecord
	if json.Unmarshal(b, &recs) != nil {
		return
	}
	now := w.now()
	for _, r := range recs {
		if r == nil || len(w.agents) >= MaxAgentRecords {
			continue
		}
		pid, start, ok := splitProcKey(r.ID)
		if !ok || !agentNames[r.Agent] {
			continue
		}
		r.pid, r.start = pid, start
		r.state, r.stateSince, r.lastActive = recordState(r.Kind), now, now
		w.agents[r.ID] = r
	}
}

// saveAgents keeps the registry for the next guestd; called with mu held.
func (w *Watcher) saveAgents() {
	recs := make([]*agentRecord, 0, len(w.agents))
	for _, r := range w.agents {
		recs = append(recs, r)
	}
	b, _ := json.Marshal(recs)
	dir := w.paths.RunDir()
	tmp := filepath.Join(dir, agentsFile+".tmp")
	if os.MkdirAll(dir, 0o755) != nil || os.WriteFile(tmp, b, 0o600) != nil || os.Rename(tmp, filepath.Join(dir, agentsFile)) != nil {
		w.log.Debug("could not keep the agent registry", "event", "agent_state", "reason", "write")
	}
}

// agentNames are the five agents a hook may name (the hook socket checks
// it first; this is the registry's own guard for the file it reads).
var agentNames = map[string]bool{"claude": true, "opencode": true, "codex": true, "gemini": true, "pi": true}

// recordState is the state a registry record's last hook leaves.
func recordState(kind string) string {
	if kind == KindNeedsInput {
		return StateNeedsInput
	}
	return hookState(kind)
}

// RecordAgentHook folds a hook from an agent with no terminal into the
// registry: SessionStart, or the first hook of any kind from a machine
// whose settings have no SessionStart, makes the record; every hook
// moves its state as a window's hook does; SessionEnd removes it.
func (w *Watcher) RecordAgentHook(h AgentHook, at time.Time) {
	pid, start, ok := splitProcKey(h.Proc)
	if !ok || !agentNames[h.Agent] {
		return
	}
	var gone *agentRecord
	w.mu.Lock()
	r := w.agents[h.Proc]
	switch {
	case h.Kind == KindSessionEnd:
		if r != nil {
			delete(w.agents, h.Proc)
			gone = r
			w.saveAgents()
		}
		w.mu.Unlock()
		if gone != nil && gone.announced != "" && gone.announced != StateUnknown {
			w.announce(gone.Agent, gone.ID, StateUnknown)
		}
		return
	case r == nil:
		if len(w.agents) >= MaxAgentRecords {
			w.mu.Unlock()
			w.log.Warn("too many agents with no terminal; this one is not listed", "event", "agent_state", "agent", h.Agent)
			return
		}
		r = &agentRecord{ID: h.Proc, Agent: h.Agent, pid: pid, start: start, stateSince: at, lastActive: at}
		w.agents[h.Proc] = r
	}
	if sessionShape.MatchString(h.SessionID) {
		r.SessionID = h.SessionID
	}
	if validInbox(h.Inbox) {
		r.Inbox = h.Inbox
	}
	r.Kind, r.At = h.Kind, at
	if st := recordState(h.Kind); st != r.state {
		r.state, r.stateSince = st, at
	}
	w.saveAgents()
	w.mu.Unlock()
}

// validInbox accepts an absolute socket path of a sane length with no
// control bytes. It is the agent's own environment, so it is checked
// before guestd keeps it.
func validInbox(p string) bool {
	if p == "" || len(p) > 255 || !filepath.IsAbs(p) {
		return false
	}
	return !strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func (w *Watcher) announce(agent, id, state string) {
	w.log.Info("agent state changed", "event", "agent_state", "agent", agent, "state", state)
	if e, ok := w.emit.(AgentIDEmitter); ok {
		e.AgentStateOf(agent, "", id, state)
		return
	}
	w.emit.AgentState(agent, "", state)
}

// refreshAgents is the registry's part of a refresh: a record whose
// process is gone, or whose pid now runs another process, is removed; a
// record whose process sits in an agent window's pane is dropped, since
// the window lists it; a working record whose process tree has been
// still for WindowlessQuietAfter goes idle. It reads stat files only.
func (w *Watcher) refreshAgents() {
	w.mu.Lock()
	if len(w.agents) == 0 {
		w.mu.Unlock()
		return
	}
	panes := w.agentPanes
	w.mu.Unlock()

	children, _ := w.procs.childIndex()
	now := w.now()
	type emission struct{ agent, id, state string }
	var out []emission

	w.mu.Lock()
	changed := false
	for id, r := range w.agents {
		if w.procStart(r.pid) != r.start {
			delete(w.agents, id)
			changed = true
			if r.announced != "" && r.announced != StateUnknown {
				out = append(out, emission{r.Agent, id, StateUnknown})
			}
			continue
		}
		if w.inAgentPane(r.pid, panes) {
			// The window lists it, under its window name.
			delete(w.agents, id)
			changed = true
			continue
		}
		if cpu, ok := w.procs.treeCPU(children, r.pid); ok {
			if cpu > r.lastCPU {
				r.lastActive = now
			}
			r.lastCPU = cpu
		}
		if recordState(r.Kind) == StateWorking {
			last := r.At
			if r.lastActive.After(last) {
				last = r.lastActive
			}
			if now.Sub(last) >= WindowlessQuietAfter {
				r.Kind, r.At = kindQuiet, now
				changed = true
			}
		}
		if st := recordState(r.Kind); st != r.state {
			r.state, r.stateSince = st, now
		}
		if r.state != r.announced && now.Sub(r.stateSince) >= StateDebounce {
			r.announced = r.state
			out = append(out, emission{r.Agent, id, r.state})
		}
	}
	if changed {
		w.saveAgents()
	}
	w.mu.Unlock()
	for _, e := range out {
		w.announce(e.agent, e.id, e.state)
	}
}

// procStart is field 22 of /proc/<pid>/stat, the process's start time in
// clock ticks since boot, or "" when there is no such process.
func (w *Watcher) procStart(pid int) string {
	_, start, ok := w.statParentStart(pid)
	if !ok {
		return ""
	}
	return start
}

// statParentStart reads a process's parent and start time from its stat
// file, and nothing else.
func (w *Watcher) statParentStart(pid int) (int, string, bool) {
	b, err := os.ReadFile(filepath.Join(w.paths.ProcPID(strconv.Itoa(pid)), "stat"))
	if err != nil {
		return 0, "", false
	}
	end := bytes.LastIndexByte(b, ')')
	if end < 0 {
		return 0, "", false
	}
	f := bytes.Fields(b[end+1:])
	if len(f) < 20 {
		return 0, "", false
	}
	ppid, err := strconv.Atoi(string(f[1]))
	if err != nil {
		return 0, "", false
	}
	return ppid, string(f[19]), true
}

// inAgentPane reports whether pid is an agent window's pane process or
// one of its descendants, walking up at most 32 parents.
func (w *Watcher) inAgentPane(pid int, panes map[int]string) bool {
	if len(panes) == 0 {
		return false
	}
	for i := 0; i < 32 && pid > 1; i++ {
		if _, ok := panes[pid]; ok {
			return true
		}
		ppid, _, ok := w.statParentStart(pid)
		if !ok {
			return false
		}
		pid = ppid
	}
	return false
}

// agentIDIn is the "pid:starttime" of the agent's own process in a pane's
// tree, the shallowest one whose name is one of the agent's binaries, or
// "" when none is found.
func (w *Watcher) agentIDIn(children map[int][]int, root int, agent string) string {
	for pid := range w.procs.agentPIDs(children, map[int]string{root: agent}) {
		if start := w.procStart(pid); start != "" {
			return strconv.Itoa(pid) + ":" + start
		}
	}
	return ""
}

// windowProc is what Signals reports for a window's agent.
func windowProc(name string, ws *windowState) *hostdv1.AgentProc {
	terminal := multiplexer.Tmux + ":" + name
	if ws.source == multiplexer.Herdr {
		terminal = multiplexer.Herdr + ":" + name
	}
	id := ws.agentID
	if id == "" {
		id = terminal
	}
	return &hostdv1.AgentProc{Agent: ws.agent, TmuxWindow: name, State: ws.state, AgentId: id, Terminal: terminal}
}

// registryProcs is the registry's part of Signals; called with mu held.
func (w *Watcher) registryProcs() []*hostdv1.AgentProc {
	out := make([]*hostdv1.AgentProc, 0, len(w.agents))
	for id, r := range w.agents {
		out = append(out, &hostdv1.AgentProc{Agent: r.Agent, State: r.state, AgentId: id})
	}
	return out
}

// AgentSession is the conversation id the registry holds for an agent
// process, or "" (tests, and the restart reopen of proposal item 8).
func (w *Watcher) AgentSession(id string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r := w.agents[id]; r != nil {
		return r.SessionID
	}
	return ""
}

// registryPIDs is every registry record's pid, for the OOM priority
// (I-200): an agent with no terminal is protected as a pane's is.
func (w *Watcher) registryPIDs() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]int, 0, len(w.agents))
	for _, r := range w.agents {
		out = append(out, r.pid)
	}
	return out
}
