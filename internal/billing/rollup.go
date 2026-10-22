package billing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// The hourly rollup (09-billing.md §5.4, kept under §5.11): a job at :05
// past each hour, idempotent, keyed on (project_id, hour). It reads
// meter_samples and nothing upstream of it and writes usage_hours, the
// internal record of hours, disk and egress that `repose status`, GET
// /usage, the gate and the overage line read. Since plan-v1 it prices
// nothing: every cents column is zero.

// Rollup turns samples into usage_hours.
type Rollup struct {
	pool *db.Pool
	m    *metrics.M
	log  *slog.Logger
	Now  func() time.Time
}

// NewRollup builds a rollup.
func NewRollup(pool *db.Pool, m *metrics.M, log *slog.Logger) *Rollup {
	return &Rollup{pool: pool, m: m, log: log.With("component", obs.ComponentAPI), Now: time.Now}
}

// Row is one usage_hours row as computed.
type Row struct {
	ProjectID uuid.UUID
	UserID    uuid.UUID
	Hour      time.Time
	Class     string
	Gap       bool
	Period    Period
	// AttendedSeconds is the seconds of the hour with somebody on the
	// machine, and UnattendedAgentSeconds the seconds with an agent at
	// work and nobody on it (DECISIONS I-806). Both are counted in
	// whole samples, about a minute apart, as running_seconds is.
	AttendedSeconds        int
	UnattendedAgentSeconds int
	Result
}

// rollupProject is what the rollup needs to know about a project for one
// hour: its owner's live subscription period, or the calendar month.
type rollupProject struct {
	id     uuid.UUID
	user   uuid.UUID
	class  string
	state  string
	volume int64
	period Period
}

// Hour rolls up one hour [hour, hour+1h), idempotently, and returns the
// rows written.
func (r *Rollup) Hour(ctx context.Context, hour time.Time) ([]Row, error) {
	start := hour.UTC().Truncate(time.Hour)
	end := start.Add(time.Hour)
	began := r.Now()
	type agg struct {
		running    int
		class      string
		diskAlloc  int64
		egress     int64
		samples    int
		attended   int
		unattended int
	}
	aggs := map[uuid.UUID]*agg{}
	// attended and unattended come out of the same scan as the hours
	// (DECISIONS I-806). A sample counts as attended when somebody is on
	// the machine, and as unattended-agent when an agent is working and
	// nobody is: that second one is the product's claim, so it is the
	// activation figure. The agent test is idle.usedSQL's -- a state that
	// is neither idle nor needs_input -- and the two counts are disjoint
	// by construction, so neither can be read as the other.
	rows, err := r.pool.Query(ctx, `select project_id, count(*) filter (where state = 'running'), count(*), max(disk_alloc), sum(net_tx),
		(array_agg(class order by ts desc))[1],
		count(*) filter (where state = 'running' and (ssh_sessions > 0 or tmux_clients > 0)),
		count(*) filter (where state = 'running' and ssh_sessions = 0 and tmux_clients = 0
			and exists (select 1 from jsonb_array_elements(coalesce(agents, '[]'::jsonb)) a
				where coalesce(a->>'state', '') not in ('idle', 'needs_input')))
		from meter_samples where ts >= $1 and ts < $2 group by project_id`, start, end)
	if err != nil {
		return nil, fmt.Errorf("read meter_samples for %s: %w", start.Format(time.RFC3339), err)
	}
	for rows.Next() {
		var pid uuid.UUID
		var a agg
		var egress *int64
		if err := rows.Scan(&pid, &a.running, &a.samples, &a.diskAlloc, &egress, &a.class, &a.attended, &a.unattended); err != nil {
			rows.Close()
			return nil, err
		}
		if egress != nil {
			a.egress = *egress
		}
		aggs[pid] = &a
	}
	rows.Close()
	// Projects that existed during the hour get a row even without
	// samples (disk is allocated while they exist); a running project
	// without samples is a gap.
	projects, err := r.projectsFor(ctx, start, end)
	if err != nil {
		return nil, err
	}
	var out []Row
	for _, p := range projects {
		a := aggs[p.id]
		in := Inputs{Class: p.class, GBAlloc: GBCeil(p.volume)}
		row := Row{ProjectID: p.id, UserID: p.user, Hour: start, Class: p.class, Period: p.period}
		if a != nil {
			in.RunningSeconds = a.running * 60
			if a.class != "" {
				row.Class = a.class
				in.Class = a.class
			}
			if a.diskAlloc > 0 {
				in.GBAlloc = GBCeil(a.diskAlloc)
			}
			in.EgressBytes = a.egress
			row.AttendedSeconds = a.attended * 60
			row.UnattendedAgentSeconds = a.unattended * 60
		} else if p.state == "running" {
			row.Gap = true
			r.m.BillingGapMinutes.Add(60)
			r.log.Warn("no samples for a running project", "event", obs.EventBillingGap, "project_id", p.id.String(), "hour", start.Format(time.RFC3339))
		}
		row.Result = Price(in)
		if err := r.write(ctx, &row); err != nil {
			return out, err
		}
		out = append(out, row)
	}
	r.m.RollupDuration.Observe(r.Now().Sub(began).Seconds())
	r.m.RollupLagSeconds.Set(r.Now().Sub(end).Seconds())
	r.log.Info("rollup done", "event", obs.EventRollupDone, "hour", start.Format(time.RFC3339), "rows", len(out))
	return out, nil
}

