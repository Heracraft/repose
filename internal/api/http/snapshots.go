package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
)

// listAllSnapshots is GET /v1/snapshots: every live snapshot the user
// has, newest first, each naming its project (DECISIONS I-904). It is
// the account-wide twin of the per-project route below, as
// GET /v1/questions is of GET /v1/projects/{id}/questions.
//
// Destroyed projects are included: their last snapshot is kept 30 days
// and is exactly what a user looking for "all my snapshots" from a home
// folder cannot otherwise reach.
func (s *Server) listAllSnapshots(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	// limit and before page through the list, keyset on (taken_at, id),
	// as /projects/destroyed does (I-420).
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
			return errf("invalid", "before must be a snapshot id")
		}
		before = &id
	}
	rows, err := s.d.Pool.Query(r.Context(), `
		select sn.id, sn.project_id, p.slug, p.destroyed_at is not null as project_destroyed,
		       sn.taken_at, sn.bytes, sn.reason, sn.expires_at
		from snapshots sn join projects p on p.id = sn.project_id
		where p.user_id = $1 and sn.deleted_at is null
		  and ($2::uuid is null or (sn.taken_at, sn.id) < (select b.taken_at, b.id from snapshots b join projects bp on bp.id = b.project_id where b.id = $2 and bp.user_id = $1))
		order by sn.taken_at desc, sn.id desc
		limit $3`, u.ID, before, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := make([]map[string]any, 0, limit)
	for rows.Next() {
		var (
			id, projectID uuid.UUID
			slug, reason  string
			destroyed     bool
			takenAt       time.Time
			bytes         int64
			expiresAt     *time.Time
		)
		if err := rows.Scan(&id, &projectID, &slug, &destroyed, &takenAt, &bytes, &reason, &expiresAt); err != nil {
			return err
		}
		out = append(out, map[string]any{
			"id": id, "project_id": projectID, "project": slug, "project_destroyed": destroyed,
			"created_at": takenAt, "bytes": bytes, "reason": reason, "expires_at": expiresAt,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProjectAny(r)
	if err != nil {
		return err
	}
	snaps, err := store.ListSnapshots(r.Context(), s.d.Pool, p.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, map[string]any{"id": sn.ID, "created_at": sn.TakenAt, "bytes": sn.Bytes, "reason": sn.Reason, "expires_at": sn.ExpiresAt})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	if p.GuestID == nil || (p.State != "running" && p.State != "stopped") {
		return errf("conflict", "%s is %s; snapshots need a running or stopped guest", p.Slug, p.State)
	}
	// `{"reason": "fork"}` is the snapshot a fork takes when its copies
	// cannot be cloned on the project's host: it expires a day after it is
	// taken (DECISIONS I-703). No body, or any other reason, is manual.
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	reason := "manual"
	if body.Reason == "fork" {
		reason = "fork"
	}
	pid := p.ID
	id, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindSnapshot, ProjectID: &pid, Params: map[string]any{"reason": reason}, Phases: ops.PlanSnapshot()}, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id})
	return nil
}

func (s *Server) restoreSnapshot(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	ctx := r.Context()
	src, err := s.userProjectAny(r)
	if err != nil {
		return err
	}
	sid, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	snap, err := store.GetSnapshot(ctx, s.d.Pool, sid)
	if err != nil || snap.ProjectID != src.ID || snap.DeletedAt != nil {
		return db.ErrNotFound
	}
	var body struct {
		AsNewProject *string `json:"as_new_project"`
		Start        *bool   `json:"start"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	start := body.Start == nil || *body.Start
	// Restoring in place needs the memory to start; as a new project it
	// adds a volume holding about what the source holds (I-585).
	req := billing.Request{Project: src.ID}
	if start {
		req.Class = src.Class
	}
	if body.AsNewProject != nil {
		req.Disk, req.AddHeldBytes, req.VolumeBytes = true, src.HeldBytes(), src.VolumeBytes
		req.Project = uuid.Nil
	}
	if err := s.gate(r, u, req); err != nil {
		return err
	}
	// A held project's snapshot does not start anywhere, as itself or as a
	// new project, until the hold is cleared (DECISIONS I-239). A copy is
	// refused even unstarted: it would be a project with no hold of its
	// own (I-460). An unstarted restore in place keeps the project's hold.
	if start || body.AsNewProject != nil {
		if err := s.abuseGate(ctx, src); err != nil {
			return err
		}
	}
	if body.AsNewProject != nil {
		target, id, err := s.restoreAsNew(ctx, u, src, snap, *body.AsNewProject, start)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id, "project_id": target.ID})
		return nil
	}
	target := src
	if src.State != "stopped" && src.State != "error" {
		return errf("conflict", "%s must be stopped before a restore replaces its volume", src.Slug)
	}
	hasClosure := false
	if target.ConfigRevisionID != nil {
		if rev, err := store.GetRevision(ctx, s.d.Pool, *target.ConfigRevisionID); err == nil && rev.SystemClosure != nil {
			hasClosure = true
		}
	}
	tid := target.ID
	id, err := s.enqueue(ctx, ops.NewOp{Kind: ops.KindRestore, ProjectID: &tid, SnapshotID: &sid, Params: map[string]any{"start": start}, Phases: ops.PlanRestore(target, hasClosure, start)}, false)
	if err != nil {
		return err
	}
	out := map[string]any{"op_id": id, "project_id": target.ID}
	writeJSON(w, http.StatusAccepted, out)
	return nil
}

func revisionStatusForCopy(cur *store.Revision) string {
	if cur.SystemClosure != nil {
		return "built"
	}
	return "building"
}

var _ = uuid.Nil
