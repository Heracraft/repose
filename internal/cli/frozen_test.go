package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// A machine the api has frozen while idle (DECISIONS I-818, I-826): the
// state stays `running`, the marker is in the STATE cell and the status
// header, the uptime is untouched, and nothing warns about it.
func frozenProject(now time.Time) *Project {
	started := now.Add(-31 * time.Hour)
	since := now.Add(-42 * time.Minute)
	sampled := now.Add(-30 * time.Second)
	ok := true
	return &Project{
		ID: "a", Slug: "todo-app", Class: "large", State: "running", StartedAt: &started,
		Resting: &ProjectResting{Since: since},
		Idle:    &ProjectIdle{Since: now.Add(-26 * time.Hour)},
		Signals: &Signals{GuestdOK: &ok, SampledAt: &sampled,
			Agents: []AgentSignal{{Agent: "claude", Window: "claude", State: "idle"}}},
	}
}

// `repose ls` says `running frozen` in the STATE cell, with no new
// column and no line under the table (I-484), and keeps UP: a pause does
// not move started_at. The unused line (I-262) says something else and
// still prints.
func TestLsStateCellSaysFrozen(t *testing.T) {
	now := time.Now()
	started := now.Add(-2 * time.Hour)
	ps := []Project{
		*frozenProject(now),
		{ID: "b", Slug: "web", Class: "small", State: "running", StartedAt: &started,
			Signals: &Signals{Agents: []AgentSignal{{Agent: "claude", Window: "claude", State: "working"}}}},
	}
	var b strings.Builder
	writeProjectsTableHere(&b, ps, "")
	out := b.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if strings.Contains(lines[0], "FROZEN") {
		t.Errorf("a column of its own: %q", lines[0])
	}
	if !regexp.MustCompile(`^todo-app\s+large\s+running frozen\s+31h00m\s+claude: idle$`).MatchString(lines[1]) {
		t.Errorf("frozen row %q", lines[1])
	}
	if !regexp.MustCompile(`^web\s+small\s+running\s+2h00m\s+claude: working$`).MatchString(lines[2]) {
		t.Errorf("awake row %q", lines[2])
	}
	if !strings.Contains(out, "todo-app: unused for 26h\n") {
		t.Errorf("the unused line went:\n%s", out)
	}
	for _, s := range []string{"resting", "wake", "pause"} {
		if strings.Contains(out, s) {
			t.Errorf("%q in the table:\n%s", s, out)
		}
	}
}

// `repose status`'s header carries the marker beside the state, after
// the uptime, which stays true through a pause.
func TestStatusHeaderSaysFrozen(t *testing.T) {
	now := time.Now()
	p := frozenProject(now)
	var b strings.Builder
	writeStatus(&b, statusView{p: p, mux: "tmux", now: now})
	out := b.String()
	first := strings.SplitN(out, "\n", 2)[0]
	if first != "todo-app  running 31h00m  frozen  large" {
		t.Errorf("header %q", first)
	}
	if !strings.Contains(out, "  unused for 26h\n") {
		t.Errorf("the unused line went:\n%s", out)
	}
	// The api keeps guestd_ok true while a machine rests (S4), and a
	// frozen machine must never be called unanswering: its next step,
	// `repose start`, would cold-boot it.
	if strings.Contains(out, "not answering") {
		t.Errorf("the guestd warning fired:\n%s", out)
	}
	// Its agents are frozen where they stood, so none is marked stale.
	if !strings.Contains(out, "  agents     claude idle\n") {
		t.Errorf("agents row:\n%s", out)
	}
	// An awake machine's header is unchanged.
	p.Resting = nil
	b.Reset()
	writeStatus(&b, statusView{p: p, mux: "tmux", now: now})
	if first := strings.SplitN(b.String(), "\n", 2)[0]; first != "todo-app  running 31h00m  large" {
		t.Errorf("awake header %q", first)
	}
}

// A frozen machine's processes cannot move on, so its agent states are
// not marked `?` however old the sample is.
func TestFrozenAgentStatesAreNotStale(t *testing.T) {
	now := time.Now()
	p := frozenProject(now)
	old := now.Add(-20 * time.Minute)
	p.Signals.SampledAt = &old
	if m := staleMark(p, now); m != "" {
		t.Errorf("frozen stale mark %q", m)
	}
	p.Resting = nil
	if m := staleMark(p, now); m != "?" {
		t.Errorf("awake stale mark %q", m)
	}
}

// A frozen machine is never one `repose start` restarts: a restart
// reboots it and loses every process. The api keeps `guestd_ok` true
// while it rests, and the CLI refuses to read a false one as dead.
func TestFrozenMachineIsNotGuestdDead(t *testing.T) {
	now := time.Now()
	p := frozenProject(now)
	no := false
	p.Signals.GuestdOK = &no
	if guestdDead(p) {
		t.Error("a frozen machine read as guestd dead")
	}
	p.Resting = nil
	if !guestdDead(p) {
		t.Error("an awake machine with guestd down read as alive")
	}
}

