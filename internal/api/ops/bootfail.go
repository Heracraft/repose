package ops

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// codeBootFailed is hostd's code for a boot that never reached Ready and
// whose console shows why (grpc-hostd.md, DECISIONS I-592).
const codeBootFailed = "boot_failed"

// EventBootFailed is the platform event a machine that did not boot its
// new system and runs its previous one records (DECISIONS I-590).
const EventBootFailed = "boot_failed"

// Warning is what an op that finished with the machine running, but not
// as asked, carries in result.warning and leaves in last_error: a start
// whose switch failed or whose new system did not boot (I-590).
func warning(code, message string) map[string]any {
	return map[string]any{"code": code, "message": message}
}

// skipFailedApply handles a start's apply phase that failed for good
// (DECISIONS I-590). The start asked for a running machine, and the
// apply is a step on the way: the machine runs (an in-place switch that
// failed keeps the system it had) or will run (a restart's apply to the
// stopped guest skips to the boot of what it has), so the op goes on
// without the apply and ends done with a warning, instead of putting a
// running project in error, which sent the next `repose start` into a
// reboot (kanali, 2026-10-07). A boot_failed revision is marked failed,
// so neither a start nor the base sweep applies it again.
func (e *Engine) skipFailedApply(ctx context.Context, op *store.Op, code, msg string) bool {
	if op.Kind != KindStart || currentPhase(op) != PhaseApplyConfig || op.ProjectID == nil {
		return false
	}
	p, err := store.GetProject(ctx, e.pool, *op.ProjectID)
	if err != nil {
		return false
	}
	human, ok := humanError(op.Kind, PhaseApplyConfig, code, msg)
	if !ok {
		human = firstLineOf(msg)
	}
	if code == codeBootFailed && op.RevisionID != nil {
		_, _ = e.pool.Exec(ctx, "update config_revisions set status = 'failed', error = $2 where id = $1 and status in ('built', 'applied')", *op.RevisionID, code+": "+human) // best effort; the warning carries it
		e.notifyPlatform(ctx, p.ID, EventBootFailed, bootFailedSummary(p.Slug, human))
	}
	w := warning(code, human)
	op.Params["warning"] = w
	if err := e.setResult(ctx, op, map[string]any{"warning": w}); err != nil {
		return false
	}
	_, _ = e.pool.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, code+": "+human) // best effort; the op result carries it
	if op.CommandID != nil {
		e.logs.Unbind(op.CommandID.String())
	}
	op.Step++
	op.CommandID, op.CommandResult, op.SentAt = nil, nil, nil
	if _, err := e.pool.Exec(ctx, "update ops set params = $2, step = $3, command_id = null, command_result = null, sent_at = null where id = $1", op.ID, op.Params, op.Step); err != nil {
		e.log.Error("op skip record", "event", "op_fail", "op_id", op.ID.String(), "err", err.Error())
		return false
	}
	e.log.Warn("start's apply failed; the start goes on without it", "event", "op_recover", "op_id", op.ID.String(), "kind", op.Kind,
		"phase", PhaseApplyConfig, "code", code, "action", "skipped")
	e.Kick()
	return true
}

// onBootFallback is a StartGuest that booted the machine's previous
// system because the one it was given never reached Ready (I-590): the
// project runs, the revision of the closure that failed is marked failed
// and the one that booted is the applied one again, last_error says what
// happened and a boot_failed event tells the user.
func (e *Engine) onBootFallback(ctx context.Context, op *store.Op, p *store.Project, st *hostdv1.StartResult) error {
	reason := st.GetBootError().GetMessage()
	if reason == "" {
		reason = "it did not answer while booting"
	}
	human := "its new system did not boot, so it runs its previous one: " + strings.TrimSuffix(reason, ".") + "; `repose logs --kind console` shows what the new one printed"
	if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'failed', error = $3 where project_id = $1 and system_closure = $2 and status in ('built', 'applied')", p.ID, st.FailedClosure, codeBootFailed+": "+human); err != nil {
		return err
	}
	var rid uuid.UUID
	var base *string
	err := e.pool.QueryRow(ctx, "select id, base_version from config_revisions where project_id = $1 and system_closure = $2 and status <> 'failed' order by created_at desc limit 1", p.ID, st.BootedClosure).Scan(&rid, &base)
	switch {
	case err == nil:
		if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'built' where project_id = $1 and id <> $2 and status = 'applied'", p.ID, rid); err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'applied' where id = $1", rid); err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update projects set config_revision_id = $2, base_version = coalesce($3, base_version) where id = $1", p.ID, rid, base); err != nil {
			return err
		}
	case db.IsNoRows(err):
		e.log.Warn("the closure a fallback booted has no revision", "event", "boot_fallback", "project_id", p.ID.String())
	default:
		return err
	}
	w := warning(codeBootFailed, human)
	op.Params["warning"] = w
	if _, err := e.pool.Exec(ctx, "update ops set params = $2 where id = $1", op.ID, op.Params); err != nil {
		return err
	}
	fields := map[string]any{"warning": w}
	if tail := st.GetBootError().GetConsoleTail(); tail != "" {
		fields["console"] = tail
	}
	if err := e.setResult(ctx, op, fields); err != nil {
		return err
	}
	if _, err := e.pool.Exec(ctx, "update projects set host_unreachable = false, last_error = $2 where id = $1", p.ID, codeBootFailed+": "+human); err != nil {
		return err
	}
	e.log.Warn("start booted the previous system", "event", "boot_fallback", "project_id", p.ID.String(), "code", st.GetBootError().GetCode())
	e.notifyPlatform(ctx, p.ID, EventBootFailed, bootFailedSummary(p.Slug, human))
	return e.setState(ctx, p, "running")
}

// bootFailedSummary is the boot_failed event's body.
func bootFailedSummary(slug, human string) string {
	return slug + ": " + strings.TrimSuffix(human, ".") + "."
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
