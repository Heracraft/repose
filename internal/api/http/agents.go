package httpapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/multiplexer"
)

// startableAgents are the agents a machine ships and the dashboard may
// start (docs/features/agents.md), hostd's guest.StartAgentAgents.
var startableAgents = []string{"claude", "opencode", "codex", "gemini", "pi"}

// agentChoice reads `agent` and `remote_control` off a body: the agent
// asked for, else def; remote control only for Claude Code, whose
// feature it is (DECISIONS I-1702).
func agentChoice(asked *string, def string, remoteControl bool) (string, error) {
	agent := def
	if asked != nil && strings.TrimSpace(*asked) != "" {
		agent = strings.TrimSpace(*asked)
	}
	if agent == "" {
		agent = "claude"
	}
	if !slices.Contains(startableAgents, agent) {
		return "", withDetail(errf("invalid", "agent must be one of %s", strings.Join(startableAgents, ", ")), map[string]any{"reason": "agent"})
	}
	if remoteControl && agent != "claude" {
		return "", withDetail(errf("invalid", "remote_control is Claude Code's; it needs agent claude"), map[string]any{"reason": "remote_control"})
	}
	return agent, nil
}

// startAgent is POST /projects/{id}/agents (DECISIONS I-1702): start an
// agent on a running machine, in a window of its own in the machine's
// tmux session, in its checkout, as `repose run` does from a laptop. The
// dashboard's Start agent. `{agent?, remote_control?}`: agent defaults
// to the project's agent_default; remote_control turns on the machine's
// Claude Code Remote Control first. An agent op does it; the 202 names
// the op, whose result says the window and whether it was already open.
func (s *Server) startAgent(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body struct {
		Agent         *string `json:"agent"`
		RemoteControl bool    `json:"remote_control"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	agent, err := agentChoice(body.Agent, p.AgentDefault, body.RemoteControl)
	if err != nil {
		return err
	}
	if multiplexer.Normalize(p.Multiplexer) == multiplexer.Herdr {
		return withDetail(errf("conflict", "%s's terminals are herdr's; start an agent there with `repose run`", p.Slug), map[string]any{"reason": "herdr"})
	}
	if p.State != "running" {
		return withDetail(errf("conflict", "%s is %s; start it first", p.Slug, p.State), map[string]any{"reason": "not_running", "state": p.State})
	}
	pid := p.ID
	id, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindAgent, ProjectID: &pid,
		Params: map[string]any{"agent": agent, "remote_control": body.RemoteControl}, Phases: ops.PlanAgent()}, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id, "agent": agent})
	return nil
}
