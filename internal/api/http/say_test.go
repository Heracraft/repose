package httpapi_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// `repose say` end to end against the api and the fake host (DECISIONS
// I-1144): the row, the command down the channel, its result, and the
// refusals a sender reads.

// TestSayStoresTheRowAndSendsOneCommand: POST /projects/:id/say writes a
// says row, sends exactly one SayToAgent carrying the text and the
// sender, and answers with the machine's state and window.
func TestSayStoresTheRowAndSendsOneCommand(t *testing.T) {
	a := newAskEnv(t, "sayer")

	r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/say", map[string]any{"text": "run the tests before you commit"})
	if r.status != 200 {
		t.Fatalf("POST say: %d %s", r.status, r.raw)
	}
	if r.body["state"] != "delivered" || r.body["window"] != "claude" {
		t.Fatalf("answer = %v, want delivered in claude", r.body)
	}
	if r.body["text"] != "run the tests before you commit" {
		t.Fatalf("answer lost the text: %v", r.body)
	}

	cmds := a.h.Fake.Says()
	if len(cmds) != 1 {
		t.Fatalf("%d SayToAgent commands, want 1", len(cmds))
	}
	c := cmds[0]
	if c.GetGuestId() != a.p.GuestID.String() || c.GetText() != "run the tests before you commit" ||
		c.GetSender() == "" || !c.GetFromOwner() || c.GetSayId() != r.body["id"] {
		t.Fatalf("command = %+v", c)
	}

	var state, window, text, sender string
	var fromOwner bool
	if err := a.h.Pool.QueryRow(a.h.Ctx,
		"select state, tmux_window, text, sender, from_owner from says where id = $1", c.GetSayId()).
		Scan(&state, &window, &text, &sender, &fromOwner); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" || window != "claude" || text != "run the tests before you commit" || !fromOwner {
		t.Fatalf("row = %s %s %q from_owner=%v", state, window, text, fromOwner)
	}
	if sender != a.u.Handle {
		t.Fatalf("sender = %q, want the account's handle %q", sender, a.u.Handle)
	}
}

// TestEverySayGetsItsOwnCommandID: the command_id is the idempotency key
// hostd stores a result under, so two messages never share one.
func TestEverySayGetsItsOwnCommandID(t *testing.T) {
	a := newAskEnv(t, "saytwice")
	for _, text := range []string{"first", "second"} {
		if r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/say", map[string]any{"text": text}); r.status != 200 {
			t.Fatalf("POST say %q: %d %s", text, r.status, r.raw)
		}
	}
	rows, err := a.h.Pool.Query(a.h.Ctx, "select command_id from says where project_id = $1", a.p.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("two messages share command_id %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 2 {
		t.Fatalf("%d command ids, want 2", len(seen))
	}
}

// TestASecondWaitingSayReplacesTheFirst (I-1144): the machine says a
// message was still waiting; the api moves the row it replaced out of
// `waiting`, so one row per window claims to be waiting.
func TestASecondWaitingSayReplacesTheFirst(t *testing.T) {
	a := newAskEnv(t, "sayreplace")
	a.h.Fake.SetSayAnswer("waiting", "claude")
	if r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/say", map[string]any{"text": "first"}); r.status != 200 || r.body["state"] != "waiting" {
		t.Fatalf("first: %d %s", r.status, r.raw)
	}

	a.h.Fake.SetSayReplaced(true)
	r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/say", map[string]any{"text": "second"})
	if r.status != 200 || r.body["state"] != "waiting" || r.body["replaced"] != true {
		t.Fatalf("second: %d %s", r.status, r.raw)
	}

	var waiting, replaced int
	if err := a.h.Pool.QueryRow(a.h.Ctx,
		"select count(*) filter (where state = 'waiting'), count(*) filter (where state = 'replaced') from says where project_id = $1",
		a.p.ID).Scan(&waiting, &replaced); err != nil {
		t.Fatal(err)
	}
	if waiting != 1 || replaced != 1 {
		t.Fatalf("%d waiting and %d replaced, want 1 and 1", waiting, replaced)
	}
}

