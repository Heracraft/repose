package resting_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/resting"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// sender stands in for hostmgr: it counts the commands the service sends
// and, when answer is set, hands each one back as a result the way the
// host's stream does.
type sender struct {
	mu   sync.Mutex
	sent []*hostdv1.Command
	fail error
	// answer is called inline from Send, as the host's stream would call
	// OnResult from the api's own goroutine.
	answer func(cmd *hostdv1.Command)
}

func (s *sender) Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error {
	s.mu.Lock()
	if s.fail != nil {
		err := s.fail
		s.mu.Unlock()
		return err
	}
	s.sent = append(s.sent, cmd)
	answer := s.answer
	s.mu.Unlock()
	if answer != nil {
		answer(cmd)
	}
	return nil
}

func (s *sender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// TestWakeOfAMachineWithNothingToWake: a wake is cheap to ask for and
// safe to ask for twice, which is what makes the gateway's five-second
// route cache harmless. Where there is nothing to wake it answers
// was_resting false and sends no command at all.
func TestWakeOfAMachineWithNothingToWake(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("nowake")
	p := h.CreateRunning(u, "box")
	snd := &sender{}
	svc := resting.New(h.Pool, snd, h.Log)

	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'stopped' where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Wake(h.Ctx, p.ID, resting.ReasonConnect)
	if err != nil || res.GetWasResting() || snd.count() != 0 {
		t.Fatalf("a stopped machine: %+v %v, %d commands sent", res, err, snd.count())
	}
	// Running again, but on a host whose Hello never named the feature:
	// a hostd older than I-818 answers a command it does not know
	// invalid_argument, so none is sent.
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'running' where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set features = '{}' where id = $1", *p.HostID); err != nil {
		t.Fatal(err)
	}
	res, err = svc.Wake(h.Ctx, p.ID, resting.ReasonConnect)
	if err != nil || res.GetWasResting() || snd.count() != 0 {
		t.Fatalf("a host without the feature: %+v %v, %d commands sent", res, err, snd.count())
	}
}

// TestWakeWithNoHostConnected: the caller is told, rather than waiting
// out the deadline, because everything waiting on a wake is a user
// waiting.
func TestWakeWithNoHostConnected(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("nohost")
	p := h.CreateRunning(u, "box")
	snd := &sender{fail: errors.New("host not connected")}
	svc := resting.New(h.Pool, snd, h.Log)
	if _, err := svc.Wake(h.Ctx, p.ID, resting.ReasonOp); !errors.Is(err, resting.ErrNoHost) {
		t.Fatalf("err %v, want ErrNoHost", err)
	}
}

// TestConcurrentWakesSendOneCommand: several connections to one frozen
// machine at once -- an editor opening three of them -- are one wake
// (DECISIONS I-818).
func TestConcurrentWakesSendOneCommand(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("onewake")
	p := h.CreateRunning(u, "box")
	if _, err := h.Pool.Exec(h.Ctx, "update projects set resting_since = now() where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	snd := &sender{}
	svc := resting.New(h.Pool, snd, h.Log)
	snd.answer = func(cmd *hostdv1.Command) {
		// Held, so the other callers arrive while this one is in flight.
		time.Sleep(50 * time.Millisecond)
		svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
			Payload: &hostdv1.Result_Wake{Wake: &hostdv1.WakeResult{WasResting: true, WakeMs: 12}}})
	}
	var wg sync.WaitGroup
	results := make([]*hostdv1.WakeResult, 3)
	errs := make([]error, 3)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.Wake(h.Ctx, p.ID, resting.ReasonConnect)
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || !results[i].GetWasResting() || results[i].GetWakeMs() != 12 {
			t.Fatalf("caller %d: %+v %v", i, results[i], errs[i])
		}
	}
	if n := snd.count(); n != 1 {
		t.Fatalf("%d commands for three callers, want 1", n)
	}
	if h.Project(p.ID).RestingSince != nil {
		t.Fatal("the marker survived the wake")
	}
}

// TestPauseResultWritesTheMarker: hostd's event is what normally writes
// the row, and the pause's own result writes it too, idempotently, so a
// lost event does not leave a frozen machine reading awake. A refusal
// (paused=false with a lower_snake why_not) writes nothing.
func TestPauseResultWritesTheMarker(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("pauseres")
	p := h.CreateRunning(u, "box")
	snd := &sender{}
	svc := resting.New(h.Pool, snd, h.Log)
	// A refusal first.
	snd.answer = func(cmd *hostdv1.Command) {
		svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
			Payload: &hostdv1.Result_Pause{Pause: &hostdv1.PauseResult{Paused: false, WhyNot: "command_in_flight"}}})
	}
	pause(t, h, svc, snd, p.ID, *p.HostID, *p.GuestID)
	if h.Project(p.ID).RestingSince != nil {
		t.Fatal("a refused pause marked the machine resting")
	}
	// Then one the host took.
	at := time.Now().Add(-time.Minute).Unix()
	snd.answer = func(cmd *hostdv1.Command) {
		svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
			Payload: &hostdv1.Result_Pause{Pause: &hostdv1.PauseResult{Paused: true, At: at}}})
	}
	pause(t, h, svc, snd, p.ID, *p.HostID, *p.GuestID)
	got := h.Project(p.ID).RestingSince
	if got == nil || got.Unix() != at {
		t.Fatalf("resting_since %v, want %v", got, time.Unix(at, 0))
	}
	// A second result does not move it: a rest never looks younger than
	// it is.
	later := time.Now().Unix()
	snd.answer = func(cmd *hostdv1.Command) {
		svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
			Payload: &hostdv1.Result_Pause{Pause: &hostdv1.PauseResult{Paused: true, At: later}}})
	}
	pause(t, h, svc, snd, p.ID, *p.HostID, *p.GuestID)
	if got := h.Project(p.ID).RestingSince; got == nil || got.Unix() != at {
		t.Fatalf("resting_since moved to %v", got)
	}
}

// pause sends one pause by hand, the way the tick does. The rule's own
// terms are tested in internal/api/http; here the result path is what is
// under test, and the sender answers it inline.
func pause(t *testing.T, h *apitest.Harness, svc *resting.Service, snd *sender, projectID, hostID, guestID uuid.UUID) {
	t.Helper()
	before := snd.count()
	if err := svc.SendPauseForTest(h.Ctx, resting.Candidate{ProjectID: projectID, HostID: hostID, GuestID: guestID}); err != nil {
		t.Fatal(err)
	}
	if snd.count() != before+1 {
		t.Fatalf("%d pauses sent, want one more than %d", snd.count(), before)
	}
}
