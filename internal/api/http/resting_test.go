package httpapi_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/idle"
	"github.com/heracraft/repose/internal/api/resting"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// restingEnv is a signed-in user with one running machine on the fake
// host, which carries the `pause` feature in its Hello.
type restingEnv struct {
	e   *env
	tok string
	u   *store.User
	p   *store.Project
}

func newRestingEnv(t *testing.T, handle string) *restingEnv {
	t.Helper()
	e := newEnv(t)
	tok := e.signIn(t, "sub-"+handle, handle)
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(e.h.Ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.CreateRunning(u, handle+"-box")
	return &restingEnv{e: e, tok: tok, u: u, p: p}
}

// sample inserts one minute of a project's history. resting is the marker
// hostd sets for a paused guest, which keeps guestd_ok true and repeats
// the last guest-written signals (DECISIONS I-818).
func (r *restingEnv) sample(t *testing.T, ts time.Time, s sampleRow) {
	t.Helper()
	ctx := r.e.h.Ctx
	if err := db.EnsurePartitions(ctx, r.e.h.Pool, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := r.e.h.Pool.Exec(ctx, `insert into meter_samples
		(ts, project_id, host_id, state, class, cpu_ns, net_rx, net_tx, ssh_sessions, tmux_clients, agents, guestd_ok, resting)
		values ($1, $2, $3, 'running', 'large', $4, $5, $6, $7, $8, $9::jsonb, $10, $11)`,
		ts, r.p.ID, r.p.HostID, s.cpuNs, s.netRx, s.netTx, s.ssh, s.tmux, s.agents(), s.guestdOK(), s.resting); err != nil {
		t.Fatal(err)
	}
}

type sampleRow struct {
	cpuNs, netRx, netTx int64
	ssh, tmux           int
	agent               string
	noGuestd            bool
	resting             bool
}

func (s sampleRow) agents() string {
	if s.agent == "" {
		return `[]`
	}
	return fmt.Sprintf(`[{"agent":"claude","window":"claude","state":%q}]`, s.agent)
}

func (s sampleRow) guestdOK() bool { return !s.noGuestd }

// quiet is a minute that counts toward a rest: nobody attached, the agent
// at its prompt, no CPU and no network.
var quiet = sampleRow{agent: "idle"}

// quietStretch writes the Samples quiet minutes the rule asks for, newest
// one minute old.
func (r *restingEnv) quietStretch(t *testing.T, s sampleRow) {
	t.Helper()
	now := time.Now().UTC()
	for i := resting.Samples; i >= 1; i-- {
		r.sample(t, now.Add(-time.Duration(i)*time.Minute), s)
	}
}

// upFor backdates started_at so the machine has been running long enough
// to rest.
func (r *restingEnv) upFor(t *testing.T, d time.Duration) {
	t.Helper()
	if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set started_at = $2 where id = $1", r.p.ID, time.Now().Add(-d)); err != nil {
		t.Fatal(err)
	}
}

// events counts the project's event rows, the ones `repose events` and
// the dashboard show.
func eventRows(t *testing.T, r *restingEnv) int {
	t.Helper()
	var n int
	if err := r.e.h.Pool.QueryRow(r.e.h.Ctx, "select count(*) from events where project_id = $1", r.p.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *restingEnv) project(t *testing.T) map[string]any {
	t.Helper()
	res := r.e.do(t, r.tok, "GET", "/projects/"+r.p.ID.String(), nil)
	if res.status != 200 {
		t.Fatalf("get project: %d %s", res.status, res.raw)
	}
	return res.body
}

// TestRestRuleSendsOnePausePerQuietMachine is the §3 rule as one read:
// fifteen quiet minutes in a row, no op, no gateway session, keep_awake
// off, on a host whose Hello named `pause` (DECISIONS I-818).
func TestRestRuleSendsOnePausePerQuietMachine(t *testing.T) {
	r := newRestingEnv(t, "resta")
	ctx := r.e.h.Ctx
	r.upFor(t, time.Hour)
	r.quietStretch(t, quiet)

	cs, err := r.e.h.Resting.Candidates(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].ProjectID != r.p.ID {
		t.Fatalf("candidates %+v, want just %s", cs, r.p.ID)
	}
	before := eventRows(t, r)
	n, err := r.e.h.Resting.Once(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pass sent %d err %v", n, err)
	}
	// hostd pauses, raises GuestResting, and the marker lands on the row
	// and on the Project, with no event a user reads and no notification
	// (the brief's S6).
	r.e.h.WaitFor("the resting marker", func() bool { return r.e.h.Project(r.p.ID).RestingSince != nil })
	body := r.project(t)
	v, ok := body["resting"].(map[string]any)
	if !ok || v["since"] == nil {
		t.Fatalf("no resting on the project: %v", body["resting"])
	}
	if body["state"] != "running" {
		t.Fatalf("state %v, want running: resting is a marker, never a state", body["state"])
	}
	if after := eventRows(t, r); after != before {
		t.Fatalf("a pause wrote %d events a user reads", after-before)
	}
	// Already resting: the rule does not ask again.
	if cs, err := r.e.h.Resting.Candidates(ctx, time.Now()); err != nil || len(cs) != 0 {
		t.Fatalf("candidates while resting: %+v %v", cs, err)
	}
}

// TestRestRuleHoldsEveryTerm: each term of §3 on its own keeps a machine
// awake. Every case is the quiet stretch with one thing changed.
func TestRestRuleHoldsEveryTerm(t *testing.T) {
	cases := []struct {
		name  string
		rows  sampleRow
		setup func(t *testing.T, r *restingEnv)
	}{
		{"an ssh session", sampleRow{agent: "idle", ssh: 1}, nil},
		{"a tmux client", sampleRow{agent: "idle", tmux: 1}, nil},
		{"an agent at work", sampleRow{agent: "working"}, nil},
		{"an agent in an unknown state", sampleRow{agent: "unknown"}, nil},
		{"guestd not answering", sampleRow{agent: "idle", noGuestd: true}, nil},
		{"CPU over the threshold", sampleRow{agent: "idle", cpuNs: resting.CPUNsPerSample}, nil},
		{"network over the threshold", sampleRow{agent: "idle", netRx: resting.NetBytesPerSample / 2, netTx: resting.NetBytesPerSample / 2}, nil},
		{"keep_awake", quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set keep_awake = true where id = $1", r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"an open gateway session", quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "insert into gateway_sessions (project_id, cert_serial, session_id) values ($1, 7, 'relay')", r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"an op in flight", quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "insert into ops (id, project_id, kind, state, params, created_at) values ($1, $2, 'snapshot', 'pending', '{}'::jsonb, now())", store.NewID(), r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"up for less than the window", quiet, func(t *testing.T, r *restingEnv) {
			r.upFor(t, time.Minute)
		}},
		// An open repose-ask question holds a machine awake (I-822): the
		// guest's clock keeps running while it rests while the sweep
		// that expires questions cannot, so a machine that rested an
		// hour comes back with every open question already past its
		// expiry and the agent waiting on one is told nobody answered.
		// The agent is in needs_input, which the rule otherwise lets
		// rest, so this term is what makes that safe -- and it was the
		// one term with no test.
		{"an open repose-ask question", quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx,
				`insert into questions (id, project_id, guest_id, event_id, agent, text, state, expires_at)
				 values ($1, $2, $3, $4, 'claude', 'which branch?', 'pending', now() + interval '20 minutes')`,
				store.NewID(), r.p.ID, store.NewID(), store.NewID()); err != nil {
				t.Fatal(err)
			}
		}},
		{"the host unreachable", quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set host_unreachable = true where id = $1", r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRestingEnv(t, fmt.Sprintf("term%d", i))
			r.upFor(t, time.Hour)
			r.quietStretch(t, c.rows)
			if c.setup != nil {
				c.setup(t, r)
			}
			cs, err := r.e.h.Resting.Candidates(r.e.h.Ctx, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(cs) != 0 {
				t.Fatalf("%s: the machine was asked to rest anyway", c.name)
			}
		})
	}
}

