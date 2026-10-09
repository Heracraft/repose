package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// stopFetchTimeout bounds the fetch a stop makes first. A machine that
// does not answer is often why it is being stopped, and its commits are
// in the stop's snapshot either way.
var stopFetchTimeout = 60 * time.Second

// fetchBeforeStop runs `git fetch repose` in this checkout when its
// `repose` remote is one of the running machines about to stop
// (DECISIONS I-615): a stopped machine cannot be fetched from, so
// without it getting the agent's last commits costs a start, a fetch and
// a stop. It prints what came, as "Fetched 3 commits on repose/main.",
// and nothing when nothing did. A fetch that fails is one line on stderr
// and the stop goes ahead. Ctrl-C during it stops nothing.
func fetchBeforeStop(ctx context.Context, e *Env, running []*Project) error {
	if e.TargetFor != nil { // tests that stand in for ssh
		return nil
	}
	root := gitRepoRoot(e.Cwd)
	if root == "" {
		return nil
	}
	slug := reposeRemoteHost(remoteURLOf(root, reposeRemoteName))
	found := false
	for _, p := range running {
		found = found || slug != "" && p.Slug == slug && p.State == "running"
	}
	if !found {
		return nil
	}
	pr := e.newProgress()
	defer pr.Fail()
	pr.Phase("Fetching from "+slug, "")
	fetched, err := fetchRepose(ctx, root)
	pr.Fail()
	if ctx.Err() != nil {
		return exitf(ExitInterrupted, "Interrupted. Nothing stopped.")
	}
	if err != nil {
		e.warn("Could not fetch from %s before stopping it: %s", slug, oneLine(err.Error()))
		return nil
	}
	if fetched != "" {
		_, _ = fmt.Fprintf(e.Out, "Fetched %s.\n", fetched)
	}
	return nil
}

// fetchRepose fetches the checkout's `repose` remote and says what it
// brought: "3 commits on repose/main and 5 on repose/worktree-1", counting
// commits no ref of the checkout had before, each once, on the first
// branch in name order that has it; "" when none.
func fetchRepose(ctx context.Context, root string) (string, error) {
	prefix := "refs/remotes/" + reposeRemoteName + "/"
	before, err := gitRefs(root, prefix)
	if err != nil {
		return "", err
	}
	known, err := gitCmd(root, "for-each-ref", "--format=%(objectname)")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, stopFetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "--quiet", reposeRemoteName)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// No prompt, and no long wait on a machine that does not answer; the
	// CLI's own ssh config when it has one, as closeMaster uses.
	sshCmd := "ssh -o BatchMode=yes -o ConnectTimeout=10"
	if sd, err := sshDir(); err == nil {
		if cfg := filepath.Join(sd, "config"); statOK(cfg) {
			sshCmd += " -F " + shQuote(cfg)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("no answer in %s", fmtElapsed(stopFetchTimeout))
		}
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	after, err := gitRefs(root, prefix)
	if err != nil {
		return "", err
	}
	var not strings.Builder
	for _, h := range strings.Fields(known) {
		not.WriteString("^" + h + "\n")
	}
	var parts []string
	names := make([]string, 0, len(after))
	for name := range after {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := after[name]
		if before[name] == h || strings.HasSuffix(name, "/HEAD") {
			continue
		}
		n, err := gitCmdStdin(root, not.String(), "rev-list", "--count", h, "--stdin")
		n = strings.TrimSpace(n)
		// A commit on two branches is counted on the first.
		not.WriteString("^" + h + "\n")
		if err != nil || n == "0" {
			continue
		}
		ref := reposeRemoteName + "/" + strings.TrimPrefix(name, prefix)
		if len(parts) == 0 {
			unit := "commits"
			if n == "1" {
				unit = "commit"
			}
			parts = append(parts, fmt.Sprintf("%s %s on %s", n, unit, ref))
		} else {
			parts = append(parts, fmt.Sprintf("%s on %s", n, ref))
		}
	}
	return joinNames(parts), nil
}

// gitRefs maps each ref under prefix to its commit.
func gitRefs(root, prefix string) (map[string]string, error) {
	out, err := gitCmd(root, "for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if name, h, ok := strings.Cut(l, " "); ok {
			refs[name] = h
		}
	}
	return refs, nil
}

func statOK(p string) bool { _, err := os.Stat(p); return err == nil }
