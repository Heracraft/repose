package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture reads a recorded hook payload. Every mapping this package claims is
// backed by one of these, so a change in an agent's payload shape shows up as
// a failing test rather than as a notification that never arrives.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestMapClaudeStop(t *testing.T) {
	transcript := filepath.Join("testdata", "claude", "transcript.jsonl")
	payload := strings.ReplaceAll(string(fixture(t, "claude/stop.json")), "TRANSCRIPT", transcript)

	p, err := Map("claude", []byte(payload))
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if p.Agent != "claude" || p.Kind != "completed" {
		t.Fatalf("payload = %+v, want a claude completion", p)
	}
	if p.Summary != "Added 14 tests for the payment module and they all pass." {
		t.Fatalf("summary = %q, want the last assistant line", p.Summary)
	}
}

func TestMapClaudeStopWithoutATranscript(t *testing.T) {
	p, err := Map("claude", fixture(t, "claude/stop-no-transcript.json"))
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if p.Kind != "completed" {
		t.Fatalf("kind = %q, want completed even with no transcript", p.Kind)
	}
	if p.Summary != "claude finished" {
		t.Fatalf("summary = %q, want the guest-conventions fallback %q (I-121)", p.Summary, "claude finished")
	}
}

func TestMapClaudeNotifications(t *testing.T) {
	cases := []struct {
		file    string
		summary string
	}{
		{"claude/notification-permission-prompt.json", "Claude needs your permission to use Bash"},
		{"claude/notification-agent-needs-input.json", "The agent is waiting for an answer"},
		{"claude/notification-untyped.json", "Claude needs your permission to use Write"},
	}
	for _, c := range cases {
		p, err := Map("claude", fixture(t, c.file))
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		if p.Kind != "needs_input" {
			t.Errorf("%s: kind = %q, want needs_input", c.file, p.Kind)
		}
		if p.Summary != c.summary {
			t.Errorf("%s: summary = %q, want %q", c.file, p.Summary, c.summary)
		}
	}
}

func TestMapClaudeIgnoresUnreportableHooks(t *testing.T) {
	// I-418: idle_prompt, typed or classified from its message, is no event.
	for _, file := range []string{"claude/subagent-stop.json", "claude/notification-unrelated.json", "claude/notification-idle-prompt.json", "claude/notification-idle-untyped.json"} {
		if _, err := Map("claude", fixture(t, file)); !errors.Is(err, ErrNoEvent) {
			t.Errorf("%s: err = %v, want ErrNoEvent", file, err)
		}
	}
}

func TestMapClaudeStopFailure(t *testing.T) {
	p, err := Map("claude", fixture(t, "claude/stopfailure.json"))
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if p.Kind != "error" {
		t.Fatalf("kind = %q, want error", p.Kind)
	}
	if !strings.Contains(p.Summary, "API error") {
		t.Fatalf("summary = %q", p.Summary)
	}
}

func TestMapCodex(t *testing.T) {
	p, err := Map("codex", fixture(t, "codex/agent-turn-complete.json"))
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if p.Agent != "codex" || p.Kind != "completed" {
		t.Fatalf("payload = %+v", p)
	}
	if p.Summary != "The auth flow is finished and the tests pass." {
		t.Fatalf("summary = %q", p.Summary)
	}
	if _, err := Map("codex", fixture(t, "codex/other-event.json")); !errors.Is(err, ErrNoEvent) {
		t.Fatalf("err = %v, want ErrNoEvent", err)
	}
}

func TestMapOpencode(t *testing.T) {
	cases := map[string]string{
		"opencode/session-idle.json":     "completed",
		"opencode/session-error.json":    "error",
		"opencode/permission-asked.json": "needs_input",
	}
	for file, want := range cases {
		p, err := Map("opencode", fixture(t, file))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if p.Kind != want {
			t.Errorf("%s: kind = %q, want %q", file, p.Kind, want)
		}
		if p.Summary == "" {
			t.Errorf("%s: summary is empty", file)
		}
	}
	if _, err := Map("opencode", fixture(t, "opencode/message-part-updated.json")); !errors.Is(err, ErrNoEvent) {
		t.Fatalf("err = %v, want ErrNoEvent for a chatty event", err)
	}
}

// pi's hooks come from the platform's pi extension (DECISIONS I-690), which
// speaks the socket's own shape; any other payload is no event.
func TestMapPiAcceptsOnlyTheNativeShape(t *testing.T) {
	if _, err := Map("pi", []byte(`{"type":"whatever"}`)); !errors.Is(err, ErrNoEvent) {
		t.Errorf("err = %v, want ErrNoEvent", err)
	}
	p, err := Map("pi", fixture(t, "pi/heuristic.json"))
	if err != nil || p.Agent != "pi" || p.Kind != "completed" {
		t.Errorf("payload = %+v %v", p, err)
	}
	p, err = Map("pi", []byte(`{"agent":"pi","kind":"prompt"}`))
	if err != nil || p.Kind != KindPrompt {
		t.Errorf("prompt = %+v %v", p, err)
	}
}

