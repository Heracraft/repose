package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// One method per route in docs/interfaces/api.md, in the doc's order.

func (c *Client) GetMe(ctx context.Context) (*Me, error) {
	var m Me
	if err := c.get(ctx, "/me", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

type PatchMeRequest struct {
	TZ     *string      `json:"tz,omitempty"`
	Notify *NotifyPatch `json:"notify,omitempty"`
}

type NotifyPatch struct {
	Email *bool `json:"email,omitempty"`
	// NtfyURL is a pointer to a pointer so PatchMeRequest can tell "leave
	// it alone" (nil, omitted) from "clear it" (points at a nil *string,
	// marshals to JSON null) from "set it" (points at a non-nil one).
	NtfyURL **string `json:"ntfy_url,omitempty"`
}

func (c *Client) PatchMe(ctx context.Context, req PatchMeRequest) (*Me, error) {
	var m Me
	if err := c.patch(ctx, "/me", req, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) NotifyTest(ctx context.Context) (*NotifyTestResult, error) {
	var r NotifyTestResult
	if err := c.post(ctx, "/me/notify-test", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var ps []Project
	if err := c.get(ctx, "/projects", &ps); err != nil {
		return nil, err
	}
	return ps, nil
}

type CreateProjectRequest struct {
	Name         string `json:"name"`
	RemoteURL    string `json:"remote_url,omitempty"`
	Class        string `json:"class"`
	TZ           string `json:"tz,omitempty"`
	AgentDefault string `json:"agent_default,omitempty"`
	// ExpiresIn makes the project temporary (DECISIONS I-347); never sent
	// with RemoteURL.
	ExpiresIn int64 `json:"expires_in_s,omitempty"`
	// PersonalOptOut keeps the account's machine.nix off the new machine
	// (repose run --no-personal, DECISIONS I-490).
	PersonalOptOut bool `json:"personal_opt_out,omitempty"`
	// Multiplexer is tmux or herdr (DECISIONS I-502); empty leaves the
	// api's default, tmux.
	Multiplexer string `json:"multiplexer,omitempty"`
}

func (c *Client) CreateProject(ctx context.Context, req CreateProjectRequest) (*Project, error) {
	var p Project
	if err := c.post(ctx, "/projects", req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	var p Project
	if err := c.get(ctx, "/projects/"+url.PathEscape(id), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

type PatchProjectRequest struct {
	Class           *string `json:"class,omitempty"`
	HoldBaseUpdates *bool   `json:"hold_base_updates,omitempty"`
	AgentDefault    *string `json:"agent_default,omitempty"`
	TZ              *string `json:"tz,omitempty"`
	// PersonalOptOut turns machine.nix off (true) or on (false) for the
	// project (DECISIONS I-490).
	PersonalOptOut *bool `json:"personal_opt_out,omitempty"`
	// Multiplexer switches the project's multiplexer from its next start
	// (repose run --multiplexer, DECISIONS I-502).
	Multiplexer *string `json:"multiplexer,omitempty"`
}

// KeepProject makes a temporary project a normal one: PATCH
// {expires_at: null} (DECISIONS I-347). PatchProjectRequest omits empty
// fields, so the null is sent from a map of its own.
func (c *Client) KeepProject(ctx context.Context, id string) (*Project, error) {
	var p Project
	if err := c.patch(ctx, "/projects/"+url.PathEscape(id), map[string]any{"expires_at": nil}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ExtendProject gives a temporary project d more from now: PATCH
// {expires_in_s} (DECISIONS I-612).
func (c *Client) ExtendProject(ctx context.Context, id string, d time.Duration) (*Project, error) {
	var p Project
	if err := c.patch(ctx, "/projects/"+url.PathEscape(id), map[string]any{"expires_in_s": int64(d / time.Second)}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) PatchProject(ctx context.Context, id string, req PatchProjectRequest) (*Project, error) {
	var p Project
	if err := c.patch(ctx, "/projects/"+url.PathEscape(id), req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// DestroyProject is DELETE /projects/:id: 202 {op_id, state} (api.md,
// I-156). The destroy is finished only when that op is done; an older
// api that answered without an op_id is waited on by polling the
// project instead (DestroyCmd).
func (c *Client) DestroyProject(ctx context.Context, id string) (string, error) {
	var r opIDResponse
	if err := c.delete(ctx, "/projects/"+url.PathEscape(id), &r); err != nil {
		return "", err
	}
	return r.OpID, nil
}

type opIDResponse struct {
	OpID string `json:"op_id"`
}

// StartResult is POST /start's answer: the op, and whether the api turned
// the start into a restart of a guest in `error` or with a dead guestd
// (api.md, I-157; absent, so false, from older builds).
type StartResult struct {
	OpID    string `json:"op_id"`
	Restart bool   `json:"restart"`
	// Create: the project had no guest (its create failed before one
	// was made), so the api runs the create again (I-406).
	Create bool `json:"create"`
}

func (c *Client) StartProject(ctx context.Context, id string) (*StartResult, error) {
	var r StartResult
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/start", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) StopProject(ctx context.Context, id string, snapshot bool) (string, error) {
	var r opIDResponse
	body := map[string]bool{"snapshot": snapshot}
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/stop", body, &r); err != nil {
		return "", err
	}
	return r.OpID, nil
}

func (c *Client) GetOp(ctx context.Context, projectID, opID string) (*Op, error) {
	var op Op
	if err := c.get(ctx, "/projects/"+url.PathEscape(projectID)+"/ops/"+url.PathEscape(opID), &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// opLongPollHeader is the api's mark on an op read whose ?wait it
// honoured (I-236).
const opLongPollHeader = "Repose-Long-Poll"

// GetOpWait is GET /projects/:id/ops/:op_id?wait=&seen= (I-236): the api
// answers when the op's version differs from seen, the op finishes, or
// wait runs out. held reports that the api honoured the wait; an api
// without it (older, or its waiter bound reached) answers at once and
// held is false.
func (c *Client) GetOpWait(ctx context.Context, projectID, opID string, wait time.Duration, seen string) (op *Op, held bool, err error) {
	op = &Op{}
	q := url.Values{"wait": {fmt.Sprintf("%dms", wait.Milliseconds())}}
	if seen != "" {
		q.Set("seen", seen)
	}
	var hdr http.Header
	path := "/projects/" + url.PathEscape(projectID) + "/ops/" + url.PathEscape(opID) + "?" + q.Encode()
	if err := c.doHeader(ctx, http.MethodGet, path, nil, op, &hdr); err != nil {
		return nil, false, err
	}
	return op, hdr.Get(opLongPollHeader) != "", nil
}

func (c *Client) ResizeProject(ctx context.Context, id string, bytes int64) (string, error) {
	var r opIDResponse
	body := map[string]int64{"volume_bytes": bytes}
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/resize", body, &r); err != nil {
		return "", err
	}
	return r.OpID, nil
}

func (c *Client) ProjectRoute(ctx context.Context, id string) (*Route, error) {
	var r Route
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/route", &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) GetConfig(ctx context.Context, id string) (*ConfigResponse, error) {
	var cfg ConfigResponse
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/config", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type putConfigResponse struct {
	RevisionID string `json:"revision_id"`
	OpID       string `json:"op_id"`
}

func (c *Client) PutConfigFragment(ctx context.Context, id, fragment string) (revisionID, opID string, err error) {
	var r putConfigResponse
	if err := c.put(ctx, "/projects/"+url.PathEscape(id)+"/config", map[string]string{"fragment": fragment}, &r); err != nil {
		return "", "", err
	}
	return r.RevisionID, r.OpID, nil
}

func (c *Client) PutConfigMenu(ctx context.Context, id string, menu any) (revisionID, opID string, err error) {
	var r putConfigResponse
	if err := c.put(ctx, "/projects/"+url.PathEscape(id)+"/config", map[string]any{"menu": menu}, &r); err != nil {
		return "", "", err
	}
	return r.RevisionID, r.OpID, nil
}

func (c *Client) ListRevisions(ctx context.Context, id string) ([]Revision, error) {
	var rs []Revision
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/config/revisions", &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

func (c *Client) ApplyRevision(ctx context.Context, id, rev string) (string, error) {
	var r opIDResponse
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/config/revisions/"+url.PathEscape(rev)+"/apply", nil, &r); err != nil {
		return "", err
	}
	return r.OpID, nil
}

func (c *Client) Catalog(ctx context.Context) ([]CatalogItem, error) {
	var items []CatalogItem
	if err := c.get(ctx, "/catalog", &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) IssueCert(ctx context.Context, publicKey string, projectIDs []string) (*CertResponse, error) {
	var r CertResponse
	body := map[string]any{"public_key": publicKey, "project_ids": projectIDs}
	if err := c.post(ctx, "/certs", body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) RevokeCertsAll(ctx context.Context) error {
	return c.post(ctx, "/certs/revoke", map[string]bool{"all": true}, nil)
}

func (c *Client) ListSecrets(ctx context.Context, id string) ([]SecretMeta, error) {
	var s []SecretMeta
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/secrets", &s); err != nil {
		return nil, err
	}
	return s, nil
}

// PutSecretResult reports whether the value reached a running guest.
type PutSecretResult struct {
	Pushed bool `json:"pushed"`
}

func (c *Client) PutSecret(ctx context.Context, id, name string, value []byte) (*PutSecretResult, error) {
	var r PutSecretResult
	body := map[string]string{"value": b64(value)}
	if err := c.put(ctx, fmt.Sprintf("/projects/%s/secrets/%s", url.PathEscape(id), url.PathEscape(name)), body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) DeleteSecret(ctx context.Context, id, name string) error {
	return c.delete(ctx, fmt.Sprintf("/projects/%s/secrets/%s", url.PathEscape(id), url.PathEscape(name)), nil)
}

func (c *Client) ListSnapshots(ctx context.Context, id string) ([]Snapshot, error) {
	var s []Snapshot
	if err := c.get(ctx, "/projects/"+url.PathEscape(id)+"/snapshots", &s); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *Client) CreateSnapshot(ctx context.Context, id string) (string, error) {
	var r opIDResponse
	if err := c.post(ctx, "/projects/"+url.PathEscape(id)+"/snapshots", nil, &r); err != nil {
		return "", err
	}
	return r.OpID, nil
}

// RestoreSnapshot starts the restore and returns its op and the project
// that owns the op: the new one with asNew (api.md's project_id), else id.
func (c *Client) RestoreSnapshot(ctx context.Context, id, snapshotID, asNew string) (opID, projectID string, err error) {
	var r struct {
		OpID      string `json:"op_id"`
		ProjectID string `json:"project_id"`
	}
	var body map[string]string
	if asNew != "" {
		body = map[string]string{"as_new_project": asNew}
	}
	if err := c.post(ctx, fmt.Sprintf("/projects/%s/snapshots/%s/restore", url.PathEscape(id), url.PathEscape(snapshotID)), body, &r); err != nil {
		return "", "", err
	}
	if r.ProjectID == "" {
		r.ProjectID = id
	}
	return r.OpID, r.ProjectID, nil
}

func (c *Client) ListEvents(ctx context.Context, id, since string) ([]Event, error) {
	var e []Event
	path := "/projects/" + url.PathEscape(id) + "/events"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	if err := c.get(ctx, path, &e); err != nil {
		return nil, err
	}
	return e, nil
}

// ListEventsBefore is the page of up to limit events older than the event
// before, newest first (I-414). An api older than I-414 ignores both and
// answers with its newest 50.
func (c *Client) ListEventsBefore(ctx context.Context, id, before string, limit int) ([]Event, error) {
	var e []Event
	path := "/projects/" + url.PathEscape(id) + "/events?before=" + url.QueryEscape(before) + "&limit=" + strconv.Itoa(limit)
	if err := c.get(ctx, path, &e); err != nil {
		return nil, err
	}
	return e, nil
}

// LogLine is one line of GET /projects/:id/logs. A console or build
// line carries line; an ops line carries the op's kind, state, duration
// and error instead (docs/interfaces/api.md), which the CLI decoded as a
// line with no text until DECISIONS I-609. raw is the api's object as
// sent, which --json passes through.
type LogLine struct {
	TS         time.Time  `json:"ts"`
	Kind       string     `json:"kind"`
	Line       string     `json:"line,omitempty"`
	OpID       string     `json:"op_id,omitempty"`
	State      string     `json:"state,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMS *int64     `json:"duration_ms,omitempty"`
	Error      *OpError   `json:"error,omitempty"`

	raw json.RawMessage
}

func (c *Client) ProjectLogs(ctx context.Context, id, kind, since string) ([]LogLine, error) {
	q := url.Values{}
	if kind != "" {
		q.Set("kind", kind)
	}
	if since != "" {
		q.Set("since", since)
	}
	path := "/projects/" + url.PathEscape(id) + "/logs"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var lines []LogLine
	err := c.getNDJSON(ctx, path, func(dec *json.Decoder) error {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		var l LogLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return err
		}
		l.raw = raw
		lines = append(lines, l)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}

// Billing is the part of GET /billing the CLI reads: whether there is a
// plan, and the disk its projects hold against it (DECISIONS I-585).
type Billing struct {
	Subscription *struct {
		Plan   string `json:"plan"`
		Status string `json:"status"`
	} `json:"subscription"`
	Usage struct {
		// DiskHeldGB is absent from an api older than I-585.
		DiskHeldGB       *float64 `json:"disk_held_gb"`
		DiskGB           int      `json:"disk_gb"`
		RunningGB        int      `json:"running_gb"`
		MemoryGB         int      `json:"memory_gb"`
		EgressGB         float64  `json:"egress_gb"`
		EgressIncludedGB int      `json:"egress_included_gb"`
	} `json:"usage"`
	Plans []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"plans"`
}

func (c *Client) GetBilling(ctx context.Context) (*Billing, error) {
	var b Billing
	if err := c.get(ctx, "/billing", &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) BillingPortal(ctx context.Context) (string, error) {
	var r struct {
		URL string `json:"url"`
	}
	if err := c.post(ctx, "/billing/portal", nil, &r); err != nil {
		return "", err
	}
	return r.URL, nil
}

// PersonalConfig is GET /me/config: the account's machine.nix
// (DECISIONS I-490). RevisionID is nil when the account has none.
type PersonalConfig struct {
	RevisionID *string    `json:"revision_id"`
	Fragment   string     `json:"fragment"`
	CreatedAt  *time.Time `json:"created_at"`
	Source     *string    `json:"source"`
	OptedOut   []string   `json:"opted_out,omitempty"`
}

// Rev is the revision id, "" for none.
func (p *PersonalConfig) Rev() string {
	if p == nil || p.RevisionID == nil {
		return ""
	}
	return *p.RevisionID
}

// PersonalChange is one project a machine.nix save reached.
type PersonalChange struct {
	ProjectID  string `json:"project_id"`
	Slug       string `json:"slug"`
	RevisionID string `json:"revision_id"`
	OpID       string `json:"op_id,omitempty"`
	Merged     bool   `json:"merged,omitempty"`
	Running    bool   `json:"running"`
}

// PutPersonalResponse is PUT /me/config's answer.
type PutPersonalResponse struct {
	PersonalConfig
	Projects  []PersonalChange `json:"projects"`
	Unchanged bool             `json:"unchanged,omitempty"`
}

// PersonalRevision is one row of GET /me/config/revisions.
type PersonalRevision struct {
	RevisionID string    `json:"revision_id"`
	CreatedAt  time.Time `json:"created_at"`
	Source     string    `json:"source"`
	Bytes      int       `json:"bytes"`
}

func (c *Client) GetPersonal(ctx context.Context) (*PersonalConfig, error) {
	var p PersonalConfig
	if err := c.get(ctx, "/me/config", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PutPersonal saves fragment as the account's machine.nix. base, when
// not nil, is the revision the text started from ("" for none): the api
// refuses with conflict when the account's copy is another.
func (c *Client) PutPersonal(ctx context.Context, fragment string, base *string) (*PutPersonalResponse, error) {
	body := map[string]any{"fragment": fragment, "source": "cli"}
	if base != nil {
		body["base_revision_id"] = *base
	}
	var r PutPersonalResponse
	if err := c.put(ctx, "/me/config", body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) ListPersonal(ctx context.Context) ([]PersonalRevision, error) {
	var rs []PersonalRevision
	if err := c.get(ctx, "/me/config/revisions", &rs); err != nil {
		return nil, err
	}
	return rs, nil
}
