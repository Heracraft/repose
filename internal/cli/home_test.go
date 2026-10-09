package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The home directory and every folder above it are the home folder; a
// folder inside it is an ordinary one (I-601).
func TestIsHomeFolder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "users", "ada")
	sub := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	for path, want := range map[string]bool{
		home:                                 true,
		home + "/":                           true,
		filepath.Join(root, "users"):         true,
		root:                                 true,
		"/":                                  true,
		sub:                                  false,
		filepath.Join(root, "usersx"):        false,
		filepath.Join(root, "users", "adam"): false,
		"":                                   false,
	} {
		if got := isHomeFolder(path); got != want {
			t.Errorf("isHomeFolder(%q) = %v, want %v", path, got, want)
		}
	}
	// Through a symlink to it, too.
	link := filepath.Join(root, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	if !isHomeFolder(link) {
		t.Error("a symlink to the home directory is not the home folder")
	}
}

// homeFixture is a run fixture whose cwd is the home directory.
func homeFixture(t *testing.T) (*runFixture, string) {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	home := filepath.Join(t.TempDir(), "ada")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return f, home
}

func wantExit(t *testing.T, what string, err error, code int, msg string) {
	t.Helper()
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != code || !strings.Contains(ee.msg, msg) {
		t.Fatalf("%s: err = %v, want exit %d with %q", what, err, code, msg)
	}
}

