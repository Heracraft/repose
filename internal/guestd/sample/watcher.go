package sample

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/multiplexer"
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

// Warning kinds this package produces. tmux_down and herdr_down are each
// sent only when project.json names that multiplexer (DECISIONS I-507).
const (
	WarnTmuxDown   = "tmux_down"
	WarnHerdrDown  = "herdr_down"
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

// MultiplexerSource is a SlugSource that also says which multiplexer
// project.json names (internal/multiplexer values, normalized). A
// SlugSource without it means tmux.
type MultiplexerSource interface {
	SlugSource
	Multiplexer() string
}

type hookRecord struct {
	kind string
	at   time.Time
}

type windowState struct {
	agent      string
	windowName string
	// source is the multiplexer that reported the pane.
	source string
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
	tmux   *tmuxSource
	herdr  *herdrSource
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
	// herdrKeys is each herdr pane id's key at the last refresh, and
	// tmuxKeys the tmux windows' names, for hook resolution (I-506).
	herdrKeys map[string]string
	tmuxKeys  map[string]bool
	// warned remembers which one-shot warnings have been sent, so tmux_down
	// and docker_down are announced once rather than every five seconds.
	warned map[string]bool
}

// NewWatcher builds a watcher. now may be nil for time.Now.
func NewWatcher(p sysdep.Paths, run sysdep.Runner, docker sysdep.Docker, slugs SlugSource, emit Emitter, log *slog.Logger, now func() time.Time) *Watcher {
	if now == nil {
		now = time.Now
	}
	uid, _ := sysdep.DevIdentity()
	return &Watcher{
		paths:   p,
		tmux:    &tmuxSource{client: tmuxClient{paths: p, run: run}, slugs: slugs, uid: uid},
		herdr:   newHerdrSource(p, uid, log, now),
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

// SetHerdrSocket points the herdr source at another socket path, and when
// uid is above 0 another peer uid (tests, where a temp root makes the
// conventional path too long for sun_path and the test user is not dev).
func (w *Watcher) SetHerdrSocket(path string, uid int) {
	w.herdr.mu.Lock()
	defer w.herdr.mu.Unlock()
	w.herdr.socket = path
	if uid > 0 {
		w.herdr.uid = uid
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
	w.refreshPanes(ctx)
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
	agents := w.procs.agentPIDs(children, panes)
	// herdr's server and the agents in its tree (I-505). The tree is
	// walked whatever project.json says: a server started by hand is
	// protected the same way.
	servers := w.procs.herdrServers(devUID)
	for pid := range w.procs.herdrAgentPIDs(children, servers) {
		agents[pid] = true
	}
	if changes := w.procs.applyOOM(devUID, agents, servers); len(changes) > 0 {
		w.log.Debug("oom priority applied", "event", "oom_priority", "changed", len(changes))
	}
	if n := w.procs.applyNice(children, servers); n > 0 {
		w.log.Debug("herdr priority applied", "event", "oom_priority", "changed", n)
	}
}

// multiplexer is what project.json names, tmux when the slug source cannot
// say.
func (w *Watcher) multiplexer() string {
	if m, ok := w.slugs.(MultiplexerSource); ok {
		return multiplexer.Normalize(m.Multiplexer())
	}
	return multiplexer.Tmux
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

// refreshPanes takes the union of both sources' agent panes (DECISIONS
// I-504) and runs the agent-state machine over it.
func (w *Watcher) refreshPanes(ctx context.Context) {
	if w.slugs.Slug() == "" {
		w.mu.Lock()
		w.refreshed = w.now()
		w.mu.Unlock()
		return
	}
	mux := w.multiplexer()

	tmuxPanes, tmuxUp, tmuxErr := w.tmux.Panes(ctx)
	if tmuxErr != nil {
		// The reason is in the error's message, which carries no session name.
		w.log.Warn("could not list tmux windows",
			"event", "agent_state", "error_code", sysdep.CodeOf(tmuxErr), "reason", tmuxErr.Error())
	}
	clients := uint32(0)
	if tmuxUp && tmuxErr == nil {
		var err error
		if clients, err = w.tmux.clients(ctx); err != nil {
			w.log.Warn("could not list tmux clients",
				"event", "agent_state", "error_code", sysdep.CodeOf(err), "reason", err.Error())
		}
	}
	herdrPanes, herdrUp, _ := w.herdr.Panes(ctx)

	if tmuxErr == nil {
		w.oneShot(WarnTmuxDown, mux == multiplexer.Tmux && !tmuxUp, "no tmux server is running for dev")
	}
	w.oneShot(WarnHerdrDown, mux == multiplexer.Herdr && !herdrUp && w.herdr.Misses() >= HerdrDownAfter,
		"herdr's socket did not answer")

	// One child index per refresh, shared by every window's tree walk.
	children, _ := w.procs.childIndex()

	now := w.now()
	type emission struct {
		agent, window, state, kind, summary string
	}
	var states []emission
	var events []emission

	w.mu.Lock()
	if tmuxErr == nil {
		// A tmux that could not be read leaves the cache to age, so a
		// Sample goes partial instead of reporting stale windows as fresh.
		w.tmuxUp = tmuxUp
		w.clients = clients
		w.refreshed = now
	}

	live := map[string]bool{}
	panes := map[int]string{} // an agent window's pane pid -> its agent
	tmuxKeys := map[string]bool{}
	var all []Pane
	for _, p := range tmuxPanes {
		if !w.procs.treeHasAnyComm(children, p.RootPID, binaries[p.Agent]) {
			// A window named after an agent whose process is not running is
			// not an agent window; the user renamed a shell.
			continue
		}
		tmuxKeys[p.Key] = true
		all = append(all, p)
	}
	herdrKeys := map[string]string{}
	for _, p := range herdrPanes {
		p.Key = disambiguate(p.Key, tmuxKeys)
		if live[p.Key] {
			continue // two herdr agents with one name: the first is reported
		}
		live[p.Key] = true
		herdrKeys[p.Ref] = p.Key
		all = append(all, p)
	}
	for k := range tmuxKeys {
		live[k] = true
	}

	for _, p := range all {
		source := multiplexer.Tmux
		if p.RootPID == 0 {
			source = multiplexer.Herdr
		}
		ws := w.windows[p.Key]
		if ws == nil || ws.source != source {
			ws = &windowState{agent: p.Agent, windowName: p.Key, source: source, lastActive: now, stateSince: now}
			w.windows[p.Key] = ws
		}
		busy := false
		if p.RootPID > 0 {
			panes[p.RootPID] = p.Agent
			cpu, ok := w.procs.treeCPU(children, p.RootPID)
			busy = ok && cpu > ws.lastCPU
			if busy {
				ws.lastCPU = cpu
				ws.lastActive = now
			} else if ok {
				ws.lastCPU = cpu
			}
			if p.Activity > ws.lastActivity {
				ws.lastActivity = p.Activity
				ws.heuristicSent = false
			}
		}

		state := w.computeState(ws, busy, p.Reported, now)
		if state != ws.state {
			ws.state = state
			ws.stateSince = now
		}
		if ws.state != ws.announced && now.Sub(ws.stateSince) >= StateDebounce {
			ws.announced = ws.state
			states = append(states, emission{agent: p.Agent, window: p.Key, state: ws.state})
		}

		if p.RootPID > 0 {
			if ev, summary, ok := w.heuristicCompletion(ws, p, now); ok {
				events = append(events, emission{agent: p.Agent, window: p.Key, kind: ev, summary: summary})
			}
		} else if p.Done && !HookedAgents[p.Agent] {
			// herdr saw the turn end (state_change_seq moved to idle or
			// done). Hooked agents report it through their hook, so
			// nothing arrives twice.
			events = append(events, emission{agent: p.Agent, window: p.Key, kind: KindCompleted, summary: p.Agent + " went idle"})
		}
	}
	for name, ws := range w.windows {
		if live[name] {
			continue
		}
		if tmuxErr != nil && ws.source == multiplexer.Tmux {
			continue // tmux could not be read; its windows stand
		}
		// The window is gone. Say so once, then forget it.
		if ws.announced != StateUnknown {
			states = append(states, emission{agent: ws.agent, window: name, state: StateUnknown})
		}
		delete(w.windows, name)
		delete(w.hooks, name)
	}
	if tmuxErr == nil {
		w.agentPanes = panes
		w.tmuxKeys = tmuxKeys
	}
	w.herdrKeys = herdrKeys
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

// computeState is the state machine of docs/workstreams/04-guestd.md §5. A
// state the multiplexer reports (herdr) is taken as is; a needs_input hook
// still wins.
func (w *Watcher) computeState(ws *windowState, busy bool, reported string, now time.Time) string {
	if rec, ok := w.hooks[keyFor(ws)]; ok && rec.kind == KindNeedsInput {
		return StateNeedsInput
	}
	if reported != "" {
		return reported
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
// completion hook in a tmux window: a quiet pane whose foreground process is
// still the agent has finished its turn, and the notification says "went
// idle" rather than "finished" so the user knows which mechanism spoke.
func (w *Watcher) heuristicCompletion(ws *windowState, p Pane, now time.Time) (kind, summary string, ok bool) {
	if HookedAgents[ws.agent] || ws.heuristicSent {
		return "", "", false
	}
	if p.Command != "" && !isAgentCommand(ws.agent, p.Command) {
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

// ResolveHerdr maps a hook's herdr pane id to the key its agent is reported
// under (DECISIONS I-506): the last refresh's key, else a fresh read of
// herdr's agents. ok is false for a pane no agent is in, and for an id that
// is too long or holds a character outside [A-Za-z0-9:_-].
func (w *Watcher) ResolveHerdr(ctx context.Context, ref string) (string, bool) {
	if !ValidHerdrRef(ref) {
		return "", false
	}
	w.mu.Lock()
	key, ok := w.herdrKeys[ref]
	tmuxKeys := w.tmuxKeys
	w.mu.Unlock()
	if ok {
		return key, true
	}
	base, ok := w.herdr.Resolve(ctx, ref)
	if !ok {
		return "", false
	}
	return disambiguate(base, tmuxKeys), true
}

// WindowOfTmuxPane resolves a tmux pane id ($TMUX_PANE) to its window name.
func (w *Watcher) WindowOfTmuxPane(ctx context.Context, pane string) (string, error) {
	return w.tmux.client.windowOfPane(ctx, pane)
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
