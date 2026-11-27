package guest

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/heracraft/repose/internal/agentwindow"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

// StartAgentAgents are the agents a StartAgent may open: the five every
// guest ships (docs/features/agents.md), as the CLI's agentNames.
var StartAgentAgents = []string{"claude", "opencode", "codex", "gemini", "pi"}

// CodeNoTerminal is a StartAgent the machine has no tmux session for: a
// herdr machine, or a session that did not come up (DECISIONS I-1702).
const CodeNoTerminal = "no_terminal"

// startAgentTimeoutS bounds the guest's half: the wait for the tmux
// session a machine that has just booted is still making (60 s) and the
// window's own start.
const startAgentTimeoutS = 90

var startAgentSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// The script's exits that are not a failure of the shell itself.
const (
	startAgentExitNoSession = 3
	startAgentExitHerdr     = 4
)

// startAgentScript is the shell a StartAgent runs as dev (DECISIONS
// I-1702): it waits for the project's tmux session, turns on Remote
// Control's switch when asked (the file `remote_control = true` carries
// from a laptop, I-1136, which the claude wrapper reads at its start),
// and opens a window named after the agent in the checkout with the
// folder trust first, as `repose run` does over SSH (agentwindow, shared
// with the CLI and guestd's triggers). A window of that name already
// open is left alone: the answer is "existed", so a resent command opens
// nothing twice. It prints "started" or "existed" on its last line.
func startAgentScript(slug, agent string, remoteControl bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "slug=%s\nagent=%s\n", agentwindow.Quote(slug), agentwindow.Quote(agent))
	fmt.Fprintf(&b, `project="$HOME/.repose/project.json"
if [ -s "$project" ] && [ "$(jq -r '.multiplexer // "tmux"' "$project" 2>/dev/null || echo tmux)" = herdr ]; then exit %d; fi
i=0
until tmux has-session -t "=$slug" 2>/dev/null; do
  i=$((i + 1)); [ "$i" -lt 120 ] || exit %d; sleep 0.5
done
`, startAgentExitHerdr, startAgentExitNoSession)
	if remoteControl {
		b.WriteString(`mkdir -p "$HOME/.repose" && printf 'on\n' > "$HOME/.repose/remote-control.new" && mv -f "$HOME/.repose/remote-control.new" "$HOME/.repose/remote-control"
`)
	}
	b.WriteString(`if tmux list-windows -t "=$slug" -F '#{window_name}' 2>/dev/null | grep -qxF "$agent"; then
`)
	if remoteControl {
		// The open window keeps the session it has; the machine's
		// remote-control window is what the app can start one from.
		b.WriteString(`  if command -v repose-remote-control >/dev/null 2>&1; then repose-remote-control >/dev/null 2>&1 || true; fi
`)
	}
	b.WriteString(`  echo existed; exit 0
fi
repose_co=$(repose-checkout 2>/dev/null) || repose_co=$HOME
[ -n "$repose_co" ] || repose_co=$HOME
login_path=$(repose-login-path 2>/dev/null || true)
[ -z "$login_path" ] || PATH=$login_path
export PATH
`)
	b.WriteString(agentwindow.NewWindow(agentwindow.Quote(slug), agentwindow.Quote(agent), `"$repose_co"`, agent, "", ""))
	b.WriteString(" || exit 1\necho started\n")
	return b.String()
}

// startAgent opens an agent's window in a running guest (DECISIONS
// I-1702). It is one of hostd's own Execs, as dev: nothing a tenant
// typed is in it (the agent is one of five names, the slug the api's),
// and neither the script nor its output is logged.
func (m *Manager) startAgent(ctx context.Context, c *hostdv1.StartAgent) (*hostdv1.StartAgentResult, *Error) {
	if !slices.Contains(StartAgentAgents, c.Agent) {
		return nil, errf(CodeInvalidArgument, "agent must be one of %s", strings.Join(StartAgentAgents, ", "))
	}
	if !startAgentSlug.MatchString(c.Slug) {
		return nil, errf(CodeInvalidArgument, "slug required")
	}
	if c.RemoteControl && c.Agent != "claude" {
		return nil, errf(CodeInvalidArgument, "remote control is Claude Code's")
	}
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	if g.State != StateRunning {
		return nil, errf(CodeInvalidArgument, "guest is %s; an agent starts on a running one", g.State)
	}
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return nil, serr
	}
	r, err := sess.Exec(ctx, []string{"/bin/sh", "-c", startAgentScript(c.Slug, c.Agent, c.RemoteControl)}, startAgentTimeoutS, "dev")
	if err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			return nil, errf(CodeInternal, "guestd Exec: %s: %s", re.Code, re.Message)
		}
		return nil, errf(CodeGuestUnresponsive, "guestd Exec: %v", err)
	}
	switch r.GetExitCode() {
	case 0:
	case startAgentExitHerdr:
		return nil, errf(CodeNoTerminal, "this machine's terminals are herdr's, which an agent is not started in from here")
	case startAgentExitNoSession:
		return nil, errf(CodeNoTerminal, "the machine's tmux session did not start within 60 seconds")
	default:
		return nil, errf(CodeInternal, "the agent's window did not open (exit %d)", r.GetExitCode())
	}
	existed := strings.HasSuffix(strings.TrimSpace(string(r.GetStdout())), "existed")
	m.log(g).Info("agent started", "event", "agent_start", "agent", c.Agent, "existed", existed, "remote_control", c.RemoteControl)
	return &hostdv1.StartAgentResult{Window: c.Agent, Existed: existed}, nil
}
