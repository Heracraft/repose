package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// Restoring by name (DECISIONS I-167). `repose destroy` used to end with
// `repose snapshots restore <snapshot id> --project <project id> --as-new
// NAME`: two ids nobody keeps, because a destroyed project no longer
// resolves by name. These routes let the CLI and the dashboard say
// `repose restore izma` instead.

type destroyedRow struct {
	ID          uuid.UUID  `db:"id"`
	Name        string     `db:"name"`
	Slug        string     `db:"slug"`
	Class       string     `db:"class"`
	RemoteURL   *string    `db:"remote_url"`
	VolumeBytes int64      `db:"volume_bytes"`
	DestroyedAt time.Time  `db:"destroyed_at"`
	SnapID      uuid.UUID  `db:"snap_id"`
	SnapTaken   time.Time  `db:"snap_taken"`
	SnapBytes   int64      `db:"snap_bytes"`
	SnapReason  string     `db:"snap_reason"`
	SnapExpires *time.Time `db:"snap_expires"`
	NameFree    bool       `db:"name_free"`
}

// listDestroyed is GET /v1/projects/destroyed: the user's destroyed
// projects that still have a snapshot to restore, newest destroy first,
// each with its newest restorable snapshot and when that one goes.
func (s *Server) listDestroyed(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	// limit and before page through the list (I-420), keyset on
	// (destroyed_at, id); with neither, the newest 100 as before.
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return errf("invalid", "limit must be 1 to 200")
		}
		limit = n
	}
	var before *uuid.UUID
	if v := r.URL.Query().Get("before"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return errf("invalid", "before must be a project id")
		}
		before = &id
	}
	rows, err := s.d.Pool.Query(r.Context(), `
		select p.id, p.name, p.slug, p.class, p.remote_url, p.volume_bytes, p.destroyed_at,
		       s.id as snap_id, s.taken_at as snap_taken, s.bytes as snap_bytes, s.reason as snap_reason, s.expires_at as snap_expires,
		       not exists (select 1 from projects l where l.user_id = p.user_id and l.slug = p.slug and l.destroyed_at is null) as name_free
		from projects p
		join lateral (
			select s.* from snapshots s where s.project_id = p.id and `+store.RestorableSnapshotWhere+`
			order by s.taken_at desc, s.created_at desc limit 1
		) s on true
		where p.user_id = $1 and p.destroyed_at is not null
		  and ($2::uuid is null or (p.destroyed_at, p.id) < (select b.destroyed_at, b.id from projects b where b.id = $2 and b.user_id = $1 and b.destroyed_at is not null))
		order by p.destroyed_at desc, p.id desc
		limit $3`, u.ID, before, limit)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[destroyedRow])
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		out = append(out, map[string]any{
			"id": d.ID, "name": d.Name, "slug": d.Slug, "class": d.Class, "remote_url": d.RemoteURL,
			"volume_bytes": d.VolumeBytes, "destroyed_at": d.DestroyedAt, "name_free": d.NameFree,
			"restorable_until": d.SnapExpires,
			"snapshot":         map[string]any{"id": d.SnapID, "created_at": d.SnapTaken, "bytes": d.SnapBytes, "reason": d.SnapReason, "expires_at": d.SnapExpires},
		})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// restoreByName is POST /v1/projects/restore: `{slug | project_id,
