package sample

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

type fixedSlug string

func (f fixedSlug) Slug() string { return string(f) }

type recorder struct {
	mu     sync.Mutex
	states []string
	events []string
	warns  []string
}

func (r *recorder) AgentState(agent, window, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, fmt.Sprintf("%s/%s=%s", agent, window, state))
}

func (r *recorder) AgentEvent(agent, window, kind, summary string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf("%s/%s=%s:%s", agent, window, kind, summary))
}

func (r *recorder) Warn(kind, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warns = append(r.warns, kind)
}

func (r *recorder) snapshot() (states, events, warns []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.states...), append([]string(nil), r.events...), append([]string(nil), r.warns...)
}

// clock is a hand-advanced time source, so state transitions are exact.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// tmuxOutput builds a list-windows result in the watcher's format.
func tmuxOutput(rows ...[4]string) sysdep.RunResult {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r[0], r[1], r[2], r[3])
	}
	return sysdep.RunResult{Stdout: []byte(b.String())}
}

func newWatcherFixture(t *testing.T, procs []fakeProc) (*Watcher, *sysdep.FakeRunner, *recorder, *clock, sysdep.Paths) {
	t.Helper()
	p := sysdep.Paths{Root: t.TempDir()}
	writeProc(t, p, procs)
	run := sysdep.NewFakeRunner()
	rec := &recorder{}
	clk := &clock{t: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	docker := &sysdep.FakeDocker{Up: true, Containers: 2}
	w := NewWatcher(p, run, docker, fixedSlug("todo-app"), rec, quietLog(), clk.now)
	tmuxSocketFile(t, p)
	return w, run, rec, clk, p
}

// tmuxSocketFile stands in for dev's tmux socket: the tmux source forks
// tmux only when it exists.
func tmuxSocketFile(t *testing.T, p sysdep.Paths) {
	t.Helper()
	uid, _ := sysdep.DevIdentity()
	path := p.TmuxSocket(uid)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentOf(t *testing.T) {
	cases := map[string]string{
		"claude":    "claude",
		"claude-2":  "claude",
		"codex-12":  "codex",
		"opencode":  "opencode",
		"gemini":    "gemini",
		"pi":        "pi",
		"shell":     "",
		"claude-x":  "",
		"claudette": "",
		"":          "",
	}
	for window, want := range cases {
		if got := AgentOf(window); got != want {
			t.Errorf("AgentOf(%q) = %q, want %q", window, got, want)
		}
	}
}

func TestWatcherReportsWorkingThenIdle(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash"},
		{pid: 101, ppid: 100, comm: "claude", ticks: 50},
	}
	w, run, rec, clk, p := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	run.Match["list-clients"] = sysdep.RunResult{Stdout: []byte("/dev/pts/0\n")}

	ctx := context.Background()
	w.Refresh(ctx) // first look: the window appears

	// The agent burns CPU: working.
	writeProc(t, p, []fakeProc{{pid: 101, ppid: 100, comm: "claude", ticks: 200}})
	clk.advance(Interval)
	w.Refresh(ctx)
	clk.advance(StateDebounce)
	w.Refresh(ctx)

	states, _, _ := rec.snapshot()
	if !containsState(states, "claude/claude="+StateWorking) {
		t.Fatalf("states = %v, want a working state", states)
	}

	// Then it stops for longer than IdleAfter.
	clk.advance(IdleAfter)
	w.Refresh(ctx)
	clk.advance(StateDebounce)
	w.Refresh(ctx)
	states, _, _ = rec.snapshot()
	if !containsState(states, "claude/claude="+StateIdle) {
		t.Fatalf("states = %v, want an idle state", states)
	}
}

func containsState(states []string, want string) bool {
	for _, s := range states {
		if s == want {
			return true
		}
	}
	return false
}

func TestWatcherIgnoresAWindowNamedAfterAnAgentThatIsNotRunning(t *testing.T) {
	// A user renamed a shell "claude". It is not an agent window.
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash"},
		{pid: 101, ppid: 100, comm: "vim"},
	}
	w, run, _, _, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "vim", "0"})

	w.Refresh(context.Background())
	sig, _ := w.Signals()
	if len(sig.GetAgents()) != 0 {
		t.Fatalf("agents = %v, want none", sig.GetAgents())
	}
}

