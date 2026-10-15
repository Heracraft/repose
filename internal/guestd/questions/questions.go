// Package questions holds the questions `repose-ask` is waiting on inside a
// guest (DECISIONS I-244). A question is opened from the hook socket, sent
// up to hostd as a Question notify, and closed by hostd's AnswerQuestion
// request, by the asker giving up, or by its own timeout.
//
// Each open question is a file under /run/repose/questions, so a guestd
// restart keeps every question an asker is still waiting on; a reboot
// clears the tmpfs, and an asker that reconnects after one gets not found.
// The question and answer text are tenant content: they go to hostd and
// into these root-only files, and never into a log line.
package questions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Limits (docs/interfaces/guest-conventions.md "Asking the user").
const (
	// TextCap is the cap on a question's text and on a notify message, the
	// same 1 KB as an AgentEvent summary.
	TextCap = 1 << 10
	// AnswerCap is the cap on an answer.
	AnswerCap = 1 << 10
	// MaxOptions is how many fixed answers a question may offer: ntfy
	// shows at most three action buttons.
	MaxOptions = 3
	// OptionCap is the cap on one option.
	OptionCap = 64
	// MaxOpen is how many questions one guest may have open at once.
	MaxOpen = 16
	// DefaultTimeout is how long an ask waits without --timeout.
	DefaultTimeout = 30 * time.Minute
	// MaxTimeout is the longest an ask may wait.
	MaxTimeout = 24 * time.Hour
	// Keep is how long a closed question stays readable, so an asker that
	// was reconnecting when the answer arrived still gets it, and how long
	// a close made on this side is re-sent on each new hostd connection.
	Keep = 10 * time.Minute
	// OrphanAfter is how long an open question may go without a poll
	// before it closes as asker_gone (DECISIONS I-690). repose-ask polls
	// every 25 s and retries a lost guestd for 2 minutes, during which
	// the poll time does not move; a restarted guestd starts every open
	// question's clock at its load.
	OrphanAfter = 90 * time.Second
)

// Reasons a question closes on this side (DECISIONS I-690), sent up as
// Question.reason. The api stores terminal as answered in the terminal and
// withdrawn as withdrawn by the agent; the rest as a plain cancel.
const (
	ReasonTerminal    = "terminal"
	ReasonWithdrawn   = "withdrawn"
	ReasonInterrupted = "interrupted"
	ReasonAskerGone   = "asker_gone"
)

// Reasons is the fixed set of close reasons.
var Reasons = map[string]bool{ReasonTerminal: true, ReasonWithdrawn: true, ReasonInterrupted: true, ReasonAskerGone: true}

// States. "" (open) is never written as a status; the rest are the status
// values of the AnswerQuestion request and the Question notify.
const (
	StateOpen      = "open"
	StateAnswered  = "answered"
	StateCancelled = "cancelled"
	StateExpired   = "expired"
	StateNoChannel = "no_channel"
)

// Errors.
var (
	ErrNotFound = errors.New("questions: no such question")
	ErrTooMany  = errors.New("questions: too many open questions")
	ErrInvalid  = errors.New("questions: invalid question")
)

