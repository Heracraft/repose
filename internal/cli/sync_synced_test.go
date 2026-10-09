package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// I-210 edge cases found by the second review of ws/15-fixes.

func wantDirtyRefusal(t *testing.T, err error) {
	t.Helper()
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitDirtyRemoteTree {
		t.Fatalf("err = %v, want exit %d", err, ExitDirtyRemoteTree)
	}
}

// An agent's edit inside a submodule does not change the superproject's
// `git add -A` tree (a submodule is only its commit there), and a carried
// submodule.recurse=true would make a reset restore the file. Any
// submodule change means the tree is not the last sync's own.
func TestSyncedTreeWithASubmoduleChangeRefuses(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	subBare := t.TempDir()
	mustRun(t, subBare, "git", "init", "--bare", "-q", "-b", "main", ".")
	seed := t.TempDir()
	mustRun(t, seed, "git", "clone", "-q", subBare, ".")
	if err := os.WriteFile(filepath.Join(seed, "s"), []byte("sub v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, seed, "git", "add", "s")
	mustRun(t, seed, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-m", "sub")
	mustRun(t, seed, "git", "push", "-q", "origin", "main")
	mustRun(t, f.local, "git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", subBare, "sub")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "add sub")

	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init")
	mustRun(t, f.guestRepo(), "git", "config", "submodule.recurse", "true")
	agent := filepath.Join(f.guestRepo(), "sub", "s")
	if err := os.WriteFile(agent, []byte("the agent's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// New laptop work, so the sync would apply (with nothing new it
	// attaches without touching the guest, I-248).
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop, later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	if b, _ := os.ReadFile(agent); string(b) != "the agent's work\n" {
		t.Fatalf("the agent's edit in the submodule was lost: %q", b)
	}
}

// A carried status.showUntrackedFiles=no hides untracked files from a
// plain `git status`: a sync that left only untracked files must still
// fingerprint them, and the agent's edit of one must still refuse.
func TestSyncedTreeIgnoresShowUntrackedFilesNo(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	mustRun(t, f.guestRepo(), "git", "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(f.local, "notes.md"), []byte("laptop notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	// The same laptop tree again goes through.
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	agent := filepath.Join(f.guestRepo(), "notes.md")
	if err := os.WriteFile(agent, []byte("the agent's notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "notes.md"), []byte("laptop notes, later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	if b, _ := os.ReadFile(agent); string(b) != "the agent's notes\n" {
		t.Fatalf("the agent's notes were overwritten: %q", b)
	}
}

// The last sync's changes are stashed, not reset away, so anything
// misjudged can be recovered, and the summary says so. --discard-remote
// stashes the whole tree under its own name (I-618).
func TestSyncedTreeIsStashed(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	// A new laptop change: the same one again is not applied at all
	// (I-224, TestUnchangedSyncSkipsTheApply).
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop, later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if list := mustRun(t, f.guestRepo(), "git", "stash", "list"); !strings.Contains(list, "repose run: last sync") {
		t.Fatalf("stash list = %q", list)
	}
	if !strings.Contains(s.String(), "last sync's changes stashed on the machine") {
		t.Errorf("summary = %q", s.String())
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop, later\n" {
		t.Errorf("README.md = %q", b)
	}
	stashes := mustRun(t, f.guestRepo(), "git", "stash", "list", "--format=%gs")
	s, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{DiscardRemote: true})
	if err != nil || s.StashedLastSync {
		t.Fatalf("--discard-remote: %+v %v", s, err)
	}
	after := mustRun(t, f.guestRepo(), "git", "stash", "list", "--format=%gs")
	if first, _, _ := strings.Cut(after, "\n"); !strings.Contains(first, "repose sync --discard-remote") || strings.TrimPrefix(after, first+"\n") != stashes {
		t.Errorf("--discard-remote: stash list %q, before %q", after, stashes)
	}
}

// The last-sync stashes are capped at the newest ten; the user's own
// stashes (and --stash-remote's "repose run") are never dropped.
func TestSyncedStashesAreCapped(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("the user's own work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "stash", "push", "-q", "-m", "my work")
	for i := 0; i < 13; i++ { // the first leaves the tree dirty; the next 12 stash
		if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte(strings.Repeat("x", i+1)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}
	list := mustRun(t, f.guestRepo(), "git", "stash", "list", "--format=%gs")
	if n := strings.Count(list, ": repose run: last sync"); n != syncStashKeep {
		t.Errorf("%d last-sync stashes, want %d:\n%s", n, syncStashKeep, list)
	}
	if !strings.Contains(list, ": my work") {
		t.Errorf("the user's stash is gone:\n%s", list)
	}
	// The newest are the ones kept: the top stash holds run 12's tree.
	if b := mustRun(t, f.guestRepo(), "git", "show", "stash@{0}:README.md"); b != strings.Repeat("x", 12) {
		t.Errorf("stash@{0} README.md = %q", b)
	}
}

// A tree that was clean at the probe and written by an agent before the
// apply refuses too: the untracked tar would otherwise overwrite a file at
// a path the laptop also sends.
func TestSyncRefusesAWriteIntoACleanTreeAfterTheProbe(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.local, "notes.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(f.guestRepo(), "notes.md")
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{BeforeApply: func(map[string]string) error {
		return os.WriteFile(agent, []byte("the agent, mid-sync\n"), 0o644)
	}})
	wantDirtyRefusal(t, err)
	if b, _ := os.ReadFile(agent); string(b) != "the agent, mid-sync\n" {
		t.Fatalf("the agent's file was overwritten: %q", b)
	}
}

// A repository with Git LFS attributes in a guest whose LFS filter cannot
// run (no git-lfs) still fingerprints: the second run from the same
// laptop tree goes through instead of exiting 6 every time.
func TestSyncedFingerprintWithoutGitLFS(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	mustRun(t, f.guestRepo(), "git", "config", "filter.lfs.clean", "git-lfs-not-installed clean -- %f")
	mustRun(t, f.guestRepo(), "git", "config", "filter.lfs.smudge", "git-lfs-not-installed smudge -- %f")
	mustRun(t, f.guestRepo(), "git", "config", "filter.lfs.required", "true")
	for name, body := range map[string]string{".gitattributes": "*.bin filter=lfs diff=lfs merge=lfs -text\n", "a.bin": "binary-ish\n"} {
		if err := os.WriteFile(filepath.Join(f.local, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatalf("second sync with an LFS attribute and no git-lfs: %v", err)
	}
}

// repose_fp never touches the real index or object store under a plain
// POSIX sh (dash does not export assignments that precede a function
// call, so GIT_INDEX_FILE=... repose_git add -A staged into the real
// index there).
func TestSyncedFingerprintLeavesTheIndexUnderPOSIXSh(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, repo, "git", "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", "cd "+shQuote(repo)+"\n"+syncedFP+"repose_fp").CombinedOutput()
	if err != nil || strings.HasPrefix(string(out), "failed") {
		t.Fatalf("repose_fp: %v %s", err, out)
	}
	if st := mustRun(t, repo, "git", "status", "--porcelain"); st != "?? new.txt" {
		t.Errorf("status after repose_fp = %q, want the file still untracked", st)
	}
}

// The stash prune drops a stash only while its index still names the
// commit it listed. Here an agent pushes a stash just as the prune starts
// dropping (a git wrapper does it on the prune's first rev-parse or drop):
// every index shifts, and none of the ten newest last-sync stashes may go.
func TestSyncStashPruneSurvivesAShift(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, repo, "git", "init", "-q", "-b", "main")
	mustRun(t, repo, "git", "config", "user.email", "t@x")
	mustRun(t, repo, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "git", "add", "f")
	mustRun(t, repo, "git", "commit", "-q", "-m", "c")
	for i := 1; i <= 12; i++ { // stash@{0} is run 12, stash@{11} run 1
		if err := os.WriteFile(filepath.Join(repo, "f"), []byte(strings.Repeat("r", i)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, repo, "git", "stash", "push", "-q", "-m", "repose run: last sync")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	once := filepath.Join(t.TempDir(), "done")
	wrapper := `#!/bin/sh
case "$1 $2" in
  "rev-parse -q"|"stash drop")
    if [ ! -e ` + once + ` ]; then
      : > ` + once + `
      echo agent > f
      ` + realGit + ` stash push -q -m "the agent's"
    fi ;;
esac
exec ` + realGit + ` "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-e", "-c", pruneSyncStashes)
	cmd.Dir = repo
	cmd.Env = append(filterTestEnv(os.Environ(), "PATH"), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prune: %v\n%s", err, out)
	}
	// Runs 3..12 are the ten newest; every one must still be there.
	var kept []string
	for _, l := range strings.Split(mustRun(t, repo, "git", "stash", "list", "--format=%H"), "\n") {
		kept = append(kept, mustRun(t, repo, "git", "show", l+":f"))
	}
	all := strings.Join(kept, ",")
	for i := 3; i <= 12; i++ {
		if !strings.Contains(","+all+",", ","+strings.Repeat("r", i)+",") {
			t.Errorf("run %d's stash was dropped; left: %s", i, all)
		}
	}
	if !strings.Contains(all, "agent") {
		t.Errorf("the agent's stash was dropped; left: %s", all)
	}
}

// A refused run leaves nothing behind in the guest's object store: the
// probe's fingerprint hashes the agent's files without writing them.
func TestSyncedProbeWritesNoObjects(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	// At a path the laptop writes too, so the run is refused (I-573).
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("brand new content 8d1f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop, later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := mustRun(t, f.guestRepo(), "git", "count-objects")
	_, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	if after := mustRun(t, f.guestRepo(), "git", "count-objects"); after != before {
		t.Errorf("objects before %q, after the refused run %q", before, after)
	}
}
