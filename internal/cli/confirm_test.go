package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// A question reads a line; EOF is no and Ctrl-C ends it at once, where it
// used to wait for a second Ctrl-C (DECISIONS I-614, review A1, A5).
func TestAskYesNoFrom(t *testing.T) {
	for _, tc := range []struct {
		in         string
		defaultYes bool
		want       bool
	}{
		{"y\n", false, true},
		{"YES\n", false, true},
		{"n\n", false, false},
		{"\n", false, false},
		{"\n", true, true},
		{"", false, false}, // EOF: /dev/null, a closed pipe
		{"", true, false},  // EOF is never a yes
		{"sure\n", true, false},
	} {
		var w bytes.Buffer
		got, err := askYesNoFrom(context.Background(), strings.NewReader(tc.in), &w, "Go? ", tc.defaultYes)
		if err != nil || got != tc.want {
			t.Errorf("%q (default yes %v): %v %v, want %v", tc.in, tc.defaultYes, got, err, tc.want)
		}
		if !strings.HasPrefix(w.String(), "Go? ") {
			t.Errorf("%q: prompt %q", tc.in, w.String())
		}
	}

	r, pw := io.Pipe() // a terminal nobody types into
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var w bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := askYesNoFrom(ctx, r, &w, "Destroy x? ", false)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err != errPromptInterrupted {
			t.Fatalf("Ctrl-C: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl-C did not end the question")
	}
	if w.String() != "Destroy x? \n" {
		t.Fatalf("Ctrl-C wrote %q", w.String())
	}
}

// /dev/null is a character device but no terminal: nobody can answer
// there, so the question is never asked (A1).
func TestCanPromptIsATerminalCheck(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if canPrompt(f) {
		t.Fatal("canPrompt(/dev/null)")
	}
	t.Setenv(envNoSpinner, "1")
	if canDrawSpinner(f) || canPrompt(nil) {
		t.Fatal("spinner on /dev/null, or a prompt on nil")
	}
}

func TestConfirmOr(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
		err  error
		code int
	}{
		{"yes", true, nil, ExitOK},
		{"no", false, nil, ExitGeneric},
		{"ctrl-c", false, errPromptInterrupted, ExitInterrupted},
		{"no terminal", false, exitf(ExitUsage, "No terminal"), ExitUsage},
	} {
		err := confirmOr(func(string) (bool, error) { return tc.ok, tc.err }, "q", "Nothing done.")
		if tc.code == ExitOK {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if exitCode(err) != tc.code {
			t.Errorf("%s: %v, want exit %d", tc.name, err, tc.code)
		}
		if tc.code == ExitGeneric || tc.code == ExitInterrupted {
			if err.Error() != "Nothing done." {
				t.Errorf("%s: %q", tc.name, err.Error())
			}
		}
	}
}

func runningProject(t *testing.T, fake *fakeapi.Fake, e *Env, name string) *Project {
	t.Helper()
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: name, Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if err := StartCmd(ctx, e, p.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

func stateOf(t *testing.T, e *Env, id string) string {
	t.Helper()
	p, err := e.Client.GetProject(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p.State
}

// A stop that would end a busy agent asks first, and refuses without a
// terminal unless --yes (DECISIONS I-614, revising I-500; review A3).
func TestStopAsksBeforeEndingABusyAgent(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "api")
	fake.SetAgents(p.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude", State: "working"}, {Agent: "codex", Window: "codex-2", State: "needs_input"}})

	var asked string
	no := func(q string) (bool, error) { asked = q; return false, nil }
	err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api"}, Snapshot: true, Confirm: no})
	if exitCode(err) != ExitGeneric || err.Error() != "Nothing stopped." {
		t.Fatalf("declined: %v", err)
	}
	if asked != "api has claude (working) and codex-2 (needs input). Stopping ends them. Stop api? [y/N] " {
		t.Fatalf("asked %q", asked)
	}
	if stateOf(t, e, p.ID) != "running" {
		t.Fatal("a declined stop stopped")
	}

	err = StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api"}, Snapshot: true})
	if exitCode(err) != ExitUsage || err.Error() != "api has claude (working) and codex-2 (needs input). No terminal to confirm stopping on; pass --yes." {
		t.Fatalf("no terminal: %v", err)
	}

	var out bytes.Buffer
	e.Out = &out
	yes := func(string) (bool, error) { return true, nil }
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api"}, Snapshot: true, Confirm: yes}); err != nil {
		t.Fatal(err)
	}
	// The question named the agents; the line after a yes does not.
	if got := out.String(); !strings.HasPrefix(got, "Stopped api in ") || strings.Contains(got, "Ended") || strings.Count(got, "\n") != 1 {
		t.Fatalf("out %q", got)
	}

	// No busy agent: no question.
	q := runningProject(t, fake, e, "web")
	fake.SetAgents(q.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude", State: "idle"}})
	never := func(q string) (bool, error) { t.Fatalf("asked %q", q); return false, nil }
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"web"}, Confirm: never}); err != nil {
		t.Fatal(err)
	}
}

