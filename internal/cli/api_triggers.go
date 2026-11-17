package cli

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// The triggers of docs/interfaces/api.md (DECISIONS I-1248..I-1262). The
// prompt's text and a payload's bytes never travel this way: the CLI
// writes both into the machine over SSH and the api holds a path.

// TriggerRoute is the webhook route a route trigger fires on.
type TriggerRoute struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// TriggerRun is one run of a trigger.
type TriggerRun struct {
	RunID       string     `json:"run_id"`
	TriggerID   string     `json:"trigger_id"`
	Trigger     string     `json:"trigger"`
	Source      string     `json:"source"`
	State       string     `json:"state"`
	Reason      string     `json:"reason"`
	Summary     string     `json:"summary"`
	Link        *EventLink `json:"link,omitempty"`
	Window      string     `json:"window"`
	DedupeKey   string     `json:"dedupe_key"`
	PayloadKept bool       `json:"payload_kept"`
	// Project is set only by the cross-project listing (`runs --all`).
	Project    string     `json:"project"`
	Skips      int        `json:"skips"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Trigger is one of a project's triggers.
type Trigger struct {
	TriggerID   string        `json:"trigger_id"`
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Route       *TriggerRoute `json:"route,omitempty"`
	Spec        string        `json:"spec"`
	Zone        string        `json:"zone"`
	NextAt      *time.Time    `json:"next_at"`
	PromptPath  string        `json:"prompt_path"`
	Workdir     string        `json:"workdir"`
	Agent       string        `json:"agent"`
	TimeoutS    int           `json:"timeout_s"`
	Concurrency int           `json:"concurrency"`
	Gate        string        `json:"gate"`
	Dedupe      string        `json:"dedupe"`
	Notify      string        `json:"notify"`
	Enabled     bool          `json:"enabled"`
	AllowStart  bool          `json:"allow_start"`
	StopAfter   bool          `json:"stop_after"`
	CatchUp     bool          `json:"catch_up"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Last        *TriggerRun   `json:"last_run,omitempty"`
	Runs        []TriggerRun  `json:"runs,omitempty"`
}

// TriggerInput is the body of POST /projects/:id/triggers. A nil field is
// one the caller did not name, which leaves it as it is on a trigger that
// exists.
type TriggerInput struct {
	Name        string  `json:"name"`
	Kind        string  `json:"kind,omitempty"`
	Route       string  `json:"route,omitempty"`
	Spec        string  `json:"spec,omitempty"`
	Zone        string  `json:"zone,omitempty"`
	PromptPath  *string `json:"prompt_path,omitempty"`
	Workdir     *string `json:"workdir,omitempty"`
	Agent       *string `json:"agent,omitempty"`
	TimeoutS    *int    `json:"timeout_s,omitempty"`
	Concurrency *int    `json:"concurrency,omitempty"`
	Gate        *string `json:"gate,omitempty"`
	Dedupe      *string `json:"dedupe,omitempty"`
	Notify      *string `json:"notify,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	AllowStart  *bool   `json:"allow_start,omitempty"`
	StopAfter   *bool   `json:"stop_after,omitempty"`
	CatchUp     *bool   `json:"catch_up,omitempty"`
}

func (c *Client) ListTriggers(ctx context.Context, id string) ([]Trigger, error) {
	out := []Trigger{}
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/triggers", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetTrigger(ctx context.Context, id, name string) (*Trigger, error) {
	var out Trigger
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/triggers/"+url.PathEscape(name), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) PutTrigger(ctx context.Context, id string, in TriggerInput) (*Trigger, error) {
	var out Trigger
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/triggers", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteTrigger(ctx context.Context, id, name string) error {
	return c.delete(ctx, "/projects/"+url.PathEscape(id)+"/triggers/"+url.PathEscape(name), nil)
}

// FireTriggerOptions is what `repose triggers fire` asks for. PayloadPath
// is a path in the machine the CLI has already written.
type FireTriggerOptions struct {
	PayloadRun  string
	PayloadPath string
	DryRun      bool
}

func (c *Client) FireTrigger(ctx context.Context, id, name string, o FireTriggerOptions) (*TriggerRun, error) {
	body := map[string]any{}
	if o.PayloadRun != "" {
		body["payload_run"] = o.PayloadRun
	}
	if o.PayloadPath != "" {
		body["payload_path"] = o.PayloadPath
	}
	if o.DryRun {
		body["dry_run"] = true
	}
	var out TriggerRun
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/triggers/"+url.PathEscape(name)+"/fire", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) TriggerRuns(ctx context.Context, id, trigger string, limit int) ([]TriggerRun, error) {
	q := url.Values{}
	if trigger != "" {
		q.Set("trigger", trigger)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/projects/" + url.PathEscape(id) + "/trigger-runs"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	out := []TriggerRun{}
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AllTriggerRuns is every project's runs in one listing, newest first,
// which `repose triggers runs --all` prints.
func (c *Client) AllTriggerRuns(ctx context.Context, limit int) ([]TriggerRun, error) {
	path := "/trigger-runs"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	out := []TriggerRun{}
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTriggerRun reads one run. wait holds the request until the run ends,
// at most the api's own ceiling; 0 answers at once.
func (c *Client) GetTriggerRun(ctx context.Context, id, runID string, wait time.Duration) (*TriggerRun, error) {
	path := "/projects/" + url.PathEscape(id) + "/trigger-runs/" + url.PathEscape(runID)
	if wait > 0 {
		path += "?wait=" + wait.String()
	}
	var out TriggerRun
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
