package resume_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/lifecycle"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/resume"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

type env struct {
	t   *testing.T
	h   *apitest.Harness
	r   *resume.Resumer
	ctx context.Context

	mu  sync.Mutex
	now time.Time
}

// newEnv is the control plane with one fake host and the resumer wired as
// app.go wires it. enforce turns the billing gate on (a configured Polar);
// off, only its suspended and past_due rules matter to nobody.
func newEnv(t *testing.T, enforce bool) *env {
	t.Helper()
	h := apitest.New(t, apitest.Options{})
	e := &env{t: t, h: h, ctx: h.Ctx, now: time.Now()}
	e.wire(enforce)
	return e
}

func (e *env) wire(enforce bool) {
	gate := billing.NewGate(e.h.Pool, billing.Config{AccessToken: "polar_oat_test", Env: billing.EnvSandbox, DashboardURL: "https://repose.herakraft.co", Enforce: enforce}, e.h.Metrics, e.h.Log)
	e.r = resume.New(e.h.Pool, e.h.Engine, gate, e.h.Metrics, e.h.Log)
	e.r.Now = e.clock
	e.r.Enabled = func() bool { return true }
	e.h.SetHelloHook(e.r.OnHello)
	e.h.Engine.SetOnFinished(e.r.OnOpFinished)
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

// reboot restarts the fake host under the running guests and reconnects
// it, then waits for the Hello's rows.
func (e *env) reboot(boot string, want int) {
	e.t.Helper()
	e.h.Fake.Reboot(boot, time.Date(2026, 10, 10, 5, 46, 12, 0, time.UTC))
	e.h.ReconnectHost(e.t)
	e.h.WaitFor("resume rows", func() bool { return e.rows(boot) == want })
}

func (e *env) rows(boot string) int {
	var n int
	if err := e.h.Pool.QueryRow(e.ctx, "select count(*) from guest_resumes where boot_id = $1", boot).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) tick() {
	e.t.Helper()
	if err := e.r.Tick(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

type rowState struct {
	outcome, reason string
	decided         bool
	opID            *uuid.UUID
	attempts        int
}

func (e *env) row(pid uuid.UUID) rowState {
	e.t.Helper()
	var s rowState
	var reason *string
	var decided *time.Time
	if err := e.h.Pool.QueryRow(e.ctx, "select outcome, reason, decided_at, op_id, attempts from guest_resumes where project_id = $1 order by created_at desc limit 1", pid).Scan(&s.outcome, &reason, &decided, &s.opID, &s.attempts); err != nil {
		e.t.Fatalf("row of %s: %v", pid, err)
	}
	if reason != nil {
		s.reason = *reason
	}
	s.decided = decided != nil
	return s
}

// settle ticks, letting each start the tick enqueued finish, until every
// row is closed or n ticks have run.
func (e *env) settle(n int) {
	e.t.Helper()
	for i := 0; i < n; i++ {
		e.tick()
		var open []uuid.UUID
		rs, err := e.h.Pool.Query(e.ctx, "select op_id from guest_resumes where decided_at is null and op_id is not null")
		if err != nil {
			e.t.Fatal(err)
		}
		for rs.Next() {
			var id uuid.UUID
			_ = rs.Scan(&id)
			open = append(open, id)
		}
		rs.Close()
		for _, id := range open {
			e.h.WaitOp(id)
		}
		var left int
		_ = e.h.Pool.QueryRow(e.ctx, "select count(*) from guest_resumes where decided_at is null").Scan(&left)
		if left == 0 {
			e.tick() // the notices
			return
		}
	}
}

func (e *env) startOps(pid uuid.UUID) int {
	var n int
	if err := e.h.Pool.QueryRow(e.ctx, "select count(*) from ops where project_id = $1 and kind = 'start'", pid).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) notices(u uuid.UUID) []string {
	e.t.Helper()
	rs, err := e.h.Pool.Query(e.ctx, "select summary from events where user_id = $1 and project_id is null and kind = 'machines_restarted' order by ts", u)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var s string
		_ = rs.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestInterruptedGuestIsStartedOnce(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	stopped := e.h.CreateRunning(u, "api")
	e.h.WaitOp(e.h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &stopped.ID, Phases: ops.PlanStop()}))
	e.reboot("boot-2", 1)
	if e.rows("boot-2") != 1 {
		t.Fatal("a machine stopped before the restart got a row")
	}
	// The Hello set the project from the host's word.
	if st := e.h.Project(p.ID).State; st != "stopped" {
		t.Fatalf("project after Hello: %s", st)
	}
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted || !r.decided || r.attempts != 1 {
		t.Fatalf("row %+v", r)
	}
	if st := e.h.Project(p.ID).State; st != "running" {
		t.Fatalf("project after the resume: %s", st)
	}
	// The same Hello again, and more ticks: no second start.
	e.h.ReconnectHost(t)
	e.settle(3)
	if n := e.startOps(p.ID); n != 1 {
		t.Fatalf("%d start ops, want 1", n)
	}
	// The project event, which notifies nobody.
	var kind string
	if err := e.h.Pool.QueryRow(e.ctx, "select kind from events where project_id = $1 and kind = 'host_restarted'", p.ID).Scan(&kind); err != nil {
		t.Fatalf("no host_restarted event: %v", err)
	}
	var outbox int
	_ = e.h.Pool.QueryRow(e.ctx, "select count(*) from events_outbox o join events ev on ev.id = o.event_id where ev.project_id = $1 and ev.kind = 'host_restarted'", p.ID).Scan(&outbox)
	if outbox != 0 {
		t.Fatal("host_restarted reached a channel")
	}
	// The op says why it ran.
	var resumeParam bool
	_ = e.h.Pool.QueryRow(e.ctx, "select coalesce((params->>'resume')::boolean, false) from ops where project_id = $1 and kind = 'start'", p.ID).Scan(&resumeParam)
	if !resumeParam {
		t.Fatal("the resume's start op does not carry resume")
	}
	// A later restart is a new boot: one more start.
	e.reboot("boot-3", 1)
	e.settle(5)
	if n := e.startOps(p.ID); n != 2 {
		t.Fatalf("%d start ops after a second restart, want 2", n)
	}
}

func TestResumeUsesTheStartGate(t *testing.T) {
	e := newEnv(t, true)
	held := e.h.NewUser("hal")
	_, _ = e.h.Pool.Exec(e.ctx, "update users set billing_status = 'exempt' where id = $1", held.ID)
	hp := e.h.CreateRunning(held, "miner")
	if _, err := e.h.Pool.Exec(e.ctx, "insert into abuse_events (id, project_id, user_id, kind, detail, hold) values ($1, $2, $3, 'miner', '{\"process\":\"xmrig\"}', true)", store.NewID(), hp.ID, held.ID); err != nil {
		t.Fatal(err)
	}
	sus := e.h.NewUser("sue")
	_, _ = e.h.Pool.Exec(e.ctx, "update users set billing_status = 'exempt' where id = $1", sus.ID)
	sp := e.h.CreateRunning(sus, "web")
	_, _ = e.h.Pool.Exec(e.ctx, "update users set billing_status = 'suspended', suspended_at = now() where id = $1", sus.ID)
	e.reboot("boot-2", 2)
	e.settle(5)
	if r := e.row(hp.ID); r.outcome != resume.OutcomeRefused || !strings.Contains(r.reason, "is on hold") {
		t.Fatalf("held project: %+v", r)
	}
	if r := e.row(sp.ID); r.outcome != resume.OutcomeRefused || !strings.Contains(r.reason, "suspended") {
		t.Fatalf("suspended account: %+v", r)
	}
	for _, pid := range []uuid.UUID{hp.ID, sp.ID} {
		if n := e.startOps(pid); n != 0 {
			t.Fatalf("a refused resume enqueued %d starts", n)
		}
	}
	// The user hears why, and how to start it.
	if ns := e.notices(held.ID); len(ns) != 1 || !strings.Contains(ns[0], "miner: not started: miner is on hold") || !strings.Contains(ns[0], "`repose start miner` starts it") {
		t.Fatalf("notice %q", ns)
	}
}

// subscribe gives u a live subscription on plan in status.
func subscribe(t *testing.T, e *env, u *store.User, plan, status string) {
	t.Helper()
	p, _ := billing.PlanByID(plan)
	start := time.Now().Add(-24 * time.Hour)
	if _, err := e.h.Pool.Exec(e.ctx, `insert into subscriptions (id, user_id, customer_id, plan, status, seats, period_start, period_end, next_billed_at, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $8, $7)`, "sub_"+u.Handle, u.ID, "ctm_"+u.Handle, plan, status, p.Seats, start, start.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	bs := "active"
	if status == billing.StatusPastDue {
		bs = "past_due"
	}
	if _, err := e.h.Pool.Exec(e.ctx, "update users set billing_status = $2 where id = $1", u.ID, bs); err != nil {
		t.Fatal(err)
	}
}

func TestResumePastDueStillRestarts(t *testing.T) {
	e := newEnv(t, true)
	u := e.h.NewUser("pat")
	_, _ = e.h.Pool.Exec(e.ctx, "update users set billing_status = 'exempt' where id = $1", u.ID)
	p := e.h.CreateRunning(u, "todo-app")
	_, _ = e.h.Pool.Exec(e.ctx, "update users set billing_status = 'trial' where id = $1", u.ID)
	subscribe(t, e, u, "pro", billing.StatusPastDue)
	e.reboot("boot-2", 1)
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted {
		t.Fatalf("past_due: %+v", r)
	}
	// The owner's own start is still refused on past_due.
	e.h.WaitOp(e.h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &p.ID, Phases: ops.PlanStop()}))
	err := db.InTx(e.ctx, e.h.Pool, func(tx db.Tx) error {
		_, err := lifecycle.Start(e.ctx, tx, lifecycle.Deps{Pool: e.h.Pool, Engine: e.h.Engine, Gate: billing.NewGate(e.h.Pool, billing.Config{AccessToken: "x", Enforce: true}, e.h.Metrics, e.h.Log)}, user(t, e, u.ID), p.ID, lifecycle.Options{})
		return err
	})
	var ref *lifecycle.Refusal
	if !errors.As(err, &ref) || ref.Reason != billing.ReasonPastDue {
		t.Fatalf("user start on past_due: %v", err)
	}
}

func user(t *testing.T, e *env, id uuid.UUID) *store.User {
	t.Helper()
	u, err := store.GetUser(e.ctx, e.h.Pool, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestResumeWaitsForThePool(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	// The host comes back with its pool down (DECISIONS I-644): its Hello
	// says so, and hosts.pool_unavailable follows it.
	e.h.Fake.SetPoolUnavailable(true)
	e.reboot("boot-2", 1)
	var down bool
	if err := e.h.Pool.QueryRow(e.ctx, "select pool_unavailable from hosts").Scan(&down); err != nil || !down {
		t.Fatalf("hosts.pool_unavailable after the Hello: %v, %v", down, err)
	}
	e.tick()
	e.tick()
	if r := e.row(p.ID); r.outcome != resume.OutcomePending || r.attempts != 0 || e.startOps(p.ID) != 0 {
		t.Fatalf("pool down: %+v, %d starts", r, e.startOps(p.ID))
	}
	// The pool is back; the next heartbeat would say so.
	e.h.Fake.SetPoolUnavailable(false)
	_, _ = e.h.Pool.Exec(e.ctx, "update hosts set pool_unavailable = false")
	// A start that fails because the guest's volume would not activate
	// waits too, and is not counted.
	e.h.Fake.SetFail("StartGuest", "volume_unavailable")
	e.tick()
	e.h.WaitOp(*e.row(p.ID).opID)
	e.tick()
	if r := e.row(p.ID); r.outcome != resume.OutcomePending || r.decided {
		t.Fatalf("volume_unavailable: %+v", r)
	}
	e.h.Fake.SetFail("StartGuest", "")
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted {
		t.Fatalf("after the pool came back: %+v", r)
	}
}

func TestResumeWaitsOnCapacity(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	e.reboot("boot-2", 1)
	e.h.Fake.SetFail("StartGuest", "insufficient_capacity")
	for i := 0; i < 3; i++ {
		e.tick()
		if r := e.row(p.ID); r.opID != nil {
			e.h.WaitOp(*r.opID)
		}
	}
	e.tick()
	if r := e.row(p.ID); r.decided || r.outcome != resume.OutcomePending {
		t.Fatalf("capacity closed the row: %+v", r)
	}
	e.h.Fake.SetFail("StartGuest", "")
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted {
		t.Fatalf("after capacity: %+v", r)
	}
	// A Ready timeout is tried once more, then the row closes failed.
	e.reboot("boot-3", 1)
	e.h.Fake.SetFail("StartGuest", "guest_unresponsive")
	e.settle(6)
	if r := e.row(p.ID); r.outcome != resume.OutcomeFailed || r.attempts != 2 || !strings.Contains(r.reason, "did not answer") {
		t.Fatalf("timeouts: %+v", r)
	}
}

func TestResumeAfterBaseBumpOpOpenAtHello(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	// The 04:00 base sweep's build and apply was open when the host went
	// down: Hello leaves a project with an open op alone, so it still
	// reads running.
	e.h.StopEngine()
	pid := p.ID
	opID, err := e.h.Engine.Enqueue(e.ctx, e.h.Pool, ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, Phases: ops.PlanBuild(true)}, false)
	if err != nil {
		t.Fatal(err)
	}
	e.reboot("boot-2", 1)
	if st := e.h.Project(p.ID).State; st != "running" {
		t.Fatalf("project %s, want the stale running", st)
	}
	e.tick()
	if r := e.row(p.ID); r.outcome != resume.OutcomePending || e.startOps(p.ID) != 0 {
		t.Fatalf("with an open op: %+v", r)
	}
	// The op ends.
	if _, err := e.h.Pool.Exec(e.ctx, "update ops set state = 'done', finished_at = now() where id = $1", opID); err != nil {
		t.Fatal(err)
	}
	op, _ := store.GetOp(e.ctx, e.h.Pool, opID)
	e.r.OnOpFinished(e.ctx, op)
	if st := e.h.Project(p.ID).State; st != "stopped" {
		t.Fatalf("after the op ended the project reads %s, not the Hello's stopped", st)
	}
	e.h.StartEngine(ops.Config{BaseRef: "deadbeef"})
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted {
		t.Fatalf("after the op: %+v", r)
	}
}

func TestResumeSkipsStopAndDestroyWhileUnreachable(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	stopP := e.h.CreateRunning(u, "stopme")
	rmP := e.h.CreateRunning(u, "rmme")
	keep := e.h.CreateRunning(u, "keep")
	// The host goes quiet; the api marks it unreachable.
	e.h.DisconnectHost()
	if _, err := e.h.Pool.Exec(e.ctx, "update hosts set last_heartbeat_at = now() - interval '5 minutes'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.HostMgr.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	// The owner stops one and removes another; both fail with
	// host_unreachable.
	e.h.StartEngine(ops.Config{BaseRef: "deadbeef", HostUnreachableGrace: time.Millisecond})
	e.wire(false)
	stopOp := e.h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &stopP.ID, Phases: ops.PlanStop()})
	rmOp := e.h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &rmP.ID, Phases: ops.PlanDestroy(rmP)})
	for _, id := range []uuid.UUID{stopOp, rmOp} {
		if op := e.h.WaitOp(id); op.State != "error" || op.Error["code"] != "host_unreachable" {
			t.Fatalf("op %s: %s %v", op.Kind, op.State, op.Error)
		}
	}
	// It comes back from a reboot with all three marked.
	e.reboot("boot-2", 3)
	e.settle(5)
	for _, pid := range []uuid.UUID{stopP.ID, rmP.ID} {
		if r := e.row(pid); r.outcome != resume.OutcomeUserStopped {
			t.Fatalf("%s: %+v", pid, r)
		}
		if n := e.startOps(pid); n != 0 {
			t.Fatalf("a machine its owner stopped was started (%d)", n)
		}
	}
	if r := e.row(keep.ID); r.outcome != resume.OutcomeStarted {
		t.Fatalf("untouched machine: %+v", r)
	}
	// The notice lists only the machine it started.
	ns := e.notices(u.ID)
	if len(ns) != 1 || !strings.Contains(ns[0], "keep: running again") || strings.Contains(ns[0], "stopme") || strings.Contains(ns[0], "rmme") {
		t.Fatalf("notice %q", ns)
	}
}