// `repose stop api web` stops both after one question, and a name that
// does not resolve stops none (I-615; review A7, theme 7).
func TestStopSeveral(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	a := runningProject(t, fake, e, "api")
	b := runningProject(t, fake, e, "web")
	fake.SetAgents(a.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude", State: "working"}})
	fake.SetAgents(b.ID, []fakeapi.AgentSignal{{Agent: "codex", Window: "codex", State: "needs_input"}})

	err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api", "nope"}, Yes: true})
	if exitCode(err) != ExitProjectNotFound || stateOf(t, e, a.ID) != "running" {
		t.Fatalf("a typo in the second name: %v, api %s", err, stateOf(t, e, a.ID))
	}

	var out bytes.Buffer
	e.Out = &out
	var asked []string
	yes := func(q string) (bool, error) { asked = append(asked, q); return true, nil }
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api", "web", "api"}, Snapshot: true, Confirm: yes}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "api has claude (working); web has codex (needs input). Stopping ends them. Stop api and web? [y/N] " {
		t.Fatalf("asked %q", asked)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Stopped api in ") || !strings.HasPrefix(lines[1], "Stopped web in ") {
		t.Fatalf("out %q", out.String())
	}
	if stateOf(t, e, a.ID) != "stopped" || stateOf(t, e, b.ID) != "stopped" {
		t.Fatal("not both stopped")
	}

	// Again: both already stopped, each says so.
	out.Reset()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"api", "web"}}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "api is already stopped.\nweb is already stopped.\n" {
		t.Fatalf("out %q", out.String())
	}
}

// `repose stop --unused` stops the running projects the api reports idle
// (I-262, I-615).
func TestStopIdle(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	a := runningProject(t, fake, e, "api")
	b := runningProject(t, fake, e, "web")
	var out bytes.Buffer
	e.Out = &out
	if err := StopProjectsCmd(ctx, e, StopOptions{Idle: true}); err != nil || out.String() != "No machine is unused.\n" {
		t.Fatalf("none idle: %v %q", err, out.String())
	}
	since := time.Now().Add(-31 * time.Hour)
	fake.SetIdle(a.ID, &since, 28)
	out.Reset()
	if err := StopProjectsCmd(ctx, e, StopOptions{Idle: true, Snapshot: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Stopped api in ") || stateOf(t, e, a.ID) != "stopped" || stateOf(t, e, b.ID) != "running" {
		t.Fatalf("out %q, web %s", out.String(), stateOf(t, e, b.ID))
	}
}

// `repose rm a b` asks once, naming what each keeps, and destroys both
// (I-615; review A7, 5.6).
func TestDestroySeveral(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	a := runningProject(t, fake, e, "api")
	var out bytes.Buffer
	e.Out = &out
	fake.SetAgents(a.ID, []fakeapi.AgentSignal{{Agent: "claude", Window: "claude", State: "working"}})
	b, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "web", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "tmp-k3f9", Class: "small", ExpiresIn: 3600})
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	yes := func(q string) (bool, error) { asked = append(asked, q); return true, nil }
	if err := DestroyProjectsCmd(ctx, e, []string{"api", "web", "tmp-k3f9"}, false, false, yes); err != nil {
		t.Fatal(err)
	}
	want := "api has claude (working). tmp-k3f9 is temporary: it keeps no snapshot and cannot be restored. Destroy api, web and tmp-k3f9? The final snapshots of api and web are kept for 30 days. [y/N] "
	if len(asked) != 1 || asked[0] != want {
		t.Fatalf("asked %q", asked)
	}
	if out.String() != "Destroying api.\nDestroying web.\nDestroying tmp-k3f9.\n" {
		t.Fatalf("out %q", out.String())
	}
	for _, id := range []string{a.ID, b.ID, tmp.ID} {
		if _, err := e.Client.GetProject(ctx, id); !isNotFound(err) {
			t.Fatalf("%s: %v", id, err)
		}
	}
}

