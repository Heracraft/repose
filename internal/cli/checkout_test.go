package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// newFolderFixture is a run fixture whose machine has no checkout (what
// guestd leaves since I-368: no ~/<slug>) and whose laptop checkout is a
// folder named folder, a clone of the fixture's repository.
func newFolderFixture(t *testing.T, folder string) *runFixture {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	if err := os.RemoveAll(f.guestRepo()); err != nil {
		t.Fatal(err)
	}
	laptop := filepath.Join(t.TempDir(), folder)
	mustRun(t, filepath.Dir(laptop), "git", "clone", "-q", f.bare, folder)
	mustRun(t, laptop, "git", "config", "user.email", "dev@example.com")
	mustRun(t, laptop, "git", "config", "user.name", "Dev Laptop")
	f.local = laptop
	f.env.Cwd = laptop
	return f
}

func readGuestFile(t *testing.T, f *runFixture, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.guestHome, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

// The owner's case (DECISIONS I-368): the laptop folder is `factory`, the
// project is named something else, and the checkout on the machine is
// ~/factory, which the run says, and which the `repose` remote, exec and
// an agent's window all use.
func TestFirstSyncNamesTheCheckoutAfterTheLaptopFolder(t *testing.T) {
	f := newFolderFixture(t, "factory")
	ctx := context.Background()
	out := &discardWriter{}
	f.env.Out = out
	if err := os.WriteFile(filepath.Join(f.local, "notes.txt"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if got := readGuestFile(t, f, "factory/notes.txt"); got != "laptop\n" {
		t.Fatalf("~/factory/notes.txt = %q, want the laptop's", got)
	}
	if got := readGuestFile(t, f, ".repose/checkout"); got != "factory\n" {
		t.Fatalf("~/.repose/checkout = %q, want factory", got)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, testSlug)); err == nil {
		t.Fatalf("~/%s was made too", testSlug)
	}
	if !strings.Contains(out.buf.String(), "Checkout: ~/factory on the machine\n") {
		t.Fatalf("output = %q, want the checkout's path", out.buf.String())
	}
	if got := remoteURLOf(f.local, reposeRemoteName); got != testSlug+".repose:~/factory" {
		t.Fatalf("repose remote = %q", got)
	}

	// A second run says nothing about where the checkout is.
	out.buf.Reset()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.buf.String(), "Checkout:") {
		t.Fatalf("second run output = %q", out.buf.String())
	}

	// exec, and an agent's window, run in it.
	eo := &discardWriter{}
	f.env.Out = eo
	if err := ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Command: []string{"pwd"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(eo.buf.String()); !strings.HasSuffix(got, "/factory") {
		t.Fatalf("exec ran in %q, want ~/factory", got)
	}
	if err := startAgentWindow(ctx, f.target, testSlug, "cat", "", "cat", "hello", true, nil, mcpApprovals{}); err != nil {
		t.Fatal(err)
	}
	pwd, err := runSSH(ctx, f.target, "tmux display -p -t "+testSlug+":cat '#{pane_current_path}'", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(pwd))); got != mustEval(t, filepath.Join(f.guestHome, "factory")) {
		t.Fatalf("agent window in %q", got)
	}
}

// A later run from a folder with another name (a second laptop, a
// renamed clone) uses the checkout the machine has.
func TestLaterRunsKeepTheRecordedCheckout(t *testing.T) {
	f := newFolderFixture(t, "factory")
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "elsewhere")
	mustRun(t, filepath.Dir(other), "git", "clone", "-q", f.bare, "elsewhere")
	if err := os.WriteFile(filepath.Join(other, "later.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.env.Cwd = other
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatal(err)
	}
	if got := readGuestFile(t, f, "factory/later.txt"); got != "x\n" {
		t.Fatalf("~/factory/later.txt = %q: the sync went elsewhere", got)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, "elsewhere")); err == nil {
		t.Fatal("~/elsewhere was made")
	}
}

