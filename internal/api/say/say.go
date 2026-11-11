// Package say carries a message the owner typed to one of a project's
// agents (DECISIONS I-1144). `repose say` and POST /projects/:id/say
// reach it; it stores the message as a row, sends it down the hostd
// channel as a SayToAgent command and waits for the machine's answer, so
// the owner is told where the message went rather than that it was
// queued somewhere.
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

// States of a says row.
const (
	StateDelivered = "delivered"
	StateWaiting   = "waiting"
	StateReplaced  = "replaced"
	StateFailed    = "failed"
)

// Errors the route maps to its own codes.
var (
	// ErrNotRunning: the machine is not running, so no agent is.
	ErrNotRunning = errors.New("say: the machine is not running")
	// ErrNoHost: the machine's host has no stream on this replica.
	ErrNoHost = errors.New("say: the machine's host is not connected")
	// ErrTimeout: the host took the command and never answered.
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
// guest itself, so this is the api's half of the same promise.
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
	// Waker wakes a resting machine first; set in app.go.
	Waker Waker

	mu      sync.Mutex
	waiting map[uuid.UUID]chan *hostdv1.Result
}

// New builds the service.
func New(pool *db.Pool, send Sender, log *slog.Logger) *Service {
	return &Service{
		pool: pool, send: send, log: log.With("component", "api"),
		Now: time.Now, waiting: map[uuid.UUID]chan *hostdv1.Result{},
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

// Send stores the message and carries it to the machine, and answers
// with what the machine did: delivered, or waiting for the end of the
// agent's turn.
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
	var isResting bool
	err := s.pool.QueryRow(ctx,
		"select state, guest_id, host_id, resting_since is not null from projects where id = $1",
		r.ProjectID).Scan(&state, &guestID, &hostID, &isResting)
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
	row := &Say{
		ID: id, ProjectID: r.ProjectID, Window: window, Text: text,
		Sender: r.Sender, State: StateWaiting, CreatedAt: s.Now().UTC(),
	}
	if _, err := s.pool.Exec(ctx,
		`insert into says (id, project_id, user_id, tmux_window, text, sender, from_owner, state, command_id, created_at)
		 values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, r.ProjectID, r.UserID, window, text, r.Sender, r.FromOwner, StateWaiting, cmdID, row.CreatedAt); err != nil {
		return nil, err
	}

	if isResting && s.Waker != nil {
		// A machine resting at its agent's prompt has to be awake to be
		// typed into; a wake that fails is a failed message, said so,
		// rather than one that silently goes nowhere.
		if _, err := s.Waker.Wake(ctx, r.ProjectID, resting.ReasonSay); err != nil {
			s.fail(ctx, id, "the machine could not be woken")
			return nil, ErrNoHost
		}
	}

	res, err := s.call(ctx, *hostID, cmdID, &hostdv1.SayToAgent{
		GuestId: guestID.String(), SayId: id.String(), Window: window,
		Text: text, Sender: r.Sender, FromOwner: r.FromOwner,
	})
	if err != nil {
		s.fail(ctx, id, failure(err))
		return nil, err
	}
	if !res.Ok {
		code, detail := res.GetError().GetCode(), res.GetError().GetMessage()
		s.fail(ctx, id, code)
		if code == "invalid_argument" || code == "not_found" {
			// The machine's own sentence names the windows, which is
			// the whole value of the refusal.
			return nil, &ErrAgent{Detail: detail, Several: code == "invalid_argument"}
		}
		return nil, fmt.Errorf("say: the machine refused the message: %s", code)
	}

	sr := res.GetSay()
	row.State, row.Window, row.Replaced = StateDelivered, window, sr.GetReplaced()
	if st := sr.GetState(); st == StateWaiting {
		row.State = StateWaiting
	}
	if w := sr.GetWindow(); w != "" {
		row.Window = w
	}
	// The machine replaced a message that was still waiting in that
	// window, so the row it replaced stops saying it is waiting.
	if row.Replaced {
		if _, err := s.pool.Exec(ctx,
			"update says set state = $1 where project_id = $2 and tmux_window = $3 and state = $4 and id <> $5",
			StateReplaced, r.ProjectID, row.Window, StateWaiting, id); err != nil {
			s.log.Warn("earlier message not marked replaced", "event", "agent_prompted", "project_id", r.ProjectID.String())
		}
	}
	if _, err := s.pool.Exec(ctx, "update says set state = $1, tmux_window = $2 where id = $3",
		row.State, row.Window, id); err != nil {
		return nil, err
	}
	s.log.Info("message sent to an agent", "event", "agent_prompted",
		"project_id", r.ProjectID.String(), "state", row.State, "replaced", row.Replaced)
	return row, nil
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

// fail records why a message never reached the agent. reason is a few
// words, never a message from the machine, which could carry the text.
func (s *Service) fail(ctx context.Context, id uuid.UUID, reason string) {
	if _, err := s.pool.Exec(ctx, "update says set state = $1, error = $2 where id = $3", StateFailed, reason, id); err != nil {
		s.log.Warn("message failure not recorded", "event", "agent_prompted", "say_id", id.String())
	}
}

// failure is the short reason stored on a failed row.
func failure(err error) string {
	switch {
	case errors.Is(err, ErrNoHost):
		return "the machine's host is not connected"
	case errors.Is(err, ErrTimeout):
		return "the machine did not answer"
	default:
		return "the message could not be sent"
	}
}
