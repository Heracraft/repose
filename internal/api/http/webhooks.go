package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/heracraft/repose/internal/api/lifecycle"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/webhooks"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// The webhook routes of docs/interfaces/api.md (DECISIONS I-1110). A
// route is a public URL into a project's machine; the api owns the rows,
// the CLI and the dashboard write them, and the edge reads the table
// over /internal.

func (s *Server) webhooksEnabled() error {
	if s.d.WebhookRoutes == nil {
		return errf("internal", "webhook routes are not configured on this api")
	}
	return nil
}

// routeJSON is one route as every user route returns it. token is empty
// where the caller did not ask for it.
func routeJSON(st *webhooks.Store, r *webhooks.Route, token string) map[string]any {
	out := map[string]any{
		"route_id":    r.ID.String(),
		"name":        r.Name,
		"port":        r.Port,
		"path":        r.Path,
		"allow_start": r.AllowStart,
		"created_at":  r.CreatedAt,
		"updated_at":  r.UpdatedAt,
	}
	if token != "" {
		out["url"] = st.URL(r.ProjectID.String(), r.Name, token)
	}
	if r.Last != nil {
		out["last_delivery"] = deliveryJSON(*r.Last)
	}
	return out
}

func deliveryJSON(d webhooks.Delivery) map[string]any {
	return map[string]any{"at": d.At, "status": d.Status, "outcome": d.Outcome,
		"duration_ms": d.DurationMs, "bytes_in": d.BytesIn, "bytes_out": d.BytesOut}
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	routes, err := s.d.WebhookRoutes.List(r.Context(), p.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(routes))
	for i := range routes {
		// The URL carries the token, so every list prints it: the user
		// asked for their own routes, and `repose webhooks url` would
		// otherwise be a second round trip per row.
		token, err := s.d.WebhookRoutes.Token(r.Context(), p.ID, routes[i].Name)
		if err != nil {
			return err
		}
		out = append(out, routeJSON(s.d.WebhookRoutes, &routes[i], token))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// putWebhook is POST /projects/:id/webhooks: it adds a route, or changes
// an existing one's target and --start and keeps its token, so a URL
// already pasted into a provider's dashboard goes on working.
func (s *Server) putWebhook(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body struct {
		Name       string  `json:"name"`
		Port       int     `json:"port"`
		Path       string  `json:"path"`
		AllowStart *bool   `json:"allow_start"`
		Target     *string `json:"target"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Target != nil {
		port, path, perr := webhooks.ParseTarget(*body.Target)
		if perr != nil {
			return errf("invalid", "%v", perr)
		}
		body.Port, body.Path = port, path
	}
	if !webhooks.ValidName(body.Name) {
		return errf("invalid", "%v", webhooks.ErrInvalidName)
	}
	if body.Port < 1 || body.Port > 65535 {
		return errf("invalid", "%v", webhooks.ErrInvalidTarget)
	}
	u := userFrom(r.Context())
	route, token, created, err := s.d.WebhookRoutes.Upsert(r.Context(), u.ID, p.ID, body.Name, body.Port, body.Path, body.AllowStart)
	if err != nil {
		if errors.Is(err, webhooks.ErrTooMany) {
			return errf("invalid", "%v", err)
		}
		if errors.Is(err, webhooks.ErrInvalidName) || errors.Is(err, webhooks.ErrInvalidTarget) {
			return errf("invalid", "%v", err)
		}
		return err
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "webhook_put", p.ID.String(), map[string]any{"name": route.Name}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("webhook route stored", "event", "webhook_route_put",
		"project_id", p.ID.String(), "route_id", route.ID.String(), "created", created)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, routeJSON(s.d.WebhookRoutes, route, token))
	return nil
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	route, err := s.d.WebhookRoutes.Get(r.Context(), p.ID, name)
	if err != nil {
		return webhookNotFound(err, name)
	}
	token, err := s.d.WebhookRoutes.Token(r.Context(), p.ID, name)
	if err != nil {
		return err
	}
	deliveries, err := s.d.WebhookRoutes.Deliveries(r.Context(), route.ID)
	if err != nil {
		return err
	}
	if len(deliveries) > 0 {
		route.Last = &deliveries[0]
	}
	out := routeJSON(s.d.WebhookRoutes, route, token)
	list := make([]map[string]any, 0, len(deliveries))
	for _, d := range deliveries {
		list = append(list, deliveryJSON(d))
	}
	out["deliveries"] = list
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) rotateWebhook(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	u := userFrom(r.Context())
	route, token, err := s.d.WebhookRoutes.Rotate(r.Context(), u.ID, p.ID, name)
	if err != nil {
		return webhookNotFound(err, name)
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "webhook_rotate", p.ID.String(), map[string]any{"name": name}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("webhook token rotated", "event", "webhook_route_rotate",
		"project_id", p.ID.String(), "route_id", route.ID.String())
	writeJSON(w, http.StatusOK, routeJSON(s.d.WebhookRoutes, route, token))
	return nil
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	u := userFrom(r.Context())
	if err := s.d.WebhookRoutes.Delete(r.Context(), p.ID, name); err != nil {
		return webhookNotFound(err, name)
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "webhook_delete", p.ID.String(), map[string]any{"name": name}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("webhook route removed", "event", "webhook_route_delete", "project_id", p.ID.String())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func webhookNotFound(err error, name string) error {
	if errors.Is(err, db.ErrNotFound) {
		return errf("not_found", "no webhook route named %s", name)
	}
	return err
}

// --- internal: the edge's side -------------------------------------------

// internalWebhookRoutes is the table the edge holds in memory. The reply
// carries each route's token hash, never a token.
func (s *Server) internalWebhookRoutes(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	table, err := s.d.WebhookRoutes.EdgeTable(r.Context(), r.URL.Query().Get("since"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, table)
	return nil
}

// internalWebhookDeliveries takes the edge's batch. Every outcome is one
// of the documented words; anything else is refused, so nothing a sender
// chose reaches a row.
func (s *Server) internalWebhookDeliveries(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	var body struct {
		Deliveries []struct {
			RouteID    string    `json:"route_id"`
			At         time.Time `json:"at"`
			Status     int       `json:"status"`
			Outcome    string    `json:"outcome"`
			DurationMs int       `json:"duration_ms"`
			BytesIn    int64     `json:"bytes_in"`
			BytesOut   int64     `json:"bytes_out"`
		} `json:"deliveries"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	for _, d := range body.Deliveries {
		rid, err := uuid.Parse(d.RouteID)
		if err != nil {
			return errf("invalid", "route_id is not valid")
		}
		if !webhooks.Outcomes[d.Outcome] {
			return errf("invalid", "outcome is not one of the documented words")
		}
		if d.Status < 0 || d.Status > 599 || d.DurationMs < 0 || d.BytesIn < 0 || d.BytesOut < 0 {
			return errf("invalid", "a delivery's numbers are out of range")
		}
		if err := s.d.WebhookRoutes.Report(r.Context(), rid, webhooks.Delivery{
			At: d.At, Status: d.Status, Outcome: d.Outcome, DurationMs: d.DurationMs, BytesIn: d.BytesIn, BytesOut: d.BytesOut,
		}); err != nil {
			// A route removed while its batch was in flight is not an
			// error for the edge to retry: the row is gone for good.
			if isForeignKeyViolation(err) {
				continue
			}
			return err
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// internalWebhookStart starts a stopped machine for a delivery on a route
// with --start, at most once per project per webhooks.StartInterval. The
// edge answers 503 either way; this only decides whether the machine is
// coming up (DECISIONS I-1114).
func (s *Server) internalWebhookStart(w http.ResponseWriter, r *http.Request) error {
	if err := s.webhooksEnabled(); err != nil {
		return err
	}
	var body struct {
		ProjectID string `json:"project_id"`
		RouteID   string `json:"route_id"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	pid, err := uuid.Parse(body.ProjectID)
	if err != nil {
		return errf("invalid", "project_id is not valid")
	}
	rid, err := uuid.Parse(body.RouteID)
	if err != nil {
		return errf("invalid", "route_id is not valid")
	}
	owner, err := s.d.WebhookRoutes.ProjectOf(r.Context(), rid)
	if err != nil || owner != pid {
		return errf("not_found", "no such webhook route")
	}
	route, err := store.GetProject(r.Context(), s.d.Pool, pid)
	if err != nil {
		return err
	}
	if route.DestroyedAt != nil {
		return db.ErrNotFound
	}
	if route.State != "stopped" {
		writeJSON(w, http.StatusOK, map[string]any{"started": false, "reason": "not_stopped"})
		return nil
	}
	claimed, err := s.d.WebhookRoutes.ClaimStart(r.Context(), pid)
	if err != nil {
		return err
	}
	if !claimed {
		writeJSON(w, http.StatusOK, map[string]any{"started": false, "reason": "too_soon"})
		return nil
	}
	started, reason, err := s.startForWebhook(r, pid, rid)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"started": started, "reason": reason})
	return nil
}

// startForWebhook runs the owner's own start path for a delivery on a
// route with --start: the same plan checks, the same abuse hold and the
// same op a `repose start` makes, so a start a delivery asked for cannot
// do what the user could not (DECISIONS I-1114). A refusal is not an
// error here; the edge answers 503 whatever happens, and the outcome
// word says which.
func (s *Server) startForWebhook(r *http.Request, projectID, routeID uuid.UUID) (started bool, reason string, err error) {
	p, err := store.GetProject(r.Context(), s.d.Pool, projectID)
	if err != nil {
		return false, "", err
	}
	u, err := store.GetUser(r.Context(), s.d.Pool, p.UserID)
	if err != nil {
		return false, "", err
	}
	var res *lifecycle.Result
	err = db.InTx(r.Context(), s.d.Pool, func(tx db.Tx) error {
		var ierr error
		res, ierr = lifecycle.Start(r.Context(), tx, lifecycle.Deps{Pool: s.d.Pool, Engine: s.d.Engine, Gate: s.d.Gate}, u, projectID, lifecycle.Options{GuestdDead: s.guestdDead})
		return ierr
	})
	var ref *lifecycle.Refusal
	if errors.As(err, &ref) {
		obs.Logger(r.Context(), s.d.Log).Info("webhook start refused", "event", "webhook_start_refused",
			"project_id", projectID.String(), "route_id", routeID.String(), "reason", ref.Code)
		return false, "start_refused", nil
	}
	if err != nil {
		return false, "", err
	}
	s.d.Engine.Kick()
	if _, err := store.Audit(r.Context(), s.d.Pool, "edge:webhook", "webhook_start", projectID.String(), map[string]any{"route_id": routeID.String(), "op_id": res.OpID.String()}); err != nil {
		return false, "", err
	}
	obs.Logger(r.Context(), s.d.Log).Info("webhook delivery started a machine", "event", "webhook_start",
		"project_id", projectID.String(), "route_id", routeID.String(), "op_id", res.OpID.String())
	return true, "started", nil
}

// isForeignKeyViolation reports whether err is Postgres' 23503: the
// route a batched delivery names was removed while the batch was in
// flight, which is nothing for the edge to retry.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
