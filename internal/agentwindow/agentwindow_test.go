// The package that decides whether an agent takes its prompt at all
// (DECISIONS I-780). Both callers — the CLI over SSH and guestd for a
// trigger's run — build one shell line out of these words, so a prompt
// that breaks the quoting runs a command on somebody's machine instead
// of being read by an agent.
package agentwindow_test

import (
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/agentwindow"
)

// Quote makes one shell word of anything, which is the whole defence:
// the line is built twice (tmux's bash, then the agent's argv), so both
// levels go through it.
func TestQuoteMakesOneWordOfAnything(t *testing.T) {
	for _, p := range []string{
		"plain",
		"it's quoted",
		"$(touch /tmp/pwned)",
		"`touch /tmp/pwned`",
		"--help",
		"a\nb",
		`say "hi"`,
		"semi; rm -rf /",
		"$HOME",
	} {
		got := agentwindow.Quote(p)
		if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
			t.Errorf("Quote(%q) = %s, want a single-quoted word", p, got)
		}
		// Every single quote inside is closed and reopened, so no bare
		// one is left to end the word early.
		if strings.Contains(strings.TrimSuffix(strings.TrimPrefix(got, "'"), "'"), "''") {
			continue
		}
		if unquote(got) != p {
			t.Errorf("Quote(%q) does not read back: %s", p, got)
		}
	}
}

// unquote is the shell's own reading of a single-quoted word.
func unquote(s string) string {
	s = strings.TrimPrefix(s, "'")
	s = strings.TrimSuffix(s, "'")
	return strings.ReplaceAll(s, `'"'"'`, "'")
}

// Each agent's form is the one read out of its own help, and a prompt
// that begins with `-` has to survive every one of those parsers.
func TestEveryAgentsPromptFormSurvivesALeadingDash(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "opencode", "gemini", "pi"} {
		args, ok := agentwindow.PromptArgv(agent, "-dash first")
		if !ok {
			t.Errorf("%s does not take a prompt that begins with -", agent)
			continue
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-dash first") {
			t.Errorf("%s: %q does not carry the prompt", agent, joined)
		}
		// Either option parsing is stopped with `--`, or the prompt is
		// one word joined to its flag with `=`; a separate word after a
		// flag loses a leading dash.
		if args[0] != "--" && !strings.Contains(args[0], "=") {
			t.Errorf("%s: %q would read the prompt as a flag", agent, joined)
		}
	}
}

// An agent with no form, an empty prompt, and pi's one refusal all keep
// the typing path rather than starting a window with no prompt.
func TestNoFormMeansNoArgvPrompt(t *testing.T) {
	if _, ok := agentwindow.PromptArgv("aider", "do it"); ok {
		t.Error("an agent with no entry was given an argv prompt")
	}
	if _, ok := agentwindow.PromptArgv("claude", ""); ok {
		t.Error("an empty prompt was put on the command line")
	}
	// pi reads a prompt that begins with @ as a file to include, so `--`
	// does not save it: the window would be left with no agent.
	if _, ok := agentwindow.PromptArgv("pi", "@notes.md and go"); ok {
		t.Error("pi was given a prompt beginning with @ on its argv")
	}
	if _, ok := agentwindow.PromptArgv("pi", "go"); !ok {
		t.Error("pi takes an ordinary prompt on its argv")
	}
}

// StartCommand is tmux's command argument: the binary and the prompt as
// one word, quoted a second time so the session's bash splits it back
// into exactly those words.
func TestStartCommandIsQuotedTwice(t *testing.T) {
	cmd := agentwindow.StartCommand("claude", "it's a 'test'")
	inner := unquote(cmd)
	if !strings.HasPrefix(inner, "'claude' '--' ") {
		t.Fatalf("the inner line is %q", inner)
	}
	if got := unquote(strings.TrimPrefix(inner, "'claude' '--' ")); got != "it's a 'test'" {
		t.Errorf("the prompt reads back as %q", got)
	}
	// An agent with no form is its own name and nothing else, which is
	// what every window was before I-780.
	if got := agentwindow.StartCommand("aider", "do it"); got != "'aider'" {
		t.Errorf("StartCommand for an agent with no form is %q", got)
	}
}

// The window a run opens: the trust shell first, then one tmux
// new-window with the agent's line as its command.
func TestNewWindowTrustsTheFolderThenOpensOneWindow(t *testing.T) {
	got := agentwindow.NewWindow("todo-app", "t-apply", "'/home/dev/todo-app'", "claude", "go", "")
	if n := strings.Count(got, "tmux new-window"); n != 1 {
		t.Errorf("%d new-window calls, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "-t todo-app -n t-apply") || !strings.Contains(got, " -d ") {
		t.Errorf("the window is not the session's, named, and detached:\n%s", got)
	}
	trust := strings.Index(got, "hasTrustDialogAccepted")
	open := strings.Index(got, "tmux new-window")
	if trust < 0 || trust > open {
		t.Error("the trust script does not run before the window opens (I-486)")
	}
}
