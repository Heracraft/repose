package guest

import (
	"context"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
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
	sess, serr := m.session(g.GuestID)
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
	sess, serr := m.session(g.GuestID)
	if serr != nil {
		return serr
	}
	if err := sess.SetPrincipals(ctx, c.Principals); err != nil {
		return errf(CodeInternal, "guestd SetPrincipals: %v", err)
	}
	return nil
}

// answerQuestion relays the user's reply to the guest whose repose-ask is
// waiting (DECISIONS I-244). The answer is tenant text and is not logged; a
// guest that is not running, or no longer knows the question, is not_found,
// which the api takes as final.
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
	sess, serr := m.session(g.GuestID)
	if serr != nil {
		return serr
	}
	err := sess.AnswerQuestion(ctx, &guestdv1.AnswerQuestion{QuestionId: c.QuestionId, Status: c.Status, Answer: c.Answer})
	if err != nil {
		if re, ok := vsockclient.IsRemote(err); ok {
			if re.Code == CodeNotFound || re.Code == CodeInvalidArgument {
				return errf(re.Code, "guestd AnswerQuestion: %s", re.Message)
			}
			return errf(CodeInternal, "guestd AnswerQuestion: %s: %s", re.Code, re.Message)
		}
		return errf(CodeGuestUnresponsive, "guestd AnswerQuestion: %v", err)
	}
	m.log(g).Info("question answered", "event", "agent_question", "question_id", c.QuestionId, "status", c.Status)
	return nil
}

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
	sess, serr := m.session(g.GuestID)
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
