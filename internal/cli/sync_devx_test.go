package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// DECISIONS I-618: the sync package of the 2026-10-08 CLI reviews.

// --discard-remote and --stash-remote move every change on the machine to
// a named stash and say how many files went and as which stash commit.
func TestSyncRemoteFlagsStashAndSaySo(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts SyncOptions
		msg  string
	}{
		{"discard", SyncOptions{DiscardRemote: true}, "repose sync --discard-remote"},
		{"stash", SyncOptions{StashRemote: true}, "repose sync --stash-remote"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			g := f.guestRepo()
			writeAt(t, g, "README.md", "agent was here\n")
			writeAt(t, g, "gen/new.go", "package gen\n")
			writeAt(t, g, "gen/other.go", "package gen\n")
			writeAt(t, f.local, "laptop.txt", "laptop\n")
			s, err := syncGuest(context.Background(), f.target, f.local, testSlug, tc.opts)
			if err != nil {
				t.Fatalf("sync: %v", err)
			}
			if st := mustRun(t, g, "git", "status", "--porcelain"); st != "?? laptop.txt" {
				t.Fatalf("machine's status %q, want only the laptop's file", st)
			}
			list := mustRun(t, g, "git", "stash", "list")
			if !strings.Contains(list, tc.msg) {
				t.Fatalf("stash list %q, want %q", list, tc.msg)
			}
			ref := mustRun(t, g, "git", "rev-parse", "--short", "stash@{0}")
			if s.StashedFiles != 3 || s.StashRef != ref {
				t.Fatalf("summary stashed %d as %q, want 3 as %q", s.StashedFiles, s.StashRef, ref)
			}
			t.Logf("sync --%s-remote printed: %s", tc.name, s.String())
			want := "Synced: 0 modified, 1 untracked; stashed the machine's changes to 3 files (git stash " + ref + ")"
			if s.String() != want {
				t.Fatalf("summary %q\nwant    %q", s.String(), want)
			}
			// The stash holds the agent's work, untracked files included.
			mustRun(t, g, "git", "stash", "apply", "-q", ref)
			wantFile(t, g, "gen/new.go", "package gen\n")
			wantFile(t, g, "README.md", "agent was here\n")
		})
	}
}

// A clean machine stashes nothing and says nothing about a stash.
func TestSyncDiscardRemoteOnACleanMachine(t *testing.T) {
	f := newSyncFixture(t)
	writeAt(t, f.local, "laptop.txt", "laptop\n")
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{DiscardRemote: true})
	if err != nil {
		t.Fatal(err)
	}
	if list := mustRun(t, f.guestRepo(), "git", "stash", "list"); list != "" || s.StashedFiles != 0 || strings.Contains(s.String(), "stash") {
		t.Fatalf("stash list %q, summary %q", list, s.String())
	}
}

// The overlap refusal offers the flag that keeps the machine's work, and
// not the one that used to throw it away.
func TestOverlapRefusalOffersOnlyTheStash(t *testing.T) {
	msg := (&dirtyTreeError{files: []string{"a.go"}}).Error()
	if strings.Contains(msg, "--discard-remote") || !strings.Contains(msg, "`repose sync --stash-remote` moves the machine's changes to its git stash first.") {
		t.Fatalf("refusal:\n%s", msg)
	}
}

