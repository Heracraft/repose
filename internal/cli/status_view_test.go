package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func intp(n int) *int { return &n }

// `repose status` is labelled rows (I-616): the header names the state,
// its uptime and the size, the hours are gone, host and ip print only
// under -v, agents are named by window with the one that needs you
// first, and the checkout row says what the laptop lacks.
func TestStatusRowsAreLabelled(t *testing.T) {
	now := time.Now()
	started := now.Add(-2*time.Hour - 14*time.Minute)
	snap := now.Add(-11 * time.Hour)
	p := &Project{
		Slug: "todo-app", Class: "large", State: "running", StartedAt: &started,
		RunningSecondsToday: 8040, RunningSecondsMonth: 41 * 3600,
		Signals: &Signals{SSHSessions: 2, TmuxClients: 1, DockerContainers: 3, Agents: []AgentSignal{
			{Agent: "claude", Window: "claude", State: "working"},
			{Agent: "codex", Window: "codex", State: "idle"},
			{Agent: "claude", Window: "claude-2", State: "needs_input"},
			{Agent: "pi", Window: "pi", State: "unknown"},
		}},
	}
	commit := now.Add(-3 * time.Hour)
	v := statusView{
		p: p, mux: "tmux", now: now,
		disk:   guestDisk{Used: 6 << 30, Size: 39 << 30},
		snaps:  []Snapshot{{CreatedAt: snap}},
		events: []Event{{TS: now.Add(-14 * time.Minute), Agent: "claude", Kind: "completed", Summary: "Added auth flow"}},
		procs:  []listeningProc{{Comm: "node", Port: 5173, Age: 3 * 24 * time.Hour, RSS: 410 << 20, HasPID: true}},
		git: []gitRow{
			{Worktree: "todo-app", Branch: "main", NotOnLaptop: intp(3), Uncommitted: intp(2), LastCommitAt: &commit},
			{Worktree: "todo-app-worktree-1", Branch: "worktree-1", NotOnLaptop: intp(0), Uncommitted: intp(0), LastCommitAt: &commit},
		},
	}
	var b strings.Builder
	writeStatus(&b, v)
	want := `todo-app  running 2h14m  large
  agents     claude-2 needs input, claude working, codex idle, pi
  checkout   main: 3 commits not on this laptop, 2 files not committed
             worktree-1: nothing new
  attached   2 SSH sessions, 1 tmux client
  docker     3 containers
  listening  node :5173 up 3d 410.0 MB
  disk       6.0 GB of 39.0 GB, snapshot 11h00m ago
  last event 14m ago, claude finished "Added auth flow"
`
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}

	// -v reads the route: the host and the machine's address, last.
	v.route = &Route{HostName: "host-01", GuestIP: "10.100.0.12"}
	b.Reset()
	writeStatus(&b, v)
	if !strings.HasSuffix(b.String(), "\n  host       host-01, ip 10.100.0.12\n") {
		t.Fatalf("-v:\n%s", b.String())
	}

	// Stopped: no uptime, no agents, nobody attached, the volume's size.
	stopped := &Project{Slug: "web", Class: "small", State: "stopped", VolumeBytes: 20 << 30}
	b.Reset()
	writeStatus(&b, statusView{p: stopped, mux: "tmux", now: now})
	if b.String() != "web  stopped  small\n  disk       20.0 GB, no snapshot yet\n" {
		t.Fatalf("stopped:\n%q", b.String())
	}
}

func TestGitRowText(t *testing.T) {
	now := time.Now()
	old := now.Add(-50 * time.Hour)
	for _, c := range []struct {
		row  gitRow
		want string
	}{
		{gitRow{Branch: "main", NotOnLaptop: intp(1), Uncommitted: intp(1)}, "main: 1 commit not on this laptop, 1 file not committed"},
		{gitRow{Branch: "main", NotOnLaptop: intp(0), Uncommitted: intp(4)}, "main: 4 files not committed"},
		{gitRow{Branch: "main", NotOnLaptop: intp(0), Uncommitted: intp(0), LastCommitAt: &old}, "main: nothing new"},
		// Outside the project's checkout the laptop cannot tell: the
		// last commit's age instead.
		{gitRow{Branch: "main", Uncommitted: intp(0), LastCommitAt: &old}, "main: last commit 2d ago"},
		{gitRow{Branch: "main", Uncommitted: intp(2)}, "main: 2 files not committed"},
		{gitRow{Branch: "main", Uncommitted: intp(0)}, "main: no commits"},
		{gitRow{Worktree: "todo-app", NotOnLaptop: intp(2), Uncommitted: intp(0)}, "todo-app (detached): 2 commits not on this laptop"},
	} {
		if got := gitRowText(c.row, now); got != c.want {
			t.Errorf("%+v: %q, want %q", c.row, got, c.want)
		}
	}
}

