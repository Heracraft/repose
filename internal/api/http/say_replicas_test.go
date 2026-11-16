package httpapi_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/say"
)

// `repose say` across the api's two processes (DECISIONS I-1162). In
// production the api is two Coolify applications: `API_MODE=http`
// serves the CLI and `API_MODE=grpc` holds every host's stream. On
// 2026-11-11 07:30 UTC every `repose say` answered "The machine did not
// take the message." because the http process sent the SayToAgent on
// its own stream set, which holds no host.
//
// httpReplica is that process: a second say.Service on the same
// database, over its own real hostmgr with no stream on it, and its
// delivery worker running as app.go runs it (the advisory lock leaves
// the pass to the replica that already holds it, which is the
// harness's).
func httpReplica(t *testing.T, a *askEnv) *say.Service {
	t.Helper()
	mgr := hostmgr.New(a.h.Pool, a.h.CA.X509(), "replica-http", a.h.Metrics, a.h.Log)
	if mgr.Connected(a.h.HostID) {
		t.Fatal("the second replica holds the host's stream; that is not the shape this tests")
	}
	s := say.New(a.h.Pool, mgr, a.h.Log)
	s.Waker = a.h.Resting
	go s.Run(a.h.Ctx)
	return s
}

// TestSayIsDeliveredByTheReplicaHoldingTheStream: a say that arrives on
// the process without the host's stream is still delivered, by the one
// that has it, and the sender reads the machine's own answer. On the
// commit before this one it is `say: the machine's host is not
// connected` and no command reaches the machine.
func TestSayIsDeliveredByTheReplicaHoldingTheStream(t *testing.T) {
	a := newAskEnv(t, "sayreplica")
	replica := httpReplica(t, a)

	out, err := replica.Send(a.h.Ctx, say.Request{
		ProjectID: a.p.ID, UserID: a.u.ID, Text: "run the tests before you commit",
		Sender: a.u.Handle, FromOwner: true,
	})
	if err != nil {
		t.Fatalf("say on the replica without the stream: %v", err)
	}
	if out.State != "delivered" || out.Window != "claude" {
		t.Fatalf("answer = %+v, want delivered in claude", out)
	}

	cmds := a.h.Fake.Says()
	if len(cmds) != 1 {
		t.Fatalf("%d SayToAgent commands reached the machine, want 1", len(cmds))
	}
	if c := cmds[0]; c.GetSayId() != out.ID.String() || c.GetText() != "run the tests before you commit" {
		t.Fatalf("command = %+v", c)
	}

	var state, window string
	if err := a.h.Pool.QueryRow(a.h.Ctx, "select state, tmux_window from says where id = $1", out.ID).Scan(&state, &window); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" || window != "claude" {
		t.Fatalf("row = %s / %s", state, window)
	}
}

// TestTheRefusalCrossesTheReplicas: the machine's sentence naming the
// agent windows is what the sender picks one from, so it has to reach
// the process that is waiting and never saw the command's result.
func TestTheRefusalCrossesTheReplicas(t *testing.T) {
	a := newAskEnv(t, "sayreplicarefuse")
	replica := httpReplica(t, a)

	a.h.Fake.SetSayFailure("invalid_argument", "2 agents are running: claude, codex. Name one with --agent")
	t.Cleanup(func() { a.h.Fake.SetSayFailure("", "") })

	_, err := replica.Send(a.h.Ctx, say.Request{ProjectID: a.p.ID, UserID: a.u.ID, Text: "hello", Sender: a.u.Handle, FromOwner: true})
	var agentErr *say.ErrAgent
	if !errors.As(err, &agentErr) {
		t.Fatalf("err = %v, want the machine's refusal", err)
	}
	if !agentErr.Several || !strings.Contains(agentErr.Detail, "claude, codex") {
		t.Fatalf("the refusal lost the windows: %+v", agentErr)
	}
}

// TestASayNobodyDeliversStaysOutOfTheTerminal: with the process that
// holds the streams down, nothing claims the row. The waiting process
// answers `machine_unreachable` as it did before and fails the row, so
// a worker that comes back later does not type the message into the
// agent's terminal long after the sender was told it did not arrive.
func TestASayNobodyDeliversStaysOutOfTheTerminal(t *testing.T) {
	a := newAskEnv(t, "sayreplicanobody")
	a.h.StopSays()
	mgr := hostmgr.New(a.h.Pool, a.h.CA.X509(), "replica-http", a.h.Metrics, a.h.Log)
	replica := say.New(a.h.Pool, mgr, a.h.Log)
	replica.Poll, replica.Wait = 10*time.Millisecond, 300*time.Millisecond

	_, err := replica.Send(a.h.Ctx, say.Request{ProjectID: a.p.ID, UserID: a.u.ID, Text: "hello", Sender: a.u.Handle, FromOwner: true})
	if !errors.Is(err, say.ErrTimeout) {
		t.Fatalf("err = %v, want the machine did not answer", err)
	}
	var state, reason string
	if err := a.h.Pool.QueryRow(a.h.Ctx, "select state, coalesce(error, '') from says where project_id = $1", a.p.ID).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Fatalf("row = %s / %q, want failed", state, reason)
	}
	if n := len(a.h.Fake.Says()); n != 0 {
		t.Fatalf("%d commands reached the machine for a say nobody delivered", n)
	}
}