// snapshot_id?, name?, start?}`. It resolves the project the way a user
// names it (a live project with that slug first, else the most recently
// destroyed ones), takes the newest restorable snapshot (or the one
// named), and restores it as a new project called `name`, by default the
// source's own name. A name a live project holds is `409 conflict` with
// `detail.reason = "name_taken"`, so the caller can ask for another.
func (s *Server) restoreByName(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u := userFrom(ctx)
	var body struct {
		Slug       string     `json:"slug"`
		ProjectID  *uuid.UUID `json:"project_id"`
		SnapshotID *uuid.UUID `json:"snapshot_id"`
		Name       *string    `json:"name"`
		Start      *bool      `json:"start"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Slug == "" && body.ProjectID == nil && body.SnapshotID == nil {
		return errf("invalid", "name the project (slug or project_id) or the snapshot (snapshot_id) to restore")
	}
	src, snap, err := s.resolveRestore(ctx, u.ID, body.Slug, body.ProjectID, body.SnapshotID)
	if err != nil {
		return err
	}
	name := src.Name
	if body.Name != nil {
		name = *body.Name
	}
	start := body.Start == nil || *body.Start
	class := ""
	if start {
		class = src.Class
	}
	if err := s.gate(r, u, billing.Request{Class: class, Disk: true, AddHeldBytes: src.HeldBytes(), VolumeBytes: src.VolumeBytes}); err != nil {
		return err
	}
	// A copy of a held project is refused whether or not it starts: it
	// would carry no hold of its own (DECISIONS I-239, I-460).
	if err := s.abuseGate(ctx, src); err != nil {
		return err
	}
	target, opID, err := s.restoreAsNew(ctx, u, src, snap, name, start)
	if err != nil {
		return err
	}
	obs.Logger(ctx, s.d.Log).Info("restore accepted", "event", "restored", "project_id", target.ID.String(), "from_project_id", src.ID.String(), "snapshot_id", snap.ID.String())
	writeJSON(w, http.StatusAccepted, map[string]any{
		"op_id": opID, "project_id": target.ID, "name": target.Name, "slug": target.Slug,
		"snapshot_id": snap.ID, "snapshot_created_at": snap.TakenAt, "snapshot_bytes": snap.Bytes, "from_project_id": src.ID,
	})
	return nil
}

// resolveRestore finds the source project and snapshot for restoreByName.
func (s *Server) resolveRestore(ctx context.Context, userID uuid.UUID, slug string, projectID, snapshotID *uuid.UUID) (*store.Project, *store.Snapshot, error) {
	notFound := func(msg string) error { return errf("not_found", "%s", msg) }
	if snapshotID != nil {
		snap, err := store.GetSnapshot(ctx, s.d.Pool, *snapshotID)
		if err != nil || snap.DeletedAt != nil || (snap.ExpiresAt != nil && snap.ExpiresAt.Before(time.Now())) {
			return nil, nil, notFound("that snapshot does not exist or has expired")
		}
		src, err := store.GetUserProjectAny(ctx, s.d.Pool, userID, snap.ProjectID)
		if err != nil {
			return nil, nil, notFound("that snapshot does not exist or has expired")
		}
		if (projectID != nil && *projectID != src.ID) || (slug != "" && slug != src.Slug) {
			return nil, nil, notFound("that snapshot is not one of " + slugOr(slug, src.Slug) + "'s")
		}
		return src, snap, nil
	}
	var candidates []store.Project
	if projectID != nil {
		p, err := store.GetUserProjectAny(ctx, s.d.Pool, userID, *projectID)
		if err != nil {
			return nil, nil, err
		}
		candidates = []store.Project{*p}
	} else {
		slug = Slug(slug)
		all, err := store.ListUserProjectsBySlug(ctx, s.d.Pool, userID, slug)
		if err != nil {
			return nil, nil, err
		}
		if len(all) == 0 {
			return nil, nil, notFound("you have no project called " + slug)
		}
		// A live project with the name is the one the user means; only
		// without one do the destroyed ones count.
		if all[0].DestroyedAt == nil {
			// A destroy still running takes the final snapshot as its
			// second step; restoring now would say "no snapshot left" or,
			// worse, restore an older one. Say so and let the caller retry
			// (I-190).
			if all[0].State == "destroying" {
				return nil, nil, withDetail(errf("conflict", "%s is still being destroyed; its final snapshot is not taken yet. Try again in a few seconds", slug),
					map[string]any{"reason": "destroying"})
			}
			candidates = all[:1]
		} else {
			candidates = all
		}
	}
	var best *store.Snapshot
	var from *store.Project
	for i := range candidates {
		snap, err := store.NewestRestorableSnapshot(ctx, s.d.Pool, candidates[i].ID)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if best == nil || snap.TakenAt.After(best.TakenAt) {
			best, from = snap, &candidates[i]
		}
	}
	if best == nil {
		return nil, nil, withDetail(errf("not_found", "%s has no snapshot left to restore", candidates[0].Slug), map[string]any{"reason": "no_snapshot"})
	}
	return from, best, nil
}

func slugOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// restoreAsNew creates a project called name from src's class, volume,
// configuration and (when no live project holds it) remote, and enqueues
// the restore of snap into it. It is the `as_new_project` path of
// POST /projects/:id/snapshots/:sid/restore and all of POST
// /projects/restore.
func (s *Server) restoreAsNew(ctx context.Context, u *store.User, src *store.Project, snap *store.Snapshot, name string, start bool) (*store.Project, uuid.UUID, error) {
	if !nameRe.MatchString(name) || Slug(name) == "" {
		return nil, uuid.Nil, errf("invalid", "the new project's name must match [A-Za-z0-9._-]{1,64}")
	}
	if u.CancelledAt != nil {
		return nil, uuid.Nil, errf("forbidden", "account is cancelled")
	}
	sub, err := billing.LiveSubscription(ctx, s.d.Pool, u.ID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	limits := billing.LimitsFor(u, sub)
	newID := store.NewID()
	var opID uuid.UUID
	err = db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, "select count(*) from projects where user_id = (select id from users where id = $1 for update) and "+countsTowardLimit, u.ID).Scan(&count); err != nil {
			return err
		}
		if count >= limits.Projects {
			return projectLimitError(count, limits.Projects, 1)
		}
		// The remote comes back with the project unless a live project
		// already has it, so the checkout finds the restored project again.
		var remote *string
		if src.RemoteURL != nil {
			var taken bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from projects where user_id = $1 and remote_url = $2 and destroyed_at is null)", u.ID, *src.RemoteURL).Scan(&taken); err != nil {
				return err
			}
			if !taken {
				remote = src.RemoteURL
			}
		}
		var err error
		opID, err = s.insertRestored(ctx, tx, u, src, snap, newID, name, src.Class, remote, start, nil)
		return err
	})
	if err != nil {
		return nil, uuid.Nil, err
	}
	s.d.Engine.Kick()
	target, err := store.GetProject(ctx, s.d.Pool, newID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return target, opID, nil
}

// insertRestored inserts the project a restore creates (id newID, called
// name, of class, with remote) from src's volume size, zone, agent, base
// and configuration, and enqueues the restore of snap into it with
// params added to the op's own. The caller holds the user's row lock and
// has checked the limits. A name a live project holds is `409 conflict`
// with `detail.reason = "name_taken"`.
func (s *Server) insertRestored(ctx context.Context, tx db.Tx, u *store.User, src *store.Project, snap *store.Snapshot, newID uuid.UUID, name, class string, remote *string, start bool, params map[string]any) (uuid.UUID, error) {
	rid := store.NewID()
	// The copy keeps the source's multiplexer, or tmux when herdr would be
	// refused for the base the copy runs (I-502).
	mux, err := copiedMultiplexer(ctx, tx, src, Slug(name), src.BaseVersion)
	if err != nil {
		return uuid.Nil, err
	}
	_, err = tx.Exec(ctx, `insert into projects (id, user_id, name, slug, remote_url, class, state, volume_bytes, tz, agent_default, base_version, config_revision_id, personal_opt_out, multiplexer, disk_held_bytes) values ($1, $2, $3, $4, $5, $6, 'stopped', $7, $8, $9, $10, $11, $12, $13, $14)`,
		newID, u.ID, name, Slug(name), remote, class, src.VolumeBytes, src.TZ, src.AgentDefault, src.BaseVersion, rid, src.PersonalOptOut, mux, src.HeldBytes())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, withDetail(errf("conflict", "a project named %s already exists; pick another name for the restored one", Slug(name)), map[string]any{"reason": "name_taken", "name": Slug(name)})
		}
		return uuid.Nil, err
	}
	if src.ConfigRevisionID != nil {
		cur, err := store.GetRevision(ctx, tx, *src.ConfigRevisionID)
		if err != nil {
			return uuid.Nil, err
		}
		// A destroyed project's closure lost its GC roots with its
		// guest (DECISIONS I-115), so the copy carries no closure
		// and the restore plan rebuilds before it boots.
		if src.DestroyedAt != nil {
			cur.SystemClosure, cur.ClosureBytes = nil, nil
		}
		// The personal layer travels with the revision (I-490), so the
		// copy is the configuration the snapshot ran.
		if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, menu, base_version, status, system_closure, closure_bytes, kernel_changed, built_at, personal, personal_revision_id, personal_opt_out) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10, $11, $12)",
			rid, newID, cur.Fragment, cur.Menu, cur.BaseVersion, revisionStatusForCopy(cur), cur.SystemClosure, cur.ClosureBytes, cur.KernelChanged, cur.Personal, cur.PersonalRevisionID, cur.PersonalOptOut); err != nil {
			return uuid.Nil, err
		}
	} else if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, status) values ($1, $2, $3, 'building')", rid, newID, DefaultFragment); err != nil {
		return uuid.Nil, err
	}
	target, err := store.GetProject(ctx, tx, newID)
	if err != nil {
		return uuid.Nil, err
	}
	hasClosure := false
	if rev, err := store.GetRevision(ctx, tx, rid); err == nil && rev.SystemClosure != nil {
		hasClosure = true
	}
	opParams := map[string]any{"start": start}
	for k, v := range params {
		opParams[k] = v
	}
	sid := snap.ID
	return s.d.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindRestore, ProjectID: &newID, SnapshotID: &sid, Params: opParams, Phases: ops.PlanRestore(target, hasClosure, start)}, false)
}