// A plain `repose run` in the home folder creates nothing and says how
// to get a machine; --name makes as many as you name, none linked to the
// folder, and a prompt goes with them (I-601).
func TestRunInTheHomeFolder(t *testing.T) {
	f, home := homeFixture(t)
	ctx := context.Background()

	e, _, _ := freshEnv(f.env, home)
	err := runRun(ctx, e, RunOptions{NoAttach: true}, false)
	wantExit(t, "plain run", err, ExitUsage, "Your home folder is not a project. cd into one, or run `repose run NAME` or `repose run --temp`.")
	err = runRun(ctx, e, RunOptions{NoAttach: true, Prompt: "fix the tests"}, false)
	wantExit(t, "plain run with a prompt", err, ExitUsage, "Your home folder is not a project.")
	if ps := listed(t, e); len(ps) != 0 {
		t.Fatalf("created %+v", ps)
	}

	for _, name := range []string{"scratch", "notes"} {
		e, out, _ := freshEnv(f.env, home)
		if err := runRun(ctx, e, RunOptions{Name: name, NoAttach: true}, false); err != nil {
			t.Fatalf("run --name %s: %v", name, err)
		}
		if strings.Contains(out.buf.String(), "synced") {
			t.Fatalf("stdout %q", out.buf.String())
		}
	}
	ps := listed(t, e)
	if len(ps) != 2 || bySlug(ps, "scratch") == nil || bySlug(ps, "notes") == nil {
		t.Fatalf("projects %+v", ps)
	}
	disk, err := loadProjectsCache(f.env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.ByDir) != 0 {
		t.Fatalf("by_dir %v, want nothing for the home folder", disk.ByDir)
	}
	// The same name again is the same machine.
	e2, _, _ := freshEnv(f.env, home)
	if err := runRun(ctx, e2, RunOptions{Name: "scratch", NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if ps := listed(t, e2); len(ps) != 2 {
		t.Fatalf("second run --name scratch made another: %+v", ps)
	}

	// --temp works there; --on and sync do not.
	e3, _, _ := freshEnv(f.env, home)
	if err := runRun(ctx, e3, RunOptions{Temp: tempDefault, NoAttach: true}, false); err != nil {
		t.Fatalf("run --temp: %v", err)
	}
	e4, _, _ := freshEnv(f.env, home)
	err = runRun(ctx, e4, RunOptions{On: "scratch", NoAttach: true}, false)
	wantExit(t, "--on", err, ExitUsage, "Your home folder is not a checkout")
	for _, opts := range []RunOptions{{Sync: true, NoAttach: true}, {Sync: true, NoAttach: true, ProjectArg: "scratch"}, {Sync: true, NoAttach: true, Name: "fresh"}} {
		e5, _, _ := freshEnv(f.env, home)
		err = runRun(ctx, e5, opts, false)
		wantExit(t, "sync", err, ExitUsage, "Your home folder is never synced. cd into a checkout.")
	}
	if bySlug(listed(t, e), "fresh") != nil {
		t.Fatal("sync --name fresh created it before refusing")
	}
}

// A dotfiles repository in the home folder is never synced, never gets
// the `repose` remote, and its remote finds no project, from a
// subfolder of it too (I-601).
func TestRunInAHomeFolderThatIsARepository(t *testing.T) {
	f, home := homeFixture(t)
	ctx := context.Background()
	mustRun(t, filepath.Dir(home), "git", "clone", "-q", "file://"+f.bare, filepath.Base(home))
	sub := filepath.Join(home, ".config")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// The repository's own project, made from elsewhere.
	remote := gitRemoteOrigin(home)
	if _, err := f.env.Client.CreateProject(ctx, CreateProjectRequest{Name: "dotfiles", RemoteURL: remote, Class: "large"}); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{home, sub} {
		e, _, _ := freshEnv(f.env, cwd)
		err := runRun(ctx, e, RunOptions{NoAttach: true}, false)
		wantExit(t, "plain run in "+cwd, err, ExitUsage, "Your home folder is not a project.")
		_, err = requireProject(ctx, e, "")
		wantExit(t, "status in "+cwd, err, ExitProjectNotFound, "Your home folder is not a project. Name one:")
	}
	e, _, _ := freshEnv(f.env, home)
	if err := runRun(ctx, e, RunOptions{Name: "loose", NoAttach: true}, false); err != nil {
		t.Fatalf("run --name loose: %v", err)
	}
	p := bySlug(listed(t, e), "loose")
	if p == nil || p.RemoteURL != "" {
		t.Fatalf("loose = %+v, want no remote", p)
	}
	if got := remoteURLOf(home, reposeRemoteName); got != "" {
		t.Fatalf("the home repository got a repose remote: %q", got)
	}
}

// A link an older CLI wrote for the home folder is ignored and dropped,
// and a command that needs a project asks for its name there (I-601).
func TestHomeFolderForgetsAnOldLink(t *testing.T) {
	f, home := homeFixture(t)
	ctx := context.Background()
	p, err := f.env.Client.CreateProject(ctx, CreateProjectRequest{Name: "obsidian", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	f.env.Cache.ByDir[home] = p.ID
	if err := saveProjectsCache(f.env.Dir, f.env.Cache); err != nil {
		t.Fatal(err)
	}
	e, _, _ := freshEnv(f.env, home)
	e.Command = "repose rm"
	err = DestroyCmd(ctx, e, "", true, false, nil)
	wantExit(t, "rm", err, ExitProjectNotFound, "Your home folder is not a project. Name one: `repose rm PROJECT`")
	if got, _ := e.Client.GetProject(ctx, p.ID); got == nil || got.State == "destroying" {
		t.Fatalf("obsidian = %+v", got)
	}
	disk, err := loadProjectsCache(f.env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := disk.ByDir[home]; ok {
		t.Fatalf("by_dir still links the home folder: %v", disk.ByDir)
	}
	// Named, it is found.
	if got, err := requireProject(ctx, e, "obsidian"); err != nil || got.ID != p.ID {
		t.Fatalf("named: %+v %v", got, err)
	}
}

// `repose rm` forgets every folder linked to the machine, so a plain run
// there makes a new one instead of landing on it while it is destroyed.
func TestDestroyForgetsLinkedFolders(t *testing.T) {
	f, _ := homeFixture(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "job search")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e, _, _ := freshEnv(f.env, dir)
	if err := runRun(ctx, e, RunOptions{NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	p := bySlug(listed(t, e), "job-search")
	if p == nil {
		t.Fatal("no job-search")
	}
	other := t.TempDir()
	disk, _ := loadProjectsCache(f.env.Dir)
	disk.Checkouts[other] = CachedCheckout{ProjectID: p.ID, Name: "other"}
	if err := saveProjectsCache(f.env.Dir, disk); err != nil {
		t.Fatal(err)
	}
	e2, _, _ := freshEnv(f.env, dir)
	if err := DestroyCmd(ctx, e2, "", true, false, nil); err != nil {
		t.Fatalf("rm: %v", err)
	}
	disk, err := loadProjectsCache(f.env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for k, id := range disk.ByDir {
		if id == p.ID {
			t.Fatalf("by_dir[%s] still names the destroyed project", k)
		}
	}
	if _, ok := disk.Checkouts[other]; ok {
		t.Fatal("checkouts still names the destroyed project")
	}
}

// `repose sync` outside a repository refuses before it creates anything,
// as I-358 says; `run` there still makes an empty machine.
func TestSyncOutsideARepositoryRefuses(t *testing.T) {
	f, _ := homeFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	e, _, _ := freshEnv(f.env, dir)
	err := runRun(ctx, e, RunOptions{Sync: true, NoAttach: true, Name: "plain"}, false)
	wantExit(t, "sync", err, ExitUsage, "This folder is not a git checkout, so there is nothing to sync.")
	if ps := listed(t, e); len(ps) != 0 {
		t.Fatalf("created %+v", ps)
	}
}
