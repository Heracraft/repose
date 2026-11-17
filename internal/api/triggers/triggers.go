// Package triggers owns a project's triggers and their runs
// (docs/features/triggers.md, DECISIONS I-1248..I-1262): the rows the CLI
// and the dashboard write, the clock that fires a schedule, the hand-off
// row a fire travels on, and the runs a machine reports back.
//
// Two things are deliberately absent from every row here. A trigger's
// prompt is a file in the machine and the row holds its path; an event's
// payload is a file in the machine and the row holds how many bytes there
// were. The privacy policy's sentence — repose never records the prompts
// you give an agent — is kept by the schema, not by a convention
// (I-1248).
//
// What is here as tenant content is a run's summary (the agent's own
// `repose-notify --done` words) and the one free-text reason, a gate's
// last line of stderr. Both are shown to the owner and never logged.
//
// A fire is a row and not a call. The api runs as two processes and only
// API_MODE=grpc holds a host's stream (I-87, I-163); I-1162 is the
// morning `say` answered machine_unreachable for every message because
// the http process sent the command. So the clock tick and the http
// process both write a `pending` run, and a worker in the grpc process
// claims it and sends RunTrigger.
package triggers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/webhooks"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/triggerspec"
)

// The limits of features/triggers.md §10 (I-1262).
const (
	// MaxTriggers is the triggers one project may have.
	MaxTriggers = 20
	// RunsKept is how many runs a trigger keeps, which is what a route
	// keeps deliveries (I-1110).
	RunsKept = 20
	// MaxConcurrency is the most runs of one trigger that may work at
	// once. A `tiny` is held to one by ClampConcurrency.
	MaxConcurrency = 3
	// PayloadsKept is how many of a trigger's payloads the guest holds,
	// which is the runs it keeps: a surviving run with bytes has its
	// payload, so `payload_kept` is read off `payload_bytes` and is not a
	// flag anything has to clear.
	PayloadsKept = 20
	// DefaultTimeout and MaxTimeout bound a run.
	DefaultTimeout = time.Hour
	MaxTimeout     = 24 * time.Hour
	// SummaryCap and ReasonCap bound the two tenant-content fields.
	SummaryCap = 1 << 10
	ReasonCap  = 200
	// FiresPerHour is the most runs a route trigger is admitted an hour.
	FiresPerHour = 60
)

// Handoff is how long the http process waits for the machine's answer to
// a `fire`, and Stale is when the worker fails a row still in the
// hand-off: past every waiter's window, so a fire cannot reach a machine
// long after the sender was told it did not (say's rule, I-1162).
const (
	Handoff = 20 * time.Second
	Stale   = 2 * Handoff
)

// Kinds of trigger.
const (
	KindRoute    = "route"
	KindSchedule = "schedule"
)

// Sources of a run.
const (
	SourceRoute = "route"
	SourceClock = "clock"
	SourceHand  = "hand"
)

// States of a run. Pending and Sending are the hand-off between the api's
// two processes and are never shown to anyone: every surface draws them
// as `queued`, as a say's are.
const (
	StatePending = "pending"
	StateSending = "sending"
	StateWorking = "working"
	StateDone    = "done"
	StateFailed  = "failed"
	StateTimeout = "timeout"
	StateSkipped = "skipped"
)

// Queued is the word a client sees for the two hand-off states.
const Queued = "queued"

// Shown turns a stored state into the word every surface prints.
func Shown(state string) string {
	if state == StatePending || state == StateSending {
		return Queued
	}
	return state
}

// Terminal reports whether a run has ended, which is what `fire --wait`
// and the dashboard's poll wait for.
func Terminal(state string) bool {
	switch state {
	case StateDone, StateFailed, StateTimeout, StateSkipped:
		return true
	}
	return false
}

// Reasons are the bounded enum a log line may carry (I-1248, the
// OBSERVABILITY rule). A gate's stderr line is the one reason that is
// free text, and it never reaches a log field.
var Reasons = map[string]bool{
	"off": true, "stopped": true, "busy": true, "error": true,
	"duplicate": true, "gated": true, "gate_timeout": true,
	"concurrency": true, "plan_limit": true,
	"prompt_missing": true, "prompt_too_large": true,
	"no_agent": true, "not_logged_in": true, "no_permission_mode": true,
	"timeout": true, "guest_restart": true, "machine_stopped": true,
	"too_many_fires": true, "unreachable": true, "payload_too_large": true,
	"route_gone": true, "payload_gone": true, "dry_run": true,
}

