package cli

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/heracraft/repose/internal/multiplexer"
)

// `repose stop -w WINDOW` (DECISIONS I-639) closes one agent's window and
// leaves the machine running. Windows from `run -p` and worktrees from
// `run --worktree` only accumulated before: nothing closed a window
// without an attach, and nothing removed a worktree. The window's
// worktree (a `<checkout>-worktree-N` folder no other window works in)
// goes with it only when it has no uncommitted file and this laptop has
// its branch tip, after the same `git fetch repose` a stop makes first.
// Otherwise it stays, and the line says why.

// windowWorktree is what the machine says about the window's folder
// before the close: a linked worktree of the checkout, or nothing.
type windowWorktree struct {
	Dir    string // absolute path on the machine
	Branch string
	Tip    string
	Dirty  int      // files git status lists
	Others []string // other windows whose folder is in it
}

// stopWindowProbeScript prints "#wt DIR\tBRANCH\tTIP\tDIRTY" when the
// window's folder is in a `-worktree-N` linked worktree, then "#other
// NAME" for each other window working in it; it exits typeExitNoWindow
// when the window is not there.
func stopWindowProbeScript(slug, window string) string {
	t := shQuote(tmuxWindowTarget(slug, window))
	return fmt.Sprintf(`tmux has-session -t %[1]s 2>/dev/null || exit %[3]d
d=$(tmux display-message -p -t %[1]s '#{pane_current_path}')
top=$(git -C "$d" rev-parse --show-toplevel 2>/dev/null) || exit 0
gd=$(git -C "$top" rev-parse --absolute-git-dir) || exit 0
cm=$(cd "$(git -C "$top" rev-parse --path-format=absolute --git-common-dir)" && pwd -P) || exit 0
[ "$gd" != "$cm" ] || exit 0
case "${top##*/}" in *-worktree-[0-9]*) ;; *) exit 0 ;; esac
b=$(git -C "$top" symbolic-ref --short -q HEAD || true)
h=$(git -C "$top" rev-parse HEAD) || exit 0
n=$(git -C "$top" status --porcelain | wc -l | tr -d ' ')
printf '#wt %%s\t%%s\t%%s\t%%s\n' "$top" "$b" "$h" "$n"
me=$(tmux display-message -p -t %[1]s '#{window_id}')
tmux list-panes -s -t %[2]s -F '#{window_id}	#{window_name}	#{pane_current_path}' | while IFS='	' read -r id name p; do
  [ "$id" != "$me" ] || continue
  case "$p/" in "$top"/*) printf '#other %%s\n' "$name" ;; esac
done | sort -u
`, t, shQuote("="+slug), typeExitNoWindow)
}

// parseStopWindowProbe reads stopWindowProbeScript's output; nil when
// the folder is no worktree of the kind run --worktree makes.
func parseStopWindowProbe(out string) *windowWorktree {
	var w *windowWorktree
	for _, l := range nonEmptyLines(out) {
		if v, ok := strings.CutPrefix(l, "#wt "); ok {
			f := strings.Split(v, "\t")
			if len(f) != 4 {
				return nil
			}
			n, _ := strconv.Atoi(f[3])
			w = &windowWorktree{Dir: f[0], Branch: f[1], Tip: f[2], Dirty: n}
		} else if v, ok := strings.CutPrefix(l, "#other "); ok && w != nil {
			w.Others = append(w.Others, v)
		}
	}
	return w
}

// worktreeRemoveScript removes the worktree from its checkout; git
// refuses one with changes, which the probe already ruled out.
func worktreeRemoveScript(dir string) string {
	return fmt.Sprintf(`top=%[1]s
cm=$(cd "$(git -C "$top" rev-parse --path-format=absolute --git-common-dir)" && pwd -P) || exit 1
git -C "${cm%%/.git}" worktree remove "$top"
`, shQuote(dir))
}

// laptopHasCommit reports whether a checkout of p on this laptop has the
// commit on one of its refs.
func laptopHasCommit(e *Env, p *Project, tip string) bool {
	for _, root := range e.laptopFolders(p) {
		out, err := gitCmd(root, "for-each-ref", "--count=1", "--contains", tip, "--format=%(refname)")
		if err == nil && strings.TrimSpace(out) != "" {
			return true
		}
	}
	return false
}

