package httpapi_test

import (
	"testing"

	"github.com/heracraft/repose/internal/api/ops"
)

// TestStartAgentRoute: POST /projects/{id}/agents starts the project's
// default agent, or the one asked for, in an agent op whose result says
// whether its window was already open; refuses what hostd would and a
// machine that is not running (DECISIONS I-1702).
func TestStartAgentRoute(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-agent", "agent")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "agents", "class": "small"})
	id := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create: %+v", op.Error)
	}
	if n := len(e.h.Fake.StartAgents()); n != 0 {
		t.Fatalf("a create without start_agent started %d agents", n)
	}
	// The default agent, claude, already runs in the fake guest.
	r = e.do(t, tok, "POST", "/projects/"+id+"/agents", nil)
	if r.status != 202 || r.body["agent"] != "claude" {
		t.Fatalf("start the default agent: %d %s", r.status, r.raw)
	}
	op := e.waitOp(t, r)
	if op.State != "done" || op.Kind != ops.KindAgent || op.Result["existed"] != true || op.Result["window"] != "claude" {
		t.Fatalf("agent op: %s %+v %v", op.State, op.Error, op.Result)
	}
	r = e.do(t, tok, "POST", "/projects/"+id+"/agents", map[string]any{"agent": "codex"})
	if op := e.waitOp(t, r); op.State != "done" || op.Result["existed"] != false || op.Result["agent"] != "codex" {
		t.Fatalf("start codex: %s %+v %v", op.State, op.Error, op.Result)
	}
	r = e.do(t, tok, "POST", "/projects/"+id+"/agents", map[string]any{"agent": "claude", "remote_control": true})
	e.waitOp(t, r)
	got := e.h.Fake.StartAgents()
	if len(got) != 3 || got[2].GetAgent() != "claude" || !got[2].GetRemoteControl() || got[2].GetSlug() != "agents" {
		t.Fatalf("commands %v", got)
	}
	for _, c := range []struct {
		body   map[string]any
		status int
		reason string
	}{
		{map[string]any{"agent": "vim"}, 400, "agent"},
		{map[string]any{"agent": "codex", "remote_control": true}, 400, "remote_control"},
	} {
		if r := e.do(t, tok, "POST", "/projects/"+id+"/agents", c.body); r.status != c.status || detailOf(t, r)["reason"] != c.reason {
			t.Fatalf("%v: %d %s, want %d %s", c.body, r.status, r.raw, c.status, c.reason)
		}
	}
	// A hostd from before StartAgent says so in words a user can act on.
	e.h.Fake.SetStartAgentFailure("invalid_argument", "unknown command")
	r = e.do(t, tok, "POST", "/projects/"+id+"/agents", map[string]any{"agent": "pi"})
	if op := e.waitOp(t, r); op.State != "error" || op.Error["message"] != ops.AgentOldHostMessage {
		t.Fatalf("an old hostd: %s %+v", op.State, op.Error)
	}
	e.h.Fake.SetStartAgentFailure("", "")
	e.waitOp(t, e.do(t, tok, "POST", "/projects/"+id+"/stop", nil))
	if r := e.do(t, tok, "POST", "/projects/"+id+"/agents", nil); r.status != 409 || detailOf(t, r)["reason"] != "not_running" {
		t.Fatalf("a stopped machine: %d %s, want 409 not_running", r.status, r.raw)
	}
}

// TestCreateStartsTheAgent: a create with start_agent ends by starting
// agent_default, and an agent that does not start leaves the machine made
// with the reason in the op's result (DECISIONS I-1702).
func TestCreateStartsTheAgent(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-made", "made")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "made", "class": "small", "agent_default": "opencode", "start_agent": true})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" || op.Result["agent"] != "opencode" || op.Result["existed"] != false {
		t.Fatalf("create with an agent: %s %+v %v", op.State, op.Error, op.Result)
	}
	if got := e.h.Fake.StartAgents(); len(got) != 1 || got[0].GetAgent() != "opencode" {
		t.Fatalf("commands %v", got)
	}
	e.h.Fake.SetStartAgentFailure("no_terminal", "the machine's tmux session did not start within 60 seconds")
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "made-2", "class": "small", "start_agent": true, "remote_control": true})
	op := e.waitOp(t, r)
	if op.State != "done" || op.Result["agent_error"] != "no_terminal: the machine's tmux session did not start within 60 seconds" {
		t.Fatalf("a create whose agent did not start: %s %+v %v", op.State, op.Error, op.Result)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "made-3", "class": "small", "remote_control": true}); r.status != 400 || detailOf(t, r)["reason"] != "remote_control" {
		t.Fatalf("remote_control without start_agent: %d %s", r.status, r.raw)
	}
}
