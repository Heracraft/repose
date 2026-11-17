package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/triggers"
	"github.com/heracraft/repose/internal/api/webhooks"
	"github.com/heracraft/repose/internal/triggerspec"
)

// The trigger routes of docs/interfaces/api.md (DECISIONS I-1248..I-1262).
// A trigger binds a webhook delivery or a time to an agent session on the
// project's machine. The api owns the rows; the prompt's text and every
// payload stay in the machine and never reach these handlers.

func (s *Server) triggersEnabled() error {
	if s.d.Triggers == nil {
		return errf("internal", "triggers are not configured on this api")
	}
	return nil
}

func triggerJSON(t *triggers.Trigger, url string) map[string]any {
	out := map[string]any{
		"trigger_id":  t.ID.String(),
		"name":        t.Name,
		"kind":        t.Kind,
		"prompt_path": t.PromptPath,
		"workdir":     t.Workdir,
		"timeout_s":   t.TimeoutS,
		"concurrency": t.Concurrency,
		"notify":      t.Notify,
		"enabled":     t.Enabled,
		"allow_start": t.AllowStart,
		"stop_after":  t.StopAfter,
		"catch_up":    t.CatchUp,
		"created_at":  t.CreatedAt,
		"updated_at":  t.UpdatedAt,
	}
	if t.Agent != "" {
		out["agent"] = t.Agent
	}
	if t.Gate != "" {
		out["gate"] = t.Gate
	}
	if t.Dedupe != "" {
		out["dedupe"] = t.Dedupe
	}
	if t.Kind == triggers.KindSchedule {
		out["spec"] = t.Spec
		out["zone"] = t.Zone
		if t.NextAt != nil {
			out["next_at"] = *t.NextAt
		}
	}
	if t.RouteName != "" {
		route := map[string]any{"name": t.RouteName}
		if url != "" {
			route["url"] = url
		}
		out["route"] = route
	}
	if t.Last != nil {
		out["last_run"] = runJSON(t.Last)
	}
	return out
}

func runJSON(r *triggers.Run) map[string]any {
	out := map[string]any{
		"run_id":          r.ID.String(),
		"trigger":         r.TriggerName,
		"source":          r.Source,
		"state":           triggers.Shown(r.State),
		"payload_bytes":   r.PayloadBytes,
		"payload_kept":    r.PayloadKept(),
		"started_machine": r.StartedMachine,
		"created_at":      r.CreatedAt,
	}
	if r.ProjectSlug != "" {
		// Only the cross-project listing sets it: a row there has to say
		// which machine it ran on.
		out["project"] = r.ProjectSlug
	}
	if r.DedupeKey != "" {
		// Which event was dropped, which is the point of writing a skip
		// down at all (features/triggers.md §7.5).
		out["dedupe_key"] = r.DedupeKey
	}
	if r.TriggerID != nil {
		out["trigger_id"] = r.TriggerID.String()
	}
	if r.Reason != "" {
		out["reason"] = r.Reason
	}
	if r.Summary != "" {
		out["summary"] = r.Summary
	}
	if r.LinkURL != "" {
		link := map[string]any{"url": r.LinkURL}
		if r.LinkLabel != "" {
			link["label"] = r.LinkLabel
		}
		out["link"] = link
	}
	if r.Skips > 1 {
		// A coalesced refusal: one row standing for this many deliveries
		// in its hour (I-1254).
		out["skips"] = r.Skips
	}
	if r.Window != "" {
		out["window"] = r.Window
	}
	if r.StartedAt != nil {
		out["started_at"] = *r.StartedAt
	}
	if r.FinishedAt != nil {
		out["finished_at"] = *r.FinishedAt
	}
	return out
}

// routeURL is a route trigger's public URL, with its token: the caller
// owns the project, and a list without it would make every row a second
// round trip (I-1110's rule for `webhooks list`).
func (s *Server) routeURL(r *http.Request, projectID uuid.UUID, t *triggers.Trigger) string {
	if t.RouteName == "" || s.d.WebhookRoutes == nil {
		return ""
	}
	token, err := s.d.WebhookRoutes.Token(r.Context(), projectID, t.RouteName)
	if err != nil || token == "" {
		return ""
	}
	return s.d.WebhookRoutes.URL(projectID.String(), t.RouteName, token)
}

