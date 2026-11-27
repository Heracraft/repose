package ops

import (
	"context"
	"strings"

	"github.com/heracraft/repose/internal/api/store"
)

// AgentOldHostMessage is a start_agent that reached a hostd from before
// StartAgent (DECISIONS I-1702), which answers any command it does not
// know as invalid_argument "unknown command". Fleet hosts get the new
// hostd with the release; a host of the user's own updates itself.
const AgentOldHostMessage = "the host this machine runs on has a repose from before starting agents from the dashboard; it can once that host updates"

// agentFailure is the sentence a failed start_agent ends with: hostd's,
// or AgentOldHostMessage for a hostd that does not know the command.
func agentFailure(code, msg string) string {
	if code == "invalid_argument" && strings.Contains(msg, "unknown command") {
		return AgentOldHostMessage
	}
	return firstLineOf(msg)
}

// skipFailedAgentStart handles a start_agent that failed. On a create the
// machine is made whatever happened to its agent, so the create goes on
// (the phase is its last) with the reason in its result as `agent_error`,
// which the dashboard shows; the machine's last_error stays clean, since
// nothing is wrong with the machine. On the agent op itself it fails the
// op with the same sentence. It reports whether it handled the result.
func (e *Engine) skipFailedAgentStart(ctx context.Context, op *store.Op, code, msg string) bool {
	if currentPhase(op) != PhaseStartAgent {
		return false
	}
	why := agentFailure(code, msg)
	if op.Kind != KindCreate {
		e.fail(ctx, op, code, why)
		return true
	}
	if err := e.setResult(ctx, op, map[string]any{"agent_error": code + ": " + why}); err != nil {
		return false
	}
	if op.CommandID != nil {
		e.logs.Unbind(op.CommandID.String())
	}
	op.Step++
	op.CommandID, op.CommandResult, op.SentAt = nil, nil, nil
	if _, err := e.pool.Exec(ctx, "update ops set step = $2, command_id = null, command_result = null, sent_at = null where id = $1", op.ID, op.Step); err != nil {
		e.log.Error("op skip record", "event", "op_fail", "op_id", op.ID.String(), "err", err.Error())
		return false
	}
	e.log.Warn("create's agent did not start; the create goes on without it", "event", "op_recover", "op_id", op.ID.String(), "kind", op.Kind,
		"phase", PhaseStartAgent, "code", code, "action", "skipped")
	e.Kick()
	return true
}
