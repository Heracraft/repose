package sample

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// Agent states, the enum of docs/interfaces/grpc-hostd.md's AgentProc.state.
const (
	StateWorking    = "working"
	StateIdle       = "idle"
	StateNeedsInput = "needs_input"
	StateUnknown    = "unknown"
)

// Agent event kinds, the enum of the AgentEvent notify.
const (
	KindCompleted  = "completed"
	KindNeedsInput = "needs_input"
	KindError      = "error"
)

// Warning kinds this package produces.
const (
	WarnTmuxDown   = "tmux_down"
	WarnDockerDown = "docker_down"
)

// Timings of the agent-state machine (docs/workstreams/04-guestd.md §5).
const (
	// Interval is how often the watcher refreshes tmux and docker. Sample
	// serves these signals from the watcher's cache rather than forking tmux
	// itself, which is what keeps a sample under the 20 ms budget.
	Interval = 5 * time.Second
	// IdleAfter is how long without CPU makes an agent idle rather than
	// unknown.
	IdleAfter = 30 * time.Second
	// HeuristicIdleAfter is how long without pane output makes a hookless
	// agent's turn count as finished (features/agents.md).
	HeuristicIdleAfter = 90 * time.Second
	// StateDebounce is how long a new state must hold before it is announced.
	StateDebounce = 5 * time.Second
	// CacheStale is how old the cache may be before Sample reports partial.
	CacheStale = 3 * Interval
	// DockerGrace is how long after guestd starts a Docker socket that does
	// not answer is still Docker starting rather than Docker down. guestd no
	// longer waits for docker.service at boot (DECISIONS I-161), and dockerd
	// takes 2 to 3 s to answer on a small guest.
	DockerGrace = 60 * time.Second
)

// Emitter receives the watcher's notifications. The server's notify queue
// implements it and must not block.
type Emitter interface {
	AgentState(agent, window, state string)
	AgentEvent(agent, window, kind, summary string)
	Warn(kind, detail string)
}

// SlugSource gives the watcher the current project slug, which is the tmux
// session name. It changes at SetupProject.
type SlugSource interface{ Slug() string }

type hookRecord struct {
	kind string
	at   time.Time
}

type windowState struct {
	agent      string
	windowName string
	// cumulative CPU of the pane's process tree at the last refresh
	lastCPU uint64
	// when the tree last consumed CPU
	lastActive time.Time
	// last tmux-reported pane activity, unix seconds
	lastActivity int64
	// current computed state and when it was first computed
	state      string
	stateSince time.Time
	// the last state announced to hostd
	announced string
	// whether the heuristic completion for this quiet period has been sent
	heuristicSent bool
}

// Watcher keeps the guest's signals fresh in the background so that a Sample
// is a read of memory plus one /proc walk.
type Watcher struct {
	paths  sysdep.Paths
	tmux   tmuxClient
	procs  *procReader
	docker sysdep.Docker
	slugs  SlugSource
	emit   Emitter
	log    *slog.Logger
	now    func() time.Time

	mu         sync.Mutex
	windows    map[string]*windowState
	hooks      map[string]hookRecord
	clients    uint32
	containers uint32
	dockerUp   bool
	tmuxUp     bool
	refreshed  time.Time
	// started is when the watcher was built; docker_down waits DockerGrace
	// from it unless the socket has answered once already.
	started    time.Time
	dockerSeen bool
	// agentPanes is the agent windows' pane pids and their agents, for the
	// OOM priority (I-200).
	agentPanes map[int]string
	// warned remembers which one-shot warnings have been sent, so tmux_down
	// and docker_down are announced once rather than every five seconds.
	warned map[string]bool
}

// NewWatcher builds a watcher. now may be nil for time.Now.
func NewWatcher(p sysdep.Paths, run sysdep.Runner, docker sysdep.Docker, slugs SlugSource, emit Emitter, log *slog.Logger, now func() time.Time) *Watcher {
	if now == nil {
		now = time.Now
	}
	return &Watcher{
		paths:   p,
		tmux:    tmuxClient{paths: p, run: run},
		procs:   newProcReader(p),
		docker:  docker,
		slugs:   slugs,
		emit:    emit,
		log:     log,
		now:     now,
		windows: map[string]*windowState{},
		hooks:   map[string]hookRecord{},
		warned:  map[string]bool{},
		started: now(),
	}
}

// Run refreshes until ctx is done.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	w.Refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Refresh(ctx)
		}
	}
}

// Refresh does one pass. It is exported so tests drive it without a ticker.
func (w *Watcher) Refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, Interval)
	defer cancel()

	w.refreshDocker(ctx)
	w.refreshTmux(ctx)
	w.refreshProcs()
}

