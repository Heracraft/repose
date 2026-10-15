package cli

import (
	"strconv"
	"strings"
)

// The hybrid first sync (I-203): when the guest has no commits yet and the
// repository is large, the guest fetches it from GitHub over its own
// network, which is seconds for hundreds of MB, instead of the laptop
// uploading the whole history over a home connection. The laptop then
// bundles only what GitHub lacks (unpushed commits), and the diff and
// untracked files follow as always. Later syncs are deltas and never
// fetch. A fetch that fails for any reason (a private repository without
// gh's login, a network problem) falls back to the bundle with one line
// saying why; the run never fails for it. sync_prefetch.go has how the
// fetches run (I-712..I-715).

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

// laptopBases are the commits the laptop last saw on origin: what the
// guest's fetch from GitHub most likely has too, and so the bases the
// laptop bundles against before that fetch has run (I-713). Not its
// tags: a tag made on the laptop and never pushed would name a base
// GitHub lacks, and cost the second payload.
func laptopBases(dir string) []string {
	out, err := gitCmd(dir, "for-each-ref", "--format=%(objectname)", "refs/remotes/origin")
	if err != nil {
		return nil
	}
	return nonEmptyLines(out)
}
