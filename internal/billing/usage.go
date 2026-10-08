package billing

import (
	"context"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// The states that hold a plan's memory (PRICING.md "Memory": running, and
// the ones on the way to running).
const memoryStates = "'running','starting','restoring','creating','building'"

// RunningProject is one machine using the plan's memory.
type RunningProject struct {
	ID    uuid.UUID
	Slug  string
	Class string
	State string
}

// RunningMemory lists the user's machines that hold memory, with the GB
// they take; except excludes the project being acted on (a restart of a
// running project does not count itself twice).
func RunningMemory(ctx context.Context, q store.Querier, userID uuid.UUID, except uuid.UUID) ([]RunningProject, int, error) {
	rows, err := q.Query(ctx, "select id, slug, class, state from projects where user_id = $1 and destroyed_at is null and state in ("+memoryStates+") and id <> $2 order by created_at, slug", userID, except)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []RunningProject
	gb := 0
	for rows.Next() {
		var p RunningProject
		if err := rows.Scan(&p.ID, &p.Slug, &p.Class, &p.State); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
		gb += ClassMemoryGB(p.Class)
	}
	return out, gb, rows.Err()
}

// HeldSQL is the bytes a projects row counts against the plan's disk
// (DECISIONS I-585): the bytes its volume holds, measured or estimated,
// else the volume's size. store.Project.HeldBytes is the same in Go.
const HeldSQL = "coalesce(disk_held_bytes, volume_bytes)"

// NewProjectHeldBytes is what a new project's volume is counted at until
// its first sample: a fresh volume held 0.14 to 1.4 GB in its first
// minute on prod (40 creates, 2026-10-07), most under 0.5 GB.
const NewProjectHeldBytes int64 = 1 << 30

// HeldDisk is the bytes the user's live projects' volumes hold, and how
// many projects that is. A project being destroyed is left out, as it
// is from the project count (DECISIONS I-300): its volume goes when the
// destroy finishes. The disk size of each is only the ceiling it may
// grow to and does not count (I-585).
func HeldDisk(ctx context.Context, q store.Querier, userID uuid.UUID) (int64, int, error) {
	var bytes int64
	var n int
	err := q.QueryRow(ctx, "select coalesce(sum("+HeldSQL+"), 0)::bigint, count(*) from projects where user_id = $1 and destroyed_at is null and state <> 'destroying'", userID).Scan(&bytes, &n)
	return bytes, n, err
}

// PeriodEgress sums the egress bytes of the user's projects over the
// period's hours as usage_hours recorded them (destroyed projects
// included: their bytes were sent).
func PeriodEgress(ctx context.Context, q store.Querier, userID uuid.UUID, p Period) (int64, error) {
	var bytes int64
	err := q.QueryRow(ctx, `select coalesce(sum(u.egress_bytes), 0) from usage_hours u join projects p on p.id = u.project_id
		where p.user_id = $1 and u.hour >= $2 and u.hour < $3`, userID, p.Start, p.End).Scan(&bytes)
	return bytes, err
}

// PeriodHours sums the running seconds per class over the period.
func PeriodHours(ctx context.Context, q store.Querier, userID uuid.UUID, p Period) (map[string]int64, error) {
	rows, err := q.Query(ctx, `select u.class, coalesce(sum(u.running_seconds), 0) from usage_hours u join projects p on p.id = u.project_id
		where p.user_id = $1 and u.hour >= $2 and u.hour < $3 group by u.class`, userID, p.Start, p.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var class string
		var secs int64
		if err := rows.Scan(&class, &secs); err != nil {
			return nil, err
		}
		out[class] = secs
	}
	return out, rows.Err()
}

// GBCeil rounds bytes up to whole GB.
func GBCeil(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + (1<<30 - 1)) >> 30
}

// Usage is the `usage` block of GET /billing and what the gate and the
// overage job read: this period's memory, disk and egress against the plan.
type Usage struct {
	Plan             Plan
	Period           Period
	RunningGB        int
	Running          []RunningProject
	DiskHeldBytes    int64
	Projects         int
	EgressBytes      int64
	EgressIncludedGB int
	OverageGB        int64
	OverageCents     int64
}

// LoadUsage reads one user's usage for a plan and period.
func LoadUsage(ctx context.Context, q store.Querier, userID uuid.UUID, plan Plan, p Period) (Usage, error) {
	u := Usage{Plan: plan, Period: p, EgressIncludedGB: plan.EgressGB}
	var err error
	if u.Running, u.RunningGB, err = RunningMemory(ctx, q, userID, uuid.Nil); err != nil {
		return u, err
	}
	if u.DiskHeldBytes, u.Projects, err = HeldDisk(ctx, q, userID); err != nil {
		return u, err
	}
	if u.EgressBytes, err = PeriodEgress(ctx, q, userID, p); err != nil {
		return u, err
	}
	u.OverageGB, u.OverageCents = OverageCents(plan, u.EgressBytes)
	return u, nil
}

// DiskHeldGB is the held bytes in GB to one decimal, as /billing shows
// them.
func (u Usage) DiskHeldGB() float64 { return gbTenths(u.DiskHeldBytes) }

// DiskOver reports whether the projects hold more than the plan's disk.
func (u Usage) DiskOver() bool { return u.DiskHeldBytes > int64(u.Plan.DiskGB)<<30 }

// gbTenths is bytes in GB rounded up to a tenth, so a figure over the
// plan never reads as equal to it.
func gbTenths(bytes int64) float64 {
	if bytes <= 0 {
		return 0
	}
	return float64((bytes*10+(1<<30-1))>>30) / 10
}

// EgressStopped reports whether the period's egress passed the plan's
// hard stop (PRICING.md "Egress": four times the allowance).
func (u Usage) EgressStopped() bool { return u.EgressBytes >= u.Plan.EgressHardStopBytes() }

// JSON is the `usage` object of GET /billing (api.md).
func (u Usage) JSON() map[string]any {
	return map[string]any{
		"running_gb": u.RunningGB, "memory_gb": u.Plan.MemoryGB,
		// disk_allocated_gb carries the held figure for one release, so an
		// older client compares the number the gate does (I-585).
		"disk_held_gb": u.DiskHeldGB(), "disk_allocated_gb": GBCeil(u.DiskHeldBytes), "disk_gb": u.Plan.DiskGB,
		"egress_gb": float64(u.EgressBytes) / (1 << 30), "egress_included_gb": u.EgressIncludedGB,
		"overage_cents": u.OverageCents, "projects": u.Projects, "project_limit": ProjectCap,
		"period_start": u.Period.Start, "period_end": u.Period.End,
	}
}
