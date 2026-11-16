package sample

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/agentprompt"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/multiplexer"
)

// `repose say` (DECISIONS I-1144): the owner types a message for an agent
// and it reaches the agent's own terminal, the way `repose run -w -p`
// does. The watcher owns the delivery because it is the one place that
// knows each window's agent, its multiplexer and the hook state the
// delivery waits for.
//
// The message waits while the agent works. Claude Code queues input typed
// mid-turn, but opencode, codex, gemini and pi do not all behave the
// same, and a message that lands in the middle of a turn and is swallowed
// is worse than one that lands five seconds later. So it goes in only
// while the agent's own hook says idle or needs_input.

// Say outcome states, the enum of SayResult.state.
const (
	// SayDelivered: the text is in the agent's terminal now.
	SayDelivered = "delivered"
	// SayWaiting: the agent is mid-turn; it goes in at the end of it.
	SayWaiting = "waiting"
)

// SayTextCap is the longest message that may be typed into an agent's
// terminal. It is four times an event summary: a message is something a
// person wrote, and a paragraph does not fit in 1 KB.
const SayTextCap = 4 << 10

// SayTries is how many refreshes in a row a waiting message may fail to
// go in before it is dropped. A failure here is tmux refusing the window
// or the agent never taking the text (I-752); retrying forever would
// type into a window the user has moved on from.
const SayTries = 3

// SayRemembered is how many delivered say ids are kept so a command that
// hostd or the api resends does not type the same message twice.
const SayRemembered = 64

// SayRequest is one message for one of this guest's agents.
type SayRequest struct {
	ID string
	// Window is the tmux window or herdr agent key; empty means the
	// guest's single agent window.
	Window string
	Text   string
	Sender string
	// FromOwner says whether Sender is the project's owner. Anyone else
	// is named to the agent, since the agent is about to act on it.
	FromOwner bool
}

// SayOutcome is what became of a SayRequest.
type SayOutcome struct {
	State    string
	Window   string
	Replaced bool
}

// pendingSay is the one message waiting for a window.
type pendingSay struct {
	id     string
	text   string
	sender string
	at     time.Time
	tries  int
}

// SayText is what is typed into the terminal: the message, named when it
// did not come from the project's owner. The owner's own message is the
// message, with nothing in front of it — a prefix on every line the owner
// sends would be noise in their own agent's transcript.
func SayText(r SayRequest) string {
	if r.FromOwner || strings.TrimSpace(r.Sender) == "" {
		return r.Text
	}
	return "[from " + r.Sender + "] " + r.Text
}

// Say takes a message for an agent. It types it in at once when the
// agent's hook state is idle or needs_input, and otherwise leaves it for
// the first refresh at which that is true. One message waits per window:
// a second replaces the first, and the outcome says so, which is what
// the sender is told.
//
// The text is tenant content: it is never logged, and the error paths
// name windows and codes only.
func (w *Watcher) Say(ctx context.Context, r SayRequest) (SayOutcome, error) {
	if strings.TrimSpace(r.Text) == "" {
		return SayOutcome{}, sysdep.Invalid("a message with nothing in it")
	}
	if len(r.Text) > SayTextCap {
		return SayOutcome{}, sysdep.Invalid("a message of %d bytes; at most %d", len(r.Text), SayTextCap)
	}

	w.mu.Lock()
	if out, done := w.saidAlready[r.ID]; done && r.ID != "" {
		w.mu.Unlock()
		return out, nil
	}
	window, err := w.sayWindow(r.Window)
	if err != nil {
		w.mu.Unlock()
		return SayOutcome{}, err
	}
	ws := w.windows[window]
	if ws.source == multiplexer.Herdr {
		w.mu.Unlock()
		// herdr's panes are written through herdr's own socket, not
		// tmux, and guestd only reads from it today.
		return SayOutcome{}, sysdep.Invalid("%s runs under herdr, which repose say does not reach yet", window)
	}
	prev := w.pending[window]
	replaced := prev != nil && prev.id != r.ID
	if w.pending == nil {
		w.pending = map[string]*pendingSay{}
	}
	w.pending[window] = &pendingSay{id: r.ID, text: SayText(r), sender: r.Sender, at: w.now()}
	ready := saySendable(ws.state)
	slug := w.slugs.Slug()
	w.mu.Unlock()

	out := SayOutcome{State: SayWaiting, Window: window, Replaced: replaced}
	if ready && w.deliverSay(ctx, slug, window) {
		out.State = SayDelivered
	}
	w.rememberSay(r.ID, out)
	return out, nil
}

// saySendable reports whether a message may be typed into an agent in
// this state. idle is the agent at its prompt; needs_input is the agent
// at a permission dialog, where typing is how the owner answers. Every
// other state (working, running, unknown) waits.
func saySendable(state string) bool {
	return state == StateIdle || state == StateNeedsInput
}