func TestParseStatusGit(t *testing.T) {
	out := "#mux\nherdr\n#git\nwt\ttodo-app\tmain\t3\t2\t1700000000\nwt\ttodo-app-worktree-1\t\t-\t-\t\nwt\tbad\nnoise\n"
	rows := parseStatusGit(out, true)
	if len(rows) != 2 {
		t.Fatalf("%+v", rows)
	}
	if r := rows[0]; r.Worktree != "todo-app" || r.Branch != "main" || *r.NotOnLaptop != 3 || *r.Uncommitted != 2 || r.LastCommitAt.Unix() != 1700000000 {
		t.Errorf("row 0 %+v", r)
	}
	if r := rows[1]; r.Branch != "" || r.NotOnLaptop != nil || r.Uncommitted != nil || r.LastCommitAt != nil {
		t.Errorf("row 1 %+v", r)
	}
	// The laptop sent nothing: no count is believed.
	if rows := parseStatusGit(out, false); rows[0].NotOnLaptop != nil {
		t.Errorf("a count without the laptop's commits: %+v", rows[0])
	}
	if rows := parseStatusGit("#mux\n", true); rows != nil {
		t.Errorf("no #git: %+v", rows)
	}
}

// `repose ls` has no TODAY or MONTH (I-616: a plan bills no hours), says
// `needs input` in words (I-617) and marks the project commands here act
// on with `*`.
func TestLsMarksHereAndDropsHours(t *testing.T) {
	ps := []Project{
		{ID: "a", Slug: "todo-app", Class: "large", State: "running", RunningSecondsToday: 8040,
			Signals: &Signals{Agents: []AgentSignal{{Agent: "codex", State: "needs_input"}}}},
		{ID: "b", Slug: "web", Class: "small", State: "stopped"},
	}
	var b strings.Builder
	writeProjectsTableHere(&b, ps, "a")
	want := "PROJECT     SIZE   STATE    UP  AGENTS\ntodo-app *  large  running  -   codex: needs input\nweb         small  stopped  -   -\n"
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestHereProjectID(t *testing.T) {
	remote := normalizeRemote("git@github.com:me/todo-app.git")
	ps := []Project{
		{ID: "a", Slug: "todo-app", RemoteURL: remote},
		{ID: "b", Slug: "todo-app-2", RemoteURL: remote},
		{ID: "c", Slug: "notes"},
	}
	repo := t.TempDir()
	mustRun(t, repo, "git", "init", "-q")
	mustRun(t, repo, "git", "remote", "add", "origin", "git@github.com:me/todo-app.git")
	plain := t.TempDir()
	root := gitRepoRoot(repo)

	t.Setenv(envProject, "")
	e := &Env{Cwd: repo, Cache: newProjectsCache()}
	if got := e.hereProjectID(ps); got != "a" {
		t.Errorf("by remote: %q", got)
	}
	e.Cache.ByRemote[remote] = CachedProject{ProjectID: "b"}
	if got := e.hereProjectID(ps); got != "b" {
		t.Errorf("by the remote's cache: %q", got)
	}
	e = &Env{Cwd: plain, Cache: newProjectsCache()}
	if got := e.hereProjectID(ps); got != "" {
		t.Errorf("a folder with no link: %q", got)
	}
	e.Cache.ByDir[plain] = "c"
	if got := e.hereProjectID(ps); got != "c" {
		t.Errorf("by_dir: %q", got)
	}
	e = &Env{Cwd: repo, Cache: newProjectsCache()}
	e.Cache.Checkouts[root] = CachedCheckout{ProjectID: "c", Name: "todo-app"}
	if got := e.hereProjectID(ps); got != "c" {
		t.Errorf("a checkout run --on added: %q", got)
	}
	// A REPOSE_PROJECT left over wins, as on every command; the marker
	// shows it.
	t.Setenv(envProject, "todo-app-2")
	if got := e.hereProjectID(ps); got != "b" {
		t.Errorf("REPOSE_PROJECT: %q", got)
	}
}

func TestPlanLineAndWarnings(t *testing.T) {
	held := 41.25
	b := &Billing{}
	if planLine(b) != "" || len(planWarnings(nil)) != 0 {
		t.Fatal("a line with no plan")
	}
	b.Subscription = &struct {
		Plan   string `json:"plan"`
		Status string `json:"status"`
	}{Plan: "solo", Status: "active"}
	b.Usage.RunningGB, b.Usage.MemoryGB, b.Usage.DiskHeldGB, b.Usage.DiskGB = 8, 8, &held, 100
	b.Usage.EgressGB, b.Usage.EgressIncludedGB = 212.04, 250
	if got := planLine(b); got != "Solo: 8 of 8 GB running, 41.3 of 100 GB disk, 212 of 250 GB egress this month" {
		t.Errorf("plan line %q", got)
	}
	b.Plans = append(b.Plans, struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{"solo", "Solo"})
	if got := planWarnings(b); len(got) != 0 {
		t.Errorf("warnings within the plan: %q", got)
	}
	b.Usage.EgressGB = 260
	b.Subscription.Status = "past_due"
	got := planWarnings(b)
	if len(got) != 2 || !strings.HasPrefix(got[0], "egress: 260 GB this month, past the plan's 250 GB") || !strings.Contains(got[0], "stop at 1000 GB") || !strings.HasPrefix(got[1], "payment failed: starting a machine is refused") {
		t.Errorf("warnings %q", got)
	}
	if got := planWarningsAfter(b, true); len(got) != 2 || got[0] != "egress past the plan: each GB past 250 GB adds $0.05, and your machines stop at 1000 GB" {
		t.Errorf("warnings under the plan line %q", got)
	}
}

// `repose ls` against the fake: the plan line under the table.
func TestLsPrintsThePlan(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{Billing: true})
	defer fake.Close()
	if _, err := fake.CreateProject("todo-app", "large"); err != nil {
		t.Fatal(err)
	}
	out := &discardWriter{}
	e := &Env{Client: newClient(fake.URL()+"/v1", staticToken("tok")), Out: out, ErrOut: &discardWriter{}, Cache: newProjectsCache(), Cwd: t.TempDir()}
	if err := ProjectsCmd(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.buf.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "Solo: 8 of 8 GB running, ") || strings.Contains(out.buf.String(), "TODAY") {
		t.Fatalf("%s", out.buf.String())
	}
}

