package billing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// The compute gate (api.md "Usage and billing", the payment_required
// table; PRICING.md "What a plan means, in rules"). Every place compute is
// asked for goes through Check: create, start, restore, fork, a class
// change, growing a volume. The refusal carries detail.reason and a
// message that is the whole sentence, so an older CLI that prints it is
// right.

// Reasons of a payment_required refusal.
const (
	ReasonSubscriptionRequired = "subscription_required"
	ReasonPlanLimit            = "plan_limit"
	ReasonDiskLimit            = "disk_limit"
	ReasonEgressLimit          = "egress_limit"
	ReasonPastDue              = "past_due"
	ReasonSuspended            = "suspended"
)

// Refusal is a payment_required error with its reason and detail.
type Refusal struct {
	Reason  string
	Message string
	Detail  map[string]any
}

func (r *Refusal) Error() string { return "payment_required: " + r.Message }

// Request is what the caller is about to do: start a machine of Class
// (empty when no memory is asked for), allocate AddDiskBytes more disk
// (0 when none; a new volume's size, or the growth of an existing one),
// on behalf of Project (uuid.Nil for a new one), which is excluded from
// the running memory it is checked against.
type Request struct {
	Class        string
	AddDiskBytes int64
	Project      uuid.UUID
}

// Gate decides.
type Gate struct {
	pool *db.Pool
	cfg  Config
	log  *slog.Logger
	m    *metrics.M
	// Enabled is whether Paddle is configured; without it every non-exempt
	// user is refused with subscription_required (I-289).
	Enabled bool
	// Enforce is BILLING_ENFORCE (§8); false lets everything through.
	Enforce bool
	Now     func() time.Time
}

// NewGate builds the gate.
func NewGate(pool *db.Pool, cfg Config, m *metrics.M, log *slog.Logger) *Gate {
	if log == nil {
		log = obs.NewLogger(obs.LogOptions{Component: obs.ComponentAPI, Writer: io.Discard})
	}
	return &Gate{pool: pool, cfg: cfg, log: log.With("component", obs.ComponentAPI), m: m, Enabled: cfg.Enabled(), Enforce: cfg.Enforce, Now: time.Now}
}

// Check returns nil or a *Refusal (or a database error).
func (g *Gate) Check(ctx context.Context, u *store.User, req Request) error {
	if u.BillingStatus == "exempt" || !g.Enforce {
		return nil
	}
	r, err := g.check(ctx, u, req)
	if err != nil {
		return err
	}
	if r != nil {
		if g.m != nil {
			g.m.BillingGateRefusedTotal.WithLabelValues(r.Reason).Inc()
		}
		plan, _ := r.Detail["plan"].(string)
		g.log.Info("compute refused by the billing gate", "event", obs.EventBillingGateRefused, "user_id", u.ID.String(), "reason", r.Reason, "plan", plan)
		return r
	}
	return nil
}

