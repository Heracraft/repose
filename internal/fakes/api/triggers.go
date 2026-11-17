package api

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/heracraft/repose/internal/triggerrun"
	"github.com/heracraft/repose/internal/triggerspec"
)

// The triggers of docs/interfaces/api.md (DECISIONS I-1248..I-1262): an
// event or a time starts an agent session on the machine. The fake keeps
// the shapes the CLI and the dashboard read, including the two the real
// api derives rather than stores -- `payload_kept` off the bytes and
// `queued` for the hand-off states -- so a test sees what the api would
// send.
//
// The prompt's text and a payload's bytes never arrive here, as they
// never arrive at the real api: both are files in the machine and the
// row holds a path.

var triggerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// maxFakeTriggers mirrors triggers.MaxTriggers.
const maxFakeTriggers = 20

// FakeTriggerRun is one run as the api returns it. `project` is set by
// the cross-project listing alone, where a row has to say which machine
// it ran on.
type FakeTriggerRun struct {
	Project        string     `json:"project,omitempty"`
	RunID          string     `json:"run_id"`
	TriggerID      string     `json:"trigger_id,omitempty"`
	Trigger        string     `json:"trigger"`
	Source         string     `json:"source"`
	State          string     `json:"state"`
	Reason         string     `json:"reason,omitempty"`
	Summary        string     `json:"summary,omitempty"`
	Window         string     `json:"window,omitempty"`
	DedupeKey      string     `json:"dedupe_key,omitempty"`
	PayloadBytes   int64      `json:"payload_bytes"`
	PayloadKept    bool       `json:"payload_kept"`
	Skips          int        `json:"skips,omitempty"`
	StartedMachine bool       `json:"started_machine"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// FakeTriggerRoute is the route a route trigger fires on.
type FakeTriggerRoute struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// FakeTrigger is one trigger as the api returns it.
type FakeTrigger struct {
	TriggerID   string            `json:"trigger_id"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Route       *FakeTriggerRoute `json:"route,omitempty"`
	Spec        string            `json:"spec,omitempty"`
	Zone        string            `json:"zone,omitempty"`
	NextAt      *time.Time        `json:"next_at,omitempty"`
	PromptPath  string            `json:"prompt_path"`
	Workdir     string            `json:"workdir"`
	Agent       string            `json:"agent,omitempty"`
	TimeoutS    int               `json:"timeout_s"`
	Concurrency int               `json:"concurrency"`
	Gate        string            `json:"gate,omitempty"`
	Dedupe      string            `json:"dedupe,omitempty"`
	Notify      string            `json:"notify"`
	Enabled     bool              `json:"enabled"`
	AllowStart  bool              `json:"allow_start"`
	StopAfter   bool              `json:"stop_after"`
	CatchUp     bool              `json:"catch_up"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Last        *FakeTriggerRun   `json:"last_run,omitempty"`
	Runs        []FakeTriggerRun  `json:"runs,omitempty"`
}

// AddTriggerRun records a run against a trigger, for a test that wants a
// list with outcomes in it.
func (f *Fake) AddTriggerRun(projectID, name string, run FakeTriggerRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil {
		return
	}
	for _, t := range p.triggers {
		if t.Name != name {
			continue
		}
		if run.RunID == "" {
			run.RunID = f.nextID()
		}
		run.Trigger = name
		run.TriggerID = t.TriggerID
		run.PayloadKept = run.PayloadBytes > 0
		if run.CreatedAt.IsZero() {
			run.CreatedAt = f.now()
		}
		p.triggerRuns = append([]FakeTriggerRun{run}, p.triggerRuns...)
		t.Last = &run
	}
}

func (f *Fake) listTriggers(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	out := []FakeTrigger{}
	for _, t := range p.triggers {
		out = append(out, *t)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) putTrigger(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
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
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	if !triggerNameRe.MatchString(body.Name) {
		return invalid("name: a trigger name matches [a-z0-9-]{1,32}")
	}
	now := f.now()
	var spec triggerspec.Spec
	if body.Spec != "" {
		s, err := triggerspec.Parse(body.Spec, body.Zone)
		if err != nil {
			return invalid("spec: %v", err)
		}
		spec = s
	}
	for _, t := range p.triggers {
		if t.Name != body.Name {
			continue
		}
		// Only the fields the caller named change, and turning a
		// schedule on re-arms its clock from now (I-1252).
		if body.Enabled != nil {
			if *body.Enabled && !t.Enabled && t.Kind == "schedule" && t.Spec != "" {
				if s, err := triggerspec.Parse(t.Spec, t.Zone); err == nil {
					next := s.Next(now, now)
					t.NextAt = &next
				}
			}
			t.Enabled = *body.Enabled
		}
		if body.PromptPath != nil {
			t.PromptPath = *body.PromptPath
		}
		if body.Notify != nil {
			t.Notify = *body.Notify
		}
		if body.Spec != "" {
			next := spec.Next(now, now)
			t.Kind, t.Spec, t.Zone, t.NextAt = "schedule", spec.Text, spec.Zone, &next
		}
		t.UpdatedAt = now
		writeJSON(w, http.StatusOK, t)
		return nil
	}
	if body.Kind == "" && body.Route == "" && body.Spec == "" {
		return notFound("trigger")
	}
	if len(p.triggers) >= maxFakeTriggers {
		return invalid("a project may have %d triggers", maxFakeTriggers)
	}
	t := &FakeTrigger{TriggerID: f.nextID(), Name: body.Name, Kind: body.Kind,
		PromptPath: ".repose/triggers/" + body.Name + ".md", Workdir: "checkout",
		TimeoutS: 3600, Concurrency: 1, Notify: "failed", Enabled: true,
		CreatedAt: now, UpdatedAt: now}
	if body.PromptPath != nil && *body.PromptPath != "" {
		t.PromptPath = *body.PromptPath
	}
	for _, f := range []struct {
		v   *string
		set func(string)
	}{
		{body.Workdir, func(s string) { t.Workdir = s }},
		{body.Agent, func(s string) { t.Agent = s }},
		{body.Gate, func(s string) { t.Gate = s }},
		{body.Dedupe, func(s string) { t.Dedupe = s }},
		{body.Notify, func(s string) { t.Notify = s }},
	} {
		if f.v != nil {
			f.set(*f.v)
		}
	}
	if body.TimeoutS != nil {
		t.TimeoutS = *body.TimeoutS
	}
	if body.Concurrency != nil {
		t.Concurrency = *body.Concurrency
	}
	for _, b := range []struct {
		v   *bool
		set func(bool)
	}{
		{body.AllowStart, func(v bool) { t.AllowStart = v }},
		{body.StopAfter, func(v bool) { t.StopAfter = v }},
		{body.CatchUp, func(v bool) { t.CatchUp = v }},
		{body.Enabled, func(v bool) { t.Enabled = v }},
	} {
		if b.v != nil {
			b.set(*b.v)
		}
	}
	switch {
	case body.Route != "":
		t.Kind = "route"
		token := fakeWebhookToken()
		route := &WebhookRoute{RouteID: f.nextID(), Name: body.Route, Port: 9227,
			Path: "/t/" + body.Name, URL: webhookURL(p.ID, body.Route, token),
			CreatedAt: now, UpdatedAt: now, token: token}
		p.webhooks = append(p.webhooks, route)
		t.Route = &FakeTriggerRoute{Name: body.Route, URL: route.URL}
	case body.Spec != "":
		next := spec.Next(now, now)
		t.Kind, t.Spec, t.Zone, t.NextAt = "schedule", spec.Text, spec.Zone, &next
	}
	p.triggers = append(p.triggers, t)
	writeJSON(w, http.StatusCreated, t)
	return nil
}

func (f *Fake) getTrigger(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	for _, t := range p.triggers {
		if t.Name != r.PathValue("name") {
			continue
		}
		out := *t
		for _, run := range p.triggerRuns {
			if run.Trigger == t.Name {
				out.Runs = append(out.Runs, run)
			}
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	return notFound("trigger")
}

func (f *Fake) deleteTrigger(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	name := r.PathValue("name")
	for i, t := range p.triggers {
		if t.Name != name {
			continue
		}
		p.triggers = append(p.triggers[:i], p.triggers[i+1:]...)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	return notFound("trigger")
}

func (f *Fake) fireTrigger(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		PayloadRun  string `json:"payload_run"`
		PayloadPath string `json:"payload_path"`
		DryRun      bool   `json:"dry_run"`
	}
	if r.ContentLength > 0 {
		if e := decodeBody(r, &body, false); e != nil {
			return e
		}
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	name := r.PathValue("name")
	for _, t := range p.triggers {
		if t.Name != name {
			continue
		}
		now := f.now()
		// The row is the hand-off, so a fire answers `queued`: the
		// machine has not been asked yet (I-1162's shape).
		run := FakeTriggerRun{RunID: f.nextID(), Trigger: name, TriggerID: t.TriggerID,
			Source: triggerrun.SourceHand, State: triggerrun.Queued, CreatedAt: now}
		p.triggerRuns = append([]FakeTriggerRun{run}, p.triggerRuns...)
		t.Last = &run
		writeJSON(w, http.StatusAccepted, run)
		return nil
	}
	return notFound("trigger")
}

func (f *Fake) listTriggerRuns(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 200 {
			return invalid("limit is 1 to 200")
		}
		limit = n
	}
	want := r.URL.Query().Get("trigger")
	out := []FakeTriggerRun{}
	for _, run := range p.triggerRuns {
		if want != "" && run.Trigger != want {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, run)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// listAllTriggerRuns is GET /v1/trigger-runs: every project's runs,
// newest first, each with its project's slug.
func (f *Fake) listAllTriggerRuns(w http.ResponseWriter, r *http.Request) *apiError {
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 200 {
			return invalid("limit is 1 to 200")
		}
		limit = n
	}
	// No lock: ServeHTTP holds f.mu for the whole dispatch, as every
	// other handler here relies on. Taking it again deadlocked this
	// route, and nothing called it, so the suite stayed green.
	out := []FakeTriggerRun{}
	for _, p := range f.userProjects(userFrom(r)) {
		for _, run := range p.triggerRuns {
			run.Project = p.Slug
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) getTriggerRun(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	for _, run := range p.triggerRuns {
		if run.RunID == r.PathValue("run_id") {
			writeJSON(w, http.StatusOK, run)
			return nil
		}
	}
	return notFound("trigger run")
}
