package billing_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db/testdb"
)

// The rollup writes hours, disk and egress, stamps the subscription's
// period, prices nothing, is idempotent, and records a gap for a running
// project with no samples.
func TestRollupWritesUsageWithoutPrices(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	hour := a.Period.Start.Add(5 * time.Hour)
	ensurePartitions(t, pool, hour, hour)
	for m := 0; m < 30; m++ {
		sample(t, pool, a.ProjectID, hour.Add(time.Duration(m)*time.Minute), "running", "large", 40<<30, 1<<20)
	}
	for m := 30; m < 60; m++ {
		sample(t, pool, a.ProjectID, hour.Add(time.Duration(m)*time.Minute), "stopped", "large", 40<<30, 0)
	}
	// A second project of the account with no samples while running: a gap.
	gapped := addProject(t, pool, a, "gapped", "small", "running", 20<<30)
	// A project of an account with no subscription: calendar month period.
	b := seedAccount(t, pool, "", "none", "small", "stopped")

	r := billing.NewRollup(pool, nop(), quiet())
	r.Now = at(hour.Add(2 * time.Hour))
	rows, err := r.Hour(ctx, hour)
	if err != nil {
		t.Fatal(err)
	}
	byProject := map[string]billing.Row{}
	for _, row := range rows {
		byProject[row.ProjectID.String()] = row
	}
	got := byProject[a.ProjectID.String()]
	if got.RunningSeconds != 1800 || got.EgressBytes != 30<<20 || got.GBAlloc != 40 || got.Class != "large" || got.Gap {
		t.Fatalf("row: %+v", got)
	}
	if got.CostCents != 0 || got.EgressCents != 0 || got.GuestCents != 0 {
		t.Fatalf("plan-v1 prices nothing: %+v", got.Result)
	}
	if !got.Period.Start.Equal(a.Period.Start) || !got.Period.End.Equal(a.Period.End) {
		t.Fatalf("period stamped from the subscription: %+v", got.Period)
	}
	if g := byProject[gapped.String()]; !g.Gap || g.RunningSeconds != 0 || g.GBAlloc != 20 {
		t.Fatalf("gap row: %+v", g)
	}
	if bb := byProject[b.ProjectID.String()]; bb.Period.Start != time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) || bb.Gap {
		t.Fatalf("no-plan project: calendar month, no gap when stopped: %+v", bb)
	}
	var version string
	var cost int64
	if err := pool.QueryRow(ctx, "select price_version, cost_cents from usage_hours where project_id = $1 and hour = $2", a.ProjectID, hour).Scan(&version, &cost); err != nil || version != "plan-v1" || cost != 0 {
		t.Fatalf("stored row: %s %d %v", version, cost, err)
	}
	// Idempotent: run again, same rows, same count.
	again, err := r.Hour(ctx, hour)
	if err != nil || len(again) != len(rows) {
		t.Fatalf("second run: %d rows %v", len(again), err)
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from usage_hours where hour = $1", hour).Scan(&n); err != nil || n != 3 {
		t.Fatalf("%d rows for the hour, want 3", n)
	}
	// Due rolls every hour after the last one up to the previous full hour.
	rolled, err := r.Due(ctx)
	if err != nil || rolled != 1 {
		t.Fatalf("due: %d %v", rolled, err)
	}
	// The period egress the gate reads is the sum.
	egress, err := billing.PeriodEgress(ctx, pool, a.UserID, a.Period)
	if err != nil || egress != 30<<20 {
		t.Fatalf("period egress %d %v", egress, err)
	}
	hours, err := billing.PeriodHours(ctx, pool, a.UserID, a.Period)
	if err != nil || hours["large"] != 1800 {
		t.Fatalf("period hours %v %v", hours, err)
	}
}

