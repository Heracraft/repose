// Package answering tells a user that a running machine has stopped
// answering (DECISIONS I-1652). hostd asks each guest's guestd for a
// sample every minute; when guestd does not answer, the sample says
// `guestd_ok` false. A machine whose guest has hung, its memory full or
// its kernel stuck, sends nothing else: its state stays `running` and its
// host stays online, and until this the only word for it was a line in
// `repose status`. Here a run of such samples long enough not to be one
// slow answer becomes `Project.not_answering` and one `not_answering`
// event per stretch.
//
// It never restarts the machine: a restart loses what is in its memory,
// and the user decides that (`repose start`, the dashboard's Restart).
package answering

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// Samples is how many samples in a row must say guestd did not answer
// before the machine is called not answering. hostd samples every minute
// and gives each guest 10 seconds, so one busy minute misses one sample;
// three in a row is about three minutes of silence (I-1652).
const Samples = 3

// Span is the least time from the first of those samples to the newest,
// so a host that sent several samples close together (a reconnect's
// resend) does not count as minutes of silence.
const Span = 2 * time.Minute

// Fresh is how recent the newest sample must be: a host that stopped
// sending has its own word (`host_unreachable`), and an old silence says
// nothing about the machine now. It is the api's guestdStaleAfter.
const Fresh = 5 * time.Minute

// Kind is the event a silent stretch raises, once.
const Kind = "not_answering"

// Silence is the trailing run of samples, since the guest last started,
// in which guestd did not answer: from the first of them to the newest.
type Silence struct {
	// Since is when it went quiet: the last sample that answered, or the
	// first silent one when none since the start did.
	Since   time.Time
	First   time.Time
	Newest  time.Time
	Samples int
}

// Long reports whether s is long enough to show at now.
func (s Silence) Long(now time.Time) bool {
	return s.Samples >= Samples && s.Newest.Sub(s.First) >= Span && now.Sub(s.Newest) < Fresh
}

// Read reads the silent run of a project's samples since startedAt: the
// samples after the newest one where guestd answered or the guest was
// paused (a paused guest's guestd is paused with it and hostd reports it
// as answering, I-818). The (project_id, ts) key bounds it to the
// project's own rows.
func Read(ctx context.Context, q store.Querier, projectID uuid.UUID, startedAt time.Time) (Silence, error) {
	var first, newest, answered *time.Time
	var n int
	err := q.QueryRow(ctx, `with last_ok as (select max(ts) as ts from meter_samples where project_id = $1 and ts >= $2 and (guestd_ok or resting))
		select min(m.ts), max(m.ts), count(*), (select ts from last_ok) from meter_samples m
		where m.project_id = $1 and m.ts >= $2
		  and m.ts > coalesce((select ts from last_ok), $2 - interval '1 second')`,
		projectID, startedAt).Scan(&first, &newest, &n, &answered)
	if err != nil || first == nil || newest == nil {
		return Silence{}, err
	}
	// Silent since the last sample that answered, when there is one: the
	// guest went quiet after it, and the first silent sample came up to a
	// minute later, so counting from that one read "for 1m" two and a
	// half minutes in (pass2-dash N5, I-1658). Long still counts from the
	// first silent sample.
	s := Silence{Since: *first, First: *first, Newest: *newest, Samples: n}
	if answered != nil {
		s.Since = *answered
	}
	return s, nil
}

// Project reports since when p has not answered, and whether it is so
// now. A machine that is not running, is frozen, or whose host is not
// answering is never one: the first has nothing to answer, the second is
// paused on purpose, and the third is said about the host (I-1370).
func Project(ctx context.Context, q store.Querier, p *store.Project, now time.Time) (time.Time, bool, error) {
	if p.State != "running" || p.StartedAt == nil || p.RestingSince != nil || p.HostUnreachable {
		return time.Time{}, false, nil
	}
	s, err := Read(ctx, q, p.ID, *p.StartedAt)
	if err != nil || !s.Long(now) {
		return time.Time{}, false, err
	}
	return s.Since, true, nil
}

// Summary is the event's text: how long, and the one thing that brings
// the machine back, with what it costs.
func Summary(slug string, silent time.Duration) string {
	return fmt.Sprintf("%s has not answered for %s. `repose start %s` restarts it; what was in its memory is lost.", slug, Duration(silent), slug)
}

// Duration is how long, in the shortest true unit: "3m", "2h", "1d".
func Duration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d/time.Minute), 1))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// Warner raises Kind once per silent stretch per project. A stretch
// starts at its first silent sample, so a machine that answers again and
// then goes quiet raises a second one; the same stretch never raises two,
// whichever replica or restart looks at it.
type Warner struct {
	Pool   *db.Pool
	Events *events.Ingest
}

// Run checks every running project whose newest sample says guestd did
// not answer, and returns how many it raised. The caller holds the lock
// that keeps it to one replica.
func (w *Warner) Run(ctx context.Context, now time.Time) (int, error) {
	rows, err := w.Pool.Query(ctx, `select p.id, p.slug, p.started_at from projects p
		where p.state = 'running' and not p.host_unreachable and p.resting_since is null and p.started_at is not null
		  and exists (select 1 from meter_samples m where m.project_id = p.id and m.ts > $1 and m.ts >= p.started_at and not m.guestd_ok and not m.resting)`,
		now.Add(-Fresh))
	if err != nil {
		return 0, err
	}
	type cand struct {
		id      uuid.UUID
		slug    string
		started time.Time
	}
	var cs []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.slug, &c.started); err != nil {
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
		s, err := Read(ctx, w.Pool, c.id, c.started)
		if err != nil {
			return n, err
		}
		if !s.Long(now) {
			continue
		}
		var raised bool
		if err := w.Pool.QueryRow(ctx, "select exists(select 1 from events where project_id = $1 and kind = $2 and ts >= $3)", c.id, Kind, s.Since).Scan(&raised); err != nil {
			return n, err
		}
		if raised {
			continue
		}
		if _, _, err := w.Events.Insert(ctx, events.Incoming{ProjectID: c.id, TS: now, Kind: Kind, Summary: Summary(c.slug, now.Sub(s.Since)), Source: "api"}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
