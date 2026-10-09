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

// newSecondRepo is another repository on the laptop, with one commit of
// its own file, the folder `repose run --on` adds to a machine (I-480).
func newSecondRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "init", "-q", "-b", "main")
	mustRun(t, dir, "git", "config", "user.email", "dev@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Dev Laptop")
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-q", "-m", "initial "+name)
	return dir
}

// The name a joining folder gets skips the checkout, its worktrees, the
// names other folders have and non-empty directories (I-480).
func TestClaimCheckoutNames(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(f.guestHome, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.guestHome, "web", "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.guestHome, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.guestHome, testSlug+"-worktree-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ want, got string }{
		{testSlug, testSlug + "-2"}, // the checkout itself (~/<slug>, a machine from before I-368)
		{"api", "api"},
		{"api", "api-2"}, // another folder called api
		{"web", "web-2"}, // not empty, not ours
		{"empty", "empty"},
		{testSlug + "-worktree-1", testSlug + "-worktree-1-2"},
	} {
		got, err := claimCheckout(ctx, f.target, testSlug, tc.want)
		if err != nil {
			t.Fatalf("claim %s: %v", tc.want, err)
		}
		if got != tc.got {
			t.Fatalf("claim %s: got %s, want %s", tc.want, got, tc.got)
		}
		if fi, err := os.Stat(filepath.Join(f.guestHome, got)); err != nil || !fi.IsDir() {
			t.Fatalf("claim %s: ~/%s not made (%v)", tc.want, got, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(f.guestHome, ".repose", "checkouts"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{testSlug + "-2", "api", "api-2", "web-2", "empty", testSlug + "-worktree-1-2"}, "\n") + "\n"
	if string(b) != want {
		t.Fatalf("checkouts file:\n%s\nwant:\n%s", b, want)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, ".repose", "checkout")); err == nil {
		t.Fatal("claiming another checkout wrote ~/.repose/checkout")
	}
}

// `repose run --on proj` from a second repository syncs it into ~/api
// beside proj's checkout, remembers the folder, and the next plain run
// there goes back to it; exec and new agent windows work in it (I-480).
func TestRunOnAddsAnotherCheckout(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()

	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	primaryHead := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD")

	api := newSecondRepo(t, "api")
	f.env.Cwd = api
	out := &discardWriter{}
	f.env.Out = out
	if err := runRun(ctx, f.env, RunOptions{On: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("run --on: %v\n%s", err, out.buf.String())
	}
	if !strings.Contains(out.buf.String(), "Added api to "+testSlug+" as ~/api.") {
		t.Fatalf("run --on said:\n%s", out.buf.String())
	}
	if b, err := os.ReadFile(filepath.Join(f.guestHome, "api", "api.txt")); err != nil || string(b) != "api\n" {
		t.Fatalf("~/api/api.txt: %q, %v", b, err)
	}
	if got := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"); got != primaryHead {
		t.Fatalf("the machine's checkout moved: %s, was %s", got, primaryHead)
	}
	if _, err := os.Stat(filepath.Join(f.guestRepo(), "api.txt")); err == nil {
		t.Fatal("api's file landed in the machine's checkout")
	}
	co := f.env.Cache.Checkouts[api]
	if co.Name != "api" || co.ProjectID == "" {
		t.Fatalf("cache entry: %+v", co)
	}
	if _, ok := f.env.Cache.ByDir[api]; ok {
		t.Fatal("the folder went into by_dir, where a CLI from before I-480 would sync it over the checkout")
	}

	// A plain run in the folder again: the same machine, the same
	// checkout, no new project, and new work reaches ~/api with `sync`.
	if err := os.WriteFile(filepath.Join(api, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, "api", "new.txt")); err != nil {
		t.Fatalf("sync did not reach ~/api: %v", err)
	}
	// By the machine's name (I-603) the folder is still its other
	// checkout: the work reaches ~/api, never the machine's own.
	if err := os.WriteFile(filepath.Join(api, "named.txt"), []byte("named\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("sync %s from the folder: %v", testSlug, err)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, "api", "named.txt")); err != nil {
		t.Fatalf("sync %s did not reach ~/api: %v", testSlug, err)
	}
	if _, err := os.Stat(filepath.Join(f.guestRepo(), "named.txt")); err == nil {
		t.Fatalf("sync %s from the folder landed in the machine's checkout", testSlug)
	}
	projects, err := f.env.Client.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("%d projects, want 1", len(projects))
	}

	res, err := requireProjectRes(ctx, f.env, "")
	if err != nil || res.Checkout != "api" {
		t.Fatalf("resolve in the folder: %+v, %v", res, err)
	}
	res, err = requireProjectRes(ctx, f.env, testSlug+":api")
	if err != nil || res.Checkout != "api" || res.Project.Slug != testSlug {
		t.Fatalf("resolve %s:api: %+v, %v", testSlug, res, err)
	}

	tgt := f.target
	tgt.Checkout = "api"
	pwd, err := runSSH(ctx, tgt, execScript(testSlug, "api", []string{"pwd"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(pwd)); filepath.Base(got) != "api" {
		t.Fatalf("exec ran in %s", got)
	}

	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n claude 'exec cat'", nil); err != nil {
		t.Fatal(err)
	}
	name, others, err := windowNameFor(ctx, tgt, testSlug, "claude")
	if err != nil || name != "api/claude" || others {
		t.Fatalf("api's first claude window: %q others=%v %v", name, others, err)
	}
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n api/claude 'exec cat'", nil); err != nil {
		t.Fatal(err)
	}
	name, others, err = windowNameFor(ctx, tgt, testSlug, "claude")
	if err != nil || name != "api/claude-2" || !others {
		t.Fatalf("api's second claude window: %q others=%v %v", name, others, err)
	}
	name, _, err = windowNameFor(ctx, f.target, testSlug, "claude")
	if err != nil || name != "claude-2" {
		t.Fatalf("the checkout's next claude window: %q %v", name, err)
	}
}

func TestRunOnRefusals(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	for _, tc := range []struct {
		name string
		opts RunOptions
		want string
		code int
	}{
		{"own checkout", RunOptions{On: testSlug, NoAttach: true}, "own checkout already", ExitUsage},
		{"no such project", RunOptions{On: "nope", NoAttach: true}, "No repose project is called nope", ExitProjectNotFound},
		{"with --temp", RunOptions{On: testSlug, Temp: 3600e9, NoAttach: true}, "cannot be used with", ExitUsage},
		{"with --name", RunOptions{On: testSlug, Name: "x", NoAttach: true}, "cannot be used with", ExitUsage},
	} {
		err := runRun(ctx, f.env, tc.opts, false)
		var xe *exitError
		if !errors.As(err, &xe) || xe.code != tc.code || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

func TestWindowLabel(t *testing.T) {
	for _, tc := range []struct{ extra, agent, want string }{
		{"", "claude", "claude"},
		{"api", "claude", "api/claude"},
		{"my.app", "codex", "my-app/codex"},
	} {
		if got := windowLabel(tc.extra, tc.agent); got != tc.want {
			t.Fatalf("windowLabel(%q, %q) = %q, want %q", tc.extra, tc.agent, got, tc.want)
		}
	}
}

// The cache keeps a folder's checkout across a save and load, and two
// processes' changes merge (I-480).
func TestProjectsCacheCheckoutsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := loadProjectsCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Checkouts["/laptop/api"] = CachedCheckout{ProjectID: "p1", Name: "api"}
	if err := saveProjectsCache(dir, c); err != nil {
		t.Fatal(err)
	}
	other, err := loadProjectsCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	other.Checkouts["/laptop/web"] = CachedCheckout{ProjectID: "p1", Name: "web"}
	other.ByDir["/laptop/x"] = "p2"
	if err := saveProjectsCache(dir, other); err != nil {
		t.Fatal(err)
	}
	got, err := loadProjectsCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Checkouts["/laptop/api"].Name != "api" || got.Checkouts["/laptop/web"].Name != "web" || got.ByDir["/laptop/x"] != "p2" {
		t.Fatalf("after merge: %+v %+v", got.Checkouts, got.ByDir)
	}
	if _, ok := got.ByRemote["checkouts"]; ok {
		t.Fatal(`"checkouts" was read as a remote`)
	}
}

// A CLI from before I-480 rewrites the checkouts key as a remote entry;
// the cache still loads, with no checkouts (I-480).
func TestProjectsCacheSurvivesAnOldCLIsCheckoutsKey(t *testing.T) {
	dir := t.TempDir()
	old := `{"by_dir": {"/a": "p1"}, "checkouts": {"project_id": "", "slug": "", "name": ""}, "github.com/a/b": {"project_id": "p2", "slug": "b", "name": "b"}}`
	if err := os.WriteFile(projectsPath(dir), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadProjectsCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Checkouts) != 0 || c.ByDir["/a"] != "p1" || c.ByRemote["github.com/a/b"].Slug != "b" {
		t.Fatalf("loaded %+v", c)
	}
}
