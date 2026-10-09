// Package temp ends temporary machines (DECISIONS I-347): a project made
// with `repose run --temp` has expires_at set, and once that has passed
// the api destroys it with no snapshot. R1-5 still stands: nothing here
// looks at whether a machine is used to decide that it should go. The
// lifetime is one its owner chose when creating it, and `repose keep`
// takes it back. What the samples decide is only when: the destroy waits
// while somebody or some agent is on the machine, up to Grace past the
// expiry.
package temp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/meter"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/multiplexer"
)

// The lifetime `POST /projects` accepts in expires_in_s.
const (
	MinLifetime = 10 * time.Minute
	MaxLifetime = 24 * time.Hour
)

// Grace is how long past its expiry a machine somebody is on waits; from
// then the destroy goes ahead regardless.
const Grace = 24 * time.Hour

// Fresh is how recent the newest sample must be to hold the destroy: an
// unreachable host sends none, and a sample from before it went quiet
// says nothing about now.
const Fresh = 10 * time.Minute

// WarnBefore is when temp_expiring goes out. A machine made with a
// lifetime of WarnBefore or less gets none: it would go out at once.
const WarnBefore = time.Hour

// RetryAfterFailure is how long the reaper leaves a temporary project
// whose destroy failed before it tries again (I-350): each failure sends
// destroy_failed, and a host that cannot delete a volume would otherwise
// notify every minute.
const RetryAfterFailure = 10 * time.Minute

// The event kinds (both notify).
const (
	KindExpiring  = "temp_expiring"
	KindDestroyed = "temp_destroyed"
)

// ExpiringSummary is the temp_expiring notification's body.
func ExpiringSummary(slug string, left time.Duration) string {
	return fmt.Sprintf("%s is temporary: it is destroyed in %s, with no snapshot kept. `repose keep %s` keeps it.", slug, Left(left), slug)
}

