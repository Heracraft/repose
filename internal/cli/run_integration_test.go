package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/testguest"
)

// This file is the integration coverage docs/workstreams/07-cli.md §7 asks
// for ("full run sequence with a local sshd ... standing in for the
// guest"); see internal/testguest's doc comment for why that sshd is an
// in-process Go server instead of the Docker fixture the doc names.

const testSlug = "proj"

func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// syncFixture is a bare "origin", a local clone (what the CLI runs
// against) and a fake guest whose $HOME/<slug> is a second clone of the
// same commit, connected over a real SSH session to an in-process sshd.
type syncFixture struct {
	bare, local, guestHome string
	guest                  *testguest.Guest
	target                 sshTarget
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	bare := t.TempDir()
	mustRun(t, bare, "git", "init", "--bare", "-q", "-b", "main", ".")

	seed := t.TempDir()
	mustRun(t, seed, "git", "clone", "-q", bare, ".")
	mustRun(t, seed, "git", "config", "user.email", "seed@example.com")
	mustRun(t, seed, "git", "config", "user.name", "Seed")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, seed, "git", "add", "README.md")
	mustRun(t, seed, "git", "commit", "-q", "-m", "initial")
	mustRun(t, seed, "git", "push", "-q", "origin", "main")

	local := t.TempDir()
	mustRun(t, local, "git", "clone", "-q", bare, ".")
	mustRun(t, local, "git", "config", "user.email", "dev@example.com")
	mustRun(t, local, "git", "config", "user.name", "Dev Laptop")

	guestHome := t.TempDir()
	guestRepo := filepath.Join(guestHome, testSlug)
	mustRun(t, guestHome, "git", "clone", "-q", bare, testSlug)
	mustRun(t, guestRepo, "git", "config", "user.email", "guest@example.com")
	mustRun(t, guestRepo, "git", "config", "user.name", "Guest")

	keyDir := t.TempDir()
	privPath, pub, err := testguest.GenerateClientKey(keyDir)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := testguest.New(guestHome, pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(guest.Close)

	host, port, _ := strings.Cut(guest.Addr, ":")
	target := sshTarget{Args: []string{
		"-p", port,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-i", privPath,
		"guest@" + host,
	}}

	return &syncFixture{bare: bare, local: local, guestHome: guestHome, guest: guest, target: target}
}

// guestRepo is the guest's checkout, found as the guest finds it
// (checkoutVar): the name ~/.repose/checkout records, else ~/<slug>.
func (f *syncFixture) guestRepo() string {
	if b, err := os.ReadFile(filepath.Join(f.guestHome, ".repose", "checkout")); err == nil {
		if n := strings.TrimSpace(string(b)); n != "" {
			return filepath.Join(f.guestHome, n)
		}
	}
	return filepath.Join(f.guestHome, testSlug)
}

func TestSyncDirtyRemoteRefused(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent was here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	ee, ok := err.(*exitError)
	if !ok {
		t.Fatalf("want *exitError, got %T: %v", err, err)
	}
	if ee.code != ExitDirtyRemoteTree {
		t.Fatalf("code = %d, want %d", ee.code, ExitDirtyRemoteTree)
	}
	if !strings.Contains(ee.msg, "README.md") {
		t.Fatalf("message missing the dirty file: %s", ee.msg)
	}
}

func TestSyncStashRemote(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent was here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{StashRemote: true}); err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	stashList := mustRun(t, f.guestRepo(), "git", "stash", "list")
	if stashList == "" {
		t.Fatal("expected a stash entry, got none")
	}
}

func TestSyncDiscardRemote(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent was here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{DiscardRemote: true}); err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	status := mustRun(t, f.guestRepo(), "git", "status", "--porcelain")
	if status != "" {
		t.Fatalf("guest tree still dirty after discard: %q", status)
	}
	content, err := os.ReadFile(filepath.Join(f.guestRepo(), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("discard did not revert README.md: %q", content)
	}
}

