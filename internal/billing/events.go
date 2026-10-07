package billing

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// The account events of DECISIONS I-291: an events row with user_id and
// no project, plus one email outbox row, written through
// events.InsertAccount on the caller's Querier (I-294 (1)). They are
// transactional (sent whatever notify_email says), because each answers
// something the user did or is about to be charged for. The summary is
// the JSON payload the notify templates read; the fields per kind are
// the table in docs/features/notifications.md, "Account emails".
const (
	KindTrialEnding           = "trial_ending"
	KindPaymentFailed         = "payment_failed"
	KindSubscriptionCancelled = "subscription_cancelled"
	KindSubscriptionEnded     = "subscription_ended"
	KindPlanChanged           = "plan_changed"
	KindEgressStopped         = "egress_stopped"
	// KindDiskOverPlan is the email when the projects come to hold more
	// than the plan's disk (DECISIONS I-585), once per period.
	KindDiskOverPlan = "disk_over_plan"
	// KindBillingStopped is the per-project event the 3-day stop records
	// (13-notifications.md §5.6); it predates I-291 and keeps its name.
	KindBillingStopped = "billing_stopped"
)

// RetentionDays is how long a stopped machine's snapshot is kept after a
// plan ends (PRICING.md "Cancelling"); subscription_ended names the date.
const RetentionDays = 30

// TrialEndingPayload is trial_ending's summary: the plan, what is charged
// and when.
type TrialEndingPayload struct {
	Plan        string    `json:"plan"`
	AmountCents int64     `json:"amount_cents"`
	ChargeAt    time.Time `json:"charge_at"`
}

// PaymentFailedPayload is payment_failed's summary on day 0 and day 2.
// PortalURL is empty when the producer has no portal session for the
// user (the template then links the plan page).
type PaymentFailedPayload struct {
	Plan        string `json:"plan"`
	AmountCents int64  `json:"amount_cents"`
	PortalURL   string `json:"portal_url,omitempty"`
}

// SubscriptionCancelledPayload is subscription_cancelled's summary.
type SubscriptionCancelledPayload struct {
	Plan   string    `json:"plan"`
	EndsAt time.Time `json:"ends_at"`
}

// SubscriptionEndedPayload is subscription_ended's summary.
type SubscriptionEndedPayload struct {
	Plan           string    `json:"plan"`
	EndedAt        time.Time `json:"ended_at"`
	RetentionUntil time.Time `json:"retention_until"`
}

// PlanChangedPayload is plan_changed's summary.
type PlanChangedPayload struct {
	FromPlan    string    `json:"from_plan"`
	ToPlan      string    `json:"to_plan"`
	EffectiveAt time.Time `json:"effective_at"`
}

// EgressStoppedPayload is egress_stopped's summary: the period's egress
// against the plan's allowance, and when the machines may run again.
type EgressStoppedPayload struct {
	Plan     string    `json:"plan"`
	EgressGB float64   `json:"egress_gb"`
	LimitGB  int       `json:"limit_gb"`
	Until    time.Time `json:"until"`
}

// DiskOverPlanPayload is disk_over_plan's summary: what the projects
// hold against the plan's disk.
type DiskOverPlanPayload struct {
	Plan    string  `json:"plan"`
	HeldGB  float64 `json:"held_gb"`
	LimitGB int     `json:"limit_gb"`
}

// trialEnding is the payload for a trialing subscription: charged at
// trial_end (or next_billed_at when Paddle set only that), the amount
// Paddle charges then, which is the introductory price when the
// subscription carries the introductory discount (DECISIONS I-497).
func trialEnding(sub *Sub) TrialEndingPayload {
	plan := sub.PlanOrSolo()
	p := TrialEndingPayload{Plan: plan.ID}
	switch {
	case sub.TrialEnd != nil:
		p.ChargeAt = sub.TrialEnd.UTC()
	case sub.NextBilledAt != nil:
		p.ChargeAt = sub.NextBilledAt.UTC()
	}
	p.AmountCents = sub.ChargeCents(p.ChargeAt)
	return p
}

// paymentFailed is the payload for a subscription whose charge at at
// failed; sub may be nil, which names Solo at its full price.
func paymentFailed(sub *Sub, at time.Time) PaymentFailedPayload {
	return PaymentFailedPayload{Plan: sub.PlanOrSolo().ID, AmountCents: sub.ChargeCents(at)}
}

// subscriptionEnded is the payload when a subscription is canceled at at.
func subscriptionEnded(plan Plan, at time.Time) SubscriptionEndedPayload {
	at = at.UTC()
	return SubscriptionEndedPayload{Plan: plan.ID, EndedAt: at, RetentionUntil: at.Add(RetentionDays * 24 * time.Hour)}
}

// egressStopped is the payload when a period's egress reached the hard
// stop.
func egressStopped(plan Plan, egressBytes int64, period Period) EgressStoppedPayload {
	return EgressStoppedPayload{Plan: plan.ID, EgressGB: float64(egressBytes) / float64(1<<30), LimitGB: plan.EgressGB, Until: period.End.UTC()}
}

// accountEventCount counts the user's events of a kind at or after since.
func accountEventCount(ctx context.Context, q store.Querier, userID uuid.UUID, kind string, since time.Time) (int, error) {
	var n int
	err := q.QueryRow(ctx, "select count(*) from events where user_id = $1 and kind = $2 and ts >= $3", userID, kind, since.UTC()).Scan(&n)
	return n, err
}