// The refusals a caller turns into its own message.
var (
	ErrInvalidName      = errors.New("a trigger name matches [a-z0-9-]{1,32}")
	ErrInvalidKind      = errors.New("a trigger fires on a route or on a schedule, not both")
	ErrInvalidDedupe    = errors.New("a dedupe key is header:NAME or json:FIELD")
	ErrDedupeOnSchedule = errors.New("--dedupe reads a delivery's header or body, so a schedule has nothing to read")
	ErrInvalidWorkdir   = errors.New("a workdir is checkout, worktree-N, ~/PATH or /PATH")
	ErrInvalidAgent     = errors.New("an agent is claude, codex, opencode, gemini or pi")
	ErrInvalidTimeout   = fmt.Errorf("a timeout is 0 or up to %s", MaxTimeout)
	ErrAtPromptOnPi     = errors.New("pi reads a prompt that begins with @ as a file, so it cannot be a trigger's prompt")
	ErrTooMany          = fmt.Errorf("a project may have %d triggers", MaxTriggers)
	ErrNotFound         = errors.New("no such trigger")
	ErrNoDoor           = errors.New("a new trigger fires on a route (--route) or on a schedule (--every)")
	ErrRunNotFound      = errors.New("no such run")
	ErrPayloadGone      = errors.New("that run's payload is no longer kept")
	ErrPayloadPath      = errors.New("a payload path is the file `repose triggers fire --payload` writes")
)

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	headerRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)
	jsonFldRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
	workdirRe = regexp.MustCompile(`^(checkout|worktree-[1-9][0-9]?|~/[^\x00]{1,200}|/[^\x00]{1,200})$`)
)

// Agents are the five the base installs, as `repose run --agent` takes
// them.
var Agents = map[string]bool{"claude": true, "codex": true, "opencode": true, "gemini": true, "pi": true}

// ValidName reports whether name is a usable trigger name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// ClampConcurrency holds a class to the agents its memory fits: a `tiny`
// is 2 GB, which holds one agent and one browser (I-1262).
func ClampConcurrency(class string, n int) int {
	if n < 1 {
		n = 1
	}
	if n > MaxConcurrency {
		n = MaxConcurrency
	}
	if class == "tiny" && n > 1 {
		return 1
	}
	return n
}

// PayloadDir is where `fire --payload` writes, and the only place a
// payload path may name: the api hands the path to the machine, so it is
// bounded to the trigger's own folder (I-1248).
func PayloadDir(name string) string { return "~/.repose/triggers/" + name + "/" }

// FirePayloadPath is the file `repose triggers fire --payload` writes.
func FirePayloadPath(name string) string { return PayloadDir(name) + "fire-payload" }

// ValidPayloadPath reports whether a path is the one `fire --payload`
// writes for that trigger.
func ValidPayloadPath(name, path string) bool { return path == FirePayloadPath(name) }

// ValidDedupe reads `header:NAME` or `json:FIELD`.
func ValidDedupe(s string) bool {
	kind, rest, ok := strings.Cut(s, ":")
	if !ok {
		return false
	}
	switch kind {
	case "header":
		return headerRe.MatchString(rest)
	case "json":
		return jsonFldRe.MatchString(rest)
	}
	return false
}

// Trigger is one row, with the newest run and the route's name where it
// has one.
type Trigger struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	Kind      string

	RouteID   *uuid.UUID
	RouteName string

	Spec     string
	Zone     string
	AnchorAt *time.Time
	NextAt   *time.Time

	PromptPath  string
	Workdir     string
	Agent       string
	TimeoutS    int
	Concurrency int
	Gate        string
	Dedupe      string
	Notify      string
	Enabled     bool
	AllowStart  bool
	StopAfter   bool
	CatchUp     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Last is the newest run, or nil for a trigger that never fired.
	Last *Run
}

// PayloadKept reports whether `fire --run` can replay this run: the
// guest keeps a payload for as long as the api keeps the run, so a row
// that survived retention and had bytes has its file (I-1262).
func (r Run) PayloadKept() bool { return r.PayloadBytes > 0 }

