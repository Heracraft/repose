// Package say carries a message the owner typed to one of a project's
// agents (DECISIONS I-1144, I-1162). `repose say` and POST
// /projects/:id/say reach it; it stores the message as a row, a worker
// in the process that holds the host's stream sends it down the hostd
// channel as a SayToAgent command and writes the machine's answer back
// into the row, and the process the owner called waits on that row, so
// the owner is told where the message went rather than that it was
// queued somewhere.
//
// The row is the hand-off because the api runs as two processes
// (`API_MODE=http` serves the CLI, `API_MODE=grpc` holds every hostd
// stream; I-87, I-163) and only the second can send a command. An
// api running as one process (`API_MODE=all`, the tests) is the same
// code with both halves in it.
//
// The message is tenant content. It is stored the way an event summary
// is and never logged, not even its length.
package say

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/resting"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// TextCap is the longest message, four times an event summary: a
// message is something a person wrote, and a paragraph does not fit in
// 1 KB. hostd and guestd cap it again.
const TextCap = 4 << 10

// Timeout is how long the api waits for the machine's answer. Delivery
// is typing into a terminal, which is a round trip to the guest and back
// and a second of I-752's submit check; a machine that has not answered
// in this long is not going to.
const Timeout = 20 * time.Second

// StaleAfter is when the worker fails a row still in the hand-off: past
// every waiter's window, so a worker that died mid-delivery cannot have
// a message typed in long after the sender was told it did not arrive.
const StaleAfter = 2 * Timeout

// notifyChannel carries "a message is waiting" to the worker, which is
// in the other process. The worker's tick covers a lost NOTIFY.
const notifyChannel = "repose_says"

// States of a says row. pending and sending are the hand-off between
// the two processes and are never shown to anyone.
const (
	StatePending   = "pending"
	StateSending   = "sending"
	StateDelivered = "delivered"
	StateWaiting   = "waiting"
	StateReplaced  = "replaced"
	StateFailed    = "failed"
)

// Short reasons stored on a failed row, which the waiting process reads
// back as the error the sender gets.
const (
	reasonNoAnswer  = "the machine did not answer"
	reasonNoHost    = "the machine's host is not connected"
	reasonNoWake    = "the machine could not be woken"
	reasonNotSent   = "the message could not be sent"
	codeNoAgent     = "not_found"
	codeSeveral     = "invalid_argument"
	deliverBatch    = 20
	deliverInterval = time.Second
	pollInterval    = 100 * time.Millisecond
)

// Errors the route maps to its own codes.
var (
	// ErrNotRunning: the machine is not running, so no agent is.
	ErrNotRunning = errors.New("say: the machine is not running")
	// ErrNoHost: the machine has no host to send the message to.
	ErrNoHost = errors.New("say: the machine's host is not connected")
	// ErrTimeout: nothing answered within the window.
	ErrTimeout = errors.New("say: the machine did not answer")
	// ErrEmpty, ErrTooLong: the message itself.
	ErrEmpty   = errors.New("say: a message with nothing in it")
	ErrTooLong = fmt.Errorf("say: a message longer than %d bytes", TextCap)
)

// ErrAgent is the machine's refusal to pick a window: no agent is
// running, the named window has none, or several are and the sender has
// to name one. Detail is the machine's own sentence, which names the
// windows, and is shown to the owner.
type ErrAgent struct {
	Detail string
	// Several is set when the refusal is "pick one", not "there is none".
	Several bool
}

func (e *ErrAgent) Error() string { return "say: " + e.Detail }

// Sender is hostmgr's Send.
type Sender interface {
	Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error
}

// Waker is internal/api/resting's Wake (DECISIONS I-818): a machine
// resting at its agent's prompt is woken before the message is typed,
// the way an answer wakes one. nil delivers as before; hostd wakes the
// guest itself, so this is the api's half of the same promise. The wake
// is a command down the same stream, so it is the worker's, not the
// waiting process's.
type Waker interface {
	Wake(ctx context.Context, projectID uuid.UUID, reason string) (*hostdv1.WakeResult, error)
}

