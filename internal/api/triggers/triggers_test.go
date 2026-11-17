package triggers_test

import (
	"context"
	"errors"
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

// A fire the guest refuses — a gate that said no, a `--dry-run`, a
// duplicate — ends the row the api already wrote for it. The first draft
// sent every guest skip down the coalescing insert, whose conflict target
// is (trigger, reason, hour) and not the primary key, so a skip of a run
// the api had already written raised 23505. The event was never acked and
// hostd resent it on every reconnect, which is the whole admission
// surface: dry_run, gated, duplicate, concurrency, off, too_many_fires.
func TestAGuestSkipOfAFireEndsTheRowTheApiWrote(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "apply", Kind: triggers.KindRoute, Route: "r", PromptPath: strp("a.md"),
	}); err != nil {
		t.Fatal(err)
	}
	run, err := s.Fire(ctx, pid, "apply", triggers.FireOpts{
		Source: triggerrun.SourceHand, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, fresh, err := s.Apply(ctx, triggers.Report{
		RunID: run.ID, ProjectID: pid, Trigger: "apply", State: triggerrun.StateSkipped,
		Reason: triggerrun.ReasonDryRun, Source: triggerrun.SourceHand,
	})
	if err != nil {
		t.Fatalf("the guest's skip of a run the api wrote: %v", err)
	}
	if !fresh {
		t.Error("the report was not taken, so hostd keeps resending it")
	}
	if got.ID != run.ID {
		t.Errorf("the skip went to %s, not to the row the fire wrote (%s)", got.ID, run.ID)
	}
	if got.State != triggerrun.StateSkipped || got.Reason != triggerrun.ReasonDryRun {
		t.Errorf("the row reads %q/%q, want skipped/dry_run", got.State, got.Reason)
	}
	runs, err := s.Runs(ctx, pid, "apply", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("%d rows for one fire, want 1", len(runs))
	}
}

// A schedule the api itself refuses hour after hour — a stopped machine,
// a plan's memory in use — folds into one row an hour, as §10 says. With
// a row per fire, `every 15m` wrote four an hour and the 20 rows kept per
// trigger evicted every run that ever ran inside five hours. A hand fire
// keeps its own row, because `fire --wait` is waiting on that id.
func TestTheApisOwnRefusalsCoalesceAndAHandFireDoesNot(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "sweep", Kind: triggers.KindSchedule, Spec: "every 15m", PromptPath: strp("s.md"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, triggers.Report{
		RunID: uuid.Must(uuid.NewV7()), ProjectID: pid, Trigger: "sweep",
		State: triggerrun.StateDone, Summary: "swept", Source: triggerrun.SourceClock,
	}); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		run, err := s.Fire(ctx, pid, "sweep", triggers.FireOpts{Source: triggerrun.SourceClock})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Answered(ctx, run.ID, triggerrun.StateSkipped, "", triggerrun.ReasonStopped); err != nil {
			t.Fatal(err)
		}
	}
	hand, err := s.Fire(ctx, pid, "sweep", triggers.FireOpts{Source: triggerrun.SourceHand})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Answered(ctx, hand.ID, triggerrun.StateSkipped, "", triggerrun.ReasonStopped); err != nil {
		t.Fatal(err)
	}
	runs, err := s.Runs(ctx, pid, "sweep", 100)
	if err != nil {
		t.Fatal(err)
	}
	var done, clockSkips, handSkips, counted int
	for _, r := range runs {
		switch {
		case r.State == triggerrun.StateDone:
			done++
		case r.ID == hand.ID:
			handSkips++
		default:
			clockSkips++
			counted = r.Skips
		}
	}
	if done != 1 {
		t.Error("the run that ran was evicted by the clock's refusals")
	}
	if clockSkips != 1 {
		t.Errorf("%d rows for 40 refused clock fires, want 1", clockSkips)
	}
	if counted != 40 {
		t.Errorf("the hour's row counted %d, want 40", counted)
	}
	if handSkips != 1 {
		t.Error("a hand fire's own row went missing; `fire --wait` waits on that id")
	}
}

