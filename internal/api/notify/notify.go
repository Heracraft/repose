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
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// Message is one notification to deliver.
type Message struct {
	EventID   uuid.UUID
	Kind      string
	Agent     string
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
}

// New builds an outbox with the given channel senders.
func New(pool *db.Pool, senders map[string]Sender, m *metrics.M, log *slog.Logger) *Outbox {
	return &Outbox{pool: pool, senders: senders, m: m, log: log.With("component", "api"), Now: time.Now, Dashboard: "https://repose.herakraft.co", APIBase: "https://api.repose.herakraft.co", Interval: 2 * time.Second}
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
				if _, err := o.Once(ctx); err != nil && ctx.Err() == nil {
					o.log.Error("outbox pass", "event", "notify_fail", "err", err.Error())
				}
				select {
				case <-ctx.Done():
					t.Stop()
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
	summary     string
	slug        string
	email       *string
	ntfy        *string
	notifyEmail bool
	userID      uuid.UUID
	eventTS     time.Time
	projectID   uuid.UUID
	questionID  *uuid.UUID
	options     []string
	expires     *time.Time
}

// Once delivers every due row and returns how many it attempted.
func (o *Outbox) Once(ctx context.Context) (int, error) {
	now := o.Now()
	var rows []row
	err := db.InTx(ctx, o.pool, func(tx db.Tx) error {
		// An event names its project, or (0007, I-269) only its user.
		rs, err := tx.Query(ctx, `select o.event_id, o.channel, o.attempts, e.kind, e.agent, e.summary, e.ts, coalesce(p.slug, ''), u.id, u.email, u.ntfy_url, u.notify_email, coalesce(p.id, '00000000-0000-0000-0000-000000000000'::uuid), q.id, q.options, q.expires_at
			from events_outbox o join events e on e.id = o.event_id left join projects p on p.id = e.project_id join users u on u.id = coalesce(p.user_id, e.user_id)
			left join questions q on q.event_id = e.id
			where o.next_at <= $1 order by o.next_at limit 100 for update of o skip locked`, now)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var r row
			if err := rs.Scan(&r.eventID, &r.channel, &r.attempts, &r.kind, &r.agent, &r.summary, &r.eventTS, &r.slug, &r.userID, &r.email, &r.ntfy, &r.notifyEmail, &r.projectID, &r.questionID, &r.options, &r.expires); err != nil {
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
	for _, r := range rows {
		o.deliver(ctx, r)
	}
	o.gauges(ctx)
	return len(rows), nil
}

func (o *Outbox) deliver(ctx context.Context, r row) {
	// A channel disabled since the row was queued is dropped. Account
	// mail is transactional and ignores notify_email (I-269, I-291).
	if (r.channel == "email" && ((!r.notifyEmail && !transactional(r.kind)) || r.email == nil || *r.email == "")) || (r.channel == "ntfy" && (r.ntfy == nil || *r.ntfy == "")) {
		_, _ = o.pool.Exec(ctx, "delete from events_outbox where event_id = $1 and channel = $2", r.eventID, r.channel) // best effort; it is re-picked and dropped again otherwise
		return
	}
	s := o.senders[r.channel]
	if s == nil {
		o.mark(ctx, r, errors.New("no sender configured"), true)
		return
	}
	m := Message{EventID: r.eventID, Kind: r.kind, Project: r.slug, Summary: r.summary, Dashboard: o.Dashboard, ProjectID: r.projectID}
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
		m.Unsubscribe = o.Unsub.URL(o.APIBase, r.userID, time.Now())
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := s.Send(sctx, m)
	cancel()
	var perm Permanent
	o.mark(ctx, r, err, errors.As(err, &perm))
}

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
	reason := err.Error()
	if len(reason) > 200 {
		reason = reason[:200]
	}
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
	var depth int
	var oldest *time.Time
	if err := o.pool.QueryRow(ctx, "select count(*), min(created_at) from events_outbox").Scan(&depth, &oldest); err == nil {
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
	verb := map[string]string{"completed": "finished", "needs_input": "needs input", "error": "hit an error", "agent_message": "says", "agent_question": "asks", "idle_running": "idle, still billing", "temp_expiring": "destroyed in an hour", "temp_destroyed": "temporary machine destroyed", "personal_failed": "machine.nix did not apply", "boot_failed": "new system did not boot"}[m.Kind]
	if verb == "" {
		verb = strings.ReplaceAll(m.Kind, "_", " ")
	}
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
		return errors.New("email: RESEND_API_KEY not set")
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
		return Permanent{fmt.Errorf("email: render %s: %w", m.Kind, err)}
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
		return fmt.Errorf("email: status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return Permanent{fmt.Errorf("email: status %d", resp.StatusCode)}
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
	prio, tag := "3", "white_check_mark"
	switch m.Kind {
	case "needs_input":
		prio, tag = "5", "question"
	case "error", "snapshot_failed", "base_update_failed", "billing_stopped", "destroy_failed", "abuse_stopped", "personal_failed", "boot_failed":
		prio, tag = "4", "x"
	case "agent_message":
		prio, tag = "3", "speech_balloon"
	case "agent_question":
		prio, tag = "5", "question"
	}
	req.Header.Set("Priority", prio)
	req.Header.Set("Tags", tag)
	req.Header.Set("Click", m.Dashboard+"/projects")
	if m.Question != nil {
		req.Header.Set("Click", m.ProjectURL())
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
		return fmt.Errorf("ntfy: status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return Permanent{fmt.Errorf("ntfy: status %d", resp.StatusCode)}
	}
	return nil
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
	m := Message{Kind: "completed", Project: "repose", Summary: "This is a test from repose", Dashboard: o.Dashboard}
	if u.Email != nil {
		m.Email = *u.Email
	}
	if u.NtfyURL != nil {
		m.NtfyURL = *u.NtfyURL
	}
	if u.NotifyEmail && m.Email != "" {
		out["email"] = o.try(ctx, "email", m)
	}
	if m.NtfyURL != "" {
		out["ntfy"] = o.try(ctx, "ntfy", m)
	}
	return out
}

func (o *Outbox) try(ctx context.Context, channel string, m Message) string {
	s := o.senders[channel]
	if s == nil {
		return "error"
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.Send(sctx, m); err != nil {
		o.log.Warn("notify test failed", "event", "notify_fail", "channel", channel)
		return "error"
	}
	return "ok"
}
