package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// fakeEditors puts launchers named names in a scratch directory that
// editorLookPath searches instead of PATH; each records its arguments and
// whether REPOSE_SSH_PREPARED reached it.
func fakeEditors(t *testing.T, names ...string) (record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	for _, n := range names {
		script := "#!/bin/sh\necho \"$(basename \"$0\") $*|prepared=${REPOSE_SSH_PREPARED:-unset}\" >> " + record + "\n"
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := editorLookPath
	editorLookPath = func(name string) (string, error) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			return "", exec.ErrNotFound
		}
		return p, nil
	}
	t.Cleanup(func() { editorLookPath = old })
	return record
}

func TestCodeOpensTheCheckoutOverSSH(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	// As in a real command, where Execute set it for the CLI's own ssh:
	// the editor must not inherit it, or its ssh would skip the prepare.
	t.Setenv(envSSHPrepared, "1")
	t.Setenv(envReposeEditor, "")

	record := fakeEditors(t, "cursor", "zed")
	var out strings.Builder
	f.env.Out = &out
	if err := CodeCmd(ctx, f.env, testSlug, ""); err != nil {
		t.Fatalf("code: %v", err)
	}
	if err := CodeCmd(ctx, f.env, testSlug, "zed"); err != nil {
		t.Fatalf("code --editor zed: %v", err)
	}
	t.Setenv(envReposeEditor, "cursor")
	if err := CodeCmd(ctx, f.env, testSlug, ""); err != nil {
		t.Fatalf("REPOSE_EDITOR=cursor: %v", err)
	}
	b, _ := os.ReadFile(record)
	want := "cursor --remote ssh-remote+proj.repose /home/dev/proj|prepared=unset\n" +
		"zed ssh://proj.repose/home/dev/proj|prepared=unset\n" +
		"cursor --remote ssh-remote+proj.repose /home/dev/proj|prepared=unset\n"
	if string(b) != want {
		t.Fatalf("launched:\n%s\nwant:\n%s", b, want)
	}
	if !strings.Contains(out.String(), "Opening proj.repose:/home/dev/proj in Cursor\n") {
		t.Fatalf("said: %q", out.String())
	}

	// A name that is not an editor, one that is not installed, none at all.
	var ee *exitError
	if err := CodeCmd(ctx, f.env, testSlug, "vim"); !errors.As(err, &ee) || ee.code != ExitUsage || !strings.Contains(ee.msg, "--editor must be one of code, cursor, zed") {
		t.Errorf("--editor vim: %v", err)
	}
	if err := CodeCmd(ctx, f.env, testSlug, "code"); !errors.As(err, &ee) || ee.code != ExitGeneric || !strings.Contains(ee.msg, "VS Code is not installed") {
		t.Errorf("--editor code: %v", err)
	}
	t.Setenv(envReposeEditor, "")
	fakeEditors(t)
	if err := CodeCmd(ctx, f.env, testSlug, ""); !errors.As(err, &ee) || !strings.Contains(ee.msg, "No editor found") {
		t.Errorf("no editor: %v", err)
	}

	// A stopped machine starts first, as attach does (I-641).
	fakeEditors(t, "code")
	if err := StopCmd(ctx, f.env, testSlug, false); err != nil {
		t.Fatal(err)
	}
	if err := CodeCmd(ctx, f.env, testSlug, ""); err != nil {
		t.Errorf("stopped: %v", err)
	}
}

// TestClaudeDesktopLink is I-1606's link: the alias and the checkout, or
// the home, and nothing else.
func TestClaudeDesktopLink(t *testing.T) {
	for _, c := range []struct{ slug, checkout, want string }{
		{"todo-app", "todo-app", "claude://code/new?ssh_host=todo-app.repose&ssh_folder=/home/dev/todo-app"},
		{"api", "job_search.v2", "claude://code/new?ssh_host=api.repose&ssh_folder=/home/dev/job_search.v2"},
		{"api", "", "claude://code/new?ssh_host=api.repose&ssh_folder=/home/dev"},
		{"api", "a&q=x", "claude://code/new?ssh_host=api.repose&ssh_folder=~"},
	} {
		if got := claudeDesktopLink(c.slug, c.checkout); got != c.want {
			t.Errorf("claudeDesktopLink(%q, %q) = %q, want %q", c.slug, c.checkout, got, c.want)
		}
	}
}

// TestCodeOpensClaudeDesktop: `repose code --editor claude` proves the
// connection, starts a stopped machine, tells the api the checkout
// (I-1610) and opens Claude Desktop's link (I-1606); with no display it
// prints the link instead.
func TestCodeOpensClaudeDesktop(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envReposeEditor, "")
	t.Setenv("REPOSE_TEST_GOOS", "darwin")
	var opened []string
	old := openLink
	openLink = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { openLink = old })
	// Nothing is installed: claude needs no command on the PATH.
	fakeEditors(t)

	if err := StopCmd(ctx, f.env, testSlug, false); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	f.env.Out = &out
	if err := CodeCmd(ctx, f.env, testSlug, "claude"); err != nil {
		t.Fatalf("code --editor claude: %v", err)
	}
	want := "claude://code/new?ssh_host=proj.repose&ssh_folder=/home/dev/proj"
	if len(opened) != 1 || opened[0] != want {
		t.Fatalf("opened %q, want [%q]", opened, want)
	}
	if !strings.Contains(out.String(), "Opening proj.repose:/home/dev/proj in Claude Desktop\n") {
		t.Fatalf("said: %q", out.String())
	}
	ps, err := f.env.Client.ListProjects(ctx)
	if err != nil || len(ps) != 1 {
		t.Fatalf("projects: %v %v", ps, err)
	}
	p := ps[0]
	if p.State != "running" {
		t.Fatalf("state %s after code on a stopped machine, want running", p.State)
	}
	if p.Checkout == nil || *p.Checkout != "proj" {
		t.Fatalf("the api's checkout is %v, want proj", p.Checkout)
	}

	// Never picked when nothing names it.
	var ee *exitError
	if err := CodeCmd(ctx, f.env, testSlug, ""); !errors.As(err, &ee) || !strings.Contains(ee.msg, "No editor found") {
		t.Errorf("no editor: %v", err)
	}

	// A Linux laptop with no display prints the link.
	t.Setenv("REPOSE_TEST_GOOS", "linux")
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	out.Reset()
	if err := CodeCmd(ctx, f.env, testSlug, "claude"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || !strings.Contains(out.String(), "Open this link on the computer with Claude Desktop: "+want+"\n") {
		t.Fatalf("headless: opened %q, said %q", opened, out.String())
	}
}
