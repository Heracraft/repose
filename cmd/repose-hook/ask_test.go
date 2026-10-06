package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/guestd/hooks"
	"github.com/heracraft/repose/internal/guestd/questions"
)

// guestSide is the real guest half behind the commands: guestd's hook
// socket with the ask routes over a questions store, recording what it
// would send hostd.
type guestSide struct {
	socket string
	store  *questions.Store
	srv    *hooks.Server
	dir    string

	mu       sync.Mutex
	messages []string
	asked    []questions.Question
	// herdr resolves herdr pane ids as guestd's watcher would.
	herdr hooks.HerdrResolver
}

func newGuestSide(t *testing.T) *guestSide {
	t.Helper()
	g := &guestSide{socket: filepath.Join(t.TempDir(), "hooks.sock"), dir: t.TempDir()}
	g.start(t)
	t.Cleanup(func() { _ = g.srv.Close(context.Background()) })
	return g
}

// restart stops guestd and starts it again over the same question
// directory (a guestd restart) or an empty one (a reboot).
func (g *guestSide) restart(t *testing.T, reboot bool) {
	t.Helper()
	_ = g.srv.Close(context.Background())
	time.Sleep(200 * time.Millisecond)
	if reboot {
		g.dir = t.TempDir()
	}
	g.start(t)
}

func (g *guestSide) start(t *testing.T) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	g.store = questions.New(g.dir, func(q questions.Question) {
		g.mu.Lock()
		g.asked = append(g.asked, q)
		g.mu.Unlock()
	}, log, nil)
	srv := hooks.NewServer(g.socket, -1, func(string, string, string, string) {}, nil, log)
	srv.SetHerdrResolver(g.herdr)
	srv.EnableAsk(g.store, func(agent, window, kind, summary string) {
		g.mu.Lock()
		g.messages = append(g.messages, strings.Join([]string{agent, window, kind, summary}, "|"))
		g.mu.Unlock()
	})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	g.srv = srv
}

// open waits for the ask to reach guestd and returns it.
func (g *guestSide) open(t *testing.T) questions.Question {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := g.store.Pending(); len(p) > 0 {
			return p[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the ask never reached guestd")
	return questions.Question{}
}

func withEnv(t *testing.T, kv ...string) {
	t.Helper()
	for i := 0; i < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
}

func TestIsSubPicksTheModeFromTheNameOrTheFirstWord(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"/run/current-system/sw/bin/repose-notify", "hi"}, "notify"},
		{[]string{"repose-ask", "q"}, "ask"},
		{[]string{"repose-hook", "ask", "q"}, "ask"},
		{[]string{"repose-hook", "notify"}, "notify"},
		{[]string{"repose-hook", `{"hook_event_name":"Stop"}`}, ""},
		{[]string{"repose-hook"}, ""},
	} {
		if got, _ := isSub(c.argv); got != c.want {
			t.Errorf("%v: %q, want %q", c.argv, got, c.want)
		}
	}
}

func TestParseSubTakesFlagsAnywhere(t *testing.T) {
	withEnv(t, "REPOSE_HOOK_AGENT", "codex")
	o, err := parseSub([]string{"ok", "to", "push?", "--options", "yes, no", "--timeout=90", "--", "--literally"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.rest, " ") != "ok to push? --literally" || strings.Join(o.options, "|") != "yes|no" || o.timeout != 90*time.Second || o.agent != "codex" {
		t.Fatalf("parsed %+v", o)
	}
	if _, err := parseSub([]string{"--options", "a"}, false); err == nil {
		t.Fatal("repose-notify accepted --options")
	}
	for _, bad := range []string{"soon", "0", "25h"} {
		if _, err := parseSub([]string{"--timeout", bad, "q"}, true); err == nil {
			t.Errorf("--timeout %s accepted", bad)
		}
	}
}

func TestAgentInAncestryReadsCommOnly(t *testing.T) {
	proc := t.TempDir()
	write := func(pid, comm, stat string) {
		d := filepath.Join(proc, pid)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644)
	}
	write("40", "bash", "40 (bash) S 30 40 40 0")
	write("30", ".claude-wrapped", "30 (.claude-wrapped) S 20 30 30 0")
	write("20", "tmux: server", "20 (tmux: server) S 1 20 20 0")
	if got := agentInAncestry(proc, 40); got != "claude" {
		t.Fatalf("agent = %q", got)
	}
	if got := agentInAncestry(proc, 20); got != "" {
		t.Fatalf("a shell outside any agent = %q", got)
	}
}

