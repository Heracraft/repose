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
// sessions.
func (h *holdEnv) sample(ts time.Time, tmux, ssh int) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, `insert into meter_samples (ts, project_id, state, class, tmux_clients, ssh_sessions) values ($1, $2, 'running', 'large', $3, $4)`, ts, h.pid, tmux, ssh); err != nil {
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

// TestHeldWhilePresentThenSent (DECISIONS I-689): an agent kind queued
// while a tmux client is attached waits a minute at a time without an
// attempt, shows "held", and goes out 10 minutes after its event even if
// the owner never leaves; a platform kind is never held.
func TestHeldWhilePresentThenSent(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.sample(start.Add(-30*time.Second), 1, 0)
	done := h.insert("agent_done", "main")
	platform := h.insert("base_updated", "")
	h.pass(start.Add(time.Second))
	if len(h.sent.sent) != 1 || h.sent.sent[0].Kind != "base_updated" {
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
	// Still attached nine minutes on: held, and the next try is at the
	// tenth minute, not past it.
	h.sample(start.Add(9*time.Minute), 0, 1)
	h.pass(start.Add(9*time.Minute + 30*time.Second))
	if len(h.sent.sent) != 1 {
		t.Fatal("sent while the owner was present")
	}
	if _, next, _ := h.outbox(done); !near(next, start.Add(notify.MaxHold)) {
		t.Fatalf("next try %v after the event, want %v", next.Sub(start), notify.MaxHold)
	}
	h.sample(start.Add(10*time.Minute), 1, 1)
	h.pass(start.Add(notify.MaxHold))
	if len(h.sent.sent) != 2 || h.sent.sent[1].Kind != "agent_done" {
		t.Fatalf("not sent at the end of the hold: %d sent", len(h.sent.sent))
	}
	if got := h.delivered(done); got == notify.DeliveredHeld || got == "" {
		t.Fatalf("delivered %q after the send", got)
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

// TestHeldRowDroppedWhenResolved: a held question answered meanwhile, and
// a held needs_input followed by a later event of the same agent window,
// are dropped with "dropped: resolved"; another agent's prompt in that
// window is not resolved by it.
func TestHeldRowDroppedWhenResolved(t *testing.T) {
	h := newHoldEnv(t)
	start := h.now
	h.sample(start, 1, 0)
	// A question row and its event, as the questions store writes them.
	qid, eid := store.NewID(), store.NewID()
	if _, err := h.pool.Exec(h.ctx, `insert into questions (id, project_id, guest_id, event_id, agent, text, options, state, expires_at, created_at) values ($1, $2, $3, $4, 'claude', 'ok?', '{}', 'pending', $5, $6)`,
		qid, h.pid, store.NewID(), eid, start.Add(time.Hour), start); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.ing.Insert(h.ctx, events.Incoming{ID: eid, ProjectID: h.pid, TS: start, Kind: "agent_question", Agent: "claude", Summary: "ok?"}); err != nil {
		t.Fatal(err)
	}
	prompt := h.insert("needs_input", "main")
	other := h.insertAs("codex", "needs_input", "main")
	h.pass(start.Add(time.Second))
	for _, id := range []uuid.UUID{eid, prompt, other} {
		if h.delivered(id) != notify.DeliveredHeld {
			t.Fatalf("%s not held: %q", id, h.delivered(id))
		}
	}
	if _, err := h.pool.Exec(h.ctx, "update questions set state = 'answered', answered_via = 'terminal' where id = $1", qid); err != nil {
		t.Fatal(err)
	}
	h.now = start.Add(20 * time.Second)
	h.insert("completed", "main")
	h.sample(start.Add(time.Minute), 0, 0)
	h.pass(start.Add(time.Minute + time.Second))
	if h.delivered(eid) != notify.DeliveredDropped || h.delivered(prompt) != notify.DeliveredDropped {
		t.Fatalf("question %q, prompt %q, want both dropped", h.delivered(eid), h.delivered(prompt))
	}
	if len(h.sent.sent) != 1 || h.sent.sent[0].EventID != other {
		t.Fatalf("sent %d, want only the other window's prompt", len(h.sent.sent))
	}
	var rows int
	_ = h.pool.QueryRow(h.ctx, "select count(*) from events_outbox").Scan(&rows)
	if rows != 0 {
		t.Fatalf("%d outbox rows left", rows)
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
	h.insert("agent_done", "main")
	prompt := h.insert("needs_input", "w2")
	for i := 0; i < 3; i++ {
		at := start.Add(time.Second + time.Duration(i)*notify.HoldStep)
		h.sample(at.Add(-time.Second), 1, 0)
		h.pass(at)
		if lag := testutil.ToFloat64(h.m.OutboxLagSeconds); lag != 0 {
			t.Fatalf("pass %d: lag %v while every row is held", i, lag)
		}
	}
	if got := testutil.ToFloat64(h.m.NotifyTotal.WithLabelValues("ntfy", "held")); got != 2 {
		t.Fatalf("held counted %v, want 2 (once per row)", got)
	}
	h.now = start.Add(3 * time.Minute)
	h.sample(h.now, 1, 0)
	h.insert("agent_message", "w2") // resolves the prompt
	h.pass(start.Add(3*time.Minute + time.Second))
	if h.delivered(prompt) != notify.DeliveredDropped {
		t.Fatalf("prompt %q, want dropped", h.delivered(prompt))
	}
	if got := testutil.ToFloat64(h.m.NotifyTotal.WithLabelValues("ntfy", "dropped")); got != 1 {
		t.Fatalf("dropped counted %v, want 1", got)
	}
	// Nobody picks the held row up once it is due: the lag grows, without
	// a pass, on any replica that runs Gauges.
	_, next, _ := h.outbox(h.insertAs("", "base_updated", ""))
	h.now = next.Add(7 * time.Minute)
	h.ob.Gauges(h.ctx)
	if lag := testutil.ToFloat64(h.m.OutboxLagSeconds); lag < 7*60-1 {
		t.Fatalf("lag %v for a row due 7 minutes ago", lag)
	}
}
