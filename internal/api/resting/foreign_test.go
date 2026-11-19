package resting_test

import (
	"context"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/resting"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// TestAPauseResultFromAnotherHostWritesNothing (DECISIONS I-1368): a
// pause's result counts only from the host the pause went to. A host the
// operator does not own could otherwise answer another host's command id
// and mark a stranger's machine resting, which is a lie the rest rule,
// the gateway's wake and the dashboard would all read. Probe: with the
// host check removed this test fails with "a result from another host
// marked the machine resting".
func TestAPauseResultFromAnotherHostWritesNothing(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("foreignpause")
	p := h.CreateRunning(u, "box")
	other, _ := h.AddHost(t, "stranger", fakehostd.Options{})
	snd := &sender{}
	svc := resting.New(h.Pool, snd, h.Log)
	svc.Metrics = h.Metrics
	at := time.Now().Add(-time.Minute).Unix()
	snd.answer = func(cmd *hostdv1.Command) {
		svc.OnResult(h.Ctx, other, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
			Payload: &hostdv1.Result_Pause{Pause: &hostdv1.PauseResult{Paused: true, At: at}}})
	}
	pause(t, h, svc, snd, p.ID, *p.HostID, *p.GuestID)
	if h.Project(p.ID).RestingSince != nil {
		t.Fatal("a result from another host marked the machine resting")
	}
	if n := h.Refused("foreign_command"); n != 1 {
		t.Fatalf("foreign_command refusals %d, want 1", n)
	}
	// The record is still there for the real host's answer.
	cmd := snd.sent[len(snd.sent)-1]
	svc.OnResult(h.Ctx, *p.HostID, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
		Payload: &hostdv1.Result_Pause{Pause: &hostdv1.PauseResult{Paused: true, At: at}}})
	if got := h.Project(p.ID).RestingSince; got == nil || got.Unix() != at {
		t.Fatalf("the real host's result did not write the marker: %v", got)
	}
}

// TestAWakeResultFromAnotherHostIsNotTheAnswer: the same for a wake. A
// forged `ok` from a stranger's host would tell a gateway dial, an answer
// or a secret push that a frozen machine is awake when nothing resumed
// it.
func TestAWakeResultFromAnotherHostIsNotTheAnswer(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("foreignwake")
	p := h.CreateRunning(u, "box")
	other, _ := h.AddHost(t, "stranger", fakehostd.Options{})
	if _, err := h.Pool.Exec(h.Ctx, "update projects set resting_since = now() where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	snd := &sender{}
	svc := resting.New(h.Pool, snd, h.Log)
	svc.Metrics = h.Metrics
	// A caller's own deadline ends the wait sooner than WakeTimeout.
	ctx, cancel := context.WithTimeout(h.Ctx, 500*time.Millisecond)
	defer cancel()
	snd.answer = func(cmd *hostdv1.Command) {
		svc.OnResult(h.Ctx, other, &hostdv1.Result{CommandId: cmd.CommandId, Ok: true,
			Payload: &hostdv1.Result_Wake{Wake: &hostdv1.WakeResult{WasResting: true, WakeMs: 1}}})
	}
	_, err := svc.Wake(ctx, p.ID, "test")
	if err == nil {
		t.Fatal("a stranger's result answered the wake")
	}
	if h.Project(p.ID).RestingSince == nil {
		t.Fatal("a stranger's result cleared the marker")
	}
	if n := h.Refused("foreign_command"); n != 1 {
		t.Fatalf("foreign_command refusals %d, want 1", n)
	}
}
