package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/config"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// The personal layer (DECISIONS I-490): the account's machine.nix, a
// home-manager module every machine of the account gets beside its
// project fragment, unless the project opted out.

func personalJSON(cur *store.PersonalRevision) map[string]any {
	if cur == nil {
		return map[string]any{"revision_id": nil, "fragment": "", "created_at": nil, "source": nil}
	}
	return map[string]any{"revision_id": cur.ID, "fragment": cur.Fragment, "created_at": cur.CreatedAt, "source": cur.Source}
}

func (s *Server) currentPersonal(ctx context.Context, userID uuid.UUID) (*store.PersonalRevision, error) {
	cur, err := store.CurrentPersonal(ctx, s.d.Pool, userID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	return cur, err
}

// getPersonal answers GET /v1/me/config: the account's current
// machine.nix, with an empty fragment and a null revision_id when it has
// none.
func (s *Server) getPersonal(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	cur, err := s.currentPersonal(r.Context(), u.ID)
	if err != nil {
		return err
	}
	out := personalJSON(cur)
	var optedOut []string
	rows, err := s.d.Pool.Query(r.Context(), "select slug from projects where user_id = $1 and destroyed_at is null and personal_opt_out order by created_at", u.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err == nil {
			optedOut = append(optedOut, slug)
		}
	}
	rows.Close()
	if optedOut == nil {
		optedOut = []string{}
	}
	out["opted_out"] = optedOut
	writeJSON(w, http.StatusOK, out)
	return nil
}

// listPersonal answers GET /v1/me/config/revisions, newest first.
func (s *Server) listPersonal(w http.ResponseWriter, r *http.Request) error {
	revs, err := store.ListPersonal(r.Context(), s.d.Pool, userFrom(r.Context()).ID, 50)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(revs))
	for i := range revs {
		j := personalJSON(&revs[i])
		delete(j, "fragment")
		j["bytes"] = len(revs[i].Fragment)
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// checkFragment is the validation a fragment gets before it is stored:
// size, then Nix's parser. name is the file errors name: fragment.nix for
// a project, machine.nix for the personal layer, whose line travels as
// detail.personal_line.
func (s *Server) checkFragment(ctx context.Context, fragment, name string) error {
	if len(fragment) > config.MaxFragmentBytes {
		return errf("invalid", "%s exceeds 256 KB", strings.TrimSuffix(name, ".nix"))
	}
	if s.d.Parser == nil {
		return nil
	}
	err := s.d.Parser.CheckNamed(ctx, fragment, name)
	var pe *config.ParseError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &pe):
		detail := map[string]any{}
		key := "fragment_line"
		if name == config.PersonalName {
			key = "personal_line"
		}
		if pe.Line > 0 {
			detail[key] = pe.Line
		}
		return withDetail(errf("invalid", "%s", config.Fmt(pe)), detail)
	case errors.Is(err, config.ErrParserUnavailable):
		obs.Logger(ctx, s.d.Log).Warn("fragment parse check skipped", "event", "config_parse_unavailable")
		return nil
	case errors.Is(err, config.ErrTooLarge):
		return errf("invalid", "%s exceeds 256 KB", strings.TrimSuffix(name, ".nix"))
	}
	return err
}

