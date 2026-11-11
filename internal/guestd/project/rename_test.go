package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSetupUnderANewNameLinksTheOldCheckout is I-255: a volume restored
// into a project with another slug (a restore --as-new) has its
// checkout at ~/<old slug>; SetupProject makes ~/<new slug> a relative
// symlink to it instead of an empty git repository, on every start, and
// never touches a directory that exists.
func TestSetupUnderANewNameLinksTheOldCheckout(t *testing.T) {
	h, p, run := newHandler(t)
	ctx := context.Background()
	if err := h.Setup(ctx, req()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// The source's checkout, with something in it.
	if err := os.MkdirAll(filepath.Join(p.ProjectDir("todo-app"), ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ProjectDir("todo-app"), "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInits := len(run.Calls())

	// The same volume starts as todo-app-old.
	copyOf := req()
	copyOf.ProjectSlug, copyOf.RemoteUrl = "todo-app-old", ""
	if err := h.Setup(ctx, copyOf); err != nil {
		t.Fatalf("setup under the new name: %v", err)
	}
	link := p.ProjectDir("todo-app-old")
	target, err := os.Readlink(link)
	if err != nil || target != "todo-app" {
		t.Fatalf("~/todo-app-old = %q, %v; want a link to todo-app", target, err)
	}
	if _, err := os.Stat(filepath.Join(link, "main.go")); err != nil {
		t.Fatalf("the checkout is not reachable through the link: %v", err)
	}
	for _, c := range run.Calls()[gitInits:] {
		if len(c.Argv) > 1 && c.Argv[0] == "git" && c.Argv[1] == "init" {
			t.Fatalf("git init ran in the linked checkout: %v", c)
		}
	}
	if h.Slug() != "todo-app-old" {
		t.Fatalf("Slug() = %q", h.Slug())
	}

	// A second start (project.json now says the copy) leaves the link.
	if err := h.Setup(ctx, copyOf); err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != "todo-app" {
		t.Fatalf("after a second setup: %q, %v", target, err)
	}

	// A copy of the copy links to the real directory, not to a link.
	ff := req()
	ff.ProjectSlug, ff.RemoteUrl = "todo-app-older", ""
	if err := h.Setup(ctx, ff); err != nil {
		t.Fatalf("setup as the copy's copy: %v", err)
	}
	if target, err := os.Readlink(p.ProjectDir("todo-app-older")); err != nil || target != "todo-app" {
		t.Fatalf("copy of a copy: %q, %v", target, err)
	}

	// The old checkout gone: the dangling link is removed, and nothing
	// replaces it (the next sync makes a checkout, I-368).
	if err := os.RemoveAll(p.ProjectDir("todo-app")); err != nil {
		t.Fatal(err)
	}
	if err := h.Setup(ctx, copyOf); err != nil {
		t.Fatalf("setup over a dangling link: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("dangling link left or replaced: %v", err)
	}
}

// TestPreviousCheckoutStaysInTheHome: a project.json naming a slug that is
// not a plain directory of the home never becomes a link target.
func TestPreviousCheckoutStaysInTheHome(t *testing.T) {
	h, p, _ := newHandler(t)
	if err := os.MkdirAll(p.Home(), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, p.ProjectDir("escape")); err != nil {
		t.Fatal(err)
	}
	for _, prev := range []string{"", "new", "../etc", "escape", "missing"} {
		if got := h.previousCheckout("new", prev); got != "" {
			t.Errorf("previousCheckout(new, %q) = %q, want none", prev, got)
		}
	}
}
