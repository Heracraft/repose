package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These cover I-150: the laptop sends its commits to the guest as a git
// bundle, so nothing in the guest ever needs credentials for origin.

// TestSyncSendsAnUnpushedCommit: the commit the owner had not pushed
// (and that v0.1.4 asked to push, or failed on) arrives without a push,
// and origin is left exactly as it was.
func TestSyncSendsAnUnpushedCommit(t *testing.T) {
	f := newSyncFixture(t)
	originBefore := mustRun(t, f.bare, "git", "rev-parse", "main")
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("local change, not pushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "commit", "-q", "-am", "local only")
	head := mustRun(t, f.local, "git", "rev-parse", "HEAD")

	summary, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if summary.Commits != 1 || !strings.Contains(summary.String(), "(1 new commit)") {
		t.Fatalf("summary = %+v %q", summary, summary.String())
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"); got != head {
		t.Fatalf("guest HEAD = %s, want %s", got, head)
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("guest is on %q, want main", got)
	}
	// origin/main in the guest is where the laptop believes origin is,
	// so the agent's `git status` says "ahead by 1" and a push works.
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "refs/remotes/origin/main"); got != originBefore {
		t.Fatalf("guest origin/main = %s, want %s", got, originBefore)
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
		t.Fatalf("guest main's upstream = %q", got)
	}
	if got := mustRun(t, f.bare, "git", "rev-parse", "main"); got != originBefore {
		t.Fatal("the sync pushed to origin")
	}

	// A second run with nothing new sends no bundle and changes nothing.
	summary, err = syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("second syncGuest: %v", err)
	}
	if summary.Commits != 0 {
		t.Fatalf("second sync sent %d commits", summary.Commits)
	}
}

