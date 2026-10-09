package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newSubRepo makes a bare repository with one commit (file s, "sub v1")
// for a submodule to point at, and returns its path.
func newSubRepo(t *testing.T) string {
	t.Helper()
	bare := t.TempDir()
	mustRun(t, bare, "git", "init", "--bare", "-q", "-b", "main", ".")
	seed := t.TempDir()
	mustRun(t, seed, "git", "clone", "-q", bare, ".")
	writeFile(t, filepath.Join(seed, "s"), "sub v1\n")
	mustRun(t, seed, "git", "add", "s")
	mustRun(t, seed, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-m", "sub")
	mustRun(t, seed, "git", "push", "-q", "origin", "main")
	return bare
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// addSubmodule adds bare as a submodule at rel in repo and commits it.
func addSubmodule(t *testing.T, repo, bare, rel string) {
	t.Helper()
	mustRun(t, repo, "git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", bare, rel)
	mustRun(t, repo, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-m", "add "+rel)
}

func subStatus(t *testing.T, dir string) string {
	t.Helper()
	return mustRun(t, dir, "git", "status", "--porcelain", "--ignore-submodules=none")
}

// TestSyncCarriesSubmodules (I-263): a submodule arrives checked out at
// the laptop's submodule HEAD, including a commit that exists only on the
// laptop, with its staged, unstaged and untracked work, and the guest's
// `git status` in both repositories reads as the laptop's. A second run
// with nothing new is "unchanged", not refused as dirty, and a run with
// new laptop work stashes the last sync's submodule changes and goes on.
func TestSyncCarriesSubmodules(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	addSubmodule(t, f.local, newSubRepo(t), "lib")
	sub := filepath.Join(f.local, "lib")

	// A commit in the submodule nobody pushed, not recorded in the
	// superproject either.
	writeFile(t, filepath.Join(sub, "s"), "sub v2, unpushed\n")
	mustRun(t, sub, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-am", "local only")
	// Staged, staged-then-edited, unstaged and untracked work inside it.
	writeFile(t, filepath.Join(sub, "staged.txt"), "staged\n")
	mustRun(t, sub, "git", "add", "staged.txt")
	writeFile(t, filepath.Join(sub, "s"), "sub v3, staged\n")
	mustRun(t, sub, "git", "add", "s")
	writeFile(t, filepath.Join(sub, "s"), "sub v4, unstaged\n")
	writeFile(t, filepath.Join(sub, "notes", "new.md"), "untracked\n")

	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	gsub := filepath.Join(f.guestRepo(), "lib")
	if got, want := mustRun(t, gsub, "git", "rev-parse", "HEAD"), mustRun(t, sub, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest submodule HEAD = %s, want the laptop's %s", got, want)
	}
	for _, rel := range []string{"s", "staged.txt", "notes/new.md"} {
		if got, want := readFile(t, filepath.Join(gsub, rel)), readFile(t, filepath.Join(sub, rel)); got != want {
			t.Fatalf("lib/%s on the guest = %q, want %q", rel, got, want)
		}
	}
	if got, want := subStatus(t, gsub), subStatus(t, sub); got != want {
		t.Fatalf("guest submodule status:\n%s\nlaptop:\n%s", got, want)
	}
	if got, want := subStatus(t, f.guestRepo()), subStatus(t, f.local); got != want {
		t.Fatalf("guest superproject status:\n%s\nlaptop:\n%s", got, want)
	}
	// The guest knows it as a submodule, with the URL .gitmodules names.
	if got := mustRun(t, f.guestRepo(), "git", "config", "--get", "submodule.lib.url"); got == "" {
		t.Fatal("submodule.lib.url is not set in the guest")
	}

	summary, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("second syncGuest: %v", err)
	}
	if !summary.Unchanged {
		t.Fatalf("second sync = %+v, want unchanged", summary)
	}

	// New laptop work in the submodule: the guest's tree is the last
	// sync's own, so it is stashed and the new state laid down.
	writeFile(t, filepath.Join(sub, "s"), "sub v5\n")
	summary, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("third syncGuest: %v", err)
	}
	if !summary.StashedLastSync {
		t.Fatalf("third sync = %+v, want the last sync stashed", summary)
	}
	if got := readFile(t, filepath.Join(gsub, "s")); got != "sub v5\n" {
		t.Fatalf("lib/s after the third sync = %q", got)
	}
	if got, want := subStatus(t, gsub), subStatus(t, sub); got != want {
		t.Fatalf("guest submodule status after the third sync:\n%s\nlaptop:\n%s", got, want)
	}

	// An agent's edit inside the submodule is its work: new laptop work
	// is refused, and the edit is kept.
	writeFile(t, filepath.Join(gsub, "staged.txt"), "the agent's\n")
	writeFile(t, filepath.Join(sub, "s"), "sub v6\n")
	_, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	if got := readFile(t, filepath.Join(gsub, "staged.txt")); got != "the agent's\n" {
		t.Fatalf("the agent's edit was lost: %q", got)
	}
	// --stash-machine puts it in the submodule's stash and syncs.
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{StashRemote: true}); err != nil {
		t.Fatalf("--stash-machine: %v", err)
	}
	if got := mustRun(t, gsub, "git", "stash", "list"); got == "" {
		t.Fatal("no stash in the guest's submodule after --stash-machine")
	}
	if got := readFile(t, filepath.Join(gsub, "s")); got != "sub v6\n" {
		t.Fatalf("lib/s after --stash-machine = %q", got)
	}
}

// TestSyncNestedAndNewSubmodulesIntoAnEmptyGuest (I-263): into a guest
// with no checkout yet, a submodule with a submodule of its own arrives
// with both checked out, and a submodule added on the laptop and only
// staged arrives staged, so `git status` reads the same on both sides.
func TestSyncNestedAndNewSubmodulesIntoAnEmptyGuest(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := os.RemoveAll(f.guestRepo()); err != nil {
		t.Fatal(err)
	}
	inner := newSubRepo(t)
	outer := newSubRepo(t)
	seed := t.TempDir()
	mustRun(t, seed, "git", "clone", "-q", outer, ".")
	addSubmodule(t, seed, inner, "inner")
	mustRun(t, seed, "git", "push", "-q", "origin", "main")
	addSubmodule(t, f.local, outer, "vendor/outer")
	mustRun(t, f.local, "git", "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init", "--recursive")
	writeFile(t, filepath.Join(f.local, "vendor/outer/inner/s"), "edited deep down\n")
	// Added and staged, not committed.
	mustRun(t, f.local, "git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", newSubRepo(t), "staged-sub")

	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	for _, rel := range []string{"vendor/outer", "vendor/outer/inner", "staged-sub"} {
		if got, want := mustRun(t, filepath.Join(f.guestRepo(), rel), "git", "rev-parse", "--show-toplevel"), filepath.Join(f.guestRepo(), rel); got != want {
			t.Fatalf("%s on the guest is not a repository of its own (top %s)", rel, got)
		}
	}
	if got := readFile(t, filepath.Join(f.guestRepo(), "vendor/outer/inner/s")); got != "edited deep down\n" {
		t.Fatalf("nested submodule file = %q", got)
	}
	for _, rel := range []string{"", "vendor/outer", "vendor/outer/inner"} {
		if got, want := subStatus(t, filepath.Join(f.guestRepo(), rel)), subStatus(t, filepath.Join(f.local, rel)); got != want {
			t.Fatalf("status of %q on the guest:\n%s\nlaptop:\n%s", rel, got, want)
		}
	}
	summary, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("second syncGuest: %v", err)
	}
	if !summary.Unchanged {
		t.Fatalf("second sync = %+v, want unchanged", summary)
	}
}

