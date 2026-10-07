package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DECISIONS I-573: a sync refuses only when the guest changed a path the
// sync writes. The guest's changes anywhere else stay where they are.

func writeAt(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	if string(b) != body {
		t.Fatalf("%s = %q, want %q", rel, b, body)
	}
}

// No overlap: the laptop's commit, edit and untracked file land, and the
// agent's edit of another tracked file and its untracked files stay.
func TestSyncKeepsTheGuestsChangesElsewhere(t *testing.T) {
	f := newSyncFixture(t)
	writeAt(t, f.local, "lib.go", "package lib\n")
	mustRun(t, f.local, "git", "add", "lib.go")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "laptop's commit")
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}

	writeAt(t, f.guestRepo(), "lib.go", "package lib // the agent's edit\n")
	writeAt(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	writeAt(t, f.local, "README.md", "laptop edit\n")
	writeAt(t, f.local, "notes.md", "laptop notes\n")
	writeAt(t, f.local, "more.go", "package lib // more\n")
	mustRun(t, f.local, "git", "add", "more.go")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "laptop's second commit")

	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("sync beside the agent's changes: %v", err)
	}
	if s.GuestKept != 2 || !strings.HasSuffix(s.String(), "; kept the machine's changes to 2 files") {
		t.Fatalf("summary %+v %q", s, s.String())
	}
	wantFile(t, f.guestRepo(), "lib.go", "package lib // the agent's edit\n")
	wantFile(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	wantFile(t, f.guestRepo(), "README.md", "laptop edit\n")
	wantFile(t, f.guestRepo(), "notes.md", "laptop notes\n")
	wantFile(t, f.guestRepo(), "more.go", "package lib // more\n")
	if got, want := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD = %s, want %s", got, want)
	}

	// The next sync sets the last sync's own changes aside and nothing of
	// the agent's: those files are neither stashed nor touched.
	writeAt(t, f.local, "README.md", "laptop edit, later\n")
	s, err = syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if !s.StashedLastSync || s.GuestKept != 2 {
		t.Fatalf("second summary %+v", s)
	}
	wantFile(t, f.guestRepo(), "README.md", "laptop edit, later\n")
	wantFile(t, f.guestRepo(), "lib.go", "package lib // the agent's edit\n")
	wantFile(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	stashed := mustRun(t, f.guestRepo(), "git", "stash", "show", "--include-untracked", "--name-only", "stash@{0}")
	if strings.Contains(stashed, "lib.go") || strings.Contains(stashed, "agent.txt") || !strings.Contains(stashed, "README.md") {
		t.Fatalf("the last-sync stash holds %q", stashed)
	}
}

// Overlap: the agent edited a file the laptop edits too. Refused, the
// message names that file and not the agent's other one, and nothing in
// the guest moves.
func TestSyncRefusesNamingTheOverlap(t *testing.T) {
	f := newSyncFixture(t)
	writeAt(t, f.guestRepo(), "README.md", "the agent's README\n")
	writeAt(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	writeAt(t, f.local, "README.md", "laptop edit\n")
	before := mustRun(t, f.guestRepo(), "git", "status", "--porcelain")

	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	msg := err.(*exitError).msg
	if !strings.HasPrefix(msg, "Not synced: the machine changed 1 file that your laptop changed too:\n  README.md\n") || strings.Contains(msg, "agent.txt") {
		t.Fatalf("message:\n%s", msg)
	}
	wantFile(t, f.guestRepo(), "README.md", "the agent's README\n")
	if after := mustRun(t, f.guestRepo(), "git", "status", "--porcelain"); after != before {
		t.Fatalf("guest status %q, before %q", after, before)
	}
}

// The owner's case (2026-10-07): only untracked files on the machine, an
// agent's scratch directory among them, and new commits on the laptop.
// It syncs and the untracked files stay.
func TestSyncWithOnlyUntrackedGuestFilesElsewhere(t *testing.T) {
	f := newSyncFixture(t)
	writeAt(t, f.guestRepo(), ".playwright-mcp/page.yml", "snapshot\n")
	writeAt(t, f.guestRepo(), "q1.yml", "q1\n")
	writeAt(t, f.local, "out/resume.tex", "laptop resume\n")
	mustRun(t, f.local, "git", "add", "out/resume.tex")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "laptop's commit")

	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.Commits != 1 || s.GuestKept != 2 {
		t.Fatalf("summary %+v", s)
	}
	wantFile(t, f.guestRepo(), ".playwright-mcp/page.yml", "snapshot\n")
	wantFile(t, f.guestRepo(), "q1.yml", "q1\n")
	wantFile(t, f.guestRepo(), "out/resume.tex", "laptop resume\n")
}

