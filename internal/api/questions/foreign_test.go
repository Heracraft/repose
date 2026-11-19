package questions_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/questions"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// sender stands in for hostmgr and records each command.
type sender struct {
	mu   sync.Mutex
	sent []*hostdv1.Command
}

func (s *sender) Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, cmd)
	return nil
}

func (s *sender) last(t *testing.T) *hostdv1.Command {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		t.Fatal("nothing was sent")
	}
	return s.sent[len(s.sent)-1]
}

// TestAnAnswerResultFromAnotherHostIsDropped (DECISIONS I-1368): the
// result of an AnswerQuestion, and of the hold that keeps a question
// open while the owner types, counts only from the host it went to. A
// host the operator does not own could otherwise mark another project's
// answer delivered, and the worker would never send it again. Probe: with
// the host check removed this test fails with "a stranger's result marked
// the answer delivered".
func TestAnAnswerResultFromAnotherHostIsDropped(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("foreignq")
	p := h.CreateRunning(u, "box")
	other, _ := h.AddHost(t, "stranger", fakehostd.Options{})
	snd := &sender{}
	svc := questions.New(h.Pool, h.Events, snd, h.Log)
	svc.Metrics = h.Metrics
	qid := uuid.Must(uuid.NewV7())
	if err := svc.OnQuestion(h.Ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: p.GuestID.String(), QuestionId: qid.String(), Agent: "claude", Text: "Drop it?", Options: []string{"yes", "no"}, TimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	delivered := func() bool {
		var at *time.Time
		if err := h.Pool.QueryRow(h.Ctx, "select delivered_at from questions where id = $1", qid).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at != nil
	}

	// The hold first (I-1242): its result reaches a log line only, and a
	// stranger's is counted and dropped.
	if _, err := svc.Hold(h.Ctx, u.ID, qid); err != nil {
		t.Fatal(err)
	}
	hold := snd.last(t)
	if svc.OnResult(h.Ctx, other, &hostdv1.Result{CommandId: hold.CommandId, Ok: true}) != true {
		t.Fatal("a stranger's hold result was not consumed")
	}
	if n := h.Refused("foreign_command"); n != 1 {
		t.Fatalf("foreign_command refusals after the hold %d, want 1", n)
	}
	if !svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: hold.CommandId, Ok: true}) {
		t.Fatal("the real host's hold result was not recognised")
	}

	// Then the answer's delivery.
	if _, err := svc.Answer(h.Ctx, &u.ID, qid, "yes", "cli"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Once(h.Ctx); err != nil {
		t.Fatal(err)
	}
	cmd := snd.last(t)
	if cmd.GetAnswerQuestion().GetQuestionId() != qid.String() {
		t.Fatalf("the last command is not the answer: %v", cmd)
	}
	svc.OnResult(h.Ctx, other, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true})
	if delivered() {
		t.Fatal("a stranger's result marked the answer delivered")
	}
	if n := h.Refused("foreign_command"); n != 2 {
		t.Fatalf("foreign_command refusals %d, want 2", n)
	}
	svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true})
	if !delivered() {
		t.Fatal("the real host's result did not mark the answer delivered")
	}
}