// Question is one ask.
type Question struct {
	ID       string    `json:"id"`
	Agent    string    `json:"agent"`
	Window   string    `json:"window,omitempty"`
	Text     string    `json:"text"`
	Options  []string  `json:"options,omitempty"`
	TimeoutS uint32    `json:"timeout_s"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	State    string    `json:"state"`
	Answer   string    `json:"answer,omitempty"`
	Closed   time.Time `json:"closed,omitempty"`
	// Reason is why this side closed it, one of Reasons; "" for an
	// expiry and for a close hostd sent.
	Reason string `json:"reason,omitempty"`
	// AgentProc is the asker's agent process as "pid:starttime", "" when
	// the asker runs under no agent. It stays in the guest: a prompt in
	// that process closes the question (I-690).
	AgentProc string `json:"agent_proc,omitempty"`
	// GuestClosed marks a close made on this side, which is re-sent on a
	// new hostd connection until Keep has passed.
	GuestClosed bool `json:"guest_closed,omitempty"`
}

// Open reports whether the question is still waiting.
func (q Question) Open() bool { return q.State == StateOpen }

// Emitter is told about a question when it opens, when it is re-announced
// on a new hostd connection, and when the guest side closes it.
type Emitter func(q Question)

type entry struct {
	q    Question
	done chan struct{}
	// lastPoll is when an asker last polled; polling counts the polls
	// in progress. Neither is persisted.
	lastPoll time.Time
	polling  int
}

// Store is the set of questions of this guest.
type Store struct {
	dir  string
	emit Emitter
	log  *slog.Logger
	now  func() time.Time

	mu sync.Mutex
	qs map[string]*entry
}

// New builds a store over dir and loads what a previous guestd left there.
func New(dir string, emit Emitter, log *slog.Logger, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	s := &Store{dir: dir, emit: emit, log: log, now: now, qs: map[string]*entry{}}
	s.load()
	return s
}

func (s *Store) load() {
	now := s.now()
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var q Question
		if json.Unmarshal(b, &q) != nil || q.ID == "" {
			continue
		}
		en := &entry{q: q, done: make(chan struct{}), lastPoll: now}
		if !q.Open() {
			close(en.done)
		}
		s.qs[q.ID] = en
	}
}

// Clean normalises a text field: valid UTF-8, trimmed, capped at n bytes on
// a rune boundary.
func Clean(s string, n int) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "?")
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Open starts a question and announces it. proc is the asker's agent
// process key ("" for none).
func (s *Store) Open(agent, window, proc, text string, options []string, timeout time.Duration) (Question, error) {
	text = Clean(text, TextCap)
	if text == "" {
		return Question{}, fmt.Errorf("%w: the question is empty", ErrInvalid)
	}
	var opts []string
	seen := map[string]bool{}
	for _, o := range options {
		o = Clean(o, OptionCap)
		if o == "" || seen[strings.ToLower(o)] {
			continue
		}
		seen[strings.ToLower(o)] = true
		opts = append(opts, o)
	}
	if len(opts) > MaxOptions {
		return Question{}, fmt.Errorf("%w: at most %d options", ErrInvalid, MaxOptions)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	now := s.now().UTC()
	q := Question{
		ID: uuid.Must(uuid.NewV7()).String(), Agent: agent, Window: window, Text: text, Options: opts,
		TimeoutS: uint32(timeout / time.Second), Created: now, Expires: now.Add(timeout), State: StateOpen,
		AgentProc: proc,
	}
	s.mu.Lock()
	open := 0
	for _, e := range s.qs {
		if e.q.Open() {
			open++
		}
	}
	if open >= MaxOpen {
		s.mu.Unlock()
		return Question{}, ErrTooMany
	}
	s.qs[q.ID] = &entry{q: q, done: make(chan struct{}), lastPoll: s.now()}
	s.persist(q)
	s.mu.Unlock()
	s.emit(q)
	return q, nil
}

// Get returns a question.
func (s *Store) Get(id string) (Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.qs[id]
	if e == nil {
		return Question{}, ErrNotFound
	}
	return e.q, nil
}

// Wait returns the question once it is closed, or as it stands when ctx
// ends first. A wait is a poll: the orphan sweep leaves the question
// alone while one is in progress and for OrphanAfter after it.
func (s *Store) Wait(ctx context.Context, id string) (Question, error) {
	s.mu.Lock()
	e := s.qs[id]
	if e != nil {
		e.lastPoll = s.now()
		e.polling++
	}
	s.mu.Unlock()
	if e == nil {
		return Question{}, ErrNotFound
	}
	select {
	case <-e.done:
	case <-ctx.Done():
	}
	s.mu.Lock()
	e.lastPoll = s.now()
	e.polling--
	s.mu.Unlock()
	return s.Get(id)
}

// Answer closes a question from hostd's AnswerQuestion. A repeat for a
// question already closed is a no-op, so the api may resend.
func (s *Store) Answer(id, status, answer string) error {
	switch status {
	case StateAnswered, StateCancelled, StateExpired, StateNoChannel:
	default:
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.closeLocked(id, status, Clean(answer, AnswerCap), "", false)
	return err
}

// Cancel closes a question from the guest side (the asker gave up or took
// it back, or the timeout passed) and tells hostd. reason is one of
// Reasons, or "" (an expiry).
func (s *Store) Cancel(id, state, reason string) error {
	if reason != "" && !Reasons[reason] {
		return fmt.Errorf("%w: unknown reason %q", ErrInvalid, reason)
	}
	s.mu.Lock()
	q, err := s.closeLocked(id, state, "", reason, true)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if q != nil {
		s.emit(*q)
	}
	return nil
}

// CloseWhere cancels every open question match accepts, with reason, tells
// hostd about each, and returns how many it closed. A reason outside
// Reasons closes nothing.
func (s *Store) CloseWhere(match func(Question) bool, reason string) int {
	if !Reasons[reason] {
		return 0
	}
	var closed []Question
	s.mu.Lock()
	for id, e := range s.qs {
		if !e.q.Open() || !match(e.q) {
			continue
		}
		if q, err := s.closeLocked(id, StateCancelled, "", reason, true); err == nil && q != nil {
			closed = append(closed, *q)
		}
	}
	s.mu.Unlock()
	for _, q := range closed {
		s.emit(q)
	}
	return len(closed)
}

// List returns the open questions match accepts, oldest first.
func (s *Store) List(match func(Question) bool) []Question {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Question
	for _, e := range s.qs {
		if e.q.Open() && match(e.q) {
			out = append(out, e.q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// closeLocked marks a question closed and returns it, or nil if it was
// already; called with mu held.
func (s *Store) closeLocked(id, state, answer, reason string, guest bool) (*Question, error) {
	e := s.qs[id]
	if e == nil {
		return nil, ErrNotFound
	}
	if !e.q.Open() {
		return nil, nil
	}
	e.q.State = state
	if state == StateAnswered {
		e.q.Answer = answer
	}
	e.q.Reason = reason
	e.q.GuestClosed = guest
	e.q.Closed = s.now().UTC()
	close(e.done)
	s.persist(e.q)
	q := e.q
	return &q, nil
}

// Pending lists, oldest first, what is announced again on a new hostd
// connection: the open questions, and the closes made on this side in the
// last Keep. The api applies a close only to a pending row, so a close it
// already has changes nothing; one lost on the way (a full queue, a hostd
// restart) arrives this way (DECISIONS I-690).
func (s *Store) Pending() []Question {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var out []Question
	for _, e := range s.qs {
		if e.q.Open() || (e.q.GuestClosed && now.Sub(e.q.Closed) <= Keep) {
			out = append(out, e.q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Sweep expires open questions past their deadline, closes those nobody
// has polled for OrphanAfter (asker_gone), and forgets closed ones older
// than Keep.
func (s *Store) Sweep() {
	now := s.now()
	var expired, orphaned []string
	s.mu.Lock()
	for id, e := range s.qs {
		switch {
		case e.q.Open() && !now.Before(e.q.Expires):
			expired = append(expired, id)
		case e.q.Open() && e.polling == 0 && now.Sub(e.lastPoll) > OrphanAfter:
			orphaned = append(orphaned, id)
		case !e.q.Open() && now.Sub(e.q.Closed) > Keep:
			delete(s.qs, id)
			_ = os.Remove(s.file(id)) // a leftover file is reloaded as closed and swept again
		}
	}
	s.mu.Unlock()
	for _, id := range expired {
		_ = s.Cancel(id, StateExpired, "") // gone meanwhile is fine
	}
	for _, id := range orphaned {
		_ = s.Cancel(id, StateCancelled, ReasonAskerGone)
	}
}

// Run sweeps every few seconds until ctx ends.
func (s *Store) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep()
		}
	}
}

func (s *Store) file(id string) string { return filepath.Join(s.dir, id+".json") }

// persist writes the question file; called with mu held. A failure is
// logged by reason only and the question lives on in memory.
func (s *Store) persist(q Question) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.log.Warn("could not create the questions directory", "event", "agent_question", "reason", "mkdir")
		return
	}
	b, _ := json.Marshal(q) // plain struct
	tmp := s.file(q.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		s.log.Warn("could not write a question", "event", "agent_question", "reason", "write")
		return
	}
	if err := os.Rename(tmp, s.file(q.ID)); err != nil {
		s.log.Warn("could not write a question", "event", "agent_question", "reason", "rename")
	}
}
