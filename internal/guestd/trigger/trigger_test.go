package trigger_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/guestd/trigger"
	"github.com/heracraft/repose/internal/triggerrun"
)

// agents is the two signals the runner reads, as a test sets them.
type agents struct {
	state    string
	present  bool
	question bool
}

func (a *agents) WindowAgent(string) (string, bool) { return a.state, a.present }
func (a *agents) QuestionOpen(string) bool          { return a.question }

type harness struct {
	t      *testing.T
	root   string
	runner *sysdep.FakeRunner
	ag     *agents
	r      *trigger.Runner
	now    time.Time
	mux    string
	// mu guards runs: two deliveries at once report from two
	// goroutines, as two of the edge's connections would. In the guest
	// the emit is a channel send (guestd's enqueue).
	mu   sync.Mutex
	runs []*guestdv1.TriggerRun
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"home/dev", "run/repose", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := &harness{t: t, root: root, runner: sysdep.NewFakeRunner(),
		ag: &agents{}, now: time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)}
	paths := sysdep.Paths{Root: root}
	h.r = trigger.New(paths, h.runner, h.ag,
		func() trigger.Project { return trigger.Project{Slug: "todo-app", Mux: h.mux} },
		func(n *guestdv1.TriggerRun) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.runs = append(h.runs, n)
		},
		slog.New(slog.DiscardHandler), func() time.Time { return h.now })
	// tmux answers with the windows the test says exist.
	h.setWindows()
	return h
}

// newHarnessAt is a runner over a volume that already exists, which is
// what a restarted guestd is.
func newHarnessAt(t *testing.T, root string) *harness {
	t.Helper()
	h := &harness{t: t, root: root, runner: sysdep.NewFakeRunner(),
		ag: &agents{}, now: time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)}
	h.r = trigger.New(sysdep.Paths{Root: root}, h.runner, h.ag,
		func() trigger.Project { return trigger.Project{Slug: "todo-app", Mux: h.mux} },
		func(n *guestdv1.TriggerRun) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.runs = append(h.runs, n)
		},
		slog.New(slog.DiscardHandler), func() time.Time { return h.now })
	h.setWindows()
	return h
}

// setWindows is what `tmux list-windows` answers.
func (h *harness) setWindows(names ...string) {
	h.runner.Match["list-windows"] = sysdep.RunResult{Stdout: []byte(strings.Join(append([]string{"shell"}, names...), "\n") + "\n")}
}

// writePrompt puts a prompt file in the checkout.
func (h *harness) writePrompt(rel, text string) {
	h.t.Helper()
	p := filepath.Join(h.root, "home/dev/todo-app", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) set(ts ...trigger.Trigger) { h.r.SetTriggers(ts) }

func (h *harness) last() *guestdv1.TriggerRun {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.runs) == 0 {
		h.t.Fatal("no run was reported")
	}
	return h.runs[len(h.runs)-1]
}

func basic(name string) trigger.Trigger {
	return trigger.Trigger{Name: name, Kind: "route", PromptPath: ".repose/triggers/" + name + ".md",
		Workdir: "checkout", Agent: "claude", TimeoutS: 3600, Concurrency: 1, Enabled: true}
}

