package ops

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// MaxPersonalFanOut bounds how many projects one personal change
// rebuilds at once (DECISIONS I-490). Plans allow far fewer projects; a
// project past the bound gets the layer with its next configuration
// change, base update or opt-in, which all read the account's current
// text.
const MaxPersonalFanOut = 50

// PersonalChange is what ApplyPersonal did to one project.
type PersonalChange struct {
	ProjectID  uuid.UUID `json:"project_id"`
	Slug       string    `json:"slug"`
	RevisionID uuid.UUID `json:"revision_id"`
	// OpID is the build op; empty when an op still waiting to start was
	// given the new text instead (Merged).
	OpID   *uuid.UUID `json:"op_id,omitempty"`
	Merged bool       `json:"merged,omitempty"`
	// Running is whether the project ran when the change was queued: a
	// running machine switches in place, a stopped one at its next start.
	Running bool `json:"running"`
}

// ApplyPersonal gives p a new revision with its current fragment and
// layer, and queues its build and, when it runs, its apply. reason is
// recorded in the op's params ("change", "opt_in", "opt_out"). A build of
// an earlier personal change that has not started yet takes the new text
// instead of a second op queueing behind it. A project whose current
// revision already carries layer is left alone (ok false).
func (e *Engine) ApplyPersonal(ctx context.Context, tx db.Tx, p *store.Project, layer store.PersonalLayer, reason string) (PersonalChange, bool, error) {
	ch := PersonalChange{ProjectID: p.ID, Slug: p.Slug, Running: p.State == "running"}
	if p.ConfigRevisionID == nil {
		return ch, false, nil
	}
	cur, err := store.GetRevision(ctx, tx, *p.ConfigRevisionID)
	if err != nil {
		return ch, false, err
	}
	var pendingOp, pendingRev uuid.UUID
	err = tx.QueryRow(ctx, `select o.id, o.revision_id from ops o join config_revisions r on r.id = o.revision_id
		where o.project_id = $1 and o.state = 'pending' and o.kind = 'build' and o.params ? 'personal' and r.status = 'building'
		order by o.created_at desc limit 1`, p.ID).Scan(&pendingOp, &pendingRev)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, "update config_revisions set personal = $2, personal_revision_id = $3, personal_opt_out = $4 where id = $1 and status = 'building'",
			pendingRev, layer.Text, layer.RevisionID, layer.OptOut); err != nil {
			return ch, false, err
		}
		ch.RevisionID, ch.Merged = pendingRev, true
		return ch, true, nil
	case !db.IsNoRows(err):
		return ch, false, err
	}
	if cur.Personal == layer.Text && cur.PersonalOptOut == layer.OptOut && cur.Status == "applied" {
		return ch, false, nil
	}
	rid := store.NewID()
	if _, err := tx.Exec(ctx, `insert into config_revisions (id, project_id, fragment, menu, base_version, status, personal, personal_revision_id, personal_opt_out)
		values ($1, $2, $3, $4, $5, 'building', $6, $7, $8)`,
		rid, p.ID, cur.Fragment, cur.Menu, p.BaseVersion, layer.Text, layer.RevisionID, layer.OptOut); err != nil {
		return ch, false, err
	}
	pid := p.ID
	// Queued behind whatever the project is doing (allowQueue): a create,
	// start or stop finishes first. PlanBuild(true) applies when the
	// guest runs then and leaves the revision built, for the next start,
	// when it does not.
	opID, err := e.Enqueue(ctx, tx, NewOp{Kind: KindBuild, ProjectID: &pid, RevisionID: &rid, Params: map[string]any{"personal": reason}, Phases: PlanBuild(true)}, true)
	if err != nil {
		return ch, false, err
	}
	ch.RevisionID, ch.OpID = rid, &opID
	return ch, true, nil
}

// personalStates are the project states a personal change reaches.
// destroying, destroyed and error are left out: an errored project gets
// the layer at its next configuration change or base update.
const personalProjectsSQL = `select id from projects where user_id = $1 and destroyed_at is null
	and state not in ('destroying', 'destroyed', 'error') and not personal_opt_out and config_revision_id is not null
	order by (state = 'running') desc, created_at limit $2`

// FanOutPersonal applies the account's current personal layer to every
// project of userID that has not opted out, inside tx, and returns what it
// queued. Running projects come first, so with the bound reached they are
// the ones served.
func (e *Engine) FanOutPersonal(ctx context.Context, tx db.Tx, userID uuid.UUID) ([]PersonalChange, error) {
	rows, err := tx.Query(ctx, personalProjectsSQL, userID, MaxPersonalFanOut)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []PersonalChange{}
	for _, id := range ids {
		p, err := store.GetProjectForUpdate(ctx, tx, id)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		layer, err := store.PersonalFor(ctx, tx, p)
		if err != nil {
			return nil, err
		}
		ch, ok, err := e.ApplyPersonal(ctx, tx, p, layer, "change")
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, ch)
		}
	}
	return out, nil
}
