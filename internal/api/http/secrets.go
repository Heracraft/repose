package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/obs"
)

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	metas, err := s.d.Secrets.List(r.Context(), p.ID.String())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(metas))
	for _, m := range metas {
		out = append(out, map[string]any{"name": m.Name, "created_at": m.CreatedAt, "updated_at": m.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if !secrets.ValidName(name) {
		if secrets.IsReserved(name) {
			return errf("invalid", "%s is reserved for the guest's sshd material", name)
		}
		return errf("invalid", "secret names match [A-Z][A-Z0-9_]{0,63}")
	}
	if secrets.IsShellName(name) {
		return errf("invalid", "%s is reserved: the machine uses it to keep each command's secrets current", name)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	value, err := base64.StdEncoding.DecodeString(body.Value)
	if err != nil {
		return errf("invalid", "value must be base64")
	}
	if len(value) > secrets.MaxValueBytes {
		return errf("invalid", "value exceeds 64 KB")
	}
	u := userFrom(r.Context())
	if err := s.d.Secrets.Put(r.Context(), u.ID.String(), p.ID.String(), name, value); err != nil {
		if errors.Is(err, secrets.ErrInvalidName) || errors.Is(err, secrets.ErrTooLarge) {
			return errf("invalid", "%v", err)
		}
		return err
	}
	s.d.Metrics.SecretsOpsTotal.WithLabelValues("put").Inc()
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "secret_put", p.ID.String(), map[string]any{"name": name}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("secret stored", "event", "secret_put", "project_id", p.ID.String())
	pushed := false
	if p.State == "running" {
		pid := p.ID
		if _, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindUpdateSecrets, ProjectID: &pid, Phases: ops.PlanUpdateSecrets()}, true); err != nil {
			return err
		}
		pushed = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "pushed": pushed})
	return nil
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if !secrets.ValidName(name) {
		return errf("invalid", "secret names match [A-Z][A-Z0-9_]{0,63}")
	}
	ok, err := s.d.Secrets.Delete(r.Context(), p.ID.String(), name)
	if err != nil {
		return err
	}
	if !ok {
		return errf("not_found", "no secret named %s", name)
	}
	u := userFrom(r.Context())
	s.d.Metrics.SecretsOpsTotal.WithLabelValues("delete").Inc()
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "secret_delete", p.ID.String(), map[string]any{"name": name}); err != nil {
		return err
	}
	if p.State == "running" {
		pid := p.ID
		if _, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindUpdateSecrets, ProjectID: &pid, Phases: ops.PlanUpdateSecrets()}, true); err != nil {
			return err
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Account secrets (DECISIONS I-697): set once for every project of the
// account, delivered with each project's own set, where a project secret
// of the same name wins.

func (s *Server) listAccountSecrets(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	metas, err := s.d.Secrets.ListAccount(r.Context(), u.ID.String())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(metas))
	for _, m := range metas {
		out = append(out, map[string]any{"name": m.Name, "created_at": m.CreatedAt, "updated_at": m.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// accountReach is where a change to the account secret name lands: the
// running projects without a secret of their own by that name, which get
// UpdateSecrets now, and the live projects that have one, which keep it.
func (s *Server) accountReach(r *http.Request, name string) (running []uuid.UUID, overridden []string, err error) {
	u := userFrom(r.Context())
	rows, err := s.d.Pool.Query(r.Context(), `select p.id, p.slug, p.state, exists (select 1 from secrets s where s.project_id = p.id and s.name = $2)
		from projects p where p.user_id = $1 and p.destroyed_at is null order by p.created_at`, u.ID, name)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	overridden = []string{}
	for rows.Next() {
		var id uuid.UUID
		var slug, state string
		var own bool
		if err := rows.Scan(&id, &slug, &state, &own); err != nil {
			return nil, nil, err
		}
		switch {
		case own:
			overridden = append(overridden, slug)
		case state == "running":
			running = append(running, id)
		}
	}
	return running, overridden, rows.Err()
}

// pushAccountChange queues UpdateSecrets for each running project the
// change reaches and returns how many were queued.
func (s *Server) pushAccountChange(r *http.Request, running []uuid.UUID) (int, error) {
	n := 0
	for _, id := range running {
		pid := id
		if _, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindUpdateSecrets, ProjectID: &pid, Phases: ops.PlanUpdateSecrets()}, true); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Server) putAccountSecret(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !secrets.ValidName(name) {
		if secrets.IsReserved(name) {
			return errf("invalid", "%s is reserved for the guest's sshd material", name)
		}
		return errf("invalid", "secret names match [A-Z][A-Z0-9_]{0,63}")
	}
	if secrets.IsShellName(name) {
		return errf("invalid", "%s is reserved: the machine uses it to keep each command's secrets current", name)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	value, err := base64.StdEncoding.DecodeString(body.Value)
	if err != nil {
		return errf("invalid", "value must be base64")
	}
	if len(value) > secrets.MaxValueBytes {
		return errf("invalid", "value exceeds 64 KB")
	}
	u := userFrom(r.Context())
	if err := s.d.Secrets.PutAccount(r.Context(), u.ID.String(), name, value); err != nil {
		if errors.Is(err, secrets.ErrInvalidName) || errors.Is(err, secrets.ErrTooLarge) {
			return errf("invalid", "%v", err)
		}
		return err
	}
	s.d.Metrics.SecretsOpsTotal.WithLabelValues("account_put").Inc()
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "account_secret_put", "user:"+u.ID.String(), map[string]any{"name": name}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("account secret stored", "event", "account_secret_put", "user_id", u.ID.String())
	running, overridden, err := s.accountReach(r, name)
	if err != nil {
		return err
	}
	pushed, err := s.pushAccountChange(r, running)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "pushed": pushed, "overridden_by": overridden})
	return nil
}

func (s *Server) deleteAccountSecret(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !secrets.ValidName(name) {
		return errf("invalid", "secret names match [A-Z][A-Z0-9_]{0,63}")
	}
	u := userFrom(r.Context())
	ok, err := s.d.Secrets.DeleteAccount(r.Context(), u.ID.String(), name)
	if err != nil {
		return err
	}
	if !ok {
		return errf("not_found", "your account has no secret named %s", name)
	}
	s.d.Metrics.SecretsOpsTotal.WithLabelValues("account_delete").Inc()
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "account_secret_delete", "user:"+u.ID.String(), map[string]any{"name": name}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("account secret removed", "event", "account_secret_delete", "user_id", u.ID.String())
	running, _, err := s.accountReach(r, name)
	if err != nil {
		return err
	}
	if _, err := s.pushAccountChange(r, running); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
