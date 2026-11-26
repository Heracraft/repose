package guestd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sample"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// writeStartProc is writeFakeProc with a start time (stat field 22).
func writeStartProc(t *testing.T, p sysdep.Paths, pid, ppid int, comm, start string) {
	t.Helper()
	dir := p.ProcPID(itoa(pid))
	mustMkdir(t, dir)
	fields := make([]string, 52)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = itoa(ppid)
	fields[18] = start
	mustWrite(t, filepath.Join(dir, "stat"), itoa(pid)+" ("+comm+") S "+strings.Join(fields, " ")+"\n")
}

// TestDesktopSessionIsListed is ENG-255 end to end through the hook
// socket: Claude Desktop's CLI, outside tmux and named for its version,
// starts a session, takes a prompt and finishes it. The Agents list
// shows it idle, working, idle; the Stop is relayed with its agent's opaque id;
// its SessionEnd takes it off the list. On main before I-1676 the list
// stayed empty throughout.
func TestDesktopSessionIsListed(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		mustMkdir(t, filepath.Dir(h.paths.ProjectJSON()))
		mustWrite(t, h.paths.ProjectJSON(), `{"slug":"todo-app"}`)
		// The hook is this test process; above it sh, Desktop's CLI and
		// its daemonised server.
		writeStartProc(t, h.paths, os.Getpid(), 400, "repose-hook", "900")
		writeStartProc(t, h.paths, 400, 300, "sh", "850")
		writeStartProc(t, h.paths, 300, 200, "2.1.330-3cc8a86", "777")
		writeStartProc(t, h.paths, 200, 1, "server", "700")
	})
	ctx := context.Background()
	listed := func() map[string]string {
		t.Helper()
		h.srv.watcher.Refresh(ctx)
		sig, _ := h.srv.watcher.Signals()
		out := map[string]string{}
		for _, a := range sig.GetAgents() {
			out[a.GetAgent()+"@"+a.GetAgentId()+"/"+a.GetTmuxWindow()] = a.GetState()
		}
		return out
	}
	key := "claude@" + h.srv.watcher.WireID("300:777") + "/"
	sock := h.srv.HookSocketPath()
	postHook(t, sock, `{"agent":"claude","kind":"session_start","session_id":"08b87fc9-c37a-4bc4-b0af-a55bc5dfc98a"}`)
	if got := listed(); got[key] != sample.StateIdle || len(got) != 1 {
		t.Fatalf("after SessionStart, agents = %v, want %s idle", got, key)
	}
	postHook(t, sock, `{"agent":"claude","kind":"prompt","summary":"secret"}`)
	if got := listed(); got[key] != sample.StateWorking {
		t.Fatalf("after the prompt, agents = %v, want working", got)
	}
	postHook(t, sock, `{"agent":"claude","kind":"completed","summary":"tests pass"}`)
	if got := listed(); got[key] != sample.StateIdle {
		t.Fatalf("after the Stop, agents = %v, want idle", got)
	}
	n := h.waitForNotify(t, "AgentEvent", func(n *guestdv1.Notify) bool { return n.GetAgentEvent() != nil })
	if ev := n.GetAgentEvent(); ev.GetAgentId() != h.srv.watcher.WireID("300:777") || ev.GetTmuxWindow() != "claude" || ev.GetKind() != "completed" {
		t.Fatalf("event = %+v, want it under the agent's name with its id", ev)
	}
	postHook(t, sock, `{"agent":"claude","kind":"session_end"}`)
	if got := listed(); len(got) != 0 {
		t.Fatalf("after SessionEnd, agents = %v, want none", got)
	}
}