// With nothing new on the laptop, the line counts the machine's commits
// and its uncommitted files apart.
func TestGuestAheadLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    SyncSummary
		want string
	}{
		{"files", SyncSummary{GuestFiles: 2, GuestCommitCount: -1}, "Nothing new to sync. The machine has uncommitted changes to 2 files."},
		{"one file", SyncSummary{GuestFiles: 1, GuestCommitCount: -1}, "Nothing new to sync. The machine has uncommitted changes to 1 file."},
		{"commits", SyncSummary{GuestCommits: true, GuestCommitCount: 3, GuestBranch: "main"}, "Nothing new to sync. The machine has 3 commits on main your laptop doesn't have."},
		{"both", SyncSummary{GuestCommits: true, GuestCommitCount: 1, GuestBranch: "fix", GuestFiles: 2}, "Nothing new to sync. The machine has 1 commit on fix your laptop doesn't have, and uncommitted changes to 2 files."},
		{"detached, uncounted", SyncSummary{GuestCommits: true, GuestCommitCount: -1}, "Nothing new to sync. The machine has commits your laptop doesn't have."},
		{"fetched already", SyncSummary{GuestCommits: true, GuestCommitCount: 0, GuestBranch: "main"}, nothingNewLine},
		{"only the last sync's", SyncSummary{GuestCommitCount: -1}, nothingNewLine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.GuestAhead = true
			if got := tc.s.String(); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// The machine's commits since the last sync are counted on the machine,
// and drop to none once the laptop has fetched them.
func TestSyncCountsTheMachinesCommits(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	g := f.guestRepo()
	for _, n := range []string{"a", "b"} {
		writeAt(t, g, n+".go", "package "+n+"\n")
		mustRun(t, g, "git", "add", n+".go")
		mustRun(t, g, "git", "commit", "-q", "-m", "agent "+n)
	}
	writeAt(t, g, "wip.go", "package wip\n")
	s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sync printed: %s", s.String())
	if want := "Nothing new to sync. The machine has 2 commits on main your laptop doesn't have, and uncommitted changes to 1 file."; s.String() != want {
		t.Fatalf("got  %q\nwant %q", s.String(), want)
	}
	// `git fetch repose`, as the user would.
	mustRun(t, f.local, "git", "fetch", "-q", g, "main")
	s, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after git fetch, sync printed: %s", s.String())
	if want := "Nothing new to sync. The machine has uncommitted changes to 1 file."; s.String() != want {
		t.Fatalf("after the fetch: %q, want %q", s.String(), want)
	}
}

// A carry line prints when it is new or changed, not on every run of the
// same project; a failure always prints.
func TestCarryNoterPrintsOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	run := func(project string, lines ...string) []string {
		n := newCarryNoter(dir, project)
		var printed []string
		for _, l := range lines {
			if n.fresh(l) {
				printed = append(printed, l)
			}
		}
		n.save()
		return printed
	}
	same := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("printed %q, want %q", got, want)
		}
	}
	creds, kept := "Logins copied: gh, codex", "Kept the machine's codex login: it is newer than the laptop's."
	fail := "Could not carry your git config; the machine keeps its previous one."
	same(run("p1", creds, kept, fail), creds, kept, fail)
	same(run("p1", creds, kept, fail), fail)
	same(run("p2", creds), creds) // another project has its own record
	same(run("p1", "Logins copied: gh"), "Logins copied: gh")
	same(run("p1", creds, kept), creds, kept) // gone for a run, so new again
	b, err := os.ReadFile(filepath.Join(dir, carryNotedName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "codex") {
		t.Fatalf("the file holds a line's text: %s", b)
	}
	var m map[string][]string
	if err := json.Unmarshal(b, &m); err != nil || len(m["p1"]) != 2 {
		t.Fatalf("file %s: %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, carryNotedName)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", fi.Mode(), err)
	}
}

// The line a run printed before its attach is shown on the multiplexer
// too, where the attach puts the user (G6).
func TestSessionShowsTheRunsMessages(t *testing.T) {
	var mu sync.Mutex
	var shown []string
	opts := sessionOptions{Slug: testSlug, Messages: []string{"Not synced: your laptop has work the machine doesn't (1 commit). `repose sync` sends it."}}
	if err := runSessionWith(context.Background(), opts, func() bool { return false }, func(m string) {
		mu.Lock()
		defer mu.Unlock()
		shown = append(shown, m)
	}); err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 || shown[0] != opts.Messages[0] {
		t.Fatalf("shown %q", shown)
	}
}

// PROJECT:CHECKOUT on a command that acts on the whole machine is refused,
// not dropped (B7).
func TestWholeMachineCommandsRefuseACheckout(t *testing.T) {
	e := &Env{Command: "repose status"}
	err := wholeMachineOnly(e, "todo-app:api")
	wantExit(t, "status", err, ExitUsage, "`repose status` acts on the whole machine, so it takes todo-app, not todo-app:api.")
	e.Command = "repose secrets list --project"
	err = wholeMachineOnly(e, "todo-app:api")
	wantExit(t, "secrets", err, ExitUsage, "`repose secrets list` acts on the whole machine, so it takes todo-app, not todo-app:api.")
	if err := wholeMachineOnly(e, "todo-app"); err != nil {
		t.Fatal(err)
	}
}

