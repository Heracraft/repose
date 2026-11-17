package guest

import (
	"context"
	"strings"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
	"github.com/heracraft/repose/internal/triggerrun"
)

func (m *Manager) updateSecrets(ctx context.Context, c *hostdv1.UpdateSecrets) *Error {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	// The whole named set, possibly empty: an empty one removes the
	// guest's last secret (I-732).
	named, err := m.replaceNamedSecrets(g.GuestID, c.Secrets)
	if err != nil {
		return err
	}
	if g.State != StateRunning {
		return nil
	}
	// A new value must reach the next command in the guest within
	// seconds, which means waking a resting one first (I-818).
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return serr
	}
	if err := sess.WriteSecrets(ctx, named); err != nil {
		return errf(CodeInternal, "guestd WriteSecrets: %v", err)
	}
	m.log(g).Info("secrets updated", "event", "guest_secrets", "count", len(c.Secrets))
	return nil
}

func (m *Manager) setPrincipals(ctx context.Context, c *hostdv1.SetPrincipals) *Error {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	g.Principals = c.Principals
	if err := m.d.State.PutGuest(g); err != nil {
		return errf(CodeInternal, "state write: %v", err)
	}
	m.writeGuestJSON(g)
	if g.State != StateRunning {
		return nil
	}
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return serr
	}
	if err := sess.SetPrincipals(ctx, c.Principals); err != nil {
		return errf(CodeInternal, "guestd SetPrincipals: %v", err)
	}
	return nil
}

// answerQuestion relays the user's reply to the guest whose repose-ask is
// waiting (DECISIONS I-244), or, with status extended, the new deadline
// of a question the owner is answering (DECISIONS I-1242). The answer is
// tenant text and is not logged; a guest that is not running, or no
// longer knows the question, is not_found, which the api takes as final.
func (m *Manager) answerQuestion(ctx context.Context, c *hostdv1.AnswerQuestion) *Error {
	if c.QuestionId == "" || c.Status == "" {
		return errf(CodeInvalidArgument, "question_id and status required")
	}
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	if g.State != StateRunning {
		return errf(CodeNotFound, "guest is %s; its questions ended with it", g.State)
	}
	// The answer is what wakes a machine whose agent asked and then
	// rested with the question open (I-818).
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return serr
	}
	err := sess.AnswerQuestion(ctx, &guestdv1.AnswerQuestion{QuestionId: c.QuestionId, Status: c.Status, Answer: c.Answer, ExpiresUnix: c.ExpiresUnix})
	if err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			if re.Code == CodeNotFound || re.Code == CodeInvalidArgument {
				return errf(re.Code, "guestd AnswerQuestion: %s", re.Message)
			}
			return errf(CodeInternal, "guestd AnswerQuestion: %s: %s", re.Code, re.Message)
		}
		return errf(CodeGuestUnresponsive, "guestd AnswerQuestion: %v", err)
	}
	if c.Status == "extended" {
		m.log(g).Info("question held open", "event", "agent_question", "question_id", c.QuestionId, "status", c.Status)
		return nil
	}
	m.log(g).Info("question answered", "event", "agent_question", "question_id", c.QuestionId, "status", c.Status)
	return nil
}

// sayToAgent relays a message the owner typed to the guest's agent
// (DECISIONS I-1144). The text is tenant content and is not logged; a
// guest that is not running has no agent to say it to.
func (m *Manager) sayToAgent(ctx context.Context, c *hostdv1.SayToAgent) (*hostdv1.SayResult, *Error) {
	if c.SayId == "" || strings.TrimSpace(c.Text) == "" {
		return nil, errf(CodeInvalidArgument, "say_id and text required")
	}
	if len(c.Text) > SayTextCap {
		return nil, errf(CodeInvalidArgument, "text is %d bytes; at most %d", len(c.Text), SayTextCap)
	}
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	if g.State != StateRunning {
		return nil, errf(CodeNotFound, "guest is %s; it runs no agent", g.State)
	}
	// A message is what wakes a machine whose agent rested at its
	// prompt, as an answer does (I-818).
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return nil, serr
	}
	res, err := sess.SayToAgent(ctx, &guestdv1.SayToAgent{
		SayId: c.SayId, Window: c.Window, Text: c.Text, Sender: c.Sender, FromOwner: c.FromOwner,
	})
	if err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			if re.Code == CodeNotFound || re.Code == CodeInvalidArgument {
				return nil, errf(re.Code, "guestd SayToAgent: %s", re.Message)
			}
			return nil, errf(CodeInternal, "guestd SayToAgent: %s: %s", re.Code, re.Message)
		}
		return nil, errf(CodeGuestUnresponsive, "guestd SayToAgent: %v", err)
	}
	if res == nil {
		res = &guestdv1.SayResult{}
	}
	m.log(g).Info("message for an agent", "event", "agent_prompted", "say_id", c.SayId, "state", res.GetState())
	return &hostdv1.SayResult{State: res.GetState(), Window: res.GetWindow(), Replaced: res.GetReplaced()}, nil
}