// LoadAccount and Explain print the overage arithmetic the runbook reads.
func TestAccountAndExplain(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	hour := a.Period.Start.Add(3 * time.Hour)
	usageHour(t, pool, a.ProjectID, hour, "large", 3600, 260<<30, a.Period)
	acct, err := billing.LoadAccount(ctx, pool, a.UserID, hour)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Plan.ID != "solo" || acct.Usage.RunningGB != 8 || acct.Usage.OverageGB != 10 || acct.Usage.OverageCents != 50 || acct.Hours["large"] != 3600 {
		t.Fatalf("account: %+v", acct.Usage)
	}
	var out bytes.Buffer
	if _, err := acct.WriteTo(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{a.SubID + " solo active (1 seat(s))", "ceil(260.00 - 250) = 10 GB x 5 cents = 50 cents", "running memory", "8 of 8 GB"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("billing show lacks %q:\n%s", want, out.String())
		}
	}
	ex, err := billing.Explain(ctx, pool, a.ProjectID, hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ex.Found || ex.RunningSeconds != 3600 || ex.OverageCents != 50 || ex.Charged != nil {
		t.Fatalf("explain: %+v", ex)
	}
	out.Reset()
	if _, err := ex.WriteTo(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"no hourly price", "ceil(260.000 - 250) = 10 GB x 5 cents = 50 cents", "not yet (sent within three hours"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("explain lacks %q:\n%s", want, out.String())
		}
	}
	// A missing row says so.
	ex, err = billing.Explain(ctx, pool, a.ProjectID, hour.Add(time.Hour))
	if err != nil || ex.Found {
		t.Fatalf("missing hour: %+v %v", ex.Found, err)
	}
}

// A webhook reply is the project's egress and no sample holds it
// (DECISIONS I-1120): it leaves the guest on port 22, where I-1063's nft
// meter reads it as a terminal's output, so the edge counts it and the
// rollup adds the hour's bytes to what the samples said.
func TestRollupAddsWebhookReplyBytesToEgress(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	hour := a.Period.Start.Add(5 * time.Hour)
	ensurePartitions(t, pool, hour, hour)
	for m := 0; m < 10; m++ {
		sample(t, pool, a.ProjectID, hour.Add(time.Duration(m)*time.Minute), "running", "large", 40<<30, 1<<20)
	}
	r := billing.NewRollup(pool, nop(), quiet())
	r.Now = at(hour.Add(2 * time.Hour))

	// The probe: without a webhook_egress row the hour is the samples'
	// 10 MiB, so the difference below is the webhook bytes and not a
	// column that was always there.
	rows, err := r.Hour(ctx, hour)
	if err != nil {
		t.Fatal(err)
	}
	base := rowFor(t, rows, a.ProjectID.String())
	if base.EgressBytes != 10<<20 {
		t.Fatalf("samples alone: %d bytes, want %d", base.EgressBytes, 10<<20)
	}

	if _, err := pool.Exec(ctx, `insert into webhook_egress (project_id, hour, bytes) values ($1, $2, $3)`,
		a.ProjectID, hour, int64(4096)); err != nil {
		t.Fatal(err)
	}
	rows, err = r.Hour(ctx, hour)
	if err != nil {
		t.Fatal(err)
	}
	got := rowFor(t, rows, a.ProjectID.String())
	if got.EgressBytes != (10<<20)+4096 {
		t.Fatalf("with webhook replies: %d bytes, want %d", got.EgressBytes, (10<<20)+4096)
	}
	// An hour the project sent nothing itself still bills its replies.
	next := hour.Add(time.Hour)
	ensurePartitions(t, pool, next, next)
	if _, err := pool.Exec(ctx, `insert into webhook_egress (project_id, hour, bytes) values ($1, $2, $3)`,
		a.ProjectID, next, int64(900)); err != nil {
		t.Fatal(err)
	}
	r.Now = at(next.Add(2 * time.Hour))
	rows, err = r.Hour(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if n := rowFor(t, rows, a.ProjectID.String()).EgressBytes; n != 900 {
		t.Fatalf("an hour with replies and no samples: %d bytes, want 900", n)
	}
}

// rowFor is the one row of the project, or a failure naming what came back.
func rowFor(t *testing.T, rows []billing.Row, projectID string) billing.Row {
	t.Helper()
	for _, row := range rows {
		if row.ProjectID.String() == projectID {
			return row
		}
	}
	t.Fatalf("no row for %s in %d rows", projectID, len(rows))
	return billing.Row{}
}
