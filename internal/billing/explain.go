package billing

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/db"
)

// Explanation is every input of one usage_hours row and the period's
// overage arithmetic around it: what `repose-admin billing explain
// <project> <hour>` prints when a user asks about an invoice line.
type Explanation struct {
	ProjectID      uuid.UUID
	Slug           string
	Hour           time.Time
	Samples        int
	RunningSamples int
	RunningSeconds int
	Class          string
	GBAlloc        int64
	EgressBytes    int64
	Gap            bool
	PriceVersion   string
	Period         Period
	Plan           Plan
	PeriodEgress   int64
	OverageGB      int64
	OverageCents   int64
	Charged        *OverageRow
	Found          bool
}

// Explain reads the row and its inputs. A missing row is reported as
// such with the samples the hour has, so "why is there no row" has an
// answer too.
func Explain(ctx context.Context, pool *db.Pool, projectID uuid.UUID, hour time.Time) (Explanation, error) {
	e := Explanation{ProjectID: projectID, Hour: hour.UTC().Truncate(time.Hour)}
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, "select slug, user_id from projects where id = $1", projectID).Scan(&e.Slug, &userID); err != nil {
		return e, err
	}
	if err := pool.QueryRow(ctx, `select count(*), count(*) filter (where state = 'running') from meter_samples where project_id = $1 and ts >= $2 and ts < $3`,
		projectID, e.Hour, e.Hour.Add(time.Hour)).Scan(&e.Samples, &e.RunningSamples); err != nil {
		return e, err
	}
	var version *string
	err := pool.QueryRow(ctx, `select class, running_seconds, gb_alloc, egress_bytes, gap, price_version, period_start, period_end
		from usage_hours where project_id = $1 and hour = $2`, projectID, e.Hour).
		Scan(&e.Class, &e.RunningSeconds, &e.GBAlloc, &e.EgressBytes, &e.Gap, &version, &e.Period.Start, &e.Period.End)
	switch {
	case db.IsNoRows(err):
		e.Period = PeriodFor(time.Time{}, e.Hour)
	case err != nil:
		return e, err
	default:
		e.Found = true
		if version != nil {
			e.PriceVersion = *version
		}
	}
	sub, err := LiveSubscription(ctx, pool, userID)
	if err != nil {
		return e, err
	}
	if sub == nil {
		sub, err = LatestSubscription(ctx, pool, userID)
		if err != nil {
			return e, err
		}
	}
	if !e.Found {
		e.Period = sub.Period(e.Hour)
	}
	e.Plan = sub.PlanFor(e.Period.Start)
	if e.PeriodEgress, err = PeriodEgress(ctx, pool, userID, e.Period); err != nil {
		return e, err
	}
	e.OverageGB, e.OverageCents = OverageCents(e.Plan, e.PeriodEgress)
	if sub != nil {
		var o OverageRow
		var txn *string
		err := pool.QueryRow(ctx, "select period_start, egress_gb::bigint, cents, sent_ref, created_at from overage_charges where subscription_id = $1 and period_start = $2", sub.ID, e.Period.Start).
			Scan(&o.PeriodStart, &o.EgressGB, &o.Cents, &txn, &o.CreatedAt)
		if err == nil {
			if txn != nil {
				o.SentRef = *txn
			}
			e.Charged = &o
		} else if !db.IsNoRows(err) {
			return e, err
		}
	}
	return e, nil
}

// WriteTo prints the explanation.
func (e Explanation) WriteTo(w io.Writer) (int64, error) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	row := func(k, v string) { _, _ = fmt.Fprintf(tw, "%s\t%s\n", k, v) }
	row("project", fmt.Sprintf("%s (%s)", e.Slug, e.ProjectID))
	row("hour", e.Hour.Format(time.RFC3339))
	row("samples", fmt.Sprintf("%d in the hour, %d running (a sample is a minute)", e.Samples, e.RunningSamples))
	if !e.Found {
		row("usage_hours row", "none: the rollup has not reached this hour, or the project did not exist yet")
	} else {
		row("class", e.Class)
		row("running seconds", fmt.Sprintf("%d (%d running samples x 60, capped at 3600)%s", e.RunningSeconds, e.RunningSamples, gapNote(e.Gap)))
		row("disk allocated", fmt.Sprintf("%d GB (the volume size, not what is used)", e.GBAlloc))
		row("egress this hour", fmt.Sprintf("%d bytes (%.3f GB)", e.EgressBytes, float64(e.EgressBytes)/(1<<30)))
		row("price version", e.PriceVersion+" (no hourly price: the plan is charged by Polar, egress over the allowance per period)")
	}
	row("period", e.Period.Start.Format(time.RFC3339)+" to "+e.Period.End.Format(time.RFC3339))
	row("plan", fmt.Sprintf("%s: %d GB egress included, $0.%02d a GB over, machines stop at %d GB", e.Plan.Name, e.Plan.EgressGB, OveragePerGBCents, e.Plan.EgressHardStopBytes()>>30))
	row("period egress", fmt.Sprintf("%.3f GB over every project of the account", float64(e.PeriodEgress)/(1<<30)))
	row("overage", fmt.Sprintf("ceil(%.3f - %d) = %d GB x %d cents = %d cents", float64(e.PeriodEgress)/(1<<30), e.Plan.EgressGB, e.OverageGB, OveragePerGBCents, e.OverageCents))
	if e.Charged != nil {
		row("overage line sent", fmt.Sprintf("%d GB, %d cents, recorded %s, sent %s", e.Charged.EgressGB, e.Charged.Cents, e.Charged.CreatedAt.UTC().Format(time.RFC3339), orDash(e.Charged.SentRef)))
	} else {
		row("overage line sent", "not yet (sent within three hours of the period's next_billed_at)")
	}
	return 0, tw.Flush()
}

func gapNote(gap bool) string {
	if gap {
		return "; GAP: a running project with no samples, counted as not running (under-billed, never estimated)"
	}
	return ""
}