func (s *Server) listTriggers(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	ts, err := s.d.Triggers.List(r.Context(), p.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(ts))
	for i := range ts {
		out = append(out, triggerJSON(&ts[i], s.routeURL(r, p.ID, &ts[i])))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) getTrigger(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	t, err := s.d.Triggers.Get(r.Context(), p.ID, r.PathValue("name"))
	if err != nil {
		return triggerErr(err)
	}
	runs, err := s.d.Triggers.Runs(r.Context(), p.ID, t.Name, triggers.RunsKept)
	if err != nil {
		return err
	}
	out := triggerJSON(&t, s.routeURL(r, p.ID, &t))
	rs := make([]map[string]any, 0, len(runs))
	for i := range runs {
		rs = append(rs, runJSON(&runs[i]))
	}
	out["runs"] = rs
	writeJSON(w, http.StatusOK, out)
	return nil
}

type triggerBody struct {
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Route       string  `json:"route"`
	Spec        string  `json:"spec"`
	Zone        string  `json:"zone"`
	PromptPath  *string `json:"prompt_path"`
	Workdir     *string `json:"workdir"`
	Agent       *string `json:"agent"`
	TimeoutS    *int    `json:"timeout_s"`
	Concurrency *int    `json:"concurrency"`
	Gate        *string `json:"gate"`
	Dedupe      *string `json:"dedupe"`
	Notify      *string `json:"notify"`
	Enabled     *bool   `json:"enabled"`
	AllowStart  *bool   `json:"allow_start"`
	StopAfter   *bool   `json:"stop_after"`
	CatchUp     *bool   `json:"catch_up"`
}

func (s *Server) putTrigger(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	u := userFrom(r.Context())
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body triggerBody
	if err := decode(r, &body); err != nil {
		return err
	}
	// A temporary machine is destroyed within 24 hours, so a schedule on
	// one is a promise that dies (I-347, I-1248).
	if p.ExpiresAt != nil {
		return withDetail(errf("conflict", "a temporary machine takes no trigger: it is destroyed within 24 hours"),
			map[string]any{"reason": "temporary_machine"})
	}
	// The zone order of I-1252: the spec's own, then the project's, then
	// the laptop's (which the CLI sends as `zone`), then UTC.
	zone := body.Zone
	if zone == "" && p.TZ != nil {
		zone = *p.TZ
	}
	in := triggers.Input{
		Name: body.Name, Kind: body.Kind, Route: body.Route, Spec: body.Spec, Zone: zone,
		PromptPath: body.PromptPath, Workdir: body.Workdir, Agent: body.Agent,
		TimeoutS: body.TimeoutS, Concurrency: body.Concurrency,
		Gate: body.Gate, Dedupe: body.Dedupe, Notify: body.Notify, Enabled: body.Enabled,
		AllowStart: body.AllowStart, StopAfter: body.StopAfter, CatchUp: body.CatchUp,
	}
	t, created, err := s.d.Triggers.Put(r.Context(), u.ID, p.ID, p.Class, in)
	if err != nil {
		return triggerErr(err)
	}
	// The machine learns the whole set, as it learns the whole named
	// secret set: the api re-sends it on every start, so a guest that
	// lost it comes back with it (I-1248).
	s.pushTriggers(r, p)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, triggerJSON(&t, s.routeURL(r, p.ID, &t)))
	return nil
}

