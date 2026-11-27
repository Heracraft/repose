package hooks

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/heracraft/repose/internal/guestd/questions"
)

// desktopProc writes a /proc tree shaped like Claude Desktop's on
// shule-id (ENG-255): self (the hook) -> 400 sh -> 300 "2.1.330-3cc8a86"
// (Desktop's version-named CLI) -> 200 server -> 1.
func desktopProc(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(pid int, comm string, ppid, start int) {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := fmt.Sprintf("%d (%s) S %d 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 %d 0 0\n", pid, comm, ppid, start)
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(os.Getpid(), "repose-hook", 400, 900)
	write(400, "sh", 300, 850)
	write(300, "2.1.330-3cc8a86", 200, 777)
	write(200, "server", 1, 700)
	return root
}

func TestAgentOrAncestorFindsDesktopsCLI(t *testing.T) {
	root := desktopProc(t)
	if got := agentOrAncestor(root, os.Getpid(), "claude"); got != "300:777" {
		t.Fatalf("agentOrAncestor = %q, want Desktop's CLI 300:777", got)
	}
	// The binary walk still wins where it finds the agent.
	if got := agentOrAncestor(fakeProc(t), os.Getpid(), "claude"); got != "300:777" {
		t.Fatalf("a nix claude = %q", got)
	}
	if got := agentOrAncestor(root, os.Getpid(), "aider"); got != "" {
		t.Fatalf("an unknown agent = %q, want none", got)
	}
}

func TestAgentOrAncestorStopsAtALoginOrAService(t *testing.T) {
	root := desktopProc(t)
	for _, comm := range []string{"sshd-session", "systemd", "tmux: server", "guestd"} {
		stat := fmt.Sprintf("300 (%s) S 200 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 777 0 0\n", comm)
		if err := os.WriteFile(filepath.Join(root, "300", "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := agentOrAncestor(root, os.Getpid(), "claude"); got != "" {
			t.Errorf("a hook run from a shell under %q = %q, want no agent", comm, got)
		}
	}
}

type agentRecorder struct {
	mu   sync.Mutex
	rows []AgentHook
}

func (a *agentRecorder) sink(h AgentHook) { a.mu.Lock(); a.rows = append(a.rows, h); a.mu.Unlock() }
func (a *agentRecorder) all() []AgentHook {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AgentHook(nil), a.rows...)
}

// TestWindowlessHooksGoToTheAgentSink (I-1676): a hook from no tmux pane
// goes to the agent sink with its process, session id and inbox; the
// prompt's text and a session hook's summary are dropped; a prompt
// closes the questions asked from that process. Nothing reaches the old
// sink.
func TestWindowlessHooksGoToTheAgentSink(t *testing.T) {
	s, rec, c := newHookServer(t, nil)
	s.SetProcRoot(desktopProc(t))
	ar := &agentRecorder{}
	s.SetAgentSink(ar.sink)
	store := questions.New(t.TempDir(), func(questions.Question) {}, quietLog(), nil)
	s.EnableAsk(store, rec.sink)
	mine, _ := store.Open("claude", "", "300:777", "q", nil, 0)

	for _, body := range []string{
		`{"agent":"claude","kind":"session_start","summary":"x","session_id":"08b87fc9-c37a-4bc4-b0af-a55bc5dfc98a"}`,
		`{"agent":"claude","kind":"prompt","summary":"secret prompt text","inbox":"/tmp/cc/inbox.sock"}`,
		`{"agent":"claude","kind":"completed","summary":"done"}`,
		`{"agent":"claude","kind":"session_end"}`,
	} {
		if resp := post(t, c, body); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: status = %d", body, resp.StatusCode)
		}
	}
	got := ar.all()
	want := []AgentHook{
		{Agent: "claude", Kind: KindSessionStart, Proc: "300:777", SessionID: "08b87fc9-c37a-4bc4-b0af-a55bc5dfc98a"},
		{Agent: "claude", Kind: KindPrompt, Proc: "300:777", Inbox: "/tmp/cc/inbox.sock"},
		{Agent: "claude", Kind: "completed", Proc: "300:777", Summary: "done"},
		{Agent: "claude", Kind: KindSessionEnd, Proc: "300:777"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("agent sink =\n %v\nwant\n %v", got, want)
	}
	if q, _ := store.Get(mine.ID); q.Open() {
		t.Fatalf("the prompt left its process's question open: %+v", q)
	}
	if rows := rec.all(); len(rows) != 0 {
		t.Fatalf("the old sink got %v", rows)
	}
}

// TestWindowedSessionHooksChangeNothing: a session hook with a window is
// answered and goes nowhere, and a window's completion is unchanged.
func TestWindowedSessionHooksChangeNothing(t *testing.T) {
	s, rec, c := newHookServer(t, nil)
	s.SetProcRoot(desktopProc(t))
	ar := &agentRecorder{}
	s.SetAgentSink(ar.sink)
	post(t, c, `{"agent":"claude","kind":"session_start","window":"claude"}`)
	post(t, c, `{"agent":"claude","kind":"completed","summary":"done","window":"claude"}`)
	if got := rec.all(); len(got) != 1 || got[0] != "claude|claude|completed|done" {
		t.Fatalf("old sink = %v", got)
	}
	if got := ar.all(); len(got) != 0 {
		t.Fatalf("agent sink = %v, want nothing from a window", got)
	}
}
