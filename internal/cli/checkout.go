package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// The machine's checkout lives at /home/dev/<name>, where <name> is the
// laptop folder the first sync came from (DECISIONS I-368). The guest
// records the name in checkoutFile; guestd, the tmux session and
// repose-checkout read it by the same rule as checkoutVar
// (interfaces/guest-conventions.md "The checkout").
const checkoutFile = "~/.repose/checkout"

// checkoutsFile lists the machine's other checkouts, one name per line:
// folders `repose run --on` added beside the checkout (DECISIONS I-480).
// The checkout itself is never in it.
const checkoutsFile = "~/.repose/checkouts"

// checkoutVar is shell that sets repose_co to the checkout's absolute
// path in the guest: the directory checkoutFile names, else ~/<slug> (a
// machine set up before I-368, whose guestd made that directory at every
// start), else the home directory itself, where a machine with no
// checkout works. slug is a validated project slug. extra, when not "",
// is one of the machine's other checkouts (I-480): ~/<extra>, whether
// or not it exists yet.
//
// A name the machine does not list in checkoutsFile, with no directory of
// its own, is refused there, exit 2 with noCheckoutMsg on stderr: only
// `repose run --on` adds a checkout, so a typo in PROJECT:CHECKOUT never
// makes an empty one (DECISIONS I-618).
func checkoutVar(slug, extra string) string {
	if extra != "" {
		return fmt.Sprintf(`repose_co="$HOME"/%[1]s
if [ ! -d "$repose_co" ] && ! grep -qxF %[1]s %[2]s 2>/dev/null; then printf '%%s\n' %[3]s >&2; exit 2; fi
`, shQuote(extra), checkoutsFile, shQuote(noCheckoutMsg(slug, extra)))
	}
	return fmt.Sprintf(`repose_n=
[ -f %[1]s ] && IFS= read -r repose_n < %[1]s || true
case "$repose_n" in ""|.*|*/*) repose_n= ;; esac
if [ -n "$repose_n" ] && [ -d "$HOME/$repose_n" ]; then repose_co="$HOME/$repose_n"
elif [ -d "$HOME/%[2]s" ]; then repose_co="$HOME/%[2]s"
else repose_co="$HOME"; fi
`, checkoutFile, slug)
}

// noCheckoutMsg is the refusal for a checkout the machine does not have.
func noCheckoutMsg(slug, extra string) string {
	return fmt.Sprintf("%s has no checkout %s. `repose run --on %s` in its folder adds it.", slug, extra, slug)
}

// noCheckoutError is err as checkoutVar's refusal when it is one: exit 2
// with its line, not a failed step.
func noCheckoutError(err error) error {
	var se *sshError
	if !errors.As(err, &se) || se.ExitCode != 2 {
		return nil
	}
	for _, l := range strings.Split(se.Stderr, "\n") {
		if strings.Contains(l, " has no checkout ") && strings.HasSuffix(l, " in its folder adds it.") {
			return exitf(ExitUsage, "%s", strings.TrimSpace(l))
		}
	}
	return nil
}

// checkoutCreate is shell, after checkoutVar, that makes the checkout
// when the guest has none: ~/<want> when that is free (missing, or an
// empty directory), else ~/<slug>; the name goes in checkoutFile. It
// prints "#created" when it made one. want is checkoutName's answer.
// For another checkout (extra, I-480) it makes ~/<extra> when missing,
// lists it in checkoutsFile and never touches checkoutFile.
func checkoutCreate(slug, want, extra string) string {
	if extra != "" {
		return fmt.Sprintf(`if [ ! -d "$repose_co" ]; then
  mkdir -p "$repose_co" ~/.repose
  grep -qxF %[1]s %[2]s 2>/dev/null || printf '%%s\n' %[1]s >> %[2]s
  echo '#created'
fi
`, shQuote(extra), checkoutsFile)
	}
	return fmt.Sprintf(`if [ "$repose_co" = "$HOME" ]; then
  repose_n=%[2]s
  if [ -z "$repose_n" ] || { [ -e "$HOME/$repose_n" ] && { [ ! -d "$HOME/$repose_n" ] || [ -n "$(ls -A "$HOME/$repose_n")" ]; }; }; then repose_n=%[1]s; fi
  mkdir -p "$HOME/$repose_n" ~/.repose
  printf '%%s\n' "$repose_n" > %[3]s
  repose_co="$HOME/$repose_n"
  echo '#created'
fi
`, slug, shQuote(want), checkoutFile)
}

// checkoutName is the directory a first sync from the checkout at root
// makes in the guest: the folder's own name, made safe the way a project
// name is ("job search" is job-search, a leading dot goes), or "" when
// nothing is left, and the guest uses the slug.
func checkoutName(root string) string {
	if root == "" {
		return ""
	}
	return dirProjectName(filepath.Base(root))
}

// checkoutReport is shell, after checkoutVar, that prints the checkout's
// name under the home as "#checkout <name>", or a bare "#checkout" when
// the machine has none.
const checkoutReport = `if [ "$repose_co" = "$HOME" ]; then echo '#checkout'; else printf '#checkout %s\n' "${repose_co##*/}"; fi
`

