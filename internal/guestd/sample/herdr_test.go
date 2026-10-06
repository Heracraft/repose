package sample

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/multiplexer"
)

// fakeHerdr is a unix listener that answers like herdr: one request line,
// one answer line, then it closes the connection.
type fakeHerdr struct {
	path string
	l    net.Listener

	mu      sync.Mutex
	answers map[string]string // method -> result object (JSON)
	hangup  bool              // close without answering (an EOF)
	reqs    []string          // methods received
	bytesIn int
	// holdMethod, when set, makes that method's answer wait for release;
	// held gets a value each time one is waiting.
	holdMethod string
	held       chan struct{}
	release    chan struct{}
}

func newFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	// A short directory: sun_path holds 108 bytes.
	dir, err := os.MkdirTemp("", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeHerdr{path: filepath.Join(dir, "herdr.sock"), answers: map[string]string{
		"ping": `{"type":"pong","version":"0.9.3","protocol":22}`,
	}}
	f.l, err = net.Listen("unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.l.Close() })
	go f.serve()
	return f
}

func (f *fakeHerdr) serve() {
	for {
		c, err := f.l.Accept()
		if err != nil {
			return
		}
		go f.answer(c)
	}
}

func (f *fakeHerdr) answer(c net.Conn) {
	defer c.Close() //nolint:errcheck // one answer per connection
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(c).ReadBytes('\n')
	f.mu.Lock()
	f.bytesIn += len(line)
	if err != nil {
		f.mu.Unlock()
		return
	}
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(line, &req)
	f.reqs = append(f.reqs, req.Method)
	hangup, res := f.hangup, f.answers[req.Method]
	hold := f.holdMethod != "" && req.Method == f.holdMethod
	held, release := f.held, f.release
	f.mu.Unlock()
	if hold {
		held <- struct{}{}
		<-release
	}
	if hangup {
		return
	}
	if res == "" {
		_, _ = fmt.Fprintf(c, `{"id":%q,"error":{"code":"unknown_method","message":"no"}}`+"\n", req.ID)
		return
	}
	_, _ = fmt.Fprintf(c, `{"id":%q,"result":%s}`+"\n", req.ID, res)
}

func (f *fakeHerdr) set(method, result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[method] = result
}

func (f *fakeHerdr) setHangup(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hangup = v
}

func (f *fakeHerdr) requests() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...), f.bytesIn
}

// agents builds an agent_list result. Each row is pane, workspace, name,
// agent, status, seq.
func agentsResult(rows ...[6]string) string {
	var parts []string
	for _, r := range rows {
		name := ""
		if r[2] != "" {
			name = fmt.Sprintf(`"name":%q,`, r[2])
		}
		parts = append(parts, fmt.Sprintf(
			`{"terminal_id":"term_1","pane_id":%q,"workspace_id":%q,%s"agent":%q,"agent_status":%q,"state_change_seq":%s,"tab_id":"w1:t1","focused":true,"revision":0}`,
			r[0], r[1], name, r[3], r[4], r[5]))
	}
	return `{"type":"agent_list","agents":[` + strings.Join(parts, ",") + `]}`
}

// muxSlug is a project that names its multiplexer.
type muxSlug struct{ slug, mux string }

func (m muxSlug) Slug() string        { return m.slug }
func (m muxSlug) Multiplexer() string { return m.mux }

// newHerdrWatcher is a watcher on a herdr project whose herdr socket is
// the fake's, with no tmux socket (a herdr machine).
func newHerdrWatcher(t *testing.T, f *fakeHerdr, mux string, log *slog.Logger) (*Watcher, *sysdep.FakeRunner, *recorder, *clock, sysdep.Paths) {
	t.Helper()
	p := sysdep.Paths{Root: t.TempDir()}
	run := sysdep.NewFakeRunner()
	rec := &recorder{}
	clk := &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	if log == nil {
		log = quietLog()
	}
	w := NewWatcher(p, run, &sysdep.FakeDocker{Up: true}, muxSlug{"todo-app", mux}, rec, log, clk.now)
	if f != nil {
		w.herdr.socket = f.path
	}
	w.herdr.uid = os.Getuid()
	return w, run, rec, clk, p
}

func agentStates(w *Watcher) map[string]string {
	sig, _ := w.Signals()
	out := map[string]string{}
	for _, a := range sig.GetAgents() {
		out[a.GetTmuxWindow()] = a.GetAgent() + ":" + a.GetState()
	}
	return out
}