func TestHookSetsNeedsInput(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash"},
		{pid: 101, ppid: 100, comm: "claude", ticks: 10},
	}
	w, run, _, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})

	ctx := context.Background()
	w.Refresh(ctx)
	w.RecordHook("claude", KindNeedsInput, clk.now())
	clk.advance(Interval)
	w.Refresh(ctx)

	sig, _ := w.Signals()
	if len(sig.GetAgents()) != 1 || sig.GetAgents()[0].GetState() != StateNeedsInput {
		t.Fatalf("agents = %+v, want one needs_input", sig.GetAgents())
	}

	// A completion clears it.
	w.RecordHook("claude", KindCompleted, clk.now())
	clk.advance(Interval)
	w.Refresh(ctx)
	sig, _ = w.Signals()
	if sig.GetAgents()[0].GetState() == StateNeedsInput {
		t.Fatal("needs_input survived a completion hook")
	}
}

func TestHeuristicCompletionForAHooklessAgent(t *testing.T) {
	procs := []fakeProc{
		{pid: 200, ppid: 1, comm: "bash"},
		{pid: 201, ppid: 200, comm: "gemini", ticks: 5},
	}
	w, run, rec, clk, _ := newWatcherFixture(t, procs)
	activity := clk.now().Unix()
	run.Match["list-windows"] = tmuxOutput([4]string{"gemini", "201", "gemini", fmt.Sprint(activity)})

	ctx := context.Background()
	w.Refresh(ctx)
	_, events, _ := rec.snapshot()
	if len(events) != 0 {
		t.Fatalf("events = %v, want none yet", events)
	}

	clk.advance(HeuristicIdleAfter + time.Second)
	w.Refresh(ctx)
	_, events, _ = rec.snapshot()
	if len(events) != 1 || !strings.Contains(events[0], "completed:gemini went idle") {
		t.Fatalf("events = %v, want one heuristic completion saying it went idle", events)
	}

	// It fires once, not every five seconds.
	clk.advance(HeuristicIdleAfter)
	w.Refresh(ctx)
	_, events, _ = rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %v, want the heuristic to fire once per quiet period", events)
	}
}

func TestHookedAgentsDoNotUseTheHeuristic(t *testing.T) {
	procs := []fakeProc{
		{pid: 300, ppid: 1, comm: "bash"},
		{pid: 301, ppid: 300, comm: "claude", ticks: 5},
	}
	w, run, rec, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "301", "claude", fmt.Sprint(clk.now().Unix())})

	ctx := context.Background()
	w.Refresh(ctx)
	clk.advance(HeuristicIdleAfter * 3)
	w.Refresh(ctx)

	_, events, _ := rec.snapshot()
	if len(events) != 0 {
		t.Fatalf("events = %v: claude has a real Stop hook, so the heuristic must stay quiet", events)
	}
}

func TestTmuxDownWarnsOnce(t *testing.T) {
	w, run, rec, clk, _ := newWatcherFixture(t, nil)
	run.Match["list-windows"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("no server running on /tmp/tmux-1000/default")}

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		w.Refresh(ctx)
		clk.advance(Interval)
	}
	_, _, warns := rec.snapshot()
	if len(warns) != 1 || warns[0] != WarnTmuxDown {
		t.Fatalf("warns = %v, want one %s", warns, WarnTmuxDown)
	}
	sig, _ := w.Signals()
	if sig.GetTmuxClients() != 0 || len(sig.GetAgents()) != 0 {
		t.Fatalf("signals with no tmux server = %+v, want zeroes", sig)
	}
}

func TestDockerDownWarnsOnce(t *testing.T) {
	p := sysdep.Paths{Root: t.TempDir()}
	run := sysdep.NewFakeRunner()
	rec := &recorder{}
	clk := &clock{t: time.Now()}
	docker := &sysdep.FakeDocker{Up: false}
	w := NewWatcher(p, run, docker, fixedSlug(""), rec, quietLog(), clk.now)

	// Docker still starting at boot is not Docker down (I-161).
	w.Refresh(context.Background())
	if _, _, warns := rec.snapshot(); len(warns) != 0 {
		t.Fatalf("warns inside the boot grace = %v, want none", warns)
	}
	clk.advance(DockerGrace)
	for i := 0; i < 3; i++ {
		w.Refresh(context.Background())
	}
	_, _, warns := rec.snapshot()
	if len(warns) != 1 || warns[0] != WarnDockerDown {
		t.Fatalf("warns = %v, want one %s", warns, WarnDockerDown)
	}

	docker.Set(true, 4)
	w.Refresh(context.Background())
	sig, _ := w.Signals()
	if sig.GetDockerContainers() != 4 {
		t.Fatalf("docker_containers = %d, want 4", sig.GetDockerContainers())
	}
}