// parseCheckout finds checkoutReport's line in out: the name, and
// whether the line was there at all.
func parseCheckout(out string) (name string, ok bool) {
	for _, l := range strings.Split(out, "\n") {
		if l == "#checkout" {
			return "", true
		}
		if n, found := strings.CutPrefix(l, "#checkout "); found {
			return strings.TrimSpace(n), true
		}
	}
	return "", false
}

// guestCheckoutName asks the guest where the checkout is: its name under
// /home/dev, or "" when the machine has none and work happens in the
// home directory.
func guestCheckoutName(ctx context.Context, t sshTarget, slug string) (string, error) {
	if t.Checkout != "" {
		return t.Checkout, nil
	}
	out, err := runSSH(ctx, t, checkoutVar(slug, "")+checkoutReport, nil)
	if err != nil {
		return "", stepFailed("find the checkout on the machine", err, "")
	}
	name, _ := parseCheckout(string(out))
	return name, nil
}

// guestHomePath is the absolute path of name under the guest's home;
// the home itself for "".
func guestHomePath(name string) string {
	if name == "" {
		return "/home/dev"
	}
	return "/home/dev/" + name
}

// homeShell is name under the home as a shell word for a guest script:
// "$HOME"/'<name>', or "$HOME".
func homeShell(name string) string {
	if name == "" {
		return `"$HOME"`
	}
	return `"$HOME"/` + shQuote(name)
}

// tildePath is name as the guest's shell spells it: "~/<name>", or "~".
func tildePath(name string) string {
	if name == "" {
		return "~"
	}
	return "~/" + name
}

// claimCheckoutScript picks the name a folder joining the machine with
// `repose run --on` gets (DECISIONS I-480): want, else want-2, want-3,
// ..., skipping the checkout itself, its worktrees, the names already in
// checkoutsFile and any directory of the home that is not empty. It
// makes the directory, lists it, and prints "#checkout <name>". A name
// already listed belongs to another laptop folder and is skipped too.
func claimCheckoutScript(slug, want string) string {
	return checkoutVar(slug, "") + fmt.Sprintf(`repose_p=
[ "$repose_co" = "$HOME" ] || repose_p=${repose_co##*/}
repose_free() {
  [ "$1" != "$repose_p" ] || return 1
  grep -qxF "$1" %[2]s 2>/dev/null && return 1
  case "$1" in "$repose_p"-worktree-*)
    case ${1#"$repose_p"-worktree-} in ""|*[!0-9]*) ;; *) return 1 ;; esac ;;
  esac
  [ ! -e "$HOME/$1" ] && return 0
  [ -d "$HOME/$1" ] && [ -z "$(ls -A "$HOME/$1")" ]
}
repose_n=%[1]s; i=2
while ! repose_free "$repose_n"; do
  [ "$i" -le 1000 ] || { echo 'no free name' >&2; exit 1; }
  repose_n=%[1]s-$i; i=$((i+1))
done
mkdir -p "$HOME/$repose_n" ~/.repose
grep -qxF "$repose_n" %[2]s 2>/dev/null || printf '%%s\n' "$repose_n" >> %[2]s
printf '#checkout %%s\n' "$repose_n"
`, shQuote(want), checkoutsFile)
}

// claimCheckout runs claimCheckoutScript and returns the name.
func claimCheckout(ctx context.Context, t sshTarget, slug, want string) (string, error) {
	out, err := runSSH(ctx, t, claimCheckoutScript(slug, want), nil)
	if err != nil {
		return "", stepFailed("add the folder to the machine", err, "")
	}
	name, ok := parseCheckout(string(out))
	if !ok || name == "" {
		return "", stepFailed("add the folder to the machine", fmt.Errorf("the machine did not say where"), "")
	}
	return name, nil
}

// windowLabel is the name an agent's tmux window starts from (I-480):
// the agent's own for the checkout, "<checkout>/<agent>" for another
// checkout, so `repose ps` and the attach can tell them apart. A dot,
// which tmux reads as the pane separator in a target, becomes a dash.
func windowLabel(extra, agent string) string {
	if extra == "" {
		return agent
	}
	return windowPrefix(extra) + "/" + agent
}

// windowPrefix is extra's part of its window names.
func windowPrefix(extra string) string {
	return strings.ReplaceAll(extra, ".", "-")
}

// wholeMachineOnly refuses PROJECT:CHECKOUT on a command that acts on the
// whole machine and would otherwise drop the checkout without a word
// (DECISIONS I-618). run, attach, sync, exec, ssh, code and the commands
// on connectRunning take it; rm removes the checkout.
func wholeMachineOnly(e *Env, projectArg string) error {
	p, co, ok := strings.Cut(e.resolveArg(projectArg), ":")
	if !ok {
		return nil
	}
	cmd := strings.TrimSuffix(e.Command, " --project")
	if cmd == "" {
		cmd = "repose"
	}
	return exitf(ExitUsage, "`%s` acts on the whole machine, so it takes %s, not %s:%s.", cmd, p, p, co)
}

