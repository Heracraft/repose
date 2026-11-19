package billing

// Own hardware: the two questions the rest of the api asks the plan about
// a host a user owns (DECISIONS I-1366, and I-1364 for the plan itself).
//
// One is "how many hosts may this account add", which `POST /hosts`
// refuses past. The other is "may this account's machines run on hardware
// repose bought", which placement reads: an Own hardware account buys no
// fleet memory, so placement is the only thing between it and the fleet,
// and a filter is the right shape for it rather than a refusal, because
// the account has a perfectly good host of its own to be placed on.
//
// Both answers come from the plan's own row (Plan.Hosts, Plan.MemoryGB),
// so selling a second host, or a plan that sells both, is a number in
// plans.go and nothing here.

import (
	"context"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// Reasons a host or a hosted placement is refused.
const (
	// ReasonOwnHardwarePlanRequired is `POST /hosts` from an account
	// whose plan sells no host.
	ReasonOwnHardwarePlanRequired = "own_hardware_plan_required"
	// ReasonHostedPlanRequired is a project asking for
	// `placement: "hosted"` on a plan that sells no memory on repose's
	// own hosts.
	ReasonHostedPlanRequired = "hosted_plan_required"
)

// ExemptHosts is how many hosts a billing-exempt account may own. One,
// the same as the Own hardware plan: an exempt account pays for nothing
// and is refused nothing (I-1090), and the owner adding their own Mac
// must not have to sell themselves a plan first.
const ExemptHosts = 1

// planHosts is the plan's own answer: the hosts an account's live
// subscription sells it, and ExemptHosts for a billing-exempt account,
// which pays for nothing and is refused nothing (I-1090). It is the one
// implementation of the number: Gate.HostsAllowed adds what only the
// gate knows (billing off, enforcement off) and `POST /hosts` refuses
// past it, LimitsFor reports the same figure as /me's `limits.hosts`,
// and the two therefore cannot tell a user different things.
func planHosts(u *store.User, sub *Sub) int {
	if u != nil && u.BillingStatus == "exempt" {
		return ExemptHosts
	}
	if sub != nil && sub.Live() {
		return sub.PlanOrSolo().Hosts
	}
	return 0
}

// HostsAllowed is how many hosts of their own the account may have now:
// the plan's figure, ExemptHosts for a billing-exempt account or an api
// that does not enforce billing, and none for an account with no live
// subscription.
func (g *Gate) HostsAllowed(ctx context.Context, q store.Querier, u *store.User) (int, error) {
	if u.BillingStatus == "exempt" || !g.Enforce {
		return ExemptHosts, nil
	}
	sub, err := LiveSubscription(ctx, q, u.ID)
	if err != nil {
		return 0, err
	}
	if sub == nil || !g.Enabled {
		return 0, nil
	}
	return planHosts(u, sub), nil
}

// FleetAllowed is whether the account's machines may be placed on the
// hosts repose runs: true unless its plan sells no memory there. It
// implements ops.FleetPolicy, which is how the ops engine asks without
// importing this package.
//
// An account with no live subscription is true: the gate has already
// refused it with subscription_required before any placement runs, and
// answering false here would turn that refusal into a placement failure
// with a worse sentence. An api that does not enforce billing is true
// for the same reason it lets everything else through.
func (g *Gate) FleetAllowed(ctx context.Context, q store.Querier, userID uuid.UUID) (bool, error) {
	if !g.Enforce {
		return true, nil
	}
	u, err := store.GetUser(ctx, q, userID)
	if err != nil {
		return false, err
	}
	if u.BillingStatus == "exempt" {
		return true, nil
	}
	sub, err := LiveSubscription(ctx, q, userID)
	if err != nil {
		return false, err
	}
	if sub == nil || !g.Enabled {
		return true, nil
	}
	return sub.PlanFor(sub.Period(g.Now()).Start).MemoryGB > 0, nil
}

// HostedRefusal is the refusal a project that asked for the hosted fleet
// gets when its plan sells no memory there. It names the one way out:
// the machine runs on the account's own host, or the account changes
// plan.
func (g *Gate) HostedRefusal(plan string) *Refusal {
	return &Refusal{
		Reason:  ReasonHostedPlanRequired,
		Message: "Your plan runs machines on your own host. Choose a plan at " + g.cfg.BillingURL() + " to run one on repose's servers.",
		Detail:  map[string]any{"reason": ReasonHostedPlanRequired, "plan": plan},
	}
}
