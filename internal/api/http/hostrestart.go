package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
)

// RestartFeature is the Hello feature of a hostd that takes RestartHost:
// it parks every machine, then restarts the host (DECISIONS I-1690). The
// value is guest.FeatureRestart's; the api does not import hostd.
const RestartFeature = "restart"

// hostRestarts reports whether the host's hostd named `restart`.
func (s *Server) hostRestarts(r *http.Request, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.d.Pool.QueryRow(r.Context(), "select $2 = any(features) from hosts where id = $1", id, RestartFeature).Scan(&ok)
	return ok, err
}

// restartHost is POST /hosts/{id}/restart (DECISIONS I-1690): the
// dashboard's Restart on a Mac of the account's own, the same as
// `repose host restart` on that Mac. It queues one host_restart op; hostd
// parks every machine and restarts the Mac once the op has its answer,
// and the machines come back with their memory at the next login. A
// restart already queued or running for the host is answered with that
// op, so a repeated press is one restart.
func (s *Server) restartHost(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	h, err := s.accountHost(r, u)
	if err != nil {
		return err
	}
	body := struct {
		Force bool `json:"force"`
	}{}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	if h.State != "ready" || h.Draining {
		return withDetail(errf("conflict", "%s is %s, so it cannot be restarted from here.", h.Name, hostStateWord(h)),
			map[string]any{"reason": "host_not_ready", "state": h.State})
	}
	ok, err := s.hostRestarts(r, h.ID)
	if err != nil {
		return err
	}
	if !ok {
		return withDetail(errf("conflict", "%s cannot park its machines, so restarting it would lose what they hold in memory; restart it yourself.", h.Name),
			map[string]any{"reason": "restart_unsupported"})
	}
	var open uuid.UUID
	err = s.d.Pool.QueryRow(r.Context(), "select id from ops where host_id = $1 and kind = $2 and state in ('pending','running') order by created_at limit 1", h.ID, ops.KindHostRestart).Scan(&open)
	if err == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"op_id": open})
		return nil
	}
	hid := h.ID
	id, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindHostRestart, HostID: &hid, Params: map[string]any{"force": body.Force}, Phases: ops.PlanHostRestart()}, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id})
	return nil
}

// getHostOp is GET /hosts/{id}/ops/{op_id}: an op of one of the account's
// hosts (a host restart, I-1690), in the shape a project's op has.
func (s *Server) getHostOp(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	h, err := s.accountHost(r, u)
	if err != nil {
		return err
	}
	opID, err := pathID(r, "op_id")
	if err != nil {
		return err
	}
	op, err := store.GetOp(r.Context(), s.d.Pool, opID)
	if err != nil || op.HostID == nil || *op.HostID != h.ID || op.ProjectID != nil {
		return errf("not_found", "no operation %s", opID)
	}
	writeJSON(w, http.StatusOK, opJSON(r, op))
	return nil
}

// hostStateWord is a host's state in the words `repose host ls` and the
// dashboard use.
func hostStateWord(h *store.Host) string {
	switch {
	case h.State == "ready" && h.Draining, h.State == "draining":
		return "being removed"
	case h.State == "registering":
		return "waiting to join"
	case h.State == "unreachable":
		return "offline"
	}
	return h.State
}