// A folder whose name is already a non-empty directory in the guest's
// home (~/go, say) is not synced into; the slug is used instead.
func TestFirstSyncFallsBackToTheSlugWhenTheNameIsTaken(t *testing.T) {
	f := newFolderFixture(t, "go")
	if err := os.MkdirAll(filepath.Join(f.guestHome, "go", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if got := readGuestFile(t, f, testSlug+"/README.md"); got != "hello\n" {
		t.Fatalf("~/%s/README.md = %q, want the checkout there", testSlug, got)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, "go", "README.md")); err == nil {
		t.Fatal("synced into the existing ~/go")
	}
	if got := readGuestFile(t, f, ".repose/checkout"); got != testSlug+"\n" {
		t.Fatalf("~/.repose/checkout = %q", got)
	}
}

// A machine set up before I-368 has ~/<slug>, and keeps it whatever the
// laptop folder is called.
func TestAnOldCheckoutStaysUnderTheSlug(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	laptop := filepath.Join(t.TempDir(), "factory")
	mustRun(t, filepath.Dir(laptop), "git", "clone", "-q", f.bare, "factory")
	if err := os.WriteFile(filepath.Join(laptop, "n.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.env.Cwd = laptop
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatal(err)
	}
	if got := readGuestFile(t, f, testSlug+"/n.txt"); got != "n\n" {
		t.Fatalf("~/%s/n.txt = %q", testSlug, got)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, "factory")); err == nil {
		t.Fatal("~/factory was made beside the old checkout")
	}
}

// Nothing synced, nothing made: `run --no-sync` and a run outside a
// repository leave the home directory as it is, and exec runs there.
func TestNoSyncMakesNoCheckout(t *testing.T) {
	f := newFolderFixture(t, "factory")
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	f.env.Cwd = plain
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"factory", testSlug, filepath.Base(plain)} {
		if _, err := os.Stat(filepath.Join(f.guestHome, d)); err == nil {
			t.Fatalf("~/%s was made without a sync", d)
		}
	}
	if readGuestFile(t, f, ".repose/checkout") != "" {
		t.Fatal("~/.repose/checkout written without a sync")
	}
	eo := &discardWriter{}
	f.env.Out = eo
	if err := ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Command: []string{"pwd"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(eo.buf.String())); got != mustEval(t, f.guestHome) {
		t.Fatalf("exec ran in %q, want the home directory", got)
	}
	if name, err := guestCheckoutName(ctx, f.target, testSlug); err != nil || name != "" {
		t.Fatalf("guestCheckoutName = %q, %v; want none", name, err)
	}
	// The first sync after that makes it.
	f.env.Cwd = f.local
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if got := readGuestFile(t, f, "factory/README.md"); got != "hello\n" {
		t.Fatalf("~/factory/README.md = %q after the first sync", got)
	}
}

// cp's relative guest paths start at the checkout, or at the home when
// the machine has none.
func TestCpGuestPathFollowsTheCheckout(t *testing.T) {
	for _, tc := range []struct{ checkout, path, want string }{
		{"factory", "logs/x.log", "factory/logs/x.log"},
		{"factory", "", "factory"},
		{"factory", "/tmp/a", "/tmp/a"},
		{"factory", "~/.bashrc", "~/.bashrc"},
		{"", "logs/x.log", "logs/x.log"},
		{"", "", "."},
	} {
		if got := (cpSide{Remote: true, Path: tc.path}).guestPath(tc.checkout); got != tc.want {
			t.Errorf("guestPath(%q) with checkout %q = %q, want %q", tc.path, tc.checkout, got, tc.want)
		}
	}
}

func TestCheckoutNameFromTheFolder(t *testing.T) {
	for root, want := range map[string]string{
		"/home/a/Downloads/projects/factory": "factory",
		"/home/a/job search":                 "job-search",
		"/home/a/.hidden":                    "hidden",
		"/":                                  "",
		"":                                   "",
	} {
		if got := checkoutName(root); got != want {
			t.Errorf("checkoutName(%q) = %q, want %q", root, got, want)
		}
	}
}

// The session starts in the home directory on a machine with no
// checkout; the sync that makes one moves an idle `shell` window into it,
// and leaves a busy one alone.
func TestFirstSyncMovesTheIdleShellIntoTheCheckout(t *testing.T) {
	for _, busy := range []bool{false, true} {
		f := newFolderFixture(t, "factory")
		ctx := context.Background()
		t.Cleanup(func() { _, _ = runSSH(context.Background(), f.target, "tmux kill-server", nil) })
		setup := "tmux kill-session -t " + testSlug + " 2>/dev/null; cd ~ && tmux new-session -d -s " + testSlug + " -n shell -c ~ bash"
		if busy {
			setup = "tmux kill-session -t " + testSlug + " 2>/dev/null; cd ~ && tmux new-session -d -s " + testSlug + " -n shell -c ~ 'sleep 600'"
		}
		if _, err := runSSH(ctx, f.target, setup, nil); err != nil {
			t.Fatal(err)
		}
		if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
			t.Fatal(err)
		}
		out, err := runSSH(ctx, f.target, "tmux display -p -t '="+testSlug+":shell' '#{pane_current_path} #{pane_current_command}'", nil)
		if err != nil {
			t.Fatal(err)
		}
		path, cmd, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		got, _ := filepath.EvalSymlinks(path)
		if busy {
			if cmd != "sleep" || got != mustEval(t, f.guestHome) {
				t.Fatalf("busy shell window: %q, want sleep left in the home directory", out)
			}
			continue
		}
		if got != mustEval(t, filepath.Join(f.guestHome, "factory")) {
			t.Fatalf("idle shell window in %q, want ~/factory", out)
		}
	}
}
