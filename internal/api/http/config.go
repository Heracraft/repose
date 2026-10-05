package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/config"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/menu"
)

func revisionJSON(rev *store.Revision) map[string]any {
	out := map[string]any{"revision_id": rev.ID, "created_at": rev.CreatedAt, "status": rev.Status, "base_version": rev.BaseVersion,
		"kernel_changed": rev.KernelChanged, "reboot_required": rev.RebootRequired, "built_at": rev.BuiltAt, "applied_at": rev.AppliedAt}
	if rev.Error != nil {
		out["error"] = *rev.Error
	}
	if rev.FragmentLine != nil {
		out["fragment_line"] = *rev.FragmentLine
	}
	// The personal layer it was built with (I-490).
	out["personal"] = rev.Personal != ""
	out["personal_opt_out"] = rev.PersonalOptOut
	if rev.PersonalRevisionID != nil {
		out["personal_revision_id"] = *rev.PersonalRevisionID
	}
	if rev.PersonalLine != nil {
		out["personal_line"] = *rev.PersonalLine
	}
	return out
}

func (s *Server) currentRevision(r *http.Request, p *store.Project) (*store.Revision, error) {
	if p.ConfigRevisionID == nil {
		return nil, errf("not_found", "project has no configuration yet")
	}
	return store.GetRevision(r.Context(), s.d.Pool, *p.ConfigRevisionID)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	rev, err := s.currentRevision(r, p)
	if err != nil {
		return err
	}
	out := map[string]any{"revision_id": rev.ID, "fragment": rev.Fragment, "base_version": rev.BaseVersion, "applied_at": rev.AppliedAt, "status": rev.Status,
		// The personal layer the active revision carries (I-490): its
		// text, the account revision it came from, and the opt-out.
		"personal": rev.Personal, "personal_revision_id": rev.PersonalRevisionID, "personal_opt_out": p.PersonalOptOut}
	if rev.Menu != nil {
		out["menu"] = rev.Menu["selection"]
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var body struct {
		Fragment *string        `json:"fragment"`
		Menu     menu.Selection `json:"menu"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if (body.Fragment == nil) == (body.Menu == nil) {
		return errf("invalid", "send exactly one of fragment or menu")
	}
	if p.State == "creating" || p.State == "building" || p.State == "destroying" || p.State == "destroyed" {
		return errf("conflict", "%s is %s", p.Slug, p.State)
	}
	cur, err := s.currentRevision(r, p)
	if err != nil {
		return err
	}
	var fragment string
	var menuJSON map[string]any
	if body.Menu != nil {
		if cur.Menu == nil && !menu.IsGenerated(cur.Fragment) && strings.TrimSpace(cur.Fragment) != strings.TrimSpace(DefaultFragment) {
			return errf("conflict", "project uses a custom fragment; use fragment mode or reset")
		}
		cat, err := menuCatalog()
		if err != nil {
			return err
		}
		fragment, err = cat.Render(body.Menu)
		if err != nil {
			var me *menu.Error
			if errors.As(err, &me) {
				return errf("invalid", "%s", me.Message)
			}
			return err
		}
		menuJSON = map[string]any{"selection": body.Menu}
	} else {
		fragment = *body.Fragment
	}
	if len(fragment) > config.MaxFragmentBytes {
		return errf("invalid", "fragment exceeds 256 KB")
	}
	if err := s.checkFragment(ctx, fragment, "fragment.nix"); err != nil {
		return err
	}
	layer, err := store.PersonalFor(ctx, s.d.Pool, p)
	if err != nil {
		return err
	}
	if strings.TrimSpace(fragment) == strings.TrimSpace(cur.Fragment) && cur.Personal == layer.Text && cur.Status == "applied" {
		writeJSON(w, http.StatusOK, map[string]any{"revision_id": cur.ID, "op_id": "", "unchanged": true, "message": "configuration unchanged; " + cur.ID.String()[:8] + " is still active"})
		return nil
	}
	rid := store.NewID()
	var opID uuid.UUID
	pid := p.ID
	err = db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, menu, base_version, status, personal, personal_revision_id, personal_opt_out) values ($1, $2, $3, $4, $5, 'building', $6, $7, $8)", rid, pid, fragment, menuJSON, p.BaseVersion, layer.Text, layer.RevisionID, layer.OptOut); err != nil {
			return err
		}
		// A personal build queued by a machine.nix save (I-490) does not
		// refuse the change: this revision carries the account's current
		// machine.nix too, so one not started yet is superseded, and one
		// already building is queued behind.
		yielded, _, ok, err := s.d.Engine.YieldPersonalBuilds(ctx, tx, pid, "config")
		if err != nil {
			return err
		}
		opID, err = s.d.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, RevisionID: &rid, Phases: ops.PlanBuild(p.State == "running")}, ok)
		if err != nil {
			return err
		}
		return s.d.Engine.SupersedePersonalBuilds(ctx, tx, yielded, rid)
	})
	if err != nil {
		if errors.Is(err, ops.ErrOpInProgress) {
			return errf("conflict", "a build is in progress")
		}
		return err
	}
	s.d.Engine.Kick()
	writeJSON(w, http.StatusAccepted, map[string]any{"revision_id": rid, "op_id": opID})
	return nil
}

func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	revs, err := store.ListRevisions(r.Context(), s.d.Pool, p.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(revs))
	for i := range revs {
		out = append(out, revisionJSON(&revs[i]))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) applyRevision(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	rid, err := pathID(r, "rev")
	if err != nil {
		return err
	}
	rev, err := store.GetRevision(r.Context(), s.d.Pool, rid)
	if err != nil || rev.ProjectID != p.ID {
		return db.ErrNotFound
	}
	if rev.SystemClosure == nil || rev.Status == "failed" {
		return errf("conflict", "revision %s was never built successfully", rid.String()[:8])
	}
	reboot := r.URL.Query().Get("reboot") == "true"
	if rev.RebootRequired && !reboot && p.State == "running" {
		return withDetail(errf("conflict", "this revision changes the kernel; confirm with ?reboot=true"), map[string]any{"reboot_required": true})
	}
	pid := p.ID
	id, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindApply, ProjectID: &pid, RevisionID: &rid, Params: map[string]any{"reboot": reboot}, Phases: ops.PlanApply()}, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id, "revision_id": rid})
	return nil
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) error {
	cat, err := menuCatalog()
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, cat.Public())
	return nil
}

// menuCatalog is workstream 12's catalog (`internal/menu`, DECISIONS
// I-44), loaded and validated once; the api's own stand-in
// (`internal/nixmenu`, I-42) is gone (I-119).
var menuCatalog = sync.OnceValues(menu.Load)
