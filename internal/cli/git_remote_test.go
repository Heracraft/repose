package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// These tests cover the `repose` git remote (DECISIONS I-272).

func newCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "-q", "-b", "main", ".")
	mustRun(t, dir, "git", "config", "user.email", "dev@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Dev")
	mustRun(t, dir, "git", "commit", "-q", "--allow-empty", "-m", "initial")
	return dir
}

// gitFails runs git in dir, wants it to fail, and returns its output.
func gitFails(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("git %s succeeded, want a failure:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

func TestReposeRemoteAddedOnce(t *testing.T) {
	dir := newCheckout(t)
	note, err := ensureReposeRemote(dir, "todo-app", "todo-app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "Added the git remote repose") {
		t.Fatalf("first call note = %q", note)
	}
	if got := mustRun(t, dir, "git", "config", "--get", "remote.repose.url"); got != "todo-app.repose:~/todo-app" {
		t.Fatalf("url = %q", got)
	}
	if got := mustRun(t, dir, "git", "config", "--get", "remote.repose.fetch"); got != "+refs/heads/*:refs/remotes/repose/*" {
		t.Fatalf("fetch refspec = %q, want git's default", got)
	}
	if got := mustRun(t, dir, "git", "config", "--get", "remote.repose.skipFetchAll"); got != "true" {
		t.Fatalf("skipFetchAll = %q", got)
	}
	before := mustRun(t, dir, "git", "config", "--list", "--local")

	// Again: nothing to say and nothing changed.
	note, err = ensureReposeRemote(dir, "todo-app", "todo-app")
	if err != nil || note != "" {
		t.Fatalf("second call = %q, %v; want silence", note, err)
	}
	if after := mustRun(t, dir, "git", "config", "--list", "--local"); after != before {
		t.Fatalf("second call changed .git/config:\n%s\nwas:\n%s", after, before)
	}
	if got := mustRun(t, dir, "git", "remote"); got != "repose" {
		t.Fatalf("remotes = %q", got)
	}

	// Pushing to it fails, and git's message says why.
	out := gitFails(t, dir, "push", "repose", "HEAD")
	if !strings.Contains(out, "this remote is fetch-only; repose sync sends your work to the machine") {
		t.Fatalf("push output:\n%s", out)
	}

	// The CLI's own remote for another slug (a project recreated under
	// a new name) is moved, silently.
	note, err = ensureReposeRemote(dir, "todo-app-2", "todo-app-2")
	if err != nil || note != "" {
		t.Fatalf("retarget = %q, %v", note, err)
	}
	if got := mustRun(t, dir, "git", "config", "--get", "remote.repose.url"); got != "todo-app-2.repose:~/todo-app-2" {
		t.Fatalf("url after retarget = %q", got)
	}
}

func TestReposeRemoteLeavesAForeignOneAlone(t *testing.T) {
	dir := newCheckout(t)
	mustRun(t, dir, "git", "remote", "add", "repose", "git@github.com:someone/repose.git")
	note, err := ensureReposeRemote(dir, "todo-app", "todo-app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "left it alone") && !strings.Contains(note, "left alone") {
		t.Fatalf("note = %q", note)
	}
	if !strings.Contains(note, "git remote add NAME todo-app.repose:~/todo-app") {
		t.Fatalf("note does not say how to add one: %q", note)
	}
	note, err = ensureReposeRemote(dir, "todo-app", "todo-app")
	if err != nil || note != "" {
		t.Fatalf("second call = %q, %v; the note is said once", note, err)
	}
	if got := mustRun(t, dir, "git", "config", "--get", "remote.repose.url"); got != "git@github.com:someone/repose.git" {
		t.Fatalf("url = %q, want the user's", got)
	}
	gitFails(t, dir, "config", "--get", "remote.repose.pushurl")
	if forgetReposeRemote(dir, "todo-app") {
		t.Fatal("forgetReposeRemote removed the user's remote")
	}
	if got := remoteURLOf(dir, "repose"); got != "git@github.com:someone/repose.git" {
		t.Fatalf("url after forget = %q", got)
	}
}

func TestIsReposeRemoteURL(t *testing.T) {
	for u, want := range map[string]bool{
		"todo-app.repose:~/todo-app":       true,
		"a.repose:~/a":                     true,
		"todo-app.repose:~/other":          true, // a checkout named after the laptop folder (I-368)
		"todo-app.repose:~/job-search.v2":  true,
		"todo-app.repose:~/.ssh":           false,
		"git@github.com:a/b.git":           false,
		"ssh://todo-app.repose/~/todo-app": false,
		"todo-app.repose:~/todo-app/.git":  false,
		"Todo-App.repose:~/Todo-App":       false,
		"todo-app.repose.evil:~/todo-app":  false,
		"x.todo-app.repose:~/x.todo-app":   false,
		"todo-app.repose:~/todo-app extra": false,
	} {
		if got := isReposeRemoteURL(u); got != want {
			t.Errorf("isReposeRemoteURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestCheckoutOwnsProject(t *testing.T) {
	dir := newCheckout(t)
	mustRun(t, dir, "git", "remote", "add", "origin", "git@github.com:you/todo-app.git")
	e := &Env{Cache: ProjectsCache{ByDir: map[string]string{}, ByRemote: map[string]CachedProject{}}}
	if !e.checkoutOwnsProject(dir, &Project{ID: "p1", Slug: "todo-app", RemoteURL: "github.com/you/todo-app"}) {
		t.Error("the checkout's own project is not recognised")
	}
	// A fork copy (no remote) or another repository's project: no.
	if e.checkoutOwnsProject(dir, &Project{ID: "p2", Slug: "todo-app-fork-1"}) {
		t.Error("a fork copy with no remote was taken as the checkout's")
	}
	if e.checkoutOwnsProject(dir, &Project{ID: "p3", Slug: "other", RemoteURL: "github.com/you/other"}) {
		t.Error("another repository's project was taken as the checkout's")
	}
	if e.checkoutOwnsProject("", &Project{ID: "p1", Slug: "todo-app", RemoteURL: "github.com/you/todo-app"}) {
		t.Error("no checkout, yet a project owns it")
	}

	// No remote: only the project `repose run --name` made here.
	bare := newCheckout(t)
	root := gitRepoRoot(bare)
	e.Cache.ByDir[root] = "p4"
	if !e.checkoutOwnsProject(root, &Project{ID: "p4", Slug: "scratch"}) {
		t.Error("a --name project is not recognised in its directory")
	}
	if e.checkoutOwnsProject(root, &Project{ID: "p5", Slug: "scratch-fork-1"}) {
		t.Error("another no-remote project was taken as the directory's")
	}
}

// reposeSSHConfig writes an ssh config in which <slug>.repose reaches the
// fixture's fake guest, the way ~/.ssh/repose/config makes it reach the
// gateway, and points git at it.
func reposeSSHConfig(t *testing.T, f *syncFixture) {
	t.Helper()
	var port, key, userHost string
	args := f.target.Args
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p":
			port = args[i+1]
			i++
		case "-i":
			key = args[i+1]
			i++
		case "-o":
			i++
		default:
			userHost = args[i]
		}
	}
	user, host, _ := strings.Cut(userHost, "@")
	cfg := filepath.Join(t.TempDir(), "config")
	body := "Host " + testSlug + ".repose\n" +
		"  HostName " + host + "\n  Port " + port + "\n  User " + user + "\n" +
		"  IdentityFile " + key + "\n  IdentitiesOnly yes\n  BatchMode yes\n" +
		"  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  LogLevel ERROR\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", "ssh -F "+cfg)
}

// TestFetchReposeBringsTheMachinesCommits is the whole round trip over a
// real SSH session: commits an agent made in the machine's checkout, on
// its branch and on a --worktree branch, arrive with `git fetch repose`
// using the URL the CLI wrote, and merge, pull and cherry-pick work on
// them. Removing the machine's remote keeps what was fetched.
func TestFetchReposeBringsTheMachinesCommits(t *testing.T) {
	f := newSyncFixture(t)
	reposeSSHConfig(t, f)
	guest := f.guestRepo()

	if err := os.WriteFile(filepath.Join(guest, "agent.txt"), []byte("from the agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, guest, "git", "add", "agent.txt")
	mustRun(t, guest, "git", "commit", "-q", "-m", "agent: add agent.txt")
	agentHead := mustRun(t, guest, "git", "rev-parse", "HEAD")

	// A --worktree branch, next to the checkout (I-253, I-342).
	wt := filepath.Join(f.guestHome, testSlug+"-worktree-1")
	mustRun(t, guest, "git", "worktree", "add", "-q", "-b", "worktree-1", wt, "HEAD")
	if err := os.WriteFile(filepath.Join(wt, "other.txt"), []byte("the other approach\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, wt, "git", "add", "other.txt")
	mustRun(t, wt, "git", "commit", "-q", "-m", "worktree-1: the other approach")
	wtHead := mustRun(t, wt, "git", "rev-parse", "HEAD")

	if _, err := ensureReposeRemote(f.local, testSlug, testSlug); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "fetch", "-q", "repose")
	if got := mustRun(t, f.local, "git", "rev-parse", "repose/main"); got != agentHead {
		t.Fatalf("repose/main = %s, want the agent's %s", got, agentHead)
	}
	// The machine's branch worktree-1 is repose/worktree-1
	// here: the remote's name, then the branch's (git's default refspec).
	if got := mustRun(t, f.local, "git", "rev-parse", "repose/worktree-1"); got != wtHead {
		t.Fatalf("repose/worktree-1 = %s, want %s", got, wtHead)
	}
	if got := mustRun(t, f.local, "git", "log", "-1", "--format=%s", "repose/main"); got != "agent: add agent.txt" {
		t.Fatalf("git log repose/main = %q", got)
	}
	if diff := mustRun(t, f.local, "git", "diff", "--stat", "main", "repose/main"); !strings.Contains(diff, "agent.txt") {
		t.Fatalf("git diff main repose/main:\n%s", diff)
	}

	mustRun(t, f.local, "git", "merge", "-q", "--ff-only", "repose/main")
	if b, err := os.ReadFile(filepath.Join(f.local, "agent.txt")); err != nil || string(b) != "from the agent\n" {
		t.Fatalf("agent.txt after the merge = %q, %v", b, err)
	}
	mustRun(t, f.local, "git", "cherry-pick", "repose/worktree-1")
	if _, err := os.Stat(filepath.Join(f.local, "other.txt")); err != nil {
		t.Fatalf("cherry-pick did not bring other.txt: %v", err)
	}

	// git pull names the machine's branch as the machine has it.
	mustRun(t, guest, "git", "commit", "-q", "--allow-empty", "-m", "agent: one more")
	mustRun(t, f.local, "git", "pull", "-q", "--no-rebase", "--no-edit", "repose", "main")
	if got := mustRun(t, f.local, "git", "log", "-1", "--format=%s", "HEAD^2"); got != "agent: one more" {
		t.Fatalf("second parent of the pull's merge = %q", got)
	}

	// `git fetch --all` leaves the machine out (it may be stopped).
	mustRun(t, guest, "git", "commit", "-q", "--allow-empty", "-m", "agent: not fetched by --all")
	before := mustRun(t, f.local, "git", "rev-parse", "repose/main")
	mustRun(t, f.local, "git", "fetch", "-q", "--all")
	if got := mustRun(t, f.local, "git", "rev-parse", "repose/main"); got != before {
		t.Fatal("git fetch --all fetched from the machine")
	}

	// The machine goes away: the remote goes, what was fetched stays.
	if !forgetReposeRemote(f.local, testSlug) {
		t.Fatal("forgetReposeRemote did not remove the CLI's remote")
	}
	if got := mustRun(t, f.local, "git", "remote"); got != "origin" {
		t.Fatalf("remotes after forget = %q", got)
	}
	if got := mustRun(t, f.local, "git", "rev-parse", "repose/worktree-1"); got != wtHead {
		t.Fatalf("fetched branch lost with the remote: %s", got)
	}
}

// TestRunAddsTheReposeRemote is the wiring: `repose run` in the checkout
// adds the remote and says so once, a second run says nothing, and
// removing the project removes it.
func TestRunAddsTheReposeRemote(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()

	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	if got := remoteURLOf(f.local, "repose"); got != testSlug+".repose:~/"+testSlug {
		t.Fatalf("remote.repose.url after run = %q", got)
	}
	errOut := f.env.ErrOut.(*discardWriter).buf.String()
	if !strings.Contains(errOut, "Added the git remote repose: `git fetch repose` brings the machine's commits to this checkout.") {
		t.Fatalf("run did not say it added the remote:\n%s", errOut)
	}

	second := &discardWriter{}
	f.env.ErrOut = second
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatalf("second runRun: %v", err)
	}
	if strings.Contains(second.buf.String(), "git remote") {
		t.Fatalf("second run mentioned the remote again:\n%s", second.buf.String())
	}

	out := &discardWriter{}
	f.env.ErrOut = out
	if err := DestroyCmd(ctx, f.env, testSlug, true, false, nil); err != nil {
		t.Fatalf("DestroyCmd: %v", err)
	}
	if got := remoteURLOf(f.local, "repose"); got != "" {
		t.Fatalf("remote still there after the project was removed: %q", got)
	}
	if !strings.Contains(out.buf.String(), "Removed the git remote repose") {
		t.Fatalf("destroy did not say it removed the remote:\n%s", out.buf.String())
	}
}

// `repose rm` of several fork copies removes each copy's remote: the
// destroys run at once, and git refuses a second writer of .git/config,
// so the edits take turns (I-631).
func TestRmOfSeveralForksRemovesEachRemote(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("runRun: %v", err)
	}
	var forks []string
	for i := 1; i <= 4; i++ {
		slug := fmt.Sprintf("%s-fork-%d", testSlug, i)
		if _, err := fake.CreateProject(slug, "small"); err != nil {
			t.Fatal(err)
		}
		forks = append(forks, slug)
	}
	if got := addForkRemotes(f.local, testSlug, forks); len(got) != len(forks) {
		t.Fatalf("fork remotes added: %v", got)
	}
	out := &discardWriter{}
	f.env.ErrOut = out
	if err := DestroyProjectsCmd(ctx, f.env, forks, true, false, nil); err != nil {
		t.Fatalf("rm: %v", err)
	}
	for _, slug := range forks {
		if got := remoteURLOf(f.local, slug); got != "" {
			t.Errorf("remote %s still there: %q", slug, got)
		}
		if !strings.Contains(out.buf.String(), "Removed the git remote "+slug+".\n") {
			t.Errorf("rm did not say it removed %s:\n%s", slug, out.buf.String())
		}
	}
	if remoteURLOf(f.local, "repose") == "" {
		t.Error("rm of the forks took the source's repose remote")
	}
}