// Say is a says row as the route shows it.
type Say struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Window    string    `json:"window,omitempty"`
	Text      string    `json:"text"`
	Sender    string    `json:"sender,omitempty"`
	State     string    `json:"state"`
	// Replaced is set when this message took the place of one that was
	// still waiting in the same window.
	Replaced  bool      `json:"replaced,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Service stores messages and carries them to the machine.
type Service struct {
	pool *db.Pool
	send Sender
	log  *slog.Logger
	// Now is the clock (tests).
	Now func() time.Time
	// Interval is the delivery worker's pass interval; Poll is how often
	// a waiting request re-reads its row; Wait is how long it waits.
	Interval time.Duration
	Poll     time.Duration
	Wait     time.Duration
	// Waker wakes a resting machine first; set in app.go.
	Waker Waker

	// kicks wakes this process's worker without a round trip through
	// Postgres, which is what `API_MODE=all` and the tests run on.
	kicks chan struct{}

	mu      sync.Mutex
	waiting map[uuid.UUID]chan *hostdv1.Result
}

// New builds the service.
func New(pool *db.Pool, send Sender, log *slog.Logger) *Service {
	return &Service{
		pool: pool, send: send, log: log.With("component", "api"),
		Now: time.Now, Interval: deliverInterval, Poll: pollInterval, Wait: Timeout,
		kicks:   make(chan struct{}, 1),
		waiting: map[uuid.UUID]chan *hostdv1.Result{},
	}
}

// Request is one `repose say`.
type Request struct {
	ProjectID uuid.UUID
	UserID    uuid.UUID
	// Window is the agent window the sender named with --agent; empty
	// lets the machine pick its single agent window.
	Window string
	Text   string
	// Sender is the account's name, which the agent is told when the
	// sender is not the project's owner. Today always the owner.
	Sender    string
	FromOwner bool
}

// Send stores the message and waits for the machine's answer: delivered,
// or waiting for the end of the agent's turn. The delivery itself is the
// worker's, in whichever process holds the host's stream.
func (s *Service) Send(ctx context.Context, r Request) (*Say, error) {
	raw := strings.TrimSpace(r.Text)
	if len(raw) > TextCap {
		// Measured before cleaning: CleanText cuts on a rune boundary,
		// so a text one byte over could otherwise be trimmed to the cap
		// and taken without the sender being told.
		return nil, ErrTooLong
	}
	text := store.CleanText(raw, TextCap)
	if text == "" {
		return nil, ErrEmpty
	}
	window := store.CleanText(strings.TrimSpace(r.Window), 64)

	var guestID, hostID *uuid.UUID
	var state string
	err := s.pool.QueryRow(ctx,
		"select state, guest_id, host_id from projects where id = $1",
		r.ProjectID).Scan(&state, &guestID, &hostID)
	if err != nil {
		return nil, err
	}
	if state != "running" || guestID == nil {
		return nil, ErrNotRunning
	}
	if hostID == nil {
		return nil, ErrNoHost
	}

	id, cmdID := store.NewID(), store.NewID()
	if _, err := s.pool.Exec(ctx,
		`insert into says (id, project_id, user_id, tmux_window, text, sender, from_owner, state, command_id, created_at)
		 values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, r.ProjectID, r.UserID, window, text, r.Sender, r.FromOwner, StatePending, cmdID, s.Now().UTC()); err != nil {
		return nil, err
	}
	s.Kick(ctx)
	return s.wait(ctx, id)
}

// Kick tells the delivery worker a row is waiting: this process's, if it
// runs one, and the other process's through NOTIFY.
func (s *Service) Kick(ctx context.Context) {
	select {
	case s.kicks <- struct{}{}:
	default:
	}
	if _, err := s.pool.Exec(ctx, "select pg_notify($1, '')", notifyChannel); err != nil {
		s.log.Warn("say notify failed; the worker's tick covers it", "event", "agent_prompted", "err", err.Error())
	}
}