func TestResumeWhenStateStillRunning(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	e.reboot("boot-2", 1)
	// The project row still says running (a missed event, a stale op).
	if _, err := e.h.Pool.Exec(e.ctx, "update projects set state = 'running' where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted || e.startOps(p.ID) != 1 {
		t.Fatalf("stale running: %+v", r)
	}
}

func TestResumeRacesHTTPRm(t *testing.T) {
	for i := 0; i < 5; i++ {
		e := newEnv(t, false)
		u := e.h.NewUser("ann")
		p := e.h.CreateRunning(u, "todo-app")
		e.h.StopEngine() // nothing runs the ops: only what got enqueued counts
		e.reboot("boot-2", 1)
		// `repose rm` as the HTTP route does it, racing the tick.
		var wg sync.WaitGroup
		wg.Add(2)
		var rmErr error
		go func() {
			defer wg.Done()
			cur := e.h.Project(p.ID)
			rmErr = db.InTx(e.ctx, e.h.Pool, func(tx db.Tx) error {
				if _, err := e.h.Engine.Enqueue(e.ctx, tx, ops.NewOp{Kind: ops.KindDestroy, ProjectID: &p.ID, Phases: ops.PlanDestroy(cur)}, false); err != nil {
					return err
				}
				return store.SetProjectState(e.ctx, tx, p.ID, "destroying")
			})
		}()
		go func() {
			defer wg.Done()
			_ = e.r.Tick(e.ctx)
		}()
		wg.Wait()
		var open int
		_ = e.h.Pool.QueryRow(e.ctx, "select count(*) from ops where project_id = $1 and state in ('pending', 'running')", p.ID).Scan(&open)
		if open != 1 {
			t.Fatalf("run %d: %d open ops after a racing rm and resume (rm: %v)", i, open, rmErr)
		}
		e.tick()
		r := e.row(p.ID)
		switch {
		case rmErr == nil && r.outcome != resume.OutcomeUserStopped && r.outcome != resume.OutcomeDestroyed && r.outcome != resume.OutcomePending:
			t.Fatalf("run %d: rm won but the row is %+v", i, r)
		case rmErr != nil && (!errors.Is(rmErr, ops.ErrOpInProgress) || r.outcome != resume.OutcomeStarting):
			t.Fatalf("run %d: rm lost with %v and the row is %+v", i, rmErr, r)
		}
	}
}

