package cli

import (
	"archive/tar"
	"fmt"
	"hash"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Submodules in the sync (DECISIONS I-263). Each submodule the laptop has
// checked out travels the way the superproject does: its commits as a git
// bundle of what the guest's copy lacks (so a commit only the laptop has,
// and a private remote, need nothing on the guest), then its staged and
// unstaged diffs, and its untracked files inside the one untracked tar.
// The guest's copy is checked out at the laptop's submodule HEAD, which
// may differ from the commit the superproject records, as on the laptop.
// Nested submodules are the same thing one level down. A submodule the
// laptop never checked out stays empty in the guest, as it is on the
// laptop.

// subLaptopRef is where each guest submodule keeps the laptop's HEAD of
// the last sync.
const subLaptopRef = "refs/repose/laptop-head"

// gitlink is one submodule entry of a repository's index or HEAD tree.
type gitlink struct{ Commit, Path string }

// indexGitlinks lists the submodule entries (mode 160000) of dir's index.
func indexGitlinks(dir string) ([]gitlink, error) {
	out, err := gitCmdStdin(dir, "", "ls-files", "-s", "-z")
	if err != nil {
		return nil, err
	}
	var links []gitlink
	for _, rec := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if ok && len(f) == 3 && f[0] == "160000" {
			links = append(links, gitlink{Commit: f[1], Path: p})
		}
	}
	return links, nil
}

// headGitlinks lists the submodule entries of dir's HEAD tree.
func headGitlinks(dir string) ([]gitlink, error) {
	out, err := gitCmdStdin(dir, "", "ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	var links []gitlink
	for _, rec := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if ok && len(f) == 3 && f[0] == "160000" {
			links = append(links, gitlink{Commit: f[2], Path: p})
		}
	}
	return links, nil
}

// hasGitmodules is the cheap test for "this repository may have
// submodules", so a repository without any pays a stat, not a listing of
// its whole index: a .gitmodules in the working tree, or in HEAD (all its
// submodules removed and staged). The guest's repose_sublist makes the
// same test.
func hasGitmodules(dir string) bool {
	if _, err := os.Lstat(filepath.Join(dir, ".gitmodules")); err == nil {
		return true
	}
	_, err := gitCmd(dir, "cat-file", "-e", "HEAD:.gitmodules")
	return err == nil
}

