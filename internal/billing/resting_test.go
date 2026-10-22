package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

// TestARestingMinuteStillBills is the §5 checklist item of the idle pause
// (DECISIONS I-818): a frozen machine holds its memory, so its minutes
// are running minutes. `resting` is a marker on the sample and `state`
// stays `running`, which is the term the rollup counts; a state string of
// its own would have taken the minutes out of usage_hours with nothing
// anywhere saying so.
func TestARestingMinuteStillBills(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	hour := a.Period.Start.Add(6 * time.Hour)
	ensurePartitions(t, pool, hour, hour)
	// Half the hour busy, half of it frozen. Every minute is a running
	// minute.
	for m := 0; m < 30; m++ {
		sample(t, pool, a.ProjectID, hour.Add(time.Duration(m)*time.Minute), "running", "large", 40<<30, 1<<20)
	}
	for m := 30; m < 60; m++ {
		restingSample(t, pool, a.ProjectID, hour.Add(time.Duration(m)*time.Minute), "large", 40<<30)
	}
	r := billing.NewRollup(pool, nop(), quiet())
	r.Now = at(hour.Add(2 * time.Hour))
	rows, err := r.Hour(ctx, hour)
	if err != nil {
		t.Fatal(err)
	}
	var got billing.Row
	for _, row := range rows {
		if row.ProjectID == a.ProjectID {
			got = row
		}
	}
	if got.RunningSeconds != 3600 {
		t.Fatalf("running_seconds %d, want 3600: a frozen minute is a running minute", got.RunningSeconds)
	}
	if got.Gap {
		t.Fatal("a frozen hour was recorded as a gap")
	}
}

// restingSample writes the minute hostd sends for a paused guest: the
// state stays running, the marker is set, and guestd_ok stays true
// because guestd is paused with its guest and not lost.
func restingSample(t *testing.T, pool *db.Pool, projectID uuid.UUID, ts time.Time, class string, diskAlloc int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into meter_samples (ts, project_id, host_id, state, class, disk_alloc, net_tx, guestd_ok, resting)
		values ($1, $2, $3, 'running', $4, $5, 0, true, true) on conflict do nothing`,
		ts.UTC(), projectID, uuid.Nil, class, diskAlloc); err != nil {
		t.Fatalf("resting sample at %s: %v", ts, err)
	}
}