func TestDestroyPrompt(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	p := func(slug string, temp bool) *Project {
		pr := &Project{Slug: slug, State: "stopped"}
		if temp {
			pr.ExpiresAt = &exp
		}
		return pr
	}
	for _, tc := range []struct {
		projects []*Project
		want     string
	}{
		{[]*Project{p("a", false)}, "Destroy a? A final snapshot is kept for 30 days. [y/N] "},
		{[]*Project{p("t", true)}, "t is temporary: destroying it keeps no snapshot and it cannot be restored. Destroy t? [y/N] "},
		{[]*Project{p("a", false), p("b", false)}, "Destroy a and b? Final snapshots are kept for 30 days. [y/N] "},
		{[]*Project{p("a", false), p("t", true)}, "t is temporary: it keeps no snapshot and cannot be restored. Destroy a and t? a's final snapshot is kept for 30 days. [y/N] "},
		{[]*Project{p("t", true), p("u", true)}, "t and u are temporary: they keep no snapshot and cannot be restored. Destroy t and u? [y/N] "},
	} {
		if got := destroyPrompt(tc.projects); got != tc.want {
			t.Errorf("got  %q\nwant %q", got, tc.want)
		}
	}
}

// The in-place restore question names the project and the snapshot, and
// whether a snapshot keeps the disk as it is now (I-614; review A4).
func TestRestoreInPlacePrompt(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 8, h, m, 0, 0, time.Local) }
	started := at(17, 0)
	snaps := []Snapshot{
		{ID: "old", CreatedAt: at(9, 0), Reason: "nightly"},
		{ID: "mine", CreatedAt: at(17, 47), Reason: "manual"},
		{ID: "stop", CreatedAt: at(17, 49), Reason: "stop"},
	}
	p := &Project{Slug: "demo", State: "stopped", StartedAt: &started}
	for _, tc := range []struct {
		name  string
		snaps []Snapshot
		id    string
		want  string
	}{
		{"stop snapshot keeps now", snaps, "mine", "Replace demo's disk with its snapshot of 2026-10-08 17:47? The stop snapshot of 2026-10-08 17:49 keeps the disk as it is now. [y/N] "},
		{"newest is the target", snaps, "stop", "Replace demo's disk with its snapshot of 2026-10-08 17:49? [y/N] "},
		{"stopped without a snapshot", snaps[:2], "old", "Replace demo's disk with its snapshot of 2026-10-08 09:00? No snapshot keeps the disk as it is now; what changed after 2026-10-08 17:47 is lost for good. [y/N] "},
		{"unlisted id", snaps, "abc", "Replace demo's disk with snapshot abc? [y/N] "},
		{"no start time", snaps, "mine", "Replace demo's disk with its snapshot of 2026-10-08 17:47? [y/N] "},
	} {
		p := p
		if tc.name == "no start time" {
			p = &Project{Slug: "demo", State: "stopped"}
		}
		if got := restoreInPlacePrompt(p, tc.snaps, tc.id); got != tc.want {
			t.Errorf("%s:\ngot  %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// A declined in-place restore exits 1 and restores nothing (I-614).
func TestSnapshotsRestoreDeclined(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "demo")
	if err := StopCmd(ctx, e, p.ID, true); err != nil {
		t.Fatal(err)
	}
	snaps, err := e.Client.ListSnapshots(ctx, p.ID)
	if err != nil || len(snaps) == 0 {
		t.Fatalf("snapshots: %v %v", snaps, err)
	}
	var asked string
	err = SnapshotsRestoreCmd(ctx, e, p.ID, snaps[0].ID, "", func(q string) (bool, error) { asked = q; return false, nil })
	if exitCode(err) != ExitGeneric || err.Error() != "Not restored." {
		t.Fatalf("declined: %v", err)
	}
	if !strings.HasPrefix(asked, "Replace demo's disk with its snapshot of ") {
		t.Fatalf("asked %q", asked)
	}
}

// `snapshots create` names the snapshot it took (I-615; review 5.4).
func TestSnapshotsCreateNamesIt(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	var out bytes.Buffer
	e.Out = &out
	p := runningProject(t, fake, e, "demo")
	out.Reset()
	if err := SnapshotsCreateCmd(ctx, e, p.ID); err != nil {
		t.Fatal(err)
	}
	snaps, _ := e.Client.ListSnapshots(ctx, p.ID)
	s := newestSnapshot(snaps)
	if s == nil || !strings.HasPrefix(out.String(), "Snapshot "+s.ID+" of demo taken in ") {
		t.Fatalf("out %q", out.String())
	}
}

// With no projects but destroyed ones that can come back, ls says so
// instead of "yet" (I-615; review 5.4).
func TestLsEmptyCountsDestroyed(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	var out bytes.Buffer
	e.Out = &out
	if err := ProjectsCmd(ctx, e); err != nil || out.String() != "No projects yet for heracraft.\n" {
		t.Fatalf("empty: %v %q", err, out.String())
	}
	p := runningProject(t, fake, e, "izma")
	if err := DestroyCmd(ctx, e, p.ID, true, true, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := ProjectsCmd(ctx, e); err != nil || out.String() != "No projects for heracraft. 1 destroyed in the last 30 days can be restored.\n" {
		t.Fatalf("after rm: %v %q", err, out.String())
	}
}

// In the checkout whose `repose` remote is the machine, a stop fetches
// the agent's commits first (I-615; review 1.1).
func TestStopFetchesFirst(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	machine := t.TempDir() // the machine's checkout
	git(machine, "init", "-q", "-b", "main")
	git(machine, "commit", "-q", "--allow-empty", "-m", "one")
	laptop := t.TempDir()
	git(laptop, "clone", "-q", machine, ".")
	for i := 0; i < 3; i++ {
		git(machine, "commit", "-q", "--allow-empty", "-m", "agent")
	}
	git(machine, "branch", "worktree-1")
	git(machine, "checkout", "-q", "worktree-1")
	git(machine, "commit", "-q", "--allow-empty", "-m", "worktree")
	git(machine, "checkout", "-q", "main")
	git(laptop, "remote", "add", "repose", "demo.repose:~/demo")
	git(laptop, "config", "url."+machine+".insteadOf", "demo.repose:~/demo")

	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	e.Cwd = filepath.Join(laptop)
	var out bytes.Buffer
	e.Out = &out
	p := runningProject(t, fake, e, "demo")
	out.Reset()
	if err := StopCmd(ctx, e, p.ID, true); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	if lines[0] != "Fetched 3 commits on repose/main and 1 on repose/worktree-1." || !strings.HasPrefix(lines[1], "Stopped demo in ") {
		t.Fatalf("out %q", out.String())
	}
	if git(laptop, "rev-parse", "repose/main") != git(machine, "rev-parse", "main") {
		t.Fatal("not fetched")
	}

	// Nothing new: no line. Another project's stop: no fetch.
	q := runningProject(t, fake, e, "other")
	git(machine, "commit", "-q", "--allow-empty", "-m", "later")
	out.Reset()
	if err := StopCmd(ctx, e, q.ID, true); err != nil || !strings.HasPrefix(out.String(), "Stopped other in ") {
		t.Fatalf("other: %v %q", err, out.String())
	}
}

// Ctrl-C while the CLI waits on a stop the api took says the stop goes
// on, not just "Interrupted." (I-615; review A6).
func TestInterruptedStopSaysItGoesOn(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	waiting := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ops/") {
			select {
			case waiting <- struct{}{}:
			default:
			}
			<-r.Context().Done() // the op never ends while the CLI waits
			return
		}
		fake.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "api")
	e.Client = newClient(srv.URL+"/v1", staticToken("tok"))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-waiting; cancel() }()
	err := StopCmd(ctx, e, p.ID, true)
	if exitCode(err) != ExitInterrupted || err.Error() != "Interrupted. The stop of api goes on." {
		t.Fatalf("Ctrl-C during the stop: %v", err)
	}
}

