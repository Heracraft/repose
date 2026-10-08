package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// Service is what the /billing routes call (api.md "Usage and billing"):
// the overview, checkout, plan changes, cancellation, the portal, the
// invoices and account deletion. It holds the Paddle client, the seat
// count and the overage job. A nil *Service is billing disabled.
type Service struct {
	pool    *db.Pool
	paddle  *Paddle
	cfg     Config
	seats   waitlist.Seats
	overage *Overage
	log     *slog.Logger
	Now     func() time.Time
}

// NewService wires the routes' dependencies together.
func NewService(pool *db.Pool, paddle *Paddle, cfg Config, seats waitlist.Seats, overage *Overage, log *slog.Logger) *Service {
	return &Service{pool: pool, paddle: paddle, cfg: cfg, seats: seats, overage: overage, log: log.With("component", obs.ComponentAPI), Now: time.Now}
}

// Paddle is the client, for the admin commands.
func (s *Service) Paddle() *Paddle { return s.paddle }

// Seats is the seat count in use.
func (s *Service) Seats() waitlist.Seats { return s.seats }

// The refusals the routes map to conflict, invalid and waitlisted.
var (
	ErrUnknownPlan      = errors.New("plan must be solo, plus or pro")
	ErrSubscribed       = errors.New("a subscription exists; change it with /billing/plan")
	ErrNoSubscription   = errors.New("the account has no subscription")
	ErrAlreadyCancelled = errors.New("the subscription is already cancelled")
	ErrNotCancelled     = errors.New("no cancellation is scheduled")
	ErrSamePlan         = errors.New("the account is on that plan already")
	ErrNoSeat           = errors.New("no free seat for the upgrade")
)

// WaitlistedError is the checkout refusal when the plan's seats are not
// free (503 waitlisted); the user is on the waitlist from then on.
type WaitlistedError struct {
	Place waitlist.Place
}

func (e *WaitlistedError) Error() string { return "waitlisted" }

// Message is the whole sentence the CLI and the dashboard show: the one
// builder every producer of it uses (waitlist.Message; I-294 (2)).
func (e *WaitlistedError) Message() string {
	return waitlist.Message(e.Place.Position, e.Place.Email)
}

// OverPlanError refuses a downgrade the account would not fit in.
// DiskHeldBytes is what the projects' volumes hold (DECISIONS I-585).
type OverPlanError struct {
	RunningGB     int
	DiskHeldBytes int64
	Plan          Plan
}

func (e *OverPlanError) Error() string {
	return fmt.Sprintf("%d GB running and %s GB of disk held do not fit the %s plan (%d GB, %d GB)", e.RunningGB, fmtGB(e.DiskHeldBytes), e.Plan.Name, e.Plan.MemoryGB, e.Plan.DiskGB)
}

// DiskHeldGB is the held figure to a tenth of a GB.
func (e *OverPlanError) DiskHeldGB() float64 { return gbTenths(e.DiskHeldBytes) }

// HeldGBText is the held figure as the message prints it.
func (e *OverPlanError) HeldGBText() string { return fmtGB(e.DiskHeldBytes) }