// keptBecause is why the worktree stays, or "" when it can go.
func keptBecause(w *windowWorktree, onLaptop bool) string {
	switch {
	case len(w.Others) > 0:
		return joinNames(w.Others) + " " + isAre(len(w.Others)) + " working in it"
	case w.Dirty > 0:
		return count(w.Dirty, "file") + " not committed"
	case !onLaptop:
		b := w.Branch
		if b == "" {
			b = w.Tip[:min(7, len(w.Tip))]
		}
		return b + " is not fetched to this laptop"
	}
	return ""
}

// StopWindowOptions is `repose stop [PROJECT] -w WINDOW [--yes]`.
type StopWindowOptions struct {
	Project string
	Window  string
	Yes     bool
	Confirm func(prompt string) (bool, error)
}

// StopWindowCmd closes one window (or herdr agent) and, on tmux, its
// worktree when nothing in it would be lost.
func StopWindowCmd(ctx context.Context, e *Env, o StopWindowOptions) error {
	project, err := requireRunningProject(ctx, e, o.Project)
	if err != nil {
		return err
	}
	if busy := busyWindow(project, o.Window); busy != "" && !o.Yes {
		clause := fmt.Sprintf("%s has %s.", project.Slug, busy)
		if o.Confirm == nil {
			return exitf(ExitUsage, "%s No terminal to confirm closing it on; pass --yes.", clause)
		}
		if err := confirmOr(o.Confirm, fmt.Sprintf("%s Closing ends it. Close %s? [y/N] ", clause, o.Window), "Nothing closed."); err != nil {
			return err
		}
	}
	target, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	mux, err := muxFor(ctx, target, project)
	if err != nil {
		return err
	}
	var wt *windowWorktree
	if mux.Name() != multiplexer.Herdr {
		out, err := runSSH(ctx, target, stopWindowProbeScript(project.Slug, o.Window), nil)
		if err != nil {
			var se *sshError
			if errors.As(err, &se) && se.ExitCode == typeExitNoWindow {
				return noWindow(project.Slug, o.Window, mux.Unit())
			}
			return stepFailed("read the "+o.Window+" window on "+project.Slug, err, "")
		}
		wt = parseStopWindowProbe(string(out))
	}
	if _, err := runSSH(ctx, target, mux.CloseScript(project.Slug, o.Window), nil); err != nil {
		var se *sshError
		if errors.As(err, &se) && se.ExitCode == typeExitNoWindow {
			return noWindow(project.Slug, o.Window, mux.Unit())
		}
		return stepFailed("close "+o.Window+" on "+project.Slug, err, "")
	}
	line := fmt.Sprintf("Closed %s on %s.", o.Window, project.Slug)
	if wt != nil {
		where := tildeGuestPath(wt.Dir)
		why := ""
		if len(wt.Others) == 0 && wt.Dirty == 0 {
			// The fetch a stop makes, so a finished agent's branch is
			// on the laptop before its worktree goes.
			if jobs := e.fetchJobsFor([]*Project{project}); len(jobs) > 0 {
				runFetches(ctx, jobs)
				for _, j := range jobs {
					if w := j.fetchFailure("removing " + where); w != "" {
						e.warn("%s", w)
					}
					if l := j.fetchedLine(); l != "" {
						_, _ = fmt.Fprintln(e.Out, l)
					}
				}
			}
		}
		why = keptBecause(wt, laptopHasCommit(e, project, wt.Tip))
		switch {
		case why != "":
			line = fmt.Sprintf("Closed %s on %s; its worktree %s stays: %s.", o.Window, project.Slug, where, why)
		default:
			if _, err := runSSH(ctx, target, worktreeRemoveScript(wt.Dir), nil); err != nil {
				line = fmt.Sprintf("Closed %s on %s; its worktree %s stays: git worktree remove failed (%s).", o.Window, project.Slug, where, oneLine(err.Error()))
			} else {
				line = fmt.Sprintf("Closed %s on %s and removed its worktree %s.", o.Window, project.Slug, where)
			}
		}
	}
	_, _ = fmt.Fprintln(e.Out, line)
	return nil
}

// busyWindow is "claude-2 (working)" when the newest sample shows that
// window's agent mid-turn or waiting for an answer, else "".
func busyWindow(p *Project, window string) string {
	if p.Signals == nil {
		return ""
	}
	for _, a := range p.Signals.Agents {
		if a.Window != window {
			continue
		}
		switch a.State {
		case "working":
			return window + " (working)"
		case "needs_input":
			return window + " (needs input)"
		}
	}
	return ""
}

// tildeGuestPath writes a path under the machine's /home/dev as ~/...
func tildeGuestPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "/home/dev/"); ok {
		return "~/" + rest
	}
	return path.Clean(p)
}
