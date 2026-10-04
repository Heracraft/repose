// Package abuse is the api's automatic response to a cryptocurrency miner
// in a guest (DECISIONS I-239) and the abuse gauges the alert rules read.
//
// A guest whose process sample names a miner (internal/abuse.MinerName,
// the name only) is stopped through the normal stop op with a snapshot, so
// nothing is lost; the stop is recorded in abuse_events, the project's
// last_error says why in plain words, the user gets an abuse_stopped
// notification and the operator gets the MinerStopped alert. The user is
// never suspended from here: that is `repose-admin users suspend`, a
// human's decision. The third stop within 24 hours puts the project on
// hold: starting it is refused until `repose-admin abuse clear`.
package abuse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	miners "github.com/heracraft/repose/internal/abuse"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// KindMiner is abuse_events.kind and the stops counter's kind label.
const KindMiner = "miner"

// EventKind is the platform event (and notification) a stop sends.
const EventKind = "abuse_stopped"

// ErrorCode prefixes projects.last_error for an abuse stop, in the
// "code: message" form every last_error has (I-159).
const ErrorCode = "abuse_stopped"

// Strikes stops within StrikeWindow put a project on hold.
const (
	Strikes      = 3
	StrikeWindow = 24 * time.Hour
)

// TermsURL is where the messages point; app sets it from DASHBOARD_URL.
var TermsURL = "https://repose.herakraft.co/terms"

// Stopper enqueues the stop op; *ops.Engine satisfies it.
type Stopper interface {
	Enqueue(ctx context.Context, q store.Querier, n ops.NewOp, allowQueue bool) (uuid.UUID, error)
	Kick()
}

// EventSink records the user-facing event; events.Ingest.Platform
// satisfies it.
type EventSink interface {
	Platform(ctx context.Context, projectID uuid.UUID, kind, summary string) error
}

// Guard watches samples for miners and refreshes the abuse gauges.
type Guard struct {
	pool   *db.Pool
	stop   Stopper
	events EventSink
	m      *metrics.M
	log    *slog.Logger
	Now    func() time.Time
}

// New builds a guard.
func New(pool *db.Pool, stop Stopper, events EventSink, m *metrics.M, log *slog.Logger) *Guard {
	return &Guard{pool: pool, stop: stop, events: events, m: m, log: log, Now: time.Now}
}

// StoppedMessage is the reason shown on `repose status` and the dashboard
// after a stop, and the notification's text.
func StoppedMessage(process string) string {
	return fmt.Sprintf("stopped: a cryptocurrency miner (%s) was running; mining is not allowed on repose, see the terms at %s", process, TermsURL)
}

// OnSamples stops every running guest whose sample names a miner. It runs
// after meter ingest on the host stream; a failure is logged and retried
// by the next sample a minute later, so a miner that survives one error
// does not survive the next.
func (g *Guard) OnSamples(ctx context.Context, hostID uuid.UUID, s *hostdv1.Samples) {
	ts := time.Unix(s.Ts, 0).UTC()
	if s.Ts == 0 {
		ts = g.Now().UTC()
	}
	for _, gs := range s.Guests {
		if gs.State != "running" {
			continue
		}
		process := ""
		for _, p := range gs.Procs {
			if name, ok := miners.MinerName(p.Comm); ok {
				process = name
				break
			}
		}
		if process == "" {
			continue
		}
		if err := g.stopForMiner(ctx, hostID, gs.GuestId, process, ts); err != nil && ctx.Err() == nil {
			g.log.Error("abuse stop failed", "event", "abuse_stop_fail", "host_id", hostID.String(), "guest_id", gs.GuestId, "kind", KindMiner, "err", err.Error())
		}
	}
}

// stopForMiner is idempotent: a project that is not running, whose sample
// predates its current start (a resent sample from before the last stop),
// or that already has an op in flight (this stop, still running) is left
// alone, so one miner is one stop however many samples name it.
func (g *Guard) stopForMiner(ctx context.Context, hostID uuid.UUID, guestID, process string, sampleTS time.Time) error {
	gid, err := uuid.Parse(guestID)
	if err != nil {
		return nil
	}
	// Only the host a project is placed on can have it stopped (I-447).
	p, err := store.GetProjectOnHost(ctx, g.pool, gid, hostID)
	if err != nil {
		if db.IsNoRows(err) || errors.Is(err, store.ErrOtherHost) {
			return nil
		}
		return err
	}
	// Sample times are whole seconds.
	if p.StartedAt != nil && sampleTS.Before(p.StartedAt.Truncate(time.Second)) {
		return nil
	}
	var (
		opID   uuid.UUID
		strike int
		hold   bool
		acted  bool
	)
	err = db.InTx(ctx, g.pool, func(tx db.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, "select state from projects where id = $1 for update", p.ID).Scan(&state); err != nil {
			return err
		}
		if state != "running" && state != "starting" {
			return nil
		}
		id, err := g.stop.Enqueue(ctx, tx, ops.NewOp{
			Kind: ops.KindStop, ProjectID: &p.ID, Phases: ops.PlanStop(),
			Params: map[string]any{"snapshot": true, "reason": "abuse"},
		}, false)
		if errors.Is(err, ops.ErrOpInProgress) {
			return nil
		}
		if err != nil {
			return err
		}
		var before int
		if err := tx.QueryRow(ctx, `select count(*) from abuse_events where project_id = $1 and kind = $2 and cleared_at is null and ts > $3`,
			p.ID, KindMiner, g.Now().Add(-StrikeWindow)).Scan(&before); err != nil {
			return err
		}
		strike = before + 1
		hold = strike >= Strikes
		if _, err := tx.Exec(ctx, `insert into abuse_events (id, project_id, user_id, ts, kind, detail, op_id, hold) values ($1, $2, $3, $4, $5, $6, $7, $8)`,
			store.NewID(), p.ID, p.UserID, g.Now(), KindMiner, map[string]any{"process": process}, id, hold); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, ErrorCode+": "+StoppedMessage(process)); err != nil {
			return err
		}
		opID, acted = id, true
		return nil
	})
	if err != nil || !acted {
		return err
	}
	g.stop.Kick()
	g.m.AbuseStopsTotal.WithLabelValues(KindMiner).Inc()
	g.log.Warn("guest stopped: a miner was running", "event", "abuse_stop", "project_id", p.ID.String(), "user_id", p.UserID.String(),
		"op_id", opID.String(), "kind", KindMiner, "process", process, "count", strike, "hold", hold)
	summary := StoppedMessage(process)
	if hold {
		summary += fmt.Sprintf(". This is the %d%s stop in 24 hours, so %s cannot be started again until we have reviewed it", strike, ordinal(strike), p.Slug)
	}
	if g.events != nil {
		if err := g.events.Platform(ctx, p.ID, EventKind, summary); err != nil {
			return fmt.Errorf("abuse_stopped event: %w", err)
		}
	}
	return nil
}

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return "th"
	case n%10 == 1:
		return "st"
	case n%10 == 2:
		return "nd"
	case n%10 == 3:
		return "rd"
	}
	return "th"
}