func TestSyncAppliesDiffAndUntracked(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("edited locally, uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "notes.md"), []byte("scratch notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	if err != nil {
		t.Fatalf("syncGuest: %v", err)
	}
	if summary.Modified != 1 || summary.Untracked != 1 {
		t.Fatalf("summary = %+v, want 1 modified, 1 untracked", summary)
	}
	readme, err := os.ReadFile(filepath.Join(f.guestRepo(), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(readme) != "edited locally, uncommitted\n" {
		t.Fatalf("diff not applied: %q", readme)
	}
	notes, err := os.ReadFile(filepath.Join(f.guestRepo(), "notes.md"))
	if err != nil {
		t.Fatalf("untracked file not copied: %v", err)
	}
	if string(notes) != "scratch notes\n" {
		t.Fatalf("untracked content wrong: %q", notes)
	}
}

func TestSyncCredentialsCopiesExactlyTheFourRows(t *testing.T) {
	f := newSyncFixture(t)
	home := t.TempDir()

	write := func(rel, content string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".config/gh/hosts.yml", "github.com:\n  oauth_token: abc\n")
	write(".codex/auth.json", `{"token":"abc"}`)
	write(".local/share/opencode/auth.json", `{"token":"abc"}`)
	// Planted but must never be copied.
	write(".claude/.credentials.json", `{"token":"should-never-travel"}`)
	write(".gemini/oauth_creds.json", `{"token":"should-never-travel"}`)
	write(".ssh/id_ed25519", "not a real key, must never travel")

	mustRun(t, f.local, "git", "config", "user.name", "Dev Laptop")
	mustRun(t, f.local, "git", "config", "user.email", "dev@example.com")

	copied, err := syncCredentials(context.Background(), f.target, home, f.local, credSyncOptions{RemoteURL: "github.com/a/b", ghToken: func() string { t.Fatal("hosts.yml has a token; the keyring must not be asked"); return "" }})
	if err != nil {
		t.Fatalf("syncCredentials: %v", err)
	}
	want := map[string]bool{"gh": true, "codex": true, "opencode": true, "git": true}
	if len(copied) != len(want) {
		t.Fatalf("copied = %v, want exactly %v", copied, want)
	}
	for _, c := range copied {
		if !want[c] {
			t.Fatalf("unexpected label copied: %s", c)
		}
	}

	for _, never := range []string{".claude/.credentials.json", ".gemini/oauth_creds.json", ".ssh/id_ed25519"} {
		if _, err := os.Stat(filepath.Join(f.guestHome, never)); err == nil {
			t.Fatalf("%s must never be synced but was found on the guest", never)
		}
	}
	ghBytes, err := os.ReadFile(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ghBytes), "abc") {
		t.Fatalf("gh hosts.yml content wrong: %s", ghBytes)
	}
	info, err := os.Stat(filepath.Join(f.guestHome, ".codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("codex auth.json mode = %v, want 0600", info.Mode().Perm())
	}
	gitconfig, err := os.ReadFile(filepath.Join(f.guestHome, ".gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gitconfig), "Dev Laptop") || !strings.Contains(string(gitconfig), "dev@example.com") {
		t.Fatalf(".gitconfig missing identity: %s", gitconfig)
	}
	// gh travelled and the remote is on github: the guest's git reaches
	// github over HTTPS with gh as the helper, so an agent can push
	// without the laptop's SSH keys (I-150).
	// Both SSH spellings, since the laptop's agent is not forwarded (I-247).
	if got := mustRun(t, f.guestHome, "git", "config", "--file", filepath.Join(f.guestHome, ".gitconfig"), "--get-all", "url.https://github.com/.insteadOf"); got != "git@github.com:\nssh://git@github.com/" {
		t.Fatalf("insteadOf = %q", got)
	}
	if got := mustRun(t, f.guestHome, "git", "config", "--file", filepath.Join(f.guestHome, ".gitconfig"), "credential.https://github.com.helper"); got != "!gh auth git-credential" {
		t.Fatalf("credential helper = %q", got)
	}
}

// gh 2.40+ keeps the token in the laptop's keyring and hosts.yml has none;
// the guest has no keyring, so the token is written into the hosts.yml
// that travels.
func TestSyncCredentialsCarriesAKeyringGhToken(t *testing.T) {
	f := newSyncFixture(t)
	home := t.TempDir()
	p := filepath.Join(home, ".config", "gh", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("github.com:\n    git_protocol: ssh\n    users:\n        dev:\n    user: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := syncCredentials(context.Background(), f.target, home, f.local, credSyncOptions{RemoteURL: "gitlab.com/a/b", ghToken: func() string { return "gho_fromkeyring" }})
	if err != nil {
		t.Fatalf("syncCredentials: %v", err)
	}
	if len(copied) == 0 || copied[0] != "gh" {
		t.Fatalf("copied = %v", copied)
	}
	b, err := os.ReadFile(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "github.com:\n    oauth_token: gho_fromkeyring\n    git_protocol: ssh") {
		t.Fatalf("token not written under github.com:\n%s", b)
	}
	if local, _ := os.ReadFile(p); strings.Contains(string(local), "oauth_token") {
		t.Fatal("the laptop's hosts.yml was changed")
	}
	// gh travelled, so github's SSH URLs go over HTTPS even for a project
	// hosted elsewhere: with no agent forwarding (I-247) a submodule or a
	// clone from github has no other way in.
	if got := mustRun(t, f.guestHome, "git", "config", "--file", filepath.Join(f.guestHome, ".gitconfig"), "--get-all", "url.https://github.com/.insteadOf"); got != "git@github.com:\nssh://git@github.com/" {
		t.Fatalf("insteadOf = %q", got)
	}
}

// features/secrets.md: a login done inside the guest (newer than the
// laptop's file) is not clobbered, and the CLI says which side won.
func TestSyncCredentialsKeepsANewerGuestLogin(t *testing.T) {
	f := newSyncFixture(t)
	home := t.TempDir()
	local := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`{"from":"laptop"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(local, old, old); err != nil {
		t.Fatal(err)
	}
	guestFile := filepath.Join(f.guestHome, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(guestFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guestFile, []byte(`{"from":"guest"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var kept []string
	copied, err := syncCredentials(context.Background(), f.target, home, f.local, credSyncOptions{Kept: func(l string) { kept = append(kept, l) }})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(guestFile); string(b) != `{"from":"guest"}` {
		t.Fatalf("the guest's newer login was overwritten: %s", b)
	}
	if len(kept) != 1 || kept[0] != "codex" {
		t.Fatalf("kept = %v", kept)
	}
	for _, c := range copied {
		if c == "codex" {
			t.Fatalf("codex reported as copied: %v", copied)
		}
	}

	// Once the laptop's is newer, it wins.
	if err := os.Chtimes(local, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := syncCredentials(context.Background(), f.target, home, f.local, credSyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(guestFile); string(b) != `{"from":"laptop"}` {
		t.Fatalf("the laptop's newer login did not arrive: %s", b)
	}
}

func TestPromptSendAndSecondWindowNaming(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	if _, err := runSSH(ctx, f.target, "tmux new-session -d -s "+testSlug+" -c ~/"+testSlug, nil); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}

	// Three prompts, three windows: cat, cat-2, cat-3 (I-253: no cap at -2).
	for i, want := range []string{"cat", "cat-2", "cat-3"} {
		name, othersOpen, err := windowNameFor(ctx, f.target, testSlug, "cat")
		if err != nil {
			t.Fatal(err)
		}
		if name != want || othersOpen != (i > 0) {
			t.Fatalf("window %d = %q othersOpen=%v, want %q/%v", i+1, name, othersOpen, want, i > 0)
		}
		prompt := "prompt number " + want
		if err := startAgentWindow(ctx, f.target, testSlug, name, "~/"+testSlug, "cat", prompt, false, nil); err != nil {
			t.Fatalf("startAgentWindow %s: %v", name, err)
		}
		if pane, err := waitForCapture(ctx, f.target, testSlug, name, prompt); err != nil {
			t.Fatalf("prompt never appeared in %s: %v\nlast capture:\n%s", name, err, pane)
		}
	}

	// A closed window's name is the next one handed out.
	if _, err := runSSH(ctx, f.target, "tmux kill-window -t "+testSlug+":cat-2", nil); err != nil {
		t.Fatal(err)
	}
	if name, _, err := windowNameFor(ctx, f.target, testSlug, "cat"); err != nil || name != "cat-2" {
		t.Fatalf("after closing cat-2: name = %q, err = %v, want cat-2", name, err)
	}

	windows, err := listWindows(ctx, f.target, testSlug)
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, w := range windows {
		has[w] = true
	}
	if !has["cat"] || !has["cat-3"] || has["cat-2"] {
		t.Fatalf("windows = %v, want cat and cat-3 and no cat-2", windows)
	}
}

// TestPromptWaitsForDevShellLoad (I-259): while the agent wrapper loads
// the checkout's dev environment it marks the pane @repose-devshell, and
// the prompt is typed only after the agent runs. The stand-in "wrapper"
// swallows whatever is typed while it loads, as a terminal does to input
// that arrives before an agent's TUI starts, so a prompt sent early never
// reaches the stand-in agent (cat writing to a file).
func TestPromptWaitsForDevShellLoad(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-session -d -s "+testSlug+" -c ~/"+testSlug, nil); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}
	// The load outlasts the ordinary wait for an agent to start.
	defer func(d time.Duration) { paneIdleTimeout = d }(paneIdleTimeout)
	paneIdleTimeout = 1 * time.Second
	got := filepath.Join(f.guestHome, "got")
	script := filepath.Join(f.guestHome, "fake-wrapper")
	body := "#!/bin/sh\n" +
		"tmux set-option -p -t \"$TMUX_PANE\" " + devShellLoadingOption + " loading\n" +
		"echo 'repose: loading the dev shell'\n" +
		"read -r swallowed; read -r swallowed\n" + // no output for well over paneIdleWait
		"tmux set-option -p -u -t \"$TMUX_PANE\" " + devShellLoadingOption + "\n" +
		"exec cat > " + got + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stand-in's loading ends when the test says so (two lines typed
	// by the test, not by startAgentWindow), four seconds in.
	go func() {
		time.Sleep(4 * time.Second)
		_, _ = runSSH(ctx, f.target, "tmux send-keys -t "+testSlug+":cat 'x' Enter 'y' Enter", nil)
	}()
	if _, err := runSSH(ctx, f.target, "tmux new-window -t "+testSlug+" -n cat -c ~/"+testSlug+" -d "+shQuote(script), nil); err != nil {
		t.Fatal(err)
	}
	loadingSeen := 0
	start := time.Now()
	if err := waitPaneIdle(ctx, f.target, testSlug, "cat", "cat", func() { loadingSeen++ }); err != nil {
		t.Fatal(err)
	}
	if loadingSeen != 1 {
		t.Fatalf("onLoading called %d times, want 1", loadingSeen)
	}
	if waited := time.Since(start); waited < 4*time.Second {
		t.Fatalf("waitPaneIdle returned after %s, before the dev shell loaded", waited)
	}
	if _, err := runSSH(ctx, f.target, "tmux send-keys -t "+testSlug+":cat -l "+shQuote("the prompt")+" && tmux send-keys -t "+testSlug+":cat Enter", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(got)
		if strings.Contains(string(b), "the prompt") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent never got the prompt; its input: %q", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestPickWindowHasNoCap(t *testing.T) {
	open := []string{"shell", "claude", "claude-2", "claude-3", "claude-4", "claude-5", "claude-6", "claude-7", "claude-8", "claude-9", "codex"}
	if name, others := pickWindow("claude", open, nil); name != "claude-10" || !others {
		t.Fatalf("pickWindow = %q/%v, want claude-10/true", name, others)
	}
	if name, others := pickWindow("pi", open, nil); name != "pi" || others {
		t.Fatalf("pickWindow(pi) = %q/%v, want pi/false", name, others)
	}
	// A window named like another agent's, or with a non-numeric suffix,
	// is not one of claude's windows.
	if name, others := pickWindow("claude", []string{"claude-x", "claudette"}, nil); name != "claude" || others {
		t.Fatalf("pickWindow over lookalikes = %q/%v, want claude/false", name, others)
	}
	taken := map[string]bool{"claude": true, "claude-2": true}
	if name, _ := pickWindow("claude", nil, func(n string) bool { return taken[n] }); name != "claude-3" {
		t.Fatalf("pickWindow with worktree leftovers = %q, want claude-3", name)
	}
}

func TestRunWorktree(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-session -d -s "+testSlug+" -c ~/"+testSlug, nil); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}
	head := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD")

	// The checkout's gitignored .env files, as a sync leaves them (I-197),
	// go into the worktree too (I-343); other ignored files and anything
	// under an ignored directory don't.
	exclude := ".env\n.env.*\nnode_modules/\nbuild.log\n"
	if err := os.WriteFile(filepath.Join(f.guestRepo(), ".git", "info", "exclude"), []byte(exclude), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		".env":                  "A=1\n",
		"api/.env.local":        "B=2\n",
		"build.log":             "x\n",
		"node_modules/pkg/.env": "C=3\n",
	} {
		p := filepath.Join(f.guestRepo(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The first window may be a worktree too; worktrees are numbered on
	// their own, apart from the window names (I-342).
	wt, err := prepareWorktree(ctx, f.target, testSlug, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Window != "cat" || wt.Dir != "~/proj-worktree-1" || wt.Branch != "worktree-1" || wt.Base != head || wt.Dirty || wt.Env != 2 {
		t.Fatalf("worktree = %+v", wt)
	}
	dir := filepath.Join(f.guestHome, "proj-worktree-1")
	if got := mustRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != "worktree-1" {
		t.Fatalf("worktree branch = %q", got)
	}
	for rel, want := range map[string]string{".env": "A=1\n", "api/.env.local": "B=2\n"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("%s in the worktree = %q, %v", rel, b, err)
		}
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600 kept", rel, fi.Mode().Perm())
		}
	}
	for _, rel := range []string{"build.log", "node_modules"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("%s was copied into the worktree", rel)
		}
	}
	if st := mustRun(t, dir, "git", "status", "--porcelain"); st != "" {
		t.Fatalf("the copied .env files show in the worktree's status: %q", st)
	}
	if err := startAgentWindow(ctx, f.target, testSlug, wt.Window, wt.Dir, "cat", "in the worktree", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := waitForCapture(ctx, f.target, testSlug, wt.Window, "in the worktree"); err != nil {
		t.Fatal(err)
	}
	pwd, err := runSSH(ctx, f.target, "tmux display -p -t "+testSlug+":cat '#{pane_current_path}'", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(pwd))); got != mustEval(t, dir) {
		t.Fatalf("pane path = %q, want %q", got, dir)
	}

	// The agent's work in the worktree is invisible to the checkout and
	// to the sync: the probe sees a clean tree, and a laptop change syncs.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("worktree agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runSSH(ctx, f.target, probeScript(testSlug, "", ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := parseProbe(string(out)); len(p.dirty) != 0 {
		t.Fatalf("a worktree made the checkout dirty: %v", p.dirty)
	}
	if err := os.WriteFile(filepath.Join(f.local, "laptop.txt"), []byte("from the laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatalf("sync with a worktree present: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) != "worktree agent\n" {
		t.Fatalf("the sync touched the worktree: README.md = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "laptop.txt")); err == nil {
		t.Fatal("the sync wrote into the worktree")
	}

	// Now the checkout is dirty (the synced untracked file): a second
	// worktree says its start lacks those changes, and is worktree-2 in
	// window cat-2.
	wt2, err := prepareWorktree(ctx, f.target, testSlug, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if wt2.Window != "cat-2" || wt2.Dir != "~/proj-worktree-2" || wt2.Branch != "worktree-2" || !wt2.Dirty {
		t.Fatalf("second worktree = %+v", wt2)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, "proj-worktree-2", "laptop.txt")); err == nil {
		t.Fatal("the uncommitted laptop.txt is in the new worktree")
	}

	// A later --worktree never reuses one: with window cat closed but
	// ~/proj-worktree-1 and worktree-1 still there, the window is cat
	// again and the worktree is 3.
	if _, err := runSSH(ctx, f.target, "tmux kill-window -t "+testSlug+":cat", nil); err != nil {
		t.Fatal(err)
	}
	wt3, err := prepareWorktree(ctx, f.target, testSlug, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if wt3.Window != "cat" || wt3.N != 3 {
		t.Fatalf("third worktree = %+v, want cat in worktree-3", wt3)
	}
	// A branch left behind without its directory still holds its number.
	mustRun(t, f.guestRepo(), "git", "worktree", "remove", "--force", filepath.Join(f.guestHome, "proj-worktree-2"))
	wt4, err := prepareWorktree(ctx, f.target, testSlug, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if wt4.N != 4 {
		t.Fatalf("with branch worktree-2 left = %+v, want worktree-4", wt4)
	}
	// The documented cleanup frees the number.
	mustRun(t, f.guestRepo(), "git", "worktree", "remove", "--force", dir)
	mustRun(t, f.guestRepo(), "git", "branch", "-D", "worktree-1")
	wt5, err := prepareWorktree(ctx, f.target, testSlug, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if wt5.N != 1 {
		t.Fatalf("after cleanup = %+v, want worktree-1 again", wt5)
	}
}

func TestRunWorktreeRefusals(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-session -d -s "+testSlug+" -c ~", nil); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}
	if err := os.RemoveAll(f.guestRepo()); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestHome, "git", "init", "-q", testSlug)
	_, err := prepareWorktree(ctx, f.target, testSlug, "cat")
	if exitCodeOf(err) != ExitUsage || !strings.Contains(err.Error(), "no commits yet") {
		t.Fatalf("empty repo: err = %v (exit %d)", err, exitCodeOf(err))
	}
	if err := os.RemoveAll(filepath.Join(f.guestRepo(), ".git")); err != nil {
		t.Fatal(err)
	}
	_, err = prepareWorktree(ctx, f.target, testSlug, "cat")
	if exitCodeOf(err) != ExitUsage || !strings.Contains(err.Error(), "not one") {
		t.Fatalf("no git: err = %v (exit %d)", err, exitCodeOf(err))
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func waitForCapture(ctx context.Context, t sshTarget, slug, window, want string) (string, error) {
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		pane, err := capturePane(ctx, t, slug, window)
		if err != nil {
			return pane, err
		}
		last = pane
		if strings.Contains(pane, want) {
			return pane, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last, exitf(ExitGeneric, "timed out waiting for %q in pane", want)
}
