package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// newRunFixture wires an Env against a fresh fakeapi.Fake and a
// testguest.Guest whose $HOME/<slug> is a clone of a bare "origin"
// repository — the same shape as newSyncFixture, but exercised through
// runRun end to end rather than through syncGuest directly.
type runFixture struct {
	*syncFixture
	env *Env
}

func newRunFixture(t *testing.T, fake *fakeapi.Fake) *runFixture {
	t.Helper()
	sf := newSyncFixture(t)
	// A real guest's tmux session is created at boot by
	// repose-tmux-session.service (docs/interfaces/guest-conventions.md
	// "tmux"), independent of anything the CLI does; the fixture must
	// have it before runRun looks for windows in it.
	if _, err := runSSH(context.Background(), sf.target, "tmux new-session -d -s "+testSlug+" -c ~/"+testSlug, nil); err != nil {
		t.Fatalf("seeding the guest tmux session: %v", err)
	}
	dir := t.TempDir()
	home := withHome(t) // repoints $HOME so ensureIdentityKey and ensureCert write under a scratch dir
	cache := newProjectsCache()
	env := &Env{
		Dir: dir, Cfg: defaultConfig(), Cache: cache, Cwd: sf.local, HomeDir: home,
		Client: newClient(fake.URL()+"/v1", staticToken("tok")),
		Out:    &discardWriter{}, ErrOut: &discardWriter{},
		TargetFor: func(slug string) sshTarget { return sf.target },
	}
	return &runFixture{syncFixture: sf, env: env}
}

type discardWriter struct{ buf strings.Builder }

func (w *discardWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func TestRunCreatesStartsAndAttachesNewProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)

	err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false)
	if err != nil {
		t.Fatalf("runRun: %v", err)
	}

	projects, err := f.env.Client.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Slug != testSlug || projects[0].State != "running" {
		t.Fatalf("projects = %+v", projects)
	}
	// The checkout has a remote, so the project is cached under it and
	// not under the directory (I-152: by_dir is for --name projects with
	// no remote only).
	remote := gitRemoteOrigin(f.local)
	if f.env.Cache.ByRemote[remote].ProjectID != projects[0].ID {
		t.Fatalf("by_remote cache not populated for %q: %+v", remote, f.env.Cache.ByRemote)
	}
	if len(f.env.Cache.ByDir) != 0 {
		t.Fatalf("by_dir written for a project with a remote: %+v", f.env.Cache.ByDir)
	}
}

func TestRunSecondTimeAttachesExistingProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()

	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	// A fresh Env (as a new process invocation would have), same cache
	// file and home, must resolve the existing project via by_dir and not
	// try to create a second one.
	reloadedCache, err := loadProjectsCache(f.env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	env2 := &Env{
		Dir: f.env.Dir, Cfg: f.env.Cfg, Cache: reloadedCache, Cwd: f.local, HomeDir: f.env.HomeDir,
		Client: f.env.Client, Out: &discardWriter{}, ErrOut: &discardWriter{}, TargetFor: f.env.TargetFor,
	}
	if err := runRun(ctx, env2, RunOptions{NoAttach: true}, false); err != nil {
		t.Fatalf("second runRun: %v", err)
	}
	projects, err := f.env.Client.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected exactly one project, got %d: %+v", len(projects), projects)
	}
}

func TestRunWithPromptSendsIntoTmuxWindow(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()

	if err := runRun(ctx, f.env, RunOptions{
		Name: testSlug, Agent: "cat", Prompt: "finish the feature", NoAttach: true,
	}, false); err != nil {
		t.Fatalf("runRun: %v", err)
	}

	pane, err := waitForCapture(ctx, f.target, testSlug, "cat", "finish the feature")
	if err != nil {
		t.Fatalf("prompt never reached the agent window: %v\n%s", err, pane)
	}
}