func TestResumeBootsCurrentClosure(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	// A built revision waits for the next start to apply it.
	if _, err := e.h.Pool.Exec(e.ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure, created_at) values ($1, $2, '{ pkgs, ... }: { }', 'built', '/nix/store/11111111111111111111111111111111-nixos-system-next', now() + interval '1 second')", store.NewID(), p.ID); err != nil {
		t.Fatal(err)
	}
	if pending, err := ops.PendingRevision(e.ctx, e.h.Pool, e.h.Project(p.ID)); err != nil || !pending {
		t.Fatalf("pending revision %v %v", pending, err)
	}
	e.reboot("boot-2", 1)
	e.settle(5)
	r := e.row(p.ID)
	if r.outcome != resume.OutcomeStarted {
		t.Fatalf("row %+v", r)
	}
	op, err := store.GetOp(e.ctx, e.h.Pool, *r.opID)
	if err != nil {
		t.Fatal(err)
	}
	if ph, _ := op.Params["phases"].([]any); len(ph) != 1 || ph[0] != ops.PhaseStartGuest {
		t.Fatalf("resume phases %v; a reboot for a change is asked for first", op.Params["phases"])
	}
}

func TestResumeSkipsForeignGuest(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	other := store.NewID()
	if _, err := e.h.Pool.Exec(e.ctx, "insert into hosts (id, name, state) values ($1, 'host-02', 'ready')", other); err != nil {
		t.Fatal(err)
	}
	h := &hostdv1.Hello{HostId: other.String(), HostBootId: "evil", Guests: []*hostdv1.GuestStatus{{GuestId: p.GuestID.String(), State: "stopped", InterruptedBootId: "evil", InterruptedAt: time.Now().Unix()}}}
	if err := e.r.OnHello(e.ctx, other, h); err != nil {
		t.Fatal(err)
	}
	if n := e.rows("evil"); n != 0 {
		t.Fatalf("a host marked a guest placed on another host: %d rows", n)
	}
}

