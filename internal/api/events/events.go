// Package events ingests hostd Event messages and the edge's HTTP hook
// path into the events table (05-control-plane-api.md §5.4, §5.9;
// 13-notifications.md §5.5): agent events are deduped, get an outbox row
// per enabled channel, and are rate-capped per project; guest state
// changes update the project; snapshot_done inserts a snapshots row;
// host warnings are logged and counted.
package events

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Kinds that reach the user's channels.
var notifyKinds = map[string]bool{
	"completed": true, "needs_input": true, "error": true,
	"billing_stopped": true, "base_updated": true, "base_update_failed": true, "snapshot_failed": true, "host_moved": true,
	"destroy_failed":       true,
	"notifications_paused": true,
	// A guest stopped because a miner was running (DECISIONS I-239).
	"abuse_stopped": true,
	// repose-notify and repose-ask (DECISIONS I-244).
	"agent_message": true, "agent_question": true,
	// A running machine nobody used for a day, once per idle stretch
	// (internal/api/idle, DECISIONS I-262).
	"idle_running": true,
	// A temporary machine an hour from its end, and its end (internal/api/
	// temp, DECISIONS I-347).
	"temp_expiring": true, "temp_destroyed": true,
}

// GuestKinds are the notifying kinds a guest may report, over vsock or the
// edge's hook path: the hook kinds and repose-notify's message (guestd's
// hooks package sends nothing else). Every other kind in notifyKinds is
// written by the api itself, and a guest that names one is refused, so a
// tenant's own code cannot send its owner mail worded as a platform notice
// or reach notifications_paused, which skips the dedupe and the hourly cap.
var GuestKinds = map[string]bool{"completed": true, "needs_input": true, "error": true, "agent_message": true}

// guestAgents are the agent names a guest may attribute an event to:
// guestd's five and "shell" for repose-notify and repose-ask run from a
// shell. The name reaches the notification's title and subject.
var guestAgents = map[string]bool{"claude": true, "opencode": true, "codex": true, "gemini": true, "pi": true, "shell": true}

// GuestAgent is the agent name to store for a guest-sourced event: the
// name itself when it is one guestd sends, else "".
func GuestAgent(name string) string {
	if guestAgents[name] {
		return name
	}
	return ""
}

// warningKinds are the host_warning kinds hostd sends, its own and the ones
// it relays from guestd (docs/interfaces/grpc-hostd.md, vsock-guestd.md).
// Any other kind is counted as "other": a label value outside a fixed set
// is a new Prometheus series per value (I-445).
var warningKinds = map[string]bool{
	"pool_high": true, "store_high": true, "build_queue_deep": true, "cache_unreachable": true,
	"guestd_lost": true, "freeze_timeout": true,
	"disk_high": true, "inotify_exhausted": true, "docker_down": true, "store_path_missing": true,
	"oom": true, "tmux_down": true, "guest_other": true,
}

// MaxWarningDetail caps a host warning's detail in the api log.
const MaxWarningDetail = 256

// Caps on the agent name and window name stored with an event.
const (
	MaxAgent  = 32
	MaxWindow = 64
)

// GuestEventsPerHour caps the guest-raised events (agent events and
// questions) stored per project per hour. hostd already limits a guest's
// notifications; this holds whatever reaches the api (I-445).
const GuestEventsPerHour = 600

// noDedupe are kinds the user sent on purpose, one notification each: two
// messages in a minute are two messages, and a question collapsed into an
// earlier one would never be answerable.
var noDedupe = map[string]bool{"agent_message": true, "agent_question": true}

// QuestionHandler is the question store (internal/api/questions), which
// sits on top of this package.
type QuestionHandler interface {
	// OnQuestion records a guest's question and notifies the owner; an
	// error leaves the host event unacked, so it is sent again.
	OnQuestion(ctx context.Context, ts time.Time, q *hostdv1.AgentQuestion) error
	// GuestStopped cancels the project's pending questions: the asker went
	// down with its guest.
	GuestStopped(ctx context.Context, projectID uuid.UUID) error
}

// MaxSummary is the summary cap.
const MaxSummary = 1024

// DedupeWindow collapses repeats of one event.
const DedupeWindow = 60 * time.Second

// RatePerHour is the per-project notification cap.
const RatePerHour = 30

// Ingest writes events.
type Ingest struct {
	pool      *db.Pool
	m         *metrics.M
	log       *slog.Logger
	now       func() time.Time
	questions QuestionHandler
}