// The live test's sequence for a hookless agent (proposal section 6):
// gemini working, then done. The watcher reports working, then idle, and
// one completion that says it went idle. No tmux is forked.
func TestHerdrWorkingThenDoneGemini(t *testing.T) {
	f := newFakeHerdr(t)
	w, run, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	ctx := context.Background()

	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "gemini", "gemini", "working", "1"}))
	w.Refresh(ctx)
	clk.advance(Interval)
	w.Refresh(ctx)
	states, events, _ := rec.snapshot()
	if !containsState(states, "gemini/gemini="+StateWorking) || len(events) != 0 {
		t.Fatalf("states = %v events = %v, want working and no event", states, events)
	}

	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "gemini", "gemini", "done", "2"}))
	for i := 0; i < 3; i++ {
		clk.advance(Interval)
		w.Refresh(ctx)
	}
	states, events, warns := rec.snapshot()
	if !containsState(states, "gemini/gemini="+StateIdle) {
		t.Fatalf("states = %v, want idle after done", states)
	}
	if len(events) != 1 || events[0] != "gemini/gemini=completed:gemini went idle" {
		t.Fatalf("events = %v, want one completion", events)
	}
	if len(warns) != 0 {
		t.Fatalf("warns = %v on a herdr project with herdr up", warns)
	}
	if got := agentStates(w); got["gemini"] != "gemini:idle" {
		t.Fatalf("agents = %v", got)
	}
	for _, c := range run.Calls() {
		if c.Argv[0] == "tmux" {
			t.Fatalf("tmux forked on a machine with no tmux socket: %v", c.Argv)
		}
	}
}

// Two transitions between polls (a turn shorter than a refresh): the
// sequence moved and the status reads idle, so one completion.
func TestHerdrSequenceJumpGivesOneCompletion(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	ctx := context.Background()
	f.set("agent.list", agentsResult([6]string{"w1:p2", "w1", "", "pi", "idle", "4"}))
	w.Refresh(ctx)
	f.set("agent.list", agentsResult([6]string{"w1:p2", "w1", "", "pi", "idle", "6"}))
	clk.advance(Interval)
	w.Refresh(ctx)
	clk.advance(Interval)
	w.Refresh(ctx)
	_, events, _ := rec.snapshot()
	if len(events) != 1 || events[0] != "pi/pi w1:p2=completed:pi went idle" {
		t.Fatalf("events = %v, want one completion", events)
	}
}

// Hooked agents report completion through their hook; herdr's sequence
// raises nothing for them, so nothing arrives twice.
func TestHerdrHookedAgentRaisesNoCompletion(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	ctx := context.Background()
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "claude", "claude", "working", "1"}))
	w.Refresh(ctx)
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "claude", "claude", "done", "2"}))
	clk.advance(Interval)
	w.Refresh(ctx)
	if _, events, _ := rec.snapshot(); len(events) != 0 {
		t.Fatalf("events = %v, want none for claude", events)
	}
}

// guestd is root and the socket path is dev's: a listener with another
// uid gets nothing written to it.
func TestHerdrPeerWithAnotherUIDIsRefused(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, _, _, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	w.herdr.uid = os.Getuid() + 4242
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "claude", "claude", "working", "1"}))
	w.Refresh(context.Background())
	// The fake reads until a newline or EOF: give it time to see the close.
	time.Sleep(50 * time.Millisecond)
	reqs, in := f.requests()
	if in != 0 || len(reqs) != 0 {
		t.Fatalf("wrote %d bytes (%v) to a peer of another uid", in, reqs)
	}
	if got := agentStates(w); len(got) != 0 {
		t.Fatalf("agents = %v", got)
	}
}