func TestResumeInsertFailureClosesStream(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	if _, err := e.h.Pool.Exec(e.ctx, "alter table guest_resumes rename to guest_resumes_gone"); err != nil {
		t.Fatal(err)
	}
	h := &hostdv1.Hello{HostBootId: "boot-2", Guests: []*hostdv1.GuestStatus{{GuestId: p.GuestID.String(), State: "stopped", InterruptedBootId: "boot-2", InterruptedAt: time.Now().Unix()}}}
	if err := e.r.OnHello(e.ctx, e.h.HostID, h); err == nil {
		t.Fatal("a failed insert was not reported, so the Hello would be taken and the mark lost")
	}
	// hostmgr closes the stream on that error (TestHelloRefusedClosesTheStream)
	// and the host sends the same Hello when it reconnects: nothing is lost.
	if _, err := e.h.Pool.Exec(e.ctx, "alter table guest_resumes_gone rename to guest_resumes"); err != nil {
		t.Fatal(err)
	}
	if err := e.r.OnHello(e.ctx, e.h.HostID, h); err != nil {
		t.Fatal(err)
	}
	if n := e.rows("boot-2"); n != 1 {
		t.Fatalf("the resent Hello wrote %d rows", n)
	}
}

func TestResumeSurvivesEnqueueFailure(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	e.reboot("boot-2", 1)
	real := e.r.Start
	e.r.Start = func(ctx context.Context, tx db.Tx, d lifecycle.Deps, u *store.User, id uuid.UUID, o lifecycle.Options) (*lifecycle.Result, error) {
		if _, err := real(ctx, tx, d, u, id, o); err != nil {
			return nil, err
		}
		return nil, errors.New("the api died between the enqueue and the commit")
	}
	e.tick()
	if r := e.row(p.ID); r.outcome != resume.OutcomePending || r.opID != nil || e.startOps(p.ID) != 0 {
		t.Fatalf("after the failure: %+v, %d starts", r, e.startOps(p.ID))
	}
	e.r.Start = real
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted || e.startOps(p.ID) != 1 {
		t.Fatalf("next tick: %+v", r)
	}
}

