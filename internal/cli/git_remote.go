package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// The `repose` git remote (DECISIONS I-272): the laptop checkout gets a
// remote pointing at the machine's checkout over the `<slug>.repose` ssh
// alias, so `git fetch repose` brings an agent's commits back with plain
// git and no round trip through origin. It lives in .git/config, which
// is never committed (R3-11 holds: nothing is written to the repository).

// reposeRemoteName is the one remote name the CLI manages.
const reposeRemoteName = "repose"

// reposeRemotePushURL makes the remote fetch-only. The machine's checkout
// is a working tree with a branch checked out, so a push would be refused
// (receive.denyCurrentBranch) or move the agent's branch under it; work
// goes the other way with `repose sync` (I-367). The text has no colon and no
// slash, so git takes it as a local path and prints it back in its error:
// "fatal: '<this text>' does not appear to be a git repository".
const reposeRemotePushURL = "this remote is fetch-only; repose sync sends your work to the machine"

// reposeRemoteNotedKey records, in the checkout's .git/config, that the
// CLI already said once that a remote named repose points elsewhere.
const reposeRemoteNotedKey = "repose.remoteNoted"

// reposeRemoteURL is the machine's checkout: ~/<dir> on slug's machine
// (dir is the laptop folder of its first sync since I-368, the slug on a
// machine set up before), over the ssh alias every project has.
func reposeRemoteURL(slug, dir string) string { return slug + ".repose:~/" + dir }

// reposeRemoteShape matches every URL reposeRemoteURL makes. A remote
// named repose with this shape is the CLI's own, so it may be retargeted
// or removed; any other is the user's and is never touched.
var reposeRemoteShape = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)\.repose:~/([A-Za-z0-9_][A-Za-z0-9._-]*)$`)

func isReposeRemoteURL(u string) bool { return reposeRemoteShape.MatchString(u) }

// reposeRemoteHost is the project slug of a URL of the CLI's shape, or "".
func reposeRemoteHost(u string) string {
	if m := reposeRemoteShape.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// remoteURLOf is the raw configured URL of a remote (no insteadOf
// rewriting), or "" when the checkout has no such remote.
func remoteURLOf(root, name string) string {
	u, err := gitCmd(root, "config", "--get", "remote."+name+".url")
	if err != nil {
		return ""
	}
	return u
}

// ensureReposeRemote makes the `repose` remote of the checkout at root
// point at slug's machine. Idempotent: an existing remote of the CLI's
// shape is retargeted if needed and otherwise left as it is; a remote
// named repose with any other URL is the user's and is left alone, with
// a note the first time. It returns what to tell the user ("" for
// nothing): a line when the remote is added, and the one-time note.
func ensureReposeRemote(root, slug, dir string) (string, error) {
	want := reposeRemoteURL(slug, dir)
	cur := remoteURLOf(root, reposeRemoteName)
	switch {
	case cur == want:
		return "", nil
	case cur == "":
		if _, err := gitCmd(root, "remote", "add", reposeRemoteName, want); err != nil {
			return "", err
		}
	case isReposeRemoteURL(cur):
		if _, err := gitCmd(root, "remote", "set-url", reposeRemoteName, want); err != nil {
			return "", err
		}
	default:
		if noted, _ := gitCmd(root, "config", "--get", reposeRemoteNotedKey); noted == "true" {
			return "", nil
		}
		if _, err := gitCmd(root, "config", reposeRemoteNotedKey, "true"); err != nil {
			return "", err
		}
		return fmt.Sprintf("This checkout already has a git remote named repose that points elsewhere; it was left alone. To fetch from the machine under another name: git remote add NAME %s", want), nil
	}
	// Fetch-only, and left out of `git fetch --all`, which would
	// otherwise reach for a machine that may be stopped.
	if _, err := gitCmd(root, "config", "remote."+reposeRemoteName+".pushurl", reposeRemotePushURL); err != nil {
		return "", err
	}
	if _, err := gitCmd(root, "config", "remote."+reposeRemoteName+".skipFetchAll", "true"); err != nil {
		return "", err
	}
	if cur != "" {
		return "", nil
	}
	return "Added the git remote repose: `git fetch repose` brings the machine's commits to this checkout.", nil
}

// forgetReposeRemote removes the `repose` remote of the checkout at root
// when it is the CLI's and points at slug's machine, which is going away.
// Only the remote's config goes: the branches already fetched from it
// (repose/main and the rest) stay, so work fetched before the machine
// was removed is not lost with it. It reports whether it removed one.
func forgetReposeRemote(root, slug string) bool {
	if root == "" || reposeRemoteHost(remoteURLOf(root, reposeRemoteName)) != slug {
		return false
	}
	_, err := gitCmd(root, "config", "--remove-section", "remote."+reposeRemoteName)
	return err == nil
}

// checkoutOwnsProject reports whether the checkout at root is project's
// own: the project's remote is the checkout's origin, or, for a project
// with no remote, the checkout is the directory `repose run --name`
// created it from (the by_dir cache). Only then is the `repose` remote
// added: another project run or attached from this checkout (a fork copy,
// a project named with --project) would point the checkout's remote at a
// different repository's machine.
func (e *Env) checkoutOwnsProject(root string, p *Project) bool {
	// A temporary machine never gets the checkout's `repose` remote: that
	// name stays with the checkout's own project (I-347).
	if root == "" || p == nil || p.Slug == "" || p.ExpiresAt != nil {
		return false
	}
	remote := gitRemoteOrigin(root)
	if p.RemoteURL != "" || remote != "" {
		return remote == p.RemoteURL
	}
	return p.ID != "" && e.Cache.ByDir[root] == p.ID
}

// addReposeRemote is what run and attach call: the remote for the
// project when this checkout is its own. A failure is a warning, never a
// failed command: the remote is a convenience beside the run.
//
// checkout is the machine's checkout name when the caller already knows
// it (a run's sync or carry learned it), nil when it does not (attach).
// Unknown, a remote already of the CLI's shape for this machine is kept
// as it is, and a missing one costs one ssh to ask the guest. A machine
// with no checkout (the name "") gets no remote: there is nothing to
// fetch (I-368).
func (e *Env) addReposeRemote(ctx context.Context, p *Project, t sshTarget, checkout *string) {
	root := gitRepoRoot(e.Cwd)
	if t.Checkout != "" {
		// Another checkout of the machine (I-480): the folder's remote
		// points at it when the folder is that checkout.
		co := e.extraCheckout()
		if root == "" || co == nil || co.ProjectID != p.ID || co.Name != t.Checkout {
			return
		}
		checkout = &t.Checkout
	} else if !e.checkoutOwnsProject(root, p) {
		return
	}
	if checkout == nil {
		if reposeRemoteHost(remoteURLOf(root, reposeRemoteName)) == p.Slug {
			return
		}
		name, err := guestCheckoutName(ctx, t, p.Slug)
		if err != nil {
			e.warn("Could not add the git remote repose (%s).", oneLine(err.Error()))
			return
		}
		checkout = &name
	}
	if *checkout == "" {
		return
	}
	note, err := ensureReposeRemote(root, p.Slug, *checkout)
	if err != nil {
		e.warn("Could not add the git remote repose (%s).", oneLine(err.Error()))
		return
	}
	if note != "" {
		_, _ = fmt.Fprintln(e.ErrOut, strings.TrimSpace(note))
	}
}