// A herdr restart: an EOF for two refreshes keeps the panes and their
// state; the third failed refresh announces them unknown. herdr_down
// comes at the second.
func TestHerdrEOFGrace(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	ctx := context.Background()
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "claude", "claude", "working", "1"}))
	w.Refresh(ctx)
	clk.advance(Interval)
	w.Refresh(ctx)

	f.setHangup(true)
	for i := 0; i < HerdrGraceRefreshes; i++ {
		clk.advance(Interval)
		w.Refresh(ctx)
	}
	states, _, warns := rec.snapshot()
	if containsState(states, "claude/claude="+StateUnknown) {
		t.Fatalf("states = %v: an EOF of two refreshes emitted unknown", states)
	}
	if got := agentStates(w); got["claude"] != "claude:working" {
		t.Fatalf("agents during the grace = %v", got)
	}
	if len(warns) != 1 || warns[0] != WarnHerdrDown {
		t.Fatalf("warns = %v, want herdr_down after two failed refreshes", warns)
	}

	clk.advance(Interval)
	w.Refresh(ctx)
	states, _, _ = rec.snapshot()
	if !containsState(states, "claude/claude="+StateUnknown) || len(agentStates(w)) != 0 {
		t.Fatalf("states = %v agents = %v, want unknown once the grace is over", states, agentStates(w))
	}

	// herdr is back: the agent returns, and a ping comes first.
	f.setHangup(false)
	clk.advance(Interval)
	w.Refresh(ctx)
	if got := agentStates(w); got["claude"] == "" {
		t.Fatalf("agents after herdr came back = %v", got)
	}
	reqs, _ := f.requests()
	if reqs[len(reqs)-2] != "ping" || reqs[len(reqs)-1] != "agent.list" {
		t.Fatalf("requests = %v, want ping then agent.list after the failures", reqs)
	}
}

// A herdr older than protocol 22 is down: no agents, no agent.list, and
// herdr_down on a herdr project.
func TestHerdrOldProtocolIsDown(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	f.set("ping", `{"type":"pong","version":"0.8.0","protocol":21}`)
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "claude", "claude", "working", "1"}))
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		w.Refresh(ctx)
		clk.advance(Interval)
	}
	if got := agentStates(w); len(got) != 0 {
		t.Fatalf("agents = %v", got)
	}
	reqs, _ := f.requests()
	for _, r := range reqs {
		if r != "ping" {
			t.Fatalf("requests = %v, want only pings", reqs)
		}
	}
	if _, _, warns := rec.snapshot(); len(warns) != 1 || warns[0] != WarnHerdrDown {
		t.Fatalf("warns = %v, want one herdr_down", warns)
	}
}

// herdr_down is a herdr project's warning, tmux_down a tmux project's.
func TestDownWarningsFollowProjectJSON(t *testing.T) {
	// A tmux project with no tmux and no herdr: tmux_down only.
	w, _, rec, clk, _ := newHerdrWatcher(t, nil, multiplexer.Tmux, nil)
	for i := 0; i < 3; i++ {
		w.Refresh(context.Background())
		clk.advance(Interval)
	}
	if _, _, warns := rec.snapshot(); len(warns) != 1 || warns[0] != WarnTmuxDown {
		t.Fatalf("tmux project: warns = %v, want tmux_down only", warns)
	}
	// A herdr project with neither: herdr_down only, after two refreshes.
	w, _, rec, clk, _ = newHerdrWatcher(t, nil, multiplexer.Herdr, nil)
	w.Refresh(context.Background())
	if _, _, warns := rec.snapshot(); len(warns) != 0 {
		t.Fatalf("herdr project: warns after one refresh = %v", warns)
	}
	for i := 0; i < 3; i++ {
		clk.advance(Interval)
		w.Refresh(context.Background())
	}
	if _, _, warns := rec.snapshot(); len(warns) != 1 || warns[0] != WarnHerdrDown {
		t.Fatalf("herdr project: warns = %v, want herdr_down only", warns)
	}
}

// A reply over 1 MiB is dropped without being decoded.
func TestHerdrReplyOverTheCapIsDropped(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, _, _, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	pad := strings.Repeat("x", HerdrReplyCap)
	f.set("agent.list", `{"type":"agent_list","pad":"`+pad+`","agents":[{"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","agent_status":"working","state_change_seq":1}]}`)
	w.Refresh(context.Background())
	if got := agentStates(w); len(got) != 0 {
		t.Fatalf("agents = %v from an oversized reply", got)
	}
	if w.herdr.Misses() != 1 {
		t.Fatalf("misses = %d, want the oversized reply counted as a failure", w.herdr.Misses())
	}
}