// TestSyncIntoAnEmptyGuestRepo is izma's case: the guest's checkout was a
// bare `git init` (the fetch had failed) or not there at all. The whole
// history travels, the branch and origin are set up, and the owner's
// uncommitted and untracked work lands on top.
func TestSyncIntoAnEmptyGuestRepo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, guestRepo string)
	}{
		{"git init only", func(t *testing.T, repo string) {
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			mustRun(t, repo, "git", "init", "-q", "-b", "master", ".")
		}},
		{"no directory", func(t *testing.T, repo string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			if err := os.RemoveAll(f.guestRepo()); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, f.guestRepo())
			mustRun(t, f.local, "git", "checkout", "-q", "-b", "feature")
			if err := os.WriteFile(filepath.Join(f.local, "a.txt"), []byte("one\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			mustRun(t, f.local, "git", "add", "a.txt")
			mustRun(t, f.local, "git", "commit", "-q", "-m", "second")
			if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("edited, uncommitted\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.local, "notes.md"), []byte("scratch\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			head := mustRun(t, f.local, "git", "rev-parse", "HEAD")

			summary, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/owner/repo"})
			if err != nil {
				t.Fatalf("syncGuest: %v", err)
			}
			if summary.Commits != 2 || summary.Modified != 1 || summary.Untracked != 1 {
				t.Fatalf("summary = %+v", summary)
			}
			repo := f.guestRepo()
			if got := mustRun(t, repo, "git", "rev-parse", "HEAD"); got != head {
				t.Fatalf("guest HEAD = %s, want %s", got, head)
			}
			if got := mustRun(t, repo, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
				t.Fatalf("guest branch = %q, want feature", got)
			}
			if got := mustRun(t, repo, "git", "rev-list", "--count", "HEAD"); got != "2" {
				t.Fatalf("guest history has %s commits, want 2", got)
			}
			if got := mustRun(t, repo, "git", "remote", "get-url", "origin"); got != "git@github.com:owner/repo.git" {
				t.Fatalf("guest origin = %q", got)
			}
			if b, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(b) != "edited, uncommitted\n" {
				t.Fatalf("diff not applied: %q", b)
			}
			if b, _ := os.ReadFile(filepath.Join(repo, "notes.md")); string(b) != "scratch\n" {
				t.Fatalf("untracked not copied: %q", b)
			}
		})
	}
}

// TestSyncMergesADivergedGuestBranch: an agent committed on the guest's
// branch and the laptop has commits of its own. The guest's branch takes
// a merge of the laptop's commit, so the agent's commit stays on it and
// the checkout has the laptop's work, with no detached HEAD (I-574).
func TestSyncMergesADivergedGuestBranch(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "add", "agent.go")
	mustRun(t, f.guestRepo(), "git", "commit", "-q", "-m", "agent's work")
	agentHead := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "commit", "-q", "-am", "laptop's work")
	head := mustRun(t, f.local, "git", "rev-parse", "HEAD")

	summary, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if !summary.Merged || summary.Diverged || summary.Detached || len(summary.Warnings()) != 0 {
		t.Fatalf("summary = %+v %v, want merged with no warning", summary, summary.Warnings())
	}
	if !strings.Contains(summary.String(), "(1 new commit), merged with the machine's main") {
		t.Fatalf("summary line = %q", summary.String())
	}
	if ref := mustRun(t, f.guestRepo(), "git", "symbolic-ref", "-q", "HEAD"); ref != "refs/heads/main" {
		t.Fatalf("guest HEAD = %q, want main", ref)
	}
	for _, c := range []string{agentHead, head} {
		if out, err := exec.Command("git", "-C", f.guestRepo(), "merge-base", "--is-ancestor", c, "HEAD").CombinedOutput(); err != nil {
			t.Fatalf("%s is not in the guest's main: %v %s", c, err, out)
		}
	}
	if got := mustRun(t, f.guestRepo(), "git", "log", "-1", "--format=%s"); got != "Merge the laptop's main (repose sync)" {
		t.Fatalf("merge commit subject = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop\n" {
		t.Fatalf("README.md = %q", b)
	}
	if st := mustRun(t, f.guestRepo(), "git", "status", "--porcelain"); st != "" {
		t.Fatalf("guest tree after the merge: %q", st)
	}
}

// TestSyncLeavesAConflictingGuestBranchAlone: the agent's commit and the
// laptop's change the same line. No merge is made: the guest's branch
// stays on the agent's commit and the laptop's commit is checked out
// detached, with one warning (I-150, I-574).
func TestSyncLeavesAConflictingGuestBranchAlone(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "commit", "-q", "-am", "agent's work")
	agentHead := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "commit", "-q", "-am", "laptop's work")
	head := mustRun(t, f.local, "git", "rev-parse", "HEAD")

	summary, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if !summary.Diverged || summary.Merged {
		t.Fatalf("summary = %+v, want diverged", summary)
	}
	want := "The machine's main has commits that could not be merged with yours, so it was left as it is and the machine is on " + head[:7] + ", detached. `git fetch repose` brings that branch here."
	if w := summary.Warnings(); len(w) != 1 || w[0] != want {
		t.Fatalf("warnings = %q, want %q", w, want)
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "refs/heads/main"); got != agentHead {
		t.Fatalf("guest main moved to %s; the agent's %s must stay", got, agentHead)
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"); got != head {
		t.Fatalf("guest HEAD = %s, want the laptop's %s", got, head)
	}
	if _, err := os.Stat(filepath.Join(f.guestRepo(), ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("a merge was left in progress")
	}
}