// SetQuestions installs the question store (before serving).
func (i *Ingest) SetQuestions(q QuestionHandler) { i.questions = q }

// New builds an ingest.
func New(pool *db.Pool, m *metrics.M, log *slog.Logger) *Ingest {
	return &Ingest{pool: pool, m: m, log: log.With("component", "api"), now: time.Now}
}

// Incoming describes an event to insert.
type Incoming struct {
	// ID, when set, is the event's id; a repeat insert with it is a
	// duplicate. Questions set it so their row can name the event first.
	ID          uuid.UUID
	ProjectID   uuid.UUID
	TS          time.Time
	Kind        string
	Agent       string
	Window      string
	Summary     string
	Source      string
	HostEventID string
}

// Insert stores an event with dedupe and outbox rows. inserted is false
// when it collapsed into an earlier one or was a duplicate.
func (i *Ingest) Insert(ctx context.Context, n Incoming) (id uuid.UUID, inserted bool, err error) {
	now := i.now()
	ts := n.TS
	var skew *int
	if ts.IsZero() || ts.Sub(now).Abs() > 5*time.Minute {
		if !ts.IsZero() {
			s := int(now.Sub(ts).Seconds())
			skew = &s
		}
		ts = now
	}
	// Text columns refuse a NUL and invalid UTF-8; one such event would
	// stay unacked and come back on every reconnect.
	n.Summary = store.CleanText(n.Summary, MaxSummary)
	n.Agent = store.CleanText(n.Agent, MaxAgent)
	n.Window = store.CleanText(n.Window, MaxWindow)
	var agent, window, hostEventID *string
	if n.Agent != "" {
		agent = &n.Agent
	}
	if n.Window != "" {
		window = &n.Window
	}
	if n.HostEventID != "" {
		hostEventID = &n.HostEventID
	}
	if n.Source == "" {
		n.Source = "host"
	}
	err = db.InTx(ctx, i.pool, func(tx db.Tx) error {
		if notifyKinds[n.Kind] && n.Kind != "notifications_paused" && !noDedupe[n.Kind] {
			// Collapse a repeat within the window, appending a new summary.
			var prevID uuid.UUID
			var prevSummary string
			err := tx.QueryRow(ctx, `select id, summary from events where project_id = $1 and coalesce(agent,'') = $2 and kind = $3 and ts > $4 order by ts desc limit 1 for update`,
				n.ProjectID, n.Agent, n.Kind, ts.Add(-DedupeWindow)).Scan(&prevID, &prevSummary)
			if err == nil {
				if n.Summary != "" && !strings.Contains(prevSummary, n.Summary) {
					merged := store.CleanText(prevSummary+"\n"+n.Summary, MaxSummary)
					if _, err := tx.Exec(ctx, "update events set summary = $2 where id = $1", prevID, merged); err != nil {
						return err
					}
				}
				id = prevID
				i.m.EventsTotal.WithLabelValues("deduped").Inc()
				return nil
			}
			if !db.IsNoRows(err) {
				return err
			}
		}
		id = n.ID
		if id == uuid.Nil {
			id = store.NewID()
		}
		tag, err := tx.Exec(ctx, `insert into events (id, project_id, ts, ts_second, kind, agent, tmux_window, summary, source, skew_seconds, host_event_id) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) on conflict do nothing`,
			id, n.ProjectID, ts, ts.Unix(), n.Kind, agent, window, n.Summary, n.Source, skew, hostEventID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			i.m.EventsTotal.WithLabelValues("duplicate").Inc()
			id = uuid.Nil
			return nil
		}
		inserted = true
		i.m.EventsTotal.WithLabelValues(n.Kind).Inc()
		if !notifyKinds[n.Kind] {
			return nil
		}
		return i.enqueue(ctx, tx, id, n.ProjectID, n.Kind, ts)
	})
	return id, inserted, err
}

