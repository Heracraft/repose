// Package notify runs the notification outbox (05-control-plane-api.md
// §5.9, 13-notifications.md §5.5): undelivered rows are picked with
// `for update skip locked`, handed to the channel's sender, and marked
// delivered or retried on the documented schedule. Workstream 13 owns
// the senders' templates; the HTTP senders here carry the documented
// headers so delivery works from the first deploy.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/eventverbs"
)

// Message is one notification to deliver.
type Message struct {
	EventID uuid.UUID
	Kind    string
	Agent   string
	// Window is the agent's tmux window or herdr pane; At is when the
	// event happened. The question and done emails print both.
	Window    string
	At        time.Time
	Project   string // slug
	Summary   string
	Email     string
	NtfyURL   string
	Dashboard string
	// Unsubscribe is the one-click link the email template embeds; empty
	// when no Unsubscriber is configured (dev) or the channel is not email.
	Unsubscribe string
	// ProjectID links the dashboard's project page.
	ProjectID uuid.UUID
	// Question is set on an agent_question (DECISIONS I-245).
	Question *QuestionLinks
}

// QuestionLinks is what a question's notification carries beyond its text:
// one signed reply link per fixed option, for this message's channel.
type QuestionLinks struct {
	ID      uuid.UUID
	Options []string
	Replies []string // parallel to Options; empty when no signer is configured
	Expires time.Time
}

// ProjectURL is the dashboard page of the message's project.
func (m Message) ProjectURL() string {
	if m.ProjectID == uuid.Nil {
		return m.Dashboard + "/projects"
	}
	return m.Dashboard + "/projects/" + m.ProjectID.String()
}

// Sender delivers on one channel.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SenderFunc adapts a function.
type SenderFunc func(ctx context.Context, m Message) error

// Send implements Sender.
func (f SenderFunc) Send(ctx context.Context, m Message) error { return f(ctx, m) }

// Permanent wraps an error that must not be retried (a 4xx from ntfy).
type Permanent struct{ Err error }

func (p Permanent) Error() string { return p.Err.Error() }
func (p Permanent) Unwrap() error { return p.Err }

// backoff is the retry schedule; after the last attempt the row is
// marked failed (13-notifications.md §5.5, bounded by the 24 h in
// 05-control-plane-api.md §5.9).
var backoff = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 8 * time.Hour, 12 * time.Hour}

// Outbox is the worker.
type Outbox struct {
	pool      *db.Pool
	senders   map[string]Sender
	m         *metrics.M
	log       *slog.Logger
	Now       func() time.Time
	Dashboard string
	Interval  time.Duration
	// Unsub signs the email unsubscribe link; nil means no link is sent
	// (dev, or before the platform secret exists).
	Unsub *Unsubscriber
	// APIBase is the api's own public origin, which serves
	// GET /notify/unsubscribe; distinct from Dashboard.
	APIBase string
	// Workers is how many sends run at once (DECISIONS I-691). Rows of
	// one user's channel go in order on one lane, so a hanging endpoint
	// holds its own rows only.
	Workers int

	mu       sync.Mutex
	lanes    map[laneKey]*lane
	inflight map[rowKey]bool
	sem      chan struct{}
	wg       sync.WaitGroup
}

// DefaultWorkers is Workers' default.
const DefaultWorkers = 8

// laneKey is one user's channel: its rows are sent in order.
type laneKey struct {
	user    uuid.UUID
	channel string
}

// rowKey is one outbox row.
type rowKey struct {
	event   uuid.UUID
	channel string
}

// lane is the rows of one laneKey waiting for their turn.
type lane struct{ rows []row }

// New builds an outbox with the given channel senders.
func New(pool *db.Pool, senders map[string]Sender, m *metrics.M, log *slog.Logger) *Outbox {
	return &Outbox{pool: pool, senders: senders, m: m, log: log.With("component", "api"), Now: time.Now, Dashboard: "https://repose.herakraft.co", APIBase: "https://api.repose.herakraft.co", Interval: 2 * time.Second,
		Workers: DefaultWorkers, lanes: map[laneKey]*lane{}, inflight: map[rowKey]bool{}}
}

