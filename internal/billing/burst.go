package billing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/obs"
)

// Burst metering and its allowance (DECISIONS I-1126, I-1128). A machine
// on a host that bursts may use cores above the base its class sells
// while the host has them idle; hostd counts that time and sends it in
// each sample, the rollup sums it into usage_hours, and this file is
// what stops it when a plan's allowance for the period runs out.
//
// Nothing here charges anybody. With overage off -- which is the only
// state that exists today -- passing the allowance costs nothing and
// stops nothing: the account's machines go back to the base cores their
// classes sell until the period ends, and every build and agent keeps
// running. The overage switch, its cap and the Polar line are the
// proposal's stage 4 and are not built.

// NsPerCoreHour is one core-hour in nanoseconds of CPU time.
const NsPerCoreHour int64 = 3600 * 1_000_000_000

// BurstSender sends one guest's burst permission to the host it runs on.
// hostmgr.Server satisfies it; a test records.
type BurstSender interface {
	Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error
}

// PeriodBurst sums the burst CPU time, in nanoseconds, of a user's
// projects over a period as usage_hours recorded it. Destroyed projects
// are included, as they are for egress: the cores were used. Like
// egress, the total can trail by up to an hour, because it is read from
// the rollup and not from the samples.
func PeriodBurst(ctx context.Context, q store.Querier, userID uuid.UUID, p Period) (int64, error) {
	var ns int64
	err := q.QueryRow(ctx, `select coalesce(sum(u.burst_cpu_ns), 0) from usage_hours u join projects p on p.id = u.project_id
		where p.user_id = $1 and u.hour >= $2 and u.hour < $3`, userID, p.Start, p.End).Scan(&ns)
	return ns, err
}

// BurstCoreHours is nanoseconds as core-hours to one decimal, which is
// how every figure a person reads is rounded.
func BurstCoreHours(ns int64) float64 {
	if ns <= 0 {
		return 0
	}
	return float64((ns*10+NsPerCoreHour/2)/NsPerCoreHour) / 10
}

// Allowance runs the burst allowance: once a tick it reads each live
// subscription's burst for the period, compares it with the plan's
// allowance, and tells every guest of that account whether it may still
// burst.
//
// It reconciles rather than reacts. The desired value is a function of
// the period's usage, so a tick that is missed, repeated or run on a
// second replica reaches the same answer, and hostd takes the same value
// twice without doing anything. `sent` is only an optimisation: it keeps
// a quiet fleet from re-sending the same command every tick. An api
// restart empties it and the next tick re-asserts every guest's state,
// which is correct and nearly free.
type Allowance struct {
	pool *db.Pool
	send BurstSender
	log  *slog.Logger
	Now  func() time.Time

	mu   sync.Mutex
	sent map[uuid.UUID]bool // guest id -> the `allowed` last sent
}

func NewAllowance(pool *db.Pool, send BurstSender, log *slog.Logger) *Allowance {
	return &Allowance{pool: pool, send: send, log: log, Now: time.Now, sent: map[uuid.UUID]bool{}}
}

// burstGuest is one of an account's guests that could be bursting.
type burstGuest struct {
	project uuid.UUID
	host    uuid.UUID
	guest   uuid.UUID
}

// Run reconciles every live subscription's guests once. It returns the
// guests it sent a command for.
func (a *Allowance) Run(ctx context.Context) ([]uuid.UUID, error) {
	now := a.Now().UTC()
	rows, err := a.pool.Query(ctx, "select "+subCols+" from subscriptions where status in ('trialing','active','past_due') order by created_at")
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[Sub])
	if err != nil {
		return nil, err
	}
	var sentIDs []uuid.UUID
	for i := range subs {
		sub := &subs[i]
		period := sub.Period(now)
		plan := sub.PlanFor(period.Start)
		used, err := PeriodBurst(ctx, a.pool, sub.UserID, period)
		if err != nil {
			return sentIDs, err
		}
		// With overage off -- the only state there is -- the limit is
		// the allowance. The cap and the price are stage 4.
		allowed := used < plan.BurstAllowanceNs()
		ids, err := a.apply(ctx, sub.UserID, allowed)
		if err != nil {
			a.log.Error("burst allowance not applied", "event", obs.EventBurstAllowance, "user_id", sub.UserID.String(), "err", err.Error())
			continue
		}
		if len(ids) > 0 {
			a.log.Info("burst permission changed", "event", obs.EventBurstAllowance, "user_id", sub.UserID.String(),
				"plan", plan.ID, "allowed", allowed, "guests", len(ids))
			sentIDs = append(sentIDs, ids...)
		}
	}
	return sentIDs, nil
}

// apply sends every guest of the account whose last sent value differs
// the new one. A guest on a host that does not name the `burst` feature
// is skipped: it is not bursting and an older hostd answers the command
// `invalid_argument`.
func (a *Allowance) apply(ctx context.Context, userID uuid.UUID, allowed bool) ([]uuid.UUID, error) {
	rows, err := a.pool.Query(ctx, `select p.id, p.host_id, p.guest_id from projects p join hosts h on h.id = p.host_id
		where p.user_id = $1 and p.destroyed_at is null and p.guest_id is not null and p.host_id is not null
		and p.state in ('running','starting') and $2 = any(h.features) order by p.id`, userID, HostFeatureBurst)
	if err != nil {
		return nil, err
	}
	var gs []burstGuest
	for rows.Next() {
		var g burstGuest
		if err := rows.Scan(&g.project, &g.host, &g.guest); err != nil {
			rows.Close()
			return nil, err
		}
		gs = append(gs, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, g := range gs {
		a.mu.Lock()
		last, seen := a.sent[g.guest]
		a.mu.Unlock()
		if seen && last == allowed {
			continue
		}
		cmd := &hostdv1.Command{CommandId: store.NewID().String(), Cmd: &hostdv1.Command_SetBurst{
			SetBurst: &hostdv1.SetBurst{GuestId: g.guest.String(), Allowed: allowed}}}
		if err := a.send.Send(ctx, g.host, cmd); err != nil {
			// A host on another replica's stream, or one that is down:
			// the next tick tries again, and hostd keeps whatever it was
			// last told in bbolt.
			continue
		}
		a.mu.Lock()
		a.sent[g.guest] = allowed
		a.mu.Unlock()
		out = append(out, g.guest)
	}
	return out, nil
}

// Forget drops a guest's remembered permission, so a machine that is
// destroyed and whose id is reused is told again.
func (a *Allowance) Forget(guestID uuid.UUID) {
	a.mu.Lock()
	delete(a.sent, guestID)
	a.mu.Unlock()
}

// HostFeatureBurst is the Hello feature a host names when it bursts
// (guest.FeatureBurst). It is spelled here as well so this package does
// not import hostd's.
const HostFeatureBurst = "burst"