// Once the socket has answered, a failure inside the grace is reported:
// the grace covers Docker starting, not Docker dying.
func TestDockerDownAfterItAnsweredWarnsInsideTheGrace(t *testing.T) {
	p := sysdep.Paths{Root: t.TempDir()}
	rec := &recorder{}
	clk := &clock{t: time.Now()}
	docker := &sysdep.FakeDocker{Up: true}
	w := NewWatcher(p, sysdep.NewFakeRunner(), docker, fixedSlug(""), rec, quietLog(), clk.now)
	w.Refresh(context.Background())
	docker.Set(false, 0)
	clk.advance(Interval)
	w.Refresh(context.Background())
	if _, _, warns := rec.snapshot(); len(warns) != 1 || warns[0] != WarnDockerDown {
		t.Fatalf("warns = %v, want one %s", warns, WarnDockerDown)
	}
}

func TestStaleCacheIsReportedNotFresh(t *testing.T) {
	w, run, _, clk, _ := newWatcherFixture(t, nil)
	run.Match["list-windows"] = tmuxOutput()
	w.Refresh(context.Background())
	if _, fresh := w.Signals(); !fresh {
		t.Fatal("a just-refreshed cache is not fresh")
	}
	clk.advance(CacheStale + time.Second)
	if _, fresh := w.Signals(); fresh {
		t.Fatal("a stale cache reported itself fresh, so a Sample would report signals that are minutes old as current")
	}
}

func TestWindowThatDisappearsIsAnnouncedUnknownOnce(t *testing.T) {
	procs := []fakeProc{
		{pid: 400, ppid: 1, comm: "bash"},
		{pid: 401, ppid: 400, comm: "codex", ticks: 5},
	}
	w, run, rec, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"codex", "401", "codex", "0"})
	ctx := context.Background()
	w.Refresh(ctx)

	run.Match["list-windows"] = tmuxOutput()
	clk.advance(Interval)
	w.Refresh(ctx)
	clk.advance(Interval)
	w.Refresh(ctx)

	states, _, _ := rec.snapshot()
	count := 0
	for _, s := range states {
		if s == "codex/codex="+StateUnknown {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("states = %v, want exactly one unknown for the closed window", states)
	}
}

func TestAnUnexpectedTmuxFailureIsReportedNotSwallowed(t *testing.T) {
	// The failure this is written against: guestd reported "no agent windows"
	// for a guest whose tmux was fine but whose tmux invocation was broken, so
	// a working agent looked idle and nothing said why.
	w, run, _, _, _ := newWatcherFixture(t, nil)
	run.Match["list-windows"] = sysdep.RunResult{
		ExitCode: 1,
		Stderr:   []byte("setpriv: failed to set the group list"),
	}
	_, _, err := w.tmux.client.listWindows(context.Background(), "todo-app")
	if err == nil {
		t.Fatal("an unexpected tmux failure was reported as an empty window list")
	}
	if !strings.Contains(err.Error(), "other") {
		t.Fatalf("err = %v, want a classified reason", err)
	}
}

// A tmux that cannot be read keeps its windows and lets the cache age, as
// before the herdr source: the sample goes partial after CacheStale.
func TestAnUnreadableTmuxLetsTheCacheAge(t *testing.T) {
	procs := []fakeProc{{pid: 100, ppid: 1, comm: "bash"}, {pid: 101, ppid: 100, comm: "claude"}}
	w, run, rec, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	ctx := context.Background()
	w.Refresh(ctx)
	run.Match["list-windows"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("setpriv: failed to set the group list")}
	for i := 0; i < 4; i++ {
		clk.advance(Interval)
		w.Refresh(ctx)
	}
	sig, fresh := w.Signals()
	if fresh {
		t.Fatal("the cache is fresh though tmux has not been read for four refreshes")
	}
	if len(sig.GetAgents()) != 1 {
		t.Fatalf("agents = %v, want the claude window kept", sig.GetAgents())
	}
	if states, _, _ := rec.snapshot(); containsState(states, "claude/claude="+StateUnknown) {
		t.Fatalf("states = %v: a tmux read failure announced the window gone", states)
	}
}

