package say_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/say"
	"github.com/heracraft/repose/internal/db/testdb"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

// sender stands in for hostmgr: it records each command and, when answer
// is set, hands it back as a result the way the host's stream does.
type sender struct {
	mu     sync.Mutex
	sent   []*hostdv1.Command
	answer func(cmd *hostdv1.Command)
}

func (s *sender) Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error {
	s.mu.Lock()
	s.sent = append(s.sent, cmd)
	answer := s.answer
	s.mu.Unlock()
	if answer != nil {
		answer(cmd)
	}
	return nil
}

func (s *sender) last() *hostdv1.Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return nil
	}
	return s.sent[len(s.sent)-1]
}

// TestASayResultFromAnotherHostIsNotTheAnswer (DECISIONS I-1368): the
// result of a SayToAgent counts only from the host it went to. A host the
// operator does not own could otherwise answer another host's command id
// with `delivered` and the sender would be told their words reached an
// agent they never reached. Probe: with the host check removed this test
// fails with "a stranger's result was taken as the delivery".
func TestASayResultFromAnotherHostIsNotTheAnswer(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("foreignsay")
	p := h.CreateRunning(u, "box")
	other, _ := h.AddHost(t, "stranger", fakehostd.Options{})
	// The harness runs a worker of its own against the fake host; this
	// test's worker is the one under test.
	h.StopSays()
	snd := &sender{}
	svc := say.New(h.Pool, snd, h.Log)
	svc.Metrics = h.Metrics
	svc.Poll = 20 * time.Millisecond
	forged := &hostdv1.SayResult{State: say.StateDelivered, Window: "claude"}
	snd.answer = func(cmd *hostdv1.Command) {
		svc.OnResult(h.Ctx, other, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
			Payload: &hostdv1.Result_Say{Say: forged}})
	}
	ctx, cancel := context.WithCancel(h.Ctx)
	defer cancel()
	// The worker, as the grpc process runs it.
	go func() {
		for ctx.Err() == nil {
			_ = svc.Once(ctx)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	type answer struct {
		s   *say.Say
		err error
	}
	done := make(chan answer, 1)
	go func() {
		s, err := svc.Send(ctx, say.Request{ProjectID: p.ID, UserID: u.ID, Text: "hi", Sender: u.Handle, FromOwner: true})
		done <- answer{s, err}
	}()
	h.WaitFor("the message sent to the host", func() bool { return snd.last() != nil })
	h.WaitFor("the stranger's result refused", func() bool { return h.Refused("foreign_command") == 1 })
	select {
	case a := <-done:
		t.Fatalf("a stranger's result was taken as the delivery: %+v %v", a.s, a.err)
	case <-time.After(300 * time.Millisecond):
	}
	// The real host's result is the answer.
	svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: snd.last().CommandId, Ok: true,
		Payload: &hostdv1.Result_Say{Say: &hostdv1.SayResult{State: say.StateDelivered, Window: "claude"}}})
	select {
	case a := <-done:
		if a.err != nil || a.s.State != say.StateDelivered {
			t.Fatalf("the real host's result: %+v %v", a.s, a.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the real host's result did not answer the send")
	}
}
