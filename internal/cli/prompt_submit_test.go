//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubTerminal is a terminal in two files: the screen, which typing
// appends the prompt to, and the Enters, of which the first drops are
// thrown away — the rest replace the screen, as an agent that starts
// work redraws. It is the race of I-752 on demand.
func stubTerminal(t *testing.T, drops int) (sh submitShell, screen, enters string) {
	t.Helper()
	dir := t.TempDir()
	screen, enters = filepath.Join(dir, "screen"), filepath.Join(dir, "enters")
	if err := os.WriteFile(screen, []byte("idle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enter := filepath.Join(dir, "enter.sh")
	if err := os.WriteFile(enter, []byte(fmt.Sprintf(`echo e >> %[1]s
[ "$(wc -l < %[1]s)" -gt %[2]d ] && printf 'working\n' > %[3]s
exit 0
`, shQuote(enters), drops, shQuote(screen))), 0o600); err != nil {
		t.Fatal(err)
	}
	return submitShell{
		capture: "cat " + shQuote(screen),
		text:    "printf '%s\\n' \"$repose_pp\" >> " + shQuote(screen),
		enter:   "sh " + shQuote(enter),
	}, screen, enters
}

// submitExit runs one generated script and returns its exit code.
func submitExit(t *testing.T, script string) int {
	t.Helper()
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return 0
}

// countLines is how many Enters a stub terminal was sent.
func countLines(t *testing.T, path string) int {
	t.Helper()
	b, _ := os.ReadFile(path)
	return len(nonEmptyLines(string(b)))
}

// The shape before I-752 — type, press Enter, say nothing — against a
// terminal that drops one Enter: the prompt sits on the screen and the
// script exits 0, which is what the user saw as a prompt typed and
// never answered.
func TestTypeAndEnterAloneLosesADroppedEnter(t *testing.T) {
	sh, screen, enters := stubTerminal(t, 1)
	script := fmt.Sprintf("repose_pp=%s\n%s && %s\n", shQuote("fix the flaky test"), sh.text, sh.enter)
	if code := submitExit(t, script); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if n := countLines(t, enters); n != 1 {
		t.Fatalf("%d Enters, want 1", n)
	}
	if b, _ := os.ReadFile(screen); !strings.Contains(string(b), "fix the flaky test") {
		t.Fatalf("screen = %q, want the prompt still in it", b)
	}
}

// I-752: the submit script's own logic against the same stub terminal.
func TestSubmitPromptPressesEnterAgainAndGivesUp(t *testing.T) {
	oldPause, oldTries := promptEnterPause, promptEnterTries
	promptEnterPause = "0" // the real pause is 0.5 s a check
	t.Cleanup(func() { promptEnterPause, promptEnterTries = oldPause, oldTries })

	for _, c := range []struct {
		name       string
		drops      int  // Enters the stub terminal throws away
		typeNoShow bool // the prompt never reaches the screen
		wantExit   int
		wantEnters int
	}{
		{name: "taken at once", wantEnters: 1},
		{name: "first Enter dropped", drops: 1, wantEnters: 2},
		{name: "every Enter dropped", drops: 99, wantExit: typeExitNotSubmitted, wantEnters: promptEnterTries},
		{name: "the prompt never showed", typeNoShow: true, wantEnters: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			sh, _, enters := stubTerminal(t, c.drops)
			if c.typeNoShow {
				sh.text = "true" // the keys land and nothing is drawn
			}
			if code := submitExit(t, submitPromptFor(sh, "fix the flaky test")); code != c.wantExit {
				t.Errorf("exit = %d, want %d", code, c.wantExit)
			}
			if n := countLines(t, enters); n != c.wantEnters {
				t.Errorf("%d Enters, want %d", n, c.wantEnters)
			}
		})
	}
}

// I-752: against a real tmux window whose first Enter is thrown away,
// the prompt still reaches the program behind the pane.
func TestTypePromptResendsEnterIntoATmuxWindow(t *testing.T) {
	t.Parallel()
	run, home := pendingTmux(t, "ready")
	target := shQuote(tmuxWindowTarget("todo", "claude"))
	sh := tmuxSubmitShell(target)
	count := filepath.Join(home, "enters")
	stub := filepath.Join(home, "enter.sh")
	if err := os.WriteFile(stub, []byte(fmt.Sprintf(`echo e >> %[1]s
if [ "$(wc -l < %[1]s)" -gt 1 ]; then %[2]s; fi
exit 0
`, shQuote(count), sh.enter)), 0o600); err != nil {
		t.Fatal(err)
	}
	sh.enter = "sh " + shQuote(stub)
	out, err := run(submitPromptFor(sh, "fix the flaky test"))
	if err != nil {
		t.Fatalf("submit: %v %s", err, out)
	}
	// The window's program echoes each line it reads, so a submitted
	// prompt shows twice: the keys typed into the input, and the echo.
	pane, _ := run("tmux capture-pane -p -t " + target)
	if n := strings.Count(pane, "fix the flaky test"); n < 2 {
		t.Fatalf("the prompt was not submitted (%d lines):\n%s", n, pane)
	}
	if n := countLines(t, count); n != 2 {
		t.Fatalf("%d Enters, want 2", n)
	}
}

func TestPromptHead(t *testing.T) {
	for prompt, want := range map[string]string{
		"fix the flaky test":                 "fix the flaky test",
		"\n\n  fix it  \nand more":           "fix it",
		"":                                   "",
		"\n \t ":                             "",
		strings.Repeat("ab", 40):             strings.Repeat("ab", 12),
		"refactor the whole of internal/cli": "refactor the whole of in",
	} {
		if got := promptHead(prompt); got != want {
			t.Errorf("promptHead(%q) = %q, want %q", prompt, got, want)
		}
	}
}
