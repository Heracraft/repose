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
