package sample

import (
	"context"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/heracraft/repose/internal/multiplexer"
)

// Pane is one agent terminal as a multiplexer reports it, cut down to what
// guestd may keep (DECISIONS R5-3): no cwd, no title, no argv.
type Pane struct {
	// Key is what guestd reports as the agent's window: the tmux window
	// name, or for herdr the agent's name, else "<agent> <pane_id>",
	// prefixed "<checkout>/" for another checkout's workspace (I-504).
	Key string
	// Agent is one of Agents; a source drops every other pane.
	Agent string
	// Ref is the herdr pane id a hook names ("herdr:<Ref>", I-506); ""
	// for tmux.
	Ref string
	// RootPID is the tmux pane's pid; 0 when the source reports the state.
	RootPID int
	// Command is tmux's pane_current_command (a process name).
	Command string
	// Activity is tmux's window_activity, unix seconds.
	Activity int64
	// Reported is the state the multiplexer reports (herdr), one of the
	// State constants; "" means guestd computes it from CPU (tmux).
	Reported string
	// Done says herdr saw a turn end since the previous read.
	Done bool
}

// Source is one multiplexer's agent discovery. Both run on every machine
// and the watcher takes the union (DECISIONS I-504); a source whose server
// is absent costs a stat.
type Source interface {
	// Name is multiplexer.Tmux or multiplexer.Herdr.
	Name() string
	// Panes lists the agent panes. up reports whether the server answered.
	// An error means the list could not be read and the previous panes
	// stand; it carries no tenant text.
	Panes(ctx context.Context) (panes []Pane, up bool, err error)
	// Resolve maps a hook's reference (a herdr pane id) to its key.
	Resolve(ctx context.Context, ref string) (key string, ok bool)
	Close()
}

// tmuxSource is the project's tmux session behind Source. It stats dev's
// tmux socket before it forks tmux, so a herdr machine pays no fork.
type tmuxSource struct {
	client tmuxClient
	slugs  SlugSource
	uid    int
}

func (t *tmuxSource) Name() string { return multiplexer.Tmux }

func (t *tmuxSource) Close() {}

// socketPresent reports whether dev's tmux server could be listening.
func (t *tmuxSource) socketPresent() bool {
	_, err := os.Stat(t.client.paths.TmuxSocket(t.uid))
	return err == nil
}

// Panes lists the session's windows whose name or foreground program is an
// agent's. The process-tree check is the watcher's (it holds the child
// index), and runs because RootPID is set.
func (t *tmuxSource) Panes(ctx context.Context) ([]Pane, bool, error) {
	if !t.socketPresent() {
		return nil, false, nil
	}
	windows, up, err := t.client.listWindows(ctx, t.slugs.Slug())
	if err != nil || !up {
		return nil, up, err
	}
	var out []Pane
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
		out = append(out, Pane{
			Key: win.Name, Agent: agent, RootPID: win.PanePID,
			Command: win.PaneCommand, Activity: win.LastActivity,
		})
	}
	return out, true, nil
}

// clients counts attached tmux clients; GuestSignals.tmux_clients stays
// tmux-only (a herdr client arrives over SSH and is in ssh_sessions).
func (t *tmuxSource) clients(ctx context.Context) (uint32, error) {
	n, _, err := t.client.listClients(ctx, t.slugs.Slug())
	return n, err
}

// Resolve maps a tmux pane id ($TMUX_PANE) to its window name.
func (t *tmuxSource) Resolve(ctx context.Context, ref string) (string, bool) {
	if !t.socketPresent() {
		return "", false
	}
	name, err := t.client.windowOfPane(ctx, ref)
	if err != nil || name == "" {
		return "", false
	}
	return name, true
}

// herdrSuffix marks a herdr key that a tmux window already holds in the
// same refresh (guest-conventions "herdr", Agent keys).
const herdrSuffix = " (herdr)"

// capKey cuts a key to multiplexer.MaxKey bytes on a rune boundary, after
// dropping control characters and invalid UTF-8, so it fits hostd's
// capWindow unchanged.
func capKey(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// disambiguate gives a herdr key that a tmux window holds the " (herdr)"
// suffix, keeping the result within MaxKey.
func disambiguate(key string, tmuxKeys map[string]bool) string {
	if !tmuxKeys[key] {
		return key
	}
	return capKey(key, multiplexer.MaxKey-len(herdrSuffix)) + herdrSuffix
}

// ValidHerdrRef reports whether a hook's herdr pane id may be looked up:
// at most MaxKey-len("herdr:") bytes of [A-Za-z0-9:_-] (I-506). Anything
// else is unresolved.
func ValidHerdrRef(ref string) bool {
	if ref == "" || len(ref) > multiplexer.MaxKey-len(multiplexer.HerdrWindowPrefix) {
		return false
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == ':', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// ensure the sources satisfy the interface.
var (
	_ Source = (*tmuxSource)(nil)
	_ Source = (*herdrSource)(nil)
)
