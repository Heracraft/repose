package guestd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sample"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// serveHerdr answers like herdr 0.9.3: one request line, one answer line
// per connection. agentList is the agent_list result.
func serveHerdr(t *testing.T, agentList string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "herdr.sock")
	serveHerdrAt(t, path, agentList)
	return path
}

// serveHerdrAt is serveHerdr at a given path. It returns the count of
// requests answered.
func serveHerdrAt(t *testing.T, path, agentList string) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() //nolint:errcheck // one answer per connection
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct{ ID, Method string }
				_ = json.Unmarshal(line, &req)
				n.Add(1)
				res := `{"type":"pong","version":"0.9.3","protocol":22}`
				if req.Method == "agent.list" {
					res = agentList
				}
				_, _ = fmt.Fprintf(c, `{"id":%q,"result":%s}`+"\n", req.ID, res)
			}(c)
		}
	}()
	return &n
}

// DECISIONS I-506 end to end: a hook from a herdr pane (window
// "herdr:w2:p1") sets the state of the agent in that pane, here
// "claude (herdr)" beside a tmux window named claude, which stays as it
// was. A pane no agent is in, and an over-long pane id, are relayed under
// the agent's name and change no state.
func TestHerdrHookWindows(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		mustMkdir(t, filepath.Dir(h.paths.ProjectJSON()))
		mustWrite(t, h.paths.ProjectJSON(), `{"slug":"todo-app","multiplexer":"herdr"}`)
		writeFakeProc(t, h.paths, 100, 1, "bash")
		writeFakeProc(t, h.paths, 101, 100, "claude")
		h.runner.Match["list-windows"] = sysdep.RunResult{Stdout: []byte("claude\t101\tclaude\t0\n")}
		h.herdrSock = serveHerdr(t, `{"type":"agent_list","agents":[`+
			`{"pane_id":"w2:p1","workspace_id":"w2","name":"claude","agent":"claude","agent_status":"working","state_change_seq":1,"cwd":"/home/dev/todo-app"}]}`)
	})
	ctx := context.Background()
	states := func() map[string]string {
		t.Helper()
		h.srv.watcher.Refresh(ctx)
		sig, _ := h.srv.watcher.Signals()
		out := map[string]string{}
		for _, a := range sig.GetAgents() {
			out[a.GetTmuxWindow()] = a.GetState()
		}
		return out
	}
	before := states()
	if before["claude (herdr)"] != sample.StateWorking || before["claude"] == sample.StateNeedsInput {
		t.Fatalf("agents before any hook = %v", before)
	}
	events := func() []string {
		var out []string
		for _, n := range h.seen() {
			if ev := n.GetAgentEvent(); ev != nil {
				out = append(out, ev.GetTmuxWindow()+"|"+ev.GetKind())
			}
		}
		return out
	}

	for _, w := range []string{"herdr:nope", "herdr:" + strings.Repeat("p", 60), "herdr:"} {
		postHook(t, h.srv.HookSocketPath(), `{"agent":"claude","kind":"needs_input","summary":"Allow Bash?","window":"`+w+`"}`)
	}
	h.waitForNotify(t, "three AgentEvents", func(*guestdv1.Notify) bool { return len(events()) == 3 })
	if got := strings.Join(events(), " "); got != "claude|needs_input claude|needs_input claude|needs_input" {
		t.Fatalf("events = %s, want each relayed under the agent's name", got)
	}
	if got := states(); got["claude"] == sample.StateNeedsInput || got["claude (herdr)"] != sample.StateWorking {
		t.Fatalf("an unresolved herdr window changed a state: %v", got)
	}

	postHook(t, h.srv.HookSocketPath(), `{"agent":"claude","kind":"needs_input","summary":"Allow Bash?","window":"herdr:w2:p1"}`)
	h.waitForNotify(t, "the resolved AgentEvent", func(*guestdv1.Notify) bool { return len(events()) == 4 })
	if got := events()[3]; got != "claude (herdr)|needs_input" {
		t.Fatalf("event = %s, want it under the herdr agent's key", got)
	}
	got := states()
	if got["claude (herdr)"] != sample.StateNeedsInput {
		t.Fatalf("agents = %v, want claude (herdr) needs_input", got)
	}
	if got["claude"] == sample.StateNeedsInput {
		t.Fatalf("agents = %v: the herdr pane's needs_input reached the tmux window claude", got)
	}
}

// A herdr project whose herdr never answers warns herdr_down, not
// tmux_down, though no tmux runs either. The warning waits for this
// boot's SetupProject, which is what starts the herdr server (I-562).
func TestHerdrProjectWarnsHerdrDown(t *testing.T) {
	const pj = `{"slug":"todo-app","multiplexer":"herdr"}`
	h := newHarness(t, func(h *harness) {
		mustMkdir(t, filepath.Dir(h.paths.ProjectJSON()))
		mustWrite(t, h.paths.ProjectJSON(), pj)
		h.runner.Match["list-windows"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("no server running on /tmp/tmux-1000/default")}
	})
	for i := 0; i < 3; i++ {
		h.srv.watcher.Refresh(context.Background())
	}
	for _, n := range h.seen() {
		if n.GetWarning() != nil {
			t.Fatalf("warning %q before SetupProject", n.GetWarning().GetKind())
		}
	}
	if err := h.srv.project.Setup(context.Background(), &guestdv1.SetupProject{ProjectSlug: "todo-app", ProjectJson: []byte(pj)}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	for i := 0; i < 3; i++ {
		h.srv.watcher.Refresh(context.Background())
	}
	h.waitForNotify(t, "herdr_down", func(n *guestdv1.Notify) bool { return n.GetWarning().GetKind() == sample.WarnHerdrDown })
	for _, n := range h.seen() {
		if n.GetWarning().GetKind() == sample.WarnTmuxDown {
			t.Fatal("tmux_down on a herdr project")
		}
	}
}
