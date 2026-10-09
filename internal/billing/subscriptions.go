package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// Sub is a subscriptions row: the record of a Polar subscription
// (db-schema.md). users.billing_status is a projection of Status.
type Sub struct {
	ID                string     `db:"id"`
	UserID            uuid.UUID  `db:"user_id"`
	CustomerID        string     `db:"customer_id"`
	Plan              string     `db:"plan"`
	Status            string     `db:"status"`
	Seats             int        `db:"seats"`
	PeriodStart       *time.Time `db:"period_start"`
	PeriodEnd         *time.Time `db:"period_end"`
	NextBilledAt      *time.Time `db:"next_billed_at"`
	TrialEnd          *time.Time `db:"trial_end"`
	CancelAt          *time.Time `db:"cancel_at"`
	ScheduledPlan     *string    `db:"scheduled_plan"`
	OverageChargedFor *time.Time `db:"overage_charged_for"`
	// Intro is whether the subscription carries the introductory discount
	// (DECISIONS I-497), and IntroUntil when it ends: the first charged
	// period plus IntroMonths (DECISIONS I-604); nil when unknown.
	Intro      bool       `db:"intro"`
	IntroUntil *time.Time `db:"intro_until"`
	// SourceModifiedAt is Polar's modified_at of the newest payload
	// applied; an older one delivered late is skipped.
	SourceModifiedAt *time.Time `db:"source_modified_at"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

const subCols = `id, user_id, customer_id, plan, status, seats, period_start, period_end, next_billed_at, trial_end, cancel_at, scheduled_plan, overage_charged_for, intro, intro_until, source_modified_at, created_at, updated_at`

// Subscription statuses, Polar's words (unpaid is stored as canceled).
const (
	StatusTrialing = "trialing"
	StatusActive   = "active"
	StatusPastDue  = "past_due"
	StatusPaused   = "paused"
	StatusCanceled = "canceled"
)

// LiveStatuses are the statuses that hold a seat and buy compute
// (DECISIONS I-290).
var LiveStatuses = []string{StatusTrialing, StatusActive, StatusPastDue}

// IsLive reports whether a status buys compute.
func IsLive(status string) bool {
	return status == StatusTrialing || status == StatusActive || status == StatusPastDue
}

// Live is the subscription in the live set.
func (s *Sub) Live() bool { return s != nil && IsLive(s.Status) }

// PlanOrSolo is the plan, defaulting so arithmetic never divides by an
// unknown plan.
func (s *Sub) PlanOrSolo() Plan {
	if s != nil {
		if p, ok := PlanByID(s.Plan); ok {
			return p
		}
	}
	return Solo
}

// Period is the subscription's current billing period, or the calendar
// month around at when Polar has not set one (a subscription just
// created has period_start; a test row may not).
func (s *Sub) Period(at time.Time) Period {
	if s != nil && s.PeriodStart != nil && s.PeriodEnd != nil && s.PeriodEnd.After(*s.PeriodStart) {
		return Period{Start: s.PeriodStart.UTC(), End: s.PeriodEnd.UTC()}
	}
	if s != nil && s.PeriodStart != nil {
		return PeriodFor(*s.PeriodStart, at)
	}
	return PeriodFor(time.Time{}, at)
}

// LiveSubscription returns the user's live subscription, or nil.
func LiveSubscription(ctx context.Context, q store.Querier, userID uuid.UUID) (*Sub, error) {
	rows, err := q.Query(ctx, "select "+subCols+" from subscriptions where user_id = $1 and status in ('trialing','active','past_due') order by created_at desc limit 1", userID)
	if err != nil {
		return nil, err
	}
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[Sub])
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, nil
	}
	return &subs[0], nil
}

// EverSubscribed reports whether the user has had any subscription, in
// any status. Only a first subscription gets the introductory price
// (DECISIONS I-497).
func EverSubscribed(ctx context.Context, q store.Querier, userID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, "select exists (select 1 from subscriptions where user_id = $1)", userID).Scan(&ok)
	return ok, err
}

// IntroAt reports whether the introductory offer covers at: the
// subscription carries the introductory discount, its plan has an
// introductory price, and at is before the discount's end (or the end is
// not known).
func (s *Sub) IntroAt(at time.Time) bool {
	return s != nil && s.Intro && s.PlanOrSolo().HasIntro() && (s.IntroUntil == nil || at.Before(*s.IntroUntil))
}

// ChargeCents is what Polar charges for the subscription's plan at at:
// the introductory price while the offer runs, else the plan's price.
func (s *Sub) ChargeCents(at time.Time) int64 {
	plan := s.PlanOrSolo()
	if s.IntroAt(at) {
		return plan.IntroCents
	}
	return plan.PriceCents
}

// PlanFor is the plan as it applies to the period starting at start: the
// introductory egress allowance while the offer covers that period
// (DECISIONS I-497), else the plan as it is. Egress arithmetic (the
// overage line, the hard stop, the gate, GET /billing) reads this.
func (s *Sub) PlanFor(start time.Time) Plan {
	plan := s.PlanOrSolo()
	if s.IntroAt(start) && plan.IntroEgressGB > 0 {
		plan.EgressGB = plan.IntroEgressGB
	}
	return plan
}

// GetSubscription reads one row by Polar id; db.ErrNotFound when absent.
func GetSubscription(ctx context.Context, q store.Querier, id string) (*Sub, error) {
	rows, err := q.Query(ctx, "select "+subCols+" from subscriptions where id = $1", id)
	if err != nil {
		return nil, err
	}
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[Sub])
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, db.ErrNotFound
	}
	return &subs[0], nil
}

// LatestSubscription is the user's newest row of any status, or nil.
func LatestSubscription(ctx context.Context, q store.Querier, userID uuid.UUID) (*Sub, error) {
	rows, err := q.Query(ctx, "select "+subCols+" from subscriptions where user_id = $1 order by created_at desc limit 1", userID)
	if err != nil {
		return nil, err
	}
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[Sub])
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, nil
	}
	return &subs[0], nil
}

// upsertSubscription writes a row from Polar's view of it and returns the
// row as it was before (nil when new), so the caller can tell what changed.
func upsertSubscription(ctx context.Context, q store.Querier, s Sub) (*Sub, error) {
	prev, err := GetSubscription(ctx, q, s.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if errors.Is(err, db.ErrNotFound) {
		prev = nil
	}
	_, err = q.Exec(ctx, `insert into subscriptions (id, user_id, customer_id, plan, status, seats, period_start, period_end, next_billed_at, trial_end, cancel_at, scheduled_plan, intro, intro_until, source_modified_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		on conflict (id) do update set customer_id = excluded.customer_id, plan = excluded.plan, status = excluded.status,
		seats = excluded.seats, period_start = excluded.period_start, period_end = excluded.period_end, next_billed_at = excluded.next_billed_at,
		trial_end = excluded.trial_end, cancel_at = excluded.cancel_at, scheduled_plan = excluded.scheduled_plan,
		intro = excluded.intro, intro_until = excluded.intro_until,
		source_modified_at = coalesce(excluded.source_modified_at, subscriptions.source_modified_at)`,
		s.ID, s.UserID, s.CustomerID, s.Plan, s.Status, s.Seats, s.PeriodStart, s.PeriodEnd, s.NextBilledAt, s.TrialEnd, s.CancelAt, s.ScheduledPlan, s.Intro, s.IntroUntil, s.SourceModifiedAt)
	if err != nil {
		return prev, err
	}
	return prev, nil
}

// projectStatus writes users.billing_status from a subscription status:
// trialing -> trial, active -> active, past_due -> past_due (with
// past_due_since kept from the first failure), canceled and paused ->
// none. A suspended account stays suspended until a payment clears it;
// an exempt one is never touched.
func projectStatus(ctx context.Context, q store.Querier, userID uuid.UUID, status string, now time.Time) error {
	var set string
	switch status {
	case StatusTrialing:
		set = "billing_status = 'trial', past_due_since = null"
	case StatusActive:
		set = "billing_status = 'active', past_due_since = null"
	case StatusPastDue:
		set = "billing_status = 'past_due', past_due_since = coalesce(past_due_since, $2::timestamptz)"
	case StatusCanceled, StatusPaused:
		set = "billing_status = 'none', past_due_since = null"
	default:
		return nil
	}
	// $2 is referenced by the past_due branch alone; the cast keeps the
	// parameter in every statement so the argument count matches.
	_, err := q.Exec(ctx, "update users set "+set+", has_card = true where id = $1 and billing_status not in ('exempt', 'suspended') and $2::timestamptz is not null", userID, now.UTC())
	if err != nil || (status != StatusCanceled && status != StatusPaused) {
		return err
	}
	// A subscription that ended (Polar revokes after its last retry) takes
	// a billing suspension with it: the account has no plan and can check
	// out again; its machines stay stopped. An operator's stays.
	_, err = q.Exec(ctx, `update users set billing_status = 'none', past_due_since = null, suspended_at = null, suspended_reason = null
		where id = $1 and billing_status = 'suspended' and suspended_reason = 'billing'`, userID)
	return err
}