// setTriggers relays the project's whole trigger set to guestd
// (DECISIONS I-1248). It is a state the api owns: hostd keeps none of
// it, and the api sends it again on every start, so a guest that lost it
// comes back with it. A guest that is not running is not an error — the
// set reaches it at its start.
func (m *Manager) setTriggers(ctx context.Context, c *hostdv1.SetTriggers) *Error {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	if g.State != StateRunning {
		return nil
	}
	sess, serr := m.session(g.GuestID)
	if serr != nil {
		return serr
	}
	set := make([]*guestdv1.Trigger, 0, len(c.Triggers))
	for _, t := range c.Triggers {
		set = append(set, &guestdv1.Trigger{
			Name: t.GetName(), Kind: t.GetKind(), PromptPath: t.GetPromptPath(),
			Workdir: t.GetWorkdir(), Agent: t.GetAgent(), TimeoutS: t.GetTimeoutS(),
			Concurrency: t.GetConcurrency(), Gate: t.GetGate(),
			Dedupe: t.GetDedupe(), Enabled: t.GetEnabled(),
		})
	}
	if err := sess.SetTriggers(ctx, &guestdv1.SetTriggers{Triggers: set}); err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			return errf(CodeInternal, "guestd SetTriggers: %s: %s", re.Code, re.Message)
		}
		return errf(CodeGuestUnresponsive, "guestd SetTriggers: %v", err)
	}
	m.log(g).Info("trigger set pushed", "event", "trigger_run", "count", len(set))
	return nil
}

// writeTriggerPrompt relays a prompt's text to guestd, which writes it
// into the machine as dev (DECISIONS I-1266). `repose triggers add -p`
// writes the same file over SSH; the dashboard has no SSH, so the api
// carries the text and stores none of it. The text is the user's prompt
// and the name is the user's word, so neither is a log field: the line
// carries the size.
func (m *Manager) writeTriggerPrompt(ctx context.Context, c *hostdv1.WriteTriggerPrompt) *Error {
	if c.Trigger == "" || c.PromptPath == "" || len(c.Text) == 0 {
		return errf(CodeInvalidArgument, "trigger, prompt_path and text required")
	}
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	if g.State != StateRunning {
		return errf(CodeNotFound, "guest is %s; it holds no files to write", g.State)
	}
	sess, serr := m.session(g.GuestID)
	if serr != nil {
		return serr
	}
	if err := sess.WriteTriggerPrompt(ctx, &guestdv1.WriteTriggerPrompt{
		Trigger: c.Trigger, Workdir: c.Workdir, PromptPath: c.PromptPath, Text: c.Text,
	}); err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			if re.Code == CodeInvalidArgument {
				return errf(re.Code, "guestd WriteTriggerPrompt: %s", re.Message)
			}
			return errf(CodeInternal, "guestd WriteTriggerPrompt: %s: %s", re.Code, re.Message)
		}
		return errf(CodeGuestUnresponsive, "guestd WriteTriggerPrompt: %v", err)
	}
	m.log(g).Info("trigger prompt written", "event", "trigger_run", "bytes", len(c.Text))
	return nil
}