// TestRunClaudeNotLoggedInAttachesInstead is 07-cli.md §5.5 step 7: no
// ~/.claude/.credentials.json in the guest (the CLI never copies it; it
// only checks) and no CLAUDE_CODE_OAUTH_TOKEN secret means attach instead
// of sending, so the user can finish the login themselves.
func TestRunClaudeNotLoggedInAttachesInstead(t *testing.T) {
	// A machine with the real Claude Code on PATH (a developer's, this
	// repo's dev box) would start it in the fake guest, where it writes
	// into the test's temporary home while cleanup removes it. A stand-in
	// that just waits keeps the window alive without touching anything;
	// set before the fixture starts tmux, whose server keeps this PATH.
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "claude"), []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))

	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()

	var out strings.Builder
	f.env.Out = &out

	// The window runs `claude`, which a guest has and a CI runner does
	// not: there the pane exits as soon as it opens and the window is
	// gone before the check below (CI, 2026-09-20). remain-on-exit keeps
	// the window, dead pane and all; what the test asserts is that it was
	// opened and nothing was typed into it. Global: with a session target
	// tmux sets this window option on the current window only, and the
	// fixture's tmux server is private to this test.
	if _, err := runSSH(ctx, f.target, "tmux set-option -g -w remain-on-exit on", nil); err != nil {
		t.Fatalf("remain-on-exit on the guest session: %v", err)
	}

	// With --no-attach nobody logs in now: exit 1, nothing typed, and
	// the line names the window to log in in (I-607).
	err := runRun(ctx, f.env, RunOptions{
		Name: testSlug, Agent: "claude", Prompt: "finish the feature", NoAttach: true,
	}, false)
	wantLoginRefusal(t, err, "claude")

	// tmux creates the window asynchronously to the client's return; on a
	// slow runner the first look can miss it (CI, 2026-09-20).
	var exists bool
	for deadline := time.Now().Add(5 * time.Second); ; {
		var err error
		windows, err := listWindows(ctx, f.target, testSlug)
		if err != nil {
			t.Fatal(err)
		}
		exists = slices.Contains(windows, "claude")
		if exists || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !exists {
		t.Fatal("expected a claude window to be opened even though the prompt was not sent")
	}
	pane, err := capturePane(ctx, f.target, testSlug, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pane, "finish the feature") {
		t.Fatalf("prompt must not have been sent: %s", pane)
	}
	if ps, _ := runSSH(ctx, f.target, "pgrep -f '[f]inish the feature' || true", nil); strings.TrimSpace(string(ps)) != "" {
		t.Fatalf("--no-attach left a waiter with the prompt: %s", ps)
	}
}

// wantLoginRefusal is `run --no-attach -p` on a machine with no Claude
// Code login (I-607): exit 1 naming the window.
func wantLoginRefusal(t *testing.T, err error, window string) {
	t.Helper()
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != ExitGeneric || !strings.Contains(ee.msg, "Claude Code is not logged in on "+testSlug+", so your prompt was not typed. Log in with `repose attach "+testSlug+" -w "+window+"`, then run your prompt again.") {
		t.Fatalf("want the login refusal for %s, got %v", window, err)
	}
}

// TestRunWorktreeThenPlainRun: `repose run --worktree PROMPT` opens the
// agent in ~/<slug>-<window> on repose/<window> and says so; a plain run
// after it opens <agent>-2 in the checkout with the shared-tree warning
// (DECISIONS I-253).
func TestRunWorktreeThenPlainRun(t *testing.T) {
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "claude"), []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	var out, errOut strings.Builder
	f.env.Out, f.env.ErrOut = &out, &errOut

	// Claude is not logged in in the fixture, so the window opens and
	// nothing is typed: what matters here is where it opens.
	wantLoginRefusal(t, runRun(ctx, f.env, RunOptions{Name: testSlug, Agent: "claude", Prompt: "try it one way", NoAttach: true, Worktree: true}, false), "claude")
	if !strings.Contains(out.String(), "Worktree: ~/proj-worktree-1 on branch worktree-1\n") {
		t.Fatalf("stdout lacks the worktree line: %s", out.String())
	}
	if strings.Contains(errOut.String(), "share one working tree") {
		t.Fatalf("a worktree run warned about a shared tree: %s", errOut.String())
	}
	pwd, err := runSSH(ctx, f.target, "tmux display -p -t "+testSlug+":claude '#{pane_current_path}'", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(pwd))); got != mustEval(t, filepath.Join(f.guestHome, "proj-worktree-1")) {
		t.Fatalf("claude's pane is in %q, want ~/proj-worktree-1", got)
	}

	errOut.Reset()
	wantLoginRefusal(t, runRun(ctx, f.env, RunOptions{Name: testSlug, Agent: "claude", Prompt: "try it another way", NoAttach: true}, false), "claude-2")
	if !strings.Contains(errOut.String(), "Another claude window is open; two agents share one working tree. `repose run --worktree` gives the next one its own.") {
		t.Fatalf("stderr lacks the shared-tree warning: %s", errOut.String())
	}
	pwd, err = runSSH(ctx, f.target, "tmux display -p -t "+testSlug+":claude-2 '#{pane_current_path}'", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(pwd))); got != mustEval(t, f.guestRepo()) {
		t.Fatalf("claude-2's pane is in %q, want the checkout", got)
	}
}