// Overview is GET /billing.
func (s *Service) Overview(ctx context.Context, u *store.User) (map[string]any, error) {
	now := s.Now().UTC()
	sub, err := LiveSubscription(ctx, s.pool, u.ID)
	if err != nil {
		return nil, err
	}
	var period Period
	if sub != nil {
		period = sub.Period(now)
	} else {
		period = Period{Start: now.AddDate(0, 0, -30), End: now}
	}
	plan := sub.PlanFor(period.Start)
	usage, err := LoadUsage(ctx, s.pool, u.ID, plan, period)
	if err != nil {
		return nil, err
	}
	count, err := s.seats.Count(ctx)
	if err != nil {
		return nil, err
	}
	intro, err := s.introEligible(ctx, u)
	if err != nil {
		return nil, err
	}
	plans := make([]map[string]any, 0, len(Plans))
	for _, p := range Plans {
		available := true
		if sub == nil {
			ok, _, err := s.seats.Reserve(ctx, u.ID.String(), p.Seats)
			if err != nil {
				return nil, err
			}
			available = ok
		} else if p.Seats > sub.Seats {
			available = count.Total == 0 || count.Free >= p.Seats-sub.Seats
		}
		plans = append(plans, map[string]any{"id": p.ID, "name": p.Name, "price_cents": p.PriceCents, "currency": p.Currency, "trial_days": p.TrialDays,
			"seats": p.Seats, "memory_gb": p.MemoryGB, "disk_gb": p.DiskGB, "egress_gb": p.EgressGB, "project_limit": ProjectCap, "available": available,
			"intro_price_cents": p.IntroCents, "intro_months": p.IntroMonths, "intro_egress_gb": p.IntroEgressGB})
	}
	place, err := WaitlistPlace(ctx, s.pool, u.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"subscription":   SubJSON(sub),
		"usage":          usage.JSON(),
		"plans":          plans,
		"intro_eligible": intro,
		"seats":          map[string]any{"total": count.Total, "held": count.Held, "free": count.Free, "waiting": count.Waiting},
		"waitlist":       place.JSON(),
		"paddle":         map[string]any{"environment": s.cfg.Environment(), "client_token": s.cfg.ClientToken},
	}
	return out, nil
}

// SubJSON is the `subscription` object of GET /billing, nil for none.
// next_charge_cents is what Paddle charges at next_billed_at, and
// intro_until is when the introductory offer ends, null when the
// subscription has none or Paddle has not fixed the end (DECISIONS I-497).
func SubJSON(sub *Sub) any {
	if sub == nil {
		return nil
	}
	var next any
	if sub.NextBilledAt != nil {
		next = sub.ChargeCents(*sub.NextBilledAt)
	}
	var introUntil any
	if sub.Intro && sub.IntroUntil != nil {
		introUntil = sub.IntroUntil
	}
	return map[string]any{"id": sub.ID, "plan": sub.Plan, "status": sub.Status, "seats": sub.Seats, "period_start": sub.PeriodStart, "period_end": sub.PeriodEnd,
		"next_billed_at": sub.NextBilledAt, "trial_end": sub.TrialEnd, "cancel_at": sub.CancelAt, "scheduled_plan": sub.ScheduledPlan,
		"next_charge_cents": next, "intro_until": introUntil}
}

// introEligible reports whether a checkout by u now carries the
// introductory discount: the discount is configured and u has never had a
// subscription (DECISIONS I-497).
func (s *Service) introEligible(ctx context.Context, u *store.User) (bool, error) {
	if s.cfg.DiscountIntro == "" {
		return false, nil
	}
	ever, err := EverSubscribed(ctx, s.pool, u.ID)
	return !ever, err
}

// Checkout is POST /billing/checkout: the seat first, then the customer
// and the transaction the dashboard opens with Paddle.js.
func (s *Service) Checkout(ctx context.Context, u *store.User, planID string) (transactionID string, err error) {
	plan, ok := PlanByID(planID)
	if !ok {
		return "", ErrUnknownPlan
	}
	if s.seats == nil || s.paddle == nil {
		return "", ErrDisabled
	}
	sub, err := LiveSubscription(ctx, s.pool, u.ID)
	if err != nil {
		return "", err
	}
	if sub != nil {
		return "", ErrSubscribed
	}
	free, place, err := s.seats.Reserve(ctx, u.ID.String(), plan.Seats)
	if err != nil {
		return "", err
	}
	if !free {
		p := waitlist.Place{}
		if place != nil {
			p = *place
		}
		if p.Email == "" && u.Email != nil {
			p.Email = *u.Email
		}
		s.log.Info("checkout waitlisted", "event", "waitlist_join", "user_id", u.ID.String(), "position", p.Position)
		return "", &WaitlistedError{Place: p}
	}
	customer, err := s.paddle.EnsureCustomer(ctx, s.pool, u.ID)
	if err != nil {
		return "", err
	}
	discount := ""
	if plan.HasIntro() {
		ok, err := s.introEligible(ctx, u)
		if err != nil {
			return "", err
		}
		if ok {
			discount = s.cfg.DiscountIntro
		}
	}
	txn, err := s.paddle.CreateCheckoutTransaction(ctx, customer, s.cfg.PlanPrice(plan.ID), discount, u.ID)
	if err != nil {
		return "", fmt.Errorf("create the checkout transaction: %w", err)
	}
	s.log.Info("checkout transaction created", "event", "billing_checkout", "user_id", u.ID.String(), "plan", plan.ID, "intro", discount != "")
	return txn, nil
}

