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

// run lists the .env files while the probe is in flight: the sync asks
// for them once, only after the probe, and writes what it gets.
func TestSyncTakesEnvFilesAfterTheProbe(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.local, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "add", ".gitignore")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "ignore env")
	if err := os.WriteFile(filepath.Join(f.local, ".env"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{
		BeforeApply: func(map[string]string) error {
			if calls != 0 {
				t.Error("EnvLater was called before the probe finished")
			}
			return nil
		},
		EnvLater: func() []envFile {
			calls++
			envs, err := buildEnvCarry(f.local)
			if err != nil {
				t.Fatal(err)
			}
			return envs
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || s.EnvFiles != 1 {
		t.Fatalf("EnvLater calls = %d, env files = %d; want 1 and 1", calls, s.EnvFiles)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), ".env")); string(b) != "A=1\n" {
		t.Errorf("guest .env = %q", b)
	}
}

// A guest carried by the previous release has a paths-only env-paths
// file: the probe reads each line as a path, says nothing, and rewrites
// it with mtimes.
func TestEnvPathsCheckReadsTheOldShape(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	for _, rel := range []string{".env", "apps/my app/.env"} {
		p := filepath.Join(repo, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("A=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(home, ".repose"), 0o700)
	if err := os.WriteFile(filepath.Join(home, ".repose", "env-paths"), []byte(".env\napps/my app/.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-e", "-c", envPathsCheck)
	cmd.Dir = repo
	cmd.Env = append(filterTestEnv(os.Environ(), "HOME"), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("check: %v %q", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".repose", "env-paths"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[1], " apps/my app/.env") || strings.HasPrefix(lines[0], ".") {
		t.Errorf("rewritten env-paths = %q", b)
	}
}

// I-197 against the local sshd harness: gitignored .env files at any
// depth travel with mode 0600, dependency directories and oversize files
// do not, a guest copy newer than the laptop's is kept and named, and an
// unchanged set is not sent again.
func TestSyncCarriesEnvFiles(t *testing.T) {
	f := newSyncFixture(t)
	write := func(rel, body string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(f.local, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".env\n.env.*\n!.env.example\n.envrc\nnode_modules/\n", 0o644)
	write(".env.example", "API_URL=\n", 0o644)
	mustRun(t, f.local, "git", "add", ".gitignore", ".env.example")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "ignore env")
	write(".env", "API_URL=https://laptop\n", 0o644)
	write(".env.local", "SECRET=1\n", 0o644)
	write("apps/web/.env", "WEB=1\n", 0o644)
	write("node_modules/pkg/.env", "DEP=1\n", 0o644)
	write("apps/big/.env", strings.Repeat("x", envFileCap+1), 0o644)
	write("apps/web/.envrc", "not an env file by name\n", 0o644)

	sync := func() *SyncSummary {
		t.Helper()
		envs, err := buildEnvCarry(f.local)
		if err != nil {
			t.Fatal(err)
		}
		s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b", Env: envs})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := sync()
	if s.EnvFiles != 3 || !strings.Contains(s.String(), ", 3 env files") {
		t.Fatalf("summary = %q (%+v)", s.String(), s)
	}
	for rel, want := range map[string]string{".env": "API_URL=https://laptop\n", ".env.local": "SECRET=1\n", "apps/web/.env": "WEB=1\n"} {
		p := filepath.Join(f.guestRepo(), rel)
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Errorf("%s = %q %v", rel, b, err)
			continue
		}
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", rel, info.Mode().Perm())
		}
	}
	for _, rel := range []string{"node_modules/pkg/.env", "apps/big/.env", "apps/web/.envrc"} {
		if fileExists(filepath.Join(f.guestRepo(), rel)) {
			t.Errorf("%s travelled", rel)
		}
	}
	ls, _ := exec.Command("ls", "-l", filepath.Join(f.guestRepo(), "apps", "web", ".env"), filepath.Join(f.guestRepo(), ".env")).CombinedOutput()
	t.Logf("guest ls -l:\n%s", ls)

	// Unchanged: nothing sent, no count.
	if s := sync(); s.EnvFiles != 0 || len(s.EnvKept) != 0 {
		t.Fatalf("unchanged sync = %+v", s)
	}

	// The guest edits apps/web/.env after the laptop's copy; the laptop
	// then changes .env. The guest's newer file is kept and named once.
	guestWeb := filepath.Join(f.guestRepo(), "apps", "web", ".env")
	if err := os.WriteFile(guestWeb, []byte("WEB=guest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(guestWeb, future, future)
	write(".env", "API_URL=https://laptop2\n", 0o644)
	s = sync()
	if len(s.EnvKept) != 1 || s.EnvKept[0] != "apps/web/.env" {
		t.Fatalf("kept = %v", s.EnvKept)
	}
	if w := strings.Join(s.Warnings(), "\n"); !strings.Contains(w, "Kept the machine's apps/web/.env") {
		t.Errorf("warnings = %q", w)
	}
	if b, _ := os.ReadFile(guestWeb); string(b) != "WEB=guest\n" {
		t.Errorf("guest's newer file overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), ".env")); string(b) != "API_URL=https://laptop2\n" {
		t.Errorf("laptop's newer .env did not arrive: %q", b)
	}
	if s := sync(); len(s.EnvKept) != 0 {
		t.Errorf("kept named again on an unchanged run: %v", s.EnvKept)
	}

	// Deleted in the guest while the laptop is unchanged: the marker
	// alone must not stop it coming back.
	if err := os.Remove(filepath.Join(f.guestRepo(), ".env.local")); err != nil {
		t.Fatal(err)
	}
	if s := sync(); s.EnvFiles == 0 {
		t.Errorf("after a guest deletion, no env file was written")
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), ".env.local")); string(b) != "SECRET=1\n" {
		t.Errorf("deleted .env.local not restored: %q", b)
	}

	// Edited in the guest while the laptop's copy did not change: kept,
	// and named once (the §6 row "one line"), not on every run after.
	guestLocal := filepath.Join(f.guestRepo(), ".env.local")
	if err := os.WriteFile(guestLocal, []byte("SECRET=guest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Hour)
	_ = os.Chtimes(guestLocal, later, later)
	s = sync()
	if len(s.EnvKept) != 1 || s.EnvKept[0] != ".env.local" || s.EnvFiles != 0 {
		t.Fatalf("a guest edit with the laptop unchanged: kept %v, written %d; want .env.local named, nothing written", s.EnvKept, s.EnvFiles)
	}
	if b, _ := os.ReadFile(guestLocal); string(b) != "SECRET=guest\n" {
		t.Errorf("the guest's edit was overwritten: %q", b)
	}
	if s := sync(); len(s.EnvKept) != 0 {
		t.Errorf("named again on the next unchanged run: %v", s.EnvKept)
	}
}