// projectsFor lists the projects that existed during the hour, each with
// the billing period the hour falls in: the owner's live subscription's
// current period when the hour is inside it, else the calendar month.
func (r *Rollup) projectsFor(ctx context.Context, start, end time.Time) ([]rollupProject, error) {
	rows, err := r.pool.Query(ctx, `select p.id, p.user_id, p.class, p.state, p.volume_bytes, s.period_start, s.period_end
		from projects p
		left join lateral (select period_start, period_end from subscriptions s where s.user_id = p.user_id
			and s.status in ('trialing','active','past_due') order by created_at desc limit 1) s on true
		where p.created_at < $1 and (p.destroyed_at is null or p.destroyed_at > $2)
		and p.user_id <> '00000000-0000-7000-8000-000000000000' order by p.id`, end, start)
	if err != nil {
		return nil, fmt.Errorf("list projects for the hour: %w", err)
	}
	defer rows.Close()
	var out []rollupProject
	for rows.Next() {
		var p rollupProject
		var ps, pe *time.Time
		if err := rows.Scan(&p.id, &p.user, &p.class, &p.state, &p.volume, &ps, &pe); err != nil {
			return nil, err
		}
		switch {
		case ps != nil && pe != nil && !start.Before(ps.UTC()) && start.Before(pe.UTC()):
			p.period = Period{Start: ps.UTC(), End: pe.UTC()}
		case ps != nil:
			p.period = PeriodFor(*ps, start)
		default:
			p.period = PeriodFor(time.Time{}, start)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// write upserts the row. A re-run of the same hour writes the same values.
func (r *Rollup) write(ctx context.Context, row *Row) error {
	_, err := r.pool.Exec(ctx, `insert into usage_hours (project_id, hour, class, running_seconds, gb_alloc, egress_bytes, cost_cents,
		guest_cents, storage_cents, egress_cents, storage_remainder, gap, period_start, period_end, credit_cents, price_version,
		attended_seconds, unattended_agent_seconds)
		values ($1,$2,$3,$4,$5,$6,0,0,0,0,0,$7,$8,$9,0,$10,$11,$12)
		on conflict (project_id, hour) do update set class = excluded.class, running_seconds = excluded.running_seconds,
		gb_alloc = excluded.gb_alloc, egress_bytes = excluded.egress_bytes, cost_cents = 0, guest_cents = 0, storage_cents = 0,
		egress_cents = 0, storage_remainder = 0, gap = excluded.gap, period_start = excluded.period_start,
		period_end = excluded.period_end, credit_cents = 0, price_version = excluded.price_version,
		attended_seconds = excluded.attended_seconds, unattended_agent_seconds = excluded.unattended_agent_seconds`,
		row.ProjectID, row.Hour, row.Class, row.RunningSeconds, row.GBAlloc, row.EgressBytes, row.Gap,
		row.Period.Start, row.Period.End, PriceVersion, row.AttendedSeconds, row.UnattendedAgentSeconds)
	if err != nil {
		return fmt.Errorf("write usage_hours: %w", err)
	}
	return nil
}

// Lag publishes the age of the newest rolled-up hour. Every replica calls
// it on every minute's tick, outside the rollup's advisory lock and
// before the rollup itself, because the gauge Hour sets is reached only
// by the lock winner and only after a rollup that succeeded: a rollup
// that errored every hour, or never won the lock, left the gauge at its
// zero value, which reads as perfectly fresh, and is why RollupLag could
// not fire (DECISIONS I-800). usage_hours is what invoices are built
// from, so the gauge has to rise on its own.
//
// The lag is measured from the hour's end, as Hour measures it. With
// nothing rolled up yet it is the age of the oldest sample still waiting,
// and zero on a deployment whose meter_samples is empty.
func (r *Rollup) Lag(ctx context.Context) error {
	var newest *time.Time
	if err := r.pool.QueryRow(ctx, "select max(hour) from usage_hours").Scan(&newest); err != nil {
		return err
	}
	now := r.Now().UTC()
	if newest != nil {
		r.m.RollupLagSeconds.Set(now.Sub(newest.UTC().Add(time.Hour)).Seconds())
		return nil
	}
	var first *time.Time
	if err := r.pool.QueryRow(ctx, "select min(ts) from meter_samples").Scan(&first); err != nil {
		return err
	}
	if first == nil {
		r.m.RollupLagSeconds.Set(0)
		return nil
	}
	r.m.RollupLagSeconds.Set(now.Sub(first.UTC().Truncate(time.Hour).Add(time.Hour)).Seconds())
	return nil
}

// Due rolls up every hour from the last rolled hour to the previous full
// hour; the first run starts at the earliest sample.
func (r *Rollup) Due(ctx context.Context) (int, error) {
	now := r.Now().UTC()
	last := now.Truncate(time.Hour) // exclusive end
	var from *time.Time
	if err := r.pool.QueryRow(ctx, "select max(hour) from usage_hours").Scan(&from); err != nil {
		return 0, err
	}
	var start time.Time
	if from != nil {
		start = from.UTC().Add(time.Hour)
	} else {
		var first *time.Time
		if err := r.pool.QueryRow(ctx, "select min(ts) from meter_samples").Scan(&first); err != nil {
			return 0, err
		}
		if first == nil {
			return 0, nil
		}
		start = first.UTC().Truncate(time.Hour)
	}
	n := 0
	for h := start; h.Before(last); h = h.Add(time.Hour) {
		if _, err := r.Hour(ctx, h); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