// PlanChange is POST /billing/plan's answer.
type PlanChange struct {
	Plan          string
	ScheduledPlan *string
	EffectiveAt   time.Time
}

// ChangePlan moves the account between plans: an upgrade at once
// (prorated by Paddle), a downgrade at period_end after checking the
// account fits (PRICING.md "Cancelling and changing plans").
func (s *Service) ChangePlan(ctx context.Context, u *store.User, planID string) (PlanChange, error) {
	target, ok := PlanByID(planID)
	if !ok {
		return PlanChange{}, ErrUnknownPlan
	}
	if s.paddle == nil {
		return PlanChange{}, ErrDisabled
	}
	sub, err := LiveSubscription(ctx, s.pool, u.ID)
	if err != nil {
		return PlanChange{}, err
	}
	if sub == nil {
		return PlanChange{}, ErrNoSubscription
	}
	current := sub.PlanOrSolo()
	if current.ID == target.ID && (sub.ScheduledPlan == nil || *sub.ScheduledPlan == target.ID) {
		return PlanChange{}, ErrSamePlan
	}
	now := s.Now().UTC()
	if target.Seats > current.Seats {
		if count, err := s.seats.Count(ctx); err != nil {
			return PlanChange{}, err
		} else if count.Total > 0 && count.Free < target.Seats-current.Seats {
			return PlanChange{}, ErrNoSeat
		}
		ps, err := s.paddle.UpdateSubscriptionItems(ctx, sub.ID, s.cfg.PlanPrice(target.ID), ProrateImmediately)
		if err != nil {
			return PlanChange{}, fmt.Errorf("upgrade the subscription: %w", err)
		}
		// The webhook writes the same; writing it here too means the
		// answer and the next /me agree without waiting for it.
		err = db.InTx(ctx, s.pool, func(tx db.Tx) error {
			if _, err := tx.Exec(ctx, "update subscriptions set plan = $2, seats = $3, scheduled_plan = null where id = $1", sub.ID, target.ID, target.Seats); err != nil {
				return err
			}
			_, err := events.InsertAccount(ctx, tx, u.ID, now, KindPlanChanged, PlanChangedPayload{FromPlan: current.ID, ToPlan: target.ID, EffectiveAt: now})
			return err
		})
		if err != nil {
			return PlanChange{}, err
		}
		_ = ps
		return PlanChange{Plan: target.ID, EffectiveAt: now}, nil
	}
	if current.ID == target.ID {
		// Undo a scheduled downgrade: back to the current price at the
		// next period, which Paddle expresses as the same item again.
		if _, err := s.paddle.UpdateSubscriptionItems(ctx, sub.ID, s.cfg.PlanPrice(current.ID), ProrateNextBillingCycle); err != nil {
			return PlanChange{}, fmt.Errorf("undo the scheduled downgrade: %w", err)
		}
		if _, err := s.pool.Exec(ctx, "update subscriptions set scheduled_plan = null where id = $1", sub.ID); err != nil {
			return PlanChange{}, err
		}
		return PlanChange{Plan: current.ID, EffectiveAt: now}, nil
	}
	// A downgrade: the account has to fit the smaller plan now.
	usage, err := LoadUsage(ctx, s.pool, u.ID, target, sub.Period(now))
	if err != nil {
		return PlanChange{}, err
	}
	if usage.RunningGB > target.MemoryGB || usage.DiskOver() {
		return PlanChange{}, &OverPlanError{RunningGB: usage.RunningGB, DiskHeldBytes: usage.DiskHeldBytes, Plan: target}
	}
	if _, err := s.paddle.UpdateSubscriptionItems(ctx, sub.ID, s.cfg.PlanPrice(target.ID), ProrateNextBillingCycle); err != nil {
		return PlanChange{}, fmt.Errorf("schedule the downgrade: %w", err)
	}
	if _, err := s.pool.Exec(ctx, "update subscriptions set scheduled_plan = $2 where id = $1", sub.ID, target.ID); err != nil {
		return PlanChange{}, err
	}
	effective := now
	if sub.PeriodEnd != nil {
		effective = sub.PeriodEnd.UTC()
	}
	sp := target.ID
	return PlanChange{Plan: current.ID, ScheduledPlan: &sp, EffectiveAt: effective}, nil
}

