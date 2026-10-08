package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/obs"
)

// The /billing routes of docs/interfaces/api.md "Usage and billing"
// (DECISIONS I-289, I-290). Every one answers 503 billing_disabled when
// the api has no PADDLE_API_KEY (s.d.Billing is nil); the logic is
// internal/billing's, this file maps its answers and refusals to HTTP.

// billingService returns the service or the disabled error.
func (s *Server) billingService() (*billing.Service, error) {
	if s.d.Billing == nil {
		return nil, billing.ErrDisabled
	}
	return s.d.Billing, nil
}

// gate is the compute gate every start, create, restore, fork, class
// change and volume growth passes (api.md's payment_required table). A
// refusal becomes 402 payment_required with detail.reason and the whole
// sentence as message.
func (s *Server) gate(r *http.Request, u *store.User, req billing.Request) error {
	err := s.d.Gate.Check(r.Context(), u, req)
	var ref *billing.Refusal
	if errors.As(err, &ref) {
		return withDetail(errf("payment_required", "%s", ref.Message), ref.Detail)
	}
	return err
}

// limits is the user's limits, for the project cap (I-569)
// (billing.LimitsFor).
func (s *Server) limits(r *http.Request, u *store.User) (billing.Limits, error) {
	sub, err := billing.LiveSubscription(r.Context(), s.d.Pool, u.ID)
	if err != nil {
		return billing.Limits{}, err
	}
	return billing.LimitsFor(u, sub), nil
}

