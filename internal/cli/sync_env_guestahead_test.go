package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// I-753: a .env the laptop changed reaches a machine that has moved on.
// A .env is gitignored, so it is in neither the refs nor the untracked
// tar the sync key is built from: the laptop has nothing new, the
// guest's own commit sets GuestAhead, and that branch of the apply used
// to carry only the carry — the env payload travelled and was dropped,
// while sync.md promises the files are copied each run.
func TestSyncCopiesEnvFilesWhenTheGuestIsAhead(t *testing.T) {
	f := newSyncFixture(t)
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(f.local, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".env\n")
	mustRun(t, f.local, "git", "add", ".gitignore")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "ignore env")
	write(".env", "TOKEN=first\n")

	sync := func() *SyncSummary {
		t.Helper()
		envs, err := buildEnvCarry(f.local)
		if err != nil {
			t.Fatal(err)
		}
		s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{Env: envs})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := sync(); s.EnvFiles != 1 {
		t.Fatalf("first sync: %+v", s)
	}

	// The machine moves on: an agent commits there.
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "agent.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "add", "agent.go")
	mustRun(t, f.guestRepo(), "git", "commit", "-q", "-m", "the agent's work")

	// The laptop rotates the token and nothing else.
	write(".env", "TOKEN=second\n")
	s := sync()
	if !s.GuestAhead {
		t.Fatalf("want the guest ahead: %+v", s)
	}
	if b, err := os.ReadFile(filepath.Join(f.guestRepo(), ".env")); err != nil || string(b) != "TOKEN=second\n" {
		t.Fatalf("the rotated .env on the machine = %q (%v)", b, err)
	}
	if s.EnvFiles != 1 {
		t.Errorf("EnvFiles = %d, want 1", s.EnvFiles)
	}
	// The checkout itself is still the agent's (I-248).
	if out := mustRun(t, f.guestRepo(), "git", "log", "-1", "--format=%s"); !strings.Contains(out, "the agent's work") {
		t.Errorf("the machine's HEAD = %q", out)
	}
	if line := s.String(); !strings.HasPrefix(line, "Copied 1 env file. The machine has ") {
		t.Errorf("summary = %q", line)
	}
	// Nothing left to send: the next run copies nothing.
	if again := sync(); again.EnvFiles != 0 || !again.GuestAhead {
		t.Errorf("second run = %+v", again)
	}
}