func TestResumePausedBySwitch(t *testing.T) {
	t.Setenv(resume.EnvSwitch, "off")
	e := newEnv(t, false)
	e.r.Enabled = resume.New(e.h.Pool, e.h.Engine, nil, nil, e.h.Log).Enabled
	if e.r.Enabled() {
		t.Fatal("REPOSE_RESUME=off reads as on")
	}
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	e.reboot("boot-2", 1)
	e.tick()
	e.tick()
	if r := e.row(p.ID); r.outcome != resume.OutcomePending || e.startOps(p.ID) != 0 {
		t.Fatalf("paused: %+v", r)
	}
	t.Setenv(resume.EnvSwitch, "")
	if !e.r.Enabled() {
		t.Fatal("unset reads as off")
	}
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeStarted {
		t.Fatalf("switched on: %+v", r)
	}
}

func TestResumeWindowAndTempExpired(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	late := e.h.CreateRunning(u, "late")
	tmp := e.h.CreateRunning(u, "tmp")
	if _, err := e.h.Pool.Exec(e.ctx, "update projects set expires_at = now() + interval '1 hour' where id = $1", tmp.ID); err != nil {
		t.Fatal(err)
	}
	e.reboot("boot-2", 2)
	// The api was down for longer than the window.
	e.advance(resume.Window + time.Minute)
	e.settle(3)
	if r := e.row(late.ID); r.outcome != resume.OutcomeWindowPassed {
		t.Fatalf("late: %+v", r)
	}
	if n := e.startOps(late.ID); n != 0 {
		t.Fatalf("started past the window: %d", n)
	}
	if r := e.row(tmp.ID); r.outcome != resume.OutcomeWindowPassed && r.outcome != resume.OutcomeTempExpired {
		t.Fatalf("tmp: %+v", r)
	}
	// Inside the window, a temporary machine past its end is not started.
	tmp2 := e.h.CreateRunning(u, "tmp2")
	if _, err := e.h.Pool.Exec(e.ctx, "update projects set expires_at = now() + interval '30 minutes' where id = $1", tmp2.ID); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.now = time.Now().Add(time.Hour)
	e.mu.Unlock()
	e.h.Fake.Reboot("boot-3", time.Now())
	e.h.ReconnectHost(t)
	e.h.WaitFor("tmp2 row", func() bool { return e.rows("boot-3") >= 1 })
	e.settle(3)
	if r := e.row(tmp2.ID); r.outcome != resume.OutcomeTempExpired {
		t.Fatalf("tmp2: %+v", r)
	}
	ns := strings.Join(e.notices(u.ID), "\n---\n")
	if len(e.notices(u.ID)) != 2 || !strings.Contains(ns, "late: not started: the server was back more than 6 hours after it stopped. `repose start late` starts it.") || !strings.Contains(ns, "tmp2: not started: it was temporary and its time ran out.") {
		t.Fatalf("notices %q", ns)
	}
}

