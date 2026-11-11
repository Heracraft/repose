package guestd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// I-1144: a SayToAgent over the wire types the message into the agent's
// own tmux window and answers `delivered` with the window it used. The
// guest's log lines carry the window and the state, never the text.
func TestSayToAgentOverTheWire(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.runner.Match["list-windows"] = sysdep.RunResult{Stdout: []byte("claude\t101\tclaude\t0\n")}
	})
	writeSayProcs(t, h)
	if err := h.srv.project.Setup(t.Context(), &guestdv1.SetupProject{ProjectSlug: "todo-app"}); err != nil {
		t.Fatal(err)
	}
	h.srv.watcher.Refresh(t.Context())

	resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_SayToAgent{SayToAgent: &guestdv1.SayToAgent{
		SayId: "0199aaaa-0000-7000-8000-00000000001a", Text: "run the tests before you commit",
		Sender: "heracraft", FromOwner: true,
	}}})
	if !resp.GetOk() {
		t.Fatalf("error: %+v", resp.GetError())
	}
	got := resp.GetSay()
	if got.GetState() != "delivered" || got.GetWindow() != "claude" || got.GetReplaced() {
		t.Fatalf("say result = %+v, want delivered in claude", got)
	}

	var typed string
	for _, c := range h.runner.Calls() {
		if len(c.Argv) == 3 && c.Argv[0] == "sh" && strings.Contains(c.Argv[2], "repose_pp=") {
			typed = c.Argv[2]
			if c.User != "dev" {
				t.Fatalf("the submit script ran as %q, want dev", c.User)
			}
		}
	}
	if !strings.Contains(typed, "repose_pp='run the tests before you commit'") {
		t.Fatalf("the message was not typed in:\n%s", typed)
	}
	if !strings.Contains(typed, "tmux send-keys -t '=todo-app:=claude' Enter") {
		t.Fatalf("no Enter into the agent's window:\n%s", typed)
	}
}

// I-1144: a guest with no agent refuses rather than typing into
// whatever window is there.
func TestSayWithNoAgentIsNotFound(t *testing.T) {
	h := newHarness(t)
	resp := h.do(t, &guestdv1.Request{Req: &guestdv1.Request_SayToAgent{SayToAgent: &guestdv1.SayToAgent{
		SayId: "0199aaaa-0000-7000-8000-00000000001b", Text: "hello", FromOwner: true,
	}}})
	if resp.GetOk() || resp.GetError().GetCode() != "not_found" {
		t.Fatalf("resp = %+v, want not_found", resp)
	}
}

// writeSayProcs gives the harness a claude whose pane pid is 101, which
// is what makes the window an agent window.
func writeSayProcs(t *testing.T, h *harness) {
	t.Helper()
	for _, pr := range []struct {
		pid, ppid int
		comm      string
	}{{100, 1, "bash"}, {101, 100, "claude"}} {
		dir := h.paths.ProcPID(strconv.Itoa(pr.pid))
		mustMkdir(t, dir)
		fields := make([]string, 52)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0] = strconv.Itoa(pr.ppid)
		fields[10] = "50"
		mustWrite(t, filepath.Join(dir, "stat"),
			fmt.Sprintf("%d (%s) S %s\n", pr.pid, pr.comm, strings.Join(fields, " ")))
		mustWrite(t, filepath.Join(dir, "comm"), pr.comm+"\n")
	}
}
