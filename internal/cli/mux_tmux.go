package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// tmuxMux is the machine's terminals in tmux: the session named after
// the slug that repose-tmux-session.service makes (guest-conventions.md
// "tmux"). Its behaviour is what every command did before I-509.
type tmuxMux struct{}

func (tmuxMux) Name() string { return multiplexer.Tmux }
func (tmuxMux) Unit() string { return "window" }

func (tmuxMux) PickName(ctx context.Context, t sshTarget, slug, agent string) (string, bool, error) {
	return windowNameFor(ctx, t, slug, agent)
}

func (tmuxMux) NamesScript(slug string) string {
	return fmt.Sprintf("tmux list-windows -t %s -F '#window #{window_name}'\n", slug)
}

func (tmuxMux) Label(extra, agent string) string { return windowLabel(extra, agent) }

func (tmuxMux) StartAgent(ctx context.Context, t sshTarget, s agentStart) error {
	return startAgentWindow(ctx, t, s.Slug, s.Name, s.Dir, s.Agent, s.Prompt, s.AttachOnly, s.OnLoading)
}

// Attach starts the session helper beside `tmux attach` (I-195).
func (tmuxMux) Attach(e *Env, a attachReq) error {
	startSessionHelper(e, a.Helper)
	return attachTmux(a.Target, a.Project.Slug, a.Window, a.TZ, a.RepoDir, a.After, a.Renew)
}

func (tmuxMux) PasteScript(slug, window string) string {
	t := "=" + slug + ":" + window
	return fmt.Sprintf(`p=$(tmux list-panes -t %s -F '#{?pane_active,#{pane_id},}' 2>/dev/null | grep .) || exit %d
tmux set-buffer -b repose-paste -- "$f" || exit 1
tmux paste-buffer -p -d -b repose-paste -t "$p" || exit 1
tmux display-message -p -t "$p" '#{window_name}'
`, shQuote(t), pasteExitNoPane)
}

// MessageScript shows text on the session's clients for four seconds
// when it has any, and does nothing otherwise.
func (tmuxMux) MessageScript(slug, text string) string {
	return fmt.Sprintf("if tmux list-clients -t %s -F x 2>/dev/null | grep -q .; then tmux display-message -d 4000 -t %s %s; fi",
		shQuote("="+slug), shQuote("="+slug+":"), shQuote(strings.ReplaceAll(text, "#", "##")))
}

// SessionEnded is tmux answering that the session is missing (exit 1);
// an ssh that could not connect (255) says nothing.
func (tmuxMux) SessionEnded(ctx context.Context, t sshTarget, slug string) (bool, error) {
	_, err := runSSH(ctx, t, "tmux has-session -t "+shQuote("="+slug)+" 2>/dev/null", nil)
	if err == nil {
		return false, nil
	}
	var se *sshError
	if errors.As(err, &se) && se.ExitCode == 1 {
		return true, nil
	}
	return false, err
}

// agentNames are the five agents every guest ships (docs/features/agents.md).
var agentNames = []string{"claude", "opencode", "codex", "gemini", "pi"}

func isAgent(name string) bool {
	return slices.Contains(agentNames, name)
}

const paneIdleWait = 1 * time.Second
const paneIdlePoll = 100 * time.Millisecond

// paneIdleTimeout is a variable so a test can show a load outlasting it.
var paneIdleTimeout = 30 * time.Second

// devShellLoadTimeout bounds how long startAgentWindow keeps waiting while
// the agent wrapper loads the checkout's dev environment, which it marks
// with the pane option devShellLoadingOption (DECISIONS I-259). A first
// load builds the dev shell, which can take minutes, and a prompt typed
// before the agent runs is lost.
const devShellLoadTimeout = 30 * time.Minute

// devShellLoadingOption is the tmux pane option the agent wrapper
// (nix/overlay/agents/devshell.sh) sets to "loading" while it loads the
// dev environment and unsets after (guest-conventions.md "Agent wrappers").
const devShellLoadingOption = "@repose-devshell"