// TestRestRuleNeedsAFullStretch: fourteen quiet minutes are not fifteen,
// and fifteen spread over an hour are a sampler that skipped.
func TestRestRuleNeedsAFullStretch(t *testing.T) {
	r := newRestingEnv(t, "stretch")
	ctx := r.e.h.Ctx
	r.upFor(t, 3*time.Hour)
	now := time.Now().UTC()
	for i := resting.Samples - 1; i >= 1; i-- {
		r.sample(t, now.Add(-time.Duration(i)*time.Minute), quiet)
	}
	if cs, err := r.e.h.Resting.Candidates(ctx, now); err != nil || len(cs) != 0 {
		t.Fatalf("14 samples: %+v %v", cs, err)
	}
	// The fifteenth, an hour back: the count is right and the window is not.
	r.sample(t, now.Add(-time.Hour), quiet)
	if cs, err := r.e.h.Resting.Candidates(ctx, now); err != nil || len(cs) != 0 {
		t.Fatalf("15 samples over an hour: %+v %v", cs, err)
	}
	r.sample(t, now.Add(-time.Duration(resting.Samples)*time.Minute), quiet)
	if cs, err := r.e.h.Resting.Candidates(ctx, now); err != nil || len(cs) != 1 {
		t.Fatalf("15 quiet minutes in a row: %+v %v", cs, err)
	}
}

