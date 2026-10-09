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

// run's words (I-603): one word is the project; for a release, several
// words or one with a space are the old prompt, with a line; an id or
// PROJECT:CHECKOUT names a project that exists.
func TestRunArgs(t *testing.T) {
	const id = "01900000-0000-7000-8000-000000000001"
	cases := []struct {
		name               string
		args               []string
		prompt, nameFlag   string
		named              bool
		wantName, wantArg  string
		wantPrompt, wantLn string
		wantErr            string
	}{
		{name: "nothing"},
		{name: "a project", args: []string{"izma"}, wantName: "izma"},
		{name: "a project and its prompt", args: []string{"izma"}, prompt: "fix it", wantName: "izma", wantPrompt: "fix it"},
		{name: "a word beside --name is the old prompt", args: []string{"fix"}, nameFlag: "izma", wantName: "izma", wantPrompt: "fix", wantLn: "repose run -p 'fix'"},
		{name: "a word beside --project is the old prompt", args: []string{"fix"}, named: true, wantPrompt: "fix", wantLn: "repose run -p 'fix'"},
		{name: "an id", args: []string{id}, wantArg: id},
		{name: "a checkout", args: []string{"todo-app:api"}, wantArg: "todo-app:api"},
		{name: "old prompt, quoted", args: []string{"fix the tests"}, wantPrompt: "fix the tests", wantLn: "The prompt goes after -p: `repose run -p 'fix the tests'`. This form stops working in the next release.\n"},
		{name: "old prompt, words", args: []string{"fix", "the", "tests"}, wantPrompt: "fix the tests", wantLn: "repose run -p 'fix the tests'"},
		{name: "old prompt beside -p", args: []string{"fix", "it"}, prompt: "x", wantErr: "run takes one PROJECT; put the prompt after -p, quoted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := RunOptions{Prompt: c.prompt, Name: c.nameFlag}
			var errOut strings.Builder
			err := runArgs(&opts, c.args, c.named, &errOut)
			if c.wantErr != "" {
				var ue cobraUsageError
				if !errors.As(err, &ue) || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want a usage error with %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if opts.Name != c.wantName || opts.ProjectArg != c.wantArg || opts.Prompt != c.wantPrompt {
				t.Fatalf("name %q arg %q prompt %q", opts.Name, opts.ProjectArg, opts.Prompt)
			}
			if c.wantLn == "" && errOut.Len() != 0 || !strings.Contains(errOut.String(), c.wantLn) {
				t.Fatalf("stderr %q, want %q", errOut.String(), c.wantLn)
			}
		})
	}
}

// Through the command tree: the usage errors come before any api call,
// and the old prompt form's line is printed before the run goes on.
func TestRunCommandArgs(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("REPOSE_API_URL", fake.URL()+"/v1")
	t.Setenv("REPOSE_PROJECT", "")
	if err := os.MkdirAll(filepath.Join(cfg, "repose"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		root := newRootCmd("test")
		root.SetArgs(args)
		var errOut strings.Builder
		root.SetOut(&strings.Builder{})
		root.SetErr(&errOut)
		err := root.ExecuteContext(context.Background())
		return errOut.String(), err
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "--worktree"}, "--worktree starts an agent in its own worktree and needs -p PROMPT"},
		{[]string{"sync", "izma", "--project", "other"}, "izma and --project other name two projects"},
		{[]string{"sync", "izma", "--name", "other"}, "izma and --name other name two projects"},
		{[]string{"run", "-p", "x", "fix", "it"}, "run takes one PROJECT"},
		{[]string{"run", "izma", "--agent", "codex"}, "--agent picks the agent for -p PROMPT"},
		{[]string{"sync", "a", "b"}, "takes at most one PROJECT"},
	} {
		_, err := run(c.args...)
		var ue cobraUsageError
		var ee *exitError
		usage := errors.As(err, &ue) || (errors.As(err, &ee) && ee.code == ExitUsage)
		if !usage || !strings.Contains(err.Error(), c.want) {
			t.Errorf("repose %s: err = %v, want %q", strings.Join(c.args, " "), err, c.want)
		}
	}
	// `sync --temp 2h spike`: the duration is --temp's, spike is PROJECT;
	// not logged in, so it stops at the api, past the argument check.
	if _, err := run("sync", "--temp", "2h", "spike"); err == nil || strings.Contains(err.Error(), "at most one PROJECT") {
		t.Fatalf("sync --temp 2h spike: %v", err)
	}
	// Not logged in, so the run stops at the api; the line came first.
	errOut, err := run("run", "fix the tests")
	if err == nil {
		t.Fatal("run with no credentials went through")
	}
	if !strings.Contains(errOut, "The prompt goes after -p: `repose run -p 'fix the tests'`.") {
		t.Fatalf("stderr %q", errOut)
	}
}

// `repose run --temp spike` in a checkout with no remote never links the
// checkout to the temporary machine (I-351, I-603).
func TestTempByNameLinksNothing(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "notes")
	mustRun(t, filepath.Dir(repo), "git", "clone", "-q", "file://"+f.bare, "notes")
	mustRun(t, repo, "git", "remote", "remove", "origin")
	e, _, _ := freshEnv(f.env, repo)
	if err := runRun(ctx, e, RunOptions{Name: "spike", Temp: tempDefault, NoAttach: true}, false); err != nil {
		t.Fatalf("run --temp spike: %v", err)
	}
	disk, err := loadProjectsCache(f.env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.ByDir) != 0 {
		t.Fatalf("by_dir %v, want nothing for a temporary machine", disk.ByDir)
	}
}

// `repose sync NAME` into a project with no remote, from a checkout with
// no remote and no entry, links the checkout to it, as `repose sync
// --project NAME` did (I-575, I-603).
func TestSyncNameLinksAnUnlinkedCheckout(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	job, err := f.env.Client.CreateProject(ctx, CreateProjectRequest{Name: "job", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "job search")
	mustRun(t, filepath.Dir(repo), "git", "clone", "-q", "file://"+f.bare, filepath.Base(repo))
	mustRun(t, repo, "git", "remote", "remove", "origin")
	e, _, _ := freshEnv(f.env, repo)
	if err := runRun(ctx, e, RunOptions{Name: "job", Sync: true, NoAttach: true}, false); err != nil {
		t.Fatalf("sync job: %v", err)
	}
	if ps := listed(t, e); len(ps) != 1 {
		t.Fatalf("projects %+v, want job alone", ps)
	}
	disk, err := loadProjectsCache(f.env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if disk.ByDir[gitRepoRoot(repo)] != job.ID {
		t.Fatalf("by_dir %v, want the checkout linked to job", disk.ByDir)
	}
}
