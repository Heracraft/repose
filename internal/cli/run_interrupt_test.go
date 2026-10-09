package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// DECISIONS I-575: Ctrl-C after `repose sync` created a project and
// before anything used it leaves no directory link behind, and says the
// project exists.
func TestSyncInterruptedAfterCreateForgetsTheProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{CreateDelay: 30 * time.Second})
	defer fake.Close()
	f := newRunFixture(t, fake)
	mustRun(t, f.local, "git", "remote", "remove", "origin") // named after the directory (I-358)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(time.Second, cancel)

	err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitInterrupted {
		t.Fatalf("err = %v, want exit %d", err, ExitInterrupted)
	}
	ps, lerr := f.env.Client.ListProjects(context.Background())
	if lerr != nil || len(ps) != 1 {
		t.Fatalf("projects = %+v, %v", ps, lerr)
	}
	slug := ps[0].Slug
	want := "Interrupted. " + slug + " was created and stays on your account; `repose rm " + slug + "` removes it."
	if ee.msg != want {
		t.Fatalf("message = %q\nwant      %q", ee.msg, want)
	}
	if len(f.env.Cache.ByDir) != 0 {
		t.Fatalf("by_dir in memory = %v", f.env.Cache.ByDir)
	}
	disk, derr := loadProjectsCache(f.env.Dir)
	if derr != nil {
		t.Fatal(derr)
	}
	if len(disk.ByDir) != 0 {
		t.Fatalf("by_dir on disk = %v", disk.ByDir)
	}
}

// `repose sync PROJECT` in a directory with no remote and no project of
// its own links the directory to PROJECT, so the next plain `repose sync`
// there lands on it instead of creating one named after the directory.
func TestSyncWithAProjectLinksAnUnlinkedDirectory(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	mustRun(t, f.local, "git", "remote", "remove", "origin")
	ctx := context.Background()
	job, err := f.env.Client.CreateProject(ctx, CreateProjectRequest{Name: "job", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}

	if err := runRun(ctx, f.env, RunOptions{ProjectArg: "job", NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("repose sync --project job: %v", err)
	}
	key := dirKey(f.local, defaultResolveDeps())
	if got := f.env.Cache.ByDir[key]; got != job.ID {
		t.Fatalf("by_dir[%s] = %q, want %s", key, got, job.ID)
	}
	if disk, err := loadProjectsCache(f.env.Dir); err != nil || disk.ByDir[key] != job.ID {
		t.Fatalf("by_dir on disk = %v, %v", disk.ByDir, err)
	}

	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("plain repose sync: %v", err)
	}
	if ps, err := f.env.Client.ListProjects(ctx); err != nil || len(ps) != 1 {
		t.Fatalf("the plain sync made another project: %+v %v", ps, err)
	}
}

// A directory with a git remote is found by it; an explicit project is
// not written for it (I-152).
func TestSyncWithAProjectLeavesACheckoutWithARemoteUnlinked(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if _, err := f.env.Client.CreateProject(ctx, CreateProjectRequest{Name: "other", Class: "small"}); err != nil {
		t.Fatal(err)
	}
	if err := runRun(ctx, f.env, RunOptions{ProjectArg: "other", NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("repose sync --project other: %v", err)
	}
	if len(f.env.Cache.ByDir) != 0 {
		t.Fatalf("by_dir = %v", f.env.Cache.ByDir)
	}
}

// Ctrl-C while the create request itself is out (I-575): the api made
// the project, so a short lookup names it; nothing is cached.
func TestSyncInterruptedDuringTheCreateRequestNamesTheProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{CreateReplyDelay: 30 * time.Second})
	defer fake.Close()
	f := newRunFixture(t, fake)
	mustRun(t, f.local, "git", "remote", "remove", "origin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(time.Second, cancel)

	err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitInterrupted {
		t.Fatalf("err = %v, want exit %d", err, ExitInterrupted)
	}
	ps, lerr := f.env.Client.ListProjects(context.Background())
	if lerr != nil || len(ps) != 1 {
		t.Fatalf("projects = %+v, %v", ps, lerr)
	}
	slug := ps[0].Slug
	if want := "Interrupted. " + slug + " was created and stays on your account; `repose rm " + slug + "` removes it."; ee.msg != want {
		t.Fatalf("message = %q\nwant      %q", ee.msg, want)
	}
	if disk, derr := loadProjectsCache(f.env.Dir); derr != nil || len(disk.ByDir) != 0 || len(f.env.Cache.ByDir) != 0 {
		t.Fatalf("by_dir: disk %v (%v), memory %v", disk.ByDir, derr, f.env.Cache.ByDir)
	}
}

// Ctrl-C before the api made anything: the lookup cannot tell, and the
// line says the project may exist.
func TestSyncInterruptedBeforeTheCreateLandedSaysItMayExist(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{CreateHold: 30 * time.Second})
	defer fake.Close()
	f := newRunFixture(t, fake)
	mustRun(t, f.local, "git", "remote", "remove", "origin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(time.Second, cancel)

	err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitInterrupted {
		t.Fatalf("err = %v, want exit %d", err, ExitInterrupted)
	}
	name := dirProjectName(filepath.Base(syncRoot(f.local)))
	if want := "Interrupted. " + name + " may have been created; `repose ls` shows it."; ee.msg != want {
		t.Fatalf("message = %q\nwant      %q", ee.msg, want)
	}
	if ps, err := f.env.Client.ListProjects(context.Background()); err != nil || len(ps) != 0 {
		t.Fatalf("projects = %+v, %v", ps, err)
	}
}
