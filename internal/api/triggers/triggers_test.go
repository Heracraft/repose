package triggers_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/triggers"
	"github.com/heracraft/repose/internal/api/webhooks"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/kv"
	"github.com/heracraft/repose/internal/triggerrun"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

// newStores builds a trigger store and the webhooks store behind it,
// sharing one key service: two kv.New() instances hold different keys,
// so a token one wrote the other cannot read.
func newStores(t *testing.T, pool *db.Pool) (*triggers.Store, *webhooks.Store) {
	t.Helper()
	hooks := webhooks.New(pool, secrets.New(pool, kv.New()), "hooks.test")
	return triggers.New(pool, hooks), hooks
}

func newStore(t *testing.T, pool *db.Pool) *triggers.Store {
	t.Helper()
	s, _ := newStores(t, pool)
	return s
}

func newProject(t *testing.T, pool *db.Pool) (userID, projectID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid := uuid.Must(uuid.NewV7())
	pid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, $2)", uid, "h-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes)
		values ($1, $2, 'p', $3, 'large', 'running', 1)`, pid, uid, "p-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	return uid, pid
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

// Turning a schedule on arms its clock from now. Without this a trigger
// off for a week has a fire a week in the past, and the first thing it
// does when the user turns it on is run an agent nobody asked for
// (I-1252). `repose triggers on` is POST /triggers with `enabled`, so
// that is the path this test uses.
func TestOnReArmsTheClock(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	// Added a week ago, so its next fire is a week in the past.
	weekAgo := time.Now().UTC().Add(-7 * 24 * time.Hour)
	s.SetNow(func() time.Time { return weekAgo })
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "triage", Kind: triggers.KindSchedule, Spec: "daily 03:00", Zone: "UTC",
		PromptPath: strp("triage.md"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{Name: "triage", Enabled: boolp(false)}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	s.SetNow(func() time.Time { return now })
	got, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{Name: "triage", Enabled: boolp(true)})
	if err != nil {
		t.Fatal(err)
	}
	if got.NextAt == nil {
		t.Fatal("a schedule turned on has no next fire")
	}
	if !got.NextAt.After(now) {
		t.Fatalf("next fire %s is not after now (%s): a trigger turned on fires at once",
			got.NextAt.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if d := got.NextAt.Sub(now); d > 24*time.Hour+time.Minute {
		t.Errorf("next fire is %s away, which is more than a day for `daily 03:00`", d)
	}
	// And the clock has nothing to claim, so no run row is written for
	// the week it was off.
	due, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("the clock claimed %d fires from a trigger just turned on", len(due))
	}
	runs, err := s.Runs(ctx, pid, "triage", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("%d runs written by turning a trigger on: %+v", len(runs), runs[0])
	}
}

// The clock claims a due fire once, whichever replica ticks and however
// often: the claim writes the next fire in the statement that takes this
// one (I-1252). Two Claim calls racing is the shape two API_MODE=grpc
// replicas have.
func TestOneFirePerTickAcrossTwoClocks(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	a, b := newStore(t, pool), newStore(t, pool)
	past := time.Now().UTC().Add(-2 * time.Minute)

	a.SetNow(func() time.Time { return past })
	if _, _, err := a.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "every", Kind: triggers.KindSchedule, Spec: "every 15m", Zone: "UTC",
		PromptPath: strp("p.md"), CatchUp: boolp(true),
	}); err != nil {
		t.Fatal(err)
	}
	// Its first fire is 15 minutes after the anchor, so bring the clock
	// past it.
	now := past.Add(16 * time.Minute)
	a.SetNow(func() time.Time { return now })
	b.SetNow(func() time.Time { return now })

	type res struct {
		n   int
		err error
	}
	out := make(chan res, 2)
	for _, s := range []*triggers.Store{a, b} {
		go func(s *triggers.Store) {
			due, err := s.Claim(ctx, 10)
			out <- res{len(due), err}
		}(s)
	}
	total := 0
	for range 2 {
		r := <-out
		if r.err != nil {
			t.Fatal(r.err)
		}
		total += r.n
	}
	if total != 1 {
		t.Fatalf("two clocks claimed %d fires for one due trigger; a second fire is a second agent on one checkout", total)
	}
	runs, err := a.Runs(ctx, pid, "every", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("%d run rows for one fire", len(runs))
	}
	if runs[0].State != triggerrun.StatePending {
		t.Errorf("the claimed run is %q, want pending", runs[0].State)
	}
}

// A delivery the edge refused writes the run the user reads, coalesced
// to one row per trigger, reason and hour: 600 refusals an hour would
// otherwise evict every run that ran from the 20 a trigger keeps
// (I-1250, I-1254, I-1262).
func TestADeliveryTheEdgeRefusedBecomesOneRowAnHour(t *testing.T) {
	pool := testdb.Open(t)
	s, hooks := newStores(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	_ = hooks

	tr, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "apply", Kind: triggers.KindRoute, Route: "recruiting", PromptPath: strp("apply.md"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.RouteID == nil {
		t.Fatal("a route trigger has no route")
	}
	// A run that really ran, which the skips must not evict.
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: uuid.Must(uuid.NewV7()), ProjectID: pid, Trigger: "apply",
		State: triggerrun.StateDone, Summary: "applied at Intel", Source: triggerrun.SourceRoute,
	}); err != nil {
		t.Fatal(err)
	}

	hour := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	for range 30 {
		if err := s.SkipFromDelivery(ctx, *tr.RouteID, "stopped", hour); err != nil {
			t.Fatal(err)
		}
	}
	// A second hour is its own row.
	if err := s.SkipFromDelivery(ctx, *tr.RouteID, "stopped", hour.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Another reason in the first hour is its own row too.
	if err := s.SkipFromDelivery(ctx, *tr.RouteID, "rate_limited", hour); err != nil {
		t.Fatal(err)
	}

	runs, err := s.Runs(ctx, pid, "apply", 100)
	if err != nil {
		t.Fatal(err)
	}
	var skipped, done, counted int
	for _, r := range runs {
		switch r.State {
		case triggerrun.StateSkipped:
			skipped++
			if r.Reason == triggerrun.ReasonStopped && r.Skips > counted {
				counted = r.Skips
			}
		case triggerrun.StateDone:
			done++
		}
	}
	if skipped != 3 {
		t.Errorf("%d skipped rows for 32 refusals across two hours and two reasons, want 3", skipped)
	}
	if counted != 30 {
		t.Errorf("the coalesced row counted %d refusals, want 30", counted)
	}
	if done != 1 {
		t.Errorf("the run that ran was evicted by the skips (%d done rows)", done)
	}
}

// A guest's report cannot poison the row: a state nobody defined is
// dropped rather than left to fail the insert for ever (the host resends
// an event the api never acks), a payload count is bounded, and the
// first terminal state wins (I-1256).
func TestAGuestReportCannotPoisonTheRow(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "apply", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("a.md"),
	}); err != nil {
		t.Fatal(err)
	}
	runID := uuid.Must(uuid.NewV7())

	// A state the column's check would refuse.
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: runID, ProjectID: pid, Trigger: "apply", State: "exploded",
	}); err != nil {
		t.Fatalf("a bogus state must be dropped, not returned as an error: %v", err)
	}
	if runs, err := s.Runs(ctx, pid, "apply", 10); err != nil {
		t.Fatal(err)
	} else if len(runs) != 0 {
		t.Fatalf("a bogus state wrote a row: %+v", runs[0])
	}

	// A working run, then a summary over the cap and a silly payload
	// count, then a second terminal state.
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: runID, ProjectID: pid, Trigger: "apply", State: triggerrun.StateWorking,
		Source: "nonsense", Window: "t-apply",
	}); err != nil {
		t.Fatal(err)
	}
	run, ended, err := s.Apply(ctx, triggers.Report{
		RunID: runID, ProjectID: pid, Trigger: "apply", State: triggerrun.StateDone,
		Summary: strings.Repeat("x", 4096), PayloadBytes: 1 << 40,
	})
	if err != nil || !ended {
		t.Fatalf("the run did not end: %v %v", ended, err)
	}
	if len(run.Summary) > triggers.SummaryCap {
		t.Errorf("the summary is %d bytes, over the %d cap", len(run.Summary), triggers.SummaryCap)
	}
	if run.PayloadBytes > triggers.PayloadCap {
		t.Errorf("payload_bytes is %d, over the 1 MiB body cap", run.PayloadBytes)
	}
	if run.Source != triggerrun.SourceRoute {
		t.Errorf("source %q was taken from the guest unchecked", run.Source)
	}
	// A repeat after a terminal state changes nothing.
	again, ended2, err := s.Apply(ctx, triggers.Report{
		RunID: runID, ProjectID: pid, Trigger: "apply", State: triggerrun.StateFailed,
		Reason: triggerrun.ReasonTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ended2 {
		t.Error("a repeat after a terminal state ended the run a second time")
	}
	if again.State != triggerrun.StateDone {
		t.Errorf("the run moved backwards to %q: the first terminal state wins", again.State)
	}
}

// A run still working when the machine stops ends, and a run the machine
// stopped reporting on is swept. Without either the row is `working` for
// ever: `fire --wait` burns its window and UsedAt counts that row as use
// for ever, so the unused notice never fires on the project again
// (I-1256, I-1260).
func TestARunningRunEndsWhenTheMachineStops(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "sweep", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("s.md"),
	}); err != nil {
		t.Fatal(err)
	}

	// One run working, then the machine stops.
	runID := uuid.Must(uuid.NewV7())
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: runID, ProjectID: pid, Trigger: "sweep", State: triggerrun.StateWorking,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.MachineStopped(ctx, pid); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetRun(ctx, pid, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != triggerrun.StateFailed || run.Reason != triggerrun.ReasonMachineStopped {
		t.Errorf("a run the machine stopped under is %q/%q, want failed/machine_stopped", run.State, run.Reason)
	}

	// A second run nobody has reported on for hours: the api's backstop,
	// not a second timeout — the guest owns that, and its clock stops
	// while a question waits (I-1256).
	old := uuid.Must(uuid.NewV7())
	started := time.Now().UTC().Add(-2*time.Hour - triggers.WorkingGrace)
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: old, ProjectID: pid, Trigger: "sweep", State: triggerrun.StateWorking,
		StartedAt: &started,
	}); err != nil {
		t.Fatal(err)
	}
	// Put it back to working, as a machine that is still up would leave it.
	if _, err := pool.Exec(ctx, `update trigger_runs set state = 'working', finished_at = null where id = $1`, old); err != nil {
		t.Fatal(err)
	}
	n, err := s.FailWorking(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the sweep ended %d runs, want 1", n)
	}
	swept, err := s.GetRun(ctx, pid, old)
	if err != nil {
		t.Fatal(err)
	}
	if swept.State != triggerrun.StateFailed || swept.Reason != triggerrun.ReasonUnreachable {
		t.Errorf("the swept run is %q/%q, want failed/unreachable: the guest owns the timeout",
			swept.State, swept.Reason)
	}
	if swept.FinishedAt == nil {
		t.Error("the swept run has no finish time, so nothing can tell it ended")
	}
}

// The api's backstop does not touch a run the guest is still holding
// open: a `repose-ask` at 02:00 waits for the owner, the guest stops
// that run's clock, and the api must not end the run the design calls
// load-bearing (I-1256).
func TestTheBackstopLeavesARunTheGuestIsHolding(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "triage", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("t.md"),
		TimeoutS: intp(60),
	}); err != nil {
		t.Fatal(err)
	}
	runID := uuid.Must(uuid.NewV7())
	// Started an hour ago, with a one-minute timeout: far past the
	// guest's own clock, inside the api's backstop.
	started := time.Now().UTC().Add(-time.Hour)
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: runID, ProjectID: pid, Trigger: "triage", State: triggerrun.StateWorking,
		StartedAt: &started,
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.FailWorking(ctx); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Fatalf("the backstop ended %d runs the guest is still holding", n)
	}
	run, err := s.GetRun(ctx, pid, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != triggerrun.StateWorking {
		t.Errorf("the run is %q, want working", run.State)
	}
}

// `rm` takes away the route it made and leaves the one the user made
// first: a removal tombstones the token for 30 days and the user cannot
// undo that (I-1250, I-1114).
func TestRemovingATriggerKeepsARouteTheUserMade(t *testing.T) {
	pool := testdb.Open(t)
	s, hooks := newStores(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	// One route the user made first, one the trigger makes.
	if _, _, _, err := hooks.Upsert(ctx, uid, pid, "mine", 3000, "/hook", nil); err != nil {
		t.Fatal(err)
	}
	for name, route := range map[string]string{"a": "mine", "b": "made"} {
		if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
			Name: name, Kind: triggers.KindRoute, Route: route, PromptPath: strp("p.md"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Delete(ctx, pid, "a"); err != nil {
		t.Fatal(err)
	} else if got != "" {
		t.Errorf("removing a trigger would remove %q, a route the user made first", got)
	}
	if got, err := s.Delete(ctx, pid, "b"); err != nil {
		t.Fatal(err)
	} else if got != "made" {
		t.Errorf("the route the trigger made is %q, want `made`", got)
	}
}

// Every refusal a caller can cause is one the surfaces turn into
// `invalid` and exit 2, never a 500 (round 3's blocker).
func TestEveryCallerRefusalIsAnInputError(t *testing.T) {
	for _, c := range []struct {
		name string
		in   triggers.Input
	}{
		{"bad name", triggers.Input{Name: "A Name", Kind: triggers.KindRoute, Route: "r"}},
		{"both doors", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", Spec: "daily 03:00"}},
		{"bad spec", triggers.Input{Name: "x", Kind: triggers.KindSchedule, Spec: "tomorrow"}},
		{"bad zone", triggers.Input{Name: "x", Kind: triggers.KindSchedule, Spec: "daily 03:00 EST"}},
		{"bad dedupe", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", Dedupe: strp("cookie:a")}},
		{"dedupe on a schedule", triggers.Input{Name: "x", Kind: triggers.KindSchedule, Spec: "daily 03:00", Dedupe: strp("json:id")}},
		{"bad workdir", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", Workdir: strp("../up")}},
		{"bad agent", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", Agent: strp("cursor")}},
		{"bad notify", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", Notify: strp("maybe")}},
		{"bad concurrency", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", Concurrency: intp(9)}},
		{"bad timeout", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", TimeoutS: intp(100000)}},
		{"absolute prompt", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("/etc/shadow")}},
		{"climbing prompt", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("../../p.md")}},
		{"bad route name", triggers.Input{Name: "x", Kind: triggers.KindRoute, Route: "My Route"}},
	} {
		in := c.in
		_, err := in.Validate("large")
		if err == nil {
			t.Errorf("%s was accepted", c.name)
			continue
		}
		if !triggers.IsInputError(err) {
			t.Errorf("%s answers %v, which no surface turns into exit 2", c.name, err)
		}
	}
}

func intp(n int) *int { return &n }

// A provider retrying the same event, which is the case `--dedupe`
// exists for, must not evict the runs that ran: a skip the guest
// reports coalesces exactly as an edge-side one does (I-1254, I-1262).
func TestGuestSkipsCoalesceAndKeepTheRunsThatRan(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "apply", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("a.md"),
		Dedupe: strp("header:X-Delivery"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: uuid.Must(uuid.NewV7()), ProjectID: pid, Trigger: "apply",
		State: triggerrun.StateDone, Summary: "applied at Intel", Source: triggerrun.SourceRoute,
	}); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if _, _, err := s.Apply(ctx, triggers.Report{
			RunID: uuid.Must(uuid.NewV7()), ProjectID: pid, Trigger: "apply",
			State: triggerrun.StateSkipped, Reason: triggerrun.ReasonDuplicate,
			DedupeKey: "d1", Source: triggerrun.SourceRoute,
		}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.Runs(ctx, pid, "apply", 100)
	if err != nil {
		t.Fatal(err)
	}
	var done, skipped, counted int
	for _, r := range runs {
		switch r.State {
		case triggerrun.StateDone:
			done++
		case triggerrun.StateSkipped:
			skipped++
			counted = r.Skips
		}
	}
	if done != 1 {
		t.Errorf("the run that ran was evicted by %d retries", 50)
	}
	if skipped != 1 {
		t.Errorf("%d skipped rows for 50 retries of one delivery, want 1", skipped)
	}
	if counted != 50 {
		t.Errorf("the coalesced row counted %d, want 50", counted)
	}
}
