package cli

import (
	"context"
	"fmt"
	"path/filepath"
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
func checkoutVar(slug, extra string) string {
	if extra != "" {
		return fmt.Sprintf("repose_co=\"$HOME\"/%s\n", shQuote(extra))
	}
	return fmt.Sprintf(`repose_n=
[ -f %[1]s ] && IFS= read -r repose_n < %[1]s || true
case "$repose_n" in ""|.*|*/*) repose_n= ;; esac
if [ -n "$repose_n" ] && [ -d "$HOME/$repose_n" ]; then repose_co="$HOME/$repose_n"
elif [ -d "$HOME/%[2]s" ]; then repose_co="$HOME/%[2]s"
else repose_co="$HOME"; fi
`, checkoutFile, slug)
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