func TestNotifyPostsTheMessage(t *testing.T) {
	g := newGuestSide(t)
	withEnv(t, "REPOSE_HOOK_SOCKET", g.socket, "REPOSE_HOOK_AGENT", "claude", "REPOSE_AGENT_WINDOW", "claude")
	var out, errb bytes.Buffer
	if code := runNotify([]string{"deploy", "is", "green"}, strings.NewReader(""), &out, &errb); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if code := runNotify(nil, strings.NewReader("from stdin\n"), &out, &errb); code != ExitOK {
		t.Fatalf("stdin: exit %d: %s", code, errb.String())
	}
	g.mu.Lock()
	got := strings.Join(g.messages, "\n")
	g.mu.Unlock()
	if got != "claude|claude|agent_message|deploy is green\nclaude|claude|agent_message|from stdin" {
		t.Fatalf("messages:\n%s", got)
	}
	if code := runNotify(nil, strings.NewReader("  "), &out, &errb); code != ExitUsage {
		t.Fatalf("empty message: exit %d", code)
	}
	withEnv(t, "REPOSE_HOOK_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	if code := runNotify([]string{"x"}, strings.NewReader(""), &out, &errb); code != ExitError {
		t.Fatalf("no guestd: exit %d", code)
	}
}

// repose-notify in a herdr pane sends the pane as its window, so guestd
// attributes the message to the agent in it (DECISIONS I-506).
// An unresolved pane goes under the agent's name.
func TestNotifyNamesTheHerdrPane(t *testing.T) {
	g := &guestSide{socket: filepath.Join(t.TempDir(), "hooks.sock"), dir: t.TempDir()}
	g.herdr = func(_ context.Context, ref string) (string, bool) {
		if ref == "w1:p3" {
			return "claude-2", true
		}
		return "", false
	}
	g.start(t)
	t.Cleanup(func() { _ = g.srv.Close(context.Background()) })
	withEnv(t, "REPOSE_HOOK_SOCKET", g.socket, "REPOSE_HOOK_AGENT", "claude", "REPOSE_AGENT_WINDOW", "",
		"TMUX_PANE", "", "HERDR_ENV", "1", "HERDR_PANE_ID", "w1:p3")
	var out, errb bytes.Buffer
	if code := runNotify([]string{"hi"}, strings.NewReader(""), &out, &errb); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	withEnv(t, "HERDR_PANE_ID", "w9:p9")
	if code := runNotify([]string{"there"}, strings.NewReader(""), &out, &errb); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	g.mu.Lock()
	got := strings.Join(g.messages, "\n")
	g.mu.Unlock()
	if got != "claude|claude-2|agent_message|hi\nclaude|claude|agent_message|there" {
		t.Fatalf("messages:\n%s", got)
	}
}

// ask runs repose-ask in the background and returns its result channel.
func ask(args []string, sigs chan os.Signal) (<-chan int, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runAsk(args, strings.NewReader(""), &out, &errb, sigs) }()
	return done, &out, &errb
}

func waitExit(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case c := <-done:
		return c
	case <-time.After(15 * time.Second):
		t.Fatal("repose-ask did not return")
		return -1
	}
}