func (s *Server) billingOverview(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.billingService()
	if err != nil {
		return err
	}
	out, err := svc.Overview(r.Context(), userFrom(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) billingCheckout(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.billingService()
	if err != nil {
		return err
	}
	u := userFrom(r.Context())
	var body struct {
		Plan string `json:"plan"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	txn, err := svc.Checkout(r.Context(), u, body.Plan)
	var wl *billing.WaitlistedError
	switch {
	case errors.Is(err, billing.ErrUnknownPlan):
		return errf("invalid", "plan must be solo, plus or pro")
	case errors.Is(err, billing.ErrSubscribed):
		return withDetail(errf("conflict", "you already have a plan; change it from the billing page"), map[string]any{"reason": "subscribed"})
	case errors.As(err, &wl):
		return withDetail(errf("waitlisted", "%s", wl.Message()), map[string]any{"position": wl.Place.Position, "joined_at": wl.Place.JoinedAt, "email": wl.Place.Email})
	case err != nil:
		return err
	}
	cfg := svc.Paddle().Config()
	writeJSON(w, http.StatusOK, map[string]any{"transaction_id": txn, "client_token": cfg.ClientToken, "environment": cfg.Environment()})
	return nil
}

func (s *Server) billingPlan(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.billingService()
	if err != nil {
		return err
	}
	var body struct {
		Plan string `json:"plan"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	ch, err := svc.ChangePlan(r.Context(), userFrom(r.Context()), body.Plan)
	var over *billing.OverPlanError
	switch {
	case errors.Is(err, billing.ErrUnknownPlan):
		return errf("invalid", "plan must be solo, plus or pro")
	case errors.Is(err, billing.ErrNoSubscription):
		return withDetail(errf("conflict", "you have no plan yet; choose one with a checkout"), map[string]any{"reason": "no_subscription"})
	case errors.Is(err, billing.ErrSamePlan):
		return withDetail(errf("conflict", "you are on that plan already"), map[string]any{"reason": "same_plan"})
	case errors.Is(err, billing.ErrNoSeat):
		return withDetail(errf("conflict", "no seat is free for the upgrade right now; try again later"), map[string]any{"reason": "no_seat"})
	case errors.As(err, &over):
		// disk_allocated_gb is the held figure too, kept one release (I-585).
		return withDetail(errf("conflict", "your account does not fit the %s plan yet: %d GB running (it allows %d) and %s GB of disk held by your projects (it allows %d); stop machines, or destroy projects or delete files in them, first",
			over.Plan.Name, over.RunningGB, over.Plan.MemoryGB, over.HeldGBText(), over.Plan.DiskGB),
			map[string]any{"reason": "over_plan", "running_gb": over.RunningGB, "disk_held_gb": over.DiskHeldGB(), "disk_allocated_gb": billing.GBCeil(over.DiskHeldBytes)})
	case err != nil:
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": ch.Plan, "scheduled_plan": ch.ScheduledPlan, "effective_at": ch.EffectiveAt})
	return nil
}

func (s *Server) billingCancel(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.billingService()
	if err != nil {
		return err
	}
	at, err := svc.Cancel(r.Context(), userFrom(r.Context()))
	switch {
	case errors.Is(err, billing.ErrNoSubscription):
		return withDetail(errf("conflict", "you have no plan to cancel"), map[string]any{"reason": "no_subscription"})
	case errors.Is(err, billing.ErrAlreadyCancelled):
		return withDetail(errf("conflict", "the plan is already cancelled"), map[string]any{"reason": "already_cancelled"})
	case err != nil:
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancel_at": at})
	return nil
}

func (s *Server) billingResume(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.billingService()
	if err != nil {
		return err
	}
	sub, err := svc.Resume(r.Context(), userFrom(r.Context()))
	switch {
	case errors.Is(err, billing.ErrNoSubscription):
		return withDetail(errf("conflict", "you have no plan to resume"), map[string]any{"reason": "no_subscription"})
	case errors.Is(err, billing.ErrNotCancelled):
		return withDetail(errf("conflict", "the plan is not cancelled"), map[string]any{"reason": "not_cancelled"})
	case err != nil:
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": sub.Plan, "period_end": sub.PeriodEnd})
	return nil
}

func (s *Server) billingPortal(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.billingService()
	if err != nil {
		return err
	}
	var body struct {
		For string `json:"for"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	if body.For != "" && body.For != "payment_method" {
		return errf("invalid", "for must be payment_method or absent")
	}
	url, err := svc.Portal(r.Context(), userFrom(r.Context()), body.For)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url})
	return nil
}

func (s *Server) billingInvoices(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.billingService()
	if err != nil {
		return err
	}
	inv, err := svc.Invoices(r.Context(), userFrom(r.Context()))
	if err != nil {
		return err
	}
	if inv == nil {
		inv = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, inv)
	return nil
}

// POST /v1/billing/webhook. Paddle authenticates itself with the
// Paddle-Signature header, so the route carries no bearer token and is
// registered outside the authenticated set. The body is read with a cap
// and never logged: it carries the customer's details, and a delivery
// that fails verification is logged with the event type only.
const webhookMaxBytes = 1 << 20

func (s *Server) billingWebhook(w http.ResponseWriter, r *http.Request) error {
	if s.d.Webhooks == nil {
		return errf("billing_disabled", "billing is not configured")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookMaxBytes))
	if err != nil {
		return errf("invalid", "could not read the webhook body")
	}
	kind, err := s.d.Webhooks.Handle(r.Context(), body, r.Header.Get("Paddle-Signature"))
	label := kind
	if label == "" {
		label = "none"
	}
	switch {
	case errors.Is(err, billing.ErrDuplicate):
		// Already applied; answering 200 stops Paddle retrying.
		s.d.Metrics.BillingWebhookTotal.WithLabelValues(label, "duplicate").Inc()
		writeJSON(w, http.StatusOK, map[string]any{"received": true, "duplicate": true})
		return nil
	case errors.Is(err, billing.ErrBadSignature):
		s.d.Metrics.BillingWebhookTotal.WithLabelValues(label, "bad_signature").Inc()
		obs.Logger(r.Context(), s.d.Log).Warn("paddle webhook signature rejected", "event", obs.EventBillingWebhook, "kind", label, "result", "bad_signature")
		return errf("invalid", "paddle signature verification failed")
	case errors.Is(err, billing.ErrDisabled):
		return err
	case err != nil:
		s.d.Metrics.BillingWebhookTotal.WithLabelValues(label, "error").Inc()
		obs.Logger(r.Context(), s.d.Log).Error("paddle webhook not applied", "event", obs.EventBillingWebhook, "kind", label, "result", "error", "err", err.Error())
		return err
	}
	s.d.Metrics.BillingWebhookTotal.WithLabelValues(label, "ok").Inc()
	writeJSON(w, http.StatusOK, map[string]any{"received": true})
	return nil
}