// putPersonal answers PUT /v1/me/config: a new machine.nix for the
// account. base_revision_id, when sent, must be the account's current
// revision ("" or null for none), or the save is refused with conflict:
// the CLI sends the revision it last pushed, so a copy edited on the
// dashboard since is never overwritten. A changed text gives every
// project that has not opted out a new revision: a running machine
// builds and switches in place, a stopped one builds and switches at its
// next start.
func (s *Server) putPersonal(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u := userFrom(ctx)
	var body struct {
		Fragment *string `json:"fragment"`
		// Base is the revision the client's text started from: absent
		// skips the check; "" or null means "the account has none". Raw,
		// so an absent field and an explicit null differ.
		Base   json.RawMessage `json:"base_revision_id"`
		Source string          `json:"source"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	checkBase := len(body.Base) > 0
	want := ""
	if checkBase && strings.TrimSpace(string(body.Base)) != "null" {
		if err := json.Unmarshal(body.Base, &want); err != nil {
			return errf("invalid", "base_revision_id must be a revision id, \"\" or null")
		}
	}
	if u.CancelledAt != nil {
		return errf("forbidden", "account is cancelled")
	}
	if body.Fragment == nil {
		return errf("invalid", "fragment is required (send \"\" to remove machine.nix)")
	}
	if body.Source == "" {
		body.Source = "cli"
	}
	if body.Source != "cli" && body.Source != "dashboard" {
		return errf("invalid", "source must be cli or dashboard")
	}
	fragment := *body.Fragment
	if strings.TrimSpace(fragment) == "" {
		fragment = ""
	}
	if fragment != "" {
		if err := s.checkFragment(ctx, fragment, config.PersonalName); err != nil {
			return err
		}
	}
	var (
		rid     uuid.UUID
		changes []ops.PersonalChange
		same    *store.PersonalRevision
	)
	err := db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		// One save at a time per account: the row lock on the user.
		if _, err := tx.Exec(ctx, "select id from users where id = $1 for update", u.ID); err != nil {
			return err
		}
		cur, err := store.CurrentPersonal(ctx, tx, u.ID)
		if errors.Is(err, db.ErrNotFound) {
			cur, err = nil, nil
		}
		if err != nil {
			return err
		}
		if checkBase {
			have := ""
			if cur != nil {
				have = cur.ID.String()
			}
			if want != have {
				d := map[string]any{"current_revision_id": nil}
				if cur != nil {
					d["current_revision_id"] = cur.ID
					d["source"] = cur.Source
					d["created_at"] = cur.CreatedAt
				}
				return withDetail(errf("conflict", "machine.nix on your account changed since this copy was pushed"), d)
			}
		}
		if (cur == nil && fragment == "") || (cur != nil && cur.Fragment == fragment) {
			same = cur
			return nil
		}
		rid = store.NewID()
		if _, err := tx.Exec(ctx, "insert into personal_revisions (id, user_id, fragment, source) values ($1, $2, $3, $4)", rid, u.ID, fragment, body.Source); err != nil {
			return err
		}
		changes, err = s.d.Engine.FanOutPersonal(ctx, tx, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	if rid == uuid.Nil {
		out := personalJSON(same)
		out["unchanged"] = true
		out["projects"] = []ops.PersonalChange{}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	s.d.Engine.Kick()
	obs.Logger(ctx, s.d.Log).Info("personal layer saved", "event", "personal_save", "user_id", u.ID.String(), "bytes", len(fragment), "projects", len(changes), "source", body.Source)
	cur, err := store.CurrentPersonal(ctx, s.d.Pool, u.ID)
	if err != nil {
		return err
	}
	out := personalJSON(cur)
	out["projects"] = changes
	writeJSON(w, http.StatusOK, out)
	return nil
}

// setPersonalOptOut records a project's opt-out and gives it a revision
// with or without the layer to match.
func (s *Server) setPersonalOptOut(ctx context.Context, p *store.Project, optOut bool) error {
	if p.PersonalOptOut == optOut {
		return nil
	}
	err := db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		fresh, err := store.GetProjectForUpdate(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update projects set personal_opt_out = $2 where id = $1", p.ID, optOut); err != nil {
			return err
		}
		fresh.PersonalOptOut = optOut
		if fresh.State == "destroying" || fresh.State == "destroyed" {
			return nil
		}
		layer, err := store.PersonalFor(ctx, tx, fresh)
		if err != nil {
			return err
		}
		reason := "opt_in"
		if optOut {
			reason = "opt_out"
		}
		_, _, err = s.d.Engine.ApplyPersonal(ctx, tx, fresh, layer, reason)
		return err
	})
	if err != nil {
		return err
	}
	s.d.Engine.Kick()
	obs.Logger(ctx, s.d.Log).Info("personal layer opt-out", "event", "personal_opt_out", "project_id", p.ID.String(), "opt_out", optOut)
	return nil
}
