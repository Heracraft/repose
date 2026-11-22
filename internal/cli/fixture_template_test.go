package cli

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/heracraft/repose/internal/testguest"
	"golang.org/x/crypto/ssh"
)

// The repositories newSyncFixture hands every test are the same three
// every time: a bare origin with one commit, a laptop clone and a guest
// clone. Building them took a dozen git processes per fixture, and the
// package builds well over a hundred fixtures (ENG-115), so they are
// built once per test binary under fixtureRoot and copied in Go into each
// test's own directories. TestMain removes fixtureRoot.
var fixtureRoot struct {
	once sync.Once
	dir  string
	err  error
}

// fixtureDir is the per-binary scratch directory the cached fixtures live
// in, made on first use.
func fixtureDir() (string, error) {
	fixtureRoot.once.Do(func() {
		fixtureRoot.dir, fixtureRoot.err = os.MkdirTemp("", "repose-cli-fixtures-")
	})
	return fixtureRoot.dir, fixtureRoot.err
}

// removeFixtureDir is TestMain's cleanup.
func removeFixtureDir() {
	if fixtureRoot.dir != "" {
		_ = os.RemoveAll(fixtureRoot.dir)
	}
}

// syncTemplate is the bare origin and the two clones, built once.
var syncTemplate struct {
	once                   sync.Once
	bare, local, guestRepo string
	err                    error
}

func gitIn(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return nil
}

func buildSyncTemplate() error {
	root, err := fixtureDir()
	if err != nil {
		return err
	}
	base, err := os.MkdirTemp(root, "sync-")
	if err != nil {
		return err
	}
	bare := filepath.Join(base, "bare")
	seed := filepath.Join(base, "seed")
	local := filepath.Join(base, "local")
	guestHome := filepath.Join(base, "guest")
	for _, d := range []string{bare, seed, local, guestHome} {
		if err := os.Mkdir(d, 0o755); err != nil {
			return err
		}
	}
	guestRepo := filepath.Join(guestHome, testSlug)
	steps := []struct {
		dir  string
		args []string
	}{
		{bare, []string{"init", "--bare", "-q", "-b", "main", "."}},
		{seed, []string{"clone", "-q", bare, "."}},
		{seed, []string{"config", "user.email", "seed@example.com"}},
		{seed, []string{"config", "user.name", "Seed"}},
	}
	for _, s := range steps {
		if err := gitIn(s.dir, s.args...); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		return err
	}
	steps = []struct {
		dir  string
		args []string
	}{
		{seed, []string{"add", "README.md"}},
		{seed, []string{"commit", "-q", "-m", "initial"}},
		{seed, []string{"push", "-q", "origin", "main"}},
		{local, []string{"clone", "-q", bare, "."}},
		{local, []string{"config", "user.email", "dev@example.com"}},
		{local, []string{"config", "user.name", "Dev Laptop"}},
		{guestHome, []string{"clone", "-q", bare, testSlug}},
		{guestRepo, []string{"config", "user.email", "guest@example.com"}},
		{guestRepo, []string{"config", "user.name", "Guest"}},
	}
	for _, s := range steps {
		if err := gitIn(s.dir, s.args...); err != nil {
			return err
		}
	}
	syncTemplate.bare, syncTemplate.local, syncTemplate.guestRepo = bare, local, guestRepo
	return nil
}

// copySyncTemplate lays the cached origin and clones into bare, local and
// guestRepo (which must not exist yet, or be empty directories), and
// points each clone's origin at the copied bare repository, as a fresh
// `git clone bare` would have.
func copySyncTemplate(t *testing.T, bare, local, guestRepo string) {
	t.Helper()
	syncTemplate.once.Do(func() { syncTemplate.err = buildSyncTemplate() })
	if syncTemplate.err != nil {
		t.Fatal(syncTemplate.err)
	}
	for _, c := range []struct{ from, to string }{
		{syncTemplate.bare, bare},
		{syncTemplate.local, local},
		{syncTemplate.guestRepo, guestRepo},
	} {
		if err := copyTree(c.from, c.to); err != nil {
			t.Fatal(err)
		}
	}
	for _, repo := range []string{local, guestRepo} {
		cfg := filepath.Join(repo, ".git", "config")
		b, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		old := "url = " + syncTemplate.bare + "\n"
		if !bytes.Contains(b, []byte(old)) {
			t.Fatalf("%s does not name the template origin %s:\n%s", cfg, syncTemplate.bare, b)
		}
		b = bytes.Replace(b, []byte(old), []byte("url = "+bare+"\n"), 1)
		if err := os.WriteFile(cfg, b, 0o644); err != nil {
			t.Fatal(err)
		}
		// A copied file has a new inode and ctime, so the index's stat
		// data no longer matches; refresh it, as the clone left it.
		mustRun(t, repo, "git", "update-index", "-q", "--refresh")
	}
}