// The decoder holds six fields, and nothing else herdr sends (titles, cwd,
// the agent session, tokens) reaches an AgentProc or a log line.
func TestHerdrDecoderKeepsSixFields(t *testing.T) {
	var fields []string
	rt := reflect.TypeOf(herdrAgent{})
	for i := 0; i < rt.NumField(); i++ {
		fields = append(fields, rt.Field(i).Tag.Get("json"))
	}
	if got := strings.Join(fields, ","); got != "pane_id,workspace_id,name,agent,agent_status,state_change_seq" {
		t.Fatalf("decoder fields = %s", got)
	}

	f := newFakeHerdr(t)
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w, _, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, log)
	secret := []string{"SECRET-TITLE", "/home/dev/secret-cwd", "SECRET-TERMINAL", "sess-SECRET", "SECRET-TOKEN"}
	f.set("agent.list", `{"type":"agent_list","agents":[{"terminal_id":"term_9","pane_id":"w1:p1","workspace_id":"w1","name":"claude","agent":"claude",`+
		`"title":"SECRET-TITLE","terminal_title":"SECRET-TERMINAL","terminal_title_stripped":"SECRET-TERMINAL","cwd":"/home/dev/secret-cwd","foreground_cwd":"/home/dev/secret-cwd",`+
		`"agent_session":{"id":"sess-SECRET","source":"claude"},"tokens":{"k":"SECRET-TOKEN"},"state_labels":{"x":"SECRET-TITLE"},`+
		`"agent_status":"working","state_change_seq":3,"tab_id":"w1:t1","focused":true,"revision":2}]}`)
	ctx := context.Background()
	w.Refresh(ctx)
	clk.advance(Interval)
	w.Refresh(ctx)

	sig, _ := w.Signals()
	dump := fmt.Sprintf("%+v", sig.GetAgents())
	states, events, _ := rec.snapshot()
	dump += strings.Join(states, " ") + strings.Join(events, " ") + logs.String()
	for _, s := range secret {
		if strings.Contains(dump, s) {
			t.Fatalf("%q from herdr reached an AgentProc, an emission or a log line:\n%s", s, dump)
		}
	}
	if len(sig.GetAgents()) != 1 || sig.GetAgents()[0].GetTmuxWindow() != "claude" || sig.GetAgents()[0].GetState() != StateWorking {
		t.Fatalf("agents = %+v", sig.GetAgents())
	}
}

// guest-conventions "herdr", Agent keys.
func TestHerdrAgentKeys(t *testing.T) {
	f := newFakeHerdr(t)
	w, run, _, _, p := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	// A tmux window named claude runs beside herdr's claude.
	tmuxSocketFile(t, p)
	writeProc(t, p, []fakeProc{{pid: 100, ppid: 1, comm: "bash"}, {pid: 101, ppid: 100, comm: "claude"}})
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	if err := os.MkdirAll(filepath.Dir(p.CheckoutsFile()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CheckoutsFile(), []byte("api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("n", 70)
	f.set("workspace.list", `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"todo-app","number":1},{"workspace_id":"w2","label":"api","number":2}]}`)
	f.set("agent.list", agentsResult(
		[6]string{"w1:p1", "w1", "claude", "claude", "idle", "1"},  // collides with the tmux window
		[6]string{"w1:p2", "w1", "", "gemini", "idle", "1"},        // unnamed
		[6]string{"w2:p1", "w2", "codex", "codex", "working", "1"}, // another checkout's workspace
		[6]string{"w1:p3", "w1", long, "opencode", "idle", "1"},    // 70 bytes
		[6]string{"w1:p4", "w1", "aider", "aider", "working", "1"}, // not one of the five
	))
	w.Refresh(context.Background())
	got := agentStates(w)
	want := map[string]string{
		"claude":         "claude:unknown", // the tmux window: no CPU history yet
		"claude (herdr)": "claude:idle",
		"gemini w1:p2":   "gemini:idle",
		"api/codex":      "codex:working",
		long[:64]:        "opencode:idle",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %v\nwant     %v", got, want)
	}
	for k := range got {
		if len(k) > multiplexer.MaxKey {
			t.Fatalf("key %q is %d bytes", k, len(k))
		}
	}

	// A hook names a pane: the key it resolves to, the suffix included.
	ctx := context.Background()
	for ref, want := range map[string]string{"w1:p1": "claude (herdr)", "w2:p1": "api/codex", "w1:p2": "gemini w1:p2"} {
		if k, ok := w.ResolveHerdr(ctx, ref); !ok || k != want {
			t.Errorf("ResolveHerdr(%q) = %q, %v; want %q", ref, k, ok, want)
		}
	}
	for _, ref := range []string{"nope", "w1:p4", "", strings.Repeat("p", 60), "w1:p1;rm", "w1/p1"} {
		if k, ok := w.ResolveHerdr(ctx, ref); ok {
			t.Errorf("ResolveHerdr(%q) = %q, want unresolved", ref, k)
		}
	}
}

// A pane the last refresh did not see (an agent that started a second
// ago) is resolved with a fresh agent.list.
func TestHerdrResolveReadsANewPane(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, _, _, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	f.set("agent.list", agentsResult())
	w.Refresh(context.Background())
	f.set("agent.list", agentsResult([6]string{"w3:p1", "w3", "claude-2", "claude", "blocked", "1"}))
	if k, ok := w.ResolveHerdr(context.Background(), "w3:p1"); !ok || k != "claude-2" {
		t.Fatalf("ResolveHerdr = %q, %v", k, ok)
	}
}

// herdr's blocked is needs_input, and a needs_input hook on a herdr key
// sets that agent's state and no tmux window's.
func TestHerdrBlockedAndHooks(t *testing.T) {
	f := newFakeHerdr(t)
	w, run, _, clk, p := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	tmuxSocketFile(t, p)
	writeProc(t, p, []fakeProc{{pid: 100, ppid: 1, comm: "bash"}, {pid: 101, ppid: 100, comm: "claude"}})
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	f.set("agent.list", agentsResult(
		[6]string{"w1:p1", "w1", "gemini", "gemini", "blocked", "1"},
		[6]string{"w1:p2", "w1", "claude", "claude", "working", "1"},
	))
	ctx := context.Background()
	w.Refresh(ctx)
	key, ok := w.ResolveHerdr(ctx, "w1:p2")
	if !ok {
		t.Fatal("w1:p2 unresolved")
	}
	w.RecordHook(key, KindNeedsInput, clk.now())
	clk.advance(Interval)
	w.Refresh(ctx)
	got := agentStates(w)
	if got["gemini"] != "gemini:needs_input" || got["claude (herdr)"] != "claude:needs_input" || got["claude"] == "claude:needs_input" {
		t.Fatalf("agents = %v", got)
	}
}

// A herdr restart inside the EOF grace: the new server counts
// state_change_seq from 0 again, so an idle gemini at seq 12 reads idle
// at seq 1. That is no turn ending, and no completion is sent.
func TestHerdrRestartInTheGraceSendsNoCompletion(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	ctx := context.Background()
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "gemini", "gemini", "idle", "12"}))
	w.Refresh(ctx)
	f.setHangup(true)
	for i := 0; i < HerdrGraceRefreshes; i++ {
		clk.advance(Interval)
		w.Refresh(ctx)
	}
	f.setHangup(false)
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "gemini", "gemini", "idle", "1"}))
	for i := 0; i < 2; i++ {
		clk.advance(Interval)
		w.Refresh(ctx)
	}
	if _, events, _ := rec.snapshot(); len(events) != 0 {
		t.Fatalf("events = %v, want none after a restart", events)
	}
	// The next turn on the new server still completes.
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "gemini", "gemini", "done", "3"}))
	clk.advance(Interval)
	w.Refresh(ctx)
	if _, events, _ := rec.snapshot(); len(events) != 1 {
		t.Fatalf("events = %v, want one completion for the new turn", events)
	}
}

