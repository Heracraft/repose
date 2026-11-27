package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// hookSink is a stand-in for guestd's hook socket that records what arrived.
type hookSink struct {
	path string

	mu     sync.Mutex
	rows   []string
	bodies []map[string]string
}

func newHookSink(t *testing.T) *hookSink {
	t.Helper()
	s := &hookSink{path: filepath.Join(t.TempDir(), "hooks.sock")}
	l, err := net.Listen("unix", s.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			s.rows = append(s.rows, body["agent"]+"|"+body["kind"]+"|"+body["summary"]+"|"+body["window"])
			s.bodies = append(s.bodies, body)
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return s
}

func (s *hookSink) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.rows...)
}

func buildHook(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "repose-hook")
	cmd := exec.Command("go", "build", "-o", out, "github.com/heracraft/repose/cmd/repose-hook")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	return out
}

// runHook runs the binary and returns its exit code, which must always be 0.
func runHook(t *testing.T, bin string, stdin string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	// The pane the test runs in is not the hook's: clear it, and let
	// env set it.
	cmd.Env = append(os.Environ(), "TMUX_PANE=", "HERDR_ENV=", "HERDR_PANE_ID=", "REPOSE_AGENT_WINDOW=")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return code, string(out)
}

func TestHookPostsAClaudeStop(t *testing.T) {
	sink := newHookSink(t)
	bin := buildHook(t)

	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	body := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"All 14 tests pass."}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"Stop","transcript_path":"` + transcript + `"}`

	code, out := runHook(t, bin, payload, []string{"REPOSE_HOOK_SOCKET=" + sink.path}, "--agent", "claude")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output: %s", code, out)
	}
	got := sink.all()
	if len(got) != 1 || got[0] != "claude|completed|All 14 tests pass.|" {
		t.Fatalf("posted = %v", got)
	}
}

func TestHookTakesCodexPayloadFromTheArgument(t *testing.T) {
	sink := newHookSink(t)
	bin := buildHook(t)
	payload := `{"type":"agent-turn-complete","last-assistant-message":"auth flow done"}`

	code, out := runHook(t, bin, "", []string{"REPOSE_HOOK_SOCKET=" + sink.path}, "--agent", "codex", payload)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output: %s", code, out)
	}
	got := sink.all()
	if len(got) != 1 || !strings.HasPrefix(got[0], "codex|completed|auth flow done") {
		t.Fatalf("posted = %v", got)
	}
}

func TestHookAlwaysExitsZero(t *testing.T) {
	bin := buildHook(t)
	// Every way this can go wrong, in one table. A non-zero exit from any of
	// them would block the agent that called it.
	cases := []struct {
		name  string
		stdin string
		env   []string
		args  []string
	}{
		{"no socket", `{"hook_event_name":"Stop"}`, []string{"REPOSE_HOOK_SOCKET=/nonexistent/hooks.sock"}, []string{"--agent", "claude"}},
		{"no agent", `{"hook_event_name":"Stop"}`, []string{"REPOSE_AGENT=", "REPOSE_HOOK_AGENT="}, nil},
		{"unknown agent", `{}`, nil, []string{"--agent", "aider"}},
		{"malformed payload", `{not json`, nil, []string{"--agent", "claude"}},
		{"empty payload", "", nil, []string{"--agent", "claude"}},
		{"unreportable hook", `{"hook_event_name":"PreToolUse"}`, nil, []string{"--agent", "claude"}},
		{"bad flag", "", nil, []string{"--nonsense"}},
	}
	for _, c := range cases {
		code, out := runHook(t, bin, c.stdin, c.env, c.args...)
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0; output: %s", c.name, code, out)
		}
	}
}

// TestHookReadsTheAgentFromTheEnvironment covers both names: the wrappers in
// nix/overlay/agents export REPOSE_HOOK_AGENT, which is what
// docs/interfaces/guest-conventions.md documents, and REPOSE_AGENT is
// accepted for one release (DECISIONS I-58). Every hook in every guest is
// silent if this is wrong, and silence is what it looks like when it works.
func TestHookReadsTheAgentFromTheEnvironment(t *testing.T) {
	for _, env := range []string{"REPOSE_HOOK_AGENT=claude", "REPOSE_AGENT=claude"} {
		sink := newHookSink(t)
		bin := buildHook(t)
		code, out := runHook(t, bin, `{"hook_event_name":"Stop"}`,
			[]string{"REPOSE_HOOK_SOCKET=" + sink.path, env})
		if code != 0 {
			t.Fatalf("%s: exit = %d; output: %s", env, code, out)
		}
		if got := sink.all(); len(got) != 1 {
			t.Fatalf("%s: posted = %v", env, got)
		}
	}
}

// TestHookReadsTheSocketFromEitherName: the shell implementation the guest
// shipped before used REPOSE_HOOKS_SOCKET.
func TestHookReadsTheSocketFromEitherName(t *testing.T) {
	sink := newHookSink(t)
	bin := buildHook(t)
	code, out := runHook(t, bin, `{"hook_event_name":"Stop"}`,
		[]string{"REPOSE_HOOKS_SOCKET=" + sink.path, "REPOSE_HOOK_AGENT=claude"})
	if code != 0 {
		t.Fatalf("exit = %d; output: %s", code, out)
	}
	if got := sink.all(); len(got) != 1 {
		t.Fatalf("posted = %v", got)
	}
}

func TestHookPassesTheWindowThrough(t *testing.T) {
	sink := newHookSink(t)
	bin := buildHook(t)
	code, out := runHook(t, bin, `{"hook_event_name":"Stop"}`,
		[]string{"REPOSE_HOOK_SOCKET=" + sink.path}, "--agent", "claude", "--window", "claude-2")
	if code != 0 {
		t.Fatalf("exit = %d; output: %s", code, out)
	}
	got := sink.all()
	if len(got) != 1 || !strings.HasSuffix(got[0], "|claude-2") {
		t.Fatalf("posted = %v, want the window carried through", got)
	}
}

// In a herdr pane (HERDR_ENV=1, no $TMUX_PANE) the hook names the pane as
// "herdr:<HERDR_PANE_ID>" for guestd to resolve (DECISIONS I-506); inside
// tmux, or with REPOSE_AGENT_WINDOW, it does not.
func TestHookNamesTheHerdrPane(t *testing.T) {
	bin := buildHook(t)
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"herdr pane", []string{"HERDR_ENV=1", "HERDR_PANE_ID=w2:p1"}, "|herdr:w2:p1"},
		{"tmux inside herdr", []string{"HERDR_ENV=1", "HERDR_PANE_ID=w2:p1", "TMUX_PANE=%3"}, "|"},
		{"wrapper window wins", []string{"HERDR_ENV=1", "HERDR_PANE_ID=w2:p1", "REPOSE_AGENT_WINDOW=claude-2"}, "|claude-2"},
		{"no HERDR_ENV", []string{"HERDR_PANE_ID=w2:p1"}, "|"},
		{"no pane id", []string{"HERDR_ENV=1"}, "|"},
	}
	for _, c := range cases {
		sink := newHookSink(t)
		env := append([]string{"REPOSE_HOOK_SOCKET=" + sink.path, "REPOSE_HOOK_AGENT=claude"}, c.env...)
		if code, out := runHook(t, bin, `{"hook_event_name":"Stop"}`, env); code != 0 {
			t.Fatalf("%s: exit = %d; output: %s", c.name, code, out)
		}
		got := sink.all()
		if len(got) != 1 || !strings.HasSuffix(got[0], c.want) || (c.want == "|" && strings.Count(got[0], "|") != 3) {
			t.Errorf("%s: posted = %v, want the window %q", c.name, got, strings.TrimPrefix(c.want, "|"))
		}
	}
}

// TestHookNamesAnAgentRunOutsideTheWrappers is I-1608: Claude Desktop's
// own Claude Code runs `repose-hook` from ~/.claude/settings.json with no
// REPOSE_HOOK_AGENT, and the payload names it.
func TestHookNamesAnAgentRunOutsideTheWrappers(t *testing.T) {
	sink := newHookSink(t)
	bin := buildHook(t)
	payload := `{"hook_event_name":"Stop","transcript_path":"/home/dev/.claude/projects/x/absent.jsonl","last_assistant_message":"Wrote hello.txt."}`
	env := []string{"REPOSE_HOOK_SOCKET=" + sink.path, "REPOSE_HOOK_AGENT=", "REPOSE_AGENT="}
	if code, out := runHook(t, bin, payload, env); code != 0 {
		t.Fatalf("exit = %d; output: %s", code, out)
	}
	codex := `{"type":"agent-turn-complete","last-assistant-message":"auth flow done"}`
	if code, out := runHook(t, bin, "", env, codex); code != 0 {
		t.Fatalf("exit = %d; output: %s", code, out)
	}
	got := sink.all()
	if len(got) != 2 || !strings.HasPrefix(got[0], "claude|completed|") || !strings.HasPrefix(got[1], "codex|completed|auth flow done") {
		t.Fatalf("posted = %v", got)
	}
	// A payload that names no agent still posts nothing.
	if _, out := runHook(t, bin, `{"hook_event_name":"UserPromptSubmit"}`, env); !strings.Contains(out, "no agent given") {
		t.Fatalf("output: %s", out)
	}
	if len(sink.all()) != 2 {
		t.Fatalf("posted = %v", sink.all())
	}
}

// TestDesktopSessionHooksCarryTheSession (I-1676): Claude Desktop's own
// Claude Code, outside the wrappers and outside tmux, posts its
// SessionStart and SessionEnd with the conversation id and its inbox
// socket's path, and never the inbox token.
func TestDesktopSessionHooksCarryTheSession(t *testing.T) {
	sink := newHookSink(t)
	bin := buildHook(t)
	env := []string{"REPOSE_HOOK_SOCKET=" + sink.path, "REPOSE_HOOK_AGENT=", "REPOSE_AGENT=",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/claude-1000/cc/inbox.sock", "CLAUDE_CODE_MESSAGING_TOKEN=s3cret-token"}
	for _, f := range []string{"session-start.json", "session-end.json"} {
		payload, err := os.ReadFile(filepath.Join("..", "..", "internal", "guestd", "hooks", "testdata", "claude", f))
		if err != nil {
			t.Fatal(err)
		}
		if code, out := runHook(t, bin, string(payload), env); code != 0 || out != "" {
			t.Fatalf("%s: exit = %d; output: %s", f, code, out)
		}
	}
	got := sink.all()
	if len(got) != 2 || got[0] != "claude|session_start||" || got[1] != "claude|session_end||" {
		t.Fatalf("posted = %v", got)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, b := range sink.bodies {
		if b["session_id"] != "08b87fc9-c37a-4bc4-b0af-a55bc5dfc98a" || b["inbox"] != "/tmp/claude-1000/cc/inbox.sock" {
			t.Fatalf("body = %v", b)
		}
		for k, v := range b {
			if strings.Contains(v, "s3cret") {
				t.Fatalf("the inbox token went out in %q", k)
			}
		}
	}
}
