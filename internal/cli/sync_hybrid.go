package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The hybrid first sync (I-203): when the guest has no commits yet and the
// repository is large, the guest clones it from GitHub over its own
// network, which is seconds for hundreds of MB, instead of the laptop
// uploading the whole history over a home connection. The laptop then
// bundles only what GitHub lacks (unpushed commits), and the diff and
// untracked files follow as always. Later syncs are deltas and never
// clone. A clone that fails for any reason (a private repository without
// gh's login, a network problem) falls back to the full bundle with one
// line saying why; the run never fails for it.
//
// A full fetch, never --filter=blob:none: a partial clone fetches blobs
// later (blame, an old checkout), which fails confusingly once the token
// expires or the repository goes private.

// hybridThresholdKiB is the laptop's `size-pack` (KiB) from which the guest
// clones. 20 MB is the proposal's starting point: about 10 s at a 16
// Mbit/s upload, against seconds from GitHub to Azure. To be set from the
// checklist's measurement (bundle vs clone, this repo and one over 500 MB).
var hybridThresholdKiB int64 = 20 * 1024

// hybridCloneURL is the URL the guest clones from: the https form of a
// github.com remote (public repositories need no credentials; private ones
// use gh's credential helper, which the credentials step configured), or
// "" when the remote is elsewhere. A variable so the sshd harness can
// point it at a local repository.
var hybridCloneURL = func(remote string) string {
	host, path, ok := strings.Cut(strings.TrimSpace(remote), "/")
	if !ok || host != "github.com" || path == "" {
		return ""
	}
	return "https://github.com/" + strings.TrimSuffix(path, ".git") + ".git"
}

// gitPackKiB is `git count-objects -v`'s size-pack, or 0.
func gitPackKiB(dir string) int64 {
	out, err := gitCmd(dir, "count-objects", "-v")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "size-pack: "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

// hybridFetch has the guest fetch every branch and tag from url into its
// empty checkout (a clone into the directory guestd already made), and
// returns the commits its refs now point at, plus those of bases (the
// laptop's own view of origin and its tags, on stdin) that the clone
// brought: a laptop behind GitHub knows none of GitHub's newer tips, and
// without a base it knows the next bundle would carry the whole history
// again. ok=false with why is the fall back; err is only for an ssh that
// failed outright.
func hybridFetch(ctx context.Context, t sshTarget, dir, url string, bases []string) (tips []string, ok bool, why string, err error) {
	script := fmt.Sprintf(`cd %s
export GIT_TERMINAL_PROMPT=0
e=$(mktemp)
if git fetch -q --no-tags %s '+refs/heads/*:refs/remotes/origin/*' '+refs/tags/*:refs/tags/*' </dev/null 2>"$e"; then
  echo '#cloned'
else
  why=$(grep '^fatal:' "$e" | head -n 1)
  [ -n "$why" ] || why=$(grep -v '^ *$' "$e" | tail -n 1)
  printf '#clonefailed %%s\n' "$(printf '%%s' "${why#fatal: }" | cut -c1-160)"
fi
rm -f "$e"
echo '#tips'
git for-each-ref --format='%%(objectname)'
git cat-file --batch-check='%%(objectname) %%(objecttype)' | while read -r o ty; do
  case $ty in commit|tag) echo "$o" ;; esac
done
`, homeShell(dir), shQuote(url))
	out, err := runSSH(ctx, t, script, strings.NewReader(strings.Join(bases, "\n")+"\n"))
	if err != nil {
		return nil, false, "", stepFailed("clone your repository on the machine", err, "")
	}
	section := ""
	seen := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "#cloned":
			ok = true
		case strings.HasPrefix(l, "#clonefailed"):
			why = strings.TrimSpace(strings.TrimPrefix(l, "#clonefailed"))
			if why == "" {
				why = "git fetch failed"
			}
		case l == "#tips":
			section = l
		case section == "#tips" && l != "" && !seen[l]:
			seen[l] = true
			tips = append(tips, l)
		}
	}
	return tips, ok, why, nil
}

// laptopBases are the commits the laptop last saw on origin and its tags:
// what a fresh clone in the guest most likely has too, and so the bases
// the laptop can bundle against after it (hybridFetch).
func laptopBases(dir string) []string {
	out, err := gitCmd(dir, "for-each-ref", "--format=%(objectname)", "refs/remotes/origin", "refs/tags")
	if err != nil {
		return nil
	}
	return nonEmptyLines(out)
}
