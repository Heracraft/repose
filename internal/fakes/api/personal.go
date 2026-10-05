package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The personal layer, machine.nix (DECISIONS I-490): GET/PUT /me/config
// and GET /me/config/revisions, as the real api answers them. A save
// gives every live project of the user that has not opted out a revision
// carrying the text, applied at once (the fake has no builds); a text
// holding forceEvalErrorMarker fails each project's op the way a broken
// machine.nix fails on a host, and the projects keep what they had.

type personalRev struct {
	ID        string    `json:"revision_id"`
	Fragment  string    `json:"fragment"`
	CreatedAt time.Time `json:"created_at"`
	Source    string    `json:"source"`
}

func (f *Fake) currentPersonal(u *userRec) *personalRev {
	if len(u.personal) == 0 {
		return nil
	}
	return u.personal[len(u.personal)-1]
}

func personalOut(cur *personalRev) map[string]any {
	if cur == nil {
		return map[string]any{"revision_id": nil, "fragment": "", "created_at": nil, "source": nil}
	}
	return map[string]any{"revision_id": cur.ID, "fragment": cur.Fragment, "created_at": cur.CreatedAt, "source": cur.Source}
}

func (f *Fake) getPersonal(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	out := personalOut(f.currentPersonal(u))
	opted := []string{}
	for _, p := range f.ownedProjects(u) {
		if p.PersonalOptOut {
			opted = append(opted, p.Slug)
		}
	}
	out["opted_out"] = opted
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) listPersonal(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	out := []map[string]any{}
	for i := len(u.personal) - 1; i >= 0; i-- {
		p := u.personal[i]
		out = append(out, map[string]any{"revision_id": p.ID, "created_at": p.CreatedAt, "source": p.Source, "bytes": len(p.Fragment)})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// ownedProjects is the user's live projects, oldest first.
func (f *Fake) ownedProjects(u *userRec) []*project {
	var out []*project
	for _, p := range f.projects {
		if p.owner == u.ID && !p.destroyed {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (f *Fake) putPersonal(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Fragment *string         `json:"fragment"`
		Base     json.RawMessage `json:"base_revision_id"`
		Source   string          `json:"source"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if body.Fragment == nil {
		return invalid("fragment is required (send \"\" to remove machine.nix)")
	}
	if len(*body.Fragment) > maxFragmentBytes {
		return invalid("machine exceeds 256 KB")
	}
	if body.Source == "" {
		body.Source = "cli"
	}
	u := userFrom(r)
	cur := f.currentPersonal(u)
	if len(body.Base) > 0 {
		want := ""
		if strings.TrimSpace(string(body.Base)) != "null" {
			if err := json.Unmarshal(body.Base, &want); err != nil {
				return invalid("base_revision_id must be a revision id, \"\" or null")
			}
		}
		have := ""
		if cur != nil {
			have = cur.ID
		}
		if want != have {
			d := map[string]any{"current_revision_id": nil}
			if cur != nil {
				d["current_revision_id"] = cur.ID
				d["source"] = cur.Source
				d["created_at"] = cur.CreatedAt
			}
			return errf("conflict", "machine.nix on your account changed since this copy was pushed").withDetail(d)
		}
	}
	text := *body.Fragment
	if strings.TrimSpace(text) == "" {
		text = ""
	}
	if (cur == nil && text == "") || (cur != nil && cur.Fragment == text) {
		out := personalOut(cur)
		out["unchanged"] = true
		out["projects"] = []any{}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	rev := &personalRev{ID: f.nextID(), Fragment: text, CreatedAt: f.now(), Source: body.Source}
	u.personal = append(u.personal, rev)
	changes := []map[string]any{}
	for _, p := range f.ownedProjects(u) {
		if p.PersonalOptOut || p.State == "destroying" || p.State == "error" {
			continue
		}
		pr, o := f.personalRevision(p, text)
		changes = append(changes, map[string]any{"project_id": p.ID, "slug": p.Slug, "revision_id": pr.ID, "op_id": o.id, "running": p.State == "running"})
	}
	out := personalOut(rev)
	out["projects"] = changes
	writeJSON(w, http.StatusOK, out)
	return nil
}

// personalRevision gives p a revision with its current fragment and the
// personal text, and the op that "built" it.
func (f *Fake) personalRevision(p *project, text string) (*Revision, *op) {
	now := f.now()
	fragment, base := fakeDefaultFragment, baseVersion
	var menu json.RawMessage
	if cur := p.revision(p.ConfigRevisionID); cur != nil {
		fragment, menu = cur.Fragment, cur.Menu
		if cur.BaseVersion != "" {
			base = cur.BaseVersion
		}
	}
	o := f.newOp(p, "config")
	rev := &Revision{ID: f.nextID(), CreatedAt: now, Fragment: fragment, Menu: menu, BaseVersion: base, Personal: text != "", personalText: text}
	if strings.Contains(text, forceEvalErrorMarker) {
		msg := "config error: attribute 'repose-force-eval-error' missing at machine.nix:1:3"
		rev.Status, rev.Error = "failed", msg
		o.State, o.Error = "error", msg
		p.revisions = append(p.revisions, rev)
		f.event(p, "personal_failed", "", "machine.nix did not apply to "+p.Slug+", which keeps its current configuration: "+msg)
		return rev, o
	}
	rev.Status, rev.AppliedAt = "applied", &now
	p.revisions = append(p.revisions, rev)
	p.ConfigRevisionID = rev.ID
	f.event(p, "config.applied", "", "revision applied")
	return rev, o
}

// setPersonalOptOut is PATCH personal_opt_out.
func (f *Fake) setPersonalOptOut(u *userRec, p *project, on bool) {
	if p.PersonalOptOut == on {
		return
	}
	p.PersonalOptOut = on
	text := ""
	if cur := f.currentPersonal(u); cur != nil && !on {
		text = cur.Fragment
	}
	f.personalRevision(p, text)
}