// TestRestRuleToleratesADroppedMinuteButNotAStaleSampler: the api drops a
// whole minute for every guest on a host when it cannot write that
// minute's partition, and hostd's own 60 s tick slips because it samples
// guests in series. Window carries slack for a few such minutes; what it
// must not tolerate is a sampler that stopped, because silence says
// nothing about who is attached.
func TestRestRuleToleratesADroppedMinuteButNotAStaleSampler(t *testing.T) {
	r := newRestingEnv(t, "gap")
	ctx := r.e.h.Ctx
	r.upFor(t, 3*time.Hour)
	now := time.Now().UTC()
	// Fifteen quiet samples with two minutes missing in the middle.
	at := 0
	for i := 0; i < resting.Samples; i++ {
		if i == 7 {
			at += 2 // the minutes the sampler did not send
		}
		r.sample(t, now.Add(-time.Duration(at)*time.Minute), quiet)
		at++
	}
	cs, err := r.e.h.Resting.Candidates(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatalf("a stretch with two dropped minutes was refused: %+v", cs)
	}
	// The same stretch, but nothing since: a host that stopped reporting.
	r2 := newRestingEnv(t, "stale")
	r2.upFor(t, 3*time.Hour)
	for i := 0; i < resting.Samples; i++ {
		r2.sample(t, now.Add(-time.Duration(i+12)*time.Minute), quiet)
	}
	cs2, err := r2.e.h.Resting.Candidates(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs2) != 0 {
		t.Fatalf("a machine whose newest sample is 12 minutes old was asked to rest: %+v", cs2)
	}
}

// TestStartOnAFrozenMachineDoesNotRebootIt is the §5 checklist item: a
// resting sample leaves guestdDead false, so `repose start` and the
// dashboard's Start refuse as "already running" instead of enqueueing
// the restart, which is a stop and a start and would lose every process
// (DECISIONS I-818).
func TestStartOnAFrozenMachineDoesNotRebootIt(t *testing.T) {
	// The control first: the same sample without the marker is a dead
	// guestd, and that does reboot.
	dead := newRestingEnv(t, "deadg")
	dead.upFor(t, time.Hour) // a sample older than started_at says nothing (I-225)
	dead.sample(t, time.Now().UTC().Add(-30*time.Second), sampleRow{agent: "idle", noGuestd: true})
	res := dead.e.do(t, dead.tok, "POST", "/projects/"+dead.p.ID.String()+"/start", nil)
	if res.status != 202 || res.body["restart"] != true {
		t.Fatalf("a dead guestd must still restart: %d %s", res.status, res.raw)
	}

	r := newRestingEnv(t, "frozen")
	r.upFor(t, time.Hour)
	// The one sample shape a paused guest produces, and the shape that
	// would still be wrong if hostd ever stopped keeping guestd_ok true:
	// resting with guestd not answering.
	r.sample(t, time.Now().UTC().Add(-30*time.Second), sampleRow{agent: "idle", noGuestd: true, resting: true})
	if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set resting_since = now() where id = $1", r.p.ID); err != nil {
		t.Fatal(err)
	}
	res = r.e.do(t, r.tok, "POST", "/projects/"+r.p.ID.String()+"/start", nil)
	if res.status != 409 {
		t.Fatalf("start on a frozen machine: %d %s, want 409 conflict and no restart", res.status, res.raw)
	}
	var ops int
	if err := r.e.h.Pool.QueryRow(r.e.h.Ctx, "select count(*) from ops where project_id = $1 and kind = 'start'", r.p.ID).Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if ops != 0 {
		t.Fatalf("%d start ops were enqueued for a frozen machine", ops)
	}
	if p := r.e.h.Project(r.p.ID); p.State != "running" || p.RestingSince == nil {
		t.Fatalf("after the refused start: state %s resting_since %v", p.State, p.RestingSince)
	}
}

