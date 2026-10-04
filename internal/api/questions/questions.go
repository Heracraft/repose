// Package questions is the api side of repose-ask (DECISIONS I-244,
// I-245): a guest's question becomes a questions row and an
// agent_question event (whose outbox rows notify the owner), the owner's
// answer comes in from the dashboard, the CLI or a signed reply link, and a
// worker in the grpc process carries every close the guest did not make
// itself back to the waiting guest as an AnswerQuestion command, retrying
// with a fresh command_id until the guest acknowledges it or no longer
// knows the question.
//
// Question and answer text are tenant content: they are stored like an
// event summary and shown to the owner, and never reach a log line.
package questions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// States.
const (
	StatePending   = "pending"
	StateAnswered  = "answered"
	StateCancelled = "cancelled"
	StateExpired   = "expired"
	StateNoChannel = "no_channel"
)

// Limits, matching the guest's (internal/guestd/questions).
const (
	TextCap    = 1 << 10
	AnswerCap  = 1 << 10
	MaxOptions = 3
	OptionCap  = 64
	MaxTimeout = 24 * time.Hour
	// DefaultTimeout covers an old guest that sent none.
	DefaultTimeout = 30 * time.Minute
	// RetryAfter is how long a delivery attempt waits for its result before
	// the worker tries again with a new command_id.
	RetryAfter = 30 * time.Second
	// GiveUpAfter is when the worker stops carrying a close to a guest that
	// never acknowledged it; the ask has timed out on its own long before.
	GiveUpAfter = MaxTimeout + time.Hour
	// ExpireGrace lets the guest's own timeout, which it reports, land
	// before the api declares the question expired.
	ExpireGrace = 30 * time.Second
)

// Errors the HTTP layer maps to codes.
var (
	ErrClosed    = errors.New("questions: the question is no longer waiting")
	ErrNotOption = errors.New("questions: the answer is not one of the options")
	ErrEmpty     = errors.New("questions: the answer is empty")
)

// Sender is hostmgr's Send.
type Sender interface {
	Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error
}

// Question is a questions row as the routes show it.
type Question struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"project_id"`
	Project     string     `json:"project"`
	Agent       string     `json:"agent"`
	Window      string     `json:"window,omitempty"`
	Text        string     `json:"text"`
	Options     []string   `json:"options"`
	State       string     `json:"state"`
	Answer      *string    `json:"answer"`
	AnsweredVia *string    `json:"answered_via"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AnsweredAt  *time.Time `json:"answered_at"`
}

// Service is the question store and its delivery worker.
type Service struct {
	pool   *db.Pool
	events *events.Ingest
	send   Sender
	log    *slog.Logger
	// Now is the clock (tests).
	Now func() time.Time
	// Interval is the worker's pass interval.
	Interval time.Duration
}

// New builds the service.
func New(pool *db.Pool, ev *events.Ingest, send Sender, log *slog.Logger) *Service {
	return &Service{pool: pool, events: ev, send: send, log: log.With("component", "api"), Now: time.Now, Interval: 2 * time.Second}
}

// clean trims s and makes it storable (no NUL or other control character
// but newline and tab, valid UTF-8), at most n bytes.
func clean(s string, n int) string {
	return store.CleanText(strings.TrimSpace(s), n)
}

