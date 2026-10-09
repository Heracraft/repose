// Package idle tells a user that a running machine nobody is using
// still holds its share of the plan's memory (DECISIONS I-262, I-617). It never stops one: R1-5 stands, and
// this is the warning R1-5's recorded signals make possible.
//
// A running project is idle when, for After, no meter sample showed an SSH
// session, a tmux client or an agent that is working. An agent sitting at
// its prompt (state idle or needs_input) does not count as use: that is
// what a forgotten machine looks like, with the agent `repose run` started
// still open. A sample with guestd not answering counts as use, since it
// says nothing about who is attached.
package idle

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
)

// After is how long a running machine goes unused before it is idle.
const After = 24 * time.Hour

// Fresh is how recent the newest sample must be for the signals to speak
// for the machine now: an unreachable host sends none, and silence is not
// evidence that nobody is attached.
const Fresh = 10 * time.Minute

// Lookback bounds how far back GET /projects looks for the last use, so
// a machine up for months costs a list no more than two weeks of its
// samples; `since` is then the start of that window, a floor. The warner
// looks back to started_at, where the stretch's start stays put.
const Lookback = 14 * 24 * time.Hour

// Kind is the event (and notification) an idle episode raises, once.
const Kind = "idle_running"

// Signals is what the samples say about one running project.
type Signals struct {
	// Newest is the newest sample since the guest started, nil when none.
	Newest *time.Time
	// LastUsed is the newest such sample showing use, nil when none did.
	LastUsed *time.Time
}

// Since computes when the current idle stretch began and whether it has
// lasted After. startedAt is the project's started_at; state its state.
func Since(now time.Time, state string, startedAt *time.Time, hostUnreachable bool, s Signals) (time.Time, bool) {
	if state != "running" || startedAt == nil || hostUnreachable {
		return time.Time{}, false
	}
	if s.Newest == nil || now.Sub(*s.Newest) > Fresh {
		return time.Time{}, false
	}
	since := *startedAt
	if s.LastUsed != nil && s.LastUsed.After(since) {
		since = *s.LastUsed
	}
	if now.Sub(since) < After {
		return time.Time{}, false
	}
	return since, true
}

// usedSQL is a sample that shows somebody, or some agent, at work.
const usedSQL = `(ssh_sessions > 0 or tmux_clients > 0 or not guestd_ok
	or exists (select 1 from jsonb_array_elements(coalesce(agents, '[]'::jsonb)) a
	           where coalesce(a->>'state', '') not in ('idle', 'needs_input')))`

// ReadSignals reads the two sample times for a project since from. The
// (project_id, ts) key bounds both to the project's own rows.
func ReadSignals(ctx context.Context, q store.Querier, projectID uuid.UUID, from time.Time) (Signals, error) {
	var s Signals
	err := q.QueryRow(ctx, `select
		(select max(ts) from meter_samples where project_id = $1 and ts >= $2),
		(select max(ts) from meter_samples where project_id = $1 and ts >= $2 and `+usedSQL+`)`,
		projectID, from).Scan(&s.Newest, &s.LastUsed)
	return s, err
}

// Project reports whether p is idle now and since when, looking back at
// most Lookback.
func Project(ctx context.Context, q store.Querier, p *store.Project, now time.Time) (time.Time, bool, error) {
	if p.State != "running" || p.StartedAt == nil || p.HostUnreachable {
		return time.Time{}, false, nil
	}
	from := *p.StartedAt
	if w := now.Add(-Lookback); w.After(from) {
		from = w
	}
	s, err := ReadSignals(ctx, q, p.ID, from)
	if err != nil {
		return time.Time{}, false, err
	}
	since, ok := Since(now, p.State, &from, p.HostUnreachable, s)
	return since, ok, nil
}

// Summary is the notification body: counts, times and the class only,
// never anything from inside the guest. Since I-289 a plan buys memory
// that may run at once, so an idle machine costs its share of the plan,
// not a rate.
func Summary(slug, class string, idleFor time.Duration) string {
	return fmt.Sprintf("%s has had no SSH session and no agent working for %dh, and holds %d GB of your plan's memory (%s) until you stop it: `repose stop %s`.",
		slug, int(idleFor/time.Hour), billing.ClassMemoryGB(class), class, slug)
}

// Warner raises Kind once per idle episode per project. An episode starts
// where the idle stretch starts, so an attach, a working agent or a
// restart ends it and the next stretch warns again; the same stretch
// never warns twice, whichever replica or restart looks at it.
type Warner struct {
	Pool   *db.Pool
	Events *events.Ingest
}

// Run checks every running project once and returns how many it warned.
// The caller holds the lock that keeps it to one replica.
func (w *Warner) Run(ctx context.Context, now time.Time) (int, error) {
	rows, err := w.Pool.Query(ctx, `select id, slug, class, started_at from projects
		where state = 'running' and not host_unreachable and started_at is not null and started_at <= $1`, now.Add(-After))
	if err != nil {
		return 0, err
	}
	type cand struct {
		id          uuid.UUID
		slug, class string
		started     time.Time
	}
	var cs []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.slug, &c.class, &c.started); err != nil {
			rows.Close()
			return 0, err
		}
		cs = append(cs, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cs {
		s, err := ReadSignals(ctx, w.Pool, c.id, c.started)
		if err != nil {
			return n, err
		}
		since, ok := Since(now, "running", &c.started, false, s)
		if !ok {
			continue
		}
		var warned bool
		if err := w.Pool.QueryRow(ctx, "select exists(select 1 from events where project_id = $1 and kind = $2 and ts >= $3)", c.id, Kind, since).Scan(&warned); err != nil {
			return n, err
		}
		if warned {
			continue
		}
		if _, _, err := w.Events.Insert(ctx, events.Incoming{ProjectID: c.id, TS: now, Kind: Kind, Summary: Summary(c.slug, c.class, now.Sub(since)), Source: "api"}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
