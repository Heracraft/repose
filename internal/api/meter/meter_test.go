package meter_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/heracraft/repose/internal/api/meter"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

// testHost is the host every seeded project is placed on; samples count
// only from the host a project is on.
var testHost = uuid.MustParse("01920000-0000-7000-8000-0000000000aa")

func seed(t *testing.T, pool *db.Pool, class string, created time.Time) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid, pid, gid := store.NewID(), store.NewID(), store.NewID()
	// The billing period is anchored at signup (09-billing.md §5.1), so the
	// anchor is set explicitly and the period is the 720 hours from it.
	if _, err := pool.Exec(ctx, "insert into users (id, handle, created_at, billing_anchor) values ($1, $2, $3, $3)", uid, "u"+uid.String()[24:], created); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into hosts (id, name, state) values ($1, $2, 'ready') on conflict do nothing", testHost, "meter-test-host"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes, guest_id, created_at, host_id) values ($1, $2, 'todo', $3, $4, 'running', $5, $6, $7, $8)", pid, uid, "s"+pid.String()[24:], class, int64(40)<<30, gid, created, testHost); err != nil {
		t.Fatal(err)
	}
	return pid, gid
}

// One large guest running 10 hours of a day with 40 GB and 3 GB egress:
// 36000 running seconds, 3 GB of egress, and no cents anywhere (plan-v1,
// DECISIONS I-289: the plan is charged by Paddle, not the hour).
func TestIngestAndSyntheticDayRollup(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := metrics.NewNop()
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	pid, gid := seed(t, pool, "large", day.Add(-time.Hour))
	ing := meter.New(pool, m, log)
	ing.SetNow(func() time.Time { return day })
	hostID := testHost
	egressPerMinute := int64(3<<30) / 600 // 3 GB spread over the 600 running minutes
	for minute := 0; minute < 24*60; minute++ {
		ts := day.Add(time.Duration(minute) * time.Minute)
		running := minute >= 8*60 && minute < 18*60
		state, tx := "stopped", int64(0)
		if running {
			state, tx = "running", egressPerMinute
		}
		ing.OnSamples(ctx, hostID, &hostdv1.Samples{Ts: ts.Unix(), Guests: []*hostdv1.GuestSample{{
			GuestId: gid.String(), State: state, Class: "large", DiskAllocBytes: 40 << 30, DiskUsedBytes: 6 << 30, NetTxBytesDelta: uint64(tx),
			Signals: &hostdv1.GuestSignals{SshSessions: 1, TmuxClients: 1, GuestdOk: true, Agents: []*hostdv1.AgentProc{{Agent: "claude", TmuxWindow: "claude", State: "working"}}},
			Procs:   []*hostdv1.ProcSample{{Comm: "claude", CpuNsDelta: 1e9, RssBytes: 1 << 30}, {Comm: "node", CpuNsDelta: 2e8, RssBytes: 1 << 20}},
		}}})
	}
	var samples, procs int
	_ = pool.QueryRow(ctx, "select count(*) from meter_samples where project_id = $1", pid).Scan(&samples)
	_ = pool.QueryRow(ctx, "select count(*) from proc_samples where project_id = $1", pid).Scan(&procs)
	if samples != 1440 || procs != 2880 {
		t.Fatalf("samples=%d procs=%d", samples, procs)
	}
	// A re-sent message is harmless.
	ing.OnSamples(ctx, hostID, &hostdv1.Samples{Ts: day.Unix(), Guests: []*hostdv1.GuestSample{{GuestId: gid.String(), State: "stopped", Class: "large"}}})
	_ = pool.QueryRow(ctx, "select count(*) from meter_samples where project_id = $1", pid).Scan(&samples)
	if samples != 1440 {
		t.Fatalf("duplicate sample inserted: %d", samples)
	}
	latest, ok, err := meter.LatestSample(ctx, pool, pid)
	if err != nil || !ok {
		t.Fatalf("latest: %v %v", ok, err)
	}
	_ = latest
	r := billing.NewRollup(pool, m, log)
	r.Now = func() time.Time { return day.Add(25 * time.Hour) }
	var rows []billing.Row
	for h := 0; h < 24; h++ {
		out, err := r.Hour(ctx, day.Add(time.Duration(h)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, out...)
	}
	if len(rows) != 24 {
		t.Fatalf("rows %d", len(rows))
	}
	var running, guestCents, storageCents, egressCents, cost, egress int64
	if err := pool.QueryRow(ctx, "select sum(running_seconds), sum(guest_cents), sum(storage_cents), sum(egress_cents), sum(cost_cents), sum(egress_bytes) from usage_hours where project_id = $1", pid).Scan(&running, &guestCents, &storageCents, &egressCents, &cost, &egress); err != nil {
		t.Fatal(err)
	}
	if running != 10*3600 {
		t.Fatalf("running seconds %d", running)
	}
	if guestCents != 0 || storageCents != 0 || egressCents != 0 || cost != 0 {
		t.Fatalf("plan-v1 rows carry no price: guest=%d storage=%d egress=%d cost=%d", guestCents, storageCents, egressCents, cost)
	}
	if egress < 3<<30-1000 || egress > 3<<30 {
		t.Fatalf("egress bytes %d", egress)
	}
	// Re-running an hour changes nothing.
	if _, err := r.Hour(ctx, day.Add(9*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var running2 int64
	_ = pool.QueryRow(ctx, "select sum(running_seconds) from usage_hours where project_id = $1", pid).Scan(&running2)
	if running2 != running {
		t.Fatalf("rollup not idempotent: %d then %d", running, running2)
	}
	// A running project with no samples for an hour is a gap with zeros.
	pid2, _ := seed(t, pool, "small", day)
	out, err := r.Hour(ctx, day.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range out {
		if row.ProjectID == pid2 {
			found = true
			if !row.Gap || row.RunningSeconds != 0 || row.GuestCents != 0 {
				t.Fatalf("gap row %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("project without samples missing from the rollup")
	}
	// Due rolls up everything up to the previous full hour.
	pool2 := testdb.Open(t)
	ing2 := meter.New(pool2, m, log)
	_, gid2 := seed(t, pool2, "xl", day)
	ing2.OnSamples(ctx, hostID, &hostdv1.Samples{Ts: day.Add(30 * time.Minute).Unix(), Guests: []*hostdv1.GuestSample{{GuestId: gid2.String(), State: "running", Class: "xl"}}})
	r2 := billing.NewRollup(pool2, m, log)
	r2.Now = func() time.Time { return day.Add(3*time.Hour + 5*time.Minute) }
	n, err := r2.Due(ctx)
	if err != nil || n != 3 {
		t.Fatalf("due rolled %d hours: %v", n, err)
	}
	if n, _ := r2.Due(ctx); n != 0 {
		t.Fatalf("second due rolled %d", n)
	}
}

// TestOneGuestsSampleCannotSinkTheHostsBatch: guest-written names Postgres
// would refuse are stored cleaned; a row that still fails costs only its
// own guest; a guest of a project on another host is not stored at all.
func TestOneGuestsSampleCannotSinkTheHostsBatch(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	m := metrics.NewNop()
	ing := meter.New(pool, m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now().UTC().Truncate(time.Second)
	ing.SetNow(func() time.Time { return now })
	pidA, gidA := seed(t, pool, "large", now.Add(-time.Hour))
	pidB, gidB := seed(t, pool, "large", now.Add(-time.Hour))
	pidC, gidC := seed(t, pool, "large", now.Add(-time.Hour))
	pidD, gidD := seed(t, pool, "large", now.Add(-time.Hour))
	if _, err := pool.Exec(ctx, "update projects set host_id = null where id = $1", pidD); err != nil {
		t.Fatal(err)
	}
	ing.OnSamples(ctx, testHost, &hostdv1.Samples{Ts: now.Unix(), Guests: []*hostdv1.GuestSample{
		{GuestId: gidA.String(), State: "running", Class: "large", NetTxBytesDelta: 100,
			Signals: &hostdv1.GuestSignals{GuestdOk: true, Agents: []*hostdv1.AgentProc{{Agent: "cl\x00aude", TmuxWindow: "w\x00", State: "idle"}}},
			Procs:   []*hostdv1.ProcSample{{Comm: "xm\x00rig", CpuNsDelta: 1}}},
		// A field no cleaning covers fails this guest's insert only.
		{GuestId: gidB.String(), State: "running", Class: "lar\x00ge", NetTxBytesDelta: 200},
		{GuestId: gidC.String(), State: "running", Class: "large", NetTxBytesDelta: 300},
		{GuestId: gidD.String(), State: "running", Class: "large", NetTxBytesDelta: 400},
	}})
	tx := func(pid uuid.UUID) int64 {
		var n int64 = -1
		_ = pool.QueryRow(ctx, "select net_tx from meter_samples where project_id = $1", pid).Scan(&n)
		return n
	}
	if tx(pidA) != 100 || tx(pidC) != 300 {
		t.Fatalf("clean guests lost their rows: A %d, C %d", tx(pidA), tx(pidC))
	}
	if tx(pidB) != -1 || tx(pidD) != -1 {
		t.Fatalf("rows that should not be stored: B %d, D %d", tx(pidB), tx(pidD))
	}
	var comm string
	_ = pool.QueryRow(ctx, "select comm from proc_samples where project_id = $1", pidA).Scan(&comm)
	if comm != "xmrig" {
		t.Fatalf("comm %q", comm)
	}
	if got := testutil.ToFloat64(m.SamplesFailed.WithLabelValues("insert")); got != 1 {
		t.Fatalf("insert failures %v", got)
	}
	if got := testutil.ToFloat64(m.HostReportsRefused.WithLabelValues("foreign_guest")); got != 1 {
		t.Fatalf("foreign guests %v", got)
	}
}
