//go:build !windows

package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// I-780: the window that `repose run -p PROMPT` opens starts the agent with
// the prompt as its own command-line argument, one agent at a time, and
// an agent with no form for it starts as it always did and is typed into.
// Before I-780 every line here ended in the bare binary.
func TestAgentWindowCommandCarriesThePrompt(t *testing.T) {
	const prompt = "fix the flaky test"
	for _, c := range []struct {
		binary string
		want   string // what `tmux new-window ... -d` is given
	}{
		{"claude", `''\''claude'\'' '\''--'\'' '\''fix the flaky test'\'''`},
		{"codex", `''\''codex'\'' '\''--'\'' '\''fix the flaky test'\'''`},
		{"gemini", `''\''gemini'\'' '\''--prompt-interactive=fix the flaky test'\'''`},
		{"opencode", `''\''opencode'\'' '\''--prompt=fix the flaky test'\'''`},
		{"pi", `''\''pi'\'' '\''--'\'' '\''fix the flaky test'\'''`},
		// No form: the command line is the binary alone, as it was
		// before I-780, and submitPromptScript types the prompt.
		{"cat", `'cat'`},
	} {
		t.Run(c.binary, func(t *testing.T) {
			got := agentWindowCommand("proj", "", c.binary, "~/proj", c.binary, prompt, mcpApprovals{})
			want := "tmux new-window -t proj -n " + c.binary + " -c ~/proj -d " + c.want
			if !strings.HasSuffix(got, want) {
				t.Fatalf("command line\n got %s\nwant it to end in %s", got, want)
			}
			_, carried := agentPromptArgv(c.binary, prompt)
			if carried == (c.binary == "cat") {
				t.Fatalf("%s carries the prompt = %v", c.binary, carried)
			}
		})
	}
}

// A prompt that begins with "@" is pi's `@file` include even after `--`
// (pi 1.0.4 exits "Error: File not found"), so it keeps the typing path;
// no other agent reads an argument that way.
func TestPiRefusesAnAtPromptAsAnArgument(t *testing.T) {
	if _, ok := agentPromptArgv("pi", "@notes.md read this"); ok {
		t.Fatal("pi took an @ prompt as an argument")
	}
	if args, ok := agentPromptArgv("pi", "read @notes.md"); !ok || args[1] != "read @notes.md" {
		t.Fatalf("pi(%q) = %q, %v", "read @notes.md", args, ok)
	}
	for _, a := range []string{"claude", "codex", "gemini", "opencode"} {
		if _, ok := agentPromptArgv(a, "@notes.md read this"); !ok {
			t.Fatalf("%s refused an @ prompt", a)
		}
	}
}

// An empty prompt is no prompt: the window starts the agent alone.
func TestNoPromptNoArgument(t *testing.T) {
	for _, a := range agentNames {
		if _, ok := agentPromptArgv(a, ""); ok {
			t.Fatalf("%s took an empty prompt as an argument", a)
		}
		if got := agentStartCommand(a, ""); got != shQuote(a) {
			t.Fatalf("%s with no prompt = %s", a, got)
		}
	}
}

// The quoting, end to end through a real tmux: the hostile prompt reaches
// the agent as one argv word, and nothing in it is expanded, run or
// read as a flag. Before I-780 the agent got no arguments at all.
func TestHostilePromptReachesTheAgentAsOneArgument(t *testing.T) {
	t.Parallel()
	// A prompt with every shape that breaks a shell word: a leading
	// dash, both quotes, a newline, $, a backtick and a command
	// substitution that must never run.
	prompt := "-n it's \"the $PATH\" and `id`\nsecond $(touch " + t.TempDir() + "/pwned) 'line'"
	args := startAgentInTmux(t, "claude", prompt)
	if want := []string{"--", prompt}; !slices.Equal(args, want) {
		t.Fatalf("argv = %q, want %q", args, want)
	}
}

