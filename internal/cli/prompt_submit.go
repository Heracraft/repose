package cli

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
// dropped, and Enter is pressed again, up to promptEnterTries times.
// Every path that types a prompt goes through submitPromptScript, so the
// check and the retries are in one place and run on the machine, in the
// same ssh as the typing: the prompt never waits on another round trip.

// promptEnterTries is how many Enters the submit script presses before
// it gives up; promptEnterPause is how long it waits before each check,
// as a shell `sleep` argument. Variables so a test can shorten the wait.
var (
	promptEnterTries = 4
	promptEnterPause = "0.5"
)

// promptHeadRunes bounds the needle promptHead builds: short enough to
// sit on one line of any terminal the agent runs in, since a wrapped
// line would not be found in the screen.
const promptHeadRunes = 24

// promptHead is what the submit script looks for on the screen: the
// start of the prompt's first line with anything in it. "" when the
// prompt has no such line, and then nothing is checked.
func promptHead(prompt string) string {
	for _, l := range strings.Split(prompt, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if r := []rune(l); len(r) > promptHeadRunes {
			return string(r[:promptHeadRunes])
		}
		return l
	}
	return ""
}

// submitShell is what one terminal gives the submit script: the shell to
// print its screen, to type the prompt in $repose_pp, and to press
// Enter. tmux and herdr each have their own (tmuxSubmitShell,
// herdrSubmitShell).
type submitShell struct {
	capture string
	text    string
	enter   string
}

// submitPromptScript types $repose_pp, presses Enter, and presses Enter
// again while the screen it typed into has not changed. It exits
// typeExitNotSubmitted when the prompt is still sitting there after the
// last try, 0 once the screen has moved on, and 0 without checking
// anything when $repose_hd (promptHead) never showed on the screen: the
// check can say nothing then, and the Enter stands as it did before.
// The screen is read a pause after the prompt is typed, since a
// terminal shows what it was sent when it next draws, not when the keys
// land. The caller sets $repose_pp and $repose_hd; submitPromptFor
// writes both.
func submitPromptScript(sh submitShell) string {
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
`, sh.text, sh.capture, sh.enter, promptEnterPause, promptEnterTries, typeExitNotSubmitted)
}

// submitPromptFor is submitPromptScript for one prompt: the prompt and
// the head to look for are written once, into the shell variables the
// script reads. Nothing logs either (sshLabel calls a script a script).
func submitPromptFor(sh submitShell, prompt string) string {
	head := promptHead(prompt)
	assign := fmt.Sprintf("repose_pp=%s\nrepose_hd=%s\n", shQuote(prompt), shQuote(head))
	if head == "" {
		// Nothing to look for on the screen: type it as before.
		return assign + sh.text + "\n" + sh.enter + "\n"
	}
	return assign + submitPromptScript(sh)
}

// promptNotSubmittedError says the prompt was typed into the agent's
// terminal and the agent never took it: the submit script pressed Enter
// promptEnterTries times and the screen never moved on
// (typeExitNotSubmitted). The prompt is sitting in the input.
type promptNotSubmittedError struct{ agent string }

func (e *promptNotSubmittedError) Error() string {
	return agentDisplayName(e.agent) + " did not take the prompt"
}

// tmuxSubmitShell is the submit script's shell for the tmux window
// target t, as tmuxWindowTarget spells it, already quoted.
func tmuxSubmitShell(t string) submitShell {
	return submitShell{
		capture: "tmux capture-pane -p -t " + t,
		text:    "tmux send-keys -t " + t + ` -l "$repose_pp"`,
		enter:   "tmux send-keys -t " + t + " Enter",
	}
}

// herdrSubmitShell is the submit script's shell for the herdr pane whose
// id is in the shell variable named pane.
func herdrSubmitShell(pane string) submitShell {
	p := `"$` + pane + `"`
	return submitShell{
		capture: "herdr pane read " + p + " --source visible --format text",
		text:    "herdr pane send-text " + p + ` "$repose_pp" >/dev/null`,
		enter:   "herdr pane send-keys " + p + " enter >/dev/null",
	}
}