// TestKeepAwakeTakesEffectAtOnce: the switch is stored, reported, and a
// machine already frozen when it goes on is woken by the request itself
// (DECISIONS I-820).
func TestKeepAwakeTakesEffectAtOnce(t *testing.T) {
	r := newRestingEnv(t, "keepa")
	if v := r.project(t)["keep_awake"]; v != false {
		t.Fatalf("keep_awake %v, want false by default", v)
	}
	r.e.h.Fake.SetResting(r.p.GuestID.String(), true)
	if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set resting_since = now() where id = $1", r.p.ID); err != nil {
		t.Fatal(err)
	}
	res := r.e.do(t, r.tok, "PATCH", "/projects/"+r.p.ID.String(), map[string]any{"keep_awake": true})
	if res.status != 200 {
		t.Fatalf("patch: %d %s", res.status, res.raw)
	}
	p := r.e.h.Project(r.p.ID)
	if !p.KeepAwake {
		t.Fatal("keep_awake was not stored")
	}
	if p.RestingSince != nil {
		t.Fatal("the machine was left frozen after keep_awake went on")
	}
	for _, g := range r.e.h.Fake.Guests() {
		if g.Resting {
			t.Fatal("the guest was left paused on its host")
		}
	}
	if v := r.project(t)["keep_awake"]; v != true {
		t.Fatalf("keep_awake %v after the patch", v)
	}
}

// TestInternalRouteAndWake is the gateway's half: the route carries the
// flag, and the wake resumes the machine and answers whether it was
// frozen (DECISIONS I-818).
func TestInternalRouteAndWake(t *testing.T) {
	r := newRestingEnv(t, "gwwake")
	login := r.p.Slug + ".gwwake"
	route := r.e.internalDo(t, "GET", "/internal/route?login="+login, nil)
	if route.status != 200 || route.body["resting"] != false {
		t.Fatalf("route of an awake machine: %d %s", route.status, route.raw)
	}

	r.e.h.Fake.SetResting(r.p.GuestID.String(), true)
	if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set resting_since = now() where id = $1", r.p.ID); err != nil {
		t.Fatal(err)
	}
	route = r.e.internalDo(t, "GET", "/internal/route?login="+login, nil)
	if route.body["resting"] != true || route.body["state"] != "running" {
		t.Fatalf("route of a frozen machine: %s", route.raw)
	}

	res := r.e.internalDo(t, "POST", "/internal/wake", map[string]any{"project_id": r.p.ID.String(), "reason": "connect"})
	if res.status != 200 || res.body["was_resting"] != true {
		t.Fatalf("wake: %d %s", res.status, res.raw)
	}
	if p := r.e.h.Project(r.p.ID); p.RestingSince != nil {
		t.Fatal("the marker survived the wake")
	}
	// Idempotent: asked for again on a machine that is awake, it answers
	// was_resting false rather than failing, which is what makes the
	// gateway's five-second route cache harmless in both directions.
	res = r.e.internalDo(t, "POST", "/internal/wake", map[string]any{"project_id": r.p.ID.String()})
	if res.status != 200 || res.body["was_resting"] != false {
		t.Fatalf("second wake: %d %s", res.status, res.raw)
	}
}

