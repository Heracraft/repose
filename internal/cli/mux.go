package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/multiplexer"
)

// The multiplexer seam (DECISIONS I-509). A project's terminals run in
// tmux or in herdr; what runs is the guest's answer, since a project
// switched while running keeps its old multiplexer until it stops
// (I-502). Every command that opens, lists or types into the machine's
// terminals asks muxFor first and goes through the answer.

// muxer is what a command needs from the machine's terminals.
type muxer interface {
	// Name is multiplexer.Tmux or multiplexer.Herdr.
	Name() string
	// Unit is the word messages use for one terminal: "window" or "tab".
	Unit() string
	// PickName chooses the next agent's name in the folder t works in
	// (the checkout, or t.Checkout), and says whether another of the
	// same agent already works there (the shared-tree warning).
	PickName(ctx context.Context, t sshTarget, slug, agent string) (name string, othersOpen bool, err error)
	// NamesScript is shell that prints "#window <name>" for each agent
	// name in use, which a worktree run skips. It rides the worktree
	// probe's ssh and fails it when the multiplexer does not answer.
	NamesScript(slug string) string
	// Label is the name agent names start from: "<checkout>/<agent>" in
	// another checkout under tmux (I-480), the agent under herdr, whose
	// agent names take no slash.
	Label(extra, agent string) string
	// StartAgent opens the agent's terminal and, unless s.AttachOnly,
	// types the prompt. A dialog the prompt must not answer is an
	// *agentDialogError.
	StartAgent(ctx context.Context, t sshTarget, s agentStart) error
	// Attach is step 8: the user's terminal on the machine's session.
	Attach(e *Env, a attachReq) error
	// PasteScript is shell, run after pasteSaveScript set $f, that types
	// the path into the focused terminal (or window) and prints where;
	// it exits pasteExitNoPane when there is none.
	PasteScript(slug, window string) string
	// TypeScript is shell that types text and Enter into the named
	// window's (or herdr agent's) terminal, or exits typeExitNoWindow
	// when there is none (`run -w`, I-639).
	TypeScript(slug, window, text string) string
	// CloseScript is shell that closes the named window (or herdr
	// agent's pane), or exits typeExitNoWindow when there is none
	// (`stop -w`, I-639).
	CloseScript(slug, window string) string
	// MessageScript is shell that shows text to whoever is attached.
	MessageScript(slug, text string) string
	// SessionEnded reports whether a temporary machine's session is
	// over (I-352). An error means it cannot say.
	SessionEnded(ctx context.Context, t sshTarget, slug string) (bool, error)
}

// typeExitNoWindow is TypeScript's and CloseScript's exit when the
// window is not there.
const typeExitNoWindow = 3

// agentStart is one `repose run -p PROMPT` agent.
type agentStart struct {
	Slug  string
	Agent string // one of agentNames; also the binary
	Name  string // the window or herdr agent name PickName chose
	// Dir is the folder as the guest's shell spells it ("~/<name>",
	// "~/<name>-worktree-<N>"), "" for the checkout (checkoutVar).
	Dir string
	// Worktree says Dir is a `--worktree` worktree (I-253): herdr opens
	// it with `herdr worktree open` so it groups under the repository.
	Worktree bool
	Prompt   string
	// AttachOnly opens the terminal and starts the agent without typing
	// into it: Claude Code's login comes first. A Prompt then goes to a
	// waiter on the machine that types it after the login (I-607).
	AttachOnly bool
	OnLoading  func() // called once while the dev shell loads (I-259)
	// MCPApprovals are the laptop's .mcp.json answers for the
	// repository, written with claude's folder trust (I-556).
	MCPApprovals mcpApprovals
}