// Cancel is POST /billing/cancel: the subscription ends at period_end
// (during the trial, at trial_end); machines run until then.
func (s *Service) Cancel(ctx context.Context, u *store.User) (cancelAt time.Time, err error) {
	if s.paddle == nil {
		return cancelAt, ErrDisabled
	}
	sub, err := LiveSubscription(ctx, s.pool, u.ID)
	if err != nil {
		return cancelAt, err
	}
	if sub == nil {
		return cancelAt, ErrNoSubscription
	}
	if sub.CancelAt != nil {
		return cancelAt, ErrAlreadyCancelled
	}
	ps, err := s.paddle.CancelSubscription(ctx, sub.ID, EffectiveNextBillingPeriod)
	if err != nil {
		return cancelAt, fmt.Errorf("cancel the subscription: %w", err)
	}
	cancelAt = s.Now().UTC()
	if ps.ScheduledChange != nil {
		if t := paddleTime(ps.ScheduledChange.EffectiveAt); t != nil {
			cancelAt = *t
		}
	} else if sub.PeriodEnd != nil {
		cancelAt = sub.PeriodEnd.UTC()
	}
	plan := sub.PlanOrSolo()
	err = db.InTx(ctx, s.pool, func(tx db.Tx) error {
		if _, err := tx.Exec(ctx, "update subscriptions set cancel_at = $2 where id = $1", sub.ID, cancelAt); err != nil {
			return err
		}
		_, err := events.InsertAccount(ctx, tx, u.ID, s.Now().UTC(), KindSubscriptionCancelled, SubscriptionCancelledPayload{Plan: plan.ID, EndsAt: cancelAt})
		return err
	})
	return cancelAt, err
}

// Resume is POST /billing/resume: undo a scheduled cancellation.
func (s *Service) Resume(ctx context.Context, u *store.User) (*Sub, error) {
	if s.paddle == nil {
		return nil, ErrDisabled
	}
	sub, err := LiveSubscription(ctx, s.pool, u.ID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, ErrNoSubscription
	}
	if sub.CancelAt == nil {
		return nil, ErrNotCancelled
	}
	if _, err := s.paddle.ResumeScheduledChange(ctx, sub.ID); err != nil {
		return nil, fmt.Errorf("remove the scheduled cancellation: %w", err)
	}
	if _, err := s.pool.Exec(ctx, "update subscriptions set cancel_at = null where id = $1", sub.ID); err != nil {
		return nil, err
	}
	sub.CancelAt = nil
	return sub, nil
}

