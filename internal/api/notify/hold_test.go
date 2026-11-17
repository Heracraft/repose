package notify_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

// holdEnv is a project with ntfy only, an ingest, and an outbox whose
// clock the test moves.
type holdEnv struct {
	t    *testing.T
	pool *db.Pool
	ctx  context.Context
	pid  uuid.UUID
	ing  *events.Ingest
	ob   *notify.Outbox
	m    *metrics.M
	sent *recorder
	now  time.Time
}

func newHoldEnv(t *testing.T) *holdEnv {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	m := metrics.NewNop()
	_, pid := seed(t, pool, "https://ntfy.example/topic")
	if _, err := pool.Exec(ctx, "update users set notify_email = false"); err != nil {
		t.Fatal(err)
	}
	h := &holdEnv{t: t, pool: pool, ctx: ctx, pid: mustUUID(pid), ing: events.New(pool, m, log), sent: &recorder{}, now: time.Now(), m: m}
	h.ob = notify.New(pool, map[string]notify.Sender{"ntfy": h.sent}, m, log)
	h.ob.Now = func() time.Time { return h.now }
	if err := db.EnsurePartitions(ctx, pool, h.now); err != nil {
		t.Fatal(err)
	}
	return h
}

// sample writes a meter sample at ts with the given tmux clients and SSH
// sessions and no last_input_at: a guest from before I-1272, which the
// hold reads by I-689's rule.
func (h *holdEnv) sample(ts time.Time, tmux, ssh int) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, `insert into meter_samples (ts, project_id, state, class, tmux_clients, ssh_sessions) values ($1, $2, 'running', 'large', $3, $4)`, ts, h.pid, tmux, ssh); err != nil {
		h.t.Fatal(err)
	}
}

// typed writes a sample at ts from a guest that reports input times
// (I-1272): somebody is attached either way, and lastInput is when they
// last touched the keyboard.
func (h *holdEnv) typed(ts, lastInput time.Time) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, `insert into meter_samples (ts, project_id, state, class, tmux_clients, ssh_sessions, last_input_at) values ($1, $2, 'running', 'large', 1, 1, $3)`, ts, h.pid, lastInput); err != nil {
		h.t.Fatal(err)
	}
}

// hold sets the user's own hold (I-1282).
func (h *holdEnv) hold(d time.Duration) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, "update users set notify_hold_seconds = $1", int(d.Seconds())); err != nil {
		h.t.Fatal(err)
	}
}

func (h *holdEnv) insert(kind, window string) uuid.UUID {
	h.t.Helper()
	return h.insertAs("claude", kind, window)
}

func (h *holdEnv) insertAs(agent, kind, window string) uuid.UUID {
	h.t.Helper()
	id, ins, err := h.ing.Insert(h.ctx, events.Incoming{ProjectID: h.pid, TS: h.now, Kind: kind, Agent: agent, Window: window, Summary: kind})
	if err != nil || !ins {
		h.t.Fatalf("insert %s: %v %v", kind, ins, err)
	}
	return id
}