// Run is one run of a trigger.
type Run struct {
	ID           uuid.UUID
	ProjectID    uuid.UUID
	TriggerID    *uuid.UUID
	TriggerName  string
	Source       string
	State        string
	Reason       string
	Summary      string
	LinkURL      string
	LinkLabel    string
	Window       string
	DedupeKey    string
	DeliveryID   *uuid.UUID
	PayloadBytes int64
	// Skips is how many refusals this row stands for: an edge-side
	// refusal coalesces into one row per trigger, reason and hour
	// (I-1254), so a week of deliveries to a stopped machine cannot
	// evict the runs that ran.
	Skips          int
	StartedMachine bool
	// PayloadPath and DryRun are what the fire asked for, kept on the
	// row because the row is the hand-off between the api's two
	// processes (I-1162).
	PayloadPath string
	DryRun      bool
	// PayloadRun is the run whose payload this one replays.
	PayloadRun *uuid.UUID
	CommandID  *uuid.UUID
	StartedAt  *time.Time
	FinishedAt *time.Time
	CreatedAt  time.Time
}

// Input is what a caller asks for. A nil pointer leaves a field as it is
// on a trigger that already exists, which is what `add` on an existing
// name does (the webhooks rule, I-1110).
type Input struct {
	Name string
	Kind string

	Route string
	Spec  string
	Zone  string

	PromptPath  *string
	Workdir     *string
	Agent       *string
	TimeoutS    *int
	Concurrency *int
	Gate        *string
	Dedupe      *string
	Notify      *string
	Enabled     *bool
	AllowStart  *bool
	StopAfter   *bool
	CatchUp     *bool
}

// Validate checks everything that does not need the database. class is
// the project's class, for ClampConcurrency; zone is the caller's zone
// for a spec that names none (I-104's order: the spec, then the project,
// then the laptop, then UTC — the caller resolves the first three and
// passes what is left).
func (in *Input) Validate(class string) (triggerspec.Spec, error) {
	var spec triggerspec.Spec
	if !ValidName(in.Name) {
		return spec, ErrInvalidName
	}
	switch {
	case in.Kind == "" && in.Route == "" && in.Spec == "":
		// The door is left as it is: `repose triggers on|off`, and an
		// `add` on a trigger that exists which names only flags. The
		// store refuses this for a name that does not exist.
	case in.Kind == KindRoute && in.Spec == "":
		if !webhooks.ValidName(in.Route) {
			return spec, webhooks.ErrInvalidName
		}
	case in.Kind == KindSchedule && in.Route == "":
		s, err := triggerspec.Parse(in.Spec, in.Zone)
		if err != nil {
			return spec, err
		}
		spec = s
	default:
		return spec, ErrInvalidKind
	}
	if in.Workdir != nil && !workdirRe.MatchString(*in.Workdir) {
		return spec, ErrInvalidWorkdir
	}
	if in.Agent != nil && *in.Agent != "" && !Agents[*in.Agent] {
		return spec, ErrInvalidAgent
	}
	if in.TimeoutS != nil && (*in.TimeoutS < 0 || time.Duration(*in.TimeoutS)*time.Second > MaxTimeout) {
		return spec, ErrInvalidTimeout
	}
	if in.Notify != nil && *in.Notify != "failed" && *in.Notify != "never" {
		return spec, errors.New("--notify is failed or never")
	}
	if in.Dedupe != nil && *in.Dedupe != "" {
		if !ValidDedupe(*in.Dedupe) {
			return spec, ErrInvalidDedupe
		}
		// A dedupe key is read off a delivery, so a schedule carrying one
		// is a flag that can never do anything.
		if in.Kind == KindSchedule {
			return spec, ErrDedupeOnSchedule
		}
	}
	if in.Concurrency != nil {
		n := ClampConcurrency(class, *in.Concurrency)
		if *in.Concurrency < 1 || *in.Concurrency > MaxConcurrency {
			return spec, fmt.Errorf("--concurrency is 1 to %d", MaxConcurrency)
		}
		in.Concurrency = &n
	}
	return spec, nil
}

// Store is the service behind the triggers.
type Store struct {
	pool  *db.Pool
	hooks *webhooks.Store
	now   func() time.Time
}

// New builds a store. hooks may be nil on an api with no webhook routes
// configured, and a route trigger is then refused rather than half made.
func New(pool *db.Pool, hooks *webhooks.Store) *Store {
	return &Store{pool: pool, hooks: hooks, now: time.Now}
}

// SetNow is for tests.
func (s *Store) SetNow(f func() time.Time) { s.now = f }

const selectTrigger = `select t.id, t.project_id, t.name, t.kind, t.route_id, coalesce(r.name, ''),
		coalesce(t.spec, ''), coalesce(t.zone, ''), t.anchor_at, t.next_at,
		t.prompt_path, t.workdir, coalesce(t.agent, ''), t.timeout_s, t.concurrency,
		coalesce(t.gate, ''), coalesce(t.dedupe, ''), t.notify,
		t.enabled, t.allow_start, t.stop_after, t.catch_up, t.created_at, t.updated_at
	from triggers t left join webhook_routes r on r.id = t.route_id`

