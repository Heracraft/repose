package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/resting"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/ca/sshca"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// internalRoute resolves <slug>.<handle> for the gateway.
func (s *Server) internalRoute(w http.ResponseWriter, r *http.Request) error {
	login := r.URL.Query().Get("login")
	i := strings.LastIndexByte(login, '.')
	if i <= 0 || i == len(login)-1 {
		return errf("invalid", "login must be <project>.<user>")
	}
	slug, handle := login[:i], login[i+1:]
	p, err := store.GetProjectByLogin(r.Context(), s.d.Pool, slug, handle)
	if err != nil {
		return err
	}
	// resting (DECISIONS I-818) tells the gateway to ask for a wake
	// before it dials. The state stays `running`, so nothing in the
	// gateway refuses on this flag: its route cache holds an answer for
	// five seconds, which makes the flag stale in both directions, and
	// the wake is idempotent precisely so that does not matter.
	out := map[string]any{"project_id": p.ID, "guest_ip": nil, "state": p.State, "principals": []string{p.ID.String()},
		"host_unreachable": p.HostUnreachable, "resting": p.State == "running" && p.RestingSince != nil}
	if p.GuestIP != nil {
		out["guest_ip"] = p.GuestIP.String()
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// internalWake resumes a machine that rests and answers when it is awake
// (DECISIONS I-818). The gateway holds its dial on this call, so it
// answers within resting.WakeTimeout either way; it is idempotent, and a
// machine that was already awake is answered `was_resting: false`.
//
// Not an op: the op that needs the machine awake is itself the project's
// one open op (internal/api/resting).
func (s *Server) internalWake(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ProjectID string `json:"project_id"`
		// Reason is a lower_snake word for the host's log line; the
		// gateway sends `connect`.
		Reason string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	pid, err := uuid.Parse(body.ProjectID)
	if err != nil {
		return errf("invalid", "project_id is not valid")
	}
	if s.d.Resting == nil {
		return errf("internal", "waking is not configured on this api")
	}
	p, err := store.GetProject(r.Context(), s.d.Pool, pid)
	if err != nil {
		return err
	}
	if p.DestroyedAt != nil {
		return db.ErrNotFound
	}
	reason := resting.ReasonConnect
	if body.Reason != "" && lowerSnake(body.Reason) {
		reason = body.Reason
	}
	res, err := s.d.Resting.Wake(r.Context(), pid, reason)
	if err != nil {
		// The machine is frozen and did not wake: a failure, and the one
		// thing about a pause the user is told (the brief's S6). The
		// metric is raised inside resting.Wake, where every caller of it
		// passes -- this route is one of four (I-822).
		obs.Logger(r.Context(), s.d.Log).Warn("wake failed", "event", "guest_wake_fail", "project_id", pid.String(), "reason", reason, "err", err.Error())
		return errf("internal", "%s did not wake", p.Slug)
	}
	writeJSON(w, http.StatusOK, map[string]any{"was_resting": res.GetWasResting(), "wake_ms": res.GetWakeMs()})
	return nil
}

// lowerSnake accepts the reason words a client may send: up to 32
// lower-case letters and underscores, so nothing from a request reaches a
// log line in any other shape.
func lowerSnake(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}

func (s *Server) internalRevoked(w http.ResponseWriter, r *http.Request) error {
	since := sinceParam(r)
	serials, err := s.d.CA.RevokedSince(r.Context(), since)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, serials)
	return nil
}

func (s *Server) internalCA(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{"user_ca_pub": s.d.CA.UserCAPub(), "host_ca_pub": s.d.CA.HostCAPub()})
	return nil
}

func (s *Server) internalSessions(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ProjectID  string `json:"project_id"`
		Event      string `json:"event"`
		CertSerial int64  `json:"cert_serial"`
		// SessionID is the gateway's id for the relay (I-176); absent
		// from a gateway older than that, accepted for one release.
		SessionID string `json:"session_id"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	pid, err := uuid.Parse(body.ProjectID)
	if err != nil {
		return errf("invalid", "project_id is not valid")
	}
	if !validSessionID(body.SessionID) {
		return errf("invalid", "session_id must be up to 64 letters, digits or dashes")
	}
	if body.Event != "opened" && body.Event != "closed" {
		return errf("invalid", "event must be opened or closed")
	}
	opened := body.Event == "opened"
	closed := !opened
	if err := s.sessions.update(r.Context(), pid, body.CertSerial, body.SessionID, opened); err != nil {
		return err
	}
	ev := "session_open"
	if closed {
		ev = "session_close"
	}
	obs.Logger(r.Context(), s.d.Log).Info("gateway session", "event", ev, "project_id", pid.String(), "cert_serial", body.CertSerial)
	writeJSON(w, http.StatusOK, map[string]any{"open": s.sessions.Count(r.Context(), pid)})
	return nil
}

func (s *Server) internalHosts(w http.ResponseWriter, r *http.Request) error {
	hosts, err := store.ListHosts(r.Context(), s.d.Pool)
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, h := range hosts {
		if h.RegisteredAt == nil || h.WGPubkey == nil {
			continue
		}
		row := map[string]any{"host_id": h.ID, "name": h.Name, "wg_pubkey": *h.WGPubkey, "state": h.State}
		if h.WGIP != nil {
			row["wg_ip"] = h.WGIP.String()
		}
		if h.GuestCIDR != nil {
			row["guest_cidr"] = h.GuestCIDR.String()
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) internalGatewayCerts(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		PublicKey string `json:"public_key"`
		ProjectID string `json:"project_id"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	pub, err := sshca.ParsePublicKey(body.PublicKey)
	if err != nil {
		return errf("invalid", "public_key is not an OpenSSH public key")
	}
	pid, err := uuid.Parse(body.ProjectID)
	if err != nil {
		return errf("invalid", "project_id is not valid")
	}
	p, err := store.GetProject(r.Context(), s.d.Pool, pid)
	if err != nil {
		return err
	}
	if p.DestroyedAt != nil {
		return db.ErrNotFound
	}
	issued, err := s.d.CA.IssueGatewayCert(r.Context(), pub, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificate": issued.Certificate, "expires_at": issued.ExpiresAt})
	return nil
}

func (s *Server) internalEvents(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		SourceIP string `json:"source_ip"`
		Agent    string `json:"agent"`
		Kind     string `json:"kind"`
		Summary  string `json:"summary"`
		Window   string `json:"window"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	id, err := s.d.Events.FromEdge(r.Context(), body.SourceIP, body.Agent, body.Kind, body.Summary)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return errf("not_found", "no project at that address")
		}
		return errf("invalid", "%v", err)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"event_id": id, "ts": time.Now().UTC()})
	return nil
}

// validSessionID accepts the gateway's relay ids (hex today) and the empty
// id of an older gateway; anything else is not an id.
func validSessionID(id string) bool {
	if len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}
