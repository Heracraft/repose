package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
)

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
	pid := p.ID
	id, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindSnapshot, ProjectID: &pid, Params: map[string]any{"reason": "manual"}, Phases: ops.PlanSnapshot()}, false)
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
