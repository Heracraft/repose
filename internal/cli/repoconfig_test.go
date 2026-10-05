package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The repose.nix at the root of the project's checkout is applied by run
// and sync without being asked, once per change, and a file that failed is
// not rebuilt by every run (DECISIONS I-489).
func TestRunAppliesRepoConfig(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	errOut := &discardWriter{}
	f.env.ErrOut = errOut

	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.local, "repose.nix"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	revisions := func(id string) int {
		t.Helper()
		revs, err := f.env.Client.ListRevisions(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return len(revs)
	}

	frag := "{ pkgs, ... }:\n{\n  home.packages = [ pkgs.ripgrep ];\n}\n"
	write(frag)
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	projects, err := f.env.Client.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects: %v %v", projects, err)
	}
	p := projects[0]
	cfg, err := f.env.Client.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Fragment != frag {
		t.Fatalf("active fragment:\n%s", cfg.Fragment)
	}
	if !strings.Contains(errOut.buf.String(), "Applying repose.nix (revision ") {
		t.Fatalf("run said:\n%s", errOut.buf.String())
	}
	n := revisions(p.ID)

	// Unchanged: nothing sent, nothing said.
	errOut.buf.Reset()
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := revisions(p.ID); got != n {
		t.Fatalf("an unchanged repose.nix made a revision (%d, was %d)", got, n)
	}
	if strings.Contains(errOut.buf.String(), "repose.nix") {
		t.Fatalf("an unchanged repose.nix was mentioned:\n%s", errOut.buf.String())
	}

	// A broken file: sent once, then named instead of rebuilt.
	write(frag + "# repose-force-eval-error\n")
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("broken run: %v", err)
	}
	n = revisions(p.ID)
	errOut.buf.Reset()
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("broken run again: %v", err)
	}
	if got := revisions(p.ID); got != n {
		t.Fatalf("a repose.nix that failed was built again (%d, was %d)", got, n)
	}
	if !strings.Contains(errOut.buf.String(), "repose.nix did not build last time") {
		t.Fatalf("the failure was not named:\n%s", errOut.buf.String())
	}
	if cfg, _ := f.env.Client.GetConfig(ctx, p.ID); cfg == nil || cfg.Fragment != frag {
		t.Fatal("a failed build changed the active configuration")
	}

	// Fixed: sent again.
	fixed := strings.Replace(frag, "ripgrep", "fd", 1)
	write(fixed)
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("fixed run: %v", err)
	}
	if cfg, _ := f.env.Client.GetConfig(ctx, p.ID); cfg == nil || cfg.Fragment != fixed {
		t.Fatal("the fixed repose.nix was not applied")
	}
}

// Another repository's repose.nix never replaces a machine's
// configuration: a run that reaches the project by name from a checkout
// that is not its own sends nothing.
func TestRepoConfigOnlyFromTheProjectsCheckout(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	projects, _ := f.env.Client.ListProjects(ctx)
	p := projects[0]

	other := t.TempDir()
	mustRun(t, other, "git", "init", "-q", "-b", "main")
	mustRun(t, other, "git", "remote", "add", "origin", "https://github.com/someone/else.git")
	if err := os.WriteFile(filepath.Join(other, "repose.nix"), []byte("{ ... }: { }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := f.env.Client.GetConfig(ctx, p.ID)
	f.env.Cwd = other
	f.env.applyRepoConfig(ctx, &p, other, false)
	after, _ := f.env.Client.GetConfig(ctx, p.ID)
	if before.RevisionID != after.RevisionID {
		t.Fatal("another repository's repose.nix was applied")
	}
}