// A checkout the machine does not list is refused, and never made; `rm
// PROJECT:CHECKOUT` removes one `run --on` added, with its worktree, and
// the laptop folder's link, after refusing while something works in it
// (B7).
func TestCheckoutsAreNamedNotMade(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// A typo syncs nothing and makes no directory.
	err := runRun(ctx, f.env, RunOptions{ProjectArg: testSlug + ":apii", NoAttach: true, Sync: true}, false)
	t.Logf("sync %s:apii: %v", testSlug, err)
	wantExit(t, "sync typo", err, ExitUsage, testSlug+" has no checkout apii. `repose run --on "+testSlug+"` in its folder adds it.")
	if _, err := os.Stat(filepath.Join(f.guestHome, "apii")); err == nil {
		t.Fatal("the typo made ~/apii")
	}
	err = RemoveCheckoutCmd(ctx, f.env, testSlug+":apii", true, nil)
	wantExit(t, "rm with none", err, ExitUsage, testSlug+" has no checkouts besides its own.")

	api := newSecondRepo(t, "api")
	f.env.Cwd = api
	if err := runRun(ctx, f.env, RunOptions{On: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("run --on: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(f.guestHome, "api-worktree-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, filepath.Join(f.guestHome, "api"), "git", "worktree", "add", "-q", "-b", "worktree-1", filepath.Join(f.guestHome, "api-worktree-1"))
	f.env.Cwd = f.local

	err = RemoveCheckoutCmd(ctx, f.env, testSlug+":apii", true, nil)
	wantExit(t, "rm typo", err, ExitUsage, testSlug+" has no checkout apii. Its checkouts: api.")
	err = DestroyCmd(ctx, f.env, testSlug+":api", false, false, nil)
	wantExit(t, "rm without -y off a terminal", err, ExitUsage, "Removing "+testSlug+":api needs a confirmation; pass --yes to skip it.")

	// A shell in the worktree holds it.
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n api/sh -c ~/api-worktree-1 'exec cat'", nil); err != nil {
		t.Fatal(err)
	}
	err = RemoveCheckoutCmd(ctx, f.env, testSlug+":api", true, nil)
	t.Logf("rm %s:api while a shell works there: %v", testSlug, err)
	wantExit(t, "rm busy", err, ExitDirtyRemoteTree, "Not removed: 1 process on "+testSlug+" works in ~/api. Close the agents and shells there first.")
	if _, err := runSSH(ctx, f.target, "tmux kill-window -t "+testSlug+":api/sh", nil); err != nil {
		t.Fatal(err)
	}

	asked := ""
	out := &discardWriter{}
	f.env.Out = out
	if err := DestroyCmd(ctx, f.env, testSlug+":api", false, false, func(p string) (bool, error) { asked = p; return true, nil }); err != nil {
		t.Fatalf("rm %s:api: %v", testSlug, err)
	}
	t.Logf("rm %s:api asked %q and printed %q", testSlug, asked, out.buf.String())
	if !strings.HasPrefix(asked, "Remove ~/api from "+testSlug+"?") {
		t.Fatalf("asked %q", asked)
	}
	if got := out.buf.String(); got != "Removed ~/api from "+testSlug+".\n" {
		t.Fatalf("said %q", got)
	}
	for _, d := range []string{"api", "api-worktree-1"} {
		if _, err := os.Stat(filepath.Join(f.guestHome, d)); err == nil {
			t.Fatalf("~/%s is still there", d)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".repose", "checkouts")); strings.TrimSpace(string(b)) != "" {
		t.Fatalf("checkouts file still lists %q", b)
	}
	if _, ok := f.env.Cache.Checkouts[api]; ok {
		t.Fatal("the folder still links to the removed checkout")
	}
	disk, err := loadProjectsCache(f.env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := disk.Checkouts[api]; ok {
		t.Fatal("projects.json still links the folder")
	}
	if _, err := os.Stat(f.guestRepo()); err != nil {
		t.Fatalf("the machine's own checkout went: %v", err)
	}
	projects, err := f.env.Client.ListProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0].State == "destroying" {
		t.Fatalf("projects %+v %v", projects, err)
	}
}
