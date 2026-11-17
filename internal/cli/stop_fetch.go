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

// stopFetchTimeout bounds the fetch `stop -w` makes before it removes a
// worktree (stop_window.go). It was the budget of the fetch every stop
// made first (I-615, I-890); that fetch is gone (I-1240), and a worktree
// is only removed once its branch is here, so the one fetch left keeps
// the 15 s.
var stopFetchTimeout = 15 * time.Second

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