// runTrigger relays a fire to guestd (DECISIONS I-1252, I-1256). The
// run's id is the command's id, so a re-send after a reconnect is
// answered with that run's state and starts no second agent. A trigger's
// name is the user's word and is not logged; the run id is.
func (m *Manager) runTrigger(ctx context.Context, c *hostdv1.RunTrigger) (*hostdv1.TriggerRunResult, *Error) {
	if c.Trigger == "" || c.RunId == "" {
		return nil, errf(CodeInvalidArgument, "trigger and run_id required")
	}
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	if g.State != StateRunning {
		return nil, errf(CodeNotFound, "guest is %s; it runs no agent", g.State)
	}
	// A fire wakes a machine resting at its agent's prompt, as a message
	// and an answer do (I-818).
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return nil, serr
	}
	res, err := sess.RunTrigger(ctx, &guestdv1.RunTrigger{
		Trigger: c.Trigger, RunId: c.RunId, Source: c.Source,
		PayloadRun: c.PayloadRun, DryRun: c.DryRun, PayloadPath: c.PayloadPath,
	})
	if err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			if re.Code == CodeNotFound || re.Code == CodeInvalidArgument {
				return nil, errf(re.Code, "guestd RunTrigger: %s", re.Message)
			}
			return nil, errf(CodeInternal, "guestd RunTrigger: %s: %s", re.Code, re.Message)
		}
		return nil, errf(CodeGuestUnresponsive, "guestd RunTrigger: %v", err)
	}
	if res == nil {
		res = &guestdv1.TriggerRunResult{}
	}
	// The result is a guest's words: the state is bounded to what a guest
	// may say, and the reason's own free text (a gate's last line of
	// stderr) is tenant content, so the log line carries the enum word
	// alone and the api gets the rest.
	state := res.GetState()
	if !triggerrun.GuestStates[state] {
		state = triggerrun.StateFailed
	}
	m.log(g).Info("trigger run", "event", "trigger_run", "run_id", c.RunId,
		"source", c.Source, "state", state, "reason", triggerrun.LogReason(res.GetReason()))
	return &hostdv1.TriggerRunResult{State: state, Window: cleanLine(res.GetWindow(), capWindow),
		Reason: cleanText(res.GetReason(), capTriggerReason)}, nil
}

// SayTextCap is the longest message `repose say` may carry, four times
// an event summary: a message is something a person wrote, and a
// paragraph does not fit in 1 KB. guestd caps it again.
const SayTextCap = 4 << 10

// ExecOutputCap is the per-stream cap from the interface doc.
const ExecOutputCap = 64 << 10

func (m *Manager) exec(ctx context.Context, c *hostdv1.Exec) (*hostdv1.ExecResult, *Error) {
	if c.AuditId == "" {
		return nil, errf(CodeInvalidArgument, "audit_id required: Exec is operator-only and audited")
	}
	if len(c.Argv) == 0 {
		return nil, errf(CodeInvalidArgument, "argv required")
	}
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	if g.State != StateRunning {
		return nil, errf(CodeInvalidArgument, "guest is %s; exec needs running", g.State)
	}
	// The command itself is not logged, not even an operator's: process
	// arguments are on the never-log list of docs/ops/OBSERVABILITY.md, and
	// the audit trail that must hold the command is the api's audit_log row
	// keyed by this audit_id (docs/interfaces/db-schema.md).
	m.log(g).Log(ctx, levelNotice, "audited exec", "event", "exec_audit", "audit_id", c.AuditId, "argv_len", len(c.Argv))
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return nil, serr
	}
	r, err := sess.Exec(ctx, c.Argv, c.TimeoutS, "dev")
	if err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			return nil, errf(CodeInternal, "guestd Exec: %s: %s", re.Code, re.Message)
		}
		return nil, errf(CodeGuestUnresponsive, "guestd Exec: %v", err)
	}
	out, errb := r.Stdout, r.Stderr
	if len(out) > ExecOutputCap {
		out = out[:ExecOutputCap]
	}
	if len(errb) > ExecOutputCap {
		errb = errb[:ExecOutputCap]
	}
	return &hostdv1.ExecResult{ExitCode: r.ExitCode, Stdout: out, Stderr: errb}, nil
}
