package httpapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/answering"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// DECISIONS I-1652: a running machine whose guestd has not answered for
// three samples in a row, two minutes apart or more, reads
// `not_answering: {since}` on GET /projects, and the warner raises one
// `not_answering` event for the stretch, which notifies nobody. One
// missed sample, a frozen machine, and a stretch an answer broke say
// nothing.
func TestMachineThatStopsAnsweringSaysSo(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-hung", "hung")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "stuck", "large")
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'running', started_at = $2 where id = $1", p.ID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	sample := func(ts time.Time, ok bool) {
		t.Helper()
		if err := db.EnsurePartitions(ctx, e.h.Pool, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, state, class, guestd_ok) values ($1, $2, 'running', 'large', $3)`, ts, p.ID, ok); err != nil {
			t.Fatal(err)
		}
	}
	silentSince := func() (string, bool) {
		t.Helper()
		r := e.do(t, tok, "GET", "/projects/"+p.ID.String(), nil)
		if r.status != 200 {
			t.Fatalf("get: %d %s", r.status, r.raw)
		}
		v, ok := r.body["not_answering"].(map[string]any)
		if !ok {
			return "", false
		}
		s, _ := v["since"].(string)
		return s, true
	}
	warner := &answering.Warner{Pool: e.h.Pool, Events: e.h.Events}
	count := func() int {
		t.Helper()
		var n int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = $2", p.ID, answering.Kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	sample(now.Add(-10*time.Minute), true)
	sample(now.Add(-9*time.Minute), false)
	sample(now.Add(-3*time.Minute), true)
	sample(now.Add(-2*time.Minute), false)
	sample(now.Add(-time.Minute), false)
	if s, ok := silentSince(); ok {
		t.Fatalf("two silent samples read not answering since %s", s)
	}
	if n, err := warner.Run(ctx, now); err != nil || n != 0 {
		t.Fatalf("warner after two silent samples: n=%d err=%v", n, err)
	}

	sample(now, false)
	s, ok := silentSince()
	if !ok {
		t.Fatal("three silent samples over two minutes: no not_answering on the project")
	}
	// Since the last sample that answered: the machine went quiet after
	// it, and the first silent sample came up to a minute later
	// (pass2-dash N5: "for 1m" 2m26s after the freeze, I-1658).
	if got, _ := time.Parse(time.RFC3339, s); !got.Equal(now.Add(-3 * time.Minute)) {
		t.Fatalf("not_answering.since %s, want %s", s, now.Add(-3*time.Minute).Format(time.RFC3339))
	}
	if n, err := warner.Run(ctx, now); err != nil || n != 1 {
		t.Fatalf("warner: n=%d err=%v, want one event", n, err)
	}
	if n, err := warner.Run(ctx, now.Add(time.Minute)); err != nil || n != 0 || count() != 1 {
		t.Fatalf("second run: n=%d err=%v events=%d, want the one event", n, err, count())
	}
	var summary string
	var outbox int
	if err := e.h.Pool.QueryRow(ctx, "select e.summary, (select count(*) from events_outbox o where o.event_id = e.id) from events e where e.project_id = $1 and e.kind = $2", p.ID, answering.Kind).Scan(&summary, &outbox); err != nil {
		t.Fatal(err)
	}
	if want := "stuck has not answered for 3m. `repose start stuck` restarts it; what was in its memory is lost."; summary != want || outbox != 0 {
		t.Fatalf("summary %q outbox %d, want %q and no notification", summary, outbox, want)
	}

	// A frozen machine's guestd is paused on purpose (I-818).
	if _, err := e.h.Pool.Exec(ctx, "update projects set resting_since = now() where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	if s, ok := silentSince(); ok {
		t.Fatalf("a frozen machine reads not answering since %s", s)
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set resting_since = null where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}

	// It answers again: nothing is said.
	sample(now.Add(time.Second), true)
	if s, ok := silentSince(); ok {
		t.Fatalf("a machine that answered again reads not answering since %s", s)
	}
	if !strings.Contains(string(e.do(t, tok, "GET", "/projects/"+p.ID.String()+"/events", nil).raw), "has not answered for 3m") {
		t.Fatal("the event is not listed under the project's events")
	}
}
