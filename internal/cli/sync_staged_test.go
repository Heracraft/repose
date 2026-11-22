package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSyncKeepsStagedAndUnstaged (I-258): the guest's `git status` lists
// the same files as staged and unstaged as the laptop's, including a file
// with both a staged change and a further unstaged one, and every file's
// content matches.
func TestSyncKeepsStagedAndUnstaged(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.local, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.md", "first\n")
	write("config.ts", "export const a = 1\n")
	mustRun(t, f.local, "git", "add", "notes.md", "config.ts")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "two more files")

	write("README.md", "hello, staged\n")
	mustRun(t, f.local, "git", "add", "README.md")
	write("config.ts", "export const a = 2\n")
	mustRun(t, f.local, "git", "add", "config.ts")
	write("config.ts", "export const a = 3\n")
	write("notes.md", "first, unstaged\n")

	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"}); err != nil {
		t.Fatalf("syncGuest: %v", err)
	}

	want := mustRun(t, f.local, "git", "status", "--porcelain", "--untracked-files=no")
	if got := mustRun(t, f.guestRepo(), "git", "status", "--porcelain", "--untracked-files=no"); got != want {
		t.Fatalf("guest status:\n%s\nlaptop status:\n%s", got, want)
	}
	if got, want := mustRun(t, f.guestRepo(), "git", "diff", "--cached"), mustRun(t, f.local, "git", "diff", "--cached"); got != want {
		t.Fatalf("guest index differs from the laptop's:\n%s\nwant:\n%s", got, want)
	}
	for _, rel := range []string{"README.md", "config.ts", "notes.md"} {
		l, err := os.ReadFile(filepath.Join(f.local, rel))
		if err != nil {
			t.Fatal(err)
		}
		g, err := os.ReadFile(filepath.Join(f.guestRepo(), rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(g) != string(l) {
			t.Fatalf("%s on the guest = %q, want %q", rel, g, l)
		}
	}

	// A second run with nothing new leaves the guest as it is and is not
	// refused as dirty (the I-210 fingerprint ignores the index split).
	summary, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b"})
	if err != nil {
		t.Fatalf("second syncGuest: %v", err)
	}
	if !summary.Unchanged {
		t.Fatalf("second sync = %+v, want unchanged", summary)
	}
	if got := mustRun(t, f.guestRepo(), "git", "status", "--porcelain", "--untracked-files=no"); got != want {
		t.Fatalf("after the second run the guest status is:\n%s\nwant:\n%s", got, want)
	}
}