func TestResumePacedPerTick(t *testing.T) {
	e := newEnv(t, false)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		e.h.CreateRunning(e.h.NewUser("u"+name), name)
	}
	e.reboot("boot-2", 5)
	e.h.StopEngine()
	e.tick()
	var starting int
	_ = e.h.Pool.QueryRow(e.ctx, "select count(*) from guest_resumes where outcome = 'starting'").Scan(&starting)
	if starting != resume.PerHostPerTick {
		t.Fatalf("first tick started %d, want %d", starting, resume.PerHostPerTick)
	}
	e.tick()
	_ = e.h.Pool.QueryRow(e.ctx, "select count(*) from guest_resumes where outcome = 'starting'").Scan(&starting)
	if starting != 5 {
		t.Fatalf("second tick: %d starting, want 5", starting)
	}
}

func TestOneNoticePerUser(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	e.h.CreateRunning(u, "todo-app")
	e.h.CreateRunning(u, "api")
	_, _ = e.h.Pool.Exec(e.ctx, "update projects set multiplexer = 'herdr' where slug = 'api'")
	_, _ = e.h.Pool.Exec(e.ctx, "update users set ntfy_url = 'https://ntfy.sh/x' where id = $1", u.ID)
	other := e.h.NewUser("bob")
	e.h.CreateRunning(other, "web")
	_, _ = e.h.Pool.Exec(e.ctx, "update users set notify_email = false where id = $1", other.ID)
	e.reboot("boot-2", 3)
	e.settle(5)
	e.h.ReconnectHost(t)
	e.settle(3)
	ns := e.notices(u.ID)
	want := "The server running your machines restarted; it was back at 05:46 UTC.\napi: running again.\ntodo-app: running again; its last agent turn was interrupted."
	if len(ns) != 1 || ns[0] != want {
		t.Fatalf("ann's notices %q, want one %q", ns, want)
	}
	var channels []string
	rs, _ := e.h.Pool.Query(e.ctx, "select o.channel from events_outbox o join events ev on ev.id = o.event_id where ev.user_id = $1 and ev.kind = 'machines_restarted' order by 1", u.ID)
	for rs.Next() {
		var c string
		_ = rs.Scan(&c)
		channels = append(channels, c)
	}
	rs.Close()
	if strings.Join(channels, ",") != "email,ntfy" {
		t.Fatalf("ann's channels %v", channels)
	}
	if ns := e.notices(other.ID); len(ns) != 1 {
		t.Fatalf("bob's notices %q", ns)
	}
	var bobOutbox int
	_ = e.h.Pool.QueryRow(e.ctx, "select count(*) from events_outbox o join events ev on ev.id = o.event_id where ev.user_id = $1", other.ID).Scan(&bobOutbox)
	if bobOutbox != 0 {
		t.Fatal("notify_email off still queued an email")
	}
}

