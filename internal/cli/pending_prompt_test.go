//go:build !windows

package cli

import (
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

// I-607: the waiter types nothing before the login, then types the
// prompt once the credentials file is there and the screen is still.
func TestPendingPromptTypesAfterLogin(t *testing.T) {
	t.Parallel()
	run, home := pendingTmux(t, "Welcome to Claude Code")
	if out, err := run(pendingPromptTmux("todo", "claude", "fix the 'flaky' test")); err != nil {
		t.Fatalf("waiter: %v %s", err, out)
	}
	time.Sleep(3 * time.Second)
	if pane, _ := run("tmux capture-pane -p -t =todo:=claude"); strings.Contains(pane, "flaky") {
		t.Fatalf("typed before the login:\n%s", pane)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		pane, _ := run("tmux capture-pane -p -t =todo:=claude")
		if strings.Count(pane, "fix the 'flaky' test") >= 1 {
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
	if out, err := run(pendingPromptTmux("todo", "claude", "do the thing")); err != nil {
		t.Fatalf("waiter: %v %s", err, out)
	}
	time.Sleep(7 * time.Second)
	if pane, _ := run("tmux capture-pane -p -t =todo:=claude"); strings.Contains(pane, "do the thing") {
		t.Fatalf("typed into the login's Enter screen:\n%s", pane)
	}
	// The window closes: the waiter ends with it.
	if _, err := run("tmux kill-window -t =todo:=claude"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if out, _ := run("pgrep -f '[d]o the thing' || true"); strings.TrimSpace(out) != "" {
		t.Fatalf("the waiter outlived its window: %s", out)
	}
}

func TestPendingPromptHerdrScript(t *testing.T) {
	s := pendingPromptHerdr("hi")
	for _, want := range []string{`repose_pane=$repose_p setsid -f sh -c `, `herdr pane run "$repose_pane" "$repose_pp"`, `foreground_process_group_id != .shell_pid`} {
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
