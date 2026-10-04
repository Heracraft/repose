package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"

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