// `repose status --json` and `repose ls --json` carry the api's field:
// both re-marshal the CLI's own Project, so a field the struct does not
// declare is dropped on the floor.
func TestFrozenJSONKeepsTheFields(t *testing.T) {
	now := time.Now()
	p := frozenProject(now)
	p.KeepAwake = true
	b, err := json.Marshal(statusJSON{Project: p})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	rest, ok := got["resting"].(map[string]any)
	if !ok || rest["since"] == nil {
		t.Fatalf("resting: %s", b)
	}
	if got["keep_awake"] != true {
		t.Fatalf("keep_awake: %s", b)
	}
	// An api that says nothing leaves resting out; keep_awake answers
	// false, as hold_base_updates does.
	p.Resting, p.KeepAwake = nil, false
	b, _ = json.Marshal([]Project{*p})
	if strings.Contains(string(b), `"resting"`) {
		t.Errorf("resting on an awake machine: %s", b)
	}
	if !strings.Contains(string(b), `"keep_awake":false`) {
		t.Errorf("keep_awake missing: %s", b)
	}
}

// Held awake is a setting a new project does not get, so it goes on the
// setup line beside base updates held (I-622).
func TestSetupLineNamesKeptAwake(t *testing.T) {
	if l := setupLine(&Project{KeepAwake: true}, "claude"); l != "setup: kept awake" {
		t.Errorf("kept awake %q", l)
	}
	if l := setupLine(&Project{HoldBaseUpdates: true, KeepAwake: true}, "claude"); l != "setup: base updates held, kept awake" {
		t.Errorf("both %q", l)
	}
	if l := setupLine(&Project{}, "claude"); l != "" {
		t.Errorf("default %q", l)
	}
}

// `repose stop --unused` still stops a frozen machine: that is the user
// asking. Its question names the busy agents from the newest sample,
// which a resting machine repeats, so it is still right.
func TestStopUnusedStillStopsAFrozenMachine(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "api")
	since := time.Now().Add(-31 * time.Hour)
	fake.SetIdle(p.ID, &since, 0)
	froze := time.Now().Add(-42 * time.Minute)
	if err := fake.SetResting(p.ID, &froze); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e.Out = &out
	if err := StopProjectsCmd(ctx, e, StopOptions{Idle: true, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, e, p.ID); got != "stopped" && got != "stopping" {
		t.Errorf("a frozen machine was left %s by --unused: %q", got, out.String())
	}
	// The question it would have asked reads the sample a resting
	// machine repeats, so a busy agent is still named.
	q := frozenProject(time.Now())
	q.Signals.Agents = []AgentSignal{{Agent: "claude", Window: "claude", State: "working"}}
	if got := busyAgents(q); got != "claude (working)" {
		t.Errorf("busy agents %q", got)
	}
}

// The api's field reaches the table, the header and --json through the
// CLI's own decode: a field the Project struct does not declare is
// dropped between the wire and stdout with nothing failing.
func TestLsReadsTheApisRestingField(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	p := runningProject(t, fake, e, "api")
	since := time.Now().Add(-42 * time.Minute)
	if err := fake.SetResting(p.ID, &since); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e.Out = &out
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "running frozen") {
		t.Errorf("ls:\n%s", out.String())
	}
	got, err := e.Client.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen(got) {
		t.Fatalf("the api said resting and the CLI read %+v", got.Resting)
	}
	var b strings.Builder
	writeStatus(&b, statusView{p: got, mux: "tmux", now: time.Now()})
	if first := strings.SplitN(b.String(), "\n", 2)[0]; !strings.Contains(first, "  frozen  ") {
		t.Errorf("header %q", first)
	}
	e.JSON = true
	out.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"resting"`) || !strings.Contains(out.String(), `"keep_awake"`) {
		t.Errorf("--json:\n%s", out.String())
	}
}

// A frozen machine gets the long first-ssh wait: it is `running`, so a
// command that found it running would otherwise take sshWaitShort, and
// the gateway spends its own wake budget inside the dial that follows --
// the user would read "did not answer ssh" about a machine whose
// processes are all still there (DECISIONS I-822).
func TestFrozenMachineGetsTheLongSSHWait(t *testing.T) {
	var e Env
	awake := &Project{State: "running"}
	e.waitFrozen(awake)
	short := e.sshWait()

	var e2 Env
	e2.waitFrozen(&Project{State: "running", Resting: &ProjectResting{Since: time.Now().Add(-42 * time.Minute)}})
	long := e2.sshWait()

	if long <= short {
		t.Fatalf("frozen wait %s, awake wait %s: a frozen machine must get the longer one", long, short)
	}
	if short != sshWaitShort || long != sshWaitTimeout {
		t.Fatalf("waits %s and %s, want %s and %s", short, long, sshWaitShort, sshWaitTimeout)
	}
}