// A restart between two refreshes, with no failed read: the sequence goes
// down, and a lower sequence is no turn ending.
func TestHerdrLowerSequenceSendsNoCompletion(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, rec, clk, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	ctx := context.Background()
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "", "pi", "idle", "37"}))
	w.Refresh(ctx)
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "", "pi", "idle", "0"}))
	clk.advance(Interval)
	w.Refresh(ctx)
	if _, events, _ := rec.snapshot(); len(events) != 0 {
		t.Fatalf("events = %v, want none for a lower sequence", events)
	}
}

// workspace.list goes out at most once a minute while it fails or lacks
// an agent's workspace, and at once for a workspace never asked about.
func TestHerdrLabelsAreNotReadEveryRefresh(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, _, clk, p := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	if err := os.MkdirAll(filepath.Dir(p.CheckoutsFile()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CheckoutsFile(), []byte("api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		reqs, _ := f.requests()
		n := 0
		for _, r := range reqs {
			if r == "workspace.list" {
				n++
			}
		}
		return n
	}
	ctx := context.Background()
	// No workspace.list answer: every request fails.
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "codex", "codex", "working", "1"}))
	for i := 0; i < 5; i++ {
		w.Refresh(ctx)
		clk.advance(Interval)
	}
	if n := count(); n != 1 {
		t.Fatalf("workspace.list sent %d times in 25 s while it fails, want 1", n)
	}
	// It answers, without w1.
	f.set("workspace.list", `{"type":"workspace_list","workspaces":[{"workspace_id":"w2","label":"api"}]}`)
	clk.advance(herdrLabelsEvery)
	for i := 0; i < 5; i++ {
		w.Refresh(ctx)
		clk.advance(Interval)
	}
	if n := count(); n != 2 {
		t.Fatalf("workspace.list sent %d times, want 2 with w1 missing from the answer", n)
	}
	// A workspace never asked about is read at once.
	f.set("agent.list", agentsResult(
		[6]string{"w1:p1", "w1", "codex", "codex", "working", "1"},
		[6]string{"w3:p1", "w3", "pi", "pi", "working", "1"},
	))
	w.Refresh(ctx)
	if n := count(); n != 3 {
		t.Fatalf("workspace.list sent %d times, want 3 after w3 appeared", n)
	}
}