// refreshProcs re-applies the OOM priorities (I-200) every refresh, so a
// dev server an agent started is back to 0 within one interval. It runs
// whether or not the project's tmux session exists.
func (w *Watcher) refreshProcs() {
	w.mu.Lock()
	panes := w.agentPanes
	w.mu.Unlock()

	children, _ := w.procs.childIndex()
	devUID, _ := sysdep.DevIdentity()
	if changes := w.procs.applyOOM(devUID, w.procs.agentPIDs(children, panes)); len(changes) > 0 {
		w.log.Debug("oom priority applied", "event", "oom_priority", "changed", len(changes))
	}
}

func (w *Watcher) refreshDocker(ctx context.Context) {
	n, err := w.docker.RunningContainers(ctx)
	w.mu.Lock()
	if err != nil {
		w.dockerUp = false
		w.containers = 0
	} else {
		w.dockerUp = true
		w.dockerSeen = true
		w.containers = uint32(n)
	}
	up := w.dockerUp
	starting := !w.dockerSeen && w.now().Sub(w.started) < DockerGrace
	w.mu.Unlock()
	if !up && starting {
		return
	}
	w.oneShot(WarnDockerDown, !up, "the docker socket did not answer")
}

func (w *Watcher) refreshTmux(ctx context.Context) {
	session := w.slugs.Slug()
	if session == "" {
		w.mu.Lock()
		w.refreshed = w.now()
		w.mu.Unlock()
		return
	}

	windows, serverUp, err := w.tmux.listWindows(ctx, session)
	if err != nil {
		// The reason is in the error's message, which carries no session name.
		w.log.Warn("could not list tmux windows",
			"event", "agent_state", "error_code", sysdep.CodeOf(err), "reason", err.Error())
		return
	}
	clients := uint32(0)
	if serverUp {
		clients, _, err = w.tmux.listClients(ctx, session)
		if err != nil {
			w.log.Warn("could not list tmux clients",
				"event", "agent_state", "error_code", sysdep.CodeOf(err), "reason", err.Error())
		}
	}

	w.oneShot(WarnTmuxDown, !serverUp, "no tmux server is running for dev")

	// One child index per refresh, shared by every window's tree walk.
	children, _ := w.procs.childIndex()

	now := w.now()
	type emission struct {
		agent, window, state, kind, summary string
	}
	var states []emission
	var events []emission

	w.mu.Lock()
	w.tmuxUp = serverUp
	w.clients = clients
	w.refreshed = now

	live := make(map[string]bool, len(windows))
	panes := map[int]string{} // an agent window's pane pid -> its agent
	for _, win := range windows {
		agent := AgentOf(win.Name)
		if agent == "" {
			// A window with another name counts while an agent is its
			// foreground program: `claude` typed in the shell window
			// (I-421). It stops counting when the agent exits.
			if agent = AgentByCommand(win.PaneCommand); agent == "" {
				continue
			}
		}
		if !w.procs.treeHasAnyComm(children, win.PanePID, binaries[agent]) {
			// A window named after an agent whose process is not running is
			// not an agent window; the user renamed a shell.
			continue
		}
		live[win.Name] = true
		panes[win.PanePID] = agent

		ws := w.windows[win.Name]
		if ws == nil {
			ws = &windowState{agent: agent, windowName: win.Name, lastActive: now, stateSince: now}
			w.windows[win.Name] = ws
		}
		cpu, ok := w.procs.treeCPU(children, win.PanePID)
		busy := ok && cpu > ws.lastCPU
		if busy {
			ws.lastCPU = cpu
			ws.lastActive = now
		} else if ok {
			ws.lastCPU = cpu
		}
		if win.LastActivity > ws.lastActivity {
			ws.lastActivity = win.LastActivity
			ws.heuristicSent = false
		}

		state := w.computeState(ws, busy, now)
		if state != ws.state {
			ws.state = state
			ws.stateSince = now
		}
		if ws.state != ws.announced && now.Sub(ws.stateSince) >= StateDebounce {
			ws.announced = ws.state
			states = append(states, emission{agent: agent, window: win.Name, state: ws.state})
		}

		if ev, summary, ok := w.heuristicCompletion(ws, win, now); ok {
			events = append(events, emission{agent: agent, window: win.Name, kind: ev, summary: summary})
		}
	}
	for name, ws := range w.windows {
		if live[name] {
			continue
		}
		// The window is gone. Say so once, then forget it.
		if ws.announced != StateUnknown {
			states = append(states, emission{agent: ws.agent, window: name, state: StateUnknown})
		}
		delete(w.windows, name)
		delete(w.hooks, name)
	}
	w.mu.Unlock()

	// The OOM priority covers every pane of the session, not only each
	// window's active one (DECISIONS I-200): an agent in a split pane, or
	// in an inactive pane of the shell window, is protected too. The
	// windows' own panes above stay in, so a failed list-panes protects no
	// less than before.
	if serverUp {
		all, err := w.tmux.listPanes(ctx, session)
		if err != nil {
			w.log.Warn("could not list tmux panes",
				"event", "agent_state", "error_code", sysdep.CodeOf(err), "reason", err.Error())
		}
		for _, pane := range all {
			if _, ok := panes[pane.PID]; ok {
				continue
			}
			for _, agent := range []string{AgentOf(pane.Window), AgentByCommand(pane.Command)} {
				if agent != "" && w.procs.treeHasAnyComm(children, pane.PID, binaries[agent]) {
					panes[pane.PID] = agent
					break
				}
			}
		}
	}

	w.mu.Lock()
	w.agentPanes = panes
	w.mu.Unlock()

	for _, e := range states {
		w.log.Info("agent state changed", "event", "agent_state", "agent", e.agent, "state", e.state)
		w.emit.AgentState(e.agent, e.window, e.state)
	}
	for _, e := range events {
		w.log.Info("agent event", "event", "agent_event", "agent", e.agent, "kind", e.kind)
		w.emit.AgentEvent(e.agent, e.window, e.kind, e.summary)
	}
}

