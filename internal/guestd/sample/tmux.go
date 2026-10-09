package sample

import (
	"context"
	"strconv"
	"strings"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// Agents are the window names that mean an agent, from
// docs/interfaces/guest-conventions.md. Further instances of an agent get
// "<agent>-N" for any N >= 2 (DECISIONS I-253), which AgentOf also
// recognises.
var Agents = []string{"claude", "opencode", "codex", "gemini", "pi"}

// binaries maps an agent to the process name to look for in the pane's
// process tree (features/agents.md's "Binary" column).
var binaries = map[string][]string{
	"claude":   {"claude"},
	"opencode": {"opencode"},
	"codex":    {"codex"},
	// Gemini CLI is a bundle the guest's node runs (DECISIONS I-46), so
	// its process is `node`, never `gemini`; without that name here the
	// window was never an agent window and the heuristic never fired on
	// host-01 (I-122).
	"gemini": {"gemini", "node"},
	"pi":     {"pi"},
}

// isAgentCommand reports whether comm is one of the process names the
// agent runs as.
func isAgentCommand(agent, comm string) bool {
	return matchesBinary(binaries[agent], comm)
}

// matchesBinary reports whether a process name (comm, or the exe link's
// basename) is one of wants, directly or as nixpkgs' wrapper renames it:
// makeWrapper moves the real program to `.X-wrapped`, so the nix claude
// runs as `.claude-wrapped` (comm and exe alike), and comm is cut to 15
// bytes (`.opencode-wrapp`). Without this the guest's claude was never
// found and never OOM-protected (DECISIONS I-213).
func matchesBinary(wants []string, name string) bool {
	for _, w := range wants {
		if name == w {
			return true
		}
		wrapped := "." + w + "-wrapped"
		if name == wrapped || (len(name) == 15 && len(name) > len(w)+1 && strings.HasPrefix(wrapped, name)) {
			return true
		}
	}
	return false
}

// HookedAgents are the agents whose completion is reported by a real hook.
// The rest fall back to the pane-idle heuristic, and the notification says so
// (features/agents.md: "claude finished" versus "gemini went idle").
var HookedAgents = map[string]bool{"claude": true, "codex": true, "opencode": true}

// AgentOf maps a tmux window name to an agent name, or "" if the window is not
// an agent window. "claude" and "claude-2" are both claude.
// AgentByCommand is the agent whose program a pane's foreground process
// is, "" for none (I-421). Gemini's `node` is left out: a dev server is
// node too, so gemini counts only in a window named after it.
func AgentByCommand(comm string) string {
	if comm == "" {
		return ""
	}
	for _, a := range Agents {
		var wants []string
		for _, b := range binaries[a] {
			if b != "node" {
				wants = append(wants, b)
			}
		}
		if matchesBinary(wants, comm) {
			return a
		}
	}
	return ""
}

func AgentOf(window string) string {
	for _, a := range Agents {
		if window == a || strings.HasPrefix(window, a+"-") {
			rest := strings.TrimPrefix(window, a+"-")
			if window == a {
				return a
			}
			if rest != "" && allDigits(rest) {
				return a
			}
		}
	}
	return ""
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// tmuxWindow is one row of `tmux list-windows`.
type tmuxWindow struct {
	Name         string
	PanePID      int
	PaneCommand  string
	LastActivity int64 // unix seconds, 0 when tmux did not report it
}

// tmuxClient runs tmux as dev. tmux is the only thing guestd forks for on the
// sampling path, which is why the watcher caches the result.
type tmuxClient struct {
	paths sysdep.Paths
	run   sysdep.Runner
}

// tmuxFailure classifies a non-zero tmux exit into a bounded reason, so the
// failure is logged without the message, which carries the project slug.
// Silently reporting "no windows" for any of these is how a guest looks idle
// while an agent is working in it.
func tmuxFailure(stderr string) string {
	switch {
	case strings.Contains(stderr, "no server running"):
		return "server_down"
	case strings.Contains(stderr, "can't find session"), strings.Contains(stderr, "session not found"):
		return "session_missing"
	case strings.Contains(stderr, "error connecting"), strings.Contains(stderr, "Permission denied"):
		return "connect_failed"
	case strings.Contains(stderr, "not found"), strings.Contains(stderr, "No such file"):
		return "tmux_missing"
	default:
		return "other"
	}
}

// listWindows returns the windows of the project's session, and whether a
// tmux server is running at all.
func (t tmuxClient) listWindows(ctx context.Context, session string) ([]tmuxWindow, bool, error) {
	const format = "#{window_name}\t#{pane_pid}\t#{pane_current_command}\t#{window_activity}"
	res, err := t.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"tmux", "list-windows", "-t", session, "-F", format},
		User:      "dev",
		Env:       sysdep.DevEnv(t.paths, "dev"),
		MaxOutput: 32 << 10,
	})
	if err != nil {
		return nil, false, sysdep.Errf(sysdep.CodeInternal, "list tmux windows: %w", err)
	}
	if res.ExitCode != 0 {
		reason := tmuxFailure(string(res.Stderr))
		if reason == "server_down" {
			return nil, false, nil
		}
		if reason == "session_missing" {
			// The project is not set up yet; that is a state, not a fault.
			return nil, true, nil
		}
		return nil, true, sysdep.Errf(sysdep.CodeInternal,
			"list tmux windows: tmux exited %d (%s)", res.ExitCode, reason)
	}
	var out []tmuxWindow
	for _, line := range strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		pid, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		w := tmuxWindow{Name: parts[0], PanePID: pid, PaneCommand: parts[2]}
		if len(parts) > 3 {
			if ts, err := strconv.ParseInt(parts[3], 10, 64); err == nil {
				w.LastActivity = ts
			}
		}
		out = append(out, w)
	}
	return out, true, nil
}

