//go:build !windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pendingTmux is a private tmux server with a session "todo" whose
// window "claude" runs a program named claude that echoes what is
// typed, after it printed screen. HOME is a temporary folder.
func pendingTmux(t *testing.T, screen string) (run func(string) (string, error), home string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("needs tmux")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("needs setsid")
	}
	dir, err := os.MkdirTemp("", "rp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	home = filepath.Join(dir, "h")
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{filepath.Join(home, ".claude"), bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// bash under the name claude: its process name is claude, as tmux
	// reports it, and it echoes each line typed (a copy of cat would be
	// coreutils' multi-call binary, which refuses another name).
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("needs bash")
	}
	if bash, err = filepath.EvalSymlinks(bash); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bash, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	env := append(filterEnvKeys(os.Environ(), "HOME", "TMUX", "TMUX_TMPDIR"), "HOME="+home, "TMUX_TMPDIR="+dir)
	run = func(script string) (string, error) {
		c := exec.Command("sh", "-c", script)
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() { _, _ = run("tmux kill-server") })
	if out, err := run("tmux -f /dev/null new-session -d -s todo -n shell -x 120 -y 30 && tmux new-window -d -t todo -n claude " + shQuote("printf '%s\\n' "+shQuote(screen)+"; exec "+filepath.Join(bin, "claude")+" -c 'while IFS= read -r l; do echo \"$l\"; done'")); err != nil {
		t.Fatalf("tmux: %v %s", err, out)
	}
	return run, home
}

// pendingNonce is a prompt unique to this run. The waiter polls for up to
// pendingReadySecs (half an hour) and the only thing pgrep can match it on
// is its prompt, so a fixed prompt made one run's leftover waiter fail the
// NEXT run of the same test: "the waiter outlived its window: <pid>", with
// nothing to do with the code under test. A waiter is left behind whenever
// the test binary dies without running its cleanups, which a `go test`
// timeout does. The nonce scopes both the pgrep and the kill to this run;
// the cleanup kills this run's waiter whatever the test did (2026-10-18).
func pendingNonce(t *testing.T, run func(string) (string, error), base string) string {
	t.Helper()
	prompt := fmt.Sprintf("%s %s-%d", base, t.Name(), time.Now().UnixNano())
	t.Cleanup(func() { _, _ = run("pkill -f " + shQuote(prompt) + " || true") })
	return prompt
}

// pendingGone polls for the waiter to be gone rather than assuming one
// wait is enough: the waiter checks its window once per loop and sleeps a
// second or two between checks, so a flat sleep races it.
func pendingGone(t *testing.T, run func(string) (string, error), prompt string) string {
	t.Helper()
	needle := "[" + prompt[:1] + "]" + prompt[1:]
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := run("pgrep -f " + shQuote(needle) + " || true")
		if strings.TrimSpace(out) == "" {
			return ""
		}
		if time.Now().After(deadline) {
			return strings.TrimSpace(out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// I-607: the waiter types nothing before the login, then types the
// prompt once the credentials file is there and the screen is still.
func TestPendingPromptTypesAfterLogin(t *testing.T) {
	t.Parallel()
	run, home := pendingTmux(t, "Welcome to Claude Code")
	prompt := pendingNonce(t, run, "fix the 'flaky' test")
	if out, err := run(pendingPromptTmux("todo", "claude", prompt)); err != nil {
		t.Fatalf("waiter: %v %s", err, out)
	}
	time.Sleep(3 * time.Second)
	if pane, _ := run("tmux capture-pane -p -t =todo:=claude"); strings.Contains(pane, prompt) {
		t.Fatalf("typed before the login:\n%s", pane)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		pane, _ := run("tmux capture-pane -p -t =todo:=claude")
		if strings.Count(pane, prompt) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never typed:\n%s", pane)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// I-607: a screen an Enter would answer gets nothing, login or not.
func TestPendingPromptWaitsOnBlockers(t *testing.T) {
	t.Parallel()
	run, home := pendingTmux(t, "Login successful. Press Enter to continue")
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := pendingNonce(t, run, "do the thing")
	if out, err := run(pendingPromptTmux("todo", "claude", prompt)); err != nil {
		t.Fatalf("waiter: %v %s", err, out)
	}
	time.Sleep(7 * time.Second)
	if pane, _ := run("tmux capture-pane -p -t =todo:=claude"); strings.Contains(pane, prompt) {
		t.Fatalf("typed into the login's Enter screen:\n%s", pane)
	}
	// The window closes: the waiter ends with it.
	if _, err := run("tmux kill-window -t =todo:=claude"); err != nil {
		t.Fatal(err)
	}
	if out := pendingGone(t, run, prompt); out != "" {
		t.Fatalf("the waiter outlived its window: %s", out)
	}
}

func TestPendingPromptHerdrScript(t *testing.T) {
	s := pendingPromptHerdr("hi")
	// The prompt and the Enter are the submit script's two commands
	// (I-752), not one `herdr pane run`.
	for _, want := range []string{`repose_pane=$repose_p setsid -f sh -c `, `herdr pane send-text "$repose_pane" "$repose_pp"`, `herdr pane send-keys "$repose_pane" enter`, `foreground_process_group_id != .shell_pid`} {
		if !strings.Contains(s, want) {
			t.Errorf("herdr waiter lacks %q", want)
		}
	}
}

// I-607: a hand-started agent in another window counts for the warning,
// in the machine's own checkout only.
func TestWindowsAndAgent(t *testing.T) {
	out := "shell\tclaude\nclaude\tclaude\ncodex\tcodex\n"
	ws, byHand := windowsAndAgent(out, "claude", true)
	if strings.Join(ws, ",") != "shell,claude,codex" || !byHand {
		t.Fatalf("%v %v", ws, byHand)
	}
	if _, byHand := windowsAndAgent("shell\tbash\nclaude-2\tclaude\n", "claude", true); byHand {
		t.Fatal("an agent window counted as by hand")
	}
	if _, byHand := windowsAndAgent(out, "claude", false); byHand {
		t.Fatal("another checkout counted the shell window")
	}
}