// TestFrozenStretchStillRaisesTheUnusedNotice is the §5 checklist item:
// a machine that rests still counts as unused, so the 24-hour notice
// (I-262, I-617) still fires once for a forgotten machine. It is what
// keeps the pause from hiding exactly the machines it targets.
func TestFrozenStretchStillRaisesTheUnusedNotice(t *testing.T) {
	r := newRestingEnv(t, "unused")
	ctx := r.e.h.Ctx
	now := time.Now().UTC().Truncate(time.Second)
	started := now.Add(-30 * time.Hour)
	if _, err := r.e.h.Pool.Exec(ctx, "update projects set started_at = $2 where id = $1", r.p.ID, started); err != nil {
		t.Fatal(err)
	}
	// Quiet for fifteen minutes, then frozen for the rest of the day:
	// hostd keeps guestd_ok true and repeats the last signals, so every
	// one of these minutes reads as unused.
	for i := 29; i >= 1; i-- {
		r.sample(t, now.Add(-time.Duration(i)*time.Hour), sampleRow{agent: "idle", resting: true})
	}
	r.sample(t, now.Add(-time.Minute), sampleRow{agent: "idle", resting: true})
	warner := &idle.Warner{Pool: r.e.h.Pool, Events: r.e.h.Events}
	n, err := warner.Run(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("warner over a frozen stretch: n=%d err=%v", n, err)
	}
	// Once per stretch, as before.
	if n, err := warner.Run(ctx, now.Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("warned twice: n=%d err=%v", n, err)
	}
	body := r.project(t)
	if v, ok := body["idle"].(map[string]any); !ok || v["since"] == nil {
		t.Fatalf("a frozen machine stopped being unused: %v", body["idle"])
	}
}

// TestRestingMarkerIsReconciledFromHello: the marker must survive an api
// restart and a lost event, so hostd carries it on every Hello and the
// api writes what the host says (DECISIONS I-818, CLAUDE.md's reconcile
// rule).
func TestRestingMarkerIsReconciledFromHello(t *testing.T) {
	r := newRestingEnv(t, "reconc")
	ctx := r.e.h.Ctx
	// The host has the guest paused and the api's row says nothing: the
	// event went missing, or this api process never saw it.
	r.e.h.Fake.SetResting(r.p.GuestID.String(), true)
	if _, err := r.e.h.Pool.Exec(ctx, "update projects set resting_since = null where id = $1", r.p.ID); err != nil {
		t.Fatal(err)
	}
	r.e.h.ReconnectHost(t)
	r.e.h.WaitFor("the marker from Hello", func() bool { return r.e.h.Project(r.p.ID).RestingSince != nil })

	// The other way round: the row says frozen and the host says awake.
	r.e.h.Fake.SetResting(r.p.GuestID.String(), false)
	if _, err := r.e.h.Pool.Exec(ctx, "update projects set resting_since = now() where id = $1", r.p.ID); err != nil {
		t.Fatal(err)
	}
	r.e.h.ReconnectHost(t)
	r.e.h.WaitFor("the marker cleared from Hello", func() bool { return r.e.h.Project(r.p.ID).RestingSince == nil })
}

// TestAnswerWakesAFrozenMachineFirst: a paused agent waiting at
// `needs_input` is only safe to leave because the answer wakes it
// (DECISIONS I-818). The delivery goes straight to the host, outside the
// op engine, so the wake is asked for there too.
func TestAnswerWakesAFrozenMachineFirst(t *testing.T) {
	a := newAskEnv(t, "frozenask")
	qid := a.ask(t, "Drop the legacy sessions table?", []string{"yes", "no"}, 1800)
	// The machine freezes while the question waits.
	a.h.Fake.SetResting(a.p.GuestID.String(), true)
	a.h.WaitFor("the resting marker", func() bool { return a.h.Project(a.p.ID).RestingSince != nil })
	r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/questions/"+qid.String()+"/answer", map[string]any{"answer": "yes"})
	if r.status != 200 {
		t.Fatalf("answer: %d %s", r.status, r.raw)
	}
	// The question is still the project's: the pause is not a state
	// change, so nothing cancelled it.
	var state string
	if err := a.h.Pool.QueryRow(a.h.Ctx, "select state from questions where id = $1", qid).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "answered" {
		t.Fatalf("question state %s: a pause must not cancel a pending question", state)
	}
	if err := a.h.Questions.Once(a.h.Ctx); err != nil {
		t.Fatal(err)
	}
	a.h.WaitFor("the answer at the guest", func() bool {
		got := a.h.Fake.Answer(qid.String())
		return got != nil && got.Status == "answered" && got.Answer == "yes"
	})
	for _, g := range a.h.Fake.Guests() {
		if g.Resting {
			t.Fatal("the answer was delivered without waking the machine")
		}
	}
}