func TestSyncDirtyRemoteTreeRefusesWithExitSix(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()

	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent left this dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The laptop has new work that would land on the agent's (I-248).
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	env2 := &Env{
		Dir: f.env.Dir, Cfg: f.env.Cfg, Cache: f.env.Cache, Cwd: f.local, HomeDir: f.env.HomeDir,
		Client: f.env.Client, Out: &discardWriter{}, ErrOut: &discardWriter{}, TargetFor: f.env.TargetFor,
	}
	err := runRun(ctx, env2, RunOptions{NoAttach: true, Sync: true}, false)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitDirtyRemoteTree {
		t.Fatalf("err = %v, want an exitError with code %d", err, ExitDirtyRemoteTree)
	}
}

// I-210: a sync that sent a modified file and an untracked one leaves
// the guest tree dirty by construction; the next `repose sync` from the
// same laptop must go through, and a change an agent then makes in the guest
// must still refuse with exit 6.
func TestSyncTwiceWithADirtyLaptopTree(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	write := func(dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(f.local, "README.md", "edited on the laptop\n")
	write(f.local, "notes/todo.md", "untracked on the laptop\n")
	newEnv := func() *Env {
		return &Env{
			Dir: f.env.Dir, Cfg: f.env.Cfg, Cache: f.env.Cache, Cwd: f.local, HomeDir: f.env.HomeDir,
			Client: f.env.Client, Out: &discardWriter{}, ErrOut: &discardWriter{}, TargetFor: f.env.TargetFor,
		}
	}

	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	if st := mustRun(t, f.guestRepo(), "git", "status", "--porcelain"); !strings.Contains(st, "README.md") || !strings.Contains(st, "notes/") {
		t.Fatalf("the first sync did not leave the laptop's changes in the guest: %q", st)
	}
	// The same laptop tree, and then one edited further: both go through.
	if err := runRun(ctx, newEnv(), RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("second runRun with the same laptop tree: %v", err)
	}
	write(f.local, "README.md", "edited again on the laptop\n")
	if err := runRun(ctx, newEnv(), RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("third runRun after a laptop edit: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "edited again on the laptop\n" {
		t.Fatalf("guest README.md = %q", b)
	}

	// An agent's changes: a synced file edited (which the laptop still
	// sends), then a new file as well. With new laptop work each refuses
	// over the synced file, names only it, and touches nothing (I-573).
	for i, change := range []struct{ rel, body string }{{"notes/todo.md", "the agent's edit\n"}, {"agent-scratch.txt", "new\n"}} {
		write(f.guestRepo(), change.rel, change.body)
		write(f.local, "README.md", fmt.Sprintf("edited on the laptop, round %d\n", i))
		err := runRun(ctx, newEnv(), RunOptions{NoAttach: true, Sync: true}, false)
		ee, ok := err.(*exitError)
		if !ok || ee.code != ExitDirtyRemoteTree {
			t.Fatalf("after the agent wrote %s: err = %v, want exit %d", change.rel, err, ExitDirtyRemoteTree)
		}
		if !strings.Contains(ee.msg, "  notes/todo.md\n") || strings.Contains(ee.msg, "agent-scratch.txt") || strings.Contains(ee.msg, "README.md") {
			t.Fatalf("refusal after %s = %q, want notes/todo.md alone", change.rel, ee.msg)
		}
		if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), change.rel)); string(b) != change.body {
			t.Fatalf("%s changed by a refused run: %q", change.rel, b)
		}
	}
}

// I-210's race: the tree matched the last sync when the probe looked, and
// an agent wrote before the apply; the apply notices and refuses rather
// than resetting the agent's work away.
func TestSyncRefusesWhenTheGuestChangesAfterTheProbe(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(f.guestRepo(), "README.md")
	// A new laptop change, so the second sync applies (the same one again
	// is not applied at all, I-224).
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop, later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{BeforeApply: func(map[string]string) error {
		return os.WriteFile(agent, []byte("the agent, mid-sync\n"), 0o644)
	}})
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitDirtyRemoteTree {
		t.Fatalf("err = %v, want exit %d", err, ExitDirtyRemoteTree)
	}
	if b, _ := os.ReadFile(agent); string(b) != "the agent, mid-sync\n" {
		t.Fatalf("the agent's write was lost: %q", b)
	}
}