// --wait returns once the project is in the state, exits 1 when it lands
// in error or the time runs out, and refuses a word that is no state.
func TestStatusWait(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	p, err := fake.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { statusWaitEvery = d }(statusWaitEvery)
	statusWaitEvery = 20 * time.Millisecond
	newE := func() (*Env, *discardWriter) {
		out := &discardWriter{}
		return &Env{Client: newClient(fake.URL()+"/v1", staticToken("tok")), Out: out, ErrOut: &discardWriter{}, Cache: newProjectsCache(), Cwd: t.TempDir(),
			TargetFor: func(string) sshTarget { return sshTarget{Args: []string{"-o", "ConnectTimeout=1", "nowhere.invalid"}} }}, out
	}
	ctx := context.Background()

	fake.SetState(p.ID, "stopping")
	go func() { time.Sleep(100 * time.Millisecond); fake.SetState(p.ID, "stopped") }()
	e, out := newE()
	if err := StatusWait(ctx, e, "todo-app", "stopped", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.buf.String(), "todo-app  stopped  large\n") {
		t.Fatalf("status after the wait:\n%s", out.buf.String())
	}

	fake.SetState(p.ID, "starting")
	go func() { time.Sleep(100 * time.Millisecond); fake.SetState(p.ID, "error") }()
	e, _ = newE()
	err = StatusWait(ctx, e, "todo-app", "running", time.Minute)
	var xe *exitError
	if !errors.As(err, &xe) || xe.code != ExitGeneric || !strings.HasPrefix(xe.msg, "todo-app is error, not running") {
		t.Fatalf("error state: %v", err)
	}

	fake.SetState(p.ID, "starting")
	e, _ = newE()
	err = StatusWait(ctx, e, "todo-app", "running", 50*time.Millisecond)
	if !errors.As(err, &xe) || xe.code != ExitGeneric || xe.msg != "todo-app is still starting after 50ms, not running." {
		t.Fatalf("timeout: %v", err)
	}

	e, _ = newE()
	if err := StatusWait(ctx, e, "todo-app", "up", time.Minute); !errors.As(err, &xe) || xe.code != ExitUsage {
		t.Fatalf("not a state: %v", err)
	}
}

