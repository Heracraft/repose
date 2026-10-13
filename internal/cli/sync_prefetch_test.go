package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// prefetchFixture is a sync fixture whose guest has no checkout yet,
// whose "GitHub" serves blobless fetches, with every remote cloned from
// GitHub (hybridCloneURL maps github.com/<name> to the bare repository
// registered under that name) and no size threshold.
func prefetchFixture(t *testing.T) (*syncFixture, map[string]string) {
	t.Helper()
	f := newSyncFixture(t)
	if err := os.RemoveAll(f.guestRepo()); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.bare, "git", "config", "uploadpack.allowFilter", "true")
	repos := map[string]string{"github.com/a/b": f.bare}
	prevURL, prevMin, prevAnon := hybridCloneURL, hybridThresholdKiB, prefetchAnonArgs
	hybridCloneURL = func(remote string) string {
		if p, ok := repos[remote]; ok {
			return "file://" + p
		}
		for _, p := range repos {
			// A submodule's origin is the bare's own path on the laptop.
			if normalizeRemote(p) == remote {
				return "file://" + p
			}
		}
		return ""
	}
	hybridThresholdKiB = 0
	t.Cleanup(func() { hybridCloneURL, hybridThresholdKiB, prefetchAnonArgs = prevURL, prevMin, prevAnon })
	return f, repos
}

// subWithHistory is a bare repository with n commits, served to
// blobless fetches, for a submodule.
func subWithHistory(t *testing.T, n int) string {
	t.Helper()
	bare := newSubRepo(t)
	mustRun(t, bare, "git", "config", "uploadpack.allowFilter", "true")
	seed := t.TempDir()
	mustRun(t, seed, "git", "clone", "-q", bare, ".")
	for i := 0; i < n-1; i++ {
		writeFile(t, filepath.Join(seed, "s"), strings.Repeat("v", i+2)+"\n")
		mustRun(t, seed, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-am", "more")
	}
	mustRun(t, seed, "git", "push", "-q", "origin", "main")
	return bare
}

// I-713: the guest's fetch costs no round trip of its own. A first sync
// that fetches from GitHub is the probe and the apply, as any sync is.
func TestPrefetchIsInTheApplysSSH(t *testing.T) {
	f, _ := prefetchFixture(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	var s *SyncSummary
	n := countSSH(t, func() {
		var err error
		if s, err = syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"}); err != nil {
			t.Fatalf("sync: %v", err)
		}
	})
	if n != 2 {
		t.Errorf("a first sync that fetches from GitHub took %d ssh commands, want 2 (probe, apply)", n)
	}
	if s.ClonedFrom != "github.com" || s.Commits != 1 {
		t.Fatalf("summary = %+v, want the history from github.com and 1 commit sent", s)
	}
	if got, want := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD %s, laptop %s", got, want)
	}
}

// I-714: a public repository is fetched without blobs, its checkout is
// whole, and the backfill brings the rest of the history and removes
// the promisor remote; origin is left as the sync sets it.
func TestPrefetchPublicIsBloblessThenBackfilled(t *testing.T) {
	f, _ := prefetchFixture(t)
	// History with blobs no checkout needs.
	other := t.TempDir()
	mustRun(t, other, "git", "clone", "-q", f.bare, ".")
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(other, "old.txt"), strings.Repeat("o", i+1)+"\n")
		mustRun(t, other, "git", "add", "old.txt")
		mustRun(t, other, "git", "-c", "user.email=o@x", "-c", "user.name=o", "commit", "-q", "-m", "old")
	}
	mustRun(t, other, "git", "rm", "-q", "old.txt")
	mustRun(t, other, "git", "-c", "user.email=o@x", "-c", "user.name=o", "commit", "-q", "-m", "gone")
	mustRun(t, other, "git", "push", "-q", "origin", "main")
	mustRun(t, f.local, "git", "pull", "-q", "--ff-only")

	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.ClonedFrom == "" || s.Commits != 0 {
		t.Fatalf("summary = %+v, want the whole history from GitHub", s)
	}
	g := f.guestRepo()
	if got := readFile(t, filepath.Join(g, "README.md")); got != "hello\n" {
		t.Fatalf("README.md on the guest = %q", got)
	}
	if got, want := subStatus(t, g), subStatus(t, f.local); got != want {
		t.Fatalf("guest status:\n%s\nlaptop:\n%s", got, want)
	}
	if got := mustRun(t, g, "git", "remote", "get-url", "origin"); got != "git@github.com:a/b.git" {
		t.Errorf("origin = %q, want the sync's own", got)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := os.Stat(filepath.Join(g, backfillMarker))
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the backfill did not finish: %s", mustRun(t, g, "git", "config", "--get-regexp", "^remote\\."))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out := mustRun(t, g, "git", "rev-list", "--all", "--objects", "--missing=print"); strings.Contains(out, "\n?") || strings.HasPrefix(out, "?") {
		t.Fatalf("objects still missing after the backfill:\n%s", out)
	}
	if out := mustRun(t, g, "git", "config", "--get-regexp", "^remote\\."); strings.Contains(out, backfillRemote) {
		t.Fatalf("the promisor remote is still there:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(g, prefetchDir)); !os.IsNotExist(err) {
		t.Fatalf("%s left behind: %v", prefetchDir, err)
	}
}

