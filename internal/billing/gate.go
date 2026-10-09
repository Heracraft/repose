package billing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
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
// (empty when no memory is asked for), on behalf of Project (uuid.Nil
// for a new one), which is excluded from the running memory it is
// checked against. Disk asks the disk question (DECISIONS I-585): the
// action may make the projects hold more (a create, a restore or a fork
// as a new project, growing a volume). AddHeldBytes is about what it
// adds at once (a new volume's first bytes, the copy of a source's), 0
// for growing a volume, which raises only the ceiling. VolumeBytes is
// the disk size asked for, new or grown: one project's disk may be at
// most the plan's whole disk.
type Request struct {
	Class        string
	Disk         bool
	AddHeldBytes int64
	VolumeBytes  int64
	Project      uuid.UUID
}

// Gate decides.
type Gate struct {
	pool *db.Pool
	cfg  Config
	log  *slog.Logger
	m    *metrics.M
	// Enabled is whether Polar is configured; without it every non-exempt
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

// Suspended is the refusal a suspended account gets, from the gate and
// from every route the account may not call (api.md "payment_required").
func (g *Gate) Suspended() *Refusal {
	return &Refusal{Reason: ReasonSuspended, Message: fmt.Sprintf("Your account is suspended. Pay at %s to lift it, or email support.", g.cfg.BillingURL()), Detail: map[string]any{"reason": ReasonSuspended}}
}

func (g *Gate) check(ctx context.Context, u *store.User, req Request) (*Refusal, error) {
	url := g.cfg.BillingURL()
	if u.SuspendedAt != nil || u.BillingStatus == "suspended" {
		return g.Suspended(), nil
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
	if req.Disk {
		// The plan's disk counts the bytes the projects' volumes hold,
		// not their sizes (I-585). Starting a stopped project is never
		// refused for disk: its bytes are already counted, and starting
		// it is how files are deleted to get under.
		limit := int64(plan.DiskGB) << 30
		if req.VolumeBytes > limit {
			return &Refusal{Reason: ReasonDiskLimit,
				Message: fmt.Sprintf("Your %s plan has %d GB of disk, so one project's disk can be at most %d GB. Upgrade at %s.", plan.Name, plan.DiskGB, plan.DiskGB, url),
				Detail:  map[string]any{"reason": ReasonDiskLimit, "plan": plan.ID, "limit_gb": plan.DiskGB, "volume_gb": GBCeil(req.VolumeBytes)}}, nil
		}
		held, _, err := HeldDisk(ctx, g.pool, u.ID)
		if err != nil {
			return nil, err
		}
		if held+req.AddHeldBytes > limit {
			return &Refusal{Reason: ReasonDiskLimit, Message: diskLimitMessage(plan, held, req.AddHeldBytes, url),
				Detail: map[string]any{"reason": ReasonDiskLimit, "plan": plan.ID, "limit_gb": plan.DiskGB, "used_gb": GBCeil(held), "held_gb": gbTenths(held), "need_gb": gbTenths(req.AddHeldBytes)}}, nil
		}
	}
	return nil, nil
}

// diskLimitMessage is the disk_limit sentence: what the projects hold
// against the plan's disk and, when this would take them past it, about
// how much it adds. A deleted file stops counting when the guest trims
// its disk: daily, and when the machine stops (I-585).
func diskLimitMessage(plan Plan, held, add int64, url string) string {
	fix := fmt.Sprintf("Destroy a project, or delete files in one (they stop counting within a day, or when it stops), or upgrade at %s.", url)
	if held >= int64(plan.DiskGB)<<30 || add <= 0 {
		return fmt.Sprintf("Your projects hold %s GB and your %s plan has %d GB of disk. %s", fmtGB(held), plan.Name, plan.DiskGB, fix)
	}
	return fmt.Sprintf("Your projects hold %s GB and your %s plan has %d GB of disk; this needs about %s GB more. %s", fmtGB(held), plan.Name, plan.DiskGB, fmtGB(add), fix)
}

// fmtGB prints bytes as GB rounded up to a tenth, without a trailing
// ".0": 1.5, 104, 0.1.
func fmtGB(bytes int64) string {
	return strconv.FormatFloat(gbTenths(bytes), 'f', -1, 64)
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
