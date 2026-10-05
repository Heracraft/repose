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
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// Failed payments (PRICING.md "Failed payments", 09-billing.md §5.11): a
// job every hour. Day 0 is the webhook's (payment_failed email, starts
// refused). Day 2: a second payment_failed email, once. Day 3: every
// running machine of the account is snapshotted and stopped (reason
// billing), the billing_stopped notification goes out and the account is
// suspended. Nothing is destroyed; R4-11's 30-day retention starts at
// suspension. The same job sends trial_ending once when a trial has 48
// hours left. Every "once" is derived from the events table: no second
// row of the kind since the moment it counts from.

const (
	pastDueGraceDays = 3
	// SecondNoticeAfter is when day 2's payment_failed goes out.
	SecondNoticeAfter = 2 * 24 * time.Hour
	// TrialEndingWindow is how long before trial_end the email goes out.
	TrialEndingWindow = 48 * time.Hour
)

// Dunning stops and suspends past-due accounts and sends the reminders.
type Dunning struct {
	pool   *db.Pool
	stop   Stopper
	events EventSink
	cfg    Config
	m      *metrics.M
	log    *slog.Logger
	// Grace is the number of days past due before machines stop; three.
	Grace time.Duration
	// Enforce is BILLING_ENFORCE (§8): false keeps the job running and
	// logging but stops nothing. Emails still go out.
	Enforce bool
	Now     func() time.Time
}

// NewDunning builds the job.
func NewDunning(pool *db.Pool, stop Stopper, events EventSink, cfg Config, m *metrics.M, log *slog.Logger) *Dunning {
	return &Dunning{pool: pool, stop: stop, events: events, cfg: cfg, m: m, log: log.With("component", obs.ComponentAPI),
		Grace: pastDueGraceDays * 24 * time.Hour, Enforce: cfg.Enforce, Now: time.Now}
}

// Stopped is one account the run acted on.
type Stopped struct {
	UserID   uuid.UUID
	Handle   string
	Projects []uuid.UUID
}

// DunningResult is what one run did.
type DunningResult struct {
	Suspended     []Stopped
	SecondNotices []uuid.UUID
	TrialEnding   []uuid.UUID
}

// Run does the three jobs. It is idempotent: an account already suspended
// is skipped, a project with an open op is left for the next run, and an
// email already sent for this past_due_since or this trial is not sent
// again.
func (d *Dunning) Run(ctx context.Context) (DunningResult, error) {
	var res DunningResult
	now := d.Now().UTC()
	// Day 2.
	rows, err := d.pool.Query(ctx, `select id, past_due_since from users where billing_status = 'past_due' and past_due_since is not null
		and past_due_since <= $1 and past_due_since > $2 order by past_due_since`, now.Add(-SecondNoticeAfter), now.Add(-d.Grace))
	if err != nil {
		return res, fmt.Errorf("list accounts on day 2: %w", err)
	}
	type acct struct {
		id    uuid.UUID
		since time.Time
	}
	var second []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.id, &a.since); err != nil {
			rows.Close()
			return res, err
		}
		second = append(second, a)
	}
	rows.Close()
	for _, a := range second {
		// "No second payment_failed event for this past_due_since yet":
		// day 0's is at or after past_due_since; a second one is day 2's.
		n, err := accountEventCount(ctx, d.pool, a.id, KindPaymentFailed, a.since.Add(-time.Minute))
		if err != nil {
			return res, err
		}
		if n >= 2 {
			continue
		}
		sub, err := LiveSubscription(ctx, d.pool, a.id)
		if err != nil {
			return res, err
		}
		if _, err := events.InsertAccount(ctx, d.pool, a.id, now, KindPaymentFailed, paymentFailed(sub, a.since)); err != nil {
			return res, err
		}
		res.SecondNotices = append(res.SecondNotices, a.id)
	}
	// Day 3.
	rows, err = d.pool.Query(ctx, `select id, handle from users
		where billing_status = 'past_due' and past_due_since is not null and past_due_since < $1
		order by past_due_since`, now.Add(-d.Grace))
	if err != nil {
		return res, fmt.Errorf("list past-due accounts: %w", err)
	}
	type named struct {
		id     uuid.UUID
		handle string
	}
	var due []named
	for rows.Next() {
		var a named
		if err := rows.Scan(&a.id, &a.handle); err != nil {
			rows.Close()
			return res, err
		}
		due = append(due, a)
	}
	rows.Close()
	for _, a := range due {
		s, err := d.suspend(ctx, a.id, a.handle)
		if err != nil {
			return res, err
		}
		res.Suspended = append(res.Suspended, s)
	}
	// Trials ending within 48 hours.
	srows, err := d.pool.Query(ctx, "select "+subCols+` from subscriptions where status = 'trialing' and trial_end is not null
		and trial_end > $1 and trial_end <= $2 order by trial_end`, now, now.Add(TrialEndingWindow))
	if err != nil {
		return res, fmt.Errorf("list ending trials: %w", err)
	}
	subs, err := pgx.CollectRows(srows, pgx.RowToStructByName[Sub])
	if err != nil {
		return res, err
	}
	for i := range subs {
		sub := &subs[i]
		n, err := accountEventCount(ctx, d.pool, sub.UserID, KindTrialEnding, sub.CreatedAt.Add(-time.Minute))
		if err != nil {
			return res, err
		}
		if n > 0 {
			continue
		}
		if _, err := events.InsertAccount(ctx, d.pool, sub.UserID, now, KindTrialEnding, trialEnding(sub)); err != nil {
			return res, err
		}
		res.TrialEnding = append(res.TrialEnding, sub.UserID)
	}
	return res, nil
}

// suspend stops one account's machines and marks it suspended.
func (d *Dunning) suspend(ctx context.Context, userID uuid.UUID, handle string) (Stopped, error) {
	res := Stopped{UserID: userID, Handle: handle}
	if !d.Enforce {
		d.log.Warn("BILLING_ENFORCE=false: past-due account not stopped", "event", obs.EventBillingStopped,
			"user_id", userID.String(), "enforced", false)
		return res, nil
	}
	stopped, err := stopUserMachines(ctx, d.pool, d.stop, d.m, d.log, userID, StopReasonPastDue)
	if err != nil {
		return res, err
	}
	res.Projects = stopped
	if d.events != nil {
		for _, pid := range stopped {
			if err := d.events.Platform(ctx, pid, KindBillingStopped,
				"Your machine was stopped because a payment failed. Update your card at "+d.cfg.BillingURL()+" to start it again; nothing is deleted for 30 days."); err != nil {
				return res, err
			}
		}
	}
	if _, err := d.pool.Exec(ctx, `update users set billing_status = 'suspended', suspended_at = coalesce(suspended_at, now()),
		suspended_reason = 'billing' where id = $1 and billing_status = 'past_due'`, userID); err != nil {
		return res, err
	}
	if _, err := store.Audit(ctx, d.pool, "billing", "billing_suspend", handle, map[string]any{"projects": len(res.Projects)}); err != nil {
		return res, err
	}
	d.log.Warn("account suspended for non-payment", "event", obs.EventBillingStopped, "user_id", userID.String(), "projects", len(res.Projects), "reason", StopReasonPastDue)
	return res, nil
}
