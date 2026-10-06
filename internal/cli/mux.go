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
	// Names lists the agent names in use, which a worktree run skips.
	Names(ctx context.Context, t sshTarget, slug string) ([]string, error)
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
	// MessageScript is shell that shows text to whoever is attached.
	MessageScript(slug, text string) string
	// SessionEnded reports whether a temporary machine's session is
	// over (I-352). An error means it cannot say.
	SessionEnded(ctx context.Context, t sshTarget, slug string) (bool, error)
}

// agentStart is one `repose run PROMPT` agent.
type agentStart struct {
	Slug  string
	Agent string // one of agentNames; also the binary
	Name  string // the window or herdr agent name PickName chose
	// Dir is the folder as the guest's shell spells it ("~/<name>",
	// "~/<name>-worktree-<N>"), "" for the checkout (checkoutVar).
	Dir string
	// Worktree says Dir is a `--worktree` worktree (I-253): herdr opens
	// it with `herdr worktree open` so it groups under the repository.
	Worktree   bool
	Prompt     string
	AttachOnly bool   // open the terminal and start the agent, type nothing
	OnLoading  func() // called once while the dev shell loads (I-259)
}

// attachReq is everything an attach needs.
type attachReq struct {
	Target  sshTarget
	Project *Project // Slug always; ExpiresAt when known
	Window  string   // the agent `run PROMPT` just started, "" for the session
	TZ      string
	RepoDir string
	After   func() // runs when an attach the CLI waited on returns
	Renew   func(context.Context) error
	// Helper is the session helper's options; on herdr the helper runs
	// in this process (the sidebar path) or beside a child client.
	Helper sessionOptions
}

// muxFor asks the guest what runs its terminals now: herdr when
// repose-herdr-server is active, tmux otherwise (also on a base with no
// such unit, and when the probe itself fails). One ssh over the
// connection the command already holds.
func muxFor(ctx context.Context, t sshTarget) muxer {
	if muxProbe(ctx, t) == multiplexer.Herdr {
		return herdrMux{}
	}
	return tmuxMux{}
}

// muxProbe is muxFor's guest question, a variable so a test can answer.
var muxProbe = func(ctx context.Context, t sshTarget) string {
	err := runSSHOK(ctx, t, muxProbeScript)
	if err == nil {
		return multiplexer.Herdr
	}
	return multiplexer.Tmux
}

// muxProbeScript exits 0 only when the herdr session unit runs.
var muxProbeScript = "systemctl --user -q is-active " + multiplexer.HerdrUnit + " 2>/dev/null"

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
const multiplexerFlagHelp = "tmux|herdr: what runs this machine's terminals, from its next start (default: config.toml's default_multiplexer, else tmux)"

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
// laptop herdr pane when the project is not temporary, else tmux.
// auto says the HERDR_ENV rule chose, whose refusal by the base gate
// falls back to tmux without a word.
func pickMultiplexer(flag, config string, temp bool) (name string, auto bool) {
	switch {
	case flag != "":
		return flag, false
	case config != "":
		return config, false
	case inHerdrPane() && !temp:
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
	return fmt.Sprintf("%s uses %s from its next start; %s keeps running until then.", slug, want, have)
}
