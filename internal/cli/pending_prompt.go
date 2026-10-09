package cli

import (
	"fmt"
	"strings"
)

// The first `repose run -p PROMPT` on a machine where Claude Code has no
// login yet (B4, DECISIONS I-607). The window opens on Claude Code's
// login, and the prompt used to be dropped: the user logged in, then had
// to detach and run the prompt again. Now a waiter on the machine,
// started beside the window and detached from the ssh, types the prompt
// once the login is there and Claude Code shows its input: the
// credentials file is non-empty, the pane runs claude, its screen has
// not changed for pendingStableSecs, and it shows none of the screens a
// typed Enter would answer (agentDialogs, and the "Press Enter to
// continue" screens that follow a login). It gives up when the window
// closes, after a day without a login, or when Claude Code is not ready
// within pendingReadySecs of the login. The prompt lives only in the
// waiter's own shell on the machine; nothing logs it.

// pendingLoginSecs is how long the waiter waits for the login.
const pendingLoginSecs = 24 * 60 * 60

// pendingReadySecs is how long, after the login, Claude Code has to show
// its input.
const pendingReadySecs = 30 * 60

// pendingStableSecs is how long the screen must stay as it is.
const pendingStableSecs = 3

// pendingBlockers are screens the waiter must not type into: the agent
// dialogs `run` never types into, and Claude Code's screens after a
// login, which an Enter dismisses with the prompt lost.
func pendingBlockers() []string {
	return append(append([]string{}, agentDialogs...), "Press Enter to continue", "Enter to confirm")
}

// pendingShell is the parts of the waiter that differ between tmux and
// herdr, each a shell command.
type pendingShell struct {
	alive   string // exit 0 while the agent's terminal is there
	runs    string // exit 0 while the agent, not a shell, is in front
	capture string // print the screen
	send    string // type the prompt (in $repose_pp) and Enter
}

// pendingPromptScript is shell that starts the waiter in the background,
// detached so the ssh that runs it returns at once.
func pendingPromptScript(prompt string, sh pendingShell) string {
	var grep strings.Builder
	for _, b := range pendingBlockers() {
		grep.WriteString(" -e " + shQuote(b))
	}
	waiter := fmt.Sprintf(`repose_pp=%[1]s
repose_end=$(( $(date +%%s) + %[2]d ))
# exists-check only: the login file is never read or copied (R2-8)
until [ -s "$HOME/.claude/.credentials.json" ]; do
  [ "$(date +%%s)" -lt "$repose_end" ] || exit 0
  %[4]s || exit 0
  sleep 2
done
repose_end=$(( $(date +%%s) + %[3]d )); repose_prev=; repose_n=0
while [ "$(date +%%s)" -lt "$repose_end" ]; do
  %[4]s || exit 0
  repose_cur=$(%[6]s 2>/dev/null)
  if %[5]s && [ -n "$repose_cur" ] && [ "$repose_cur" = "$repose_prev" ] && ! printf '%%s\n' "$repose_cur" | grep -qF%[9]s; then
    repose_n=$((repose_n + 1))
    if [ "$repose_n" -ge %[8]d ]; then %[7]s; exit 0; fi
  else
    repose_n=0
  fi
  repose_prev=$repose_cur
  sleep 1
done`, shQuote(prompt), pendingLoginSecs, pendingReadySecs, sh.alive, sh.runs, sh.capture, sh.send, pendingStableSecs, grep.String())
	return "setsid -f sh -c " + shQuote(waiter) + " </dev/null >/dev/null 2>&1\n"
}

// pendingPromptTmux is the waiter for a tmux window.
func pendingPromptTmux(slug, window, prompt string) string {
	t := shQuote(tmuxWindowTarget(slug, window))
	return pendingPromptScript(prompt, pendingShell{
		alive:   "tmux has-session -t " + t + " 2>/dev/null",
		runs:    `[ "$(tmux display -p -t ` + t + ` '#{pane_current_command}' 2>/dev/null)" = claude ]`,
		capture: "tmux capture-pane -p -t " + t,
		send:    "tmux send-keys -t " + t + ` -l "$repose_pp" && tmux send-keys -t ` + t + " Enter",
	})
}

// pendingPromptHerdr is the waiter for the herdr pane whose id is in the
// start script's $repose_p. A pane whose shell is in front is not the
// agent: Claude Code exited, and the prompt would run as a command.
func pendingPromptHerdr(prompt string) string {
	return `repose_pane=$repose_p ` + pendingPromptScript(prompt, pendingShell{
		alive:   `herdr pane get "$repose_pane" 2>/dev/null | grep -q '"result"'`,
		runs:    `herdr pane process-info "$repose_pane" 2>/dev/null | jq -e '.result.process_info | .foreground_process_group_id != .shell_pid' >/dev/null 2>&1`,
		capture: `herdr pane read "$repose_pane" --source visible --format text`,
		send:    `herdr pane run "$repose_pane" "$repose_pp"`,
	})
}