// Run polls until ctx ends, under the outbox advisory lock.
func (o *Outbox) Run(ctx context.Context) {
	for {
		release, ok, err := db.TryLock(ctx, o.pool, db.LockOutbox)
		if err != nil {
			o.log.Error("outbox lock", "event", "notify_fail", "err", err.Error())
		}
		if ok {
			t := time.NewTicker(o.Interval)
			for {
				// A pass does not wait for its sends: a slow lane keeps
				// sending while the next pass picks up everyone else's rows.
				if _, err := o.pass(ctx); err != nil && ctx.Err() == nil {
					o.log.Error("outbox pass", "event", "notify_fail", "err", err.Error())
				}
				select {
				case <-ctx.Done():
					t.Stop()
					o.wg.Wait()
					release()
					return
				case <-t.C:
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

type row struct {
	eventID     uuid.UUID
	channel     string
	attempts    int
	kind        string
	agent       *string
	window      *string
	summary     string
	slug        string
	email       *string
	ntfy        *string
	notifyEmail bool
	userID      uuid.UUID
	eventTS     time.Time
	projectID   uuid.UUID
	questionID  *uuid.UUID
	qState      *string
	options     []string
	expires     *time.Time
	delivered   *string // this channel's delivered value, if any
}

// Once delivers every due row, waits for the sends, and returns how many
// rows it picked.
func (o *Outbox) Once(ctx context.Context) (int, error) {
	n, err := o.pass(ctx)
	o.wg.Wait()
	o.gauges(ctx)
	return n, err
}

// pass picks the due rows that are not already on a lane and hands them
// to their lanes.
func (o *Outbox) pass(ctx context.Context) (int, error) {
	now := o.Now()
	o.mu.Lock()
	if o.lanes == nil {
		o.lanes, o.inflight = map[laneKey]*lane{}, map[rowKey]bool{}
	}
	busy := make([]string, 0, len(o.inflight))
	for k := range o.inflight {
		busy = append(busy, k.event.String()+"/"+k.channel)
	}
	o.mu.Unlock()
	var rows []row
	err := db.InTx(ctx, o.pool, func(tx db.Tx) error {
		// An event names its project, or (0007, I-269) only its user.
		rs, err := tx.Query(ctx, `select o.event_id, o.channel, o.attempts, e.kind, e.agent, e.tmux_window, e.summary, e.ts, coalesce(p.slug, ''), u.id, u.email, u.ntfy_url, u.notify_email, coalesce(p.id, '00000000-0000-0000-0000-000000000000'::uuid), q.id, q.state, q.options, q.expires_at, e.delivered ->> o.channel
			from events_outbox o join events e on e.id = o.event_id left join projects p on p.id = e.project_id join users u on u.id = coalesce(p.user_id, e.user_id)
			left join questions q on q.event_id = e.id
			where o.next_at <= $1 and not (o.event_id::text || '/' || o.channel = any($2::text[]))
			order by o.next_at limit 100 for update of o skip locked`, now, busy)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var r row
			if err := rs.Scan(&r.eventID, &r.channel, &r.attempts, &r.kind, &r.agent, &r.window, &r.summary, &r.eventTS, &r.slug, &r.userID, &r.email, &r.ntfy, &r.notifyEmail, &r.projectID, &r.questionID, &r.qState, &r.options, &r.expires, &r.delivered); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		if err := rs.Err(); err != nil {
			return err
		}
		// Hold the rows out of other workers' reach for the delivery window.
		for _, r := range rows {
			if _, err := tx.Exec(ctx, "update events_outbox set next_at = $3 where event_id = $1 and channel = $2", r.eventID, r.channel, now.Add(time.Minute)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	o.dispatch(ctx, rows)
	o.gauges(ctx)
	return len(rows), nil
}

// dispatch appends each row to its lane, starting a lane that is idle.
// At most Workers sends run at once across lanes.
func (o *Outbox) dispatch(ctx context.Context, rows []row) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.sem == nil {
		w := o.Workers
		if w <= 0 {
			w = DefaultWorkers
		}
		o.sem = make(chan struct{}, w)
	}
	for _, r := range rows {
		o.inflight[rowKey{r.eventID, r.channel}] = true
		k := laneKey{r.userID, r.channel}
		if l, ok := o.lanes[k]; ok {
			l.rows = append(l.rows, r)
			continue
		}
		o.lanes[k] = &lane{rows: []row{r}}
		o.wg.Add(1)
		go o.runLane(ctx, k)
	}
}

// runLane sends a lane's rows one at a time, in order, until it is empty.
func (o *Outbox) runLane(ctx context.Context, k laneKey) {
	defer o.wg.Done()
	for {
		o.mu.Lock()
		l := o.lanes[k]
		if len(l.rows) == 0 {
			delete(o.lanes, k)
			o.mu.Unlock()
			return
		}
		r := l.rows[0]
		l.rows = l.rows[1:]
		o.mu.Unlock()
		o.sem <- struct{}{}
		o.deliver(ctx, r)
		<-o.sem
		o.mu.Lock()
		delete(o.inflight, rowKey{r.eventID, r.channel})
		o.mu.Unlock()
	}
}

func (o *Outbox) deliver(ctx context.Context, r row) {
	// A channel disabled since the row was queued is dropped. Account
	// mail is transactional and ignores notify_email (I-269, I-291).
	if (r.channel == "email" && ((!r.notifyEmail && !transactional(r.kind)) || r.email == nil || *r.email == "")) || (r.channel == "ntfy" && (r.ntfy == nil || *r.ntfy == "")) {
		_, _ = o.pool.Exec(ctx, "delete from events_outbox where event_id = $1 and channel = $2", r.eventID, r.channel) // best effort; it is re-picked and dropped again otherwise
		return
	}
	if heldKinds[r.kind] && r.projectID != uuid.Nil {
		if over, err := o.resolved(ctx, r); err != nil {
			o.log.Error("outbox resolved check", "event", "notify_fail", "channel", r.channel, "kind", r.kind, "err", err.Error())
			return // the row's next_at already moved a minute on; it is tried again then
		} else if over {
			o.drop(ctx, r)
			return
		}
		now := o.Now()
		if until := r.eventTS.Add(MaxHold); now.Before(until) {
			present, err := o.present(ctx, r.projectID, now)
			if err != nil {
				o.log.Error("outbox presence check", "event", "notify_fail", "channel", r.channel, "kind", r.kind, "err", err.Error())
				return
			}
			if present {
				next := now.Add(HoldStep)
				if next.After(until) {
					next = until
				}
				o.hold(ctx, r, next)
				return
			}
		}
	}
	s := o.senders[r.channel]
	if s == nil {
		o.mark(ctx, r, ErrNotConfigured, true)
		return
	}
	m := Message{EventID: r.eventID, Kind: r.kind, Project: r.slug, Summary: r.summary, Dashboard: o.Dashboard, ProjectID: r.projectID, At: r.eventTS}
	if r.window != nil {
		m.Window = *r.window
	}
	if r.questionID != nil && r.expires != nil {
		q := &QuestionLinks{ID: *r.questionID, Options: r.options, Expires: *r.expires}
		if o.Unsub != nil {
			for i := range r.options {
				q.Replies = append(q.Replies, o.Unsub.ReplyURL(o.APIBase, q.ID, i, q.Expires, r.channel))
			}
		}
		m.Question = q
	}
	if r.agent != nil {
		m.Agent = *r.agent
	}
	if r.email != nil {
		m.Email = *r.email
	}
	if r.ntfy != nil {
		m.NtfyURL = *r.ntfy
	}
	if r.channel == "email" && o.Unsub != nil && !transactional(r.kind) {
		m.Unsubscribe = o.Unsub.URL(o.APIBase, r.userID, o.Now())
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := s.Send(sctx, m)
	cancel()
	var perm Permanent
	o.mark(ctx, r, err, errors.As(err, &perm))
}

// heldKinds are what an agent reports to its owner: held while the owner
// is at the machine (DECISIONS I-689). Platform and account kinds are
// never held.
var heldKinds = map[string]bool{"agent_question": true, "agent_done": true, "agent_message": true, "needs_input": true, "error": true}

// The hold (DECISIONS I-689): a row of a held kind waits HoldStep at a
// time while the owner is present, for at most MaxHold from its event;
// presence is a tmux client or an SSH session in the project's latest
// sample, when that sample is at most PresenceFresh old.
const (
	HoldStep      = time.Minute
	MaxHold       = 10 * time.Minute
	PresenceFresh = 2 * time.Minute
)

// present says the project's owner is at the machine now.
func (o *Outbox) present(ctx context.Context, projectID uuid.UUID, now time.Time) (bool, error) {
	var ts time.Time
	var n int
	err := o.pool.QueryRow(ctx, "select ts, tmux_clients + ssh_sessions from meter_samples where project_id = $1 order by ts desc limit 1", projectID).Scan(&ts, &n)
	if db.IsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return n > 0 && now.Sub(ts) <= PresenceFresh, nil
}

// resolved says what a row reports is over: its question is no longer
// pending (answered anywhere, withdrawn, expired), or a needs_input was
// followed by a later event from the same agent window.
func (o *Outbox) resolved(ctx context.Context, r row) (bool, error) {
	switch r.kind {
	case "agent_question":
		return r.qState != nil && *r.qState != "pending", nil
	case "needs_input":
		var later bool
		err := o.pool.QueryRow(ctx, `select exists (select 1 from events l join events e on e.id = $1
			where l.project_id = e.project_id and coalesce(l.agent, '') = coalesce(e.agent, '') and coalesce(l.tmux_window, '') = coalesce(e.tmux_window, '')
			and l.ts > e.ts and l.id <> e.id)`, r.eventID).Scan(&later)
		return later, err
	}
	return false, nil
}

// hold moves a row's next_at on without counting an attempt and records
// "held" on the event.
func (o *Outbox) hold(ctx context.Context, r row, next time.Time) {
	_ = db.InTx(ctx, o.pool, func(tx db.Tx) error { // best effort: the row's next_at already moved a minute on
		if _, err := tx.Exec(ctx, "update events_outbox set next_at = $3 where event_id = $1 and channel = $2", r.eventID, r.channel, next); err != nil {
			return err
		}
		if r.delivered != nil && *r.delivered == DeliveredHeld {
			return nil
		}
		_, err := tx.Exec(ctx, "update events set delivered = delivered || jsonb_build_object($2::text, $3::text) where id = $1", r.eventID, r.channel, DeliveredHeld)
		return err
	})
	if r.delivered == nil || *r.delivered != DeliveredHeld {
		o.m.NotifyTotal.WithLabelValues(r.channel, "held").Inc()
		o.log.Info("notification held", "event", "notify_hold", "channel", r.channel, "kind", r.kind)
	}
}

// drop deletes a row whose news is over and records "dropped: resolved".
func (o *Outbox) drop(ctx context.Context, r row) {
	o.m.NotifyTotal.WithLabelValues(r.channel, "dropped").Inc()
	o.log.Info("notification dropped", "event", "notify_drop", "channel", r.channel, "kind", r.kind)
	_ = db.InTx(ctx, o.pool, func(tx db.Tx) error { // best effort: re-picked and dropped again otherwise
		if _, err := tx.Exec(ctx, "update events set delivered = delivered || jsonb_build_object($2::text, $3::text) where id = $1", r.eventID, r.channel, DeliveredDropped); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "delete from events_outbox where event_id = $1 and channel = $2", r.eventID, r.channel)
		return err
	})
}

// The delivered values of the hold (DECISIONS I-689), beside a send time,
// "error: <why>" and "failed: <why>".
const (
	DeliveredHeld    = "held"
	DeliveredDropped = "dropped: resolved"
)

func (o *Outbox) mark(ctx context.Context, r row, err error, permanent bool) {
	now := o.Now()
	if err == nil {
		o.m.NotifyTotal.WithLabelValues(r.channel, "ok").Inc()
		if !r.eventTS.IsZero() {
			o.m.NotifyDeliveryLatencySeconds.Observe(now.Sub(r.eventTS).Seconds())
		}
		o.log.Info("notification sent", "event", "notify_send", "channel", r.channel, "kind", r.kind)
		_ = db.InTx(ctx, o.pool, func(tx db.Tx) error { // a failed mark re-sends once; the delivered key is idempotent
			if _, err := tx.Exec(ctx, "update events set delivered = delivered || jsonb_build_object($2::text, $3::text) where id = $1", r.eventID, r.channel, now.UTC().Format(time.RFC3339)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "delete from events_outbox where event_id = $1 and channel = $2", r.eventID, r.channel)
			return err
		})
		return
	}
	attempts := r.attempts + 1
	// Never err.Error(): a network error carries the URL it dialled (an
	// ntfy topic), and delivered is returned by the events route
	// (DECISIONS I-691).
	reason := sendFailure(err)
	if permanent || attempts > len(backoff) {
		o.m.NotifyTotal.WithLabelValues(r.channel, "failed").Inc()
		o.log.Warn("notification failed", "event", "notify_fail", "channel", r.channel, "kind", r.kind, "attempts", attempts)
		_ = db.InTx(ctx, o.pool, func(tx db.Tx) error { // see above
			if _, err := tx.Exec(ctx, "update events set delivered = delivered || jsonb_build_object($2::text, $3::text) where id = $1", r.eventID, r.channel, "failed: "+reason); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "delete from events_outbox where event_id = $1 and channel = $2", r.eventID, r.channel)
			return err
		})
		return
	}
	o.m.NotifyTotal.WithLabelValues(r.channel, "retry").Inc()
	next := now.Add(backoff[attempts-1])
	_, _ = o.pool.Exec(ctx, "update events_outbox set attempts = $3, next_at = $4, last_error = $5 where event_id = $1 and channel = $2", r.eventID, r.channel, attempts, next, reason) // best effort; the held row is re-picked in a minute
	_, _ = o.pool.Exec(ctx, "update events set delivered = delivered || jsonb_build_object($2::text, $3::text) where id = $1", r.eventID, r.channel, "error: "+reason)
}

func (o *Outbox) gauges(ctx context.Context) {
	// The lag is how long the oldest due row has waited for a send: a
	// held row, a retry waiting out its backoff and a row being sent
	// (next_at moved a minute on) are not due (DECISIONS I-691).
	var depth int
	var oldest *time.Time
	if err := o.pool.QueryRow(ctx, "select count(*), min(next_at) filter (where next_at <= $1) from events_outbox", o.Now()).Scan(&depth, &oldest); err == nil {
		o.m.OutboxDepth.Set(float64(depth))
		if oldest != nil {
			o.m.OutboxLagSeconds.Set(o.Now().Sub(*oldest).Seconds())
		} else {
			o.m.OutboxLagSeconds.Set(0)
		}
	}
}

// transactional kinds are account mail: sent whatever notify_email says
// and without an unsubscribe link, since each answers something the user
// did or is about to be charged for (DECISIONS I-269, I-291). The list is
// events.AccountKinds, so a producer and the outbox cannot disagree.
func transactional(kind string) bool { return events.AccountKinds[kind] }

// Title renders the one-line title of a message: what the ntfy Title
// header and ordinary email subjects use.
func Title(m Message) string {
	if m.Kind == TestKind {
		return "repose: test"
	}
	// A user notice names no project: its subject is its title (I-645).
	if events.UserNoticeKinds[m.Kind] {
		return Subject(m)
	}
	// The CLI's `repose events` says the same words (I-634).
	verb := eventverbs.Verb(m.Kind)
	if m.Agent != "" {
		return fmt.Sprintf("%s: %s %s", m.Project, m.Agent, verb)
	}
	return fmt.Sprintf("%s: %s", m.Project, verb)
}

// platformSubjects are the dedicated subject lines DESIGN.md §13 and
// 13-notifications.md §5.6 give platform-originated events: the user, not
// an agent, is what changed state, so "<project>: <verb>" reads wrong. The
// account kinds (I-291) name no project at all.
var platformSubjects = map[string]string{
	"billing_stopped":        "Your machines were stopped: a payment failed",
	"abuse_stopped":          "Your machine was stopped: a cryptocurrency miner was running",
	"welcome":                "Welcome to repose",
	"waitlist_joined":        "You're on the waitlist",
	"waitlist_invited":       "A seat is yours for 72 hours",
	"waitlist_expired":       "Your seat hold ran out",
	"trial_ending":           "Your free week ends soon",
	"payment_failed":         "Your payment failed",
	"subscription_cancelled": "Your plan is ending",
	"subscription_ended":     "Your plan has ended",
	"plan_changed":           "Your plan changed",
	"egress_stopped":         "Your machines were stopped: egress limit",
	"disk_over_plan":         "Your projects hold more than your plan's disk",
	"machines_restarted":     "The server running your machines restarted",
}

// Subject is the email subject line: Title for agent events, the
// documented wording for platform events.
func Subject(m Message) string {
	if s, ok := platformSubjects[m.Kind]; ok {
		return s
	}
	return Title(m)
}

// Email sends through Resend's HTTP API.
type Email struct {
	APIKey string
	From   string
	HTTP   *http.Client
	URL    string
}

// Send implements Sender.
func (e *Email) Send(ctx context.Context, m Message) error {
	if e.APIKey == "" {
		return fmt.Errorf("email: RESEND_API_KEY not set: %w", ErrNotConfigured)
	}
	if m.Email == "" {
		return Permanent{errors.New("email: user has no address")}
	}
	from := e.From
	if from == "" {
		from = "repose <notify@repose.herakraft.co>"
	}
	url := e.URL
	if url == "" {
		url = "https://api.resend.com/emails"
	}
	// One HTML and one text rendering of the same content, in one call
	// (DECISIONS I-291); a client that shows neither still has the subject.
	r, err := Render(m)
	if err != nil {
		return Permanent{fmt.Errorf("email: render %s: %w: %w", m.Kind, ErrRender, err)}
	}
	body := map[string]any{"from": from, "to": []string{m.Email}, "subject": "[repose] " + r.Subject, "html": r.HTML, "text": r.Text}
	if m.Unsubscribe != "" {
		// RFC 8058 one-click: the mail client POSTs to the link itself.
		body["headers"] = map[string]string{"List-Unsubscribe": "<" + m.Unsubscribe + ">", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// A send that timed out after Resend took it, or a failed mark after a
	// sent one, is retried; the key makes Resend take it once (I-691).
	if m.EventID != uuid.Nil {
		req.Header.Set("Idempotency-Key", m.EventID.String()+"/email")
	}
	client := e.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close() // status is all we need
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return &statusError{"email", resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		return Permanent{&statusError{"email", resp.StatusCode}}
	}
	return nil
}

// Ntfy posts to the user's ntfy URL.
type Ntfy struct {
	HTTP *http.Client
}

// Send implements Sender.
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	if m.NtfyURL == "" {
		return Permanent{errors.New("ntfy: no url")}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.NtfyURL, strings.NewReader(m.Summary))
	if err != nil {
		return Permanent{err}
	}
	req.Header.Set("Title", Title(m))
	// Priority says the urgency; no tag, since ntfy shows tags as emoji
	// (DECISIONS I-691).
	req.Header.Set("Priority", ntfyPriority(m.Kind))
	req.Header.Set("Click", m.ProjectURL())
	if m.Question != nil {
		if a := ntfyActions(m); a != "" {
			req.Header.Set("Actions", a)
		}
	}
	// The URL is the user's: the default client reaches public addresses
	// only and follows no redirect (I-444).
	client := n.HTTP
	if client == nil {
		client = ntfyClient()
	}
	resp, err := client.Do(req)
	for _, refused := range []error{ErrNonPublicAddress, ErrRedirect} {
		if errors.Is(err, refused) {
			return Permanent{fmt.Errorf("ntfy: %w", refused)}
		}
	}
	if err != nil {
		return err
	}
	_ = resp.Body.Close() // status is all we need
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return &statusError{"ntfy", resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		return Permanent{&statusError{"ntfy", resp.StatusCode}}
	}
	return nil
}

// ntfyPriority is 5 for what waits on the owner, 4 for a done report or
// a failure, 3 for the rest (a message, a platform notice).
func ntfyPriority(kind string) string {
	switch kind {
	case "needs_input", "agent_question":
		return "5"
	case "agent_done", "error", "snapshot_failed", "base_update_failed", "billing_stopped", "destroy_failed", "abuse_stopped", "personal_failed", "boot_failed", "machines_restarted":
		return "4"
	}
	return "3"
}

// ntfyAction is one entry of ntfy's JSON action list.
type ntfyAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
	Method string `json:"method,omitempty"`
	Clear  bool   `json:"clear,omitempty"`
}

// ntfyActions renders a question's buttons as ntfy's JSON action list, which
// the Actions header accepts as well as the comma format and which needs no
// quoting rules for an option containing a comma or a semicolon: an http
// button per option POSTing its signed reply link, or one view button to
// the project page when the question takes free text. Non-ASCII is escaped
// so the header stays ASCII.
func ntfyActions(m Message) string {
	q := m.Question
	var acts []ntfyAction
	if len(q.Options) > 0 && len(q.Replies) == len(q.Options) {
		for i, o := range q.Options {
			acts = append(acts, ntfyAction{Action: "http", Label: o, URL: q.Replies[i], Method: "POST", Clear: true})
		}
	} else {
		acts = append(acts, ntfyAction{Action: "view", Label: "Answer", URL: m.ProjectURL()})
	}
	b, err := json.Marshal(acts)
	if err != nil {
		return ""
	}
	return asciiJSON(string(b))
}

// asciiJSON escapes every non-ASCII rune of a JSON text as \uXXXX.
func asciiJSON(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&b, "\\u%04x\\u%04x", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}

// Test sends a test message directly through every configured channel
// of the user and returns per-channel results (POST /me/notify-test).
func (o *Outbox) Test(ctx context.Context, u *store.User) map[string]string {
	out := map[string]string{}
	m := Message{Kind: TestKind, Summary: "This is a test from repose.", Dashboard: o.Dashboard}
	if u.Email != nil {
		m.Email = *u.Email
	}
	if u.NtfyURL != nil {
		m.NtfyURL = *u.NtfyURL
	}
	if u.NotifyEmail && m.Email != "" {
		out["email"], _ = o.try(ctx, "email", m)
	}
	if m.NtfyURL != "" {
		var why string
		if out["ntfy"], why = o.try(ctx, "ntfy", m); why != "" {
			// The URL is the user's, so why it failed is theirs to fix
			// (DECISIONS I-622): ntfy_error says it in a few words.
			out["ntfy_error"] = why
		}
	}
	return out
}

// try sends m on channel: "ok", or "error" with why in a few words.
func (o *Outbox) try(ctx context.Context, channel string, m Message) (string, string) {
	s := o.senders[channel]
	if s == nil {
		return "error", "not configured on this server"
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.Send(sctx, m); err != nil {
		o.log.Warn("notify test failed", "event", "notify_fail", "channel", channel)
		return "error", sendFailure(err)
	}
	return "ok", ""
}

// TestKind is the kind of POST /me/notify-test's message, titled
// "repose: test" (DECISIONS I-691). It is never an event.
const TestKind = "test"

// Errors a send names without the URL.
var (
	ErrNotConfigured = errors.New("not configured on this server")
	ErrRender        = errors.New("the message did not render")
)

// sendFailure is a failed send in a few words a user can act on: the
// status the server answered, or why there was none. Never the URL. It is
// what events.delivered stores and what the test route answers.
func sendFailure(err error) string {
	var status *statusError
	var dns *net.DNSError
	switch {
	case errors.Is(err, ErrNotConfigured):
		return "not configured on this server"
	case errors.Is(err, ErrRender):
		return "the message did not render"
	case errors.As(err, &status):
		return fmt.Sprintf("the server answered %d", status.code)
	case errors.Is(err, ErrNonPublicAddress):
		return "the address is not on the public internet"
	case errors.Is(err, ErrRedirect):
		return "the server redirects, and repose follows no redirect"
	case errors.Is(err, context.DeadlineExceeded):
		return "no answer within 15 seconds"
	case errors.As(err, &dns):
		return "the host name does not resolve"
	}
	return "could not connect"
}

// statusError is an HTTP answer a sender treats as a failure.
type statusError struct {
	channel string
	code    int
}

func (e *statusError) Error() string { return fmt.Sprintf("%s: status %d", e.channel, e.code) }