// The laptop's uncommitted edit is to a file the agent's commits changed:
// a merge would put the agent's version under it, so the branch is left
// alone and the laptop's commit is checked out detached (I-574).
func TestSyncDoesNotMergeUnderTheLaptopsUncommittedWork(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "notes.md"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "add", "notes.md")
	mustRun(t, f.guestRepo(), "git", "commit", "-q", "-m", "agent's notes")
	agentHead := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD")
	mustRun(t, f.local, "git", "commit", "-q", "--allow-empty", "-m", "laptop's work")
	if err := os.WriteFile(filepath.Join(f.local, "notes.md"), []byte("laptop, untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if !summary.Diverged || summary.Merged {
		t.Fatalf("summary = %+v, want diverged", summary)
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "refs/heads/main"); got != agentHead {
		t.Fatalf("guest main moved to %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "notes.md")); string(b) != "laptop, untracked\n" {
		t.Fatalf("notes.md = %q", b)
	}
}

// TestSyncNoRemoteSendsHistory replaces I-138's whole-tree commit: a
// --name project without a remote gets the laptop's real commits, so a
// deletion on the laptop is a deletion in the guest too.
func TestSyncNoRemoteSendsHistory(t *testing.T) {
	f := newSyncFixture(t)
	local := t.TempDir()
	mustRun(t, local, "git", "init", "-q", "-b", "main", ".")
	mustRun(t, local, "git", "config", "user.email", "dev@example.com")
	mustRun(t, local, "git", "config", "user.name", "Dev Laptop")
	for _, name := range []string{"main.go", "old.go"} {
		if err := os.WriteFile(filepath.Join(local, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, local, "git", "add", ".")
	mustRun(t, local, "git", "commit", "-q", "-m", "local only")
	if err := os.WriteFile(filepath.Join(local, "main.go"), []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guestRepo := f.guestRepo()
	if err := os.RemoveAll(guestRepo); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(guestRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, guestRepo, "git", "init", "-q", "-b", "main", ".")
	// The whole history arrives in one fetch, which can start git's
	// detached auto gc/maintenance; it went on writing into .git while
	// t.TempDir's cleanup removed it (CI, 2026-09-29).
	mustRun(t, guestRepo, "git", "config", "gc.auto", "0")
	mustRun(t, guestRepo, "git", "config", "maintenance.auto", "false")

	if _, err := syncGuest(context.Background(), f.target, local, testSlug, SyncOptions{NoRemote: true}); err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(guestRepo, "main.go")); string(b) != "package main // edited\n" {
		t.Fatalf("edit not in the guest: %q", b)
	}
	if out, err := exec.Command("git", "-C", guestRepo, "remote").Output(); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("a no-remote project got a remote: %q %v", out, err)
	}

	// Commit a deletion on the laptop; discard the guest's staged copy
	// of the earlier edit the way the user would after committing it.
	mustRun(t, local, "git", "rm", "-q", "old.go")
	mustRun(t, local, "git", "commit", "-q", "-am", "drop old.go")
	if _, err := syncGuest(context.Background(), f.target, local, testSlug, SyncOptions{NoRemote: true, DiscardRemote: true}); err != nil {
		t.Fatalf("second syncGuest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(guestRepo, "old.go")); !os.IsNotExist(err) {
		t.Fatalf("old.go deleted on the laptop is still in the guest (%v)", err)
	}
	if got, want := mustRun(t, guestRepo, "git", "rev-parse", "HEAD"), mustRun(t, local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD %s, laptop %s", got, want)
	}
}

// TestSyncOverAMultiplexedConnection runs the sync through the same
// ControlMaster lines the generated config carries (I-149): one TCP
// connection, every later ssh a session on it, and no ssh call left
// waiting on the persisted master's pipes.
func TestSyncOverAMultiplexedConnection(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh client")
	}
	f := newSyncFixture(t)
	cmDir, err := os.MkdirTemp("", "cm") // short: a unix socket path has a 104-108 byte limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cmDir) })
	mux := append([]string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(cmDir, "cm-%C"),
		"-o", "ControlPersist=60",
	}, f.target.Args...)
	target := sshTarget{Args: mux}
	t.Cleanup(func() {
		args := append([]string{"-O", "exit"}, mux...)
		_ = exec.Command("ssh", args...).Run()
	})

	start := time.Now()
	if err := waitForSSH(context.Background(), target, nil); err != nil {
		t.Fatalf("waitForSSH: %v", err)
	}
	if time.Since(start) > sshWaitDelay {
		t.Fatalf("the first (master) ssh took %s: its pipes were held open", time.Since(start))
	}
	socks, _ := filepath.Glob(filepath.Join(cmDir, "cm-*"))
	if len(socks) != 1 {
		t.Fatalf("control sockets = %v, want exactly one", socks)
	}
	check := exec.Command("ssh", append([]string{"-O", "check"}, mux...)...)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("ssh -O check: %v %s", err, out)
	}

	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("over the mux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "commit", "-q", "-am", "mux")
	if _, err := syncGuest(context.Background(), target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatalf("syncGuest over the mux: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "over the mux\n" {
		t.Fatalf("guest README = %q", b)
	}
	if n := f.guest.Connections(); n != 1 {
		t.Fatalf("the guest saw %d SSH connections, want 1 (every call multiplexed)", n)
	}
}

// guestOnFeatureX gives the guest's main a commit the laptop lacks (body
// in rel), then leaves the guest on feature-x with a commit of its own,
// and gives the laptop a commit to README.md. It returns main's and
// feature-x's commits.
func guestOnFeatureX(t *testing.T, f *syncFixture, rel, body string) (mainHead, fxHead string) {
	t.Helper()
	g := f.guestRepo()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(g, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, g, "git", "add", rel)
	mustRun(t, g, "git", "commit", "-q", "-m", "agent's main")
	mustRun(t, g, "git", "checkout", "-q", "-b", "feature-x")
	if err := os.WriteFile(filepath.Join(g, "fx.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, g, "git", "add", "fx.txt")
	mustRun(t, g, "git", "commit", "-q", "-m", "agent's feature")
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "commit", "-q", "-am", "laptop's work")
	return mustRun(t, g, "git", "rev-parse", "main"), mustRun(t, g, "git", "rev-parse", "feature-x")
}

// The guest is on feature-x and its main has commits the laptop lacks:
// the sync moves the checkout to main as it always does, merging the
// laptop's commit there; feature-x keeps its commit (I-574).
func TestSyncMergesTheGuestsMainFromAnotherBranch(t *testing.T) {
	f := newSyncFixture(t)
	mainHead, fxHead := guestOnFeatureX(t, f, "agent.go", "package agent\n")
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "scratch.txt"), []byte("the agent's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if !s.Merged || s.Detached || s.GuestKept != 1 {
		t.Fatalf("summary %+v", s)
	}
	g := f.guestRepo()
	if ref := mustRun(t, g, "git", "symbolic-ref", "-q", "HEAD"); ref != "refs/heads/main" {
		t.Fatalf("guest HEAD = %q, want main", ref)
	}
	if out, err := exec.Command("git", "-C", g, "merge-base", "--is-ancestor", mainHead, "main").CombinedOutput(); err != nil {
		t.Fatalf("the agent's main commit is not in main: %v %s", err, out)
	}
	if got := mustRun(t, g, "git", "rev-parse", "feature-x"); got != fxHead {
		t.Fatalf("feature-x moved to %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(g, "README.md")); string(b) != "laptop\n" {
		t.Fatalf("README.md = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(g, "scratch.txt")); string(b) != "the agent's\n" {
		t.Fatalf("scratch.txt = %q", b)
	}
}

// The same with a main that conflicts with the laptop's commit: no merge,
// the laptop's commit is checked out detached, and neither of the
// guest's branches moves.
func TestSyncLeavesTheGuestsBranchesAloneWhenMainConflicts(t *testing.T) {
	f := newSyncFixture(t)
	mainHead, fxHead := guestOnFeatureX(t, f, "README.md", "agent\n")
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if !s.Diverged || s.Merged {
		t.Fatalf("summary %+v, want diverged", s)
	}
	g := f.guestRepo()
	if got := mustRun(t, g, "git", "rev-parse", "main"); got != mainHead {
		t.Fatalf("main moved to %s", got)
	}
	if got := mustRun(t, g, "git", "rev-parse", "feature-x"); got != fxHead {
		t.Fatalf("feature-x moved to %s", got)
	}
	if got, want := mustRun(t, g, "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD = %s, want the laptop's %s", got, want)
	}
}