func TestTmuxFailureClassification(t *testing.T) {
	cases := map[string]string{
		"no server running on /tmp/tmux-1000/default": "server_down",
		"can't find session: todo-app":                "session_missing",
		"error connecting to /tmp/tmux-1000/default":  "connect_failed",
		"exec: \"tmux\": executable file not found":   "tmux_missing",
		"something nobody has seen":                   "other",
	}
	for stderr, want := range cases {
		if got := tmuxFailure(stderr); got != want {
			t.Errorf("tmuxFailure(%q) = %q, want %q", stderr, got, want)
		}
	}
}

// Gemini CLI runs as `node` (the bundle under the guest's node, I-46): the
// window is still gemini's and the heuristic still fires (I-122). On
// host-01 the pane's command was `node` and no completion ever came.
func TestHeuristicCompletionForGeminiRunningAsNode(t *testing.T) {
	procs := []fakeProc{
		{pid: 200, ppid: 1, comm: "bash"},
		// node names its main thread MainThread (what host-01 showed, I-125);
		// the exe link is what says node.
		{pid: 201, ppid: 200, comm: "MainThread", exe: "node", ticks: 5},
	}
	w, run, rec, clk, _ := newWatcherFixture(t, procs)
	activity := clk.now().Unix()
	run.Match["list-windows"] = tmuxOutput([4]string{"gemini", "201", "node", fmt.Sprint(activity)})
	ctx := context.Background()
	w.Refresh(ctx)
	clk.advance(HeuristicIdleAfter + time.Second)
	w.Refresh(ctx)
	_, events, _ := rec.snapshot()
	if len(events) != 1 || !strings.Contains(events[0], "completed:gemini went idle") {
		t.Fatalf("events = %v, want one heuristic completion for gemini running as node", events)
	}
	if !isAgentCommand("gemini", "node") || isAgentCommand("pi", "node") || !isAgentCommand("pi", "pi") {
		t.Fatal("isAgentCommand does not follow the binaries table")
	}
}

// I-421: claude started in the shell window counts as an agent while it is
// the foreground program, keyed by that window for its hooks; node in a
// window not named gemini, and a shell with no agent in front, do not.
func TestAnAgentInAWindowWithAnotherName(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash"},
		{pid: 101, ppid: 100, comm: ".claude-wrapped", ticks: 50},
		{pid: 200, ppid: 1, comm: "bash"},
		{pid: 201, ppid: 200, comm: "node", ticks: 50},
		{pid: 300, ppid: 1, comm: "bash"},
	}
	w, run, _, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput(
		[4]string{"shell", "100", ".claude-wrapped", "0"},
		[4]string{"server", "200", "node", "0"},
		[4]string{"misc", "300", "bash", "0"},
	)
	ctx := context.Background()
	w.Refresh(ctx)
	sig, _ := w.Signals()
	if got := sig.GetAgents(); len(got) != 1 || got[0].GetAgent() != "claude" || got[0].GetTmuxWindow() != "shell" {
		t.Fatalf("agents = %v, want claude in window shell only", got)
	}
	w.RecordHook("shell", KindNeedsInput, clk.now())
	clk.advance(Interval)
	w.Refresh(ctx)
	sig, _ = w.Signals()
	if got := sig.GetAgents(); len(got) != 1 || got[0].GetState() != StateNeedsInput {
		t.Fatalf("after a needs_input hook from the shell window: %v", got)
	}
	// claude exits; the shell is in front again and the window stops counting.
	run.Match["list-windows"] = tmuxOutput([4]string{"shell", "100", "bash", "0"})
	clk.advance(Interval)
	w.Refresh(ctx)
	sig, _ = w.Signals()
	if got := sig.GetAgents(); len(got) != 0 {
		t.Fatalf("agents after claude exited = %v", got)
	}
	for comm, want := range map[string]string{"claude": "claude", ".claude-wrapped": "claude", ".opencode-wrapp": "opencode", "codex": "codex", "gemini": "gemini", "node": "", "bash": "", "": ""} {
		if got := AgentByCommand(comm); got != want {
			t.Errorf("AgentByCommand(%q) = %q, want %q", comm, got, want)
		}
	}
}

