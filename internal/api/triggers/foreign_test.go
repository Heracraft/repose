package triggers_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/triggers"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/triggerrun"
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

// TestARunResultFromAnotherHostIsDropped (DECISIONS I-1368): a RunTrigger's
// result counts only from the host the run went to. A host the operator
// does not own could otherwise write `working`, `done` or `failed` onto
// another project's run, with its own summary, and the owner would read
// it as their agent's. Probe: with the host check removed this test fails
// with "a stranger's result moved the run".
func TestARunResultFromAnotherHostIsDropped(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("foreignrun")
	p := h.CreateRunning(u, "box")
	other, _ := h.AddHost(t, "stranger", fakehostd.Options{})
	if _, _, err := h.Triggers.Put(h.Ctx, u.ID, p.ID, p.Class, triggers.Input{
		Name: "apply", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("a.md"),
	}); err != nil {
		t.Fatal(err)
	}
	run, err := h.Triggers.Fire(h.Ctx, p.ID, "apply", triggers.FireOpts{Source: triggerrun.SourceHand})
	if err != nil {
		t.Fatal(err)
	}
	snd := &sender{}
	clock := triggers.NewClock(h.Triggers, snd, h.Log)
	clock.Metrics = h.Metrics
	if err := clock.Once(h.Ctx); err != nil {
		t.Fatal(err)
	}
	snd.mu.Lock()
	n := len(snd.sent)
	snd.mu.Unlock()
	if n != 1 || snd.sent[0].CommandId != run.ID.String() {
		t.Fatalf("sent %d commands, want the run %s", n, run.ID)
	}
	state := func() string {
		var st string
		if err := h.Pool.QueryRow(h.Ctx, "select state from trigger_runs where id = $1", run.ID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	forged := &hostdv1.Result{CommandId: run.ID.String(), Ok: true,
		Payload: &hostdv1.Result_TriggerRun{TriggerRun: &hostdv1.TriggerRunResult{State: triggerrun.StateWorking, Window: "t-apply"}}}
	clock.OnResult(h.Ctx, other, forged)
	if st := state(); st != triggers.StateSending {
		t.Fatalf("a stranger's result moved the run to %q", st)
	}
	if n := h.Refused("foreign_command"); n != 1 {
		t.Fatalf("foreign_command refusals %d, want 1", n)
	}
	clock.OnResult(h.Ctx, *p.HostID, forged)
	if st := state(); st != triggerrun.StateWorking {
		t.Fatalf("the real host's result left the run %q", st)
	}
}