// I-713: a repository the anonymous fetch cannot read is fetched in full
// once the apply has copied the logins, not blobless.
func TestPrefetchPrivateFetchesInFull(t *testing.T) {
	f, _ := prefetchFixture(t)
	prefetchAnonArgs = "-c protocol.file.allow=never"
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.ClonedFrom == "" || s.CloneFailed != "" || s.Commits != 0 {
		t.Fatalf("summary = %+v, want a full fetch", s)
	}
	g := f.guestRepo()
	if out, _ := gitCmd(g, "config", "--get-regexp", "^remote\\."); strings.Contains(out, "promisor") {
		t.Fatalf("a private repository was fetched blobless:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(g, backfillMarker)); !os.IsNotExist(err) {
		t.Fatalf("a backfill was started for a full fetch")
	}
}

// I-713: the laptop's view of origin names a commit GitHub does not have
// (force-pushed away), so the first payload's bundle lacks its base; the
// apply stops before touching the checkout and the second payload,
// bundled against what the guest fetched, lands it.
func TestPrefetchMissingBaseSendsAgain(t *testing.T) {
	f, _ := prefetchFixture(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "pushed, then force-pushed away\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "gone from GitHub")
	gone := mustRun(t, f.local, "git", "rev-parse", "HEAD")
	mustRun(t, f.local, "git", "update-ref", "refs/remotes/origin/main", gone)
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")

	// run's EnvLater is a channel read: a second call would block for good.
	envCalls := 0
	envLater := func() []envFile {
		if envCalls++; envCalls > 1 {
			t.Fatal("EnvLater called twice")
		}
		return nil
	}
	var s *SyncSummary
	n := countSSH(t, func() {
		var err error
		if s, err = syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b", EnvLater: envLater}); err != nil {
			t.Fatalf("sync: %v", err)
		}
	})
	if n != 3 {
		t.Errorf("%d ssh commands, want 3 (probe, apply, second apply)", n)
	}
	if s.ClonedFrom == "" || s.Commits != 2 {
		t.Fatalf("summary = %+v, want 2 commits sent after the fetch", s)
	}
	if got, want := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD %s, laptop %s", got, want)
	}
}

// I-712: a submodule's history comes from GitHub too; the laptop sends
// only its commit GitHub lacks, and the guest's copy is checked out at
// the laptop's submodule HEAD with its origin set.
func TestPrefetchSubmoduleFromGitHub(t *testing.T) {
	f, repos := prefetchFixture(t)
	subBare := subWithHistory(t, 6)
	repos["github.com/x/lib"] = subBare
	addSubmodule(t, f.local, subBare, "lib")
	mustRun(t, f.local, "git", "push", "-q", "origin", "main")
	sub := filepath.Join(f.local, "lib")
	writeFile(t, filepath.Join(sub, "s"), "unpushed in the submodule\n")
	mustRun(t, sub, "git", "-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-am", "local only")

	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.Commits != 1 || len(s.SubCloneFailed) > 0 {
		t.Fatalf("summary = %+v, want 1 commit sent (the submodule's unpushed one), not its history", s)
	}
	gsub := filepath.Join(f.guestRepo(), "lib")
	if got, want := mustRun(t, gsub, "git", "rev-parse", "HEAD"), mustRun(t, sub, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest submodule HEAD %s, laptop %s", got, want)
	}
	if got := mustRun(t, gsub, "git", "rev-list", "--count", "HEAD"); got != "7" {
		t.Fatalf("guest submodule history = %s commits, want 7", got)
	}
	if got, want := subStatus(t, gsub), subStatus(t, sub); got != want {
		t.Fatalf("guest submodule status:\n%s\nlaptop:\n%s", got, want)
	}
	if got := mustRun(t, gsub, "git", "remote"); got != "origin" {
		t.Fatalf("guest submodule remotes = %q", got)
	}
	// The next sync is a delta, with nothing fetched.
	s, err = syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil || !s.Unchanged {
		t.Fatalf("second sync = %+v %v, want unchanged", s, err)
	}
}

// I-712: a submodule GitHub cannot serve falls back to the bundle with
// a line naming it.
func TestPrefetchSubmoduleFallsBack(t *testing.T) {
	f, repos := prefetchFixture(t)
	subBare := subWithHistory(t, 3)
	repos["github.com/x/lib"] = subBare
	addSubmodule(t, f.local, subBare, "lib")
	if err := os.RemoveAll(subBare); err != nil {
		t.Fatal(err)
	}
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(s.SubCloneFailed) != 1 || !strings.Contains(strings.Join(s.Warnings(), "\n"), "could not fetch submodule lib from GitHub") {
		t.Fatalf("summary = %+v %v, want the submodule's fetch failure named", s, s.Warnings())
	}
	if s.Commits < 3 {
		t.Fatalf("summary = %+v, want the submodule's history sent", s)
	}
	gsub := filepath.Join(f.guestRepo(), "lib")
	if got, want := mustRun(t, gsub, "git", "rev-parse", "HEAD"), mustRun(t, filepath.Join(f.local, "lib"), "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest submodule HEAD %s, laptop %s", got, want)
	}
}

// I-715: the summary line ends with the sync's time, as a ✓ line does.
func TestSyncResultLineHasItsTime(t *testing.T) {
	s := &SyncSummary{Modified: 1, Took: 2300 * time.Millisecond}
	if got := syncResultLine(s, false, ""); got != "Synced: 1 modified, 0 untracked  2.3s" {
		t.Fatalf("line = %q", got)
	}
	s = &SyncSummary{GuestAhead: true, GuestFiles: 1, Took: time.Second}
	if got := syncResultLine(s, false, ""); strings.Contains(got, "1.0s") {
		t.Fatalf("nothing-new line has a time: %q", got)
	}
}
