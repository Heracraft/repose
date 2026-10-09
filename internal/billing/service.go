package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
// invoices and account deletion. It holds the Polar client, the seat
// count and the overage job. A nil *Service is billing disabled.
type Service struct {
	pool    *db.Pool
	polar   *Polar
	cfg     Config
	seats   waitlist.Seats
	overage *Overage
	log     *slog.Logger
	Now     func() time.Time
}

// NewService wires the routes' dependencies together.
func NewService(pool *db.Pool, polar *Polar, cfg Config, seats waitlist.Seats, overage *Overage, log *slog.Logger) *Service {
	return &Service{pool: pool, polar: polar, cfg: cfg, seats: seats, overage: overage, log: log.With("component", obs.ComponentAPI), Now: time.Now}
}

// Polar is the client, for the admin commands.
func (s *Service) Polar() *Polar { return s.polar }

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
	}
	return out, nil
}

// SubJSON is the `subscription` object of GET /billing, nil for none.
// next_charge_cents is what Polar charges at next_billed_at, and
// intro_until is when the introductory offer ends, null when the
// subscription has none (DECISIONS I-497, I-604).
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

// Checkout is POST /billing/checkout: the seat first, then the Polar
// checkout the dashboard sends the browser to. customerIP is the
// browser's address, for the tax country.
func (s *Service) Checkout(ctx context.Context, u *store.User, planID, customerIP string) (checkoutURL string, err error) {
	plan, ok := PlanByID(planID)
	if !ok {
		return "", ErrUnknownPlan
	}
	if s.seats == nil || s.polar == nil {
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
	if u.Email == nil || *u.Email == "" {
		return "", errors.New("the account has no email address; Polar needs one for the customer")
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
	billingURL := s.cfg.BillingURL()
	co, err := s.polar.CreateCheckout(ctx, CheckoutRequest{ProductID: s.cfg.PlanProduct(plan.ID), UserID: u.ID, Email: *u.Email, DiscountID: discount,
		CustomerIP: customerIP, SuccessURL: billingURL + "?checkout=done", ReturnURL: billingURL})
	if err != nil {
		return "", fmt.Errorf("create the checkout: %w", err)
	}
	if co.URL == "" {
		return "", errors.New("create the checkout: Polar answered no url")
	}
	s.log.Info("checkout created", "event", "billing_checkout", "user_id", u.ID.String(), "plan", plan.ID, "intro", discount != "")
	return co.URL, nil
}

// PlanChange is POST /billing/plan's answer.
type PlanChange struct {
	Plan          string
	ScheduledPlan *string
	EffectiveAt   time.Time
}

// ChangePlan moves the account between plans: an upgrade at once
// (the difference charged by Polar now), a downgrade at period_end after checking the
// account fits (PRICING.md "Cancelling and changing plans").
func (s *Service) ChangePlan(ctx context.Context, u *store.User, planID string) (PlanChange, error) {
	target, ok := PlanByID(planID)
	if !ok {
		return PlanChange{}, ErrUnknownPlan
	}
	if s.polar == nil {
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
		// Leaving Solo ends its introductory offer.
		if _, err := s.polar.ChangeProduct(ctx, sub.ID, s.cfg.PlanProduct(target.ID), ProrateInvoice, sub.Intro || current.HasIntro()); err != nil {
			return PlanChange{}, fmt.Errorf("upgrade the subscription: %w", err)
		}
		// The webhook writes the same; writing it here too means the
		// answer and the next /me agree without waiting for it.
		err = db.InTx(ctx, s.pool, func(tx db.Tx) error {
			if _, err := tx.Exec(ctx, "update subscriptions set plan = $2, seats = $3, scheduled_plan = null, intro = false, intro_until = null where id = $1", sub.ID, target.ID, target.Seats); err != nil {
				return err
			}
			_, err := events.InsertAccount(ctx, tx, u.ID, now, KindPlanChanged, PlanChangedPayload{FromPlan: current.ID, ToPlan: target.ID, EffectiveAt: now})
			return err
		})
		if err != nil {
			return PlanChange{}, err
		}
		return PlanChange{Plan: target.ID, EffectiveAt: now}, nil
	}
	if current.ID == target.ID {
		// Undo a scheduled downgrade: Polar drops the pending update.
		if _, err := s.polar.ClearPendingUpdate(ctx, sub.ID); err != nil {
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
	if _, err := s.polar.ChangeProduct(ctx, sub.ID, s.cfg.PlanProduct(target.ID), ProrateNextPeriod, false); err != nil {
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
	if s.polar == nil {
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
	ps, err := s.polar.SetCancelAtPeriodEnd(ctx, sub.ID, true)
	if err != nil {
		return cancelAt, fmt.Errorf("cancel the subscription: %w", err)
	}
	cancelAt = s.Now().UTC()
	if t := ps.CancelAt(); t != nil {
		cancelAt = *t
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
	if s.polar == nil {
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
	if _, err := s.polar.SetCancelAtPeriodEnd(ctx, sub.ID, false); err != nil {
		return nil, fmt.Errorf("remove the scheduled cancellation: %w", err)
	}
	if _, err := s.pool.Exec(ctx, "update subscriptions set cancel_at = null where id = $1", sub.ID); err != nil {
		return nil, err
	}
	sub.CancelAt = nil
	return sub, nil
}

// Portal is POST /billing/portal: Polar's customer portal. Polar has no
// deep link for the payment method, so purpose is accepted and the same
// portal answers it. Polar knows a user only after their first checkout.
func (s *Service) Portal(ctx context.Context, u *store.User, purpose string) (string, error) {
	if s.polar == nil {
		return "", ErrDisabled
	}
	if u.BillingCustomerID == nil || *u.BillingCustomerID == "" {
		ever, err := EverSubscribed(ctx, s.pool, u.ID)
		if err != nil {
			return "", err
		}
		if !ever {
			return "", ErrNoSubscription
		}
	}
	url, err := s.polar.CustomerPortal(ctx, u.ID, s.cfg.PortalReturnURL)
	if IsPolarStatus(err, 404) || IsPolarStatus(err, 422) {
		return "", ErrNoSubscription
	}
	if err != nil {
		return "", fmt.Errorf("open the customer portal: %w", err)
	}
	return url, nil
}

// Invoices is GET /billing/invoices: Polar's orders for the user, newest
// first, up to 24, in the shape the dashboard has always read. An order
// whose invoice Polar has not generated yet gets no PDF link this time
// and is asked for one, which a later listing shows.
func (s *Service) Invoices(ctx context.Context, u *store.User) ([]map[string]any, error) {
	if s.polar == nil {
		return nil, ErrDisabled
	}
	out := []map[string]any{}
	if u.BillingCustomerID == nil || *u.BillingCustomerID == "" {
		return out, nil
	}
	orders, err := s.polar.ListOrders(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("list the orders: %w", err)
	}
	// At most three generation requests a listing: Polar's rate limit is
	// the account's, and a refused one is asked again on a later listing.
	generate := 3
	for _, o := range orders {
		if o.Status == "draft" || o.Status == "void" {
			continue
		}
		inv := map[string]any{"id": o.ID, "number": o.InvoiceNumber, "status": o.Status, "currency": strings.ToUpper(o.Currency),
			"amount_cents": o.TotalAmount, "subtotal_cents": o.NetAmount, "tax_cents": o.TaxAmount, "created_at": o.CreatedAt,
			"period_start": nil, "period_end": nil, "hosted_url": nil, "pdf_url": nil}
		if o.IsInvoiceGenerated {
			if pdf, err := s.polar.OrderInvoiceURL(ctx, o.ID); err == nil && pdf != "" {
				inv["pdf_url"], inv["hosted_url"] = pdf, pdf
			}
		} else if o.Status != "pending" && generate > 0 {
			generate--
			if err := s.polar.GenerateOrderInvoice(ctx, o.ID); err != nil {
				s.log.Warn("invoice generation refused", "event", "billing_invoice", "user_id", u.ID.String(), "result", "error", "err", err.Error())
			}
		}
		out = append(out, inv)
	}
	return out, nil
}

// CloseAccount is DELETE /me's billing half: send any pending egress
// overage for the current period first, then end the subscription. With
// no overage it is revoked at once; with some it is cancelled at the
// period's end, because Polar bills metered usage only on the order a
// period's end makes and a revoke makes none (DECISIONS I-604). No new
// period is charged either way. A user without a subscription has
// nothing to do here.
func (s *Service) CloseAccount(ctx context.Context, userID uuid.UUID) error {
	sub, err := LiveSubscription(ctx, s.pool, userID)
	if err != nil {
		return err
	}
	if sub == nil {
		return nil
	}
	if s.polar == nil {
		return ErrDisabled
	}
	owed := false
	if s.overage != nil {
		c, err := s.overage.ChargePeriod(ctx, sub)
		if err != nil {
			return fmt.Errorf("send the pending overage before cancelling: %w", err)
		}
		owed = c != nil && c.Cents > 0
	}
	now := s.Now().UTC()
	if owed {
		ps, err := s.polar.SetCancelAtPeriodEnd(ctx, sub.ID, true)
		if err != nil && !endedAtPolar(err) {
			return fmt.Errorf("cancel the subscription: %w", err)
		}
		cancelAt := now
		if ps != nil {
			if t := ps.CancelAt(); t != nil {
				cancelAt = *t
			}
		}
		_, err = s.pool.Exec(ctx, "update subscriptions set cancel_at = $2 where id = $1", sub.ID, cancelAt)
		return err
	}
	if _, err := s.polar.RevokeSubscription(ctx, sub.ID); err != nil && !endedAtPolar(err) {
		return fmt.Errorf("revoke the subscription: %w", err)
	}
	_, err = s.pool.Exec(ctx, "update subscriptions set status = 'canceled', cancel_at = $2 where id = $1", sub.ID, now)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, "update users set billing_status = 'none' where id = $1 and billing_status <> 'exempt'", userID)
	return err
}

// endedAtPolar reports whether Polar refused because the subscription is
// already gone or already cancelled, which for account deletion is done.
func endedAtPolar(err error) bool {
	var pe *PolarError
	return errors.As(err, &pe) && (pe.Status == 404 || pe.Type == "AlreadyCanceledSubscription")
}