// enqueue writes one outbox row per enabled channel, applying the
// per-project hourly cap with a single digest event past it.
func (i *Ingest) enqueue(ctx context.Context, tx db.Tx, eventID, projectID uuid.UUID, kind string, ts time.Time) error {
	var notifyEmail bool
	var email, ntfy *string
	if err := tx.QueryRow(ctx, "select u.notify_email, u.email, u.ntfy_url from users u join projects p on p.user_id = u.id where p.id = $1", projectID).Scan(&notifyEmail, &email, &ntfy); err != nil {
		return err
	}
	var channels []string
	if notifyEmail && email != nil && *email != "" {
		channels = append(channels, "email")
	}
	if ntfy != nil && *ntfy != "" {
		channels = append(channels, "ntfy")
	}
	if len(channels) == 0 {
		return nil
	}
	if kind != "notifications_paused" {
		var recent int
		if err := tx.QueryRow(ctx, `select count(distinct o.event_id) from events_outbox o join events e on e.id = o.event_id where e.project_id = $1 and e.ts > $2 and e.kind <> 'notifications_paused'`, projectID, ts.Add(-time.Hour)).Scan(&recent); err != nil {
			return err
		}
		if recent >= RatePerHour {
			var paused int
			if err := tx.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = 'notifications_paused' and ts > $2", projectID, ts.Add(-time.Hour)).Scan(&paused); err != nil {
				return err
			}
			if paused == 0 {
				did := store.NewID()
				if _, err := tx.Exec(ctx, `insert into events (id, project_id, ts, ts_second, kind, summary, source) values ($1, $2, $3, $4, 'notifications_paused', $5, 'api')`,
					did, projectID, ts, ts.Unix(), "30+ events in the last hour; notifications for this project are paused until the top of the hour. See the dashboard."); err != nil {
					return err
				}
				for _, ch := range channels {
					if _, err := tx.Exec(ctx, "insert into events_outbox (event_id, channel) values ($1, $2)", did, ch); err != nil {
						return err
					}
				}
			}
			i.m.EventsTotal.WithLabelValues("rate_limited").Inc()
			return nil
		}
	}
	for _, ch := range channels {
		if _, err := tx.Exec(ctx, "insert into events_outbox (event_id, channel) values ($1, $2) on conflict do nothing", eventID, ch); err != nil {
			return err
		}
	}
	return nil
}

