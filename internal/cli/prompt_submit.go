package cli

import (
	"github.com/heracraft/repose/internal/agentprompt"
)

// The submit script of DECISIONS I-752 lives in internal/agentprompt, a
// leaf package, because guestd types a prompt too since `repose say`
// (DECISIONS I-1144) and neither program may import the other. The names
// below are the CLI's own spellings of it; the reasoning, the measured
// numbers and the script itself are in that package.

// promptHead is the start of the prompt's first line with anything in
// it, which the submit script looks for on the screen.
func promptHead(prompt string) string { return agentprompt.Head(prompt) }

// submitShell is what one terminal gives the submit script.
type submitShell = agentprompt.Shell

// submitPromptScript types $repose_pp, presses Enter, and presses Enter
// again while the screen it typed into has not changed.
func submitPromptScript(sh submitShell) string { return agentprompt.Script(sh) }

// submitPromptFor is submitPromptScript for one prompt.
func submitPromptFor(sh submitShell, prompt string) string { return agentprompt.For(sh, prompt) }

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
func tmuxSubmitShell(t string) submitShell { return agentprompt.TmuxShell(t) }

// herdrSubmitShell is the submit script's shell for the herdr pane whose
// id is in the shell variable named pane.
func herdrSubmitShell(pane string) submitShell { return agentprompt.HerdrShell(pane) }