func (s *Server) deleteTrigger(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	route, err := s.d.Triggers.Delete(r.Context(), p.ID, r.PathValue("name"))
	if err != nil {
		return triggerErr(err)
	}
	if route != "" && s.d.WebhookRoutes != nil {
		if err := s.d.WebhookRoutes.Delete(r.Context(), p.ID, route); err != nil {
			return err
		}
	}
	s.pushTriggers(r, p)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type fireBody struct {
	PayloadRun string `json:"payload_run"`
	DryRun     bool   `json:"dry_run"`
	// PayloadPath is the file `repose triggers fire --payload` wrote
	// over SSH. The bytes never come through the api (I-1248).
	PayloadPath string `json:"payload_path"`
}

func (s *Server) fireTrigger(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body fireBody
	// A fire takes no body at all, which is what `repose triggers fire`
	// sends.
	if r.ContentLength > 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	opts := triggers.FireOpts{Source: triggers.SourceHand, DryRun: body.DryRun, PayloadPath: body.PayloadPath}
	if body.PayloadRun != "" {
		id, err := uuid.Parse(body.PayloadRun)
		if err != nil {
			return errf("invalid", "payload_run is a run id")
		}
		opts.PayloadRun = &id
	}
	run, err := s.d.Triggers.Fire(r.Context(), p.ID, r.PathValue("name"), opts)
	if err != nil {
		return triggerErr(err)
	}
	writeJSON(w, http.StatusAccepted, runJSON(&run))
	return nil
}

// listAllTriggerRuns is GET /trigger-runs: the user's runs across every
// project, newest first, each with its project's slug.
func (s *Server) listAllTriggerRuns(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	limit := triggers.RunsKept
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 200 {
			return errf("invalid", "limit is 1 to 200")
		}
		limit = n
	}
	runs, err := s.d.Triggers.AllRuns(r.Context(), userFrom(r.Context()).ID, limit)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(runs))
	for i := range runs {
		out = append(out, runJSON(&runs[i]))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) listTriggerRuns(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	limit := triggers.RunsKept
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 200 {
			return errf("invalid", "limit is 1 to 200")
		}
		limit = n
	}
	runs, err := s.d.Triggers.Runs(r.Context(), p.ID, r.URL.Query().Get("trigger"), limit)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(runs))
	for i := range runs {
		out = append(out, runJSON(&runs[i]))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// getTriggerRun answers one run, and with `wait` holds the request until
// the run ends: `repose triggers fire --wait` and the dashboard's row both
// read it. The long poll is the shape of the "Projects" section's, so a
// held read is marked and bounded.
func (s *Server) getTriggerRun(w http.ResponseWriter, r *http.Request) error {
	if err := s.triggersEnabled(); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(r.PathValue("run_id"))
	if err != nil {
		return errf("invalid", "a run id is a uuid")
	}
	wait, err := parseWait(r.URL.Query().Get("wait"))
	if err != nil {
		return err
	}
	run, err := s.d.Triggers.GetRun(r.Context(), p.ID, id)
	if err != nil {
		return triggerErr(err)
	}
	switch {
	case wait <= 0:
	case triggers.Terminal(run.State):
		w.Header().Set(LongPollHeader, "1")
	default:
		// The waiter bound is the op reads' own (I-236): past it the
		// answer comes at once without the header, and the client polls.
		release, ok := s.waiters.acquire(userFrom(r.Context()).ID)
		if ok {
			deadline := time.Now().Add(wait)
			for time.Now().Before(deadline) {
				select {
				case <-r.Context().Done():
					release()
					return nil // the client went away
				case <-time.After(opWaitPoll):
				}
				cur, err := s.d.Triggers.GetRun(r.Context(), p.ID, id)
				if err != nil {
					release()
					return triggerErr(err)
				}
				run = cur
				if triggers.Terminal(run.State) {
					break
				}
			}
			release()
			w.Header().Set(LongPollHeader, "1")
		}
	}
	writeJSON(w, http.StatusOK, runJSON(&run))
	return nil
}

// triggerErr maps the store's refusals to the api's codes. A name the
// project does not have is `not_found`, which the CLI turns into exit 1,
// and a bad field is `invalid`, which it turns into exit 2.
func triggerErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, triggers.ErrNotFound):
		return errf("not_found", "no trigger of that name")
	case errors.Is(err, triggers.ErrRunNotFound):
		return errf("not_found", "no run of that id")
	case errors.Is(err, triggers.ErrPayloadGone):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "payload_gone"})
	case errors.Is(err, triggers.ErrTooMany):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "too_many"})
	case errors.Is(err, triggerspec.ErrSpec):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "spec"})
	case errors.Is(err, triggers.ErrAtPromptOnPi):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "prompt_at"})
	case errors.Is(err, triggers.ErrDedupeOnSchedule):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "dedupe_on_schedule"})
	case errors.Is(err, triggers.ErrPayloadPath):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "payload_path"})
	case errors.Is(err, triggers.ErrInvalidPrompt):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "prompt_path"})
	case errors.Is(err, webhooks.ErrInvalidName):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "route_name"})
	case errors.Is(err, webhooks.ErrTooMany):
		return withDetail(errf("invalid", "%s", err.Error()), map[string]any{"reason": "too_many_routes"})
	case errors.Is(err, triggers.ErrInvalidName), errors.Is(err, triggers.ErrInvalidKind),
		errors.Is(err, triggers.ErrInvalidDedupe), errors.Is(err, triggers.ErrInvalidWorkdir),
		errors.Is(err, triggers.ErrInvalidAgent), errors.Is(err, triggers.ErrInvalidTimeout):
		return errf("invalid", "%s", err.Error())
	}
	// Every other refusal the store makes is a field the caller got
	// wrong, and a 500 on a typed flag is the first thing a new user
	// would see. A store error that is not one of ours is still an
	// internal one, which is what an unwrapped pgx error is.
	if triggers.IsInputError(err) {
		return errf("invalid", "%s", err.Error())
	}
	return err
}

// pushTriggers tells the machine the project's set changed. A stopped
// machine is not an error: the api sends the whole set again at its next
// start (I-1248).
func (s *Server) pushTriggers(r *http.Request, p *store.Project) {
	if s.d.TriggerPush == nil {
		return
	}
	s.d.TriggerPush(r.Context(), p.ID)
}