// populated reports whether dir is a repository of its own (a checked-out
// submodule has a .git file or directory), not just a directory of the
// repository above it.
func populated(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// populatedSubmodules lists every checked-out submodule under top,
// nested ones included, parents first, with slash paths relative to top
// and the commit the index above it records.
func populatedSubmodules(top string) []gitlink {
	var out []gitlink
	var walk func(rel string)
	walk = func(rel string) {
		dir := filepath.Join(top, filepath.FromSlash(rel))
		if _, err := os.Lstat(filepath.Join(dir, ".gitmodules")); err != nil {
			return // as the machine's repose_sublist
		}
		links, err := indexGitlinks(dir)
		if err != nil {
			return
		}
		for _, l := range links {
			p := path.Join(rel, l.Path)
			if populated(filepath.Join(top, filepath.FromSlash(p))) {
				out = append(out, gitlink{Commit: l.Commit, Path: p})
				walk(p)
			}
		}
	}
	walk("")
	return out
}

// gitlinkIndexOps is the shell that makes the guest's index record the
// same submodule commits as the laptop's, run in the repository: the
// diffs leave submodules out (a "Subproject commit" hunk does not apply
// to a checkout), so a staged submodule change, addition or removal is
// set here instead. "" when the index records what HEAD does.
func gitlinkIndexOps(dir string) (string, error) {
	if !hasGitmodules(dir) {
		return "", nil
	}
	idx, err := indexGitlinks(dir)
	if err != nil {
		return "", err
	}
	head, err := headGitlinks(dir)
	if err != nil {
		return "", err
	}
	inHead := map[string]string{}
	for _, l := range head {
		inHead[l.Path] = l.Commit
	}
	var b strings.Builder
	for _, l := range idx {
		if inHead[l.Path] != l.Commit {
			_, _ = fmt.Fprintf(&b, "git update-index --add --cacheinfo %s\n", shQuote("160000,"+l.Commit+","+l.Path))
		}
		delete(inHead, l.Path)
	}
	gone := make([]string, 0, len(inHead))
	for p := range inHead {
		gone = append(gone, p)
	}
	sort.Strings(gone) // the ops are part of the sync key
	for _, p := range gone {
		_, _ = fmt.Fprintf(&b, "git update-index --force-remove -- %s\nrmdir %s 2>/dev/null || true\n", shQuote(p), shQuote(p))
	}
	return b.String(), nil
}

// laptopSub is one checked-out submodule, as the sync sends it.
type laptopSub struct {
	Path     string // slash path relative to the superproject
	Head     string
	Recorded string // the commit the index above records for it
	Branch   string // "" when detached, as submodules usually are
	Shallow  bool   // a shallow clone: its history cannot be bundled
	Staged   string
	Unstaged string
	IndexOps string // gitlinkIndexOps for its own nested submodules
	Dirty    int    // modified tracked files, nested submodules left out
	revs     []string
	commits  int
}

// readLaptopSubs reads every checked-out submodule's state: HEAD, branch,
// diffs and dirty count. The untracked files are listed by the caller,
// with the superproject's, so the size limits apply to them all.
func readLaptopSubs(top string) ([]laptopSub, error) {
	var subs []laptopSub
	for _, l := range populatedSubmodules(top) {
		rel := l.Path
		dir := filepath.Join(top, filepath.FromSlash(rel))
		head, err := gitHeadCommit(dir)
		if err != nil {
			continue // a submodule with no commit yet: nothing to check out
		}
		s := laptopSub{Path: rel, Head: head, Recorded: l.Commit}
		s.Branch, _ = gitCurrentBranch(dir)
		if sh, _ := gitCmd(dir, "rev-parse", "--is-shallow-repository"); sh == "true" {
			s.Shallow = true
			s.Dirty, _ = dirtyFileCount(dir)
			subs = append(subs, s)
			continue
		}
		if s.Staged, s.Unstaged, err = gitDiffsBinary(dir); err != nil {
			return nil, stepFailed("diff your submodule "+rel, err, "")
		}
		if s.IndexOps, err = gitlinkIndexOps(dir); err != nil {
			return nil, stepFailed("read your submodule "+rel, err, "")
		}
		if s.Dirty, err = dirtyFileCount(dir); err != nil {
			return nil, stepFailed("read your submodule "+rel, err, "")
		}
		subs = append(subs, s)
	}
	return subs, nil
}

// dirtyFileCount is the number of modified tracked files in dir, not
// counting its submodules, which are counted on their own.
func dirtyFileCount(dir string) (int, error) {
	out, err := gitCmd(dir, "status", "--porcelain", "--untracked-files=no", "--ignore-submodules=all")
	if err != nil {
		return 0, err
	}
	return len(nonEmptyLines(out)), nil
}

// changed reports whether the sub leaves the guest's tree dirty: a diff,
// a staged submodule change of its own, or a HEAD other than the commit
// the index above records.
func (s laptopSub) changed() bool {
	return strings.TrimSpace(s.Staged) != "" || strings.TrimSpace(s.Unstaged) != "" || s.IndexOps != "" || s.Head != s.Recorded
}

// hashSubs folds the submodules' state into the sync key.
func hashSubs(key hash.Hash, subs []laptopSub) {
	for _, s := range subs {
		_, _ = fmt.Fprintf(key, "\x00sub\x00%s\x00%s\x00%s\x00%v\x00%d\x00%s%d\x00%s%d\x00%s", s.Path, s.Head, s.Branch, s.Shallow,
			len(s.Staged), s.Staged, len(s.Unstaged), s.Unstaged, len(s.IndexOps), s.IndexOps)
	}
}

// planSubCommits works out, for each bundled submodule, the commits the
// guest's copy lacks (its tips come from the probe) and returns their
// total.
func planSubCommits(top string, subs []laptopSub, tips map[string][]string) (int, error) {
	total := 0
	for i := range subs {
		s := &subs[i]
		if s.Shallow {
			continue
		}
		dir := filepath.Join(top, filepath.FromSlash(s.Path))
		revs, err := revsToSend(dir, []string{"HEAD"}, tips[s.Path])
		if err != nil {
			return 0, err
		}
		n, err := countRevs(dir, revs)
		if err != nil {
			return 0, err
		}
		s.revs, s.commits = revs, n
		total += n
	}
	return total, nil
}

// addSubsToApply puts each submodule's bundle and diffs in the apply's tar
// (under sub/<i>/) and returns the shell that lays them down, run in the
// superproject after its own diffs and before the untracked tar.
func addSubsToApply(tw *tar.Writer, top string, subs []laptopSub) (string, error) {
	var b strings.Builder
	for i, s := range subs {
		dir := filepath.Join(top, filepath.FromSlash(s.Path))
		p := shQuote(s.Path)
		parent, name := path.Split(s.Path)
		parent = strings.TrimSuffix(parent, "/")
		if parent == "" {
			parent = "."
		}
		if s.Shallow {
			// Nothing to bundle: the guest fetches the commit the project
			// records from the submodule's own remote, and a failure is a
			// warning, not a failed run.
			_, _ = fmt.Fprintf(&b, `if [ ! -e %[1]s/.git ]; then
  if ! e=$(cd %[2]s && GIT_TERMINAL_PROMPT=0 git -c core.sshCommand='ssh -o BatchMode=yes' submodule update -q --init -- %[3]s 2>&1); then
    printf '#subfailed %%s\t%%s\n' %[1]s "$(printf '%%s\n' "$e" | grep -v '^$' | tail -n 1)"
  fi
fi
`, p, shQuote(parent), shQuote(name))
			continue
		}
		pre := fmt.Sprintf("sub/%d/", i)
		_, _ = fmt.Fprintf(&b, "mkdir -p %[1]s\n[ -e %[1]s/.git ] || git init -q %[1]s\n", p)
		b.WriteString("(\ncd " + p + "\n")
		if s.commits > 0 {
			bundle, err := os.CreateTemp("", "repose-subbundle-*")
			if err != nil {
				return "", err
			}
			bp := bundle.Name()
			_ = bundle.Close()
			_, err = gitCmdStdin(dir, strings.Join(s.revs, "\n")+"\n", "bundle", "create", "-q", bp, "--stdin")
			if err == nil {
				err = tarAddFile(tw, pre+"bundle", bp)
			}
			_ = os.Remove(bp)
			if err != nil {
				return "", stepFailed("pack your submodule "+s.Path+" for the machine (git bundle)", err, "")
			}
			_, _ = fmt.Fprintf(&b, "git bundle unbundle \"$t/%sbundle\" >/dev/null\n", pre)
		}
		// A ref for the laptop's HEAD: the next probe lists it, so a
		// guest whose HEAD moved on (an agent's commit) is not sent the
		// whole history again, and gc keeps a commit only the laptop has
		// once nothing else points at it. Hidden from `git branch -a`.
		_, _ = fmt.Fprintf(&b, "git update-ref %s %s\n", subLaptopRef, s.Head)
		if s.Branch == "" {
			_, _ = fmt.Fprintf(&b, "git checkout -q --detach %s\n", s.Head)
		} else {
			// The laptop's branch, created or fast-forwarded; an agent's
			// commits on the guest's copy of it are left where they are.
			_, _ = fmt.Fprintf(&b, `if cur=$(git rev-parse -q --verify refs/heads/%[1]s); then
  if git merge-base --is-ancestor "$cur" %[2]s; then git checkout -q -B %[1]s %[2]s; else git checkout -q --detach %[2]s; fi
else
  git checkout -q -b %[1]s %[2]s
fi
`, shQuote(s.Branch), s.Head)
		}
		for _, d := range []struct{ name, body, apply string }{{"staged.diff", s.Staged, "git apply --index"}, {"unstaged.diff", s.Unstaged, "git apply"}} {
			if strings.TrimSpace(d.body) == "" {
				continue
			}
			if err := tarAddBytes(tw, pre+d.name, []byte(d.body)); err != nil {
				return "", err
			}
			_, _ = fmt.Fprintf(&b, "%s \"$t/%s%s\"\n", d.apply, pre, d.name)
		}
		b.WriteString(s.IndexOps)
		b.WriteString(")\n")
		// Registered the way `git submodule init` would, with the URL
		// .gitmodules names (resolved against the guest's origin), so the
		// agent's `git submodule update` and `git push` inside it work.
		_, _ = fmt.Fprintf(&b, "(cd %s && git submodule init -q -- %s && git submodule sync -q -- %s) >/dev/null 2>&1 || true\n", shQuote(parent), shQuote(name), shQuote(name))
	}
	return b.String(), nil
}

// subUntracked lists the untracked files of every bundled submodule, as
// paths relative to the superproject, for the one untracked tar.
func subUntracked(top string, subs []laptopSub) ([]string, error) {
	var out []string
	for _, s := range subs {
		if s.Shallow {
			continue
		}
		files, err := gitUntrackedFiles(filepath.Join(top, filepath.FromSlash(s.Path)))
		if err != nil {
			return nil, stepFailed("list your untracked files in "+s.Path, err, "")
		}
		for _, f := range files {
			out = append(out, s.Path+"/"+f)
		}
	}
	return out, nil
}