func (g *Gate) check(ctx context.Context, u *store.User, req Request) (*Refusal, error) {
	url := g.cfg.BillingURL()
	if u.SuspendedAt != nil || u.BillingStatus == "suspended" {
		return &Refusal{Reason: ReasonSuspended, Message: fmt.Sprintf("Your account is suspended. Pay at %s to lift it, or email support.", url), Detail: map[string]any{"reason": ReasonSuspended}}, nil
	}
	sub, err := LiveSubscription(ctx, g.pool, u.ID)
	if err != nil {
		return nil, err
	}
	if sub == nil || !g.Enabled {
		r := &Refusal{Reason: ReasonSubscriptionRequired, Message: fmt.Sprintf("Choose a plan at %s first.", url), Detail: map[string]any{"reason": ReasonSubscriptionRequired, "waitlist": nil}}
		if place, err := WaitlistPlace(ctx, g.pool, u.ID); err != nil {
			return nil, err
		} else if place != nil && place.Position > 0 {
			r.Detail["waitlist"] = map[string]any{"position": place.Position, "joined_at": place.JoinedAt}
			email := ""
			if u.Email != nil {
				email = *u.Email
			}
			r.Message = waitlist.Message(place.Position, email)
		}
		return r, nil
	}
	period := sub.Period(g.Now())
	plan := sub.PlanFor(period.Start)
	if sub.Status == StatusPastDue || u.BillingStatus == "past_due" {
		return &Refusal{Reason: ReasonPastDue, Message: fmt.Sprintf("Your last payment failed. Update your card at %s to start machines again.", url), Detail: map[string]any{"reason": ReasonPastDue, "plan": plan.ID}}, nil
	}
	egress, err := PeriodEgress(ctx, g.pool, u.ID, period)
	if err != nil {
		return nil, err
	}
	if egress >= plan.EgressHardStopBytes() {
		return &Refusal{Reason: ReasonEgressLimit,
			Message: fmt.Sprintf("Your machines are stopped until %s: this period's egress passed %d GB, four times the %s plan's %d GB allowance. Upgrade at %s, or wait for the period to end.",
				period.End.Format("2 January"), plan.EgressHardStopBytes()>>30, plan.Name, plan.EgressGB, url),
			Detail: map[string]any{"reason": ReasonEgressLimit, "plan": plan.ID, "limit_gb": plan.EgressHardStopBytes() >> 30, "used_gb": GBCeil(egress), "until": period.End}}, nil
	}
	if req.Class != "" {
		running, usedGB, err := RunningMemory(ctx, g.pool, u.ID, req.Project)
		if err != nil {
			return nil, err
		}
		need := ClassMemoryGB(req.Class)
		if usedGB+need > plan.MemoryGB {
			slugs := make([]string, 0, len(running))
			for _, p := range running {
				slugs = append(slugs, p.Slug)
			}
			return &Refusal{Reason: ReasonPlanLimit, Message: planLimitMessage(plan, req.Class, slugs, url),
				Detail: map[string]any{"reason": ReasonPlanLimit, "plan": plan.ID, "limit_gb": plan.MemoryGB, "used_gb": usedGB, "projects": slugs}}, nil
		}
	}
	if req.AddDiskBytes > 0 {
		// Every live project counts, the one being grown included at its
		// current size: AddDiskBytes is the growth.
		allocated, _, err := AllocatedDisk(ctx, g.pool, u.ID, uuid.Nil)
		if err != nil {
			return nil, err
		}
		usedGB := GBCeil(allocated)
		if usedGB+GBCeil(req.AddDiskBytes) > int64(plan.DiskGB) {
			return &Refusal{Reason: ReasonDiskLimit,
				Message: fmt.Sprintf("Your %s plan allocates up to %d GB of disk and your projects use %d GB; this needs %d GB more. Destroy a project, or upgrade at %s.",
					plan.Name, plan.DiskGB, usedGB, GBCeil(req.AddDiskBytes), url),
				Detail: map[string]any{"reason": ReasonDiskLimit, "plan": plan.ID, "limit_gb": plan.DiskGB, "used_gb": usedGB}}, nil
		}
	}
	return nil, nil
}

// planLimitMessage names the machines using the memory, or the class that
// does not fit at all: "Your Solo plan runs 8 GB at once and todo-app is
// using it. Stop it, or upgrade at https://repose.herakraft.co/billing."
func planLimitMessage(plan Plan, class string, slugs []string, url string) string {
	need := ClassMemoryGB(class)
	if need > plan.MemoryGB {
		return fmt.Sprintf("Your %s plan runs %d GB at once and an %s machine needs %d GB. Upgrade to %s at %s.", plan.Name, plan.MemoryGB, class, need, SmallestFor(class).Name, url)
	}
	switch len(slugs) {
	case 0:
		return fmt.Sprintf("Your %s plan runs %d GB at once. Upgrade at %s.", plan.Name, plan.MemoryGB, url)
	case 1:
		return fmt.Sprintf("Your %s plan runs %d GB at once and %s is using it. Stop it, or upgrade at %s.", plan.Name, plan.MemoryGB, slugs[0], url)
	}
	return fmt.Sprintf("Your %s plan runs %d GB at once and %s are using it. Stop one, or upgrade at %s.", plan.Name, plan.MemoryGB, joinAnd(slugs), url)
}

