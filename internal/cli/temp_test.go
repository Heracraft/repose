package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func listed(t *testing.T, e *Env) []Project {
	t.Helper()
	ps, err := e.Client.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func bySlug(ps []Project, slug string) *Project {
	for i := range ps {
		if ps[i].Slug == slug {
			return &ps[i]
		}
	}
	return nil
}

func freshEnv(e *Env, cwd string) (*Env, *discardWriter, *discardWriter) {
	out, errOut := &discardWriter{}, &discardWriter{}
	return &Env{
		Dir: e.Dir, Cfg: e.Cfg, Cache: e.Cache, Cwd: cwd, HomeDir: e.HomeDir,
		Client: e.Client, Out: out, ErrOut: errOut, TargetFor: e.TargetFor,
	}, out, errOut
}

// `repose run --temp` in a checkout: a new project named tmp-XXXX with
// no remote and an expiry, the checkout synced into it, and nothing
// written to the cache or the checkout's git remotes (I-347, I-351).
func TestRunTempCreatesWithoutRemote(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	before := time.Now()
	e, _, errOut := freshEnv(f.env, f.local)
	if err := runRun(ctx, e, RunOptions{Temp: 3 * time.Hour, NoAttach: true}, false); err != nil {
		t.Fatalf("runRun --temp: %v (stderr %q)", err, errOut.buf.String())
	}
	ps := listed(t, e)
	if len(ps) != 1 {
		t.Fatalf("projects = %+v", ps)
	}
	p := ps[0]
	if !strings.HasPrefix(p.Slug, "tmp-") || len(p.Slug) != len("tmp-")+4 {
		t.Fatalf("slug %q, want tmp- and four characters", p.Slug)
	}
	if p.RemoteURL != "" {
		t.Fatalf("remote_url %q, want none", p.RemoteURL)
	}
	if p.ExpiresAt == nil || p.ExpiresAt.Before(before.Add(3*time.Hour-time.Minute)) || p.ExpiresAt.After(time.Now().Add(3*time.Hour+time.Minute)) {
		t.Fatalf("expires_at %v, want about 3h from now", p.ExpiresAt)
	}
	if len(e.Cache.ByDir) != 0 || len(e.Cache.ByRemote) != 0 {
		t.Fatalf("cache written: %+v %+v", e.Cache.ByDir, e.Cache.ByRemote)
	}
	if loaded, err := loadProjectsCache(e.Dir); err != nil || len(loaded.ByDir) != 0 || len(loaded.ByRemote) != 0 {
		t.Fatalf("projects.json written: %+v %v", loaded, err)
	}
	if out := mustRun(t, f.local, "git", "remote"); strings.Contains(out, "repose") {
		t.Fatalf("checkout got a repose remote: %q", out)
	}
	// The checkout went up, with its whole history (no remote to clone).
	// A new machine's checkout is named after the laptop folder (I-368).
	if _, err := os.Stat(filepath.Join(f.guestHome, checkoutName(f.local), "README.md")); err != nil {
		t.Fatalf("checkout not synced into ~/%s: %v", checkoutName(f.local), err)
	}
	if !strings.Contains(errOut.buf.String(), p.Slug+" is temporary: destroyed in 3h.") {
		t.Fatalf("stderr %q lacks the time left", errOut.buf.String())
	}

	// Again: always a new machine, never the last one.
	e2, _, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e2, RunOptions{Temp: tempDefault, NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatalf("second runRun --temp: %v", err)
	}
	if ps := listed(t, e2); len(ps) != 2 {
		t.Fatalf("second --temp made %d projects in all, want 2", len(ps))
	}
	// --temp with --project is a usage error.
	if err := runRun(ctx, e2, RunOptions{Temp: tempDefault, ProjectArg: p.Slug}, false); err == nil || err.(*exitError).code != ExitUsage {
		t.Fatalf("--temp --project: %v", err)
	}
}

// Outside a git repository --temp makes an empty machine and says so;
// with --no-sync it says nothing.
func TestTempWithoutRepoSkipsSync(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	e, out, _ := freshEnv(f.env, t.TempDir())
	if err := runRun(ctx, e, RunOptions{Temp: tempDefault, Name: "spike", NoAttach: true}, false); err != nil {
		t.Fatalf("runRun --temp outside a repo: %v", err)
	}
	if !strings.Contains(out.buf.String(), "Not a git repository, so nothing was synced.") {
		t.Fatalf("stdout %q", out.buf.String())
	}
	p := bySlug(listed(t, e), "spike")
	if p == nil || p.ExpiresAt == nil {
		t.Fatalf("spike = %+v", p)
	}
	e2, out2, _ := freshEnv(f.env, t.TempDir())
	if err := runRun(ctx, e2, RunOptions{Temp: tempDefault, NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2.buf.String(), "nothing was synced") {
		t.Fatalf("--no-sync said %q", out2.buf.String())
	}
	// The name is taken now: the usual -2 retry.
	e3, _, _ := freshEnv(f.env, t.TempDir())
	if err := runRun(ctx, e3, RunOptions{Temp: tempDefault, Name: "spike", NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if bySlug(listed(t, e3), "spike-2") == nil {
		t.Fatalf("projects %+v, want spike-2", listed(t, e3))
	}
}

// Every refusal the sync makes of the checkout comes before anything is
// created: no commit, a shallow clone (I-353). A directory that is not a
// repository is no longer refused (I-358).
func TestSyncRefusalComesBeforeCreate(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()

	noCommit := t.TempDir()
	mustRun(t, noCommit, "git", "init", "-q")
	shallow := t.TempDir()
	mustRun(t, shallow, "git", "clone", "-q", "--depth", "1", "file://"+f.bare, ".")
	for _, c := range []struct {
		name, dir string
		temp      time.Duration
		want      string
	}{
		{"no commit", noCommit, 0, "no commits yet"},
		{"no commit, temporary", noCommit, tempDefault, "no commits yet"},
		{"shallow", shallow, 0, "shallow clone"},
		{"shallow, temporary", shallow, tempDefault, "shallow clone"},
	} {
		e, _, _ := freshEnv(f.env, c.dir)
		err := runRun(ctx, e, RunOptions{Name: "refused", Temp: c.temp, NoAttach: true}, false)
		ee, ok := err.(*exitError)
		if !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, c.want) {
			t.Fatalf("%s: err = %v", c.name, err)
		}
		if ps := listed(t, e); len(ps) != 0 {
			t.Fatalf("%s: created %+v before refusing", c.name, ps)
		}
	}
}

// A plain `repose run` in a directory with no git remote creates a
// project named after the directory, skips the sync outside a repository,
// and lands on the same project next time (I-358).
func TestRunWithoutRemoteNamesTheProjectAfterTheDirectory(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "job search")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e, out, _ := freshEnv(f.env, dir)
	if err := runRun(ctx, e, RunOptions{NoAttach: true}, false); err != nil {
		t.Fatalf("plain run outside a repo: %v", err)
	}
	if !strings.Contains(out.buf.String(), "Not a git repository, so nothing was synced.") {
		t.Fatalf("stdout %q", out.buf.String())
	}
	p := bySlug(listed(t, e), "job-search")
	if p == nil || p.ExpiresAt != nil || p.RemoteURL != "" {
		t.Fatalf("job-search = %+v in %+v", p, listed(t, e))
	}
	e2, _, errOut := freshEnv(e, dir)
	if err := runRun(ctx, e2, RunOptions{NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatal(err)
	}
	if ps := listed(t, e2); len(ps) != 1 {
		t.Fatalf("second run made another: %+v", ps)
	}
	if !strings.Contains(errOut.buf.String(), "Using job-search, the machine last made in this directory.") {
		t.Fatalf("stderr %q", errOut.buf.String())
	}

	// A repository with no remote: the repository root's name, from a
	// subdirectory too, and the checkout is synced.
	repo := filepath.Join(t.TempDir(), "notes")
	mustRun(t, filepath.Dir(repo), "git", "clone", "-q", "file://"+f.bare, "notes")
	mustRun(t, repo, "git", "remote", "remove", "origin")
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	e3, _, _ := freshEnv(e, sub)
	if err := runRun(ctx, e3, RunOptions{NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatal(err)
	}
	if bySlug(listed(t, e3), "notes") == nil {
		t.Fatalf("no notes: %+v", listed(t, e3))
	}
}

// `repose keep` clears the expiry; on a normal project it says so.
func TestKeepClearsExpiry(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "tmp-abcd", Class: "large", ExpiresIn: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if p.ExpiresAt == nil {
		t.Fatal("create with expires_in_s answered no expires_at")
	}
	if err := KeepCmd(ctx, e, "tmp-abcd"); err != nil {
		t.Fatal(err)
	}
	if got := e.Out.(*discardWriter).buf.String(); got != "tmp-abcd is no longer temporary.\n" {
		t.Fatalf("stdout %q", got)
	}
	got, err := e.Client.GetProject(ctx, p.ID)
	if err != nil || got.ExpiresAt != nil {
		t.Fatalf("after keep: %+v %v", got, err)
	}
	e.Out = &discardWriter{}
	if err := KeepCmd(ctx, e, "tmp-abcd"); err != nil {
		t.Fatal(err)
	}
	if got := e.Out.(*discardWriter).buf.String(); got != "tmp-abcd is not temporary.\n" {
		t.Fatalf("stdout %q", got)
	}
	// Anything but null is refused.
	var raw map[string]any
	err = e.Client.patch(ctx, "/projects/"+p.ID, map[string]any{"expires_at": "2030-01-01T00:00:00Z"}, &raw)
	if ae, ok := err.(*APIError); !ok || ae.Code != "invalid" {
		t.Fatalf("expires_at set to a time: %v", err)
	}
	// And a lifetime out of range, or with a remote, at create.
	for _, req := range []CreateProjectRequest{
		{Name: "short", Class: "large", ExpiresIn: 60},
		{Name: "long", Class: "large", ExpiresIn: 90000},
		{Name: "remote", Class: "large", ExpiresIn: 3600, RemoteURL: "github.com/a/b"},
	} {
		if _, err := e.Client.CreateProject(ctx, req); err == nil {
			t.Fatalf("create %+v accepted", req)
		}
	}
}

// Ending a temporary machine's tmux session destroys it; a session still
// there (a detach) does not (I-352).
func TestTempSessionEndDestroys(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	e, _, errOut := freshEnv(f.env, f.local)
	// Named after the fixture's session, which its guest already has.
	if err := runRun(ctx, e, RunOptions{Temp: tempDefault, Name: testSlug, NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	p := bySlug(listed(t, e), testSlug)
	if p == nil || p.ExpiresAt == nil {
		t.Fatalf("project = %+v", p)
	}
	tempSessionEnded(ctx, e, f.target, p)
	if bySlug(listed(t, e), testSlug) == nil {
		t.Fatal("destroyed while its session was still there")
	}
	if _, err := runSSH(ctx, f.target, "tmux kill-session -t "+testSlug, nil); err != nil {
		t.Fatal(err)
	}
	tempSessionEnded(ctx, e, f.target, p)
	if bySlug(listed(t, e), testSlug) != nil {
		t.Fatal("still there after its session ended")
	}
	if !strings.Contains(errOut.buf.String(), testSlug+" is temporary and its session has ended; destroying it.") {
		t.Fatalf("stderr %q", errOut.buf.String())
	}
	// A normal project is never destroyed this way.
	normal, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "normal", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	tempSessionEnded(ctx, e, f.target, normal)
	if bySlug(listed(t, e), "normal") == nil {
		t.Fatal("a normal project was destroyed")
	}
}

// The owner's bug (2026-09-29): `repose run --no-sync --name boxd` in the
// home directory attached to the project made there before under another
// name. An explicit --name picks that name's project or creates it; a
// plain run lands on the last --name project made there and says so;
// --temp always makes a new one (I-348).
func TestRunNameInHomePicksTheNamedProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	home := t.TempDir()
	run := func(opts RunOptions) (string, error) {
		t.Helper()
		e, _, errOut := freshEnv(f.env, home)
		opts.NoSync, opts.NoAttach = true, true
		err := runRun(ctx, e, opts, false)
		f.env.Cache = e.Cache
		return errOut.buf.String(), err
	}
	if _, err := run(RunOptions{Name: "issuer-migration"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(RunOptions{Name: "boxd"}); err != nil {
		t.Fatal(err)
	}
	ps := listed(t, f.env)
	if len(ps) != 2 || bySlug(ps, "boxd") == nil || bySlug(ps, "issuer-migration") == nil {
		t.Fatalf("--name boxd did not make boxd: %+v", ps)
	}
	// The same command again lands on boxd, not on a new one.
	if _, err := run(RunOptions{Name: "boxd"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(RunOptions{Name: "issuer-migration"}); err != nil {
		t.Fatal(err)
	}
	if ps := listed(t, f.env); len(ps) != 2 {
		t.Fatalf("a repeated --name created another: %+v", ps)
	}
	// Plain run: the last --name project made here, named.
	stderr, err := run(RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Using boxd, the machine last made in this directory.") {
		t.Fatalf("stderr %q", stderr)
	}
	// --temp: a new machine every time.
	for i := 0; i < 2; i++ {
		if _, err := run(RunOptions{Temp: tempDefault}); err != nil {
			t.Fatal(err)
		}
	}
	if ps := listed(t, f.env); len(ps) != 4 {
		t.Fatalf("two --temp runs: %d projects, want 4", len(ps))
	}
}

// In a checkout, --name makes a second project for the same repository
// (lifecycle.md "A second machine for the same repository"): no remote,
// reached by name, synced from the checkout; the checkout's own project
// stays what a plain run uses (I-348).
func TestRunNameInCheckoutMakesASecondProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	remote := gitRemoteOrigin(f.local)
	own := f.env.Cache.ByRemote[remote].ProjectID
	e, _, errOut := freshEnv(f.env, f.local)
	if err := runRun(ctx, e, RunOptions{Name: "proj-experiment", NoAttach: true}, false); err != nil {
		t.Fatalf("run --name proj-experiment: %v (stderr %q)", err, errOut.buf.String())
	}
	ps := listed(t, e)
	exp := bySlug(ps, "proj-experiment")
	if len(ps) != 2 || exp == nil || exp.RemoteURL != "" {
		t.Fatalf("projects = %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, checkoutName(f.local), "README.md")); err != nil {
		t.Fatalf("checkout not synced into the second project: %v", err)
	}
	if e.Cache.ByRemote[remote].ProjectID != own || len(e.Cache.ByDir) != 0 {
		t.Fatalf("cache moved: %+v %+v", e.Cache.ByRemote, e.Cache.ByDir)
	}
	// Again by name: the same second project.
	e2, _, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e2, RunOptions{Name: "proj-experiment", NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatal(err)
	}
	if len(listed(t, e2)) != 2 {
		t.Fatal("the second --name run created a third project")
	}
	// A --name that is another repository's project is refused.
	if _, err := e2.Client.CreateProject(ctx, CreateProjectRequest{Name: "other", Class: "large", RemoteURL: "github.com/someone/else"}); err != nil {
		t.Fatal(err)
	}
	err := runRun(ctx, e2, RunOptions{Name: "other", NoAttach: true}, false)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "github.com/someone/else") {
		t.Fatalf("--name of another repository's project: %v", err)
	}
}

// `repose rm` on a temporary project says no snapshot is kept, offers no
// restore, and keeps none; `repose ls` says when one goes.
func TestRmAndLsOfATemporaryProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "tmp-q7wd", Class: "large", ExpiresIn: 3 * 3600})
	if err != nil {
		t.Fatal(err)
	}
	var ls strings.Builder
	writeProjectsTable(&ls, listed(t, e))
	rows := strings.Split(strings.TrimSpace(ls.String()), "\n")
	if len(rows) != 2 || !strings.HasSuffix(rows[0], "LEFT") || !strings.HasSuffix(rows[1], " 3h") {
		t.Fatalf("ls (I-484: time left is the LEFT column, nothing under the table):\n%s", ls.String())
	}
	var asked string
	confirm := func(prompt string) (bool, error) { asked = prompt; return true, nil }
	if err := DestroyCmd(ctx, e, "tmp-q7wd", false, true, confirm); err != nil {
		t.Fatal(err)
	}
	if asked != "Destroy tmp-q7wd? It is temporary: no snapshot is kept and it cannot be restored. [y/N] " {
		t.Fatalf("asked %q", asked)
	}
	if out := e.Out.(*discardWriter).buf.String(); !strings.Contains(out, "It was temporary, so no snapshot was kept.") || strings.Contains(out, "restore") {
		t.Fatalf("stdout %q", out)
	}
	destroyed, err := e.Client.ListDestroyed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range destroyed {
		if d.ID == p.ID {
			t.Fatalf("a temporary project is listed as restorable: %+v", d)
		}
	}
}

func TestTempFlagParsing(t *testing.T) {
	for _, c := range []struct {
		raw     string
		args    []string
		want    time.Duration
		rest    int
		wantErr bool
	}{
		{"", []string{"fix it"}, 0, 1, false},
		{tempBare, nil, 24 * time.Hour, 0, false},
		{tempBare, []string{"3h"}, 3 * time.Hour, 0, false},
		{tempBare, []string{"90m", "fix", "it"}, 90 * time.Minute, 2, false},
		{tempBare, []string{"fix", "it"}, 24 * time.Hour, 2, false},
		{"1h30m", []string{"fix"}, 90 * time.Minute, 1, false},
		{"5m", nil, 0, 0, true},
		{"25h", nil, 0, 0, true},
		{"soon", nil, 0, 0, true},
		{tempBare, []string{"48h"}, 0, 0, true},
	} {
		d, rest, err := resolveTempFlag(c.raw, c.args)
		if (err != nil) != c.wantErr || (!c.wantErr && (d != c.want || len(rest) != c.rest)) {
			t.Errorf("resolveTempFlag(%q, %q) = %v, %q, %v", c.raw, c.args, d, rest, err)
		}
	}
	for d, want := range map[time.Duration]string{
		5 * time.Hour: "5h", 4*time.Hour + time.Minute: "5h", 59 * time.Minute: "59m", 30 * time.Second: "1m", 24 * time.Hour: "24h",
	} {
		if got := timeLeft(d); got != want {
			t.Errorf("timeLeft(%s) = %q, want %q", d, got, want)
		}
	}
}