// The hand-off's clock runs from the last time the worker took the row,
// not from when it was written. The pass that requeues the fire which
// started a machine used to fail that same fire `unreachable` on the next
// pass, 60 seconds later, and notify the owner about a machine that was
// starting exactly as asked.
func TestARequeuedFireIsNotStaleOnTheNextPass(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "nightly", Kind: triggers.KindSchedule, Spec: "daily 03:00",
		PromptPath: strp("n.md"), AllowStart: boolp(true),
	}); err != nil {
		t.Fatal(err)
	}
	// Written a minute and a half ago, which is past Stale on its own.
	s.SetNow(func() time.Time { return time.Now().UTC().Add(-90 * time.Second) })
	run, err := s.Fire(ctx, pid, "nightly", triggers.FireOpts{Source: triggerrun.SourceClock})
	if err != nil {
		t.Fatal(err)
	}
	s.SetNow(time.Now)
	taken, err := s.TakePending(ctx, 10)
	if err != nil || len(taken) != 1 {
		t.Fatalf("TakePending: %v, %d rows", err, len(taken))
	}
	if err := s.Requeue(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	n, err := s.FailStale(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the fire that started the machine was failed `unreachable` while the machine started")
	}
	// A hand-off nobody takes for the whole ceiling does end, so a run
	// waiting for a machine that never arrives is not waiting for ever.
	s.SetNow(func() time.Time { return time.Now().UTC().Add(triggers.MaxWait + time.Minute) })
	if n, err := s.FailStale(ctx); err != nil || n != 1 {
		t.Errorf("past MaxWait: %d rows ended, %v", n, err)
	}
	got, err := s.GetRun(ctx, pid, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reason != triggerrun.ReasonUnreachable {
		t.Errorf("reason %q, want unreachable", got.Reason)
	}
}

// The cross-project listing joins `projects` onto `trigger_runs`, and
// the two tables share `id`, `state` and `created_at`. With the
// unqualified column list every call answered 500 — `column reference
// "id" is ambiguous` (SQLSTATE 42702) — and nothing caught it, because
// AllRuns had no store test and the fake api answers the same route in
// Go. It is one trigger run away from being caught, so here it is.
func TestAllRunsReadsEveryProjectsRuns(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, one := newProject(t, pool)
	two := secondProject(t, pool, uid)
	_, other := newProject(t, pool) // another account's

	for _, p := range []struct {
		id      uuid.UUID
		trigger string
		ago     time.Duration
	}{{one, "apply", 2 * time.Hour}, {two, "triage", time.Hour}, {other, "sweep", time.Minute}} {
		if _, _, err := s.Put(ctx, uid, p.id, "large", triggers.Input{
			Name: p.trigger, Kind: triggers.KindSchedule, Spec: "daily 03:00",
			PromptPath: strp("p.md"),
		}); err != nil {
			t.Fatal(err)
		}
		s.SetNow(func() time.Time { return time.Now().UTC().Add(-p.ago) })
		if _, _, err := s.Apply(ctx, triggers.Report{
			RunID: uuid.Must(uuid.NewV7()), ProjectID: p.id, Trigger: p.trigger,
			State: triggerrun.StateDone, Summary: "did it", Source: triggerrun.SourceClock,
		}); err != nil {
			t.Fatal(err)
		}
		s.SetNow(time.Now)
	}

	runs, err := s.AllRuns(ctx, uid, 20)
	if err != nil {
		t.Fatalf("AllRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("%d runs, want this account's 2 and not the other account's", len(runs))
	}
	// Newest first, each saying which machine it ran on.
	if runs[0].TriggerName != "triage" || runs[1].TriggerName != "apply" {
		t.Errorf("order: %s then %s, want triage then apply", runs[0].TriggerName, runs[1].TriggerName)
	}
	for _, r := range runs {
		if r.ProjectSlug == "" {
			t.Errorf("%s has no project slug, so a fleet row cannot say where it ran", r.TriggerName)
		}
		if r.State != triggerrun.StateDone || r.Summary != "did it" {
			t.Errorf("%s came back as %q/%q: the join took the project's columns",
				r.TriggerName, r.State, r.Summary)
		}
	}
	// The limit is the api's, and a limit outside it falls back rather
	// than reaching Postgres as a negative.
	if got, err := s.AllRuns(ctx, uid, 1); err != nil || len(got) != 1 {
		t.Errorf("limit 1: %d runs, %v", len(got), err)
	}
	if got, err := s.AllRuns(ctx, uid, -3); err != nil || len(got) != 2 {
		t.Errorf("a limit outside 1..200: %d runs, %v", len(got), err)
	}
}

// secondProject is another project of the same account.
func secondProject(t *testing.T, pool *db.Pool, userID uuid.UUID) uuid.UUID {
	t.Helper()
	pid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(), `insert into projects (id, user_id, name, slug, class, state, volume_bytes)
		values ($1, $2, 'p', $3, 'large', 'running', 1)`, pid, userID, "p-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	return pid
}

// The two column lists are the same columns in the same order, so the
// one query that joins scans into the same fields as every other.
func TestTheJoinedColumnListIsTheOtherOne(t *testing.T) {
	plain, joined := splitCols(triggers.RunColsForTest), splitCols(triggers.RunColsRForTest)
	for i := range joined {
		joined[i] = strings.ReplaceAll(joined[i], "r.", "")
	}
	if len(plain) != len(joined) {
		t.Fatalf("%d columns unqualified, %d qualified", len(plain), len(joined))
	}
	for i := range plain {
		if plain[i] != joined[i] {
			t.Errorf("column %d: %q unqualified, %q qualified", i, plain[i], joined[i])
		}
	}
	// And every qualified column really names the run's table, which is
	// the whole point of the second list.
	for _, f := range splitCols(triggers.RunColsRForTest) {
		if !strings.Contains(f, "r.") {
			t.Errorf("%q is not qualified, so a join makes it ambiguous", f)
		}
	}
}

// splitCols is a select list's columns, splitting on the commas between
// them and not on the ones inside a call like coalesce(x, ”).
func splitCols(list string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range list {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.Join(strings.Fields(list[start:i]), " "))
				start = i + 1
			}
		}
	}
	return append(out, strings.Join(strings.Fields(list[start:]), " "))
}