// attachReq is everything an attach needs.
type attachReq struct {
	// Ctx is the command's context: Ctrl-C ends it. The herdr attach
	// follows it until the client or the helper owns the terminal.
	Ctx     context.Context
	Target  sshTarget
	Project *Project // Slug always; ExpiresAt when known
	Window  string   // the agent `run -p PROMPT` just started, "" for the session
	// Named says the user named Window (`attach --window`, I-606): a
	// window that is not there is refused, exit 2, instead of the
	// session.
	Named   bool
	TZ      string
	RepoDir string
	After   func() // runs when an attach the CLI waited on returns
	Renew   func(context.Context) error
	// Release prints the sidebar adds' failures held while the attach
	// owns the terminal (herdrSyncFor). The caller runs it after Attach
	// returns; the sidebar path, which hands nothing over, runs it once
	// its adds are done.
	Release func()
	// Helper is the session helper's options; on herdr the helper runs
	// in this process (the sidebar path) or beside a child client.
	Helper sessionOptions
}

// muxFor asks the guest what runs its terminals now, in one ssh over the
// connection the command already holds (muxFromProbe has the rule). An
// error is herdr down on a herdr machine; the command stops there.
func muxFor(ctx context.Context, t sshTarget, p *Project) (muxer, error) {
	a, err := muxProbe(ctx, t)
	return muxFromProbe(a, err, p)
}

// muxProbeAnswer is the guest's answer to muxStateScript.
type muxProbeAnswer struct {
	Herdr string // repose-herdr-server's ActiveState, "inactive" without the unit
	Boot  string // project.json's multiplexer for this boot, "" when absent
	Tmux  bool   // a tmux server answers
}

// muxProbe is muxFor's guest question, a variable so a test can answer.
var muxProbe = func(ctx context.Context, t sshTarget) (muxProbeAnswer, error) {
	out, err := runSSH(ctx, t, muxStateScript, nil)
	if err != nil {
		return muxProbeAnswer{}, err
	}
	return parseMuxProbe(string(out)), nil
}

// muxStateScript prints what muxFromProbe decides on: the herdr unit's
// state, this boot's multiplexer and whether tmux answers.
var muxStateScript = `printf '#herdr %s\n' "$(systemctl --user show -p ActiveState --value ` + multiplexer.HerdrUnit + ` 2>/dev/null)"
printf '#boot %s\n' "$(jq -r '.multiplexer // empty' "$HOME/.repose/project.json" 2>/dev/null)"
if tmux list-sessions >/dev/null 2>&1; then echo '#tmux'; fi
true`

func parseMuxProbe(out string) muxProbeAnswer {
	var a muxProbeAnswer
	for _, l := range nonEmptyLines(out) {
		l = strings.TrimSpace(l)
		switch {
		case l == "#tmux":
			a.Tmux = true
		case strings.HasPrefix(l, "#herdr "):
			a.Herdr = strings.TrimSpace(strings.TrimPrefix(l, "#herdr "))
		case strings.HasPrefix(l, "#boot "):
			a.Boot = strings.TrimSpace(strings.TrimPrefix(l, "#boot "))
		}
	}
	return a
}

// herdrUp is the unit states that mean herdr is or is about to be there:
// activating covers Restart's 5 s wait after a crash.
func herdrUp(state string) bool {
	return state == "active" || state == "activating" || state == "reloading"
}

// muxFromProbe is the rule, first match wins: an ssh that failed takes
// the stored value; a herdr unit that is up is herdr; a tmux server that
// answers is tmux; a boot that named herdr with neither is herdr down,
// an error naming the unit's state; anything else is tmux (a base with
// no herdr unit included).
func muxFromProbe(a muxProbeAnswer, err error, p *Project) (muxer, error) {
	switch {
	case err != nil:
		if p == nil {
			return tmuxMux{}, nil
		}
		return muxByName(p.Multiplexer), nil
	case herdrUp(a.Herdr):
		return herdrMux{}, nil
	case a.Tmux:
		return tmuxMux{}, nil
	case multiplexer.Normalize(a.Boot) == multiplexer.Herdr:
		slug := "the machine"
		if p != nil {
			slug = p.Slug
		}
		state := a.Herdr
		if state == "" {
			state = "unknown"
		}
		return nil, exitf(ExitGeneric, "herdr is not running on %s: %s is %s. `repose stop %s` and `repose start %s` start it again.", slug, strings.TrimSuffix(multiplexer.HerdrUnit, ".service"), state, slug, slug)
	}
	return tmuxMux{}, nil
}

// muxProbeScript exits 0 only when the herdr session unit runs: the
// condition the guest-side scripts (messages, sync, status) test.
var muxProbeScript = "systemctl --user -q is-active " + multiplexer.HerdrUnit + " 2>/dev/null"

