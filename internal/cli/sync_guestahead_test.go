package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// I-248: `repose run` on a machine that changed since the last sync.

// syncedWithALaptopEdit is a fixture after one sync that carried a laptop
// edit, so the guest holds the last sync's key.
func syncedWithALaptopEdit(t *testing.T) *syncFixture {
	t.Helper()
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	return f
}

// The laptop has nothing new: an agent's uncommitted files are left as
// they are, the run goes on, and the summary says so in one line. Only
// the probe's ssh runs.
func TestSyncLeavesTheGuestAloneWhenTheLaptopHasNothingNew(t *testing.T) {
	f := syncedWithALaptopEdit(t)
	agent := map[string]string{"README.md": "the agent's README\n"}
	for i := 0; i < 26; i++ {
		agent[fmt.Sprintf("gen/f%02d.go", i)] = fmt.Sprintf("package gen // %d\n", i)
	}
	for rel, body := range agent {
		p := filepath.Join(f.guestRepo(), rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var s *SyncSummary
	n := countSSH(t, func() {
		var err error
		if s, err = syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{}); err != nil {
			t.Fatalf("sync with nothing new on the laptop: %v", err)
		}
	})
	if !s.GuestAhead || s.Unchanged || n != 1 {
		t.Fatalf("guestAhead=%v unchanged=%v ssh=%d", s.GuestAhead, s.Unchanged, n)
	}
	// Every file counts, each of gen/'s 26 too (I-573).
	want := "Nothing new to sync. The machine has uncommitted changes to 27 files."
	if s.String() != want {
		t.Fatalf("summary = %q\nwant      %q", s.String(), want)
	}
	for rel, body := range agent {
		if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), rel)); string(b) != body {
			t.Fatalf("%s touched: %q", rel, b)
		}
	}
	if list := mustRun(t, f.guestRepo(), "git", "stash", "list"); list != "" {
		t.Fatalf("stashed: %q", list)
	}

	// --stash-remote still syncs over them, keeping them in the stash.
	if s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{StashRemote: true}); err != nil || s.GuestAhead {
		t.Fatalf("--stash-remote: %+v %v", s, err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop\n" {
		t.Fatalf("README.md after --stash-remote = %q", b)
	}
	if list := mustRun(t, f.guestRepo(), "git", "stash", "list"); !strings.Contains(list, "repose sync --stash-remote") {
		t.Fatalf("stash list = %q", list)
	}
}

// An agent that committed on the branch, with nothing new on the laptop:
// the guest stays on its branch (not checked out detached at the laptop's
// older commit), and the line says what is there.
func TestSyncLeavesTheGuestsCommitsAlone(t *testing.T) {
	f := newSyncFixture(t)
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "add", "feature.go")
	mustRun(t, f.guestRepo(), "git", "-c", "user.email=a@x", "-c", "user.name=agent", "commit", "-q", "-m", "agent's commit")
	agentHead := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD")

	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !s.GuestAhead || s.Detached || s.String() != "Nothing new to sync. The machine has 1 commit on main your laptop doesn't have." {
		t.Fatalf("summary %+v %q", s, s.String())
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"); got != agentHead {
		t.Fatalf("guest HEAD moved to %s", got)
	}
	if ref := mustRun(t, f.guestRepo(), "git", "symbolic-ref", "-q", "HEAD"); ref == "" {
		t.Fatal("guest detached")
	}
}

