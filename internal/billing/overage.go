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

// The egress overage (PRICING.md "Egress", DECISIONS I-289). Hourly, for
// every live subscription whose next_billed_at is within three hours and
// whose period has no line yet: GB over the allowance times five cents,
// recorded in overage_charges (the primary key makes a retry safe) and
// sent as a Paddle one-time charge on the next invoice. Paddle locks the
// invoice about thirty minutes before charging, hence three hours. The
// same tick applies the hard stop: a user whose period egress passed four
// times the allowance has their machines stopped once per period.

// ChargeWindow is how close to next_billed_at the line is sent.
const ChargeWindow = 3 * time.Hour

// Overage is the hourly job.
type Overage struct {
	pool   *db.Pool
	paddle ChargeSender
	cfg    Config
	stop   Stopper
	m      *metrics.M
	log    *slog.Logger
	// Enforce is BILLING_ENFORCE (§8): false sends the charges (money owed
	// is owed) but stops no machine.
	Enforce bool
	Now     func() time.Time
}

// NewOverage builds the job. A nil paddle records lines but sends
// nothing, which is the disabled deploy.
func NewOverage(pool *db.Pool, paddle ChargeSender, cfg Config, stop Stopper, m *metrics.M, log *slog.Logger) *Overage {
	return &Overage{pool: pool, paddle: paddle, cfg: cfg, stop: stop, m: m, log: log.With("component", obs.ComponentAPI), Enforce: cfg.Enforce, Now: time.Now}
}

// Charge is one overage line the run sent or found already sent.
type Charge struct {
	SubscriptionID string
	UserID         uuid.UUID
	PeriodStart    time.Time
	EgressGB       int64
	Cents          int64
	TransactionID  string
	Sent           bool
}

// Run does both halves and returns what it did.
func (o *Overage) Run(ctx context.Context) (charges []Charge, stopped []uuid.UUID, err error) {
	charges, err = o.chargeDue(ctx)
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
		and next_billed_at is not null and next_billed_at <= $1 and period_start is not null
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
		c, err := o.ChargePeriod(ctx, &subs[i], EffectiveNextBillingPeriod)
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
// tick, `repose-admin billing overage-now` and account deletion call;
// effectiveFrom is next_billing_period for the first two and immediately
// for the last. A period with a line already recorded is not sent twice:
// the recorded row is returned as found.
func (o *Overage) ChargePeriod(ctx context.Context, sub *Sub, effectiveFrom string) (*Charge, error) {
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
		var existingTxn *string
		var inserted bool
		err := db.InTx(ctx, o.pool, func(tx db.Tx) error {
			tag, err := tx.Exec(ctx, `insert into overage_charges (subscription_id, period_start, egress_gb, cents) values ($1, $2, $3, $4)
				on conflict (subscription_id, period_start) do nothing`, sub.ID, period.Start, overGB, cents)
			if err != nil {
				return err
			}
			inserted = tag.RowsAffected() == 1
			if !inserted {
				return tx.QueryRow(ctx, "select paddle_transaction_id from overage_charges where subscription_id = $1 and period_start = $2", sub.ID, period.Start).Scan(&existingTxn)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("record the overage line: %w", err)
		}
		switch {
		case inserted && o.paddle != nil:
			desc := fmt.Sprintf("Egress overage: %d GB over the %s plan's %d GB (%s to %s) at $0.05/GB", overGB, plan.Name, plan.EgressGB, period.Start.Format("2 Jan"), period.End.Format("2 Jan 2006"))
			txn, err := o.paddle.CreateOneTimeCharge(ctx, sub.ID, cents, desc, effectiveFrom)
			if err != nil {
				// The row stays with no transaction id and no
				// overage_charged_for, so the next run finds the row,
				// sees no id, and does not send again: the operator
				// sends it by hand (RUNBOOK "Overage charge failed").
				return nil, fmt.Errorf("send the overage charge to Paddle: %w", err)
			}
			c.Sent = true
			c.TransactionID = txn
			if txn != "" {
				if _, err := o.pool.Exec(ctx, "update overage_charges set paddle_transaction_id = $3 where subscription_id = $1 and period_start = $2", sub.ID, period.Start, txn); err != nil {
					return nil, err
				}
			}
			if o.m != nil {
				o.m.BillingOverageChargesTotal.WithLabelValues("ok").Inc()
			}
			o.log.Info("overage charged", "event", obs.EventOverageCharged, "user_id", sub.UserID.String(), "result", "ok", "cents", cents, "gb", overGB)
		case inserted:
			o.log.Warn("overage line recorded but Paddle is not configured", "event", obs.EventOverageCharged, "user_id", sub.UserID.String(), "result", "disabled", "cents", cents)
		default:
			if existingTxn != nil {
				c.TransactionID = *existingTxn
			}
		}
	}
	if _, err := o.pool.Exec(ctx, "update subscriptions set overage_charged_for = $2 where id = $1", sub.ID, period.Start); err != nil {
		return nil, err
	}
	return c, nil
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