// OnQuestion records a guest's question (events.QuestionHandler). A repeat
// of a known id is ignored; a guest-side close (state cancelled|expired)
// closes the row without anything to deliver back. Both act only on the
// sending guest's own project's questions: an id another project holds is
// ignored (DECISIONS I-447).
func (s *Service) OnQuestion(ctx context.Context, ts time.Time, q *hostdv1.AgentQuestion) error {
	id, err := uuid.Parse(q.QuestionId)
	if err != nil {
		return nil // nothing to store and nothing a resend would fix
	}
	gid, err := uuid.Parse(q.GuestId)
	if err != nil {
		return nil
	}
	p, err := store.GetProjectByGuest(ctx, s.pool, gid)
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if q.State != "" {
		st := q.State
		if st != StateCancelled && st != StateExpired {
			return nil
		}
		tag, err := s.pool.Exec(ctx, "update questions set state = $2, delivered_at = now(), delivery = 'guest' where id = $1 and project_id = $3 and state = 'pending'", id, st, p.ID)
		if err == nil && tag.RowsAffected() > 0 {
			s.log.Info("question closed by the guest", "event", "agent_question", "question_id", id.String(), "state", st)
		}
		return err
	}
	var known bool
	var eventID, owner uuid.UUID
	err = s.pool.QueryRow(ctx, "select event_id, project_id from questions where id = $1", id).Scan(&eventID, &owner)
	switch {
	case err == nil:
		if owner != p.ID {
			s.log.Warn("question id held by another project", "event", "agent_question", "question_id", id.String(), "project_id", p.ID.String())
			return nil
		}
		known = true
	case !db.IsNoRows(err):
		return err
	}
	if !known {
		text := clean(q.Text, TextCap)
		if text == "" {
			return nil
		}
		opts := []string{}
		for _, o := range q.Options {
			if o = clean(o, OptionCap); o != "" && len(opts) < MaxOptions {
				opts = append(opts, o)
			}
		}
		timeout := time.Duration(q.TimeoutS) * time.Second
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		if timeout > MaxTimeout {
			timeout = MaxTimeout
		}
		now := s.Now()
		if ts.IsZero() || ts.Sub(now).Abs() > 5*time.Minute {
			ts = now
		}
		var channels int
		if err := s.pool.QueryRow(ctx, `select (case when u.notify_email and coalesce(u.email,'') <> '' then 1 else 0 end) + (case when coalesce(u.ntfy_url,'') <> '' then 1 else 0 end)
			from users u where u.id = $1`, p.UserID).Scan(&channels); err != nil {
			return err
		}
		state := StatePending
		var nextAt any
		if channels == 0 {
			// Nobody would hear it: the ask fails at once with its own
			// exit code instead of waiting out the timeout.
			state, nextAt = StateNoChannel, now
		}
		eventID = store.NewID()
		agent := store.CleanText(q.Agent, events.MaxAgent)
		if agent == "" {
			agent = "shell"
		}
		var window *string
		if w := store.CleanText(q.TmuxWindow, events.MaxWindow); w != "" {
			window = &w
		}
		if _, err := s.pool.Exec(ctx, `insert into questions (id, project_id, guest_id, event_id, agent, tmux_window, text, options, state, expires_at, deliver_next_at, created_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) on conflict (id) do nothing`,
			id, p.ID, gid, eventID, agent, window, text, opts, state, ts.Add(timeout), nextAt, ts); err != nil {
			return err
		}
		// Whatever row won a race, its event id is the one to use; a row
		// another project won is not this guest's to announce.
		if err := s.pool.QueryRow(ctx, "select event_id, project_id from questions where id = $1", id).Scan(&eventID, &owner); err != nil {
			return err
		}
		if owner != p.ID {
			return nil
		}
		s.log.Info("question opened", "event", "agent_question", "question_id", id.String(), "project_id", p.ID.String(), "agent", agent, "state", state, "options", len(opts), "text_bytes", len(text))
	}
	// The event is inserted last and by a fixed id, so a failure between
	// the two inserts is repaired by the host's resend.
	var row struct {
		agent, text string
		window      *string
		created     time.Time
	}
	if err := s.pool.QueryRow(ctx, "select agent, tmux_window, text, created_at from questions where id = $1 and project_id = $2", id, p.ID).Scan(&row.agent, &row.window, &row.text, &row.created); err != nil {
		return err
	}
	w := ""
	if row.window != nil {
		w = *row.window
	}
	_, _, err = s.events.Insert(ctx, events.Incoming{ID: eventID, ProjectID: p.ID, TS: row.created, Kind: "agent_question", Agent: row.agent, Window: w, Summary: row.text, Source: "host"})
	return err
}

// GuestStopped cancels the pending questions of a project whose guest
// stopped (events.QuestionHandler). Nothing is delivered: the asker is gone.
func (s *Service) GuestStopped(ctx context.Context, projectID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, "update questions set state = 'cancelled', delivered_at = now(), delivery = 'gone' where project_id = $1 and state = 'pending'", projectID)
	if err == nil && tag.RowsAffected() > 0 {
		s.log.Info("questions cancelled with their guest", "event", "agent_question", "project_id", projectID.String(), "count", tag.RowsAffected())
	}
	return err
}

const selectCols = `q.id, q.project_id, p.slug, q.agent, coalesce(q.tmux_window, ''), q.text, q.options, q.state, q.answer, q.answered_via, q.created_at, q.expires_at, q.answered_at`

func scan(r interface{ Scan(...any) error }) (Question, error) {
	var q Question
	err := r.Scan(&q.ID, &q.ProjectID, &q.Project, &q.Agent, &q.Window, &q.Text, &q.Options, &q.State, &q.Answer, &q.AnsweredVia, &q.CreatedAt, &q.ExpiresAt, &q.AnsweredAt)
	if q.Options == nil {
		q.Options = []string{}
	}
	return q, err
}