// An agent's commit inside a submodule, with nothing new on the laptop,
// is the guest moving on (I-248): the run attaches, the commit stays.
func TestSyncLeavesAnAgentsSubmoduleCommitAlone(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	addSubmodule(t, f.local, newSubRepo(t), "lib")
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	gsub := filepath.Join(f.guestRepo(), "lib")
	writeFile(t, filepath.Join(gsub, "s"), "the agent's\n")
	mustRun(t, gsub, "git", "-c", "user.email=a@x", "-c", "user.name=a", "commit", "-q", "-am", "agent")
	agentHead := mustRun(t, gsub, "git", "rev-parse", "HEAD")
	summary, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if !summary.GuestAhead {
		t.Fatalf("summary = %+v, want GuestAhead", summary)
	}
	if got := mustRun(t, gsub, "git", "rev-parse", "HEAD"); got != agentHead {
		t.Fatalf("the agent's submodule commit was moved: HEAD %s, want %s", got, agentHead)
	}
}

// The agent pulled newer work into a submodule and committed on top, so
// no ref in the guest's submodule points at a commit the laptop's
// submodule knows; with nothing new on the laptop the submodule is left
// alone all the same (I-284, the submodule half).
func TestSyncLeavesASubmoduleThatPulledPastTheLaptopAlone(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	subBare := newSubRepo(t)
	addSubmodule(t, f.local, subBare, "lib")
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	mate := t.TempDir()
	mustRun(t, mate, "git", "clone", "-q", subBare, ".")
	writeFile(t, filepath.Join(mate, "s"), "sub v2\n")
	mustRun(t, mate, "git", "-c", "user.email=m@x", "-c", "user.name=m", "commit", "-q", "-am", "a teammate's")
	mustRun(t, mate, "git", "push", "-q", "origin", "main")

	gsub := filepath.Join(f.guestRepo(), "lib")
	mustRun(t, gsub, "git", "-c", "protocol.file.allow=always", "fetch", "-q", subBare, "main")
	mustRun(t, gsub, "git", "checkout", "-q", "FETCH_HEAD")
	writeFile(t, filepath.Join(gsub, "s"), "the agent's\n")
	mustRun(t, gsub, "git", "-c", "user.email=a@x", "-c", "user.name=a", "commit", "-q", "-am", "agent")
	agentHead := mustRun(t, gsub, "git", "rev-parse", "HEAD")
	// Nothing in the guest's submodule may still name the laptop's commit.
	laptopSubHead := mustRun(t, filepath.Join(f.local, "lib"), "git", "rev-parse", "HEAD")
	for _, ref := range strings.Fields(mustRun(t, gsub, "git", "for-each-ref", "--points-at", laptopSubHead, "--format=%(refname)")) {
		mustRun(t, gsub, "git", "update-ref", "-d", ref)
	}

	summary, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if !summary.GuestAhead {
		t.Fatalf("summary = %+v, want GuestAhead", summary)
	}
	if got := mustRun(t, gsub, "git", "rev-parse", "HEAD"); got != agentHead {
		t.Fatalf("the agent's submodule commit was moved: HEAD %s, want %s", got, agentHead)
	}
}

