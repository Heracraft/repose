package billing

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// Account is one user's billing state in one place: what `repose-admin
// billing show` prints and what the sandbox runbook reads its evidence
// from (docs/ops/M4-GATE.md): the subscription, the plan, the period, the
// period's hours, disk and egress, and the overage arithmetic.
type Account struct {
	Handle       string
	UserID       uuid.UUID
	Status       string
	HasCard      bool
	Customer     string
	Sub          *Sub
	Plan         Plan
	Period       Period
	PastDueSince *time.Time
	SuspendedAt  *time.Time
	Limits       Limits
	Usage        Usage
	Hours        map[string]int64
	Overage      []OverageRow
	Place        *Place
}

// OverageRow is one overage_charges row.
type OverageRow struct {
	PeriodStart time.Time
	EgressGB    int64
	Cents       int64
	// SentRef is the external id Polar accepted the line under; "" while
	// it is not sent.
	SentRef   string
	CreatedAt time.Time
}

// LoadAccount reads the account from the database alone.
func LoadAccount(ctx context.Context, pool *db.Pool, userID uuid.UUID, at time.Time) (Account, error) {
	u, err := store.GetUser(ctx, pool, userID)
	if err != nil {
		return Account{}, err
	}
	a := Account{Handle: u.Handle, UserID: u.ID, Status: u.BillingStatus, HasCard: u.HasCard, PastDueSince: u.PastDueSince, SuspendedAt: u.SuspendedAt}
	if u.BillingCustomerID != nil {
		a.Customer = *u.BillingCustomerID
	}
	if a.Sub, err = LiveSubscription(ctx, pool, u.ID); err != nil {
		return a, err
	}
	if a.Sub == nil {
		if a.Sub, err = LatestSubscription(ctx, pool, u.ID); err != nil {
			return a, err
		}
	}
	a.Period = a.Sub.Period(at)
	a.Plan = a.Sub.PlanFor(a.Period.Start)
	a.Limits = LimitsFor(u, a.Sub)
	if a.Usage, err = LoadUsage(ctx, pool, u.ID, a.Plan, a.Period); err != nil {
		return a, err
	}
	if a.Hours, err = PeriodHours(ctx, pool, u.ID, a.Period); err != nil {
		return a, err
	}
	if a.Place, err = WaitlistPlace(ctx, pool, u.ID); err != nil {
		return a, err
	}
	if a.Sub != nil {
		rows, err := pool.Query(ctx, "select period_start, egress_gb::bigint, cents, coalesce(sent_ref, ''), created_at from overage_charges where subscription_id = $1 order by period_start desc limit 6", a.Sub.ID)
		if err != nil {
			return a, err
		}
		defer rows.Close()
		for rows.Next() {
			var o OverageRow
			if err := rows.Scan(&o.PeriodStart, &o.EgressGB, &o.Cents, &o.SentRef, &o.CreatedAt); err != nil {
				return a, err
			}
			a.Overage = append(a.Overage, o)
		}
		return a, rows.Err()
	}
	return a, nil
}

func fmtT(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// WriteTo prints the account as a two-column table.
func (a Account) WriteTo(w io.Writer) (int64, error) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	row := func(k, v string) { _, _ = fmt.Fprintf(tw, "%s\t%s\n", k, v) }
	row("handle", a.Handle)
	row("billing", a.Status)
	row("has_card", fmt.Sprint(a.HasCard))
	row("polar customer", orDash(a.Customer))
	if a.Sub == nil {
		row("subscription", "- (no plan chosen)")
	} else {
		row("subscription", fmt.Sprintf("%s %s %s (%d seat(s))", a.Sub.ID, a.Sub.Plan, a.Sub.Status, a.Sub.Seats))
		row("  next billed", fmtT(a.Sub.NextBilledAt))
		row("  trial ends", fmtT(a.Sub.TrialEnd))
		row("  cancels at", fmtT(a.Sub.CancelAt))
		if a.Sub.ScheduledPlan != nil {
			row("  scheduled plan", *a.Sub.ScheduledPlan)
		}
		row("  overage charged for", fmtT(a.Sub.OverageChargedFor))
	}
	row("past due since", fmtT(a.PastDueSince))
	row("suspended", fmtT(a.SuspendedAt))
	row("limits", fmt.Sprintf("%d projects, xl %d, %d GB running, %d GB disk, %d GB egress", a.Limits.Projects, a.Limits.XL, a.Limits.MemoryGB, a.Limits.DiskGB, a.Limits.EgressGB))
	if a.Place != nil {
		row("waitlist", fmt.Sprintf("position %d, joined %s, invited %s, hold until %s", a.Place.Position, a.Place.JoinedAt.UTC().Format(time.RFC3339), fmtT(a.Place.InvitedAt), fmtT(a.Place.HoldUntil)))
	}
	row("period", a.Period.Start.Format(time.RFC3339)+" to "+a.Period.End.Format(time.RFC3339))
	slugs := make([]string, 0, len(a.Usage.Running))
	for _, p := range a.Usage.Running {
		slugs = append(slugs, p.Slug+" ("+p.Class+")")
	}
	row("  running memory", fmt.Sprintf("%d of %d GB: %s", a.Usage.RunningGB, a.Plan.MemoryGB, orDash(strings.Join(slugs, ", "))))
	row("  disk held", fmt.Sprintf("%s of %d GB over %d project(s)", fmtGB(a.Usage.DiskHeldBytes), a.Plan.DiskGB, a.Usage.Projects))
	for _, c := range []string{"small", "large", "xl"} {
		if secs := a.Hours[c]; secs > 0 {
			row("  hours "+c, fmt.Sprintf("%.1f", float64(secs)/3600))
		}
	}
	row("  egress", fmt.Sprintf("%.2f GB of %d GB included; hard stop at %d GB", float64(a.Usage.EgressBytes)/(1<<30), a.Plan.EgressGB, a.Plan.EgressHardStopBytes()>>30))
	row("  overage", fmt.Sprintf("ceil(%.2f - %d) = %d GB x %d cents = %d cents", float64(a.Usage.EgressBytes)/(1<<30), a.Plan.EgressGB, a.Usage.OverageGB, OveragePerGBCents, a.Usage.OverageCents))
	for i, o := range a.Overage {
		k := ""
		if i == 0 {
			k = "overage lines"
		}
		row(k, fmt.Sprintf("%s: %d GB, %d cents, sent %s", o.PeriodStart.UTC().Format("2006-01-02"), o.EgressGB, o.Cents, orDash(o.SentRef)))
	}
	return 0, tw.Flush()
}