// Portal is POST /billing/portal: Paddle's customer portal, or the deep
// link that updates the payment method.
func (s *Service) Portal(ctx context.Context, u *store.User, purpose string) (string, error) {
	if s.paddle == nil {
		return "", ErrDisabled
	}
	customer, err := s.paddle.EnsureCustomer(ctx, s.pool, u.ID)
	if err != nil {
		return "", err
	}
	var subIDs []string
	sub, err := LiveSubscription(ctx, s.pool, u.ID)
	if err != nil {
		return "", err
	}
	if sub != nil {
		subIDs = []string{sub.ID}
	}
	urls, err := s.paddle.PortalSession(ctx, customer, subIDs)
	if err != nil {
		return "", fmt.Errorf("open the customer portal: %w", err)
	}
	if purpose == "payment_method" && sub != nil {
		if u := urls.UpdatePaymentMethod[sub.ID]; u != "" {
			return u, nil
		}
	}
	return urls.Overview, nil
}

// Invoices is GET /billing/invoices: Paddle's transactions for the
// customer, newest first, up to 24, in the shape the dashboard has
// always read.
func (s *Service) Invoices(ctx context.Context, u *store.User) ([]map[string]any, error) {
	if s.paddle == nil {
		return nil, ErrDisabled
	}
	out := []map[string]any{}
	if u.PaddleCustomerID == nil || *u.PaddleCustomerID == "" {
		return out, nil
	}
	txns, err := s.paddle.ListTransactions(ctx, *u.PaddleCustomerID)
	if err != nil {
		return nil, fmt.Errorf("list the transactions: %w", err)
	}
	for _, t := range txns {
		inv := map[string]any{"id": t.ID, "number": t.InvoiceNumber, "status": t.Status, "currency": t.CurrencyCode,
			"amount_cents": int64(0), "subtotal_cents": int64(0), "tax_cents": int64(0), "created_at": t.CreatedAt,
			"period_start": nil, "period_end": nil, "hosted_url": nil, "pdf_url": nil}
		if t.Details != nil {
			inv["amount_cents"] = minorUnits(t.Details.Totals.GrandTotal)
			inv["subtotal_cents"] = minorUnits(t.Details.Totals.Subtotal)
			inv["tax_cents"] = minorUnits(t.Details.Totals.Tax)
		}
		if t.BillingPeriod != nil {
			inv["period_start"], inv["period_end"] = t.BillingPeriod.StartsAt, t.BillingPeriod.EndsAt
		}
		if t.Status == "completed" || t.Status == "billed" || t.Status == "past_due" {
			if pdf, err := s.paddle.InvoicePDF(ctx, t.ID); err == nil && pdf != "" {
				inv["pdf_url"], inv["hosted_url"] = pdf, pdf
			}
		}
		out = append(out, inv)
	}
	return out, nil
}

func minorUnits(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// CloseAccount is DELETE /me's billing half: charge any pending overage
// for the current period first, then cancel the subscription at once,
// because Paddle drops one-time charges on a cancelled subscription. A
// user without a subscription has nothing to do here.
func (s *Service) CloseAccount(ctx context.Context, userID uuid.UUID) error {
	sub, err := LiveSubscription(ctx, s.pool, userID)
	if err != nil {
		return err
	}
	if sub == nil {
		return nil
	}
	if s.overage != nil {
		if _, err := s.overage.ChargePeriod(ctx, sub, EffectiveImmediately); err != nil {
			return fmt.Errorf("charge the pending overage before cancelling: %w", err)
		}
	}
	if s.paddle == nil {
		return ErrDisabled
	}
	if _, err := s.paddle.CancelSubscription(ctx, sub.ID, EffectiveImmediately); err != nil {
		return fmt.Errorf("cancel the subscription: %w", err)
	}
	now := s.Now().UTC()
	_, err = s.pool.Exec(ctx, "update subscriptions set status = 'canceled', cancel_at = $2 where id = $1", sub.ID, now)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, "update users set billing_status = 'none' where id = $1 and billing_status <> 'exempt'", userID)
	return err
}