// Hold is a project that may not start: the newest held stop.
type Hold struct {
	Process string
	At      time.Time
}

// StartHold reports whether projectID is on hold (an uncleared held stop).
func StartHold(ctx context.Context, q store.Querier, projectID uuid.UUID) (*Hold, error) {
	var h Hold
	err := q.QueryRow(ctx, `select coalesce(detail->>'process', ''), ts from abuse_events
		where project_id = $1 and hold and cleared_at is null order by ts desc limit 1`, projectID).Scan(&h.Process, &h.At)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// HoldMessage is the refusal a start of a held project gets.
func HoldMessage(slug string, h *Hold) string {
	return fmt.Sprintf("%s is on hold: it was stopped %d times within 24 hours because a cryptocurrency miner (%s) was running, and mining is not allowed on repose (%s). It cannot be started until we review it; reply to the notification or write to the contact address on the site",
		slug, Strikes, h.Process, TermsURL)
}

// Clear lifts every uncleared stop of a project (the hold and the strikes
// toward the next one) and returns how many it cleared.
func Clear(ctx context.Context, q store.Querier, projectID uuid.UUID, actor string) (int64, error) {
	tag, err := q.Exec(ctx, "update abuse_events set cleared_at = now(), cleared_by = $2 where project_id = $1 and cleared_at is null", projectID, actor)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// busyUnattendedSQL counts projects that for the last six hours ran every
// vCPU at 90 percent or more with no SSH session, no tmux client and no
// agent in any sample, and guestd answering in all of them (a sample with
// guestd down says nothing about who is attached). An agent running for
// hours unattended is the product, so a present agent excludes a project.
// cpu_ns is the guest unit's CPU per sample, about 60 s apart.
const busyUnattendedSQL = `select count(*) from (
	select project_id from meter_samples
	where ts > $1 and state = 'running'
	group by project_id
	having min(ts) < $2 and count(*) >= $3
	   and sum(cpu_ns)::float8 / (count(*) * 60e9 * max(case class when 'small' then 2 when 'xl' then 8 else 4 end)) >= 0.9
	   and max(ssh_sessions) = 0 and max(tmux_clients) = 0
	   and max(jsonb_array_length(coalesce(agents, '[]'::jsonb))) = 0
	   and bool_and(guestd_ok)
) s`

// BusyUnattendedWindow is how long a guest must be busy with nobody there.
const BusyUnattendedWindow = 6 * time.Hour

// egressHighSQL counts projects over a terabyte of egress in 24 hours, the
// input of EgressHigh.
const egressHighSQL = `select count(*) from (
	select project_id from meter_samples where ts > $1 group by project_id having sum(net_tx) > 1e12
) s`

// Refresh recomputes the gauges the abuse alerts read. Every replica may
// run it; the queries only read and the alerts take the max.
func (g *Guard) Refresh(ctx context.Context) error {
	now := g.Now()
	var busy int
	// 300 samples of the 360 a six-hour window holds: the guest ran the
	// whole window, with room for a missed minute or a hostd restart.
	if err := g.pool.QueryRow(ctx, busyUnattendedSQL, now.Add(-BusyUnattendedWindow), now.Add(-BusyUnattendedWindow+10*time.Minute), 300).Scan(&busy); err != nil {
		return fmt.Errorf("busy unattended: %w", err)
	}
	g.m.AbuseBusyUnattendedProjects.Set(float64(busy))
	var egress int
	if err := g.pool.QueryRow(ctx, egressHighSQL, now.Add(-24*time.Hour)).Scan(&egress); err != nil {
		return fmt.Errorf("egress high: %w", err)
	}
	g.m.EgressAlertProjects.Set(float64(egress))
	var held int
	if err := g.pool.QueryRow(ctx, "select count(distinct project_id) from abuse_events where hold and cleared_at is null").Scan(&held); err != nil {
		return fmt.Errorf("held: %w", err)
	}
	g.m.AbuseHeldProjects.Set(float64(held))
	return nil
}