// copyTree copies the directory tree at from to to, keeping modes and
// modification times and recreating symlinks.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(dst, info.Mode().Perm())
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		case info.Mode().IsRegular():
			if err := copyFile(p, dst, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(dst, info.ModTime(), info.ModTime())
		default:
			return fmt.Errorf("copyTree: %s is neither a file, a directory nor a symlink", p)
		}
	})
}

func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// reposeBinary is `go build ../../cmd/repose`, built once per test binary
// for the tests that run the CLI as a separate process.
var reposeBinary struct {
	once sync.Once
	path string
	err  error
}

func builtRepose(t *testing.T) string {
	t.Helper()
	reposeBinary.once.Do(func() {
		root, err := fixtureDir()
		if err != nil {
			reposeBinary.err = err
			return
		}
		bin := filepath.Join(root, "repose")
		build := exec.Command("go", "build", "-o", bin, "../../cmd/repose")
		build.Env = append(filterTestEnv(os.Environ(), "HOME", "XDG_CONFIG_HOME"), "HOME="+realHome, "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			reposeBinary.err = fmt.Errorf("go build: %v\n%s", err, out)
			return
		}
		reposeBinary.path = bin
	})
	if reposeBinary.err != nil {
		t.Fatal(reposeBinary.err)
	}
	return reposeBinary.path
}

var clientKeyTemplate struct {
	once     sync.Once
	privPath string
	pub      ssh.PublicKey
	err      error
}

func cachedClientKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	clientKeyTemplate.once.Do(func() {
		root, err := fixtureDir()
		if err != nil {
			clientKeyTemplate.err = err
			return
		}
		keyDir, err := os.MkdirTemp(root, "key-")
		if err != nil {
			clientKeyTemplate.err = err
			return
		}
		clientKeyTemplate.privPath, clientKeyTemplate.pub, clientKeyTemplate.err = testguest.GenerateClientKey(keyDir)
	})
	if clientKeyTemplate.err != nil {
		t.Fatal(clientKeyTemplate.err)
	}
	return clientKeyTemplate.privPath, clientKeyTemplate.pub
}

var subRepoTemplate struct {
	once sync.Once
	bare string
	err  error
}

func buildSubRepoTemplate() error {
	root, err := fixtureDir()
	if err != nil {
		return err
	}
	base, err := os.MkdirTemp(root, "subrepo-")
	if err != nil {
		return err
	}
	bare := filepath.Join(base, "bare")
	seed := filepath.Join(base, "seed")
	for _, d := range []string{bare, seed} {
		if err := os.Mkdir(d, 0o755); err != nil {
			return err
		}
	}
	if err := gitIn(bare, "init", "--bare", "-q", "-b", "main", "."); err != nil {
		return err
	}
	if err := gitIn(seed, "clone", "-q", bare, "."); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(seed, "s"), []byte("sub v1\n"), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"add", "s"},
		{"-c", "user.email=s@x", "-c", "user.name=s", "commit", "-q", "-m", "sub"},
		{"push", "-q", "origin", "main"},
	} {
		if err := gitIn(seed, args...); err != nil {
			return err
		}
	}
	subRepoTemplate.bare = bare
	return nil
}

func copySubRepoTemplate(t *testing.T, bare string) {
	t.Helper()
	subRepoTemplate.once.Do(func() { subRepoTemplate.err = buildSubRepoTemplate() })
	if subRepoTemplate.err != nil {
		t.Fatal(subRepoTemplate.err)
	}
	if err := copyTree(subRepoTemplate.bare, bare); err != nil {
		t.Fatal(err)
	}
}