// A submodule that is a shallow clone on the laptop cannot be bundled:
// the guest fetches it itself, and when that fails (here git refuses the
// file:// remote) the run goes on with a warning naming it.
func TestSyncShallowSubmoduleFailsSoftly(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	bare := newSubRepo(t)
	seed := t.TempDir()
	mustRun(t, seed, "git", "clone", "-q", bare, ".")
	writeFile(t, filepath.Join(seed, "s"), "v2\n")
	mustRun(t, seed, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-am", "v2")
	mustRun(t, seed, "git", "push", "-q", "origin", "main")
	mustRun(t, f.local, "git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", "--depth", "1", "file://"+bare, "lib")
	mustRun(t, f.local, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-m", "add lib")
	if got := mustRun(t, filepath.Join(f.local, "lib"), "git", "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("fixture: lib is not shallow (%s)", got)
	}
	writeFile(t, filepath.Join(f.local, "lib", "s"), "laptop edit\n")

	summary, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	var warned, notSent bool
	for _, w := range summary.Warnings() {
		if strings.Contains(w, "Submodule lib is empty on the machine") {
			warned = true
		}
		if strings.Contains(w, "Your changes inside submodule lib were not sent") {
			notSent = true
		}
	}
	if !warned || !notSent {
		t.Fatalf("warnings = %q, want the fetch failure and the unsent changes", summary.Warnings())
	}
}

// Under a plain POSIX sh the fingerprint lists every checked-out
// submodule, nested ones too, and moves when a file inside one changes.
func TestSyncedFingerprintCoversSubmodulesUnderPOSIXSh(t *testing.T) {
	f := newSyncFixture(t)
	inner, outer := newSubRepo(t), newSubRepo(t)
	seed := t.TempDir()
	mustRun(t, seed, "git", "clone", "-q", outer, ".")
	addSubmodule(t, seed, inner, "my inner")
	mustRun(t, seed, "git", "push", "-q", "origin", "main")
	addSubmodule(t, f.local, outer, "outer")
	mustRun(t, f.local, "git", "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init", "--recursive")
	fp := func() string {
		out, err := exec.Command("sh", "-c", "cd "+shQuote(f.local)+"\n"+syncedFP+"repose_fp").CombinedOutput()
		if err != nil {
			t.Fatalf("repose_fp: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	before := fp()
	if strings.Contains(before, "failed") || !strings.Contains(before, " outer=") || !strings.Contains(before, " outer/my inner=") {
		t.Fatalf("fingerprint = %q, want both submodules in it", before)
	}
	writeFile(t, filepath.Join(f.local, "outer", "my inner", "s"), "changed\n")
	if after := fp(); after == before || strings.Contains(after, "failed") {
		t.Fatalf("fingerprint after an edit in the nested submodule = %q, before %q", after, before)
	}
}

// .env files inside a checked-out submodule travel like the
// superproject's, with paths from the superproject.
func TestEnvCarryIncludesSubmodules(t *testing.T) {
	f := newSyncFixture(t)
	addSubmodule(t, f.local, newSubRepo(t), "lib")
	sub := filepath.Join(f.local, "lib")
	writeFile(t, filepath.Join(sub, ".gitignore"), ".env\n")
	writeFile(t, filepath.Join(sub, ".env"), "B=2\n")
	envs, err := buildEnvCarry(f.local)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].Rel != "lib/.env" || string(envs[0].Body) != "B=2\n" {
		t.Fatalf("env files = %+v, want lib/.env", envs)
	}
}
