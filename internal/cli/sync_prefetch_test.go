package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// waitBackfilled waits for the guest checkout g to be whole: no promisor
// remote, no marker, no prefetch directory, no missing object.
func waitBackfilled(t *testing.T, g string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		remotes, _ := gitCmd(g, "config", "--get-regexp", "^remote\\.")
		_, merr := os.Stat(filepath.Join(g, backfillMarker))
		_, perr := os.Stat(filepath.Join(g, prefetchDir))
		missing, _ := gitCmd(g, "rev-list", "--all", "--objects", "--missing=print")
		hasMissing := strings.HasPrefix(missing, "?") || strings.Contains(missing, "\n?")
		if !strings.Contains(remotes, backfillRemote) && os.IsNotExist(merr) && os.IsNotExist(perr) && !hasMissing {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the checkout is still partial: remotes:\n%s\nmarker: %v, %s: %v, objects missing: %v", remotes, merr == nil, prefetchDir, perr == nil, hasMissing)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// oldBlobHistory pushes commits to f.bare whose blobs no checkout of its
// tip needs, and pulls them into the laptop.
func oldBlobHistory(t *testing.T, f *syncFixture) {
	t.Helper()
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
}

// I-714: a first sync refused after its blobless fetch finished (here by
// the overlap check; Ctrl-C or a dropped upload stop it at the same
// point) does not leave the checkout partial: the next sync, which
// fetches nothing, still backfills it and removes the promisor remote.
func TestPrefetchRefusedFirstSyncStillBackfills(t *testing.T) {
	f, _ := prefetchFixture(t)
	oldBlobHistory(t, f)
	g := f.guestRepo()
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, g, "git", "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(g, "notes.txt"), "the machine's\n")
	writeFile(t, filepath.Join(f.local, "notes.txt"), "the laptop's\n")

	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"}); err == nil {
		t.Fatal("the first sync was not refused over notes.txt")
	}
	if out, _ := gitCmd(g, "config", "--get-regexp", "^remote\\."); !strings.Contains(out, backfillRemote) {
		t.Fatalf("no promisor remote after the refused sync, so nothing partial to test:\n%s", out)
	}
	if err := os.Remove(filepath.Join(g, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := readFile(t, filepath.Join(g, "notes.txt")); got != "the laptop's\n" || s.Untracked != 1 {
		t.Fatalf("second sync = %+v, notes.txt %q", s, got)
	}
	waitBackfilled(t, g)
}

// I-713: when a fetch falls short, the second payload carries only the
// bundles; the untracked files, the diffs and the .env files of the
// first, already on the guest, are not sent again.
func TestPrefetchResendCarriesOnlyBundles(t *testing.T) {
	f, _ := prefetchFixture(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "pushed, then force-pushed away\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "gone from GitHub")
	gone := mustRun(t, f.local, "git", "rev-parse", "HEAD")
	mustRun(t, f.local, "git", "update-ref", "refs/remotes/origin/main", gone)
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	writeFile(t, filepath.Join(f.local, "README.md"), "unstaged\n")
	writeFile(t, filepath.Join(f.local, "big.bin"), strings.Repeat("u", 1<<20))

	var sent [][]string
	observeSyncPayload = func(names []string) { sent = append(sent, names) }
	t.Cleanup(func() { observeSyncPayload = nil })
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("%d payloads, want 2: %v", len(sent), sent)
	}
	if got := strings.Join(sent[1], " "); got != "bundle" {
		t.Errorf("second payload = %q, want only the bundle", got)
	}
	if !strings.Contains(strings.Join(sent[0], " "), "untracked.tar") {
		t.Errorf("first payload = %v, want the untracked files in it", sent[0])
	}
	g := f.guestRepo()
	if got := readFile(t, filepath.Join(g, "README.md")); got != "unstaged\n" {
		t.Errorf("README.md on the guest = %q", got)
	}
	if got := readFile(t, filepath.Join(g, "big.bin")); len(got) != 1<<20 {
		t.Errorf("big.bin on the guest is %d bytes", len(got))
	}
	if got, want := mustRun(t, g, "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want || s.Commits != 2 {
		t.Fatalf("guest HEAD %s, laptop %s, summary %+v", got, want, s)
	}
	if _, err := os.Stat(filepath.Join(g, prefetchDir)); !os.IsNotExist(err) {
		t.Errorf("%s left behind: %v", prefetchDir, err)
	}
}

// I-713: the second apply finds the first payload gone (another sync's
// probe cleared it), says so, and the laptop sends it whole.
func TestPrefetchResendWithoutTheKeptPayload(t *testing.T) {
	f, _ := prefetchFixture(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "pushed, then force-pushed away\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "gone from GitHub")
	gone := mustRun(t, f.local, "git", "rev-parse", "HEAD")
	mustRun(t, f.local, "git", "update-ref", "refs/remotes/origin/main", gone)
	writeFile(t, filepath.Join(f.local, "new.txt"), "untracked\n")

	var sent [][]string
	observeSyncPayload = func(names []string) {
		if sent = append(sent, names); len(sent) == 2 {
			// The probe made the checkout; guestRepo finds it now.
			if err := os.RemoveAll(filepath.Join(f.guestRepo(), prefetchDir, "payload")); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { observeSyncPayload = nil })
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(sent) != 3 || !strings.Contains(strings.Join(sent[2], " "), "untracked.tar") {
		t.Fatalf("payloads = %v, want a third, whole one", sent)
	}
	if got := readFile(t, filepath.Join(f.guestRepo(), "new.txt")); got != "untracked\n" {
		t.Errorf("new.txt on the guest = %q", got)
	}
}

// Review round 2: in a blobless checkout, the apply's check for the
// laptop-only commit it checks out must not ask GitHub for it, and the
// laptop's bundle is unbundled, not fetched. A
// missing object there makes git lazy-fetch from the promisor remote,
// a round trip that fails (GitHub lacks the commit) on the apply's
// critical path, once per such commit: 7 s on a prod guest.
func TestPrefetchWantCheckDoesNotLazyFetch(t *testing.T) {
	f, _ := prefetchFixture(t)
	oldBlobHistory(t, f)
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	trace := filepath.Join(t.TempDir(), "trace2.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.ClonedFrom == "" || s.Commits != 1 {
		t.Fatalf("summary = %+v, want the history from GitHub and 1 commit sent", s)
	}
	waitBackfilled(t, f.guestRepo())
	// A lazy fetch is a child `git fetch repose-backfill ... --stdin`; one
	// for an object GitHub lacks exits non-zero.
	type ev struct {
		Event string   `json:"event"`
		SID   string   `json:"sid"`
		Child int      `json:"child_id"`
		Argv  []string `json:"argv"`
		Code  int      `json:"code"`
	}
	lazy := map[string][]string{}
	var failed, walks []string
	for _, l := range strings.Split(readFile(t, trace), "\n") {
		var e ev
		if json.Unmarshal([]byte(l), &e) != nil {
			continue
		}
		k := e.SID + "/" + strconv.Itoa(e.Child)
		switch e.Event {
		case "child_start":
			a := strings.Join(e.Argv, " ")
			if strings.Contains(a, "fetch "+backfillRemote) && strings.Contains(a, "--stdin") {
				lazy[k] = e.Argv
			}
			// A fetch's connectivity check: in a blobless checkout it
			// enumerates every promisor object, 17 s for cpython's
			// history on a prod guest, where unbundling the same bundle
			// took 0.03 s.
			if strings.Contains(a, "rev-list --objects --stdin --exclude-promisor-objects") {
				walks = append(walks, a)
			}
		case "child_exit":
			if argv, ok := lazy[k]; ok && e.Code != 0 {
				failed = append(failed, strings.Join(argv, " "))
			}
		}
	}
	if len(walks) > 0 {
		t.Errorf("the apply walked the promisor objects %d times (a fetch's connectivity check):\n%s", len(walks), strings.Join(walks, "\n"))
	}
	if len(failed) > 0 {
		t.Fatalf("%d lazy fetches failed during the sync (an object GitHub lacks was asked for):\n%s", len(failed), strings.Join(failed, "\n"))
	}
}

// Review round 2: `git backfill --all` needs revision arguments (git
// 2.54). A guest whose git has backfill without them is fetched in full,
// since its backfill would fail on every sync and leave the checkout
// partial for good.
func TestPrefetchBloblessNeedsBackfillRevisions(t *testing.T) {
	f, _ := prefetchFixture(t)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "git"), "#!/bin/sh\n"+
		"if [ \"$1\" = -C ] && [ \"$3\" = backfill ] && [ \"$4\" = -h ]; then\n"+
		"  echo 'usage: git backfill [--min-batch-size=<n>] [--[no-]sparse]'; exit 129\nfi\n"+
		"exec "+real+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.ClonedFrom == "" || s.CloneFailed != "" {
		t.Fatalf("summary = %+v, want the history from GitHub", s)
	}
	g := f.guestRepo()
	if out, _ := gitCmd(g, "config", "--get-regexp", "^remote\\."); strings.Contains(out, "promisor") {
		t.Fatalf("fetched blobless on a git whose backfill takes no revisions:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(g, backfillMarker)); !os.IsNotExist(err) {
		t.Fatalf("a backfill was marked for a full fetch")
	}
}

// Review round 2: REPOSE_TIMING's `sync payload build` of a second
// payload times that payload's build alone, not the first apply too.
func TestPrefetchResendTimesItsOwnBuild(t *testing.T) {
	f, _ := prefetchFixture(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "pushed, then force-pushed away\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "gone from GitHub")
	mustRun(t, f.local, "git", "update-ref", "refs/remotes/origin/main", mustRun(t, f.local, "git", "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	var buf strings.Builder
	prev := timingOut
	timingOut = &buf
	t.Cleanup(func() { timingOut = prev })
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	timingOut = prev
	at := func(l string) int64 {
		f := strings.Fields(l)
		n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(f[1], "+"), "ms"), 10, 64)
		return n
	}
	var again int64 = -1
	builds := 0
	for _, l := range strings.Split(buf.String(), "\n") {
		switch {
		case strings.Contains(l, "sync: the fetches lack a base, sending again"):
			again = at(l)
		case strings.Contains(l, " sync payload build "):
			builds++
			if again < 0 {
				continue
			}
			f := strings.Fields(l)
			ms, _ := strconv.ParseInt(strings.TrimSuffix(f[len(f)-1], "ms"), 10, 64)
			if ms > at(l)-again+2 {
				t.Fatalf("the second payload's build took %d ms by its line, but it started %d ms before:\n%s", ms, at(l)-again, buf.String())
			}
		}
	}
	if again < 0 || builds != 2 {
		t.Fatalf("want a resend and two build lines:\n%s", buf.String())
	}
}

// I-742: REPOSE_TIMING names each step of the guest's apply with its
// time, so a slow first sync says where the guest spent it.
func TestApplyStepsAreTimed(t *testing.T) {
	f, _ := prefetchFixture(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	var buf strings.Builder
	prev := timingOut
	timingOut = &buf
	t.Cleanup(func() { timingOut = prev })
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	timingOut = prev
	var steps []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if _, rest, ok := strings.Cut(l, " sync apply "); ok {
			fs := strings.Fields(rest)
			if len(fs) != 2 || !strings.HasSuffix(fs[1], "ms") {
				t.Fatalf("line %q", l)
			}
			steps = append(steps, fs[0])
		}
	}
	if got, want := strings.Join(steps, " "), "upload fetch checks guard checkout diffs submodules record end"; got != want {
		t.Fatalf("apply steps %q, want %q:\n%s", got, want, buf.String())
	}
}

func TestApplyStepsParse(t *testing.T) {
	got := applySteps("x\n#t start 100.000000\n#carry y\n#t upload 101,250000\n#t bad\n#t end 101.2505\n")
	if strings.Join(got, ",") != "upload 1250ms,end 1ms" {
		t.Fatalf("applySteps = %q", got)
	}
}

// I-744: a project with no remote of its own (a temporary machine,
// I-351) is fetched from the checkout's own origin, as its submodules
// already are, instead of having its whole history sent from the laptop.
func TestPrefetchWithoutAProjectRemoteUsesTheCheckoutsOrigin(t *testing.T) {
	f, _ := prefetchFixture(t)
	// The checkout's origin is on GitHub (the fixture's github.com/a/b).
	mustRun(t, f.local, "git", "remote", "set-url", "origin", "git@github.com:a/b.git")
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{NoRemote: true})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.ClonedFrom != "github.com" || s.Commits != 0 {
		t.Fatalf("summary = %+v, want the history fetched by the machine and no commit sent", s)
	}
	g := f.guestRepo()
	if got := readFile(t, filepath.Join(g, "README.md")); got != "hello\n" {
		t.Fatalf("README.md on the guest = %q", got)
	}
	// A project without a remote still gets no origin (I-351).
	if out := mustRun(t, g, "git", "remote"); strings.Contains(out, "origin") {
		t.Errorf("the guest has an origin: %q", out)
	}
	// The backfill ends before the test does, or its writes race the
	// cleanup of the guest's home.
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := os.Stat(filepath.Join(g, backfillMarker))
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the backfill did not finish")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
