package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// gitInitAnswers stands in for the terminal: canAsk says whether there is
// one, answer is the reply, and asked counts the questions.
func gitInitAnswers(t *testing.T, canAsk, answer bool) *int {
	t.Helper()
	asked := 0
	oldCan, oldAsk := canAskGitInit, askGitInit
	canAskGitInit = func() bool { return canAsk }
	askGitInit = func(_ context.Context, prompt string) (bool, error) {
		if !strings.HasSuffix(prompt, " isn't a git repository. Make it one and sync it? [Y/n] ") {
			t.Errorf("prompt %q", prompt)
		}
		asked++
		return answer, nil
	}
	t.Cleanup(func() { canAskGitInit, askGitInit = oldCan, oldAsk })
	return &asked
}

// testGitIdentity gives git a name and an email for the commit, and nothing
// else from the machine's own configuration.
func testGitIdentity(t *testing.T, extra string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Test\n\temail = test@example.com\n"+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A yes makes the folder a repository with one commit (DECISIONS
// I-1065): its files and a starter .gitignore, never its .env or a
// dependency tree.
func TestOfferGitInitMakesARepository(t *testing.T) {
	testGitIdentity(t, "")
	asked := gitInitAnswers(t, true, true)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"main.py": "print(1)\n", "lib/util.py": "x = 1\n", ".env": "KEY=secret\n", "node_modules/pkg/index.js": "x\n"})
	outcome, done, err := offerGitInit(context.Background(), dir)
	if err != nil || outcome != gitInitMade || *asked != 1 {
		t.Fatalf("outcome %v, err %v, asked %d", outcome, err, *asked)
	}
	tracked, _ := gitCmd(dir, "ls-files")
	if tracked != ".gitignore\nlib/util.py\nmain.py" || done.files != 3 {
		t.Fatalf("tracked (%d):\n%s", done.files, tracked)
	}
	if n, _ := gitCmd(dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("%s commits", n)
	}
	if err := syncPrecheck(dir, true); err != nil {
		t.Fatalf("the sync still refuses: %v", err)
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, want := range []string{".env\n", ".env.*\n", "node_modules/\n", ".venv/\n"} {
		if !strings.Contains(string(ignore), want) {
			t.Errorf(".gitignore lacks %q:\n%s", want, ignore)
		}
	}
}

// A .gitignore the folder already has is kept as it is.
func TestOfferGitInitKeepsAGitignore(t *testing.T) {
	testGitIdentity(t, "")
	gitInitAnswers(t, true, true)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{".gitignore": "out/\n", "a.txt": "a\n", "out/b.txt": "b\n"})
	if outcome, _, err := offerGitInit(context.Background(), dir); err != nil || outcome != gitInitMade {
		t.Fatalf("outcome %v, err %v", outcome, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); string(b) != "out/\n" {
		t.Fatalf(".gitignore became %q", b)
	}
	if tracked, _ := gitCmd(dir, "ls-files"); tracked != ".gitignore\na.txt" {
		t.Fatalf("tracked:\n%s", tracked)
	}
}

// A no, no terminal, or a folder past what one sync sends leaves the
// folder alone; the last two are never asked.
func TestOfferGitInitLeavesTheFolderAlone(t *testing.T) {
	testGitIdentity(t, "")
	for _, c := range []struct {
		name        string
		canAsk, yes bool
		big         bool
		want        gitInitOutcome
		wantAsked   int
	}{
		{name: "no", canAsk: true, yes: false, want: gitInitDeclined, wantAsked: 1},
		{name: "no terminal", canAsk: false, yes: true, want: gitInitNotAsked},
		{name: "too big", canAsk: true, yes: true, big: true, want: gitInitTooBig},
	} {
		t.Run(c.name, func(t *testing.T) {
			asked := gitInitAnswers(t, c.canAsk, c.yes)
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{"a.txt": "a\n"})
			if c.big {
				// Sparse: the size counts, the disk does not.
				f, err := os.Create(filepath.Join(dir, "data.bin"))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(maxUntrackedBytes + 1); err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
			}
			outcome, _, err := offerGitInit(context.Background(), dir)
			if err != nil || outcome != c.want || *asked != c.wantAsked {
				t.Fatalf("outcome %v (want %v), err %v, asked %d", outcome, c.want, err, *asked)
			}
			entries, _ := os.ReadDir(dir)
			for _, en := range entries {
				if en.Name() == ".git" || en.Name() == ".gitignore" {
					t.Fatalf("%s left in the folder", en.Name())
				}
			}
		})
	}
}

// The big file inside a dependency tree does not count: the starter
// .gitignore keeps it out of the commit.
func TestScanFolderSkipsWhatIsIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "node_modules", "big"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(maxUntrackedBytes + 1)
	_ = f.Close()
	if over, _ := scanFolder(dir, maxUntrackedBytes, maxInitFiles); over {
		t.Fatal("node_modules counted")
	}
	// Past the file count is too big as well.
	writeFiles(t, dir, map[string]string{"a": "", "b": "", "c": ""})
	if over, _ := scanFolder(dir, maxUntrackedBytes, 2); !over {
		t.Fatal("three files passed a limit of two")
	}
}