// OnEvent handles a hostd Event and reports whether to ack it.
func (i *Ingest) OnEvent(ctx context.Context, hostID uuid.UUID, ev *hostdv1.Event) bool {
	ts := time.Unix(ev.Ts, 0)
	switch e := ev.Ev.(type) {
	case *hostdv1.Event_GuestStateChanged:
		p, err := i.projectOnHost(ctx, hostID, e.GuestStateChanged.GuestId)
		if err != nil {
			if !errors.Is(err, store.ErrOtherHost) {
				i.log.Warn("state change for unknown guest", "event", "guest_state", "host_id", hostID.String(), "guest_id", e.GuestStateChanged.GuestId)
			}
			return true
		}
		open, _ := store.OpenOpsForProject(ctx, i.pool, p.ID)
		st := e.GuestStateChanged.State
		if len(open) == 0 && p.State != st && p.State != "destroyed" && validState(st) {
			if err := store.SetProjectState(ctx, i.pool, p.ID, st); err != nil {
				i.log.Error("state update", "event", "guest_state", "project_id", p.ID.String(), "err", err.Error())
				return false
			}
			i.log.Info("guest state from host", "event", "guest_state", "project_id", p.ID.String(), "state", st, "reason", e.GuestStateChanged.Reason)
			// The reason shown with an error state is this one, never an
			// older error left by an op the guest survived (a failed
			// config build showed as the cause of a later crash).
			if st == "error" {
				reason := "internal: the environment stopped unexpectedly"
				if r := e.GuestStateChanged.Reason; r != "" {
					reason += " (" + r + ")"
				}
				_, _ = i.pool.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, reason) // best effort; the event below carries the state
			}
		}
		_, _, err = i.Insert(ctx, Incoming{ProjectID: p.ID, TS: ts, Kind: "guest_state_changed", Summary: st, Source: "host", HostEventID: ev.EventId})
		if err != nil {
			i.log.Error("state event insert", "event", "guest_state", "err", err.Error())
			return false
		}
		if i.questions != nil && st != "running" && st != "starting" && st != "creating" && st != "building" {
			if err := i.questions.GuestStopped(ctx, p.ID); err != nil {
				i.log.Error("cancel questions of a stopped guest", "event", "agent_question", "project_id", p.ID.String(), "err", err.Error())
				return false
			}
		}
		return true
	case *hostdv1.Event_AgentQuestion:
		if i.questions == nil {
			return true
		}
		p, err := i.projectOnHost(ctx, hostID, e.AgentQuestion.GuestId)
		if err != nil {
			return true
		}
		// A close (a state set) is never capped: it ends a question the
		// cap already let in.
		if e.AgentQuestion.State == "" {
			if capped, err := i.overGuestCap(ctx, p.ID); err != nil {
				i.log.Error("guest event count", "event", "agent_question", "err", err.Error())
				return false
			} else if capped {
				return true
			}
		}
		if err := i.questions.OnQuestion(ctx, ts, e.AgentQuestion); err != nil {
			i.log.Error("agent question insert", "event", "agent_question", "err", err.Error())
			return false
		}
		return true
	case *hostdv1.Event_AgentEvent:
		p, err := i.projectOnHost(ctx, hostID, e.AgentEvent.GuestId)
		if err != nil {
			return true
		}
		kind := e.AgentEvent.Kind
		if !GuestKinds[kind] {
			kind = "error"
		}
		if capped, err := i.overGuestCap(ctx, p.ID); err != nil {
			i.log.Error("guest event count", "event", "agent_event", "err", err.Error())
			return false
		} else if capped {
			return true
		}
		_, _, err = i.Insert(ctx, Incoming{ProjectID: p.ID, TS: ts, Kind: kind, Agent: GuestAgent(store.CleanText(e.AgentEvent.Agent, MaxAgent)), Window: e.AgentEvent.TmuxWindow, Summary: e.AgentEvent.Summary, Source: "host", HostEventID: ev.EventId})
		if err != nil {
			i.log.Error("agent event insert", "event", "agent_event", "err", err.Error())
			return false
		}
		return true
	case *hostdv1.Event_SnapshotDone:
		p, err := i.projectOnHost(ctx, hostID, e.SnapshotDone.GuestId)
		if err != nil {
			return true
		}
		if e.SnapshotDone.BlobPath == "" {
			return true
		}
		if !SnapshotPathOf(p, e.SnapshotDone.BlobPath) {
			i.m.HostReportsRefused.WithLabelValues("bad_snapshot").Inc()
			i.log.Warn("snapshot path outside the project's prefix", "event", "snapshot_done", "host_id", hostID.String(), "project_id", p.ID.String())
			return true
		}
		if now := i.now(); ts.After(now) {
			ts = now
		}
		_, err = i.pool.Exec(ctx, `insert into snapshots (id, project_id, host_id, blob_path, bytes, reason, taken_at) values ($1, $2, $3, $4, $5, 'scheduled', $6) on conflict (blob_path) do nothing`,
			store.NewID(), p.ID, p.HostID, e.SnapshotDone.BlobPath, int64(e.SnapshotDone.Bytes), ts)
		if err != nil {
			i.log.Error("snapshot event insert", "event", "snapshot_done", "err", err.Error())
			return false
		}
		return true
	case *hostdv1.Event_HostWarning:
		kind := e.HostWarning.Kind
		if !warningKinds[kind] {
			kind = "other"
		}
		i.m.HostWarningsTotal.WithLabelValues(kind).Inc()
		// hostd writes the detail (a guest's is rebuilt from its numbers
		// and process name); the cap and cleaning hold it to one short line
		// whatever a host sends.
		i.log.Warn("host warning", "event", "host_warning", "host_id", hostID.String(), "kind", kind, "detail", cleanLine(e.HostWarning.Detail, MaxWarningDetail))
		return true
	case *hostdv1.Event_OperatorLogin:
		// The audit row 14 §5 requires for every operator SSH login
		// (I-140). The host re-sends an event whose ack was lost, so the
		// host event id in the detail is what keeps it to one row.
		ol := e.OperatorLogin
		var dup bool
		if err := i.pool.QueryRow(ctx, "select exists(select 1 from audit_log where action = 'operator_login' and detail->>'host_event_id' = $1)", ev.EventId).Scan(&dup); err != nil {
			i.log.Error("operator login lookup", "event", "operator_login", "err", err.Error())
			return false
		}
		if dup {
			return true
		}
		actor := "operator"
		switch {
		case ol.KeyId != "":
			actor = ol.KeyId
		case ol.KeyFingerprint != "":
			actor = "operator:key:" + ol.KeyFingerprint
		}
		detail := map[string]any{"pam_type": ol.PamType, "user_present": ol.UserPresent, "key_id": ol.KeyId, "serial": ol.Serial, "key_fingerprint": ol.KeyFingerprint, "host_event_id": ev.EventId, "ts": ts.UTC().Format(time.RFC3339)}
		if _, err := store.Audit(ctx, i.pool, actor, "operator_login", hostID.String(), detail); err != nil {
			i.log.Error("operator login audit insert", "event", "operator_login", "err", err.Error())
			return false
		}
		i.log.Log(ctx, slog.Level(2), "operator login", "event", "operator_login", "host_id", hostID.String(), "key_id", ol.KeyId, "cert_serial", ol.Serial)
		return true
	}
	return true
}