func TestAskExitCodes(t *testing.T) {
	old := pollWait
	pollWait = 1
	t.Cleanup(func() { pollWait = old })
	g := newGuestSide(t)
	withEnv(t, "REPOSE_HOOK_SOCKET", g.socket, "REPOSE_HOOK_AGENT", "", "REPOSE_AGENT", "")

	t.Run("answered prints the answer", func(t *testing.T) {
		done, out, errb := ask([]string{"Drop the legacy table?", "--options", "yes,no"}, make(chan os.Signal))
		q := g.open(t)
		if q.Text != "Drop the legacy table?" || strings.Join(q.Options, ",") != "yes,no" {
			t.Fatalf("asked %+v", q)
		}
		if err := g.store.Answer(q.ID, questions.StateAnswered, "yes"); err != nil {
			t.Fatal(err)
		}
		if c := waitExit(t, done); c != ExitOK || out.String() != "yes\n" {
			t.Fatalf("exit %d stdout %q stderr %q", c, out, errb)
		}
	})
	for _, c := range []struct {
		status string
		want   int
	}{
		{questions.StateExpired, ExitTimeout},
		{questions.StateNoChannel, ExitNoChannel},
		{questions.StateCancelled, ExitCancelled},
	} {
		t.Run(c.status, func(t *testing.T) {
			done, out, _ := ask([]string{"q?"}, make(chan os.Signal))
			q := g.open(t)
			_ = g.store.Answer(q.ID, c.status, "")
			if got := waitExit(t, done); got != c.want || out.Len() != 0 {
				t.Fatalf("exit %d stdout %q, want %d", got, out, c.want)
			}
		})
	}
	t.Run("a guestd restart keeps the question", func(t *testing.T) {
		done, out, _ := ask([]string{"q?"}, make(chan os.Signal))
		q := g.open(t)
		g.restart(t, false)
		if err := g.store.Answer(q.ID, questions.StateAnswered, "after the restart"); err != nil {
			t.Fatalf("the restarted guestd does not know the question: %v", err)
		}
		if got := waitExit(t, done); got != ExitOK || out.String() != "after the restart\n" {
			t.Fatalf("exit %d stdout %q", got, out)
		}
	})
	t.Run("a guest that forgot the question", func(t *testing.T) {
		done, _, errb := ask([]string{"q?"}, make(chan os.Signal))
		g.open(t)
		// A reboot empties the tmpfs: the new guestd does not know the id.
		g.restart(t, true)
		if got := waitExit(t, done); got != ExitCancelled || !strings.Contains(errb.String(), "gone") {
			t.Fatalf("exit %d stderr %q", got, errb)
		}
	})
	t.Run("interrupt cancels the question", func(t *testing.T) {
		sigs := make(chan os.Signal, 1)
		done, _, _ := ask([]string{"q?"}, sigs)
		q := g.open(t)
		sigs <- syscall.SIGINT
		if got := waitExit(t, done); got != ExitInterrupted {
			t.Fatalf("exit %d", got)
		}
		if got, _ := g.store.Get(q.ID); got.State != questions.StateCancelled {
			t.Fatalf("question after ^C: %+v", got)
		}
	})
	t.Run("usage", func(t *testing.T) {
		for _, args := range [][]string{{}, {"q", "--options", "a,b,c,d"}, {"q", "--timeout", "never"}} {
			done, _, _ := ask(args, make(chan os.Signal))
			if got := waitExit(t, done); got != ExitUsage {
				t.Errorf("%v: exit %d", args, got)
			}
		}
	})
}

// TestAskBinaryUnderItsOwnName runs the built binary under the name
// repose-ask, as the guest base installs it, against a guestd whose
// question times out.
func TestAskBinaryUnderItsOwnName(t *testing.T) {
	g := newGuestSide(t)
	bin := buildHook(t)
	link := filepath.Join(t.TempDir(), "repose-ask")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(link, "--timeout", "1s", "still there?")
	cmd.Env = append(os.Environ(), "REPOSE_HOOK_SOCKET="+g.socket)
	go func() {
		for i := 0; i < 400; i++ {
			g.store.Sweep()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if err == nil || !errorsAs(err, &ee) || ee.ExitCode() != ExitTimeout {
		t.Fatalf("exit %v, want %d; output %s", err, ExitTimeout, out)
	}
	if !strings.Contains(string(out), "no answer before the timeout") {
		t.Fatalf("output %s", out)
	}
}

func errorsAs(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError) //nolint:errorlint // exec returns it unwrapped
	if ok {
		*target = e
	}
	return ok
}
