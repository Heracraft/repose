package questions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(t *testing.T) (*Store, *clock, *[]Question) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	var emitted []Question
	s := New(t.TempDir(), func(q Question) { mu.Lock(); emitted = append(emitted, q); mu.Unlock() },
		slog.New(slog.NewTextHandler(io.Discard, nil)), c.now)
	return s, c, &emitted
}

func TestOpenValidatesAndCaps(t *testing.T) {
	s, _, emitted := newStore(t)
	if _, err := s.Open("claude", "w", "", "   ", nil, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty text: %v", err)
	}
	if _, err := s.Open("claude", "w", "", "q", []string{"a", "b", "c", "d"}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("four options: %v", err)
	}
	q, err := s.Open("claude", "w", "", strings.Repeat("é", 1000), []string{strings.Repeat("x", 100), "no", "No"}, 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Text) > TextCap || !strings.HasPrefix(q.Text, "é") {
		t.Fatalf("text not capped on a rune boundary: %d bytes", len(q.Text))
	}
	if len(q.Options) != 2 || len(q.Options[0]) != OptionCap {
		t.Fatalf("options = %q", q.Options)
	}
	if q.TimeoutS != uint32(MaxTimeout/time.Second) {
		t.Fatalf("timeout not capped: %d", q.TimeoutS)
	}
	if len(*emitted) != 1 {
		t.Fatalf("emitted %d", len(*emitted))
	}
	for i := 1; i < MaxOpen; i++ {
		if _, err := s.Open("claude", "w", "", "q", nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Open("claude", "w", "", "one too many", nil, 0); !errors.Is(err, ErrTooMany) {
		t.Fatalf("seventeenth: %v", err)
	}
}

func TestSweepExpiresAndForgets(t *testing.T) {
	s, c, emitted := newStore(t)
	q, _ := s.Open("pi", "pi", "", "still there?", nil, time.Minute)
	c.add(61 * time.Second)
	s.Sweep()
	got, err := s.Get(q.ID)
	if err != nil || got.State != StateExpired {
		t.Fatalf("after the timeout: %+v %v", got, err)
	}
	if last := (*emitted)[len(*emitted)-1]; last.State != StateExpired {
		t.Fatalf("expiry not announced: %+v", last)
	}
	// The answer that arrives after expiry changes nothing.
	if err := s.Answer(q.ID, StateAnswered, "yes"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(q.ID); got.State != StateExpired || got.Answer != "" {
		t.Fatalf("late answer applied: %+v", got)
	}
	c.add(Keep + time.Second)
	s.Sweep()
	if _, err := s.Get(q.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("closed question kept past Keep: %v", err)
	}
}

func TestWaitReturnsOnCloseOrDeadline(t *testing.T) {
	s, _, _ := newStore(t)
	q, _ := s.Open("claude", "w", "", "q", nil, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got, _ := s.Wait(ctx, q.ID); got.State != StateOpen {
		t.Fatalf("wait past the deadline = %+v", got)
	}
	go func() { time.Sleep(10 * time.Millisecond); _ = s.Answer(q.ID, StateNoChannel, "") }()
	if got, _ := s.Wait(context.Background(), q.ID); got.State != StateNoChannel {
		t.Fatalf("wait = %+v", got)
	}
	if err := s.Answer(q.ID, "bogus", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bogus status: %v", err)
	}
}

// TestCloseWhereCarriesTheReason (I-690): a prompt in the asker's agent
// closes only that agent process's open questions, with the reason, and
// each close is announced.
func TestCloseWhereCarriesTheReason(t *testing.T) {
	s, _, emitted := newStore(t)
	mine, _ := s.Open("claude", "claude", "101:5000", "q1", nil, 0)
	other, _ := s.Open("claude", "claude-2", "202:6000", "q2", nil, 0)
	n := s.CloseWhere(func(q Question) bool { return q.AgentProc == "101:5000" }, ReasonTerminal)
	if n != 1 {
		t.Fatalf("closed %d, want 1", n)
	}
	got, _ := s.Get(mine.ID)
	if got.State != StateCancelled || got.Reason != ReasonTerminal {
		t.Fatalf("mine = %+v", got)
	}
	if got, _ := s.Get(other.ID); !got.Open() {
		t.Fatalf("another agent's question was closed: %+v", got)
	}
	last := (*emitted)[len(*emitted)-1]
	if last.ID != mine.ID || last.Reason != ReasonTerminal {
		t.Fatalf("close not announced with its reason: %+v", last)
	}
	if n := s.CloseWhere(func(Question) bool { return true }, "bogus"); n != 0 {
		t.Fatalf("a reason outside the set closed %d", n)
	}
}

// TestCancelReason: the asker's DELETE closes with the reason it gives;
// List shows only open questions.
func TestCancelReason(t *testing.T) {
	s, _, _ := newStore(t)
	q, _ := s.Open("shell", "shell", "", "q", nil, 0)
	if err := s.Cancel(q.ID, StateCancelled, ReasonWithdrawn); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(q.ID); got.Reason != ReasonWithdrawn {
		t.Fatalf("reason = %q", got.Reason)
	}
	if l := s.List(func(Question) bool { return true }); len(l) != 0 {
		t.Fatalf("list = %+v, want no open question", l)
	}
}

// TestSweepClosesAnUnpolledQuestion (I-690): a question nobody has polled
// for OrphanAfter closes as asker_gone; a poll in progress keeps it.
func TestSweepClosesAnUnpolledQuestion(t *testing.T) {
	s, c, emitted := newStore(t)
	gone, _ := s.Open("claude", "claude", "", "anyone?", nil, time.Hour)
	waited, _ := s.Open("claude", "claude", "", "still here", nil, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() { close(started); _, _ = s.Wait(ctx, waited.ID) }()
	<-started
	time.Sleep(20 * time.Millisecond)
	c.add(OrphanAfter + time.Second)
	s.Sweep()
	if got, _ := s.Get(gone.ID); got.State != StateCancelled || got.Reason != ReasonAskerGone {
		t.Fatalf("unpolled = %+v", got)
	}
	if got, _ := s.Get(waited.ID); !got.Open() {
		t.Fatalf("a question with a poll in progress was closed: %+v", got)
	}
	if last := (*emitted)[len(*emitted)-1]; last.Reason != ReasonAskerGone {
		t.Fatalf("not announced: %+v", last)
	}
}

// TestPollTimeStartsAtLoad: after a guestd restart, an open question gets
// OrphanAfter from the load, not from its last poll before the restart.
func TestPollTimeStartsAtLoad(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s1 := New(dir, func(Question) {}, log, c.now)
	q, _ := s1.Open("claude", "claude", "", "q", nil, time.Hour)
	c.add(10 * time.Minute)
	s2 := New(dir, func(Question) {}, log, c.now)
	s2.Sweep()
	if got, _ := s2.Get(q.ID); !got.Open() {
		t.Fatalf("closed at load: %+v", got)
	}
	c.add(OrphanAfter + time.Second)
	s2.Sweep()
	if got, _ := s2.Get(q.ID); got.Reason != ReasonAskerGone {
		t.Fatalf("not closed after OrphanAfter from load: %+v", got)
	}
}

// TestPendingResendsGuestCloses (I-690): a close made on this side stays
// in Pending for Keep, so a close lost on the way is sent again on the
// next hostd connection; one that came from hostd is not.
func TestPendingResendsGuestCloses(t *testing.T) {
	s, c, _ := newStore(t)
	open, _ := s.Open("claude", "claude", "", "open", nil, time.Hour)
	closed, _ := s.Open("claude", "claude", "", "closed here", nil, time.Hour)
	answered, _ := s.Open("claude", "claude", "", "answered there", nil, time.Hour)
	_ = s.Cancel(closed.ID, StateCancelled, ReasonWithdrawn)
	_ = s.Answer(answered.ID, StateAnswered, "yes")
	ids := map[string]Question{}
	for _, q := range s.Pending() {
		ids[q.ID] = q
	}
	if _, ok := ids[open.ID]; !ok || len(ids) != 2 {
		t.Fatalf("pending = %+v", ids)
	}
	if q := ids[closed.ID]; q.State != StateCancelled || q.Reason != ReasonWithdrawn {
		t.Fatalf("the guest close is not re-sent as it was: %+v", q)
	}
	c.add(Keep + time.Second)
	for _, q := range s.Pending() {
		if q.ID == closed.ID {
			t.Fatal("a close older than Keep is still re-sent")
		}
	}
}