func validState(s string) bool {
	switch s {
	case "creating", "building", "starting", "running", "stopping", "stopped", "restoring", "destroying", "destroyed", "error":
		return true
	}
	return false
}

// projectOnHost resolves a guest the host reported, counting and dropping
// one that is not on that host (I-447).
func (i *Ingest) projectOnHost(ctx context.Context, hostID uuid.UUID, guestID string) (*store.Project, error) {
	gid, err := uuid.Parse(guestID)
	if err != nil {
		return nil, err
	}
	p, err := store.GetProjectOnHost(ctx, i.pool, gid, hostID)
	if errors.Is(err, store.ErrOtherHost) {
		i.m.HostReportsRefused.WithLabelValues("foreign_guest").Inc()
		i.log.Warn("host reported a guest that is not on it", "event", "foreign_guest", "host_id", hostID.String(), "guest_id", guestID)
	}
	return p, err
}

// overGuestCap reports whether the project already stored
// GuestEventsPerHour guest-raised events in the last hour.
func (i *Ingest) overGuestCap(ctx context.Context, projectID uuid.UUID) (bool, error) {
	var n int
	err := i.pool.QueryRow(ctx, `select count(*) from (select 1 from events where project_id = $1 and ts > $2 and source = 'host'
		and kind in ('completed','needs_input','error','agent_message','agent_question') limit $3) x`,
		projectID, i.now().Add(-time.Hour), GuestEventsPerHour).Scan(&n)
	if err != nil {
		return false, err
	}
	if n >= GuestEventsPerHour {
		i.m.HostReportsRefused.WithLabelValues("project_cap").Inc()
		return true, nil
	}
	return false, nil
}

// SnapshotPathOf reports whether a snapshot blob path is one hostd writes
// for the project: <user_id>/<project_id>/<name> or, for a guest created
// without a user id, <project_id>/<name>.
func SnapshotPathOf(p *store.Project, blobPath string) bool {
	parts := strings.Split(blobPath, "/")
	name := parts[len(parts)-1]
	if name == "" || name == "." || name == ".." || !strings.HasSuffix(name, ".img.zst") {
		return false
	}
	switch len(parts) {
	case 3:
		return parts[0] == p.UserID.String() && parts[1] == p.ID.String()
	case 2:
		return parts[0] == p.ID.String()
	}
	return false
}

func cleanLine(s string, n int) string {
	return store.CleanText(strings.NewReplacer("\n", " ", "\t", " ").Replace(s), n)
}

// ErrNotGuestKind is FromEdge's answer to a kind a guest may not report.
var ErrNotGuestKind = errors.New("unknown event kind")

// FromEdge handles a hook event that arrived over HTTP through the edge
// (I-4): the source ip maps to a project.
func (i *Ingest) FromEdge(ctx context.Context, sourceIP, agent, kind, summary string) (uuid.UUID, error) {
	ip, err := netip.ParseAddr(sourceIP)
	if err != nil {
		return uuid.Nil, errors.New("source_ip is not an address")
	}
	p, err := store.GetProjectByGuestIP(ctx, i.pool, ip)
	if err != nil {
		return uuid.Nil, err
	}
	if !GuestKinds[kind] {
		return uuid.Nil, ErrNotGuestKind
	}
	id, _, err := i.Insert(ctx, Incoming{ProjectID: p.ID, TS: i.now(), Kind: kind, Agent: GuestAgent(agent), Summary: summary, Source: "http"})
	return id, err
}

// Platform inserts an api-originated event (billing_stopped,
// base_updated, ...).
func (i *Ingest) Platform(ctx context.Context, projectID uuid.UUID, kind, summary string) error {
	_, _, err := i.Insert(ctx, Incoming{ProjectID: projectID, TS: i.now(), Kind: kind, Summary: summary, Source: "api"})
	return err
}