// Two herdr agents with one key: the second is not reported, and a hook
// from its pane changes no state, the reported agent's included.
func TestHerdrDuplicateKeyHookResolvesToNothing(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, _, _, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	f.set("agent.list", agentsResult(
		[6]string{"w1:p1", "w1", "claude", "claude", "working", "1"},
		[6]string{"w2:p1", "w2", "claude", "claude", "working", "1"},
	))
	ctx := context.Background()
	w.Refresh(ctx)
	if k, ok := w.ResolveHerdr(ctx, "w1:p1"); !ok || k != "claude" {
		t.Fatalf("ResolveHerdr(w1:p1) = %q, %v", k, ok)
	}
	if k, ok := w.ResolveHerdr(ctx, "w2:p1"); ok {
		t.Fatalf("ResolveHerdr(w2:p1) = %q, want unresolved", k)
	}
	// A pane the refresh has not seen yet, with the reported agent's name.
	f.set("agent.list", agentsResult(
		[6]string{"w1:p1", "w1", "claude", "claude", "working", "1"},
		[6]string{"w3:p1", "w3", "claude", "claude", "working", "1"},
	))
	if k, ok := w.ResolveHerdr(ctx, "w3:p1"); ok {
		t.Fatalf("ResolveHerdr(w3:p1) = %q, want unresolved", k)
	}
}

// A refresh in progress against a slow herdr holds the source; a hook for
// a pane the last refresh did not see gives up at its own deadline.
func TestHerdrResolveDoesNotWaitOnASlowRefresh(t *testing.T) {
	f := newFakeHerdr(t)
	w, _, _, _, _ := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "gemini", "gemini", "working", "1"}))
	f.mu.Lock()
	f.holdMethod = "agent.list"
	f.held = make(chan struct{}, 4)
	f.release = make(chan struct{})
	f.mu.Unlock()
	done := make(chan struct{})
	go func() {
		w.Refresh(context.Background())
		close(done)
	}()
	<-f.held
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	k, ok := w.ResolveHerdr(ctx, "w9:p1")
	if took := time.Since(start); ok || took > time.Second {
		t.Fatalf("ResolveHerdr = %q, %v after %v; want unresolved at the deadline", k, ok, took)
	}
	close(f.release)
	<-done
}

// tmux unreadable for one refresh: its claude window stands, and herdr's
// claude keeps its suffix instead of taking the window's place.
func TestHerdrKeyKeepsItsSuffixWhileTmuxIsUnreadable(t *testing.T) {
	f := newFakeHerdr(t)
	w, run, rec, clk, p := newHerdrWatcher(t, f, multiplexer.Herdr, nil)
	tmuxSocketFile(t, p)
	writeProc(t, p, []fakeProc{{pid: 100, ppid: 1, comm: "bash"}, {pid: 101, ppid: 100, comm: "claude"}})
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	f.set("agent.list", agentsResult([6]string{"w1:p1", "w1", "claude", "claude", "blocked", "1"}))
	ctx := context.Background()
	w.Refresh(ctx)
	clk.advance(StateDebounce)
	w.Refresh(ctx)
	before := agentStates(w)
	if before["claude (herdr)"] != "claude:needs_input" || before["claude"] == "" {
		t.Fatalf("agents = %v", before)
	}
	statesBefore, _, _ := rec.snapshot()

	run.Match["list-windows"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("setpriv: failed to set the group list")}
	clk.advance(Interval)
	w.Refresh(ctx)
	if got := agentStates(w); !reflect.DeepEqual(got, before) {
		t.Fatalf("agents with tmux unreadable = %v, want %v", got, before)
	}
	if k, ok := w.ResolveHerdr(ctx, "w1:p1"); !ok || k != "claude (herdr)" {
		t.Fatalf("ResolveHerdr = %q, %v", k, ok)
	}
	if states, _, _ := rec.snapshot(); len(states) != len(statesBefore) {
		t.Fatalf("states = %v, want none new", states[len(statesBefore):])
	}
}
