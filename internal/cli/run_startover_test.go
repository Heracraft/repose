package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose rm` then `repose run` in the checkout: the run waits for the
// destroy to end and creates a fresh project under the same name, instead
// of stopping at "is destroying" (I-301).
func TestRunStartsOverAfterADestroy(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{DestroyDelay: 250 * time.Millisecond})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	old := f.env.Cache.ByRemote[gitRemoteOrigin(f.local)].ProjectID
	if _, err := f.env.Client.DestroyProject(ctx, old); err != nil {
		t.Fatal(err)
	}
	var errOut strings.Builder
	env2 := &Env{
		Dir: f.env.Dir, Cfg: f.env.Cfg, Cache: f.env.Cache, Cwd: f.local, HomeDir: f.env.HomeDir,
		Client: f.env.Client, Out: &discardWriter{}, ErrOut: &errOut, TargetFor: f.env.TargetFor,
	}
	if err := runRun(ctx, env2, RunOptions{NoAttach: true}, false); err != nil {
		t.Fatalf("second runRun: %v (stderr %q)", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "Waiting for the old "+testSlug+" to finish destroying") {
		t.Fatalf("stderr %q", errOut.String())
	}
	projects, err := f.env.Client.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID == old || projects[0].Slug != testSlug || projects[0].State != "running" {
		t.Fatalf("projects = %+v", projects)
	}
	if got := env2.Cache.ByRemote[gitRemoteOrigin(f.local)].ProjectID; got != projects[0].ID {
		t.Fatalf("cache names %s, want the fresh %s", got, projects[0].ID)
	}
}

// A destroy that fails leaves the project in error; the run says so and
// creates nothing beside it.
func TestRunStopsWhenTheDestroyFails(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	old := f.env.Cache.ByRemote[gitRemoteOrigin(f.local)].ProjectID
	fake.SetState(old, "destroying")
	go func() {
		time.Sleep(150 * time.Millisecond)
		fake.SetState(old, "error")
	}()
	err := runRun(ctx, f.env, RunOptions{NoAttach: true}, false)
	if err == nil || !strings.Contains(err.Error(), "error state") {
		t.Fatalf("err = %v", err)
	}
	if projects, _ := f.env.Client.ListProjects(ctx); len(projects) != 1 {
		t.Fatalf("projects = %+v", projects)
	}
}