// A commit git refuses (here, signing that cannot sign) leaves the folder
// as it was and says so.
func TestOfferGitInitUndoesAFailedCommit(t *testing.T) {
	testGitIdentity(t, "[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = false\n")
	gitInitAnswers(t, true, true)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.txt": "a\n"})
	_, _, err := offerGitInit(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "git commit failed: ") {
		t.Fatalf("err %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Fatalf("folder holds %v", entries)
	}
}

// End to end: `repose run` in a plain folder, answered yes, syncs the
// folder's files to the machine's checkout; `repose sync` answered no
// exits 1 and creates nothing.
func TestRunInAPlainFolderOffersARepository(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	testGitIdentity(t, "")
	ctx := context.Background()

	gitInitAnswers(t, true, false)
	no := filepath.Join(t.TempDir(), "declined")
	writeFiles(t, no, map[string]string{"a.txt": "a\n"})
	e, _, _ := freshEnv(f.env, no)
	err := runRun(ctx, e, RunOptions{Sync: true, NoAttach: true, Name: "declined"}, false)
	wantExit(t, "sync", err, ExitGeneric, "Nothing was synced.")
	if ps := listed(t, e); len(ps) != 0 {
		t.Fatalf("created %+v", ps)
	}

	gitInitAnswers(t, true, true)
	dir := filepath.Join(t.TempDir(), "plainapp")
	writeFiles(t, dir, map[string]string{"main.py": "print('hi')\n", ".env": "KEY=secret\n"})
	e, out, _ := freshEnv(f.env, dir)
	if err := runRun(ctx, e, RunOptions{Name: "plainapp", NoAttach: true}, false); err != nil {
		t.Fatalf("runRun: %v", err)
	}
	if !strings.Contains(out.buf.String(), "Committed 2 files to a new git repository.\n") || strings.Contains(out.buf.String(), "synced") {
		t.Fatalf("stdout:\n%s", out.buf.String())
	}
	got, err := runSSH(ctx, f.target, "cat ~/plainapp/main.py && git -C ~/plainapp ls-files", nil)
	if err != nil || string(got) != "print('hi')\n.gitignore\nmain.py\n" {
		t.Fatalf("the machine's checkout: %q (%v)", got, err)
	}
}

// Keys and credentials stay out of the first commit, as .env does; an
// .env.example, which projects commit, goes in.
func TestOfferGitInitLeavesSecretsOut(t *testing.T) {
	testGitIdentity(t, "")
	gitInitAnswers(t, true, true)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"app.go": "package main\n", ".env.local": "K=1\n", ".env.example": "K=\n", "id_rsa": "k\n", "id_ed25519.pub": "k\n", "tls/server.pem": "k\n", "credentials.json": "{}\n", ".netrc": "m\n"})
	if outcome, _, err := offerGitInit(context.Background(), dir); err != nil || outcome != gitInitMade {
		t.Fatalf("outcome %v, err %v", outcome, err)
	}
	if tracked, _ := gitCmd(dir, "ls-files"); tracked != ".env.example\n.gitignore\napp.go" {
		t.Fatalf("tracked:\n%s", tracked)
	}
}

// A repository inside the folder is left out by name: committed, it
// would be a commit pointer the sync cannot fill.
func TestOfferGitInitLeavesNestedRepositoriesOut(t *testing.T) {
	testGitIdentity(t, "")
	gitInitAnswers(t, true, true)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"main.py": "x\n", "vendor/lib/a.py": "y\n"})
	if _, err := gitCmd(filepath.Join(dir, "vendor", "lib"), "init", "-q"); err != nil {
		t.Fatal(err)
	}
	outcome, done, err := offerGitInit(context.Background(), dir)
	if err != nil || outcome != gitInitMade {
		t.Fatalf("outcome %v, err %v", outcome, err)
	}
	if tracked, _ := gitCmd(dir, "ls-files", "-s"); strings.Contains(tracked, "160000") || strings.Contains(tracked, "vendor") {
		t.Fatalf("the nested repository was committed:\n%s", tracked)
	}
	if l := done.lines(); len(l) != 2 || l[1] != "Left out vendor/lib: it is a git repository of its own." {
		t.Fatalf("lines %q", l)
	}
}

// Without a name and email for git, the run says how to set them, and
// the folder is left as it was.
func TestOfferGitInitWithoutAnIdentity(t *testing.T) {
	testGitIdentity(t, "")
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	gitInitAnswers(t, true, true)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.txt": "a\n"})
	_, _, err := offerGitInit(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), `Git has no name and email to commit with. Set them with git config --global user.name "Your Name"`) {
		t.Fatalf("err %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("folder holds %v", entries)
	}
}

// --temp never asks: a scratch machine from a scratch folder.
func TestTempNeverOffersARepository(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	asked := gitInitAnswers(t, true, true)
	e, out, _ := freshEnv(f.env, t.TempDir())
	if err := runRun(context.Background(), e, RunOptions{Temp: tempDefault, Name: "scratch", NoAttach: true}, false); err != nil {
		t.Fatalf("runRun --temp: %v", err)
	}
	if *asked != 0 || !strings.Contains(out.buf.String(), "Not a git repository, so nothing was synced.") {
		t.Fatalf("asked %d, stdout %q", *asked, out.buf.String())
	}
}