func joinAnd(s []string) string {
	if len(s) <= 1 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// Limits is the `limits` block of /me: the account's project cap
// (running or stopped, the same on every plan, I-569), whether xl fits,
// memory, disk and egress. An exempt account, or one without a plan, has
// the operator-set xl_limit and the Solo plan's other numbers, which is
// what an operator's own account works within (I-16).
type Limits struct {
	Projects int
	XL       int
	MemoryGB int
	DiskGB   int
	EgressGB int
	Plan     *Plan
}

// LimitsFor computes a user's limits from their live subscription, with
// the egress allowance of the current period (DECISIONS I-497).
func LimitsFor(u *store.User, sub *Sub) Limits {
	if sub != nil && sub.Live() {
		p := sub.PlanFor(sub.Period(time.Now()).Start)
		xl := 0
		if p.AllowsXL() {
			xl = 1
		}
		return Limits{Projects: AccountProjectCap(u.ProjectLimit), XL: xl, MemoryGB: p.MemoryGB, DiskGB: p.DiskGB, EgressGB: p.EgressGB, Plan: &p}
	}
	l := Limits{Projects: AccountProjectCap(u.ProjectLimit), XL: u.XLLimit, MemoryGB: Solo.MemoryGB, DiskGB: Solo.DiskGB, EgressGB: Solo.EgressGB}
	if u.BillingStatus == "exempt" {
		// An exempt account is not gated on memory or disk; its limits
		// block says what the biggest plan would allow.
		l.MemoryGB, l.DiskGB, l.EgressGB = Pro.MemoryGB, Pro.DiskGB, Pro.EgressGB
		if l.XL == 0 {
			l.XL = 1
		}
	}
	return l
}

// JSON is the /me limits object.
func (l Limits) JSON() map[string]any {
	return map[string]any{"projects": l.Projects, "xl": l.XL, "memory_gb": l.MemoryGB, "disk_gb": l.DiskGB, "egress_gb": l.EgressGB}
}

// Place is a user's waitlist row as /me, /billing and the gate report it.
type Place struct {
	Position  int
	JoinedAt  time.Time
	InvitedAt *time.Time
	HoldUntil *time.Time
}

// WaitlistPlace reads the user's waitlist place from the 0008 schema
// (I-290): the position among waiting users of live accounts, oldest
// first, or the invitation and its hold. nil when the user holds no place
// (never joined, converted, or suspended, cancelled or deleted).
func WaitlistPlace(ctx context.Context, q store.Querier, userID uuid.UUID) (*Place, error) {
	var p Place
	var converted *time.Time
	err := q.QueryRow(ctx, `select w.joined_at, w.invited_at, w.hold_until, w.converted_at,
		coalesce((select count(*) from waitlist w2 join users u2 on u2.id = w2.user_id
			where w2.invited_at is null and w2.converted_at is null and u2.suspended_at is null and u2.cancelled_at is null and u2.deleted_at is null
			and (w2.joined_at, w2.user_id) <= (w.joined_at, w.user_id)), 0)::integer
		from waitlist w join users u on u.id = w.user_id
		where w.user_id = $1 and u.suspended_at is null and u.cancelled_at is null and u.deleted_at is null`, userID).
		Scan(&p.JoinedAt, &p.InvitedAt, &p.HoldUntil, &converted, &p.Position)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if converted != nil {
		return nil, nil
	}
	if p.InvitedAt != nil {
		p.Position = 0
	}
	return &p, nil
}

// JSON is the /me and /billing `waitlist` object.
func (p *Place) JSON() any {
	if p == nil {
		return nil
	}
	return map[string]any{"position": p.Position, "joined_at": p.JoinedAt, "invited_at": p.InvitedAt, "hold_until": p.HoldUntil}
}