// Left renders a time left the way the CLI does: minutes under an hour,
// else whole hours, rounded up so "destroyed in 1h" is never early.
func Left(d time.Duration) string {
	if d < time.Hour {
		m := int((d + time.Minute - 1) / time.Minute)
		if m < 1 {
			m = 1
		}
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh", int((d+time.Hour-1)/time.Hour))
}

// Busy reports whether a sample shows somebody or some agent on the
// machine: an ssh session, a tmux client, or an agent that is not at its
// prompt (idle or needs_input), as idle.usedSQL counts use. A sample with
// guestd not answering is not busy here: it says nothing either way, and
// the Grace bound makes waiting on it pointless.
func Busy(l *meter.Latest) bool {
	if l == nil {
		return false
	}
	if l.SSHSessions > 0 || l.TmuxClients > 0 {
		return true
	}
	return agentWorking(l)
}

// agentWorking says an agent in the sample is not at its prompt.
func agentWorking(l *meter.Latest) bool {
	for _, a := range l.Agents {
		if s := a["state"]; s != "idle" && s != "needs_input" {
			return true
		}
	}
	return false
}

// Holds reports whether the destroy of a project that expired at
// expiresAt waits at now, given its newest sample (nil when none) and the
// multiplexer it runs. On herdr only a working agent holds it: a laptop
// herdr keeps an SSH connection to each machine in its sidebar whether
// anyone looks at it or not (I-511), so a session says nothing there, and
// the machine goes at its expiry (I-602).
func Holds(now, expiresAt time.Time, l *meter.Latest, mux string) bool {
	if !now.Before(expiresAt.Add(Grace)) {
		return false
	}
	if l == nil || now.Sub(l.TS) > Fresh {
		return false
	}
	if multiplexer.Normalize(mux) == multiplexer.Herdr {
		return agentWorking(l)
	}
	return Busy(l)
}

// Reaper warns about and destroys temporary projects. The caller runs it
// once a minute under db.LockSweeper, so one replica does.
type Reaper struct {
	Pool   *db.Pool
	Engine *ops.Engine
	Events *events.Ingest
	Log    *slog.Logger
}

// Result counts one run.
type Result struct {
	Warned, Destroyed, Waiting int
}

// Run warns the projects an hour from their expiry and enqueues the
// destroy of the expired ones.
func (r *Reaper) Run(ctx context.Context, now time.Time) (Result, error) {
	var res Result
	n, err := r.warn(ctx, now)
	res.Warned = n
	if err != nil {
		return res, err
	}
	ids, err := r.candidates(ctx, now)
	if err != nil {
		return res, err
	}
	for _, id := range ids {
		destroyed, waiting, err := r.reap(ctx, id, now)
		if err != nil {
			return res, err
		}
		if destroyed {
			res.Destroyed++
		}
		if waiting {
			res.Waiting++
		}
	}
	if res.Destroyed > 0 {
		r.Engine.Kick()
	}
	return res, nil
}

// live is a temporary project the reaper may act on: not destroyed and
// not being destroyed.
const live = `expires_at is not null and destroyed_at is null and state not in ('destroying', 'destroyed')`

func (r *Reaper) warn(ctx context.Context, now time.Time) (int, error) {
	rows, err := r.Pool.Query(ctx, `select id, slug, expires_at from projects p
		where `+live+` and expires_at > $1 and expires_at <= $2 and expires_at - created_at > interval '1 second' * $3
		and not exists (select 1 from events e where e.project_id = p.id and e.kind = $4)`,
		now, now.Add(WarnBefore), int64(WarnBefore/time.Second), KindExpiring)
	if err != nil {
		return 0, err
	}
	type cand struct {
		id      uuid.UUID
		slug    string
		expires time.Time
	}
	var cs []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.slug, &c.expires); err != nil {
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
		if _, _, err := r.Events.Insert(ctx, events.Incoming{ProjectID: c.id, TS: now, Kind: KindExpiring, Summary: ExpiringSummary(c.slug, c.expires.Sub(now)), Source: "api"}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (r *Reaper) candidates(ctx context.Context, now time.Time) ([]uuid.UUID, error) {
	rows, err := r.Pool.Query(ctx, `select id from projects where `+live+` and expires_at <= $1 order by expires_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

var errSkip = errors.New("temporary project no longer due")

// reap is one project in its own transaction: locked with skip locked
// (another replica, or a user's DELETE, has it), the rule checked again
// inside, then the destroy enqueued behind any open op and the project
// marked destroying, as DELETE /projects/:id does.
func (r *Reaper) reap(ctx context.Context, id uuid.UUID, now time.Time) (destroyed, waiting bool, err error) {
	var opID uuid.UUID
	err = db.InTx(ctx, r.Pool, func(tx db.Tx) error {
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, `select id from projects where id = $1 and `+live+` and expires_at <= $2 for update skip locked`, id, now).Scan(&locked); err != nil {
			if db.IsNoRows(err) {
				return errSkip
			}
			return err
		}
		p, err := store.GetProject(ctx, tx, id)
		if err != nil {
			return err
		}
		var openDestroy bool
		var lastFailed *time.Time
		if err := tx.QueryRow(ctx, `select
				exists(select 1 from ops where project_id = $1 and kind = $2 and state in ('pending', 'running')),
				(select max(finished_at) from ops where project_id = $1 and kind = $2 and state = 'error')`,
			id, ops.KindDestroy).Scan(&openDestroy, &lastFailed); err != nil {
			return err
		}
		if openDestroy || (lastFailed != nil && now.Sub(*lastFailed) < RetryAfterFailure) {
			return errSkip
		}
		l, ok, err := meter.LatestSample(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			l = nil
		}
		if Holds(now, *p.ExpiresAt, l, p.Multiplexer) {
			waiting = true
			return errSkip
		}
		opID, err = r.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindDestroy, ProjectID: &id, Phases: ops.PlanDestroy(p), Params: map[string]any{"expired": true}}, true)
		if err != nil {
			return err
		}
		return store.SetProjectState(ctx, tx, id, "destroying")
	})
	if errors.Is(err, errSkip) {
		return false, waiting, nil
	}
	if err != nil {
		return false, false, err
	}
	if r.Log != nil {
		r.Log.Info("temporary project expired", "event", "temp_expire", "project_id", id.String(), "op_id", opID.String())
	}
	return true, false, nil
}
