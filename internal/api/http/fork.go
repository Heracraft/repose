package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// Forking a project (DECISIONS I-254): N new projects restored from one
// snapshot of a live project, created in one transaction so the project
// limit is checked for all N before any exists, and a resent request (the
// same request_id) answers with the projects the first one made.

// MaxForks bounds one fork request; the project limit bounds it further.
const MaxForks = 10

type forkedProject struct {
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Class     string    `json:"class"`
	OpID      uuid.UUID `json:"op_id"`
}

// forkProject is POST /v1/projects/{id}/fork: `{snapshot_id, count?,
// name?, class?, start?, request_id?}` → 202 `{snapshot_id,
// snapshot_created_at, from_project_id, projects: [{project_id, name,
// slug, class, op_id}]}`.
func (s *Server) forkProject(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u := userFrom(ctx)
	src, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body struct {
		SnapshotID *uuid.UUID `json:"snapshot_id"`
		Count      *int       `json:"count"`
		Name       string     `json:"name"`
		Class      string     `json:"class"`
		Start      *bool      `json:"start"`
		RequestID  *uuid.UUID `json:"request_id"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.SnapshotID == nil {
		return errf("invalid", "snapshot_id is required: take a snapshot of %s first (POST /projects/:id/snapshots)", src.Slug)
	}
	count := 1
	if body.Count != nil {
		count = *body.Count
	}
	if count < 1 || count > MaxForks {
		return errf("invalid", "count must be 1 to %d", MaxForks)
	}
	snap, err := store.GetSnapshot(ctx, s.d.Pool, *body.SnapshotID)
	if err != nil || snap.ProjectID != src.ID || snap.DeletedAt != nil || (snap.ExpiresAt != nil && snap.ExpiresAt.Before(time.Now())) {
		return errf("not_found", "that snapshot is not one of %s's, or it has expired", src.Slug)
	}
	class := src.Class
	if body.Class != "" {
		if !scheduler.ValidClass(body.Class) {
			return errf("invalid", "class must be small, large or xl")
		}
		class = body.Class
	}
	base := body.Name
	if base == "" {
		base = src.Slug + "-fork"
	}
	if !nameRe.MatchString(base) || Slug(base) == "" {
		return errf("invalid", "name must match [A-Za-z0-9._-]{1,64}")
	}
	start := body.Start == nil || *body.Start
	// The gate sees one machine of the class and the bytes all N copies
	// hold, each what the source holds (I-585); the memory of the rest is
	// checked as each fork's restore starts it.
	gateClass := ""
	if start {
		gateClass = class
	}
	if err := s.gate(r, u, billing.Request{Class: gateClass, Disk: true, AddHeldBytes: src.HeldBytes() * int64(count), VolumeBytes: src.VolumeBytes}); err != nil {
		return err
	}
	if u.CancelledAt != nil {
		return errf("forbidden", "account is cancelled")
	}
	limits, err := s.limits(r, u)
	if err != nil {
		return err
	}
	// A fork of a held project is refused whether or not the copies
	// start: they would carry no hold of their own (DECISIONS I-460).
	if err := s.abuseGate(ctx, src); err != nil {
		return err
	}
	var out []forkedProject
	resent := false
	err = db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		// The user's row lock serialises this with every create and
		// restore, so the limit holds for all N and a resend waits for
		// the first request's commit.
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from projects where user_id = (select id from users where id = $1 for update) and "+countsTowardLimit, u.ID).Scan(&n); err != nil {
			return err
		}
		if body.RequestID != nil {
			prev, err := forkedByRequest(ctx, tx, u.ID, *body.RequestID)
			if err != nil {
				return err
			}
			if len(prev) > 0 {
				out, resent = prev, true
				return nil
			}
		}
		if n+count > limits.Projects {
			return projectLimitError(n, limits.Projects, count)
		}
		names, err := forkNames(ctx, tx, u.ID, Slug(base), count)
		if err != nil {
			return err
		}
		params := map[string]any{"fork_of": src.ID.String()}
		if body.RequestID != nil {
			params["fork_request_id"] = body.RequestID.String()
		}
		for _, name := range names {
			id := store.NewID()
			// No remote: the source is live and keeps it, so `repose run`
			// in the checkout still means the source (I-254).
			opID, err := s.insertRestored(ctx, tx, u, src, snap, id, name, class, nil, start, params)
			if err != nil {
				return err
			}
			// The named secrets come along under the same user key, sealed
			// again for the new project (a ciphertext is bound to its
			// project, I-433). The guest's own sshd material (lowercase
			// names) does not; every guest gets its own (I-3).
			if err := s.d.Secrets.CopyNamed(ctx, tx, src.ID.String(), id.String()); err != nil {
				return err
			}
			out = append(out, forkedProject{ProjectID: id, Name: name, Slug: Slug(name), Class: class, OpID: opID})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !resent {
		s.d.Engine.Kick()
		obs.Logger(ctx, s.d.Log).Info("fork accepted", "event", "forked", "project_id", src.ID.String(), "snapshot_id", snap.ID.String(), "count", len(out), "class", class)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"snapshot_id": snap.ID, "snapshot_created_at": snap.TakenAt, "from_project_id": src.ID, "projects": out,
	})
	return nil
}

// forkNames picks count names `<base>-<k>` for the lowest k no live
// project of the user has, trimming base so each fits a slug's 40
// characters.
func forkNames(ctx context.Context, tx db.Tx, userID uuid.UUID, base string, count int) ([]string, error) {
	rows, err := tx.Query(ctx, "select slug from projects where user_id = $1 and destroyed_at is null", userID)
	if err != nil {
		return nil, err
	}
	slugs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, sl := range slugs {
		taken[sl] = true
	}
	var names []string
	for k := 1; len(names) < count; k++ {
		suffix := fmt.Sprintf("-%d", k)
		b := base
		if len(b)+len(suffix) > 40 {
			b = strings.TrimRight(b[:40-len(suffix)], "-")
		}
		name := b + suffix
		if !taken[name] {
			names = append(names, name)
			taken[name] = true
		}
	}
	return names, nil
}

// forkedByRequest is what an earlier fork request with this id made: the
// projects whose restore op carries it, in the order they were created.
func forkedByRequest(ctx context.Context, tx db.Tx, userID, requestID uuid.UUID) ([]forkedProject, error) {
	rows, err := tx.Query(ctx, `select p.id, p.name, p.slug, p.class, o.id from ops o join projects p on p.id = o.project_id
		where p.user_id = $1 and o.kind = 'restore' and o.params->>'fork_request_id' = $2
		order by p.created_at, length(p.slug), p.slug`, userID, requestID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []forkedProject
	for rows.Next() {
		var f forkedProject
		if err := rows.Scan(&f.ProjectID, &f.Name, &f.Slug, &f.Class, &f.OpID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return out, nil
}