// listWindows returns the names of tmux session slug's windows.
func listWindows(ctx context.Context, t sshTarget, slug string) ([]string, error) {
	out, err := runSSH(ctx, t, fmt.Sprintf("tmux list-windows -t %s -F '#{window_name}'", slug), nil)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// isWindowOf reports whether window is one of agent's windows: the agent's
// own name or "<agent>-N" (the rule guestd's sample.AgentOf applies).
func isWindowOf(agent, window string) bool {
	if window == agent {
		return true
	}
	n, ok := strings.CutPrefix(window, agent+"-")
	if !ok || n == "" {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// nextWindowName is the lowest free name among agent, agent-2, agent-3, ...
// (DECISIONS I-253: any number of agent windows in one guest).
func nextWindowName(agent string, taken func(string) bool) string {
	if !taken(agent) {
		return agent
	}
	for n := 2; ; n++ {
		if name := fmt.Sprintf("%s-%d", agent, n); !taken(name) {
			return name
		}
	}
}

// windowNameFor picks the agent's window name: the agent's own name, else
// the lowest free "<agent>-N", each behind "<checkout>/" in another
// checkout (windowLabel, I-480) (07-cli.md §5.5 step 7 and §6's "second
// prompt while agent window exists", DECISIONS I-253). othersOpen says
// another window of the same agent is open, which is what the
// shared-working-tree warning is for.
func windowNameFor(ctx context.Context, t sshTarget, slug, agent string) (name string, othersOpen bool, err error) {
	windows, err := listWindows(ctx, t, slug)
	if err != nil {
		return "", false, err
	}
	name, othersOpen = pickWindow(windowLabel(t.Checkout, agent), windows, nil)
	return name, othersOpen, nil
}

// pickWindow is windowNameFor's choice over a known window list;
// alsoTaken names what a worktree run must skip besides open windows.
func pickWindow(agent string, windows []string, alsoTaken func(string) bool) (name string, othersOpen bool) {
	open := map[string]bool{}
	for _, w := range windows {
		open[w] = true
		if isWindowOf(agent, w) {
			othersOpen = true
		}
	}
	name = nextWindowName(agent, func(n string) bool {
		return open[n] || (alsoTaken != nil && alsoTaken(n))
	})
	return name, othersOpen
}

// needsClaudeLogin implements the check in 07-cli.md §5.5 step 7: the
// claude agent with no on-guest credentials file and no
// CLAUDE_CODE_OAUTH_TOKEN secret must be attached to, not sent a prompt.
func needsClaudeLogin(ctx context.Context, t sshTarget, hasOAuthSecret bool) (bool, error) {
	if hasOAuthSecret {
		return false, nil
	}
	// Non-empty, not just present: with the Claude login share (I-278) the
	// file is always there, bind-mounted, and empty until the user's first
	// /login in any of their machines. A size check on the guest only; the
	// file is never read or copied (DECISIONS R2-8).
	err := runSSHOK(ctx, t, "test -s ~/.claude/.credentials.json")
	return err != nil, nil // a non-zero test means no login yet
}

// startAgentWindow opens the tmux window and, unless the caller says to
// attach instead, waits for the pane to go idle and sends the prompt.
// binary is the process tmux launches and the name pane_current_command
// must settle on before the prompt is sent; production passes the agent's
// real binary name, tests substitute a stand-in. dir is the window's
// working directory as the guest's shell spells it: the checkout's
// "~/<name>", a worktree's "~/<name>-worktree-<N>" (I-253), or "" to find
// the checkout in the guest (checkoutVar, I-368). onLoading, when not nil, is
// called once if the wrapper says it is loading the dev environment.
// For claude, the same ssh first marks that folder trusted in
// ~/.claude.json (claudeTrustScript, I-486), and a trust dialog that shows
// anyway is never typed into: the error is an *agentDialogError.
func startAgentWindow(ctx context.Context, t sshTarget, slug, windowName, dir, binary, prompt string, attachOnly bool, onLoading func()) error {
	if _, err := runSSH(ctx, t, agentWindowCommand(slug, t.Checkout, windowName, dir, binary), nil); err != nil {
		return err
	}
	if attachOnly {
		return nil
	}
	if err := waitPaneIdle(ctx, t, slug, windowName, binary, onLoading); err != nil {
		return err
	}
	if _, err := runSSH(ctx, t, fmt.Sprintf("tmux send-keys -t %s:%s -l %s", slug, windowName, shQuote(prompt)), nil); err != nil {
		return err
	}
	_, err := runSSH(ctx, t, fmt.Sprintf("tmux send-keys -t %s:%s Enter", slug, windowName), nil)
	return err
}

// agentWindowCommand is the shell startAgentWindow runs to open the
// window, with claude's trust flag set first.
func agentWindowCommand(slug, extra, windowName, dir, binary string) string {
	prefix, cdir := "", dir
	if dir == "" {
		prefix, cdir = checkoutVar(slug, extra), `"$repose_co"`
	}
	if binary == "claude" {
		prefix += claudeTrustScript(cdir)
	}
	return prefix + fmt.Sprintf("tmux new-window -t %s -n %s -c %s -d %s", slug, windowName, cdir, shQuote(binary))
}

// claudeTrustScript is shell, run before tmux starts claude in dir (a
// shell word: "~/<name>..." or "$repose_co"), that sets
// projects["<dir, symlinks resolved>"].hasTrustDialogAccepted to true in
// ~/.claude.json, the flag Claude Code (2.1.283) reads to skip its "Is
// this a project you trust?" dialog. That dialog's default is "No, exit",
// so the prompt and Enter `repose run` types would quit Claude Code
// (I-486). Only folders repose itself starts an agent in get here: the
// checkout, a --worktree directory, another checkout. A false Claude Code
// wrote after an earlier refusal is replaced, since this run is the user
// asking for an agent there; every other key in the file is kept. The
// file is written only when the flag is not already true, atomically, and
// never when it is not valid JSON. Best effort: the window starts whatever
// happens here, and waitPaneIdle catches a dialog that shows anyway.
func claudeTrustScript(dir string) string {
	return fmt.Sprintf(`{ repose_tp=$(cd %s 2>/dev/null && pwd -P) && command -v jq >/dev/null && repose_cj="$HOME/.claude.json" && {
  if [ ! -s "$repose_cj" ]; then
    repose_tt=$(mktemp "$repose_cj.XXXXXX") && jq -n --arg p "$repose_tp" '{projects: {($p): {hasTrustDialogAccepted: true}}}' > "$repose_tt" && chmod 600 "$repose_tt" && mv -f "$repose_tt" "$repose_cj"
  elif jq -e --arg p "$repose_tp" '(.projects // {})[$p].hasTrustDialogAccepted != true' "$repose_cj" >/dev/null 2>&1; then
    repose_tt=$(mktemp "$repose_cj.XXXXXX") && jq --arg p "$repose_tp" '.projects[$p].hasTrustDialogAccepted = true' "$repose_cj" > "$repose_tt" && chmod 600 "$repose_tt" && mv -f "$repose_tt" "$repose_cj"
  fi
  [ -z "${repose_tt:-}" ] || rm -f "$repose_tt"
}; } >/dev/null 2>&1 || true
`, dir)
}

// agentDialogs are lines a Claude Code dialog shows that a typed prompt
// must not answer: its folder trust dialog, in the wording of 2.1.283 and
// of earlier releases. Matching them only stops the CLI from typing; it
// never presses a key in the dialog (I-486, I-283's rejected answer).
var agentDialogs = []string{
	"Yes, I trust this folder",
	"Is this a project you created or one you trust",
	"Do you trust the files in this folder?",
}

// agentDialogError says the agent's pane settled on a dialog, so the
// prompt was not typed.
type agentDialogError struct{}

func (*agentDialogError) Error() string {
	return "Claude Code is asking whether you trust the folder it started in"
}

// paneShowsDialog reports whether capture holds one of agentDialogs.
func paneShowsDialog(capture string) bool {
	for _, d := range agentDialogs {
		if strings.Contains(capture, d) {
			return true
		}
	}
	return false
}

// waitPaneIdle polls pane_current_command until it names binary and its
// captured content has not changed for paneIdleWait. While the pane
// carries devShellLoadingOption the agent has not started yet, and the
// wait goes on past paneIdleTimeout, up to devShellLoadTimeout (I-259).
// A pane that settles on a trust dialog (paneShowsDialog) returns an
// *agentDialogError instead of nil, so nothing is typed into it (I-486).
func waitPaneIdle(ctx context.Context, t sshTarget, slug, windowName, binary string, onLoading func()) error {
	start := time.Now()
	deadline := start.Add(paneIdleTimeout)
	var lastCapture string
	var stableSince time.Time
	sawLoading := false
	for {
		cmdOut, err := runSSH(ctx, t, fmt.Sprintf("tmux display -p -t %s:%s '#{pane_current_command} #{%s}'", slug, windowName, devShellLoadingOption), nil)
		if err != nil {
			return err
		}
		current, marker, _ := strings.Cut(strings.TrimSpace(string(cmdOut)), " ")
		loading := strings.TrimSpace(marker) == "loading"
		capture, err := runSSH(ctx, t, fmt.Sprintf("tmux capture-pane -p -t %s:%s", slug, windowName), nil)
		if err != nil {
			return err
		}
		if loading {
			stableSince = time.Time{}
			if !sawLoading && onLoading != nil {
				onLoading()
			}
			sawLoading = true
			deadline = time.Now().Add(paneIdleTimeout)
			if limit := start.Add(devShellLoadTimeout); deadline.After(limit) {
				deadline = limit
			}
		} else if current == binary {
			if string(capture) == lastCapture {
				if !stableSince.IsZero() && time.Since(stableSince) >= paneIdleWait {
					if paneShowsDialog(string(capture)) {
						return &agentDialogError{}
					}
					return nil
				}
				if stableSince.IsZero() {
					stableSince = time.Now()
				}
			} else {
				stableSince = time.Time{}
			}
		} else {
			stableSince = time.Time{}
		}
		lastCapture = string(capture)
		if time.Now().After(deadline) {
			if paneShowsDialog(string(capture)) {
				return &agentDialogError{}
			}
			return nil // best effort: send the prompt anyway rather than hang forever
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(paneIdlePoll):
		}
	}
}

// capturePane is a small helper the integration test uses to assert the
// prompt landed inside the agent's pane.
func capturePane(ctx context.Context, t sshTarget, slug, windowName string) (string, error) {
	out, err := runSSH(ctx, t, fmt.Sprintf("tmux capture-pane -p -t %s:%s", slug, windowName), nil)
	return string(out), err
}

// shQuote single-quotes s for a POSIX shell command line, the way the
// doc's literal `tmux send-keys -l '<prompt>'` needs when prompt itself
// may contain spaces or shell metacharacters.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// agentWorktree is where a `repose run --worktree` agent works (DECISIONS
// I-253, I-342): a git worktree of the guest's checkout beside it, outside
// the synced tree, on its own branch.
type agentWorktree struct {
	Window string // tmux window or herdr agent name, "<agent>" or "<agent>-N"
	N      int    // the worktree's number, 1 and up
	Dir    string // "~/<checkout>-worktree-<N>", as the guest's shell spells it
	// Checkout is the checkout's name under the home (I-368).
	Checkout string
	Branch   string // "worktree-<N>"
	Base     string // the checkout's HEAD the branch starts from
	Dirty    bool   // the checkout had uncommitted changes, which the worktree lacks
	Env      int    // .env files copied from the checkout (I-343)
}

// worktreeDir and worktreeBranch are the only places the worktree layout
// is spelled (DECISIONS I-342, interfaces/guest-conventions.md). The
// branch has no "repose/" of its own: the laptop's `git fetch repose`
// already files it under repose/, as repose/worktree-<N>.
func worktreeDir(checkout string, n int) string {
	return fmt.Sprintf("~/%s-worktree-%d", checkout, n)
}
func worktreeBranch(n int) string { return fmt.Sprintf("worktree-%d", n) }

// worktreeProbeScript reports, in one ssh, the agent names in use (from
// names, the muxer's NamesScript), the checkout's HEAD and whether it is
// dirty, and which worktree directories and worktree-N branches already
// exist, so the number skips both.
func worktreeProbeScript(slug, extra, names string) string {
	return fmt.Sprintf(`set -e
%[3]s%[2]s[ "$repose_co" != "$HOME" ] || { echo '#nogit'; exit 0; }
echo "#checkout ${repose_co##*/}"
cd "$repose_co" && [ -e .git ] || { echo '#nogit'; exit 0; }
h=$(git rev-parse -q --verify HEAD) || { echo '#nohead'; exit 0; }
echo "#head $h"
[ -z "$(git status --porcelain 2>/dev/null)" ] || echo '#dirty'
git for-each-ref --format='#branch %%(refname:strip=2)' 'refs/heads/worktree-*'
for p in "$repose_co"-worktree-*; do [ -e "$p" ] && echo "#dir ${p##*/}"; done
true`, slug, checkoutVar(slug, extra), names)
}

// worktreeAddScript makes the worktree and copies into it the checkout's
// gitignored .env files (I-343), the ones a sync carries (I-197), which
// `git worktree add` leaves behind. It prints "#env" once per file copied.
// Ignored directories come out of ls-files collapsed (node_modules/), so
// a dependency tree costs one line and is never looked into.
func worktreeAddScript(wt *agentWorktree) string {
	return fmt.Sprintf(`set -e
cd %[1]s
git worktree add -q -b %[2]s %[3]s %[4]s
w=%[3]s
git ls-files -z --others --ignored --exclude-standard --directory | tr '\0' '\n' | while IFS= read -r f; do
  case "${f##*/}" in .env|.env.*) ;; *) continue ;; esac
  [ -f "$f" ] && [ ! -L "$f" ] && [ ! -e "$w/$f" ] || continue
  mkdir -p "$w/$(dirname "$f")" && cp -p "$f" "$w/$f" && echo '#env'
done
true`, homeShell(wt.Checkout), shQuote(wt.Branch), wt.Dir, wt.Base)
}

// prepareWorktree picks the window name and the worktree number for a
// worktree run and creates the worktree: `git worktree add -b worktree-<N>
// ~/<slug>-worktree-<N> HEAD`, plus the checkout's .env files. It never
// reuses a worktree: a number whose directory or branch exists is skipped,
// so each --worktree run starts fresh from HEAD.
func prepareWorktree(ctx context.Context, t sshTarget, slug, agent string) (*agentWorktree, error) {
	return prepareWorktreeWith(ctx, t, slug, agent, tmuxMux{})
}

// prepareWorktreeWith is prepareWorktree on the machine's multiplexer,
// whose names the agent's name skips (I-509).
func prepareWorktreeWith(ctx context.Context, t sshTarget, slug, agent string, m muxer) (*agentWorktree, error) {
	out, err := runSSH(ctx, t, worktreeProbeScript(slug, t.Checkout, m.NamesScript(slug)), nil)
	if err != nil {
		step := "list the guest's tmux windows"
		if m.Name() == multiplexer.Herdr {
			step = "list herdr's agents on the machine"
		}
		return nil, stepFailed(step, err, "")
	}
	var windows []string
	taken := map[string]bool{}
	wt := &agentWorktree{}
	for _, l := range nonEmptyLines(string(out)) {
		switch l {
		case "#nogit":
			if wt.Checkout == "" {
				return nil, exitf(ExitUsage, "--worktree needs a git checkout on the machine, and %s has none yet. `repose sync` sends yours; or run without --worktree.", slug)
			}
			return nil, exitf(ExitUsage, "--worktree needs a git checkout on the machine, and %s is not one. Run without --worktree.", tildePath(wt.Checkout))
		case "#nohead":
			return nil, exitf(ExitUsage, "%s on the machine has no commits yet, so there is nothing to start a worktree from. Commit first, or run without --worktree.", tildePath(wt.Checkout))
		case "#dirty":
			wt.Dirty = true
		default:
			if w, ok := strings.CutPrefix(l, "#window "); ok {
				windows = append(windows, w)
			} else if c, ok := strings.CutPrefix(l, "#checkout "); ok {
				wt.Checkout = c
			} else if h, ok := strings.CutPrefix(l, "#head "); ok {
				wt.Base = h
			} else if b, ok := strings.CutPrefix(l, "#branch "); ok {
				taken[b] = true
			} else if d, ok := strings.CutPrefix(l, "#dir "); ok {
				if n, ok := strings.CutPrefix(d, wt.Checkout+"-"); ok {
					taken[n] = true
				}
			}
		}
	}
	wt.Window, _ = pickWindow(m.Label(t.Checkout, agent), windows, nil)
	wt.N = nextWorktree(taken)
	wt.Dir = worktreeDir(wt.Checkout, wt.N)
	wt.Branch = worktreeBranch(wt.N)
	out, err = runSSH(ctx, t, worktreeAddScript(wt), nil)
	if err != nil {
		return nil, stepFailed("create the worktree "+wt.Dir+" in the guest", err, "")
	}
	for _, l := range nonEmptyLines(string(out)) {
		if l == "#env" {
			wt.Env++
		}
	}
	return wt, nil
}

// nextWorktree is the lowest N >= 1 whose worktree-<N> is not taken, as a
// directory name or a branch.
func nextWorktree(taken map[string]bool) int {
	n := 1
	for taken[worktreeBranch(n)] {
		n++
	}
	return n
}