func TestNoticeAtTenMinutesListsWaiting(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	e.h.CreateRunning(u, "todo-app")
	e.h.CreateRunning(u, "api")
	e.reboot("boot-2", 2)
	e.h.Fake.SetFail("StartGuest", "insufficient_capacity")
	_, _ = e.h.Pool.Exec(e.ctx, "update hosts set state = state") // nothing; capacity keeps both waiting
	e.tick()
	if ns := e.notices(u.ID); len(ns) != 0 {
		t.Fatalf("a notice before 10 minutes with rows open: %q", ns)
	}
	e.advance(resume.NoticeAfter + time.Second)
	e.tick()
	ns := e.notices(u.ID)
	if len(ns) != 1 || !strings.Contains(ns[0], "api: waiting to start. `repose start api` starts it now.") || !strings.Contains(ns[0], "todo-app: waiting to start") {
		t.Fatalf("notice %q", ns)
	}
	// They start later; no second notice.
	e.h.Fake.SetFail("StartGuest", "")
	e.settle(6)
	if ns := e.notices(u.ID); len(ns) != 1 {
		t.Fatalf("a second notice: %q", ns)
	}
}

func TestNoticeText(t *testing.T) {
	back := time.Date(2026, 10, 10, 5, 46, 12, 0, time.UTC)
	got := resume.NoticeText(back,
		resume.Line{Slug: "todo-app", Outcome: resume.OutcomeStarted, Mux: "tmux"},
		resume.Line{Slug: "api", Outcome: resume.OutcomeRefused, Reason: "Your account is suspended. Pay at https://repose.herakraft.co/billing to lift it, or email support."},
		resume.Line{Slug: "gone", Outcome: resume.OutcomeDestroyed},
		resume.Line{Slug: "halted", Outcome: resume.OutcomeUserStopped},
	)
	want := "The server running your machines restarted; it was back at 05:46 UTC.\n" +
		"api: not started: Your account is suspended. Pay at https://repose.herakraft.co/billing to lift it, or email support. `repose start api` starts it.\n" +
		"todo-app: running again; its last agent turn was interrupted."
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if s := resume.NoticeText(back, resume.Line{Slug: "x", Outcome: resume.OutcomeUserStopped}); s != "" {
		t.Fatalf("only machines the owner stopped: %q", s)
	}
}

