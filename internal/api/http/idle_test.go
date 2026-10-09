package httpapi_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/idle"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// DECISIONS I-262: a running project with no SSH session, no tmux client
// and no working agent for a day shows `idle` on GET /projects, and the
// warner raises one idle_running event (with its email) per idle stretch,
// never a stop. An agent at its prompt is not use; an attach is.
func TestIdleRunningProjectWarnsOncePerStretch(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-idle", "idler")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "forgotten", "large")
	now := time.Now().UTC().Truncate(time.Second)
	started := now.Add(-30 * time.Hour)
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'running', started_at = $2 where id = $1", p.ID, started); err != nil {
		t.Fatal(err)
	}
	sample := func(ts time.Time, ssh, tmux int, agents string, guestdOK bool) {
		t.Helper()
		if err := db.EnsurePartitions(ctx, e.h.Pool, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, ssh_sessions, tmux_clients, agents, guestd_ok)
			values ($1, $2, 'running', 'large', $3, $4, $5::jsonb, $6)`, ts, p.ID, ssh, tmux, agents, guestdOK); err != nil {
			t.Fatal(err)
		}
	}
	idleOf := func() map[string]any {
		t.Helper()
		r := e.do(t, tok, "GET", "/projects/"+p.ID.String(), nil)
		if r.status != 200 {
			t.Fatalf("get: %d %s", r.status, r.raw)
		}
		v, _ := r.body["idle"].(map[string]any)
		return v
	}
	warner := &idle.Warner{Pool: e.h.Pool, Events: e.h.Events}
	events := func() int {
		t.Helper()
		var n int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = 'idle_running'", p.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Attached 20 hours ago; since then only an agent sitting at its prompt.
	sample(now.Add(-29*time.Hour), 0, 0, `[{"agent":"claude","window":"claude","state":"working"}]`, true)
	sample(now.Add(-20*time.Hour), 1, 1, `[]`, true)
	sample(now.Add(-2*time.Minute), 0, 0, `[{"agent":"claude","window":"claude","state":"idle"}]`, true)
	if v := idleOf(); v != nil {
		t.Fatalf("idle after 20h: %v", v)
	}
	if n, err := warner.Run(ctx, now); err != nil || n != 0 || events() != 0 {
		t.Fatalf("warned at 20h: n=%d err=%v events=%d", n, err, events())
	}

	// A day and more: idle, with the large rate, warned once.
	later := now.Add(6 * time.Hour)
	sample(later.Add(-time.Minute), 0, 0, `[{"agent":"claude","window":"claude","state":"needs_input"}]`, true)
	if n, err := warner.Run(ctx, later); err != nil || n != 1 {
		t.Fatalf("warner at 26h: n=%d err=%v", n, err)
	}
	if n, err := warner.Run(ctx, later.Add(time.Minute)); err != nil || n != 0 {
		t.Fatalf("second run warned again: n=%d err=%v", n, err)
	}
	if events() != 1 {
		t.Fatalf("idle_running events = %d, want 1", events())
	}
	var summary string
	var outbox int
	if err := e.h.Pool.QueryRow(ctx, "select e.summary, (select count(*) from events_outbox o where o.event_id = e.id) from events e where e.project_id = $1 and e.kind = 'idle_running'", p.ID).Scan(&summary, &outbox); err != nil {
		t.Fatal(err)
	}
	if outbox == 0 || !strings.Contains(summary, "repose stop forgotten") || !strings.Contains(summary, "holds 8 GB of your plan's memory") || !strings.Contains(summary, "for 26h") {
		t.Fatalf("summary %q, outbox rows %d", summary, outbox)
	}
	if st := e.h.Project(p.ID).State; st != "running" {
		t.Fatalf("the warning changed the state to %s", st)
	}

	// An attach ends the stretch; the next day-long stretch warns again.
	sample(later.Add(-30*time.Second), 1, 1, `[]`, true)
	if n, _ := warner.Run(ctx, later); n != 0 {
		t.Fatalf("warned right after an attach")
	}
	// The events table stamps the wall clock; move the first warning out
	// of the 60-second dedupe window a real day apart would leave anyway.
	if _, err := e.h.Pool.Exec(ctx, "update events set ts = ts - interval '1 hour', ts_second = ts_second - 3600 where project_id = $1 and kind = 'idle_running'", p.ID); err != nil {
		t.Fatal(err)
	}
	again := later.Add(25 * time.Hour)
	sample(again.Add(-time.Minute), 0, 0, `[]`, true)
	if n, err := warner.Run(ctx, again); err != nil || n != 1 || events() != 2 {
		t.Fatalf("second stretch: n=%d err=%v events=%d", n, err, events())
	}

	// guestd not answering says nothing about who is there: not idle.
	sample(again, 0, 0, `[]`, false)
	s, err := idle.ReadSignals(ctx, e.h.Pool, p.ID, started)
	if err != nil || s.LastUsed == nil || !s.LastUsed.Equal(again) || !s.Newest.Equal(again) {
		t.Fatalf("a sample with guestd down is not counted as use: %+v %v", s, err)
	}
}

// GET /projects carries idle.since and idle.hourly_cents for an idle
// project (docs/interfaces/api.md).
func TestProjectJSONIdle(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-idlej", "idlej")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "quiet", "small")
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'running', started_at = $2 where id = $1", p.ID, now.Add(-27*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, e.h.Pool, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class) values ($1, $2, 'running', 'small')`, now.Add(-time.Minute), p.ID); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "GET", "/projects", nil)
	if r.status != 200 {
		t.Fatalf("list: %d %s", r.status, r.raw)
	}
	var list []struct {
		Idle *struct {
			Since       time.Time `json:"since"`
			HourlyCents int64     `json:"hourly_cents"`
		} `json:"idle"`
	}
	if err := json.Unmarshal([]byte(r.raw), &list); err != nil {
		t.Fatal(err)
	}
	// hourly_cents is 0 since I-289 (a plan buys memory, not hours) and
	// stays one release.
	if len(list) != 1 || list[0].Idle == nil || list[0].Idle.HourlyCents != 0 || !list[0].Idle.Since.Equal(now.Add(-27*time.Hour)) {
		t.Fatalf("idle in list: %s", r.raw)
	}
}

// A machine idle longer than idle.Lookback shows the window's start: the
// list reads two weeks of samples, not months.
func TestProjectJSONIdleLookback(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-idlel", "idlel")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "ancient", "large")
	now := time.Now().UTC()
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'running', started_at = $2 where id = $1", p.ID, now.Add(-40*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, e.h.Pool, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class) values ($1, $2, 'running', 'large')`, now.Add(-time.Minute), p.ID); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "GET", "/projects/"+p.ID.String(), nil)
	v, _ := r.body["idle"].(map[string]any)
	if v == nil {
		t.Fatalf("not idle: %s", r.raw)
	}
	since, err := time.Parse(time.RFC3339Nano, v["since"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if d := now.Sub(since); d < idle.Lookback-time.Minute || d > idle.Lookback+time.Minute {
		t.Fatalf("since %v is %v ago, want about %v", since, d, idle.Lookback)
	}
}