// row is a says row as the waiting request and the worker read it back.
type row struct {
	id        uuid.UUID
	projectID uuid.UUID
	window    string
	text      string
	sender    string
	state     string
	replaced  bool
	reason    string
	refusal   string
	createdAt time.Time
}

func (s *Service) read(ctx context.Context, id uuid.UUID) (*row, error) {
	var w row
	w.id = id
	err := s.pool.QueryRow(ctx,
		`select project_id, tmux_window, text, sender, state, replaced, coalesce(error, ''), coalesce(refusal, ''), created_at
		 from says where id = $1`, id).
		Scan(&w.projectID, &w.window, &w.text, &w.sender, &w.state, &w.replaced, &w.reason, &w.refusal, &w.createdAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// wait polls the row until the worker writes the machine's answer into
// it or the window runs out. Polling and not LISTEN: a say is a person
// typing, the window is 20 seconds, and a listening connection per
// request would hold a pool connection for all of it.
func (s *Service) wait(ctx context.Context, id uuid.UUID) (*Say, error) {
	deadline := s.Now().Add(s.Wait)
	t := time.NewTicker(s.Poll)
	defer t.Stop()
	for {
		w, err := s.read(ctx, id)
		if err != nil {
			return nil, err
		}
		if w.state != StatePending && w.state != StateSending {
			return answer(w)
		}
		if !s.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
	// Nobody delivered it in the window. A row still pending is failed
	// here so it is not typed into the agent's terminal long after the
	// sender was told it did not arrive; one the worker has already
	// claimed is left to it and the sender is told the same thing.
	if _, err := s.pool.Exec(ctx,
		"update says set state = $1, error = $2 where id = $3 and state = $4",
		StateFailed, reasonNoAnswer, id, StatePending); err != nil {
		return nil, err
	}
	w, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.state == StatePending || w.state == StateSending {
		return nil, ErrTimeout
	}
	return answer(w)
}

// answer turns a finished row into what the route returns.
func answer(w *row) (*Say, error) {
	if w.state == StateFailed {
		return nil, rowError(w.reason, w.refusal)
	}
	return &Say{
		ID: w.id, ProjectID: w.projectID, Window: w.window, Text: w.text,
		Sender: w.sender, State: w.state, Replaced: w.replaced, CreatedAt: w.createdAt,
	}, nil
}

// rowError reads a failed row's short reason back as the error the
// sender gets. It is the other half of fail and refuse.
func rowError(reason, refusal string) error {
	switch reason {
	case codeNoAgent, codeSeveral:
		// The machine's own sentence names the windows, which is the
		// whole value of the refusal.
		return &ErrAgent{Detail: refusal, Several: reason == codeSeveral}
	case reasonNoAnswer:
		return ErrTimeout
	case reasonNoHost, reasonNoWake, reasonNotSent, "":
		return ErrNoHost
	}
	return fmt.Errorf("say: the machine refused the message: %s", reason)
}

// Run is the delivery worker. It runs in the process that holds the host
// streams, under an advisory lock so one replica runs it.
func (s *Service) Run(ctx context.Context) {
	for {
		release, ok, err := db.TryLock(ctx, s.pool, db.LockSays)
		if err != nil && ctx.Err() == nil {
			s.log.Error("says lock", "event", "agent_prompted", "err", err.Error())
		}
		if ok {
			s.loop(ctx)
			release()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *Service) loop(ctx context.Context) {
	lctx, stopListen := context.WithCancel(ctx)
	defer stopListen()
	go s.listen(lctx)
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		if err := s.Once(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("says pass", "event", "agent_prompted", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kicks:
		}
	}
}

// listen turns a NOTIFY from the other process into a pass. A lost
// connection only costs latency: the ticker still delivers every row.
func (s *Service) listen(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := s.pool.Acquire(ctx)
		if err != nil {
			return
		}
		if _, err := conn.Exec(ctx, "listen "+notifyChannel); err == nil {
			for {
				if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				select {
				case s.kicks <- struct{}{}:
				default:
				}
			}
		}
		// A connection that failed mid-wait is not returned to the pool
		// still listening.
		_ = conn.Conn().Close(context.Background())
		conn.Release()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

// delivery is one claimed row and the machine it goes to.
type delivery struct {
	id        uuid.UUID
	projectID uuid.UUID
	window    string
	text      string
	sender    string
	fromOwner bool
	cmdID     uuid.UUID
	guestID   *uuid.UUID
	hostID    *uuid.UUID
	resting   bool
}

// Once fails the rows nobody finished in time and sends every message
// that is waiting to go. Exposed for tests.
func (s *Service) Once(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx,
		"update says set state = $1, error = $2 where state in ($3, $4) and created_at < $5",
		StateFailed, reasonNoAnswer, StatePending, StateSending, s.Now().Add(-StaleAfter)); err != nil {
		return err
	}
	// One statement claims the batch, so no row is held under a lock for
	// the 20 seconds a delivery can take: a row in `sending` is this
	// worker's and nothing else picks it up.
	rows, err := s.pool.Query(ctx, `update says s set state = $1
		from (select id from says where state = $2 order by created_at limit $3 for update skip locked) d,
		     projects p
		where s.id = d.id and p.id = s.project_id
		returning s.id, s.project_id, s.tmux_window, s.text, s.sender, s.from_owner, s.command_id,
		          p.guest_id, p.host_id, p.resting_since is not null`,
		StateSending, StatePending, deliverBatch)
	if err != nil {
		return err
	}
	var due []delivery
	for rows.Next() {
		var d delivery
		if err := rows.Scan(&d.id, &d.projectID, &d.window, &d.text, &d.sender, &d.fromOwner, &d.cmdID,
			&d.guestID, &d.hostID, &d.resting); err != nil {
			rows.Close()
			return err
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, d := range due {
		wg.Add(1)
		// One goroutine each: a machine that never answers holds its own
		// delivery for 20 seconds and nobody else's.
		go func() {
			defer wg.Done()
			s.deliver(ctx, d)
		}()
	}
	wg.Wait()
	return nil
}

// deliver carries one message to its machine and writes back what the
// machine answered.
func (s *Service) deliver(ctx context.Context, d delivery) {
	if d.guestID == nil || d.hostID == nil {
		// The machine stopped between the message being stored and this
		// pass.
		s.fail(ctx, d.id, reasonNoHost)
		return
	}
	if d.resting && s.Waker != nil {
		// A machine resting at its agent's prompt has to be awake to be
		// typed into; a wake that fails is a failed message, said so,
		// rather than one that silently goes nowhere.
		if _, err := s.Waker.Wake(ctx, d.projectID, resting.ReasonSay); err != nil {
			s.fail(ctx, d.id, reasonNoWake)
			return
		}
	}
	res, err := s.call(ctx, *d.hostID, d.cmdID, &hostdv1.SayToAgent{
		GuestId: d.guestID.String(), SayId: d.id.String(), Window: d.window,
		Text: d.text, Sender: d.sender, FromOwner: d.fromOwner,
	})
	if err != nil {
		s.fail(ctx, d.id, failure(err))
		return
	}
	if !res.Ok {
		s.refuse(ctx, d.id, res.GetError().GetCode(), res.GetError().GetMessage())
		return
	}

	sr := res.GetSay()
	state, window, replaced := StateDelivered, d.window, sr.GetReplaced()
	if sr.GetState() == StateWaiting {
		state = StateWaiting
	}
	if w := sr.GetWindow(); w != "" {
		window = w
	}
	// The machine replaced a message that was still waiting in that
	// window, so the row it replaced stops saying it is waiting.
	if replaced {
		if _, err := s.pool.Exec(ctx,
			"update says set state = $1 where project_id = $2 and tmux_window = $3 and state = $4 and id <> $5",
			StateReplaced, d.projectID, window, StateWaiting, d.id); err != nil {
			s.log.Warn("earlier message not marked replaced", "event", "agent_prompted", "project_id", d.projectID.String())
		}
	}
	if _, err := s.pool.Exec(ctx, "update says set state = $1, tmux_window = $2, replaced = $3 where id = $4",
		state, window, replaced, d.id); err != nil {
		s.log.Error("message state not recorded", "event", "agent_prompted", "say_id", d.id.String(), "err", err.Error())
		return
	}
	s.log.Info("message sent to an agent", "event", "agent_prompted",
		"project_id", d.projectID.String(), "state", state, "replaced", replaced)
}

// call sends one SayToAgent and waits for its result. command_id is the
// idempotency key hostd stores the result under, so a resend answers
// from the record instead of typing twice.
func (s *Service) call(ctx context.Context, hostID, cmdID uuid.UUID, m *hostdv1.SayToAgent) (*hostdv1.Result, error) {
	ch := make(chan *hostdv1.Result, 1)
	s.mu.Lock()
	s.waiting[cmdID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiting, cmdID)
		s.mu.Unlock()
	}()

	cmd := &hostdv1.Command{CommandId: cmdID.String(), Cmd: &hostdv1.Command_SayToAgent{SayToAgent: m}}
	if err := s.send.Send(ctx, hostID, cmd); err != nil {
		return nil, ErrNoHost
	}
	wctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	select {
	case res := <-ch:
		return res, nil
	case <-wctx.Done():
		return nil, ErrTimeout
	}
}

// OnResult takes the result of a SayToAgent this service sent and
// reports whether it was one. Wired in app.go beside the other result
// handlers, which ignore a command_id they do not know.
func (s *Service) OnResult(_ context.Context, _ uuid.UUID, r *hostdv1.Result) bool {
	cmdID, err := uuid.Parse(r.CommandId)
	if err != nil {
		return false
	}
	s.mu.Lock()
	ch, ok := s.waiting[cmdID]
	s.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- r:
	default:
	}
	return true
}

// Delivered closes the message the machine has just typed into window:
// one stored `waiting` stops waiting (DECISIONS I-1144). The events
// ingest calls it on an `agent_prompted` event, which is the machine
// saying the text reached the terminal. There is at most one waiting
// message per window, so (project, window) names it. A failure leaves a
// stale state and loses nothing else, so it is logged and not retried.
func (s *Service) Delivered(ctx context.Context, projectID uuid.UUID, window string) {
	if _, err := s.pool.Exec(ctx,
		"update says set state = $1 where project_id = $2 and tmux_window = $3 and state = $4",
		StateDelivered, projectID, window, StateWaiting); err != nil {
		s.log.Warn("message not marked delivered", "event", "agent_prompted", "project_id", projectID.String())
	}
}

// fail records why a message never reached the agent. reason is a few
// words, never a message from the machine, which could carry the text.
func (s *Service) fail(ctx context.Context, id uuid.UUID, reason string) {
	if _, err := s.pool.Exec(ctx, "update says set state = $1, error = $2 where id = $3", StateFailed, reason, id); err != nil {
		s.log.Warn("message failure not recorded", "event", "agent_prompted", "say_id", id.String())
	}
}

// refuse records the machine's own refusal. detail is the machine's
// sentence and is kept only for the two refusals that name the agent
// windows, which is what the sender reads to pick one; it is stored
// beside the message it belongs to and reaches no log line.
func (s *Service) refuse(ctx context.Context, id uuid.UUID, code, detail string) {
	if code != codeNoAgent && code != codeSeveral {
		detail = ""
	}
	if _, err := s.pool.Exec(ctx,
		"update says set state = $1, error = $2, refusal = nullif($3, '') where id = $4",
		StateFailed, code, detail, id); err != nil {
		s.log.Warn("message refusal not recorded", "event", "agent_prompted", "say_id", id.String())
	}
}

// failure is the short reason stored on a failed row.
func failure(err error) string {
	switch {
	case errors.Is(err, ErrNoHost):
		return reasonNoHost
	case errors.Is(err, ErrTimeout):
		return reasonNoAnswer
	default:
		return reasonNotSent
	}
}