// I-200 covers every pane of the session. list-windows names only each
// window's active pane; the claude window here has a second claude pane
// and a vite pane, and the shell window has claude in its inactive pane.
// All three claudes get OOMProtected and the vite stays at 0.
func TestOOMPriorityCoversSplitPanes(t *testing.T) {
	procs := []fakeProc{
		{pid: 50, ppid: 1, comm: "tmux: server", uid: 1000},
		{pid: 100, ppid: 50, comm: "bash", uid: 1000},
		{pid: 101, ppid: 100, comm: ".claude-wrapped", uid: 1000, exe: ".claude-wrapped"},
		{pid: 110, ppid: 50, comm: "bash", uid: 1000},
		{pid: 111, ppid: 110, comm: ".claude-wrapped", uid: 1000, exe: ".claude-wrapped"},
		{pid: 120, ppid: 50, comm: "bash", uid: 1000},
		{pid: 121, ppid: 120, comm: "node", uid: 1000, exe: "node"},
		{pid: 200, ppid: 50, comm: "bash", uid: 1000},
		{pid: 210, ppid: 50, comm: "bash", uid: 1000},
		{pid: 211, ppid: 210, comm: ".claude-wrapped", uid: 1000, exe: ".claude-wrapped"},
	}
	w, run, _, _, p := newWatcherFixture(t, procs)
	adj := map[int]int{101: 0, 111: 0, 121: -800, 211: 0}
	for pid, v := range adj {
		if err := os.WriteFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"), []byte(fmt.Sprintf("%d\n", v)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, pid := range []int{50, 100, 110, 120, 200, 210} {
		if err := os.WriteFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run.Match["list-windows"] = tmuxOutput(
		[4]string{"claude", "100", "claude", "0"},
		[4]string{"shell", "200", "bash", "0"},
	)
	run.Match["list-clients"] = sysdep.RunResult{Stdout: []byte("/dev/pts/0\n")}
	run.Match["list-panes"] = sysdep.RunResult{Stdout: []byte(
		"claude\t100\tclaude\nclaude\t110\tclaude\nclaude\t120\tnode\nshell\t200\tbash\nshell\t210\tclaude\n")}

	w.Refresh(context.Background())

	want := map[int]int{101: OOMProtected, 111: OOMProtected, 211: OOMProtected, 121: 0, 50: OOMProtected, 100: 0, 210: 0}
	for pid, v := range want {
		b, _ := os.ReadFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"))
		if got := strings.TrimSpace(string(b)); got != fmt.Sprint(v) {
			t.Errorf("pid %d oom_score_adj = %s, want %d", pid, got, v)
		}
	}
	var sawPanes bool
	for _, c := range run.Calls() {
		if strings.Join(c.Argv, " ") == "tmux list-panes -s -t todo-app -F #{window_name}\t#{pane_pid}\t#{pane_current_command}" {
			sawPanes = true
		}
	}
	if !sawPanes {
		t.Error("list-panes -s was not run for the project session")
	}
}

// I-606: a tmux agent window carries its announced state in
// @repose-state, set once per change, so `repose ps` and the status line
// read it without the api; a herdr agent (no tmux window) gets none.
func TestWatcherMarksTheTmuxWindowState(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash"},
		{pid: 101, ppid: 100, comm: "claude", ticks: 50},
	}
	w, run, _, clk, p := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude-2", "101", "claude", "0"})
	ctx := context.Background()
	w.Refresh(ctx)
	writeProc(t, p, []fakeProc{{pid: 101, ppid: 100, comm: "claude", ticks: 200}})
	clk.advance(Interval)
	w.Refresh(ctx)
	clk.advance(StateDebounce)
	w.Refresh(ctx)
	var sets []string
	for _, c := range run.Calls() {
		if len(c.Argv) > 1 && c.Argv[1] == "set-option" {
			sets = append(sets, strings.Join(c.Argv, " "))
		}
	}
	want := "tmux set-option -w -t =todo-app:=claude-2 " + StateOption + " " + StateWorking
	if len(sets) != 1 || sets[0] != want {
		t.Fatalf("set-option calls %q, want [%q]", sets, want)
	}
	// Still working: no change, no second call.
	writeProc(t, p, []fakeProc{{pid: 101, ppid: 100, comm: "claude", ticks: 400}})
	clk.advance(Interval)
	w.Refresh(ctx)
	n := 0
	for _, c := range run.Calls() {
		if len(c.Argv) > 1 && c.Argv[1] == "set-option" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d set-option calls after a refresh with no change", n)
	}
}