// tmuxPane is one row of `tmux list-panes -s`.
type tmuxPane struct {
	Window  string
	PID     int
	Command string
}

// listPanes returns every pane of the project's session, in every window.
// list-windows reports only each window's active pane, so an agent in a
// split pane would be missed (DECISIONS I-200).
func (t tmuxClient) listPanes(ctx context.Context, session string) ([]tmuxPane, error) {
	const format = "#{window_name}\t#{pane_pid}\t#{pane_current_command}"
	res, err := t.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"tmux", "list-panes", "-s", "-t", session, "-F", format},
		User:      "dev",
		Env:       sysdep.DevEnv(t.paths, "dev"),
		MaxOutput: 64 << 10,
	})
	if err != nil {
		return nil, sysdep.Errf(sysdep.CodeInternal, "list tmux panes: %w", err)
	}
	if res.ExitCode != 0 {
		reason := tmuxFailure(string(res.Stderr))
		if reason == "server_down" || reason == "session_missing" {
			return nil, nil
		}
		return nil, sysdep.Errf(sysdep.CodeInternal,
			"list tmux panes: tmux exited %d (%s)", res.ExitCode, reason)
	}
	var out []tmuxPane
	for _, line := range strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		pid, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		out = append(out, tmuxPane{Window: parts[0], PID: pid, Command: parts[2]})
	}
	return out, nil
}

// listClients counts attached tmux clients for the session.
func (t tmuxClient) listClients(ctx context.Context, session string) (uint32, bool, error) {
	res, err := t.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"tmux", "list-clients", "-t", session, "-F", "#{client_tty}"},
		User:      "dev",
		Env:       sysdep.DevEnv(t.paths, "dev"),
		MaxOutput: 8 << 10,
	})
	if err != nil {
		return 0, false, sysdep.Errf(sysdep.CodeInternal, "list tmux clients: %w", err)
	}
	if res.ExitCode != 0 {
		reason := tmuxFailure(string(res.Stderr))
		if reason == "server_down" {
			return 0, false, nil
		}
		if reason == "session_missing" {
			return 0, true, nil
		}
		return 0, true, sysdep.Errf(sysdep.CodeInternal,
			"list tmux clients: tmux exited %d (%s)", res.ExitCode, reason)
	}
	var n uint32
	for _, line := range strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n") {
		if line != "" {
			n++
		}
	}
	return n, true, nil
}

// windowOfPane resolves a tmux pane id (the $TMUX_PANE of a hook's caller) to
// its window name.
func (t tmuxClient) windowOfPane(ctx context.Context, pane string) (string, error) {
	res, err := t.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"tmux", "display-message", "-p", "-t", pane, "#{window_name}"},
		User:      "dev",
		Env:       sysdep.DevEnv(t.paths, "dev"),
		MaxOutput: 4 << 10,
	})
	if err != nil {
		return "", sysdep.Errf(sysdep.CodeInternal, "resolve tmux pane: %w", err)
	}
	if res.ExitCode != 0 {
		return "", sysdep.NotFound("resolve tmux pane: tmux exited %d", res.ExitCode)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// StateOption is the tmux window option guestd keeps an agent window's
// state in (DECISIONS I-606, guest-conventions.md "tmux"): `repose ps`
// reads it as STATE, and the status line marks a window that waits for
// input. It is set when the announced state changes, so a guestd that
// restarts sets it again within StateDebounce.
const StateOption = "@repose-state"

// setWindowState sets StateOption on window of session; best effort. The
// window is named exactly, so a prefix never hits another window.
func (t tmuxClient) setWindowState(ctx context.Context, session, window, state string) error {
	res, err := t.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"tmux", "set-option", "-w", "-t", "=" + session + ":=" + window, StateOption, state},
		User:      "dev",
		Env:       sysdep.DevEnv(t.paths, "dev"),
		MaxOutput: 4 << 10,
	})
	if err != nil {
		return sysdep.Errf(sysdep.CodeInternal, "set tmux window state: %w", err)
	}
	if res.ExitCode != 0 {
		return sysdep.Errf(sysdep.CodeInternal, "set tmux window state: tmux exited %d (%s)", res.ExitCode, tmuxFailure(string(res.Stderr)))
	}
	return nil
}