// With new laptop work at paths the machine changed too, the refusal
// names only those paths (eight, then a count) and the two flags; the
// machine's changes elsewhere are not listed (I-573).
func TestSyncRefusalNamesOnlyTheOverlap(t *testing.T) {
	f := syncedWithALaptopEdit(t)
	for i := 0; i < 27; i++ {
		if err := os.WriteFile(filepath.Join(f.guestRepo(), fmt.Sprintf("agent%02d.txt", i)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The laptop adds ten of the agent's 27 paths, with its own content.
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(f.local, fmt.Sprintf("agent%02d.txt", i)), []byte("laptop\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop, later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	msg := err.(*exitError).msg
	want := "Not synced: the machine changed 10 files that your laptop changed too:\n" +
		"  agent00.txt\n  agent01.txt\n  agent02.txt\n  agent03.txt\n  agent04.txt\n  agent05.txt\n  agent06.txt\n  agent07.txt\n" +
		"  and 2 more\n" +
		"`repose sync --stash-remote` moves the machine's changes to its git stash first."
	if msg != want {
		t.Fatalf("message:\n%s\nwant:\n%s", msg, want)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop\n" {
		t.Fatalf("README.md changed by a refused run: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "agent00.txt")); string(b) != "x\n" {
		t.Fatalf("agent00.txt changed by a refused run: %q", b)
	}
}

func TestDirtyTreeErrorShortListHasNoCount(t *testing.T) {
	msg := (&dirtyTreeError{files: []string{"a.go", "b/c.go", "d.go"}}).Error()
	if !strings.Contains(msg, "changed 3 files") || !strings.Contains(msg, "\n  a.go\n  b/c.go\n  d.go\n") || strings.Contains(msg, "more") {
		t.Fatalf("message:\n%s", msg)
	}
	if msg := (&dirtyTreeError{files: []string{"a.go"}}).Error(); !strings.HasPrefix(msg, "Not synced: the machine changed 1 file that") {
		t.Fatalf("one file:\n%s", msg)
	}
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = fmt.Sprintf("f%d", i)
	}
	if msg := (&dirtyTreeError{files: nine}).Error(); strings.Contains(msg, "more") || !strings.Contains(msg, "  f8\n") {
		t.Fatalf("nine files are listed in full:\n%s", msg)
	}
}

// End to end through runRun: a second `repose sync`, from an unchanged
// laptop, goes through, touches nothing and prints the one line. (A plain
// run leaves an existing checkout alone and says nothing, I-367.)
func TestSyncLeavesItAloneWhenOnlyTheMachineChanged(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	env2 := &Env{
		Dir: f.env.Dir, Cfg: f.env.Cfg, Cache: f.env.Cache, Cwd: f.local, HomeDir: f.env.HomeDir,
		Client: f.env.Client, Out: &out, ErrOut: &discardWriter{}, TargetFor: f.env.TargetFor,
	}
	if err := runRun(ctx, env2, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("second runRun: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing new to sync. The machine has uncommitted changes to 1 file.\n") {
		t.Fatalf("output = %q", out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "agent\n" {
		t.Fatalf("README.md = %q", b)
	}
}

// The guest moved past every commit the laptop knows: the agent pulled
// newer work from origin and committed on top, and the laptop never
// fetched. No guest ref points at a commit the laptop has, yet the guest
// has all of the laptop's history, so there is still nothing new to send
// and the agent's branch is left where it is (I-248). Seen live on
// 2026-09-27: the run checked the laptop's older commit out detached over
// an agent's branch.
func TestSyncLeavesTheGuestAloneWhenItPulledPastTheLaptop(t *testing.T) {
	f := newSyncFixture(t)
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	// Someone else pushes; the agent pulls it and commits on top.
	mate := t.TempDir()
	mustRun(t, mate, "git", "clone", "-q", f.bare, ".")
	mustRun(t, mate, "git", "-c", "user.email=m@x", "-c", "user.name=mate", "commit", "-q", "--allow-empty", "-m", "a teammate's commit")
	mustRun(t, mate, "git", "push", "-q", "origin", "main")
	mustRun(t, f.guestRepo(), "git", "pull", "-q", "--ff-only")
	mustRun(t, f.guestRepo(), "git", "-c", "user.email=a@x", "-c", "user.name=agent", "commit", "-q", "--allow-empty", "-m", "agent's commit")
	agentHead := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD")

	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"); got != agentHead {
		t.Fatalf("guest HEAD moved from the agent's %s to %s", agentHead, got)
	}
	if ref := mustRun(t, f.guestRepo(), "git", "symbolic-ref", "-q", "HEAD"); ref != "refs/heads/main" {
		t.Fatalf("guest left main: %q", ref)
	}
	if !s.GuestAhead || s.Detached {
		t.Fatalf("summary %+v %q", s, s.String())
	}
}