// Gemini CLI 0.61.0's extension hooks (I-690), captured from the real
// binary: BeforeAgent is the prompt, AfterAgent the end of the turn, with
// the reply as the summary; one a Stop-style hook forced is a pause.
func TestMapGemini(t *testing.T) {
	p, err := Map("gemini", fixture(t, "gemini/before-agent.json"))
	if err != nil || p.Kind != KindPrompt || p.Summary != "" {
		t.Fatalf("BeforeAgent = %+v %v", p, err)
	}
	p, err = Map("gemini", fixture(t, "gemini/after-agent.json"))
	if err != nil || p.Kind != "completed" || p.Summary != "Probe reply: all done." {
		t.Fatalf("AfterAgent = %+v %v", p, err)
	}
	// stop_hook_active marks the end of a turn an AfterAgent hook forced
	// on, as with Claude's Stop: the turn's end.
	if p, err := Map("gemini", fixture(t, "gemini/after-agent-hook-active.json")); err != nil || p.Kind != "completed" {
		t.Fatalf("a forced AfterAgent: %+v %v", p, err)
	}
	if _, err := Map("gemini", []byte(`{"hook_event_name":"BeforeModel"}`)); !errors.Is(err, ErrNoEvent) {
		t.Fatalf("BeforeModel: %v", err)
	}
	p, err = Map("gemini", fixture(t, "gemini/heuristic.json"))
	if err != nil || p.Kind != "completed" {
		t.Fatalf("native shape: %+v %v", p, err)
	}
}

// Codex 0.157.1 runs a UserPromptSubmit hook from /etc/codex/config.toml
// (I-690), captured from the real binary; the prompt is not read.
func TestMapCodexUserPromptSubmit(t *testing.T) {
	p, err := Map("codex", fixture(t, "codex/user-prompt-submit.json"))
	if err != nil || p.Agent != "codex" || p.Kind != KindPrompt || p.Summary != "" {
		t.Fatalf("payload = %+v %v", p, err)
	}
}

func TestMapRejectsUnknownAgents(t *testing.T) {
	if _, err := Map("aider", []byte(`{"kind":"completed"}`)); err == nil {
		t.Fatal("an agent the platform does not ship was accepted")
	}
}

func TestSummaryIsClippedToOneReadableLine(t *testing.T) {
	long := strings.Repeat("word ", 200)
	got := clip(long)
	if len([]rune(got)) > SummaryChars+1 {
		t.Fatalf("clip returned %d runes, want at most %d plus an ellipsis", len([]rune(got)), SummaryChars)
	}
	if strings.Contains(clip("a\nb\tc"), "\n") {
		t.Fatal("clip left a newline in a notification summary")
	}
}

func TestTranscriptTailIsReadFromTheEnd(t *testing.T) {
	// A transcript larger than the tail: the reader must still find the last
	// assistant line rather than give up.
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"padding padding padding"}]}}` + "\n")
	}
	b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the final word"}]}}` + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lastAssistantLine(path); got != "the final word" {
		t.Fatalf("lastAssistantLine = %q, want the final word", got)
	}
}

// TestMapClaudeStopSummaryIsTheLastMessage (I-688): Claude Code 2.1.283's
// Stop carries the last assistant message, captured from the real binary.
func TestMapClaudeStopSummaryIsTheLastMessage(t *testing.T) {
	p, err := Map("claude", fixture(t, "claude/stop-last-message.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "completed" || p.Summary != "Probe reply: all done. The tests pass." {
		t.Fatalf("payload = %+v", p)
	}
}

// TestMapClaudeStopPauses (I-688): a Stop while a background task runs
// is a pause: no event. A finished task and a cron are no pause. A Stop
// with stop_hook_active true ends the turn a Stop hook forced on, so it
// is the turn's end too: Claude Code sets the flag on the Stop after the
// continuation, not on the one a hook is about to block.
func TestMapClaudeStopPauses(t *testing.T) {
	if _, err := Map("claude", fixture(t, "claude/stop-background-task.json")); !errors.Is(err, ErrNoEvent) {
		t.Errorf("background task: err = %v, want ErrNoEvent", err)
	}
	p, err := Map("claude", fixture(t, "claude/stop-finished-task.json"))
	if err != nil || p.Kind != "completed" || p.Summary != "The wait finished." {
		t.Fatalf("finished task: %+v %v", p, err)
	}
	p, err = Map("claude", fixture(t, "claude/stop-hook-active.json"))
	if err != nil || p.Kind != "completed" || p.Summary != "Continuing." {
		t.Fatalf("stop_hook_active: %+v %v", p, err)
	}
}

// TestMapClaudeUserPromptSubmit (I-690): the prompt hook is kind prompt
// and the prompt's text goes nowhere.
func TestMapClaudeUserPromptSubmit(t *testing.T) {
	p, err := Map("claude", fixture(t, "claude/user-prompt-submit.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Agent != "claude" || p.Kind != KindPrompt || p.Summary != "" {
		t.Fatalf("payload = %+v, want a prompt with no summary", p)
	}
	// Claude Code 2.1.283 fires the hook for the turn a finished
	// background task starts, with its own <task-notification> as the
	// prompt: nobody typed it, so it closes no question. It is kind
	// resume, which marks the agent working again and nothing else.
	p, err = Map("claude", fixture(t, "claude/user-prompt-submit-task-notification.json"))
	if err != nil || p.Kind != KindResume || p.Summary != "" {
		t.Fatalf("task notification: %+v %v, want kind resume", p, err)
	}
}
