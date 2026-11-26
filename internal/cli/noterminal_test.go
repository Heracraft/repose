package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An agent outside any terminal repose can see (Claude Desktop's,
// DECISIONS I-1676): ps lists it after the windows with no name and no
// command, -q leaves it out, status and ls name it, and attach -w with
// its agent's name says where it runs.
func TestNoTerminalAgentInTheLists(t *testing.T) {
	now := time.Unix(1000000, 0)
	fresh := now.Add(-30 * time.Second)
	l, err := parsePs("1000000\n#home /home/dev\n#co /home/dev/todo-app\n" +
		"0\tshell\tbash\t999990\t1\t\t/home/dev/todo-app\n")
	if err != nil {
		t.Fatal(err)
	}
	sig := &Signals{SampledAt: &fresh, Agents: []AgentSignal{{Agent: "claude", State: "working", AgentID: "501:4100"}}}
	rows := psRowsTmux(l, sig, now)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want the window and the agent", len(rows))
	}
	r := rows[1]
	if r.Name != "" || r.Command != nil || r.Agent == nil || *r.Agent != "claude" || *r.AgentState != "working" || r.AgentID != "501:4100" || r.Terminal != "" || r.Index != nil {
		t.Fatalf("row = %+v", r)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"agent_id":"501:4100"`) || !strings.Contains(string(b), `"command":null`) {
		t.Fatalf("json = %s", b)
	}
	stale := now.Add(-5 * time.Minute)
	sig.SampledAt = &stale
	if got := psRowsTmux(l, sig, now); len(got) != 1 {
		t.Fatalf("a stale sample listed it: %d rows", len(got))
	}

	recent := time.Now().Add(-30 * time.Second)
	p := &Project{Slug: "todo-app", State: "running", Signals: &Signals{SampledAt: &recent, Agents: []AgentSignal{
		{Agent: "claude", Window: "claude", State: "idle", AgentID: "101:11", Terminal: "tmux:claude"},
		{Agent: "claude", State: "working", AgentID: "501:4100"},
	}}}
	if got := agentList(p); got != "claude (no terminal) working, claude idle" {
		t.Fatalf("status row %q", got)
	}
	err = noTerminalAttach(&Project{Slug: "todo-app", Signals: &Signals{Agents: p.Signals.Agents[1:]}}, "claude")
	if err == nil || err.Error() != "claude runs outside tmux on todo-app; open it where it was started." {
		t.Fatalf("attach -w claude = %v", err)
	}
	if err := noTerminalAttach(p, "claude"); err != nil {
		t.Fatalf("a window named claude was refused: %v", err)
	}
	if err := noTerminalAttach(p, "codex"); err != nil {
		t.Fatalf("an unlisted name was refused: %v", err)
	}
}