// removeCheckoutScript checks, and with remove deletes, the machine's
// other checkout name (DECISIONS I-618): its directory and the worktrees
// `run --worktree` made from it, and its line in checkoutsFile. It prints
// "#notlisted" and the "#listed <name>" lines for a name the machine does
// not list, "#busy <n>" while n processes work in one of those
// directories (an agent, a shell), else "#ok" or "#removed".
func removeCheckoutScript(name string, remove bool) string {
	act := "echo '#ok'"
	if remove {
		act = fmt.Sprintf(`rm -rf -- "$@"
{ grep -vxF "$repose_n" %[1]s || true; } > %[1]s.new && mv %[1]s.new %[1]s
echo '#removed'`, checkoutsFile)
	}
	return fmt.Sprintf(`cd "$HOME"
repose_n=%[1]s
if ! grep -qxF "$repose_n" %[2]s 2>/dev/null; then
  echo '#notlisted'
  [ ! -f %[2]s ] || sed 's/^/#listed /' %[2]s
  exit 0
fi
set -- "$HOME/$repose_n"
for d in "$HOME/$repose_n"-worktree-*; do
  [ -f "$d/.git" ] && grep -qF "/$repose_n/.git/worktrees/" "$d/.git" && set -- "$@" "$d"
done
repose_busy=0
for p in /proc/[0-9]*; do
  c=$(readlink "$p/cwd" 2>/dev/null) || continue
  for d in "$@"; do
    case $c in "$d"|"$d"/*) repose_busy=$((repose_busy+1)); break ;; esac
  done
done
if [ "$repose_busy" -gt 0 ]; then printf '#busy %%s\n' "$repose_busy"; exit 0; fi
%[3]s
`, shQuote(name), checkoutsFile, act)
}

// RemoveCheckoutCmd is `repose rm PROJECT:CHECKOUT` (DECISIONS I-618):
// the machine's other checkout goes, with its worktrees, after a
// question; the machine and its own checkout stay. Folders on this
// laptop linked to it forget it.
func RemoveCheckoutCmd(ctx context.Context, e *Env, projectArg string, yes bool, confirm func(prompt string) (bool, error)) error {
	res, err := requireProjectRes(ctx, e, projectArg)
	if err != nil {
		return err
	}
	project, name := res.Project, res.Checkout
	if project.State != "running" {
		return notRunningError(project)
	}
	if !yes && confirm == nil {
		return exitf(ExitUsage, "Removing %s:%s needs a confirmation; pass --yes to skip it.", project.Slug, name)
	}
	t, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	check := func(remove bool) (string, error) {
		out, err := runSSH(ctx, t, removeCheckoutScript(name, remove), nil)
		if err != nil {
			return "", stepFailed("remove the checkout", err, "")
		}
		var listed []string
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			if n, ok := strings.CutPrefix(l, "#listed "); ok {
				listed = append(listed, n)
			}
		}
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			switch {
			case l == "#notlisted" && len(listed) == 0:
				return "", exitf(ExitUsage, "%s has no checkouts besides its own.", project.Slug)
			case l == "#notlisted":
				return "", exitf(ExitUsage, "%s has no checkout %s. Its checkouts: %s.", project.Slug, name, strings.Join(listed, ", "))
			case strings.HasPrefix(l, "#busy "):
				n, _ := strconv.Atoi(strings.TrimPrefix(l, "#busy "))
				return "", exitf(ExitDirtyRemoteTree, "Not removed: %d %s on %s %s in %s. Close the agents and shells there first.", n, plural(n, "process", "processes"), project.Slug, plural(n, "works", "work"), tildePath(name))
			case l == "#ok", l == "#removed":
				return l, nil
			}
		}
		return "", stepFailed("remove the checkout", fmt.Errorf("the machine did not answer"), "")
	}
	if !yes {
		if _, err := check(false); err != nil {
			return err
		}
		ok, err := confirm(fmt.Sprintf("Remove %s from %s? It is deleted with its worktrees, uncommitted work and any commits you have not fetched or pushed. [y/N] ", tildePath(name), project.Slug))
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(e.Out, "Nothing removed.")
			return nil
		}
	}
	if _, err := check(true); err != nil {
		return err
	}
	e.forgetCheckout(project.ID, name)
	_, _ = fmt.Fprintf(e.Out, "Removed %s from %s.\n", tildePath(name), project.Slug)
	return nil
}

// forgetCheckout drops every folder's link to the checkout name of the
// project id, in e.Cache and in projects.json as it is on disk.
func (e *Env) forgetCheckout(id, name string) {
	drop := func(c *ProjectsCache) {
		for k, co := range c.Checkouts {
			if co.ProjectID == id && co.Name == name {
				delete(c.Checkouts, k)
			}
		}
	}
	drop(&e.Cache)
	disk, err := loadProjectsCache(e.Dir)
	if err == nil {
		drop(&disk)
		err = saveProjectsCache(e.Dir, disk)
	}
	if err != nil {
		e.warn("Could not save %s (%s).", projectsPath(e.Dir), oneLine(err.Error()))
	}
}