// A prompt typed in a browser is a hand-off and not a column of the
// trigger (I-1266). The http process cannot reach a host (I-1162), so
// the text waits on its own row until the process that holds the stream
// has given it to the machine; the `triggers` row keeps a path and never
// the text, which is what the privacy promise rests on.
func TestAPromptIsAHandOffAndNeverATriggersColumn(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	const text = "Triage the new issues; tell me what needs me"
	got, created, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "triage", Kind: triggers.KindSchedule, Spec: "weekdays 07:00", Zone: "Europe/Berlin",
		Prompt: strp(text),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("the first Put did not create the trigger")
	}
	// The row holds the default path and nothing of the text.
	if got.PromptPath != triggers.DefaultPromptPath("triage") {
		t.Errorf("prompt_path is %q, want %q", got.PromptPath, triggers.DefaultPromptPath("triage"))
	}
	var row string
	if err := pool.QueryRow(ctx, "select triggers::text from triggers where project_id = $1", pid).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row, "Triage the new issues") {
		t.Fatalf("the trigger row holds the prompt's text: %s", row)
	}

	pending, err := s.PendingPrompts(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("%d prompts waiting, want 1", len(pending))
	}
	if pending[0].Trigger != "triage" || pending[0].Body != text {
		t.Errorf("waiting prompt is %+v", pending[0])
	}
	if pending[0].PromptPath != triggers.DefaultPromptPath("triage") || pending[0].Workdir != "checkout" {
		t.Errorf("the hand-off does not say where it goes: %+v", pending[0])
	}

	// A second prompt replaces the first: one row a trigger, so a user
	// who types twice does not leave the first text behind.
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "triage", Prompt: strp("second"), Workdir: strp("worktree-2"),
	}); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingPrompts(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Body != "second" || pending[0].Workdir != "worktree-2" {
		t.Fatalf("after a second prompt the hand-off is %+v", pending)
	}

	// The machine has it: the api forgets it.
	if err := s.ClearPrompt(ctx, pid, "triage"); err != nil {
		t.Fatal(err)
	}
	if pending, err = s.PendingPrompts(ctx, pid); err != nil || len(pending) != 0 {
		t.Fatalf("after the machine took it: %d waiting, err %v", len(pending), err)
	}
}

// A prompt no machine ever took is forgotten, so a user's words do not
// sit in the api for a week (I-1266). The sweeper runs in the clock's
// own pass.
func TestAPromptNoMachineTookIsSweptAway(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	old := time.Now().UTC().Add(-triggers.PromptHandoff - time.Minute)
	s.SetNow(func() time.Time { return old })
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "triage", Kind: triggers.KindSchedule, Spec: "daily 03:00", Zone: "UTC",
		Prompt: strp("never delivered"),
	}); err != nil {
		t.Fatal(err)
	}
	s.SetNow(time.Now().UTC)
	n, err := s.SweepPrompts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the sweep forgot %d prompts, want 1", n)
	}
	pending, err := s.PendingPrompts(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("%d prompts still waiting after the sweep", len(pending))
	}
	// And a prompt inside the window is left alone.
	if _, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "triage", Prompt: strp("fresh"),
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SweepPrompts(ctx); err != nil || n != 0 {
		t.Fatalf("the sweep forgot %d fresh prompts (err %v)", n, err)
	}
}

// Every refusal the CLI makes on `-p`, the api makes on `prompt`: the
// same size and the same pair pi cannot take (I-780, I-1266).
func TestAPromptsRefusalsAreTheCLIs(t *testing.T) {
	pool := testdb.Open(t)
	s := newStore(t, pool)
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	_, _, err := s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "big", Kind: triggers.KindSchedule, Spec: "daily 03:00", Zone: "UTC",
		Prompt: strp(strings.Repeat("x", triggers.PromptCap+1)),
	})
	if !errors.Is(err, triggers.ErrPromptTooLarge) {
		t.Errorf("a prompt over the cap answered %v, want ErrPromptTooLarge", err)
	}
	_, _, err = s.Put(ctx, uid, pid, "large", triggers.Input{
		Name: "at", Kind: triggers.KindSchedule, Spec: "daily 03:00", Zone: "UTC",
		Agent: strp("pi"), Prompt: strp("@notes.md read this"),
	})
	if !errors.Is(err, triggers.ErrAtPromptOnPi) {
		t.Errorf("an @-prompt on pi answered %v, want ErrAtPromptOnPi", err)
	}
	// Both are the caller's fault, so both are `invalid` and not a 500.
	if !triggers.IsInputError(triggers.ErrPromptTooLarge) {
		t.Error("ErrPromptTooLarge is not an input error, so a long prompt would answer 500")
	}
}