// herdrUpScript exits 0 when the herdr session unit is in one of
// herdrUp's states: running, or about to (activating covers the server's
// own workspace step and Restart's wait).
var herdrUpScript = `case "$(systemctl --user show -p ActiveState --value ` + multiplexer.HerdrUnit + ` 2>/dev/null)" in active|activating|reloading) true ;; *) false ;; esac`

// muxByName is the multiplexer for a stored or remembered value.
func muxByName(name string) muxer {
	if multiplexer.Normalize(name) == multiplexer.Herdr {
		return herdrMux{}
	}
	return tmuxMux{}
}

// checkMultiplexerFlag is --multiplexer's check: tmux, herdr or unset;
// anything else exits 2 naming both.
func checkMultiplexerFlag(v string) error {
	if v == "" || multiplexer.Valid(v) {
		return nil
	}
	return cobraUsageError{fmt.Errorf("--multiplexer takes %s, got %q", strings.Join(multiplexer.Names, " or "), v)}
}

// multiplexerFlagHelp is --multiplexer's help line (features/run-and-attach.md).
const multiplexerFlagHelp = "the multiplexer `NAME`, tmux or herdr, that runs this machine's terminals from its next start (default: config.toml's default_multiplexer, else herdr from a herdr pane, else tmux)"

// addMultiplexerFlag adds --multiplexer to run and sync.
func addMultiplexerFlag(cmd *cobra.Command, v *string) {
	cmd.Flags().StringVar(v, "multiplexer", "", multiplexerFlagHelp)
	_ = cmd.RegisterFlagCompletionFunc("multiplexer", cobra.FixedCompletions(multiplexer.Names, cobra.ShellCompDirectiveNoFileComp))
}

// inHerdrPane says the CLI runs in a laptop herdr pane: herdr sets
// HERDR_ENV=1 in its panes (cli-config.md "The laptop's herdr").
func inHerdrPane() bool { return os.Getenv("HERDR_ENV") == "1" }

// pickMultiplexer is the multiplexer a new project gets (I-502): the
// flag, else config.toml's default_multiplexer, else herdr from a
// laptop herdr pane, else tmux; a temporary project too (I-602).
// auto says the HERDR_ENV rule chose, whose refusal by the base gate
// falls back to tmux without a word.
func pickMultiplexer(flag, config string) (name string, auto bool) {
	switch {
	case flag != "":
		return flag, false
	case config != "":
		return config, false
	case inHerdrPane():
		return multiplexer.Herdr, true
	}
	return multiplexer.Tmux, false
}

// baseGateRefusal is the api's answer to herdr on a base without it
// (api.md "The base gate"): 409 with detail.reason base_update_needed.
func baseGateRefusal(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) && ae.Code == "conflict" && ae.Detail != nil && ae.Detail["reason"] == "base_update_needed" {
		return ae, true
	}
	return nil, false
}

// switchMultiplexer is --multiplexer on an existing project: a PATCH
// when the value differs, before anything else the run does (I-502). A
// running project prints when the change takes effect; a stopped one
// starts on it and says nothing more. The same value sends nothing.
func switchMultiplexer(ctx context.Context, e *Env, p *Project, want string) error {
	have := multiplexer.Normalize(p.Multiplexer)
	if want == "" || want == have {
		return nil
	}
	got, err := e.Client.PatchProject(ctx, p.ID, PatchProjectRequest{Multiplexer: &want})
	if ae, ok := baseGateRefusal(err); ok {
		return exitf(ExitGeneric, "%s", ae.Message)
	}
	if err != nil {
		return err
	}
	if got != nil && got.Multiplexer != "" {
		p.Multiplexer = got.Multiplexer
	} else {
		p.Multiplexer = want
	}
	if p.State == "running" {
		_, _ = fmt.Fprintln(e.Out, switchLine(p.Slug, want, have))
	}
	return nil
}

// switchLine is what a switch of a running machine prints.
func switchLine(slug, want, have string) string {
	return fmt.Sprintf("%s uses %s from its next start; %s runs until then.", slug, want, have)
}