// A start that failed on capacity waits and is not counted: it does not
// use up the one more try a Ready timeout gets. After a host restart,
// with every guest starting at once, a machine may first find the host
// full and then boot slowly.
func TestReadyRetryNotUsedByCapacity(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	e.reboot("boot-2", 1)
	// round runs the tick that enqueues a start, lets it fail, and the
	// tick that reads the failure.
	round := func() rowState {
		e.tick()
		r := e.row(p.ID)
		if r.opID == nil {
			t.Fatalf("no start enqueued: %+v", r)
		}
		e.h.WaitOp(*r.opID)
		e.tick()
		return e.row(p.ID)
	}
	e.h.Fake.SetFail("StartGuest", "insufficient_capacity")
	for i := 0; i < 2; i++ {
		if r := round(); r.decided || r.outcome != resume.OutcomePending {
			t.Fatalf("capacity %d: %+v", i+1, r)
		}
	}
	e.h.Fake.SetFail("StartGuest", "guest_unresponsive")
	if r := round(); r.decided || r.outcome != resume.OutcomePending {
		t.Fatalf("the first Ready timeout closed the row: %+v", r)
	}
	if r := round(); r.outcome != resume.OutcomeFailed || r.attempts != 4 {
		t.Fatalf("the second Ready timeout: %+v", r)
	}
}

// A start the owner asked for before the host went down, still open at
// the Hello and done after it, is the owner's: the row closes
// already_running and no second start is enqueued.
func TestResumeOwnersStartOpenAtHello(t *testing.T) {
	e := newEnv(t, false)
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	e.h.WaitOp(e.h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &p.ID, Phases: ops.PlanStop()}))
	e.h.StopEngine()
	startID := e.h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &p.ID, Phases: ops.PlanStart(false)})
	// hostd had the guest starting when the host went down.
	if _, err := e.h.Pool.Exec(e.ctx, "update projects set state = 'starting' where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	e.h.Fake.SetInterrupted(p.GuestID.String(), "boot-2", time.Now())
	e.reboot("boot-2", 1)
	e.h.StartEngine(ops.Config{BaseRef: "deadbeef"})
	e.wire(false)
	if op := e.h.WaitOp(startID); op.State != "done" {
		t.Fatalf("owner's start: %s %v", op.State, op.Error)
	}
	e.settle(5)
	if r := e.row(p.ID); r.outcome != resume.OutcomeAlreadyRunning {
		t.Fatalf("row %+v", r)
	}
	if n := e.startOps(p.ID); n != 1 {
		t.Fatalf("%d start ops, want the owner's only", n)
	}
	if st := e.h.Project(p.ID).State; st != "running" {
		t.Fatalf("project %s", st)
	}
}

// Another op that ends while a row is still pending leaves a machine
// its owner started meanwhile running.
func TestOpFinishedKeepsOwnersStartedMachine(t *testing.T) {
	e := newEnv(t, false)
	e.r.Enabled = func() bool { return false }
	u := e.h.NewUser("ann")
	p := e.h.CreateRunning(u, "todo-app")
	e.reboot("boot-2", 1)
	if op := e.h.WaitOp(e.h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &p.ID, Phases: ops.PlanStart(false)})); op.State != "done" {
		t.Fatalf("owner's start: %s %v", op.State, op.Error)
	}
	if st := e.h.Project(p.ID).State; st != "running" {
		t.Fatalf("after the owner's start: %s", st)
	}
	pid := p.ID
	e.r.OnOpFinished(e.ctx, &store.Op{Kind: ops.KindBuild, ProjectID: &pid, State: "done"})
	if st := e.h.Project(p.ID).State; st != "running" {
		t.Fatalf("a build that ended set the owner's running machine to %s", st)
	}
}
