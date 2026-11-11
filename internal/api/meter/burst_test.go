package meter_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/heracraft/repose/internal/api/meter"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db/testdb"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// A large held at its 6-core ceiling owes 4 core-hours of burst an hour,
// and that is what reaches usage_hours. A sample from a hostd that sends
// no burst figure -- which is every host without the feature -- is null,
// and the hour reads 0 (DECISIONS I-1126, I-1128).
func TestBurstRollsUpIntoUsageHoursAndFallsBackToNone(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := metrics.NewNop()
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	pid, gid := seed(t, pool, "large", day.Add(-time.Hour))
	ing := meter.New(pool, m, log)
	ing.SetNow(func() time.Time { return day })
	// A minute at four cores above the base.
	const perMinute = 4 * 60 * 1_000_000_000
	for minute := 0; minute < 120; minute++ {
		g := &hostdv1.GuestSample{GuestId: gid.String(), State: "running", Class: "large", DiskAllocBytes: 40 << 30,
			Signals: &hostdv1.GuestSignals{GuestdOk: true}}
		if minute < 60 {
			g.BurstCpuNsDelta = proto.Uint64(perMinute)
			g.BurstCapMillicores = 6000
		}
		ing.OnSamples(ctx, testHost, &hostdv1.Samples{Ts: day.Add(time.Duration(minute) * time.Minute).Unix(), Guests: []*hostdv1.GuestSample{g}})
	}
	var withBurst int
	if err := pool.QueryRow(ctx, "select count(burst_cpu_ns) from meter_samples where project_id = $1", pid).Scan(&withBurst); err != nil {
		t.Fatal(err)
	}
	if withBurst != 60 {
		t.Fatalf("samples carrying a burst figure: %d; want 60", withBurst)
	}
	r := billing.NewRollup(pool, m, log)
	r.Now = func() time.Time { return day.Add(3 * time.Hour) }
	for h := 0; h < 2; h++ {
		if _, err := r.Hour(ctx, day.Add(time.Duration(h)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := pool.Query(ctx, "select burst_cpu_ns from usage_hours where project_id = $1 order by hour", pid)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for rows.Next() {
		var ns int64
		_ = rows.Scan(&ns)
		got = append(got, ns)
	}
	rows.Close()
	want := int64(4 * billing.NsPerCoreHour)
	if len(got) != 2 || got[0] != want || got[1] != 0 {
		t.Fatalf("burst by hour %v; want [%d 0]", got, want)
	}
	if ch := billing.BurstCoreHours(got[0]); ch != 4 {
		t.Fatalf("the hour reads %v core-hours; want 4", ch)
	}
	// A re-run of the same hour writes the same figure (the rollup is
	// idempotent, and the overage reads it per period).
	if _, err := r.Hour(ctx, day); err != nil {
		t.Fatal(err)
	}
	var again int64
	if err := pool.QueryRow(ctx, "select burst_cpu_ns from usage_hours where project_id = $1 and hour = $2", pid, day).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != want {
		t.Fatalf("a second rollup of the hour wrote %d; want %d", again, want)
	}
}

// The api clamps a burst figure no controller can have produced, so a
// host that sends nonsense cannot bill an account out of its allowance.
func TestBurstSampleIsClampedAtTheApi(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	pid, gid := seed(t, pool, "large", day.Add(-time.Hour))
	ing := meter.New(pool, metrics.NewNop(), log)
	ing.SetNow(func() time.Time { return day })
	ing.OnSamples(ctx, testHost, &hostdv1.Samples{Ts: day.Unix(), Guests: []*hostdv1.GuestSample{{
		GuestId: gid.String(), State: "running", Class: "large", Signals: &hostdv1.GuestSignals{GuestdOk: true},
		BurstCpuNsDelta: proto.Uint64(1 << 62),
	}}})
	var ns int64
	if err := pool.QueryRow(ctx, "select burst_cpu_ns from meter_samples where project_id = $1", pid).Scan(&ns); err != nil {
		t.Fatal(err)
	}
	if ns != 4*3600*1_000_000_000 {
		t.Fatalf("a huge burst figure was stored as %d; want it clamped to four core-hours", ns)
	}
}