func scanTrigger(rows pgx.Rows) (Trigger, error) {
	var t Trigger
	err := rows.Scan(&t.ID, &t.ProjectID, &t.Name, &t.Kind, &t.RouteID, &t.RouteName,
		&t.Spec, &t.Zone, &t.AnchorAt, &t.NextAt,
		&t.PromptPath, &t.Workdir, &t.Agent, &t.TimeoutS, &t.Concurrency,
		&t.Gate, &t.Dedupe, &t.Notify,
		&t.Enabled, &t.AllowStart, &t.StopAfter, &t.CatchUp, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// List returns a project's triggers by name, each with its newest run.
func (s *Store) List(ctx context.Context, projectID uuid.UUID) ([]Trigger, error) {
	rows, err := s.pool.Query(ctx, selectTrigger+` where t.project_id = $1 order by t.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	last, err := s.lastRuns(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if r, ok := last[out[i].ID]; ok {
			out[i].Last = r
		}
	}
	return out, nil
}

// Get returns one trigger by name.
func (s *Store) Get(ctx context.Context, projectID uuid.UUID, name string) (Trigger, error) {
	rows, err := s.pool.Query(ctx, selectTrigger+` where t.project_id = $1 and t.name = $2`, projectID, name)
	if err != nil {
		return Trigger{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Trigger{}, err
		}
		return Trigger{}, ErrNotFound
	}
	t, err := scanTrigger(rows)
	if err != nil {
		return Trigger{}, err
	}
	rows.Close()
	last, err := s.lastRuns(ctx, projectID)
	if err != nil {
		return Trigger{}, err
	}
	t.Last = last[t.ID]
	return t, nil
}

// lastRuns is the newest run of each of a project's triggers, in one
// query: a list of twenty triggers is not twenty round trips.
func (s *Store) lastRuns(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID]*Run, error) {
	rows, err := s.pool.Query(ctx, `select distinct on (trigger_id) `+runCols+`
		from trigger_runs where project_id = $1 and trigger_id is not null
		order by trigger_id, created_at desc`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]*Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		if r.TriggerID != nil {
			run := r
			out[*r.TriggerID] = &run
		}
	}
	return out, rows.Err()
}

const runCols = `id, project_id, trigger_id, trigger_name, source, state, coalesce(reason, ''),
	coalesce(summary, ''), coalesce(link_url, ''), coalesce(link_label, ''),
	coalesce(tmux_window, ''), coalesce(dedupe_key, ''), delivery_id,
	payload_bytes, skips, started_machine, coalesce(payload_path, ''), dry_run, payload_run,
	command_id, started_at, finished_at, created_at`

func scanRun(rows pgx.Rows) (Run, error) {
	var r Run
	err := rows.Scan(&r.ID, &r.ProjectID, &r.TriggerID, &r.TriggerName, &r.Source, &r.State,
		&r.Reason, &r.Summary, &r.LinkURL, &r.LinkLabel, &r.Window, &r.DedupeKey, &r.DeliveryID,
		&r.PayloadBytes, &r.Skips, &r.StartedMachine, &r.PayloadPath, &r.DryRun, &r.PayloadRun,
		&r.CommandID, &r.StartedAt, &r.FinishedAt, &r.CreatedAt)
	return r, err
}

// Runs lists a project's runs, newest first. trigger empty means every
// trigger's.
func (s *Store) Runs(ctx context.Context, projectID uuid.UUID, trigger string, limit int) ([]Run, error) {
	if limit < 1 || limit > 200 {
		limit = RunsKept
	}
	q := `select ` + runCols + ` from trigger_runs where project_id = $1`
	args := []any{projectID}
	if trigger != "" {
		q += ` and trigger_name = $2`
		args = append(args, trigger)
	}
	q += fmt.Sprintf(` order by created_at desc limit %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun returns one run of a project.
func (s *Store) GetRun(ctx context.Context, projectID, runID uuid.UUID) (Run, error) {
	rows, err := s.pool.Query(ctx, `select `+runCols+` from trigger_runs where project_id = $1 and id = $2`, projectID, runID)
	if err != nil {
		return Run{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Run{}, err
		}
		return Run{}, ErrRunNotFound
	}
	return scanRun(rows)
}
