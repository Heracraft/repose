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
		at := -1
		for i, a := range args {
			if strings.Contains(a, "-dash first") {
				at = i
			}
		}
		if at < 0 {
			t.Errorf("%s: %q does not carry the prompt", agent, joined)
			continue
		}
		// Either option parsing is stopped with `--` right before the
		// prompt, or the prompt is one word joined to its flag with `=`;
		// a separate word after a flag loses a leading dash.
		if (at == 0 || args[at-1] != "--") && !strings.Contains(args[at], "=") {
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

// A window's environment goes on `tmux new-window -e`, because a tmux
// window inherits the server's environment and not the client's: a
// trigger's REPOSE_PAYLOAD set around the tmux command never reached
// the agent, which said so on the first live run (2026-11-17). Proved
// on tmux 3.7c: with -e the window's own process has the variables,
// without it the count is 0.
func TestTheWindowCarriesItsOwnEnvironment(t *testing.T) {
	// Only the tmux part: the trust script before it has a `jq -e` of
	// its own, which is how the first version of this test failed.
	tmuxPart := func(line string) string { return line[strings.Index(line, "tmux new-window"):] }
	got := agentwindow.NewWindow("todo-app", "t-apply", "'/home/dev/todo-app'", "claude", "go", "",
		"REPOSE_TRIGGER=apply", "REPOSE_PAYLOAD=/home/dev/.repose/triggers/apply/runs/r1/payload", "")
	got = tmuxPart(got)
	for _, want := range []string{
		"-e 'REPOSE_TRIGGER=apply'",
		"-e 'REPOSE_PAYLOAD=/home/dev/.repose/triggers/apply/runs/r1/payload'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is not on the command:\n%s", want, got)
		}
	}
	// An empty pair is not passed at all, and every -e comes before the
	// command word, where tmux reads it.
	if strings.Contains(got, "-e ''") {
		t.Error("an empty -e went on the command")
	}
	if i, j := strings.LastIndex(got, " -e "), strings.Index(got, " -d "); i > j {
		t.Errorf("an -e after -d:\n%s", got)
	}
	// A value with a space, a quote or a $ in it is one word still.
	tricky := tmuxPart(agentwindow.NewWindow("s", "w", "'/d'", "claude", "", "", "X=a 'b' $(echo c)"))
	if !strings.Contains(tricky, `-e 'X=a '"'"'b'"'"' $(echo c)'`) {
		t.Errorf("a value with quotes in it is not one word:\n%s", tricky)
	}
	// With no env the command is what it always was.
	if plain := tmuxPart(agentwindow.NewWindow("s", "w", "'/d'", "claude", "go", "")); strings.Contains(plain, " -e ") {
		t.Errorf("an -e with no environment to pass:\n%s", plain)
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

// opencode takes the prompt on a private server (DECISIONS I-1562). On
// the machine's background service, opencode 2.3.2's TUI kills a service
// that misses three 2-second checks and gives up after 120 s, so on a busy
// machine `repose run -p` printed Ready and the prompt never ran.
func TestOpencodeTakesThePromptOnAPrivateServer(t *testing.T) {
	args, ok := agentwindow.PromptArgv("opencode", "create hello.txt")
	if !ok {
		t.Fatal("opencode does not take a prompt on its argv")
	}
	want := []string{"--standalone", "--prompt=create hello.txt"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("opencode argv = %q, want %q", args, want)
	}
}