// An untracked file on the machine at a path the laptop also adds, as an
// untracked file or in a commit, is an overlap: the untracked tar or the
// checkout would write over it. A file inside an untracked directory the
// laptop adds counts the same.
func TestSyncRefusesAGuestUntrackedFileTheLaptopAlsoAdds(t *testing.T) {
	for name, laptop := range map[string]func(t *testing.T, f *syncFixture){
		"untracked": func(t *testing.T, f *syncFixture) { writeAt(t, f.local, "notes/todo.md", "laptop\n") },
		"committed": func(t *testing.T, f *syncFixture) {
			writeAt(t, f.local, "notes/todo.md", "laptop\n")
			mustRun(t, f.local, "git", "add", "notes/todo.md")
			mustRun(t, f.local, "git", "commit", "-q", "-m", "laptop's todo")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSyncFixture(t)
			writeAt(t, f.guestRepo(), "notes/todo.md", "the agent's\n")
			writeAt(t, f.guestRepo(), "notes/other.md", "the agent's other\n")
			laptop(t, f)
			_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
			wantDirtyRefusal(t, err)
			msg := err.(*exitError).msg
			if !strings.Contains(msg, "\n  notes/todo.md\n") || strings.Contains(msg, "other.md") {
				t.Fatalf("message:\n%s", msg)
			}
			wantFile(t, f.guestRepo(), "notes/todo.md", "the agent's\n")
		})
	}
}

// A rename the agent staged counts at both of its paths.
func TestSyncRefusesOverTheOldPathOfAGuestRename(t *testing.T) {
	f := newSyncFixture(t)
	mustRun(t, f.guestRepo(), "git", "mv", "README.md", "READ.md")
	writeAt(t, f.local, "README.md", "laptop edit\n")
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	if msg := err.(*exitError).msg; !strings.Contains(msg, "\n  README.md\n") {
		t.Fatalf("message:\n%s", msg)
	}
}

// The owner's whole case: the agent committed on the branch and left
// uncommitted files at other paths, and the laptop has new commits. The
// laptop's commits are merged into the agent's branch and the files stay.
func TestSyncMergesBesideTheGuestsUncommittedFiles(t *testing.T) {
	f := newSyncFixture(t)
	writeAt(t, f.guestRepo(), "agent.go", "package agent\n")
	mustRun(t, f.guestRepo(), "git", "add", "agent.go")
	mustRun(t, f.guestRepo(), "git", "commit", "-q", "-m", "agent's commit")
	writeAt(t, f.guestRepo(), "agent.go", "package agent // uncommitted\n")
	writeAt(t, f.guestRepo(), "q2.yml", "q2\n")
	writeAt(t, f.local, "laptop.go", "package laptop\n")
	mustRun(t, f.local, "git", "add", "laptop.go")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "laptop's commit")

	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !s.Merged || s.Detached || s.GuestKept != 2 {
		t.Fatalf("summary %+v", s)
	}
	if ref := mustRun(t, f.guestRepo(), "git", "symbolic-ref", "-q", "HEAD"); ref != "refs/heads/main" {
		t.Fatalf("guest HEAD = %q", ref)
	}
	wantFile(t, f.guestRepo(), "agent.go", "package agent // uncommitted\n")
	wantFile(t, f.guestRepo(), "q2.yml", "q2\n")
	wantFile(t, f.guestRepo(), "laptop.go", "package laptop\n")
}
