package billing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// The egress overage (PRICING.md "Egress", DECISIONS I-289, I-604).
// Hourly, for every live subscription whose period ends within three
// hours and whose period has no line yet: GB over the allowance, recorded
// in overage_charges (the primary key makes a retry safe) and sent to
// Polar as one egress_overage event, which the plan product's metered
// price bills at five cents a GB on the renewal order. The event's
// external id is the subscription and period, so a resend after a failure
// is harmless and the next tick makes it. The same tick applies the hard
// stop: a user whose period egress passed four times the allowance has
// their machines stopped once per period.

// ChargeWindow is how close to period_end the line is sent: Polar bills
// metered usage on the order it makes at the period's end, and an event
// it receives later lands on the next period's order.
const ChargeWindow = 3 * time.Hour

// Overage is the hourly job.
type Overage struct {
	pool   *db.Pool
	sender OverageSender
	cfg    Config
	stop   Stopper
	m      *metrics.M
	log    *slog.Logger
	// Enforce is BILLING_ENFORCE (§8): false sends the overage (money owed
	// is owed) but stops no machine.
	Enforce bool
	Now     func() time.Time
}

// NewOverage builds the job. A nil sender records lines but sends
// nothing, which is the disabled deploy.
func NewOverage(pool *db.Pool, sender OverageSender, cfg Config, stop Stopper, m *metrics.M, log *slog.Logger) *Overage {
	return &Overage{pool: pool, sender: sender, cfg: cfg, stop: stop, m: m, log: log.With("component", obs.ComponentAPI), Enforce: cfg.Enforce, Now: time.Now}
}

// Charge is one overage line the run sent or found already sent.
type Charge struct {
	SubscriptionID string
	UserID         uuid.UUID
	PeriodStart    time.Time
	EgressGB       int64
	Cents          int64
	// Ref is the external id Polar accepted the event under; "" while
	// it is not sent.
	Ref  string
	Sent bool
}

// Run does both halves and returns what it did.
func (o *Overage) Run(ctx context.Context) (charges []Charge, stopped []uuid.UUID, err error) {
	charges, err = o.chargeDue(ctx)
	if err != nil {
		return charges, nil, err
	}
	late, err := o.resendUnsent(ctx)
	charges = append(charges, late...)
	if err != nil {
		return charges, nil, err
	}
	stopped, err = o.hardStops(ctx)
	if err != nil {
		return charges, stopped, err
	}
	_, err = o.DiskOverPlan(ctx)
	return charges, stopped, err
}

// DiskOverPlan writes a disk_over_plan email for every user whose
// projects hold more than the plan's disk, once per period (DECISIONS
// I-585). Nothing is stopped: the gate refuses what would add bytes
// (create, restore, fork, growing a disk) until they hold less. It
// returns the users it wrote to.
func (o *Overage) DiskOverPlan(ctx context.Context) ([]uuid.UUID, error) {
	now := o.Now().UTC()
	rows, err := o.pool.Query(ctx, "select "+subCols+" from subscriptions where status in ('trialing','active','past_due') order by created_at")
	if err != nil {
		return nil, err
	}
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[Sub])
	if err != nil {
		return nil, err
	}
	var told []uuid.UUID
	for i := range subs {
		sub := &subs[i]
		period := sub.Period(now)
		plan := sub.PlanFor(period.Start)
		held, _, err := HeldDisk(ctx, o.pool, sub.UserID)
		if err != nil {
			return told, err
		}
		if held <= int64(plan.DiskGB)<<30 {
			continue
		}
		n, err := accountEventCount(ctx, o.pool, sub.UserID, KindDiskOverPlan, period.Start)
		if err != nil {
			return told, err
		}
		if n > 0 {
			continue
		}
		if _, err := events.InsertAccount(ctx, o.pool, sub.UserID, now, KindDiskOverPlan, DiskOverPlanPayload{Plan: plan.ID, HeldGB: gbTenths(held), LimitGB: plan.DiskGB}); err != nil {
			return told, err
		}
		o.log.Info("projects hold more than the plan's disk", "event", obs.EventBillingDiskOverPlan, "user_id", sub.UserID.String(), "plan", plan.ID)
		told = append(told, sub.UserID)
	}
	return told, nil
}