// Ctrl-C during `resize --size` and `fork` says what is left (review A6,
// I-633): the stop goes on and the size is the old one; the snapshot
// goes on and nothing was forked; the copies exist and start.
func TestInterruptedResizeAndForkSayWhatIsLeft(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	waiting := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ops/") {
			select {
			case waiting <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		fake.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "demo")
	e.Client = newClient(srv.URL+"/v1", staticToken("tok"))

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-waiting; cancel() }()
	to := "small"
	if p.Class == "small" {
		to = "large"
	}
	err := ResizeClassCmd(ctx, e, p.ID, to, func(string) (bool, error) { return true, nil })
	if want := "Interrupted. The stop of demo goes on, and it stays " + p.Class + "."; exitCode(err) != ExitInterrupted || err.Error() != want {
		t.Fatalf("resize: %v, want %q", err, want)
	}

	ctx, cancel = context.WithCancel(context.Background())
	go func() { <-waiting; cancel() }()
	err = ForkCmd(ctx, e, ForkOptions{ProjectArg: p.ID, Count: 1})
	if exitCode(err) != ExitInterrupted || err.Error() != "Interrupted. The snapshot of demo goes on; nothing was forked." {
		t.Fatalf("fork: %v", err)
	}

	res := &ForkResult{Projects: []ForkedProject{{Slug: "demo-fork-1"}, {Slug: "demo-fork-2"}}}
	if got := interruptedFork(res, false).Error(); got != "Interrupted. demo-fork-1 and demo-fork-2 exist, and their starts go on." {
		t.Errorf("two: %q", got)
	}
	if got := interruptedFork(&ForkResult{Projects: res.Projects[:1]}, true).Error(); got != "Interrupted. demo-fork-1 exists, and its restore goes on." {
		t.Errorf("one, --no-start: %q", got)
	}
}
