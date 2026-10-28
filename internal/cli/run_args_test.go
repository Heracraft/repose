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

// run's words (I-603, I-888): one word is the project; an id or
// PROJECT:CHECKOUT names a project that exists; anything else is a
// usage error naming -p. The old prompt form, which I-603 kept for one
// release and which lasted eight, is gone: it built a machine nobody
// asked for out of whatever words were left over.
func TestRunArgs(t *testing.T) {
	const id = "01900000-0000-7000-8000-000000000001"
	const wantPFirst = "run takes one PROJECT; put the prompt after -p, quoted"
	cases := []struct {
		name              string
		args              []string
		prompt, nameFlag  string
		project           string
		wantName, wantArg string
		wantPrompt        string
		wantErr           string
	}{
		{name: "nothing"},
		{name: "a project", args: []string{"izma"}, wantName: "izma"},
		{name: "a project and its prompt", args: []string{"izma"}, prompt: "fix it", wantName: "izma", wantPrompt: "fix it"},
		{name: "the project --project names", args: []string{"izma"}, project: "izma", wantName: "izma"},
		{name: "the project --name names", args: []string{"izma"}, nameFlag: "izma", wantName: "izma"},
		{name: "an id", args: []string{id}, wantArg: id},
		{name: "a checkout", args: []string{"todo-app:api"}, wantArg: "todo-app:api"},
		{name: "a word beside --name that is not it", args: []string{"fix"}, nameFlag: "izma", wantErr: wantPFirst},
		{name: "a word beside --project that is not it", args: []string{"fix"}, project: "izma", wantErr: wantPFirst},
		{name: "one word with spaces", args: []string{"fix the tests"}, wantErr: wantPFirst},
		{name: "several words", args: []string{"fix", "the", "tests"}, wantErr: wantPFirst},
		{name: "several words beside -p", args: []string{"fix", "it"}, prompt: "x", wantErr: wantPFirst},
		// The owner's line: --temp takes no value here, so `3hr` was a
		// leftover beside the PROJECT and the pair became a prompt.
		{name: "a project and a leftover duration", args: []string{"traffic", "3hr"}, wantErr: wantPFirst},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := RunOptions{Prompt: c.prompt, Name: c.nameFlag}
			err := runArgs(&opts, c.args, c.project)
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
		})
	}
}

// Through the command tree: the usage errors come before any api call.
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
		{[]string{"sync", "a", "b"}, "takes at most one PROJECT"},
		// The old prompt form, gone since I-888.
		{[]string{"run", "fix the tests"}, "run takes one PROJECT"},
		{[]string{"run", "--agent", "claude", "fix", "the", "tests"}, "run takes one PROJECT"},
		// The owner's two lines: a duration spelled with a word is
		// refused, rather than left over to become a prompt (I-888).
		{[]string{"run", "traffic", "--temp", "3hr"}, `--temp takes a duration from 10m to 24h, such as 3h or 90m, got "3hr"`},
		{[]string{"run", "--temp", "1hr", "boilerplate"}, `--temp takes a duration from 10m to 24h, such as 3h or 90m, got "1hr"`},
	} {
		_, err := run(c.args...)
		var ue cobraUsageError
		var ee *exitError
		usage := errors.As(err, &ue) || (errors.As(err, &ee) && ee.code == ExitUsage)
		if !usage || !strings.Contains(err.Error(), c.want) {
			t.Errorf("repose %s: err = %v, want %q", strings.Join(c.args, " "), err, c.want)
		}
	}
	// Past the argument checks (they stop at the api, not logged in):
	// one project named two ways.
	for _, args := range [][]string{{"run", "izma", "--project", "izma"}} {
		_, err := run(args...)
		var ue cobraUsageError
		var ee *exitError
		if err == nil || errors.As(err, &ue) || (errors.As(err, &ee) && ee.code == ExitUsage) {
			t.Errorf("repose %s: err = %v, want past the argument checks", strings.Join(args, " "), err)
		}
	}
	// `sync --temp 2h spike`: the duration is --temp's, spike is PROJECT;
	// not logged in, so it stops at the api, past the argument check.
	if _, err := run("sync", "--temp", "2h", "spike"); err == nil || strings.Contains(err.Error(), "at most one PROJECT") {
		t.Fatalf("sync --temp 2h spike: %v", err)
	}
	// `run spike --temp 3h` still works: the duration is found wherever
	// it sits among the arguments (I-631), so only the misspellings
	// above are refused. Not logged in, so it stops at the api.
	if _, err := run("run", "spike", "--temp", "3h"); err == nil || strings.Contains(err.Error(), "takes one PROJECT") {
		t.Fatalf("run spike --temp 3h: %v", err)
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

// `repose sync NAME` from a repository that shares no commit with the
// machine's checkout, neither with an origin to compare, is refused
// before anything is written (I-631); the same repository syncs.
func TestSyncRefusesAnUnrelatedCheckout(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if _, err := f.env.Client.CreateProject(ctx, CreateProjectRequest{Name: "job", Class: "large"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "job")
	mustRun(t, filepath.Dir(repo), "git", "clone", "-q", "file://"+f.bare, filepath.Base(repo))
	mustRun(t, repo, "git", "remote", "remove", "origin")
	e, _, _ := freshEnv(f.env, repo)
	if err := runRun(ctx, e, RunOptions{Name: "job", Sync: true, NoAttach: true}, false); err != nil {
		t.Fatalf("sync job: %v", err)
	}

	other := filepath.Join(t.TempDir(), "api")
	mustRun(t, filepath.Dir(other), "git", "init", "-q", filepath.Base(other))
	mustRun(t, other, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "i")
	e2, _, _ := freshEnv(f.env, other)
	err := runRun(ctx, e2, RunOptions{Name: "job", Sync: true, NoAttach: true}, false)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "job's checkout shares no commit with this one") {
		t.Fatalf("sync job from an unrelated repository: %v", err)
	}
	if got := remoteURLOf(other, "repose"); got != "" {
		t.Fatalf("the refused sync added the repose remote: %q", got)
	}
	// An agent's commit on the machine leaves the laptop knowing none of
	// its tips; the roots still match, so the same repository syncs.
	if out, err := runSSH(ctx, f.target, checkoutVar("job", "")+`cd "$repose_co" && git -c user.name=a -c user.email=a@example.com commit -q --allow-empty -m agent && git for-each-ref --format='%(refname)'`, nil); err != nil {
		t.Fatalf("agent commit: %v %s", err, out)
	}
	if err := refuseUnrelated(ctx, f.target, repo, "job", guestProbe{tips: []string{"0123456789012345678901234567890123456789"}}, false); err != nil {
		t.Fatalf("a related checkout with unknown tips was refused: %v", err)
	}
	mustRun(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "more")
	if err := runRun(ctx, e, RunOptions{Name: "job", Sync: true, NoAttach: true}, false); err != nil {
		t.Fatalf("second sync job: %v", err)
	}
}