// chargeDue sends the line for every subscription about to bill.
func (o *Overage) chargeDue(ctx context.Context) ([]Charge, error) {
	now := o.Now().UTC()
	rows, err := o.pool.Query(ctx, "select "+subCols+` from subscriptions where status in ('trialing','active','past_due')
		and period_end is not null and period_end <= $1 and period_start is not null
		and (overage_charged_for is null or overage_charged_for <> period_start) order by next_billed_at`, now.Add(ChargeWindow))
	if err != nil {
		return nil, fmt.Errorf("list subscriptions about to bill: %w", err)
	}
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[Sub])
	if err != nil {
		return nil, err
	}
	var out []Charge
	for i := range subs {
		c, err := o.ChargePeriod(ctx, &subs[i])
		if err != nil {
			if o.m != nil {
				o.m.BillingOverageChargesTotal.WithLabelValues("error").Inc()
			}
			o.log.Error("overage charge failed", "event", obs.EventOverageCharged, "user_id", subs[i].UserID.String(), "result", "error", "err", err.Error())
			continue
		}
		if c != nil {
			out = append(out, *c)
		}
	}
	return out, nil
}

// ChargePeriod computes and, when over, sends the current period's line
// for one subscription, then marks the period charged. It is what the
// tick, `repose-admin billing overage-now` and account deletion call. A
// period whose line Polar already accepted is not sent twice: the
// recorded row is returned as found. A line recorded but not accepted
// (Polar failed) is brought up to the period's egress so far and sent.
func (o *Overage) ChargePeriod(ctx context.Context, sub *Sub) (*Charge, error) {
	if sub.PeriodStart == nil {
		return nil, nil
	}
	period := sub.Period(o.Now())
	plan := sub.PlanFor(period.Start)
	egress, err := PeriodEgress(ctx, o.pool, sub.UserID, period)
	if err != nil {
		return nil, err
	}
	overGB, cents := OverageCents(plan, egress)
	c := &Charge{SubscriptionID: sub.ID, UserID: sub.UserID, PeriodStart: period.Start, EgressGB: overGB, Cents: cents}
	if cents > 0 {
		var sentRef *string
		err := db.InTx(ctx, o.pool, func(tx db.Tx) error {
			if _, err := tx.Exec(ctx, `insert into overage_charges (subscription_id, period_start, egress_gb, cents) values ($1, $2, $3, $4)
				on conflict (subscription_id, period_start) do update set egress_gb = excluded.egress_gb, cents = excluded.cents
				where overage_charges.sent_ref is null`, sub.ID, period.Start, overGB, cents); err != nil {
				return err
			}
			var gb, cc int64
			if err := tx.QueryRow(ctx, "select egress_gb::bigint, cents, sent_ref from overage_charges where subscription_id = $1 and period_start = $2", sub.ID, period.Start).Scan(&gb, &cc, &sentRef); err != nil {
				return err
			}
			c.EgressGB, c.Cents = gb, cc
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("record the overage line: %w", err)
		}
		switch {
		case sentRef != nil:
			c.Ref = *sentRef
		case o.sender != nil:
			ref := OverageExternalID(sub.ID, period.Start)
			if err := o.sender.SendOverage(ctx, sub.UserID, ref, c.EgressGB, period.Start); err != nil {
				// The row stays with no sent_ref and the period unmarked,
				// so the next tick sends it again under the same external
				// id (RUNBOOK "Overage charge failed").
				return nil, fmt.Errorf("send the overage to Polar: %w", err)
			}
			if _, err := o.pool.Exec(ctx, "update overage_charges set sent_ref = $3 where subscription_id = $1 and period_start = $2", sub.ID, period.Start, ref); err != nil {
				return nil, err
			}
			c.Sent, c.Ref = true, ref
			if o.m != nil {
				o.m.BillingOverageChargesTotal.WithLabelValues("ok").Inc()
			}
			o.log.Info("overage charged", "event", obs.EventOverageCharged, "user_id", sub.UserID.String(), "result", "ok", "cents", c.Cents, "gb", c.EgressGB)
		default:
			o.log.Warn("overage line recorded but Polar is not configured", "event", obs.EventOverageCharged, "user_id", sub.UserID.String(), "result", "disabled", "cents", c.Cents)
		}
	}
	if _, err := o.pool.Exec(ctx, "update subscriptions set overage_charged_for = $2 where id = $1", sub.ID, period.Start); err != nil {
		return nil, err
	}
	return c, nil
}

// resendUnsent sends every recorded line of an earlier period that Polar
// never accepted, as recorded and under its own external id: a period
// whose sends all failed until its renewal lands on the next renewal
// order rather than being lost (Polar bills an event by when it arrives).
func (o *Overage) resendUnsent(ctx context.Context) ([]Charge, error) {
	if o.sender == nil {
		return nil, nil
	}
	rows, err := o.pool.Query(ctx, `select c.subscription_id, s.user_id, c.period_start, c.egress_gb::bigint, c.cents
		from overage_charges c join subscriptions s on s.id = c.subscription_id
		where c.sent_ref is null and s.provider = 'polar' and (s.period_start is null or c.period_start < s.period_start)
		order by c.period_start`)
	if err != nil {
		return nil, fmt.Errorf("list unsent overage lines: %w", err)
	}
	var pending []Charge
	for rows.Next() {
		var c Charge
		if err := rows.Scan(&c.SubscriptionID, &c.UserID, &c.PeriodStart, &c.EgressGB, &c.Cents); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Charge
	for _, c := range pending {
		ref := OverageExternalID(c.SubscriptionID, c.PeriodStart)
		if err := o.sender.SendOverage(ctx, c.UserID, ref, c.EgressGB, c.PeriodStart); err != nil {
			if o.m != nil {
				o.m.BillingOverageChargesTotal.WithLabelValues("error").Inc()
			}
			o.log.Error("overage resend failed", "event", obs.EventOverageCharged, "user_id", c.UserID.String(), "result", "error", "err", err.Error())
			continue
		}
		if _, err := o.pool.Exec(ctx, "update overage_charges set sent_ref = $3 where subscription_id = $1 and period_start = $2", c.SubscriptionID, c.PeriodStart, ref); err != nil {
			return out, err
		}
		if o.m != nil {
			o.m.BillingOverageChargesTotal.WithLabelValues("ok").Inc()
		}
		o.log.Info("overage charged late", "event", obs.EventOverageCharged, "user_id", c.UserID.String(), "result", "ok", "cents", c.Cents, "gb", c.EgressGB)
		c.Sent, c.Ref = true, ref
		out = append(out, c)
	}
	return out, nil
}

// hardStops stops the machines of every user whose period egress passed
// the plan's ceiling, once per period, and records the egress_stopped
// event. The gate refuses their starts with egress_limit until period_end.
func (o *Overage) hardStops(ctx context.Context) ([]uuid.UUID, error) {
	now := o.Now().UTC()
	rows, err := o.pool.Query(ctx, "select "+subCols+" from subscriptions where status in ('trialing','active','past_due') order by created_at")
	if err != nil {
		return nil, err
	}
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[Sub])
	if err != nil {
		return nil, err
	}
	var stopped []uuid.UUID
	for i := range subs {
		sub := &subs[i]
		period := sub.Period(now)
		plan := sub.PlanFor(period.Start)
		egress, err := PeriodEgress(ctx, o.pool, sub.UserID, period)
		if err != nil {
			return stopped, err
		}
		if egress < plan.EgressHardStopBytes() {
			continue
		}
		n, err := accountEventCount(ctx, o.pool, sub.UserID, KindEgressStopped, period.Start)
		if err != nil {
			return stopped, err
		}
		if n > 0 {
			continue
		}
		if !o.Enforce {
			o.log.Warn("BILLING_ENFORCE=false: egress ceiling passed, machines not stopped", "event", obs.EventBillingStopped, "user_id", sub.UserID.String(), "reason", StopReasonEgress, "enforced", false)
			continue
		}
		if o.stop != nil {
			if _, err := stopUserMachines(ctx, o.pool, o.stop, o.m, o.log, sub.UserID, StopReasonEgress); err != nil {
				return stopped, err
			}
		}
		if _, err := events.InsertAccount(ctx, o.pool, sub.UserID, now, KindEgressStopped, egressStopped(plan, egress, period)); err != nil {
			return stopped, err
		}
		stopped = append(stopped, sub.UserID)
	}
	return stopped, nil
}