// List returns a user's questions, newest first: pending ones only, or the
// latest of every state. projectID narrows it to one project.
func (s *Service) List(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID, pendingOnly bool, limit int) ([]Question, error) {
	sql := `select ` + selectCols + ` from questions q join projects p on p.id = q.project_id
		where p.user_id = $1 and p.destroyed_at is null and ($2::uuid is null or q.project_id = $2)`
	if pendingOnly {
		sql += ` and q.state = 'pending' and q.expires_at > now()`
	}
	sql += ` order by (q.state = 'pending') desc, q.created_at desc limit $3`
	rows, err := s.pool.Query(ctx, sql, userID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Question{}
	for rows.Next() {
		q, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Get returns one question; userID, when not nil, must own its project.
func (s *Service) Get(ctx context.Context, userID *uuid.UUID, id uuid.UUID) (*Question, error) {
	q, err := scan(s.pool.QueryRow(ctx, `select `+selectCols+` from questions q join projects p on p.id = q.project_id
		where q.id = $1 and ($2::uuid is null or p.user_id = $2)`, id, userID))
	if db.IsNoRows(err) {
		return nil, db.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// normalise checks an answer against the question's options and returns it
// in the option's own spelling.
func normalise(q *Question, answer string) (string, error) {
	answer = clean(answer, AnswerCap)
	if answer == "" {
		return "", ErrEmpty
	}
	if len(q.Options) == 0 {
		return answer, nil
	}
	for _, o := range q.Options {
		if strings.EqualFold(o, answer) {
			return o, nil
		}
	}
	return "", ErrNotOption
}

// Answer records the owner's answer. userID nil means a signed reply link,
// which the caller has verified. The first answer wins: a second one, or an
// answer after the question closed, is ErrClosed with the question as it
// stands.
func (s *Service) Answer(ctx context.Context, userID *uuid.UUID, id uuid.UUID, answer, via string) (*Question, error) {
	q, err := s.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if q.State == StatePending && !q.ExpiresAt.After(s.Now()) {
		q.State = StateExpired // the worker writes it at its next pass
	}
	if q.State != StatePending {
		return q, ErrClosed
	}
	a, err := normalise(q, answer)
	if err != nil {
		return q, err
	}
	tag, err := s.pool.Exec(ctx, `update questions set state = 'answered', answer = $2, answered_via = $3, answered_at = now(), deliver_next_at = now()
		where id = $1 and state = 'pending' and expires_at > now()`, id, a, via)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		q, err = s.Get(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		return q, ErrClosed
	}
	s.log.Info("question answered", "event", "agent_question", "question_id", id.String(), "via", via)
	return s.Get(ctx, userID, id)
}

// AnswerOption answers with the option a reply link names.
func (s *Service) AnswerOption(ctx context.Context, id uuid.UUID, opt int, via string) (*Question, error) {
	q, err := s.Get(ctx, nil, id)
	if err != nil {
		return nil, err
	}
	if opt < 0 || opt >= len(q.Options) {
		return q, ErrNotOption
	}
	return s.Answer(ctx, nil, id, q.Options[opt], via)
}

// Cancel dismisses a pending question; the waiting ask exits cancelled.
func (s *Service) Cancel(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*Question, error) {
	q, err := s.Get(ctx, &userID, id)
	if err != nil {
		return nil, err
	}
	tag, err := s.pool.Exec(ctx, "update questions set state = 'cancelled', answered_via = 'dashboard', answered_at = now(), deliver_next_at = now() where id = $1 and state = 'pending'", id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return q, ErrClosed
	}
	s.log.Info("question dismissed", "event", "agent_question", "question_id", id.String())
	return s.Get(ctx, &userID, id)
}

// Run is the delivery worker; it runs in the process that holds the host
// streams, under an advisory lock so one replica runs it.
func (s *Service) Run(ctx context.Context) {
	for {
		release, ok, err := db.TryLock(ctx, s.pool, db.LockQuestions)
		if err != nil && ctx.Err() == nil {
			s.log.Error("questions lock", "event", "agent_question", "err", err.Error())
		}
		if ok {
			t := time.NewTicker(s.Interval)
			for {
				if err := s.Once(ctx); err != nil && ctx.Err() == nil {
					s.log.Error("questions pass", "event", "agent_question", "err", err.Error())
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

type pendingDelivery struct {
	id      uuid.UUID
	guestID uuid.UUID
	state   string
	answer  *string
	hostID  *uuid.UUID
	created time.Time
}

// Once expires overdue questions and sends every due delivery.
func (s *Service) Once(ctx context.Context) error {
	now := s.Now()
	if _, err := s.pool.Exec(ctx, "update questions set state = 'expired', delivered_at = now(), delivery = 'guest' where state = 'pending' and expires_at < $1", now.Add(-ExpireGrace)); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, "update questions set delivered_at = now(), delivery = 'given_up' where state <> 'pending' and delivered_at is null and created_at < $1", now.Add(-GiveUpAfter)); err != nil {
		return err
	}
	var due []pendingDelivery
	err := db.InTx(ctx, s.pool, func(tx db.Tx) error {
		rows, err := tx.Query(ctx, `select q.id, q.guest_id, q.state, q.answer, p.host_id, q.created_at
			from questions q join projects p on p.id = q.project_id and p.guest_id = q.guest_id
			where q.state <> 'pending' and q.delivered_at is null and q.deliver_next_at <= $1
			order by q.deliver_next_at limit 50 for update of q skip locked`, now)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d pendingDelivery
			if err := rows.Scan(&d.id, &d.guestID, &d.state, &d.answer, &d.hostID, &d.created); err != nil {
				rows.Close()
				return err
			}
			due = append(due, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// A close for a guest the project no longer has (destroyed,
		// restored elsewhere) has nobody to reach.
		if _, err := tx.Exec(ctx, `update questions q set delivered_at = now(), delivery = 'gone' from projects p
			where p.id = q.project_id and q.state <> 'pending' and q.delivered_at is null and (p.guest_id is distinct from q.guest_id)`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, d := range due {
		s.deliver(ctx, now, d)
	}
	return nil
}

func (s *Service) deliver(ctx context.Context, now time.Time, d pendingDelivery) {
	cmdID := store.NewID()
	if _, err := s.pool.Exec(ctx, "update questions set deliver_command_id = $2, deliver_attempts = deliver_attempts + 1, deliver_next_at = $3 where id = $1", d.id, cmdID, now.Add(RetryAfter)); err != nil {
		s.log.Error("question delivery record", "event", "agent_question", "question_id", d.id.String(), "err", err.Error())
		return
	}
	if d.hostID == nil {
		return // no host now; retried when one is
	}
	answer := ""
	if d.answer != nil && d.state == StateAnswered {
		answer = *d.answer
	}
	cmd := &hostdv1.Command{CommandId: cmdID.String(), Cmd: &hostdv1.Command_AnswerQuestion{AnswerQuestion: &hostdv1.AnswerQuestion{
		GuestId: d.guestID.String(), QuestionId: d.id.String(), Status: d.state, Answer: answer,
	}}}
	if err := s.send.Send(ctx, *d.hostID, cmd); err != nil {
		// Not connected: the next pass after RetryAfter tries again.
		s.log.Info("question delivery deferred", "event", "agent_question", "question_id", d.id.String(), "reason", "host_not_connected")
	}
}

// OnResult takes the result of an AnswerQuestion command and reports
// whether it was one. ok and not_found (the guest no longer knows the
// question) are final; anything else is retried by the worker, including
// invalid_argument, which is what a hostd older than I-244 answers to a
// command it does not know: the answer waits for the host switch.
func (s *Service) OnResult(ctx context.Context, hostID uuid.UUID, r *hostdv1.Result) bool {
	cmdID, err := uuid.Parse(r.CommandId)
	if err != nil {
		return false
	}
	var qid uuid.UUID
	err = s.pool.QueryRow(ctx, "select id from questions where deliver_command_id = $1", cmdID).Scan(&qid)
	if db.IsNoRows(err) {
		return false
	}
	if err != nil {
		s.log.Error("question result lookup", "event", "command_result", "command_id", r.CommandId, "err", err.Error())
		return false
	}
	code := r.GetError().GetCode()
	switch {
	case r.Ok:
		code = "ok"
		_, err = s.pool.Exec(ctx, "update questions set delivered_at = now(), delivery = 'ok' where id = $1 and delivered_at is null", qid)
	case code == "not_found":
		_, err = s.pool.Exec(ctx, "update questions set delivered_at = now(), delivery = 'gone' where id = $1 and delivered_at is null", qid)
	}
	if err != nil {
		s.log.Error("question result store", "event", "command_result", "command_id", r.CommandId, "err", err.Error())
	}
	s.log.Info("question delivery result", "event", "command_result", "command_id", r.CommandId, "question_id", qid.String(), "host_id", hostID.String(), "result", code)
	return true
}

// Describe is a one-line state for messages (the reply page, the CLI).
func Describe(q *Question) string {
	switch q.State {
	case StateAnswered:
		if q.Answer != nil {
			return fmt.Sprintf("already answered: %s", *q.Answer)
		}
		return "already answered"
	case StateExpired:
		return "this question has expired"
	case StateCancelled:
		return "this question was cancelled"
	case StateNoChannel:
		return "this question was closed"
	}
	if q.State == StatePending {
		return "waiting for an answer"
	}
	return q.State
}