// A delivery opens one window, with the prompt as the agent's own
// argument (I-780) and the payload as a file the session can read.
func TestADeliveryOpensOneWindowWithThePromptOnArgv(t *testing.T) {
	h := newHarness(t)
	h.set(basic("apply"))
	h.writePrompt(".repose/triggers/apply.md", "Read $REPOSE_PAYLOAD and apply if it fits")

	res, err := h.r.Start(context.Background(), trigger.Request{
		Trigger: "apply", RunID: "019b2c71-0000-7000-8000-000000000001",
		Source: triggerrun.SourceRoute, Payload: []byte(`{"id":"evt_1"}`),
		Headers: map[string]string{"X-GitHub-Delivery": "d1", "Repose-Webhook-URL": "https://hooks/x/tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != triggerrun.StateWorking || res.Window != "t-apply" {
		t.Fatalf("result %+v, want working in t-apply", res)
	}
	var opened *sysdep.RunSpec
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 2 && strings.Contains(c.Argv[2], "tmux new-window") {
			spec := c
			opened = &spec
		}
	}
	if opened == nil {
		t.Fatal("no window was opened")
	}
	cmd := opened.Argv[2]
	if !strings.Contains(cmd, "-n t-apply") {
		t.Errorf("the window is not named t-apply: %s", cmd)
	}
	// The prompt is a word on claude's own command line, not typed: the
	// line tmux gets is the agent, `--` and the prompt, each quoted
	// twice so the session's bash splits it back into those three words
	// (I-780).
	start := cmd[strings.Index(cmd, "tmux new-window"):]
	for _, want := range []string{`'claude'`, `'--'`, "Read $REPOSE_PAYLOAD and apply if it fits"} {
		if !strings.Contains(start, want) {
			t.Errorf("the window's command has no %s: %s", want, start)
		}
	}
	// And nothing is typed into a pane: no send-keys anywhere.
	for _, c := range h.runner.Calls() {
		if strings.Contains(strings.Join(c.Argv, " "), "send-keys") {
			t.Error("the runner typed the prompt into a pane, which submits 1 in 10 (I-780)")
		}
	}
	if opened.User != "dev" {
		t.Errorf("the window was opened as %q, want dev", opened.User)
	}
	env := strings.Join(opened.Env, "\n")
	for _, want := range []string{"REPOSE_TRIGGER=apply", "REPOSE_RUN=019b2c71", "REPOSE_SOURCE=route", "REPOSE_PAYLOAD="} {
		if !strings.Contains(env, want) {
			t.Errorf("the session's environment has no %s", want)
		}
	}
	// The payload and its headers are files in the machine.
	dir := filepath.Join(h.root, "home/dev/.repose/triggers/apply/runs/019b2c71-0000-7000-8000-000000000001")
	if b, err := os.ReadFile(filepath.Join(dir, "payload")); err != nil || string(b) != `{"id":"evt_1"}` {
		t.Errorf("payload file: %q %v", b, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "headers.json"))
	if err != nil {
		t.Fatalf("headers file: %v", err)
	}
	var headers map[string]string
	if err := json.Unmarshal(b, &headers); err != nil {
		t.Fatal(err)
	}
	if headers["x-github-delivery"] != "d1" {
		t.Errorf("the provider's header is missing: %v", headers)
	}
	// Never the one that carries the route's token: a file an agent
	// reads is no place for a credential that fires the trigger.
	for k := range headers {
		if strings.EqualFold(k, "repose-webhook-url") {
			t.Error("headers.json carries Repose-Webhook-URL, which is the route's token")
		}
	}
	if got := h.last(); got.State != triggerrun.StateWorking || got.Window != "t-apply" {
		t.Errorf("reported %+v", got)
	}
}

// The dedupe window is a file on the volume, so a provider's retry after
// a reboot is still a duplicate (I-1254).
func TestDedupeSkipsARetryAndSurvivesARestart(t *testing.T) {
	h := newHarness(t)
	tr := basic("apply")
	tr.Dedupe = "header:X-GitHub-Delivery"
	h.set(tr)
	h.writePrompt(".repose/triggers/apply.md", "do it")

	req := func(id, delivery string) trigger.Request {
		return trigger.Request{Trigger: "apply", RunID: id, Source: triggerrun.SourceRoute,
			Headers: map[string]string{"X-GitHub-Delivery": delivery}}
	}
	if res, err := h.r.Start(context.Background(), req("r1", "d1")); err != nil || res.State != triggerrun.StateWorking {
		t.Fatalf("first delivery: %+v %v", res, err)
	}
	// The same delivery again, with the concurrency slot free.
	h.ag.state, h.ag.present = "idle", true
	h.setWindows("t-apply")
	h.r.SetTriggers([]trigger.Trigger{tr}) // the api re-sends the set
	res, err := h.r.Start(context.Background(), req("r2", "d1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.State != triggerrun.StateSkipped || res.Reason != triggerrun.ReasonDuplicate {
		t.Fatalf("a retry of one delivery is %+v, want skipped/duplicate", res)
	}
	// A new runner over the same home is a guestd restart: the window is
	// still remembered.
	h2 := trigger.New(sysdep.Paths{Root: h.root}, h.runner, h.ag,
		func() trigger.Project { return trigger.Project{Slug: "todo-app"} },
		func(*guestdv1.TriggerRun) {}, slog.New(slog.DiscardHandler), func() time.Time { return h.now })
	h2.SetTriggers([]trigger.Trigger{tr})
	res, err = h2.Start(context.Background(), req("r3", "d1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.State != triggerrun.StateSkipped || res.Reason != triggerrun.ReasonDuplicate {
		t.Fatalf("after a restart the retry is %+v, want skipped/duplicate", res)
	}
}

// A gate's non-zero exit skips the run, and its own last line of stderr
// is the reason the owner reads. The payload reaches it on stdin.
func TestAGateRefusesAnEventAndSaysWhy(t *testing.T) {
	h := newHarness(t)
	tr := basic("deploy")
	tr.Gate = "./verify.sh"
	h.set(tr)
	h.writePrompt(".repose/triggers/deploy.md", "deploy")
	h.runner.Match["verify.sh"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("checking\nno signature header\n")}

	res, err := h.r.Start(context.Background(), trigger.Request{
		Trigger: "deploy", RunID: "r1", Source: triggerrun.SourceRoute, Payload: []byte("{}"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != triggerrun.StateSkipped {
		t.Fatalf("a gated event is %+v, want skipped", res)
	}
	if res.Reason != triggerrun.GateReason("no signature header") {
		t.Errorf("the reason is %q, want the gate's last line", res.Reason)
	}
	// And no window was opened.
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 2 && strings.Contains(c.Argv[2], "tmux new-window") {
			t.Error("a gated event opened a window")
		}
	}
	// The gate saw the payload on stdin and ran as dev.
	var gate *sysdep.RunSpec
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 2 && strings.Contains(c.Argv[2], "verify.sh") {
			spec := c
			gate = &spec
		}
	}
	if gate == nil {
		t.Fatal("the gate did not run")
	}
	if string(gate.Stdin) != "{}" {
		t.Errorf("the gate's stdin is %q, want the payload", gate.Stdin)
	}
	if gate.User != "dev" {
		t.Errorf("the gate ran as %q, want dev", gate.User)
	}
	// Its whole output is in the run's directory, where
	// guest-conventions.md says it is: the row carries the last line
	// only, and `fire --dry-run` is a loop a gate is written in.
	b, err := os.ReadFile(filepath.Join(h.root, "home/dev/.repose/triggers/deploy/runs/r1/gate.err"))
	if err != nil {
		t.Fatalf("gate.err: %v", err)
	}
	if !strings.Contains(string(b), "checking") {
		t.Errorf("gate.err holds %q, want the gate's own output", b)
	}
}

// `--dry-run` runs the gate and starts no agent, which is how a gate
// gets written.
func TestADryRunRunsTheGateAndStartsNoAgent(t *testing.T) {
	h := newHarness(t)
	tr := basic("deploy")
	tr.Gate = "true"
	h.set(tr)
	h.writePrompt(".repose/triggers/deploy.md", "deploy")

	res, err := h.r.Start(context.Background(), trigger.Request{
		Trigger: "deploy", RunID: "r1", Source: triggerrun.SourceHand, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != triggerrun.StateSkipped || res.Reason != triggerrun.ReasonDryRun {
		t.Fatalf("a dry run is %+v, want skipped/dry_run", res)
	}
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 2 && strings.Contains(c.Argv[2], "tmux new-window") {
			t.Error("a dry run opened a window")
		}
	}
}

// Admission, in the order of §7: a trigger that is off takes no event
// but still takes a hand fire; the concurrency limit skips; a missing
// prompt fails and says so rather than firing every morning in silence.
func TestAdmissionRefusals(t *testing.T) {
	h := newHarness(t)
	off := basic("sweep")
	off.Enabled = false
	h.set(off)
	h.writePrompt(".repose/triggers/sweep.md", "sweep")

	res, _ := h.r.Start(context.Background(), trigger.Request{Trigger: "sweep", RunID: "r1", Source: triggerrun.SourceRoute})
	if res.State != triggerrun.StateSkipped || res.Reason != triggerrun.ReasonOff {
		t.Errorf("an event on a trigger that is off is %+v, want skipped/off", res)
	}
	// `off` shuts the doors; a fire is the owner asking.
	res, _ = h.r.Start(context.Background(), trigger.Request{Trigger: "sweep", RunID: "r2", Source: triggerrun.SourceHand})
	if res.State != triggerrun.StateWorking {
		t.Errorf("a hand fire of a trigger that is off is %+v, want working", res)
	}
	// Its slot is taken now.
	res, _ = h.r.Start(context.Background(), trigger.Request{Trigger: "sweep", RunID: "r3", Source: triggerrun.SourceHand})
	if res.State != triggerrun.StateSkipped || res.Reason != triggerrun.ReasonConcurrency {
		t.Errorf("a second run at the limit is %+v, want skipped/concurrency", res)
	}
	// A prompt file that is not there.
	gone := basic("gone")
	h.set(gone)
	res, _ = h.r.Start(context.Background(), trigger.Request{Trigger: "gone", RunID: "r4", Source: triggerrun.SourceClock})
	if res.State != triggerrun.StateFailed || res.Reason != triggerrun.ReasonPromptMissing {
		t.Errorf("a missing prompt is %+v, want failed/prompt_missing", res)
	}
	// A trigger this guest does not know is an api that has not re-sent
	// its set.
	if _, err := h.r.Start(context.Background(), trigger.Request{Trigger: "nope", RunID: "r5"}); err == nil {
		t.Error("an unknown trigger was accepted")
	}
}

// A fire the api sent twice is one run and one agent: the run's id is
// the command's id, so a re-send after a reconnect lands on the same
// row (I-1252).
func TestARepeatedFireStartsNoSecondAgent(t *testing.T) {
	h := newHarness(t)
	h.set(basic("apply"))
	h.writePrompt(".repose/triggers/apply.md", "go")
	req := trigger.Request{Trigger: "apply", RunID: "r1", Source: triggerrun.SourceClock}
	if _, err := h.r.Start(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	windows := 0
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 2 && strings.Contains(c.Argv[2], "tmux new-window") {
			windows++
		}
	}
	if _, err := h.r.Start(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	after := 0
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 2 && strings.Contains(c.Argv[2], "tmux new-window") {
			after++
		}
	}
	if after != windows {
		t.Errorf("a repeated fire opened %d windows, want the first one alone", after-windows+1)
	}
}

// The supervisor ends a run when the agent's turn ends, and the run's
// clock stops while a question it asked is waiting: a `repose-ask` at
// 02:00 must not be killed by the timeout (I-1256).
func TestTheSupervisorEndsARunAndWaitsForAQuestion(t *testing.T) {
	h := newHarness(t)
	tr := basic("triage")
	tr.TimeoutS = 60
	h.set(tr)
	h.writePrompt(".repose/triggers/triage.md", "triage")
	if _, err := h.r.Start(context.Background(), trigger.Request{Trigger: "triage", RunID: "r1", Source: triggerrun.SourceClock}); err != nil {
		t.Fatal(err)
	}
	h.setWindows("t-triage")

	// Working, and a question is pending: hours pass and the run lives.
	h.ag.state, h.ag.present, h.ag.question = "needs_input", true, true
	for range 5 {
		h.now = h.now.Add(time.Hour)
		h.r.Sweep(context.Background())
	}
	if got := h.last(); got.State != triggerrun.StateWorking {
		t.Fatalf("a run waiting for an answer ended as %q/%q", got.State, got.Reason)
	}

	// The owner answers and the agent reports its own words; the next
	// pass ends the run as done with those words.
	h.ag.question = false
	h.r.OnAgentEvent("t-triage", "agent_done", "12 issues triaged, 2 need you", "", "")
	h.ag.state = "idle"
	h.now = h.now.Add(time.Minute)
	h.r.Sweep(context.Background())
	got := h.last()
	if got.State != triggerrun.StateDone {
		t.Fatalf("the run ended as %q/%q, want done", got.State, got.Reason)
	}
	if got.Summary != "12 issues triaged, 2 need you" {
		t.Errorf("the summary is %q, want the agent's own words", got.Summary)
	}
}

// A run past its timeout is killed, and only its own window: killing the
// server would take every agent on the machine.
func TestATimeoutKillsThatWindowAndNoOther(t *testing.T) {
	h := newHarness(t)
	tr := basic("apply")
	tr.TimeoutS = 60
	h.set(tr)
	h.writePrompt(".repose/triggers/apply.md", "go")
	if _, err := h.r.Start(context.Background(), trigger.Request{Trigger: "apply", RunID: "r1", Source: triggerrun.SourceClock}); err != nil {
		t.Fatal(err)
	}
	h.setWindows("t-apply")
	h.ag.state, h.ag.present = "working", true
	// Two passes with time working between them, past the minute.
	for range 2 {
		h.now = h.now.Add(90 * time.Second)
		h.r.Sweep(context.Background())
	}
	if got := h.last(); got.State != triggerrun.StateTimeout || got.Reason != triggerrun.ReasonTimeout {
		t.Fatalf("the run is %q/%q, want timeout", got.State, got.Reason)
	}
	var killed []string
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 1 && c.Argv[0] == "tmux" && c.Argv[1] == "kill-window" {
			killed = append(killed, c.Argv[len(c.Argv)-1])
		}
		if len(c.Argv) > 1 && c.Argv[1] == "kill-server" {
			t.Fatal("the timeout killed the tmux server, which takes every agent with it")
		}
	}
	if len(killed) != 1 || killed[0] != "todo-app:t-apply" {
		t.Errorf("killed %v, want todo-app:t-apply alone", killed)
	}
}

// guestd restarting does not touch tmux, so a run whose window is alive
// keeps working and only a run with no window ends (I-1256).
func TestReconcileKeepsALiveRunAndEndsALostOne(t *testing.T) {
	h := newHarness(t)
	h.set(basic("apply"), basic("sweep"))
	h.writePrompt(".repose/triggers/apply.md", "a")
	h.writePrompt(".repose/triggers/sweep.md", "s")
	for name, id := range map[string]string{"apply": "r1", "sweep": "r2"} {
		if _, err := h.r.Start(context.Background(), trigger.Request{Trigger: name, RunID: id, Source: triggerrun.SourceClock}); err != nil {
			t.Fatal(err)
		}
	}
	// A new runner over the same /run is the restart. Only t-apply's
	// window survived.
	var reported []*guestdv1.TriggerRun
	h2 := trigger.New(sysdep.Paths{Root: h.root}, h.runner, h.ag,
		func() trigger.Project { return trigger.Project{Slug: "todo-app"} },
		func(n *guestdv1.TriggerRun) { reported = append(reported, n) },
		slog.New(slog.DiscardHandler), func() time.Time { return h.now })
	h.setWindows("t-apply")
	h2.Reconcile(context.Background())

	states := map[string]string{}
	for _, n := range reported {
		states[n.GetRunId()] = n.GetState() + "/" + n.GetReason()
	}
	if states["r1"] != triggerrun.StateWorking+"/" {
		t.Errorf("the run whose window is alive is %q, want working", states["r1"])
	}
	if states["r2"] != triggerrun.StateFailed+"/"+triggerrun.ReasonGuestRestart {
		t.Errorf("the run whose window is gone is %q, want failed/guest_restart", states["r2"])
	}
}

// The receiver answers the edge, not the provider's world: 202 when the
// event is admitted, 200 with the reason when admission dropped it, 404
// for a trigger that is not there, and it never waits for the agent.
func TestTheReceiverAnswersTheEdge(t *testing.T) {
	h := newHarness(t)
	tr := basic("apply")
	h.set(tr)
	h.writePrompt(".repose/triggers/apply.md", "go")
	srv := httptest.NewServer(http.HandlerFunc(h.r.HandleForTest))
	defer srv.Close()

	post := func(path string, body string) (int, string) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, _ := post("/t/apply", `{"id":1}`); code != http.StatusAccepted {
		t.Errorf("an admitted event answered %d, want 202", code)
	}
	// The slot is taken, so the next one is dropped with its reason.
	code, body := post("/t/apply", `{"id":2}`)
	if code != http.StatusOK || !strings.Contains(body, triggerrun.ReasonConcurrency) {
		t.Errorf("a dropped event answered %d %q", code, body)
	}
	if code, _ := post("/t/nope", ""); code != http.StatusNotFound {
		t.Errorf("an unknown trigger answered %d, want 404", code)
	}
	if code, _ := post("/t/apply", strings.Repeat("x", trigger.PayloadCap+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over the cap answered %d, want 413", code)
	}
}

// Two deliveries that arrive together take one slot and one window
// name. The first version checked the limit, then wrote the payload,
// then ran a gate of up to ten seconds, and only then registered the
// run: both passed `concurrency: 1` and both opened `t-apply`.
func TestTwoDeliveriesAtOnceTakeOneSlotAndOneName(t *testing.T) {
	h := newHarness(t)
	tr := basic("apply")
	tr.Concurrency = 1
	tr.Gate = "slow-gate"
	h.set(tr)
	h.writePrompt(".repose/triggers/apply.md", "go")
	// The gate admits, slowly, which is where the window was.
	h.runner.Match["slow-gate"] = sysdep.RunResult{ExitCode: 0}

	type got struct {
		res trigger.Result
		err error
	}
	out := make(chan got, 2)
	for i := range 2 {
		go func(i int) {
			res, err := h.r.Start(context.Background(), trigger.Request{
				Trigger: "apply", RunID: "r" + string(rune('1'+i)), Source: triggerrun.SourceRoute,
				Payload: []byte("{}"),
			})
			out <- got{res, err}
		}(i)
	}
	working, skipped := 0, 0
	for range 2 {
		g := <-out
		if g.err != nil {
			t.Fatal(g.err)
		}
		switch g.res.State {
		case triggerrun.StateWorking:
			working++
		case triggerrun.StateSkipped:
			skipped++
			if g.res.Reason != triggerrun.ReasonConcurrency {
				t.Errorf("the second delivery is %q, want concurrency", g.res.Reason)
			}
		}
	}
	if working != 1 || skipped != 1 {
		t.Fatalf("%d working and %d skipped, want one of each", working, skipped)
	}
	var windows []string
	for _, c := range h.runner.Calls() {
		if len(c.Argv) > 2 && strings.Contains(c.Argv[2], "tmux new-window") {
			windows = append(windows, c.Argv[2])
		}
	}
	if len(windows) != 1 {
		t.Fatalf("%d windows opened for one slot", len(windows))
	}
}

// A run that ends with no summary is still done: the state is the
// machine's and the words are the agent's (I-688). The first version
// read "no summary" as "no agent" and failed, notified and alarmed the
// owner for every quiet run.
func TestARunThatSaidNothingIsStillDone(t *testing.T) {
	h := newHarness(t)
	h.set(basic("sweep"))
	h.writePrompt(".repose/triggers/sweep.md", "sweep")
	if _, err := h.r.Start(context.Background(), trigger.Request{
		Trigger: "sweep", RunID: "r1", Source: triggerrun.SourceClock,
	}); err != nil {
		t.Fatal(err)
	}
	h.setWindows("t-sweep")
	h.ag.state, h.ag.present = "working", true
	h.now = h.now.Add(time.Second)
	h.r.Sweep(context.Background())
	// The agent exits without saying anything and its window closes.
	h.setWindows()
	h.now = h.now.Add(time.Second)
	h.r.Sweep(context.Background())
	got := h.last()
	if got.State != triggerrun.StateDone {
		t.Fatalf("a quiet run ended %q/%q, want done", got.State, got.Reason)
	}
	if got.Summary != "" {
		t.Errorf("summary %q, want none", got.Summary)
	}
}

// A gated event is not remembered as seen: it paid for nothing, so the
// provider's retry of it is gated again rather than read as a duplicate.
func TestAGatedEventIsNotADuplicateNextTime(t *testing.T) {
	h := newHarness(t)
	tr := basic("deploy")
	tr.Gate = "verify.sh"
	tr.Dedupe = "header:X-Delivery"
	h.set(tr)
	h.writePrompt(".repose/triggers/deploy.md", "deploy")
	h.runner.Match["verify.sh"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("no signature\n")}

	req := func(id string) trigger.Request {
		return trigger.Request{Trigger: "deploy", RunID: id, Source: triggerrun.SourceRoute,
			Headers: map[string]string{"X-Delivery": "d1"}, Payload: []byte("{}")}
	}
	if res, _ := h.r.Start(context.Background(), req("r1")); res.State != triggerrun.StateSkipped {
		t.Fatalf("the gated event is %+v", res)
	}
	// The gate is fixed and the provider retries the same delivery.
	h.runner.Match["verify.sh"] = sysdep.RunResult{ExitCode: 0}
	res, err := h.r.Start(context.Background(), req("r2"))
	if err != nil {
		t.Fatal(err)
	}
	if res.State != triggerrun.StateWorking {
		t.Fatalf("the retry of a gated event is %+v, want working: a gated event paid for nothing", res)
	}
}

// The set is the api's state and the receiver answers 404 without it, so
// a guestd that restarts reads back the set it was last sent. Without
// this every delivery after a guestd restart was a 404 the edge reports
// as `route_gone` — "the route was removed" — about a route that is
// still there, and every scheduled fire failed the same way, until the
// next start of the machine.
func TestTheSetSurvivesAGuestdRestart(t *testing.T) {
	h := newHarness(t)
	h.set(basic("apply"), basic("triage"))

	// A second runner over the same volume is the restarted guestd.
	next := newHarnessAt(t, h.root)
	if _, ok := next.r.Get("apply"); ok {
		t.Fatal("the set was there before Load, so this test proves nothing")
	}
	next.r.Load()
	for _, name := range []string{"apply", "triage"} {
		if _, ok := next.r.Get(name); !ok {
			t.Errorf("%s was lost by the restart, so its deliveries are 404s", name)
		}
	}
	// A set emptied by the api does not come back from the volume.
	h.set()
	last := newHarnessAt(t, h.root)
	last.r.Load()
	if _, ok := last.r.Get("apply"); ok {
		t.Error("a trigger the user removed came back after a restart")
	}
}

// A run on a herdr project is refused with the reason that says so. The
// runner opens a tmux window, and a herdr machine has no tmux session,
// so `tmux new-window` failed and the run was reported `no_agent` — "the
// agent is not installed on this machine" — which is not what happened.
func TestAHerdrMachineRefusesTheFireAndSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.mux = "herdr"
	h.set(basic("apply"))
	h.writePrompt(".repose/triggers/apply.md", "go")
	res, err := h.r.Start(context.Background(), trigger.Request{
		Trigger: "apply", RunID: "r1", Source: triggerrun.SourceRoute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != triggerrun.StateSkipped || res.Reason != triggerrun.ReasonHerdr {
		t.Fatalf("a herdr machine answered %q/%q, want skipped/herdr", res.State, res.Reason)
	}
	for _, c := range h.runner.Calls() {
		if strings.Contains(strings.Join(c.Argv, " "), "new-window") {
			t.Error("it tried to open a tmux window on a herdr machine")
		}
	}
}

// A prompt-hooked agent reads `idle` until its first hook, so the
// seconds between its window opening and it taking the prompt look
// exactly like a finished turn. The supervisor ticks every five seconds:
// without the settle grace a run was reported `done`, with no summary,
// while its agent was still starting — and `--stop-after` then stopped
// the machine under it.
func TestARunIsNotDoneBeforeItsAgentStarts(t *testing.T) {
	h := newHarness(t)
	h.set(basic("apply"))
	h.writePrompt(".repose/triggers/apply.md", "go")
	if _, err := h.r.Start(context.Background(), trigger.Request{
		Trigger: "apply", RunID: "r1", Source: triggerrun.SourceRoute,
	}); err != nil {
		t.Fatal(err)
	}
	h.setWindows("t-apply")

	// Claude is in the window and has sent no hook yet.
	h.ag.state, h.ag.present = "idle", true
	for range 6 {
		h.now = h.now.Add(5 * time.Second)
		h.r.Sweep(context.Background())
	}
	if got := h.last(); got.State != triggerrun.StateWorking {
		t.Fatalf("a run whose agent had not started yet ended as %q/%q", got.State, got.Reason)
	}

	// Its first hook arrives, it works, and the turn ends: that idle is
	// the real one.
	h.ag.state = "working"
	h.now = h.now.Add(5 * time.Second)
	h.r.Sweep(context.Background())
	h.ag.state = "idle"
	h.now = h.now.Add(5 * time.Second)
	h.r.Sweep(context.Background())
	if got := h.last(); got.State != triggerrun.StateDone {
		t.Fatalf("the finished turn did not end the run: %q/%q", got.State, got.Reason)
	}
}

// An agent that never starts at all does end, so a run is not open for
// ever: past the settle grace the supervisor believes the idle it reads.
func TestAnAgentThatNeverStartsEndsPastTheGrace(t *testing.T) {
	h := newHarness(t)
	h.set(basic("apply"))
	h.writePrompt(".repose/triggers/apply.md", "go")
	if _, err := h.r.Start(context.Background(), trigger.Request{
		Trigger: "apply", RunID: "r1", Source: triggerrun.SourceRoute,
	}); err != nil {
		t.Fatal(err)
	}
	h.setWindows("t-apply")
	h.ag.state, h.ag.present = "idle", true
	h.now = h.now.Add(trigger.SettleGrace + time.Second)
	h.r.Sweep(context.Background())
	if got := h.last(); !triggerrun.Terminal(got.State) {
		t.Fatalf("the run is still %q past the settle grace", got.State)
	}
}

// Two sets at once: hostd sends one on a start and another when a
// restarted guestd says Ready, so the write to the volume has to be
// serialised with the map. An unsynchronised save could rename the
// older set in last, or tear the file between the two, which is the
// failure keeping it on the volume exists to prevent. Run with -race.
func TestTwoSetsAtOnceLeaveOneWholeFile(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				h.r.SetTriggers([]trigger.Trigger{basic("apply"), basic("triage")})
				return
			}
			h.r.SetTriggers([]trigger.Trigger{basic("apply"), basic("triage"), basic("sweep")})
		}()
	}
	wg.Wait()

	next := newHarnessAt(t, h.root)
	next.r.Load()
	// Whichever of the two sets landed last, the file is one of them
	// whole: `apply` and `triage` are in both.
	for _, name := range []string{"apply", "triage"} {
		if _, ok := next.r.Get(name); !ok {
			t.Errorf("%s is missing, so the kept set was torn", name)
		}
	}
	// And no temp file is left behind for a Load to trip over.
	entries, err := os.ReadDir(filepath.Dir(next.r.SetPathForTest()))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".trigger-set-") {
			t.Errorf("a temp file was left behind: %s", e.Name())
		}
	}
}