// --watch off a terminal prints a block only when it changes, and an
// api error that can clear does not end it (F2).
func TestStatusWatchPrintsChangesAndRidesOutErrors(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	p, err := fake.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(p.ID, "stopped")
	defer func(d time.Duration) { statusWatchEvery = d }(statusWatchEvery)
	statusWatchEvery = 30 * time.Millisecond
	out, errOut := &watchOut{}, &watchOut{}
	e := &Env{Client: newClient(fake.URL()+"/v1", staticToken("tok")), Out: out, ErrOut: errOut, Cache: newProjectsCache(), Cwd: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- StatusWatch(ctx, e, p.ID, false) }()
	time.Sleep(150 * time.Millisecond)
	fake.FailNext("GET", "/v1/projects/"+p.ID, "internal")
	time.Sleep(150 * time.Millisecond)
	fake.SetState(p.ID, "stopping")
	time.Sleep(150 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "todo-app  stopped  large\n") != 1 || strings.Count(got, "todo-app  stopping  large\n") != 1 {
		t.Fatalf("blocks:\n%s", got)
	}
	if !strings.Contains(errOut.String(), "Could not read the status at ") {
		t.Fatalf("the failed read was not said: %q", errOut.String())
	}

	// A project that does not exist ends the watch.
	if err := StatusWatch(context.Background(), e, "nope", false); err == nil {
		t.Fatal("watch of no project went on")
	}
}