// pass runs the outbox at the env's clock, a second past the due rows.
func (h *holdEnv) pass(at time.Time) {
	h.t.Helper()
	h.now = at
	if _, err := h.ob.Once(h.ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *holdEnv) delivered(id uuid.UUID) string {
	var d map[string]string
	_ = h.pool.QueryRow(h.ctx, "select delivered from events where id = $1", id).Scan(&d)
	return d["ntfy"]
}

func (h *holdEnv) outbox(id uuid.UUID) (attempts int, next time.Time, ok bool) {
	err := h.pool.QueryRow(h.ctx, "select attempts, next_at from events_outbox where event_id = $1", id).Scan(&attempts, &next)
	return attempts, next, err == nil
}

// TestHeldWhilePresentThenSent (DECISIONS I-689, I-1272, I-1274): a
// report queued while somebody is typing in the machine waits HoldStep
// at a time without an attempt, shows "held", and goes out MaxHold after
// its event even if they never stop; a platform kind is never held.
func TestHeldWhilePresentThenSent(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.typed(start.Add(-30*time.Second), start.Add(-30*time.Second))
	done := h.insert("agent_done", "main")
	platform := h.insert("snapshot_failed", "")
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 1 || h.sent.sent[0].Kind != "snapshot_failed" {
		t.Fatalf("sent %v, want the platform notice only", h.sent.sent)
	}
	if got := h.delivered(done); got != notify.DeliveredHeld {
		t.Fatalf("delivered %q, want held", got)
	}
	attempts, next, ok := h.outbox(done)
	if !ok || attempts != 0 || !near(next, start.Add(time.Second+notify.HoldStep)) {
		t.Fatalf("held row: ok %v attempts %d next in %v", ok, attempts, next.Sub(start.Add(time.Second)))
	}
	if h.delivered(platform) == "" {
		t.Fatal("platform notice not marked")
	}
	// Still typing a step before the end: held, and the next try is at
	// the end of the hold, not past it.
	at := start.Add(notify.MaxHold - notify.HoldStep)
	h.typed(at, at)
	h.pass(at)
	if len(h.sent.sent) != 1 {
		t.Fatal("sent while the owner was present")
	}
	if _, next, _ := h.outbox(done); !near(next, start.Add(notify.MaxHold)) {
		t.Fatalf("next try %v after the event, want %v", next.Sub(start), notify.MaxHold)
	}
	h.typed(start.Add(notify.MaxHold), start.Add(notify.MaxHold))
	h.pass(start.Add(notify.MaxHold))
	if len(h.sent.sent) != 2 || h.sent.sent[1].Kind != "agent_done" {
		t.Fatalf("not sent at the end of the hold: %d sent", len(h.sent.sent))
	}
	if got := h.delivered(done); got == notify.DeliveredHeld || got == "" {
		t.Fatalf("delivered %q after the send", got)
	}
}

// TestAttachedButIdleIsNotPresence (DECISIONS I-1272) is the owner's
// 2026-11-17 report: an `agent_done` carrying a deployment URL sat held
// for the whole hold while they were away from the keyboard, because the
// laptop's SSH session was still open. A fresh sample with a client and
// a session, whose last input is older than InputIdle, is not presence,
// and the report goes out at once.
func TestAttachedButIdleIsNotPresence(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.typed(start, start.Add(-notify.InputIdle-time.Minute))
	done := h.insert("agent_done", "main")
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 1 || h.sent.sent[0].Kind != "agent_done" {
		t.Fatalf("sent %v, want the report at once", h.sent.sent)
	}
	if got := h.delivered(done); got == notify.DeliveredHeld || got == "" {
		t.Fatalf("delivered %q, want a send time", got)
	}
}

// TestGuestWithoutInputTimeFallsBackToSessionCounts (I-1272): a sample
// from a guest or a host from before this carries no last_input_at, and
// the hold reads I-689's rule rather than treating the machine as empty.
func TestGuestWithoutInputTimeFallsBackToSessionCounts(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.sample(start, 0, 1) // an SSH session, no input time
	done := h.insert("agent_done", "main")
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 0 {
		t.Fatalf("sent %v, want held under the old rule", h.sent.sent)
	}
	if got := h.delivered(done); got != notify.DeliveredHeld {
		t.Fatalf("delivered %q, want held", got)
	}
}

// TestHoldOffForTheUser (I-1282): a user whose hold is 0 is never held,
// however present they are.
func TestHoldOffForTheUser(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.hold(0)
	h.typed(start, start)
	done := h.insert("agent_done", "main")
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 1 || h.delivered(done) == notify.DeliveredHeld {
		t.Fatalf("sent %v, delivered %q: want the report at once", h.sent.sent, h.delivered(done))
	}
}

// TestTheUsersOwnHoldIsTheWindow (I-1282): the hold runs for as long as
// the user set, not for MaxHold.
func TestTheUsersOwnHoldIsTheWindow(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.hold(5 * time.Minute) // neither MaxHold nor I-689's ten minutes
	h.typed(start, start)
	done := h.insert("agent_done", "main")
	h.pass(start.Add(time.Second))
	if h.delivered(done) != notify.DeliveredHeld {
		t.Fatalf("delivered %q, want held", h.delivered(done))
	}
	at := start.Add(notify.MaxHold + time.Minute)
	h.typed(at, at)
	h.pass(at)
	if len(h.sent.sent) != 0 {
		t.Fatalf("sent %v past MaxHold, want held to the user's own 5m", h.sent.sent)
	}
	at = start.Add(5 * time.Minute)
	h.typed(at, at)
	h.pass(at)
	if len(h.sent.sent) != 1 {
		t.Fatalf("sent %d at the end of the user's hold, want 1", len(h.sent.sent))
	}
}

// TestNeedsInputIsNeverHeld (I-1276): an agent stopped at a permission
// prompt is not a report that can wait, so it goes out at once with the
// owner typing.
func TestNeedsInputIsNeverHeld(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.typed(start, start)
	prompt := h.insert("needs_input", "main")
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 1 || h.sent.sent[0].Kind != "needs_input" {
		t.Fatalf("sent %v, want the prompt at once", h.sent.sent)
	}
	if got := h.delivered(prompt); got == notify.DeliveredHeld || got == "" {
		t.Fatalf("delivered %q, want a send time", got)
	}
}

// TestALaterPromptDoesNotResolveAnEarlierOne (I-1278): only the end of
// the turn means a `needs_input` is over. On 30 days of prod, 7 of 20
// dropped prompts were killed by a later `needs_input` and one by an
// `agent_question`, which left an agent still waiting and the owner
// told nothing.
func TestALaterPromptDoesNotResolveAnEarlierOne(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	// A prompt is never held (I-1276), so each row here is parked past
	// the later events instead, which is the race the drop is about: a
	// row still queued when the next event of its window lands.
	park := func(id uuid.UUID, at time.Time) {
		if _, err := h.pool.Exec(h.ctx, "update events_outbox set next_at = $2 where event_id = $1", id, at); err != nil {
			t.Fatal(err)
		}
	}
	first := h.insert("needs_input", "main")
	park(first, start.Add(time.Hour))
	// Later events in the same window: another prompt and a question.
	// Each is more than the ingest's 60 second dedupe window after the
	// one before it and less than its 5 minute skew clamp from now, or
	// the ingest would fold them into one event.
	h.now = start.Add(2 * time.Minute)
	h.insert("needs_input", "main")
	h.now = start.Add(3 * time.Minute)
	h.insert("agent_question", "main")
	h.pass(start.Add(time.Hour + time.Second))
	if got := h.delivered(first); got == notify.DeliveredDropped || got == "" {
		t.Fatalf("prompt %q after a later prompt and question, want a send: the agent is still waiting", got)
	}
	// The turn ending does resolve a prompt.
	h.now = start.Add(4 * time.Minute)
	prompt := h.insert("needs_input", "w2")
	park(prompt, start.Add(2*time.Hour))
	h.now = start.Add(4*time.Minute + 30*time.Second)
	h.insert("completed", "w2")
	h.pass(start.Add(2*time.Hour + time.Second))
	if got := h.delivered(prompt); got != notify.DeliveredDropped {
		t.Fatalf("prompt %q after its turn ended, want dropped", got)
	}
}

// TestNotHeldWhenAwayOrStale: no client, or a sample older than two
// minutes, is not presence.
func TestNotHeldWhenAwayOrStale(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.sample(start.Add(-3*time.Minute), 2, 0) // stale
	h.insert("needs_input", "main")
	h.pass(start.Add(time.Second))
	h.sample(start.Add(30*time.Second), 0, 0) // fresh, nobody
	h.insert("agent_message", "main")
	h.pass(start.Add(31 * time.Second))
	if len(h.sent.sent) != 2 {
		t.Fatalf("%d sent, want 2", len(h.sent.sent))
	}
}

// TestQuestionSentWhilePresentAndHeldRowDroppedWhenResolved: a question
// goes out at once with the owner attached (I-1242, amending I-689), a
// question already answered in the terminal by its first send is dropped
// with "dropped: resolved", and a report in the same window is held
// meanwhile. Probe: with "agent_question" back in heldKinds, eid reads
// held after the first pass.
func TestQuestionSentWhilePresentAndHeldRowDroppedWhenResolved(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.sample(start, 1, 0)
	// Two question rows and their events, as the questions store writes
	// them; the second is answered before the first pass.
	qid, eid := store.NewID(), store.NewID()
	answered, aeid := store.NewID(), store.NewID()
	for _, q := range []struct{ q, e uuid.UUID }{{qid, eid}, {answered, aeid}} {
		if _, err := h.pool.Exec(h.ctx, `insert into questions (id, project_id, guest_id, event_id, agent, text, options, state, expires_at, created_at) values ($1, $2, $3, $4, 'claude', 'ok?', '{}', 'pending', $5, $6)`,
			q.q, h.pid, store.NewID(), q.e, start.Add(time.Hour), start); err != nil {
			t.Fatal(err)
		}
		if _, _, err := h.ing.Insert(h.ctx, events.Incoming{ID: q.e, ProjectID: h.pid, TS: start, Kind: "agent_question", Agent: "claude", Summary: "ok?"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.pool.Exec(h.ctx, "update questions set state = 'answered', answered_via = 'terminal' where id = $1", answered); err != nil {
		t.Fatal(err)
	}
	report := h.insert("agent_message", "main")
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 1 || h.sent.sent[0].EventID != eid {
		t.Fatalf("sent %v, want the pending question alone, at once", h.sent.sent)
	}
	if got := h.delivered(eid); got == notify.DeliveredHeld || got == "" {
		t.Fatalf("question delivered %q, want a send time", got)
	}
	if h.delivered(aeid) != notify.DeliveredDropped {
		t.Fatalf("question answered before its send: %q, want dropped", h.delivered(aeid))
	}
	if h.delivered(report) != notify.DeliveredHeld {
		t.Fatalf("report not held: %q", h.delivered(report))
	}
	h.sample(start.Add(time.Minute), 0, 0)
	h.pass(start.Add(time.Minute + time.Second))
	if len(h.sent.sent) != 2 || h.sent.sent[1].EventID != report {
		t.Fatalf("sent %d, want the question and then the report once nobody is there", len(h.sent.sent))
	}
	var rows int
	_ = h.pool.QueryRow(h.ctx, "select count(*) from events_outbox").Scan(&rows)
	if rows != 0 {
		t.Fatalf("%d outbox rows left", rows)
	}
}

// TestQuestionClosedWhileQueuedOnItsLane (I-689): the "is it over" check
// reads the question at send time. A question row picked while pending
// that waits on its lane behind a slower send, and whose question closes
// meanwhile, is dropped, not sent with answer buttons.
func TestQuestionClosedWhileQueuedOnItsLane(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	qid, eid := store.NewID(), store.NewID()
	if _, err := h.pool.Exec(h.ctx, `insert into questions (id, project_id, guest_id, event_id, agent, text, options, state, expires_at, created_at) values ($1, $2, $3, $4, 'claude', 'ok?', '{}', 'pending', $5, $6)`,
		qid, h.pid, store.NewID(), eid, start.Add(time.Hour), start); err != nil {
		t.Fatal(err)
	}
	first := h.insert("agent_done", "main")
	h.now = start.Add(time.Millisecond)
	if _, _, err := h.ing.Insert(h.ctx, events.Incoming{ID: eid, ProjectID: h.pid, TS: h.now, Kind: "agent_question", Agent: "claude", Summary: "ok?"}); err != nil {
		t.Fatal(err)
	}
	// The first send closes the question, as an answer in the terminal
	// would while the lane is busy with it.
	h.ob = notify.New(h.pool, map[string]notify.Sender{"ntfy": notify.SenderFunc(func(ctx context.Context, m notify.Message) error {
		if m.EventID == first {
			if _, err := h.pool.Exec(ctx, "update questions set state = 'answered', answered_via = 'terminal' where id = $1", qid); err != nil {
				return err
			}
		}
		return h.sent.Send(ctx, m)
	})}, h.m, slog.New(slog.DiscardHandler))
	h.ob.Now = func() time.Time { return h.now }
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 1 || h.sent.sent[0].EventID != first {
		t.Fatalf("sent %d, want only the done report", len(h.sent.sent))
	}
	if got := h.delivered(eid); got != notify.DeliveredDropped {
		t.Fatalf("question %q, want dropped", got)
	}
}

// TestSlowLaneKeepsLagAtZero (I-691): rows waiting on a lane behind slow
// sends to one user's server are not due, so the lag that pages stays 0
// while the lane works through them.
func TestSlowLaneKeepsLagAtZero(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	for i := 0; i < 5; i++ {
		h.insert("agent_done", "main")
	}
	var lags []float64
	h.ob = notify.New(h.pool, map[string]notify.Sender{"ntfy": notify.SenderFunc(func(ctx context.Context, m notify.Message) error {
		h.now = h.now.Add(40 * time.Second) // a slow server
		h.ob.Gauges(ctx)
		lags = append(lags, testutil.ToFloat64(h.m.OutboxLagSeconds))
		return h.sent.Send(ctx, m)
	})}, h.m, slog.New(slog.DiscardHandler))
	h.ob.Now = func() time.Time { return h.now }
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 5 {
		t.Fatalf("sent %d, want 5", len(h.sent.sent))
	}
	for i, lag := range lags {
		if lag != 0 {
			t.Fatalf("send %d: lag %v while the rows wait on a working lane", i, lag)
		}
	}
}

// near compares a time read back from Postgres, which keeps microseconds.
func near(a, b time.Time) bool { return a.Sub(b).Abs() < time.Millisecond }

// TestHoldMetricsAndLag (DECISIONS I-689, I-691): a row held over several
// passes counts `held` once, a drop counts `dropped`, and the outbox lag
// stays 0 while a row is held and grows for a due row nobody picks up.
func TestHoldMetricsAndLag(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.hold(10 * time.Minute)
	h.insert("agent_done", "main")
	h.insert("agent_message", "w2")
	prompt := h.insert("needs_input", "w3")
	// The prompt is never held (I-1276); it is parked past the passes so
	// the drop below is the one being counted.
	if _, err := h.pool.Exec(h.ctx, "update events_outbox set next_at = $2 where event_id = $1", prompt, start.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		at := start.Add(time.Second + time.Duration(i)*notify.HoldStep)
		h.typed(at.Add(-time.Second), at.Add(-time.Second))
		h.pass(at)
		if lag := testutil.ToFloat64(h.m.OutboxLagSeconds); lag != 0 {
			t.Fatalf("pass %d: lag %v while every row is held", i, lag)
		}
	}
	if got := testutil.ToFloat64(h.m.NotifyTotal.WithLabelValues("ntfy", "held")); got != 2 {
		t.Fatalf("held counted %v, want 2 (once per row)", got)
	}
	h.now = start.Add(3 * time.Minute)
	h.typed(h.now, h.now)
	h.insert("completed", "w3") // ends the prompt's turn
	h.pass(start.Add(3*time.Minute + time.Second))
	if h.delivered(prompt) != notify.DeliveredDropped {
		t.Fatalf("prompt %q, want dropped", h.delivered(prompt))
	}
	if got := testutil.ToFloat64(h.m.NotifyTotal.WithLabelValues("ntfy", "dropped")); got != 1 {
		t.Fatalf("dropped counted %v, want 1", got)
	}
	// Nobody picks the held row up once it is due: the lag grows, without
	// a pass, on any replica that runs Gauges.
	_, next, _ := h.outbox(h.insertAs("", "snapshot_failed", ""))
	h.now = next.Add(7 * time.Minute)
	h.ob.Gauges(h.ctx)
	if lag := testutil.ToFloat64(h.m.OutboxLagSeconds); lag < 7*60-1 {
		t.Fatalf("lag %v for a row due 7 minutes ago", lag)
	}
}
