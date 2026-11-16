// Package agentprompt generates the shell script that puts a prompt into
// a running agent's terminal and checks that the agent took it.
//
// It is a leaf package because two programs need the same script and
// neither may import the other: the CLI types a prompt over ssh
// (`repose run -w WINDOW -p TEXT`), and guestd types one from inside the
// guest when a `repose say` arrives (DECISIONS I-1144). Keeping one
// generator keeps I-752's check and retries in one place; two copies is
// how one of them would quietly lose the retry.
package agentprompt

import (
	"fmt"
	"strings"
)

// Typing a prompt is not submitting it (DECISIONS I-752). `repose run -p
// PROMPT` typed the prompt and pressed Enter, and once in a while the
// agent took the text and not the Enter: the prompt sat in Claude Code's
// input, the command printed "Ready" and exited 0, and the machine
// reported the agent idle. The race is not a slow start — the path that
// waits for the pane to go idle loses it too — so the fix is to check
// the terminal afterwards instead of waiting longer: the screen the
// prompt was typed into, unchanged after Enter, means the Enter was
// dropped, and Enter is pressed again, up to EnterTries times.

// ExitNotSubmitted is the script's exit code when the prompt was typed
// and the agent never took it. It is internal/cli's typeExitNotSubmitted
// and must stay the same number: the CLI reads it off an ssh exit.
const ExitNotSubmitted = 5

// EnterTries is how many Enters the submit script presses before it
// gives up; EnterPause is how long it waits before each check, as a
// shell `sleep` argument. Variables so a test can shorten the wait.
//
// The span is NOT the variable, and this was measured rather than
// reasoned (I-752, 2026-10-17). Live on a real `small` against prod:
// 4 Enters over about two seconds submitted 2 prompts in 10, and 30
// Enters over about a minute submitted 0 in 9, against a baseline of 1
// in 10 with no retrying at all. Widening it bought nothing and cost a
// minute of pressing Enter into a pane that was never going to take it,
// so it stays narrow. What the retries cannot fix is in I-752: a fresh
// agent satisfies the waiter's readiness gate while still not accepting
// a submission. A `repose say` does not meet that case: it is typed
// into an agent whose own hook says it is idle at its prompt.
var (
	EnterTries = 4
	EnterPause = "0.5"
)

// HeadRunes bounds the needle Head builds: short enough to sit on one
// line of any terminal the agent runs in, since a wrapped line would not
// be found in the screen.
const HeadRunes = 24

// Head is what the submit script looks for on the screen: the start of
// the prompt's first line with anything in it. "" when the prompt has no
// such line, and then nothing is checked.
func Head(prompt string) string {
	for _, l := range strings.Split(prompt, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if r := []rune(l); len(r) > HeadRunes {
			return string(r[:HeadRunes])
		}
		return l
	}
	return ""
}

// Shell is what one terminal gives the submit script: the shell to print
// its screen, to type the prompt in $repose_pp, and to press Enter. tmux
// and herdr each have their own (TmuxShell, HerdrShell).
type Shell struct {
	Capture string
	Text    string
	Enter   string
}

// Script types $repose_pp, presses Enter, and presses Enter again while
// the screen it typed into has not changed. It exits ExitNotSubmitted
// when the prompt is still sitting there after the last try, 0 once the
// screen has moved on, and 0 without checking anything when $repose_hd
// (Head) never showed on the screen: the check can say nothing then, and
// the Enter stands as it did before. The screen is read a pause after
// the prompt is typed, since a terminal shows what it was sent when it
// next draws, not when the keys land. The caller sets $repose_pp and
// $repose_hd; For writes both.
func Script(sh Shell) string {
	return fmt.Sprintf(`%[1]s || exit 1
sleep %[4]s
repose_st=$(%[2]s 2>/dev/null)
%[3]s || exit 1
printf '%%s\n' "$repose_st" | grep -qF -e "$repose_hd" || exit 0
repose_sn=0
while :; do
  sleep %[4]s
  [ "$(%[2]s 2>/dev/null)" = "$repose_st" ] || exit 0
  repose_sn=$((repose_sn + 1))
  [ "$repose_sn" -lt %[5]d ] || exit %[6]d
  %[3]s || exit 1
done
`, sh.Text, sh.Capture, sh.Enter, EnterPause, EnterTries, ExitNotSubmitted)
}

// For is Script for one prompt: the prompt and the head to look for are
// written once, into the shell variables the script reads. Nothing logs
// either (the CLI's sshLabel calls a script a script; guestd never logs
// a command line).
func For(sh Shell, prompt string) string {
	head := Head(prompt)
	assign := fmt.Sprintf("repose_pp=%s\nrepose_hd=%s\n", Quote(prompt), Quote(head))
	if head == "" {
		// Nothing to look for on the screen: type it as before.
		return assign + sh.Text + "\n" + sh.Enter + "\n"
	}
	return assign + Script(sh)
}

// Quote single-quotes s for a POSIX shell command line, the way the
// literal `tmux send-keys -l '<prompt>'` needs when the prompt itself may
// contain spaces or shell metacharacters.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TmuxShell is the submit script's shell for the tmux window target t,
// already quoted.
func TmuxShell(t string) Shell {
	return Shell{
		Capture: "tmux capture-pane -p -t " + t,
		Text:    "tmux send-keys -t " + t + ` -l "$repose_pp"`,
		Enter:   "tmux send-keys -t " + t + " Enter",
	}
}

// HerdrShell is the submit script's shell for the herdr pane whose id is
// in the shell variable named pane.
func HerdrShell(pane string) Shell {
	p := `"$` + pane + `"`
	return Shell{
		Capture: "herdr pane read " + p + " --source visible --format text",
		Text:    "herdr pane send-text " + p + ` "$repose_pp" >/dev/null`,
		Enter:   "herdr pane send-keys " + p + " enter >/dev/null",
	}
}