// computeState is the state machine of docs/workstreams/04-guestd.md §5.
func (w *Watcher) computeState(ws *windowState, busy bool, now time.Time) string {
	if rec, ok := w.hooks[keyFor(ws)]; ok && rec.kind == KindNeedsInput {
		return StateNeedsInput
	}
	switch {
	case busy:
		return StateWorking
	case now.Sub(ws.lastActive) >= IdleAfter:
		return StateIdle
	default:
		return StateUnknown
	}
}

// keyFor is the hook map key: the window name the hook reported.
func keyFor(ws *windowState) string { return ws.windowName }

// heuristicCompletion implements features/agents.md for agents with no
// completion hook: a quiet pane whose foreground process is still the agent
// has finished its turn, and the notification says "went idle" rather than
// "finished" so the user knows which mechanism spoke.
func (w *Watcher) heuristicCompletion(ws *windowState, win tmuxWindow, now time.Time) (kind, summary string, ok bool) {
	if HookedAgents[ws.agent] || ws.heuristicSent {
		return "", "", false
	}
	if win.PaneCommand != "" && !isAgentCommand(ws.agent, win.PaneCommand) {
		return "", "", false
	}
	quiet := now.Sub(ws.lastActive)
	if ws.lastActivity > 0 {
		quiet = now.Sub(time.Unix(ws.lastActivity, 0))
	}
	if quiet < HeuristicIdleAfter {
		return "", "", false
	}
	ws.heuristicSent = true
	return KindCompleted, ws.agent + " went idle", true
}

// oneShot sends a warning the first time a condition becomes true and rearms
// when it clears.
func (w *Watcher) oneShot(kind string, bad bool, detail string) {
	w.mu.Lock()
	was := w.warned[kind]
	w.warned[kind] = bad
	w.mu.Unlock()
	if bad && !was {
		w.log.Warn(detail, "event", "warning", "kind", kind)
		w.emit.Warn(kind, detail)
	}
}

// RecordHook folds an agent hook event into the state machine, so that a
// needs_input hook shows as needs_input in the next sample and a completion
// clears it.
func (w *Watcher) RecordHook(window, kind string, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if window == "" {
		return
	}
	w.hooks[window] = hookRecord{kind: kind, at: at}
	if ws, ok := w.windows[window]; ok && kind != KindNeedsInput {
		// A completion or error ends the waiting state immediately rather
		// than at the next refresh.
		ws.state = StateIdle
		ws.stateSince = at
	}
}

// Signals returns the cached guest signals and whether the cache is fresh.
func (w *Watcher) Signals() (*hostdv1.GuestSignals, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	sig := &hostdv1.GuestSignals{
		TmuxClients:      w.clients,
		DockerContainers: w.containers,
		GuestdOk:         true,
	}
	for name, ws := range w.windows {
		sig.Agents = append(sig.Agents, &hostdv1.AgentProc{
			Agent: ws.agent, TmuxWindow: name, State: ws.state,
		})
	}
	sort.Slice(sig.Agents, func(i, j int) bool {
		return sig.Agents[i].GetTmuxWindow() < sig.Agents[j].GetTmuxWindow()
	})
	fresh := !w.refreshed.IsZero() && w.now().Sub(w.refreshed) <= CacheStale
	return sig, fresh
}