// sayWindow resolves the window a message is for. The caller holds the
// lock. An empty name means the guest's one agent window; more than one
// is a refusal that names them, so the sender can pick with --agent.
func (w *Watcher) sayWindow(want string) (string, error) {
	var names []string
	for name := range w.windows {
		names = append(names, name)
	}
	sort.Strings(names)
	if want != "" {
		if _, ok := w.windows[want]; !ok {
			if len(names) == 0 {
				return "", sysdep.NotFound("no agent is running on this machine")
			}
			return "", sysdep.NotFound("no agent in window %s; the agents here are %s", want, strings.Join(names, ", "))
		}
		return want, nil
	}
	switch len(names) {
	case 0:
		return "", sysdep.NotFound("no agent is running on this machine")
	case 1:
		return names[0], nil
	default:
		return "", sysdep.Invalid("%d agents are running: %s. Name one with --agent", len(names), strings.Join(names, ", "))
	}
}

// rememberSay keeps a delivered say id so a resent command is answered
// from the record instead of typed again.
func (w *Watcher) rememberSay(id string, out SayOutcome) {
	if id == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.saidAlready == nil {
		w.saidAlready = map[string]SayOutcome{}
		w.saidOrder = nil
	}
	if _, seen := w.saidAlready[id]; !seen {
		w.saidOrder = append(w.saidOrder, id)
	}
	w.saidAlready[id] = out
	for len(w.saidOrder) > SayRemembered {
		delete(w.saidAlready, w.saidOrder[0])
		w.saidOrder = w.saidOrder[1:]
	}
}

// drainSays types in the messages waiting on windows whose agent has
// come back to its prompt. refreshPanes calls it once per refresh, after
// the states it computed are announced, so a turn that ended on this
// refresh delivers on this refresh.
func (w *Watcher) drainSays(ctx context.Context, slug string) {
	w.mu.Lock()
	var ready []string
	for window := range w.pending {
		ws, ok := w.windows[window]
		if !ok {
			// The window closed with the message still waiting.
			delete(w.pending, window)
			w.log.Info("message dropped", "event", "agent_prompted", "window", window, "reason", "window_gone")
			continue
		}
		if saySendable(ws.state) {
			ready = append(ready, window)
		}
	}
	sort.Strings(ready)
	w.mu.Unlock()

	for _, window := range ready {
		w.deliverSay(ctx, slug, window)
	}
}

// deliverSay types the message waiting on one window into its terminal
// and reports whether it went in. On success it emits the agent_prompted
// event, which records the delivery, its sender and its window, and
// never the text. A failure is retried at the next refresh, SayTries
// times, and then dropped with a log line.
func (w *Watcher) deliverSay(ctx context.Context, slug, window string) bool {
	w.mu.Lock()
	p := w.pending[window]
	ws := w.windows[window]
	if p == nil || ws == nil || w.delivering[window] {
		// Already going in: Say delivers straight away when the agent is
		// at its prompt, and the refresh that follows must not type the
		// same message a second time.
		w.mu.Unlock()
		return false
	}
	if w.delivering == nil {
		w.delivering = map[string]bool{}
	}
	w.delivering[window] = true
	agent, text, sender := ws.agent, p.text, p.sender
	w.mu.Unlock()

	script := agentprompt.For(agentprompt.TmuxShell(sayTarget(slug, window)), text)
	res, err := w.tmux.client.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"sh", "-c", script},
		User:      "dev",
		Env:       sysdep.DevEnv(w.paths, "dev"),
		MaxOutput: 4 << 10,
	})
	ok := err == nil && res.ExitCode == 0

	w.mu.Lock()
	delete(w.delivering, window)
	cur := w.pending[window]
	if cur == nil || cur.id != p.id {
		// Replaced while this one was being typed; the newer message
		// stands and this delivery's outcome is forgotten.
		w.mu.Unlock()
		return ok
	}
	if ok {
		delete(w.pending, window)
		w.mu.Unlock()
		w.log.Info("message delivered", "event", "agent_prompted", "agent", agent, "window", window)
		w.emit.AgentEvent(agent, window, KindAgentPrompted, sender)
		return true
	}
	// A failure is retried at the next refresh, SayTries times, unless
	// the agent took the text and did not submit it (I-752's exit 5):
	// typing it again would stack a second copy on the first, so that
	// one is dropped at once.
	notSubmitted := err == nil && res.ExitCode == agentprompt.ExitNotSubmitted
	cur.tries++
	tries := cur.tries
	give := notSubmitted || tries >= SayTries
	if give {
		delete(w.pending, window)
	}
	w.mu.Unlock()
	reason := "tmux_failed"
	if notSubmitted {
		reason = "not_submitted"
	}
	level := w.log.Info
	if give {
		level = w.log.Warn
	}
	level("message not delivered", "event", "agent_prompted", "agent", agent, "window", window,
		"reason", reason, "tries", tries, "dropped", give)
	return false
}

// sayTarget is the tmux target of one agent window, named exactly so a
// prefix never hits another window (as setWindowState spells it).
func sayTarget(slug, window string) string {
	return agentprompt.Quote("=" + slug + ":=" + window)
}

// PendingSays is how many messages wait in this guest, for tests.
func (w *Watcher) PendingSays() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending)
}
