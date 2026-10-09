package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func TestResolveProjectOrder(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	client := newClient(fake.URL()+"/v1", staticToken("tok"))
	ctx := context.Background()

	p, err := client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", RemoteURL: "github.com/a/b", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := client.CreateProject(ctx, CreateProjectRequest{Name: "explicit-target", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("explicit project id wins over everything", func(t *testing.T) {
		cache := newProjectsCache()
		deps := resolveDeps{RemoteFor: func(string) string { return "github.com/a/b" }}
		res, err := resolveProject(ctx, client, t.TempDir(), "/cwd", other.ID, &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project == nil || res.Project.ID != other.ID {
			t.Fatalf("got %+v, want %s", res.Project, other.ID)
		}
	})

	t.Run("explicit slug resolves via listing", func(t *testing.T) {
		cache := newProjectsCache()
		deps := resolveDeps{RemoteFor: func(string) string { return "" }}
		res, err := resolveProject(ctx, client, t.TempDir(), "/cwd", "explicit-target", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project == nil || res.Project.ID != other.ID {
			t.Fatalf("got %+v, want %s", res.Project, other.ID)
		}
	})

	// I-152: the owner's nuru-wasm checkout resolved to age-calculator
	// because an earlier --project had written by_dir. A by_dir entry for
	// a project whose remote is not the directory's is ignored and
	// dropped from the cache.
	t.Run("by_dir for another repository's project is ignored and forgotten", func(t *testing.T) {
		cache := newProjectsCache()
		cache.ByDir["/cwd"] = other.ID
		dir := t.TempDir()
		deps := resolveDeps{RemoteFor: func(string) string { return "github.com/a/b" }}
		res, err := resolveProject(ctx, client, dir, "/cwd", "", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project == nil || res.Project.ID != p.ID {
			t.Fatalf("the directory's remote should have won: got %+v", res.Project)
		}
		if _, ok := cache.ByDir["/cwd"]; ok {
			t.Fatal("the poisoned by_dir entry is still cached")
		}
		reloaded, _ := loadProjectsCache(dir)
		if _, ok := reloaded.ByDir["/cwd"]; ok {
			t.Fatal("the poisoned by_dir entry is still on disk")
		}
	})

	t.Run("by_dir for a project with no remote in a directory with none", func(t *testing.T) {
		cache := newProjectsCache()
		cache.ByDir["/cwd"] = other.ID
		deps := resolveDeps{RemoteFor: func(string) string { return "" }}
		res, err := resolveProject(ctx, client, t.TempDir(), "/cwd", "", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project == nil || res.Project.ID != other.ID {
			t.Fatalf("by_dir should have been used: got %+v", res.Project)
		}
	})

	t.Run("by_dir is keyed by the repository root", func(t *testing.T) {
		cache := newProjectsCache()
		cache.ByDir["/repo"] = other.ID
		deps := resolveDeps{RemoteFor: func(string) string { return "" }, RootFor: func(string) string { return "/repo" }}
		res, err := resolveProject(ctx, client, t.TempDir(), "/repo/src/deep", "", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project == nil || res.Project.ID != other.ID {
			t.Fatalf("a subdirectory should find the root's project: got %+v", res.Project)
		}
	})

	t.Run("an explicit project does not write by_dir", func(t *testing.T) {
		cache := newProjectsCache()
		dir := t.TempDir()
		deps := resolveDeps{RemoteFor: func(string) string { return "github.com/a/b" }}
		if _, err := resolveProject(ctx, client, dir, "/cwd", "explicit-target", &cache, deps); err != nil {
			t.Fatal(err)
		}
		if len(cache.ByDir) != 0 {
			t.Fatalf("by_dir = %v after an explicit --project", cache.ByDir)
		}
		// And the next bare run in that directory resolves by its remote.
		res, err := resolveProject(ctx, client, dir, "/cwd", "", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project == nil || res.Project.ID != p.ID {
			t.Fatalf("bare run after --project: got %+v, want %s", res.Project, p.ID)
		}
	})

	t.Run("an unknown explicit project says how to list them", func(t *testing.T) {
		cache := newProjectsCache()
		deps := resolveDeps{RemoteFor: func(string) string { return "" }}
		_, err := resolveProject(ctx, client, t.TempDir(), "/cwd", "nope", &cache, deps)
		ee, ok := err.(*exitError)
		if !ok || ee.code != ExitProjectNotFound || !strings.Contains(ee.msg, "repose ls") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("remote resolves and populates the cache", func(t *testing.T) {
		cache := newProjectsCache()
		dir := t.TempDir()
		deps := resolveDeps{RemoteFor: func(string) string { return "github.com/a/b" }}
		res, err := resolveProject(ctx, client, dir, "/cwd", "", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project == nil || res.Project.ID != p.ID {
			t.Fatalf("got %+v, want %s", res.Project, p.ID)
		}
		if cache.ByRemote["github.com/a/b"].ProjectID != p.ID {
			t.Fatalf("cache not populated: %+v", cache.ByRemote)
		}
		reloaded, err := loadProjectsCache(dir)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.ByRemote["github.com/a/b"].ProjectID != p.ID {
			t.Fatalf("cache not persisted: %+v", reloaded.ByRemote)
		}
	})

	t.Run("no remote and nothing cached returns empty result", func(t *testing.T) {
		cache := newProjectsCache()
		deps := resolveDeps{RemoteFor: func(string) string { return "" }}
		res, err := resolveProject(ctx, client, t.TempDir(), "/cwd", "", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project != nil {
			t.Fatalf("expected no project, got %+v", res.Project)
		}
	})

	t.Run("unknown remote returns empty result but reports the remote", func(t *testing.T) {
		cache := newProjectsCache()
		deps := resolveDeps{RemoteFor: func(string) string { return "github.com/unknown/repo" }}
		res, err := resolveProject(ctx, client, t.TempDir(), "/cwd", "", &cache, deps)
		if err != nil {
			t.Fatal(err)
		}
		if res.Project != nil || res.Remote != "github.com/unknown/repo" {
			t.Fatalf("got %+v", res)
		}
	})
}

func TestErrNoProjectFoundMessages(t *testing.T) {
	e := errNoProjectFound("").(*exitError)
	if e.code != ExitProjectNotFound {
		t.Fatalf("code = %d", e.code)
	}
	e2 := errNoProjectFound("github.com/a/b").(*exitError)
	if e2.code != ExitProjectNotFound {
		t.Fatalf("code = %d", e2.code)
	}
}

// A command that takes PROJECT only as --project says so in its hint;
// one with PROJECT in its usage keeps the argument form.
func TestNoProjectHintForm(t *testing.T) {
	root := newRootCmd("test")
	for args, want := range map[string]string{
		"mcp forward notes":    "Name one: `repose mcp forward NAME... --project PROJECT`",
		"secrets import --mcp": "Name one: `repose secrets import --project PROJECT`",
		"secrets set NAME":     "Name one: `repose secrets set NAME --project PROJECT`",
		"mcp list":             "Name one: `repose mcp list PROJECT`",
		"attach":               "Name one: `repose attach PROJECT`",
		"exec ls":              "Name one: `repose exec PROJECT COMMAND`",
	} {
		cmd, _, err := root.Find(strings.Fields(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		if got := errNoProjectFoundFor("", hintCommand(cmd)).Error(); !strings.Contains(got, want) {
			t.Errorf("repose %s: %q, want %q", args, got, want)
		}
	}
	got := errNoProjectFoundFor("github.com/a/b", "repose mcp forward --project").Error()
	if !strings.Contains(got, "or name one: `repose mcp forward --project PROJECT`.") {
		t.Errorf("with a remote: %q", got)
	}
}