// startAgentInTmux runs agentWindowCommand's shell in a tmux of its own,
// with a stand-in agent on PATH that records its argv, and returns the
// words that agent was started with.
func startAgentInTmux(t *testing.T, binary, prompt string) []string {
	t.Helper()
	for _, c := range []string{"tmux", "sh"} {
		if _, err := exec.LookPath(c); err != nil {
			t.Skipf("needs %s", c)
		}
	}
	// Short, because the tmux socket lives under it.
	dir, err := os.MkdirTemp("", "rp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	bin, argvFile := filepath.Join(dir, "bin"), filepath.Join(dir, "argv")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// NUL-separated, since an argument may hold a newline.
	stand := fmt.Sprintf(`#!/bin/sh
: > %[1]s.part
for a in "$@"; do printf '%%s\0' "$a" >> %[1]s.part; done
mv %[1]s.part %[1]s
sleep 30
`, shQuote(argvFile))
	if err := os.WriteFile(filepath.Join(bin, binary), []byte(stand), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(filterEnvKeys(os.Environ(), "HOME", "PATH", "TMUX", "TMUX_TMPDIR"),
		"HOME="+dir, "PATH="+bin+":"+os.Getenv("PATH"), "TMUX_TMPDIR="+dir)
	run := func(script string) (string, error) {
		c := exec.Command("sh", "-c", script)
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() { _, _ = run("tmux kill-server") })
	if out, err := run("tmux -f /dev/null new-session -d -s proj -n shell -x 120 -y 30"); err != nil {
		t.Fatalf("tmux: %v %s", err, out)
	}
	if out, err := run(agentWindowCommand("proj", "", binary, dir, binary, prompt, mcpApprovals{})); err != nil {
		t.Fatalf("new-window: %v %s", err, out)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		b, err := os.ReadFile(argvFile)
		if err == nil {
			pane, _ := run("tmux capture-pane -p -t '=proj:=" + binary + "'")
			if strings.Contains(pane, "pwned") || strings.Contains(pane, "uid=") {
				t.Fatalf("the prompt was run by the shell:\n%s", pane)
			}
			// Split leaves a trailing empty word after the last NUL.
			raw := bytes.Split(b, []byte{0})
			if len(raw) > 0 && len(raw[len(raw)-1]) == 0 {
				raw = raw[:len(raw)-1]
			}
			args := make([]string, 0, len(raw))
			for _, a := range raw {
				args = append(args, string(a))
			}
			return args
		}
		if time.Now().After(deadline) {
			pane, _ := run("tmux capture-pane -p -t '=proj:=" + binary + "'")
			t.Fatalf("the agent recorded no argv:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// innerLine undoes shQuote's outer level: the command line the guest's
// bash is handed, which is the single argument tmux passes it.
func innerLine(t *testing.T, s string) string {
	t.Helper()
	inner, ok := strings.CutPrefix(s, "'")
	if !ok {
		t.Fatalf("agentStartCommand did not return a single-quoted word: %s", s)
	}
	inner, ok = strings.CutSuffix(inner, "'")
	if !ok {
		t.Fatalf("agentStartCommand did not return a single-quoted word: %s", s)
	}
	return strings.ReplaceAll(inner, `'\''`, "'")
}

// TestWindowRunsASimpleCommand holds the one invariant the agent's window
// depends on: the line tmux starts is a *simple command* -- one quoted
// word per argv word and nothing else -- so the guest's bash execs the
// agent instead of staying in front of it, and `pane_current_command` is
// the agent's own name. Three readers depend on that field: `repose ps`,
// windowNameFor, and guestd's agent detection. A pipeline, a
// `cd X && ...`, a `;` or a trailing `&` in agentStartCommand would make
// a working agent read as a shell in all three, silently, and no running
// process in a test can show it because every test agent is a stand-in
// whose comm is its interpreter. Asserting the whole string is what
// catches an edit to that function, which is the only moment this can
// regress; TestBashExecsTheAgent is the same invariant on a real bash.
func TestWindowRunsASimpleCommand(t *testing.T) {
	prompts := []string{
		"fix the flaky test",
		"",
		"   ",
		`-n it's "the $PATH" and ` + "`id`",
		"first\nsecond",
		"read @notes.md",
		"$(touch /tmp/repose-should-not-exist)",
		"h\u00e9llo \u00fcnicode \u2705",
		strings.Repeat("long ", 2048),
	}
	for _, binary := range append(slices.Clone(agentNames), "cat") {
		for _, p := range prompts {
			// The line the window must run: the binary, then each word
			// of the agent's form, each one shQuote'd, joined by one
			// space -- and the whole line shQuote'd again, because tmux
			// hands its command to bash as a single argument.
			want := shQuote(binary)
			if args, carried := agentPromptArgv(binary, p); carried {
				for _, a := range args {
					want += " " + shQuote(a)
				}
				want = shQuote(want)
			}
			if got := agentStartCommand(binary, p); got != want {
				t.Errorf("agentStartCommand(%q, %.24q)\n got %s\nwant %s", binary, p, got, want)
			}
		}
	}
}

// TestBashExecsTheAgent is the same invariant seen from the other side,
// on a real bash: when the line is a simple command bash replaces itself
// with the program, so the program's pid IS the shell's pid, which is
// what makes pane_current_command the agent's name.
//
// It is a statement of the premise, not the regression guard, and the
// difference was measured rather than assumed. Forcing agentStartCommand
// to return a subshell (`( line )`) or a list (`line ; true`) fails this
// test for all five agents, and a pipeline fails it too. But `cd . && line`
// does NOT: bash still execs the last command of an && list, so the pids
// still match while the line is no longer simple. TestWindowRunsASimpleCommand
// is what catches that one, by asserting the whole string.
func TestBashExecsTheAgent(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}
	dir := t.TempDir()
	for _, binary := range agentNames {
		stand := filepath.Join(dir, binary)
		if err := os.WriteFile(stand, []byte("#!/bin/sh\necho $$\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, binary := range agentNames {
		t.Run(binary, func(t *testing.T) {
			line := agentStartCommand(binary, "fix the flaky test")
			// What the guest's bash is handed: the one argument tmux
			// gives it, with the outer quoting undone by the shell that
			// ran the tmux command.
			inner := innerLine(t, line)
			cmd := exec.Command(bash, "-c", inner)
			cmd.Env = append(os.Environ(), "PATH="+dir)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("bash -c %s: %v", inner, err)
			}
			if got, want := strings.TrimSpace(string(out)), fmt.Sprint(cmd.Process.Pid); got != want {
				t.Errorf("the agent ran as pid %s, bash was pid %s: bash did not exec it, so pane_current_command would read the shell", got, want)
			}
		})
	}
}