// End to end against the fake api and the local sshd harness: the
// checkout row counts the commits the laptop lacks, per worktree, and
// the files not committed; --json carries the same.
func TestStatusShowsTheCheckoutsGit(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	guest := f.guestRepo()
	if err := os.WriteFile(filepath.Join(guest, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, guest, "git", "add", "a.txt")
	mustRun(t, guest, "git", "commit", "-q", "-m", "agent work")
	if err := os.WriteFile(filepath.Join(guest, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(f.guestHome, "wt-1")
	mustRun(t, guest, "git", "worktree", "add", "-q", "-b", "worktree-1", wt)
	for _, n := range []string{"c", "d"} {
		if err := os.WriteFile(filepath.Join(wt, n+".txt"), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, wt, "git", "add", n+".txt")
		mustRun(t, wt, "git", "commit", "-q", "-m", n)
	}

	status := func() string {
		out := &discardWriter{}
		f.env.Out = out
		if err := StatusCmd(ctx, f.env, testSlug); err != nil {
			t.Fatal(err)
		}
		return out.buf.String()
	}
	got := status()
	branch, _ := gitCmd(guest, "rev-parse", "--abbrev-ref", "HEAD")
	laptopHead, _ := gitCmd(f.local, "rev-parse", "HEAD")
	n, _ := gitCmd(guest, "rev-list", "--count", "HEAD", "^"+laptopHead)
	if n == "1" {
		n += " commit"
	} else {
		n += " commits"
	}
	nw, _ := gitCmd(wt, "rev-list", "--count", "HEAD", "^"+laptopHead)
	if !strings.Contains(got, "\n  checkout   "+branch+": "+n+" not on this laptop, 1 file not committed\n             worktree-1: "+nw+" commits not on this laptop\n") {
		t.Fatalf("checkout rows:\n%s", got)
	}
	// The run's sync may have committed the laptop's tree on the guest;
	// either way, after a fetch of everything the laptop lacks nothing.
	mustRun(t, f.local, "git", "fetch", "-q", guest, "+refs/heads/*:refs/remotes/repose/*")
	got = status()
	if !strings.Contains(got, "\n  checkout   "+branch+": 1 file not committed\n             worktree-1: nothing new\n") {
		t.Fatalf("after a fetch:\n%s", got)
	}

	// --json: the Project's fields and the rows.
	out := &discardWriter{}
	f.env.Out, f.env.JSON = out, true
	if err := StatusCmd(ctx, f.env, testSlug); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Slug string   `json:"slug"`
		Git  []gitRow `json:"git"`
	}
	if err := json.Unmarshal([]byte(out.buf.String()), &doc); err != nil {
		t.Fatal(err, out.buf.String())
	}
	if doc.Slug != testSlug || len(doc.Git) != 2 || doc.Git[1].Branch != "worktree-1" || doc.Git[1].NotOnLaptop == nil || *doc.Git[1].NotOnLaptop != 0 || *doc.Git[0].Uncommitted != 1 {
		t.Fatalf("json:\n%s", out.buf.String())
	}

	// From a folder that is not the project's checkout the laptop cannot
	// count: the last commit instead.
	f.env.JSON = false
	f.env.Cwd = t.TempDir()
	got = status()
	if !strings.Contains(got, "\n  checkout   "+branch+": last commit just now, 1 file not committed\n") {
		t.Fatalf("outside the checkout:\n%s", got)
	}
}

// watchOut is a writer a goroutine writes while the test reads.
type watchOut struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *watchOut) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *watchOut) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// I-634: a running machine that did not answer the probe gets a checkout
// row that says so, and --json a git_error, so neither reads as a
// machine with no checkout.
func TestStatusSaysTheProbeFailed(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	p, err := fake.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(p.ID, "running")
	ctx := context.Background()
	newE := func(json bool) (*Env, *discardWriter) {
		out := &discardWriter{}
		return &Env{Client: newClient(fake.URL()+"/v1", staticToken("tok")), Out: out, ErrOut: &discardWriter{}, Cache: newProjectsCache(), Cwd: t.TempDir(), JSON: json,
			TargetFor: func(string) sshTarget { return sshTarget{Args: []string{"-p", "1", "127.0.0.1"}} }}, out
	}
	e, out := newE(false)
	if err := StatusCmd(ctx, e, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), "\n  checkout   unknown (ssh failed: ") {
		t.Fatalf("status:\n%s", out.buf.String())
	}
	e, out = newE(true)
	if err := StatusCmd(ctx, e, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), `"git_error": "ssh_failed"`) {
		t.Fatalf("json:\n%s", out.buf.String())
	}

	var b bytes.Buffer
	writeGitRowsOr(&b, nil, time.Now(), "ssh_timeout", "")
	if b.String() != "  checkout   unknown (no ssh answer in 4 s)\n" {
		t.Fatalf("timeout row %q", b.String())
	}
}

// I-634: an agent that needs input says how long it has waited, from
// the event that began the wait; ls gives the longest; a stale sample
// marks the states with `?`.
func TestAgentWaitAges(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-30 * time.Second)
	p := &Project{State: "running", Idle: &ProjectIdle{Since: now.Add(-26 * time.Hour)}, Signals: &Signals{SampledAt: &fresh, Agents: []AgentSignal{
		{Agent: "claude", Window: "claude", State: "working"},
		{Agent: "claude", Window: "claude-2", State: "needs_input"},
		{Agent: "codex", Window: "codex", State: "needs_input"},
	}}}
	noteWaits(p, []Event{
		{TS: now.Add(-4 * time.Hour), Kind: "needs_input", Window: "claude-2"},
		{TS: now.Add(-5 * time.Hour), Kind: "completed", Window: "claude-2"},
		// codex's newest event is not a question: no age.
		{TS: now.Add(-3 * time.Hour), Kind: "needs_input", Window: "codex"},
		{TS: now.Add(-1 * time.Hour), Kind: "completed", Window: "codex"},
	})
	if got := agentList(p); got != "claude-2 needs input 4h, codex needs input, claude working" {
		t.Fatalf("status row %q", got)
	}
	if got := agentState(p); got != "3 agents: 2 needs input (4h), 1 working" {
		t.Fatalf("ls cell %q", got)
	}
	p.Signals.Agents = p.Signals.Agents[1:2]
	if got := idleLine(p, now); got != "unused for 26h; claude-2 needs input 4h" {
		t.Fatalf("idle line %q", got)
	}
	stale := now.Add(-10 * time.Minute)
	p.Signals.SampledAt = &stale
	if got := agentState(p); got != "claude: needs input? 4h" {
		t.Fatalf("stale cell %q", got)
	}
}