// TestSayRefusals: what the sender reads when the message, the machine
// or the agent is not there. Each refusal carries detail.reason, which
// is what the CLI branches on.
func TestSayRefusals(t *testing.T) {
	a := newAskEnv(t, "sayrefuse")
	path := "/projects/" + a.p.ID.String() + "/say"

	t.Run("empty", func(t *testing.T) {
		r := a.do(t, a.tok, "POST", path, map[string]any{"text": "   "})
		if r.status != 400 {
			t.Fatalf("%d %s", r.status, r.raw)
		}
	})

	t.Run("too long", func(t *testing.T) {
		r := a.do(t, a.tok, "POST", path, map[string]any{"text": strings.Repeat("x", (4<<10)+1)})
		if r.status != 400 || reasonOf(r) != "text_too_long" {
			t.Fatalf("%d %s", r.status, r.raw)
		}
		var n int
		_ = a.h.Pool.QueryRow(a.h.Ctx, "select count(*) from says where project_id = $1", a.p.ID).Scan(&n)
		if n != 0 {
			t.Fatalf("%d rows stored for a refused message", n)
		}
	})

	t.Run("no agent", func(t *testing.T) {
		a.h.Fake.SetSayFailure("not_found", "no agent is running on this machine")
		t.Cleanup(func() { a.h.Fake.SetSayFailure("", "") })
		r := a.do(t, a.tok, "POST", path, map[string]any{"text": "hello"})
		if r.status != 409 || reasonOf(r) != "no_agent" {
			t.Fatalf("%d %s", r.status, r.raw)
		}
		var state, errReason string
		if err := a.h.Pool.QueryRow(a.h.Ctx,
			"select state, coalesce(error, '') from says where project_id = $1 order by created_at desc limit 1", a.p.ID).
			Scan(&state, &errReason); err != nil {
			t.Fatal(err)
		}
		if state != "failed" || errReason != "not_found" {
			t.Fatalf("row = %s / %q, want failed", state, errReason)
		}
	})

	t.Run("several agents names them", func(t *testing.T) {
		a.h.Fake.SetSayFailure("invalid_argument", "2 agents are running: claude, codex. Name one with --agent")
		t.Cleanup(func() { a.h.Fake.SetSayFailure("", "") })
		r := a.do(t, a.tok, "POST", path, map[string]any{"text": "hello"})
		if r.status != 400 || reasonOf(r) != "several_agents" {
			t.Fatalf("%d %s", r.status, r.raw)
		}
		if msg := errMessage(r); !strings.Contains(msg, "claude, codex") {
			t.Fatalf("the refusal lost the windows: %q", msg)
		}
	})

	t.Run("a stopped machine", func(t *testing.T) {
		stopProject(t, a)
		r := a.do(t, a.tok, "POST", path, map[string]any{"text": "hello"})
		if r.status != 409 || reasonOf(r) != "not_running" {
			t.Fatalf("%d %s", r.status, r.raw)
		}
	})
}

// TestSayIsTheOwnersOnly: another account's project is not found, as
// every project route answers.
func TestSayIsTheOwnersOnly(t *testing.T) {
	a := newAskEnv(t, "sayowner")
	e := a.env
	other := e.signIn(t, "sub-saystranger", "saystranger")
	r := e.do(t, other, "POST", "/projects/"+a.p.ID.String()+"/say", map[string]any{"text": "hello"})
	if r.status != 404 {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	if n := len(a.h.Fake.Says()); n != 0 {
		t.Fatalf("%d commands sent for a stranger's say", n)
	}
}

func reasonOf(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["detail"].(map[string]any)
	s, _ := d["reason"].(string)
	return s
}

// stopProject moves the project out of running without going through an
// op, which is all this test needs of a stopped machine.
func stopProject(t *testing.T, a *askEnv) {
	t.Helper()
	if _, err := a.h.Pool.Exec(a.h.Ctx, "update projects set state = 'stopped' where id = $1", a.p.ID); err != nil {
		t.Fatal(err)
	}
}

// TestAWaitingSayStopsWaitingWhenTheMachineTypesItIn (I-1144): a
// message stored `waiting` is closed by the machine's own
// `agent_prompted` event, which is the only thing that knows a
// mid-turn message went in at the end of the turn.
func TestAWaitingSayStopsWaitingWhenTheMachineTypesItIn(t *testing.T) {
	a := newAskEnv(t, "saydelivered")
	a.h.Fake.SetSayAnswer("waiting", "claude")
	r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/say", map[string]any{"text": "and squash the fixups"})
	if r.status != 200 || r.body["state"] != "waiting" {
		t.Fatalf("POST say: %d %s", r.status, r.raw)
	}

	a.h.Fake.AgentEvent(&hostdv1.AgentEvent{
		GuestId: a.p.GuestID.String(), Agent: "claude", TmuxWindow: "claude",
		Kind: "agent_prompted", Summary: a.u.Handle,
	})
	a.h.WaitFor("the says row to stop waiting", func() bool {
		var state string
		_ = a.h.Pool.QueryRow(a.h.Ctx, "select state from says where id = $1", r.body["id"]).Scan(&state)
		return state == "delivered"
	})
}
