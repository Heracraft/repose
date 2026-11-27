package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db/testdb"
)

// A hosted plan includes hosts of the user's own, and a machine on one
// counts nothing against the plan (DECISIONS I-1704): not its memory at a
// start or a create, not its disk, not its egress, while machines on
// repose's servers are measured exactly as before.
func TestHostedPlanIncludesOwnHosts(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	f := newFakePolar()
	defer f.Close()
	g := newGate(pool, f)
	// Solo's 8 GB is used by one large machine on repose's servers.
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	u := user(t, pool, a)
	if n, err := g.HostsAllowed(ctx, pool, u); err != nil || n != billing.UnlimitedHosts {
		t.Fatalf("a Solo account may add %d hosts (%v), want any number", n, err)
	}
	host := store.NewID()
	if _, err := pool.Exec(ctx, `insert into hosts (id, name, state, owner_account_id) values ($1, 'mac-mini', 'ready', $2)`, host, u.ID); err != nil {
		t.Fatal(err)
	}
	// A stopped machine on the Mac holding 200 GB, twice Solo's disk.
	own := store.NewID()
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes, disk_held_bytes, host_id, created_at)
		values ($1, $2, 'own-a', 'own-a', 'large', 'stopped', $3, $3, $4, $5)`, own, u.ID, int64(200)<<30, host, a.Period.Start); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(ctx, u, billing.Request{Class: "large", Project: own}); err != nil {
		t.Fatalf("starting the machine on the Mac: %v", err)
	}
	if err := g.Check(ctx, u, billing.Request{Class: "large", Disk: true, AddHeldBytes: billing.NewProjectHeldBytes, VolumeBytes: 40 << 30, OwnHost: true}); err != nil {
		t.Fatalf("a create placement puts on the Mac: %v", err)
	}
	// On repose's servers the plan holds: the memory is in use...
	if r := refusal(t, g.Check(ctx, u, billing.Request{Class: "small", Disk: true, AddHeldBytes: billing.NewProjectHeldBytes, VolumeBytes: 20 << 30})); r.Reason != billing.ReasonPlanLimit {
		t.Fatalf("a create on repose's servers: %+v, want plan_limit", r)
	}
	// ...and the disk counts only the 40 GB there, not the Mac's 200.
	if err := g.Check(ctx, u, billing.Request{Disk: true, AddHeldBytes: billing.NewProjectHeldBytes, VolumeBytes: 20 << 30}); err != nil {
		t.Fatalf("a stopped create on repose's servers with the Mac's disk left out: %v", err)
	}
	held, n, err := billing.HeldFleetDisk(ctx, pool, u.ID)
	if err != nil || held != 40<<30 || n != 2 {
		t.Fatalf("fleet disk held %d over %d projects (%v), want 40 GB over 2", held, n, err)
	}
	// An hour of 300 GB sent from the Mac is the user's own connection;
	// 1 GB from the machine on repose's servers is the plan's.
	hour := a.Period.Start.Add(5 * time.Hour)
	ensurePartitions(t, pool, hour, hour)
	for m := 0; m < 2; m++ {
		ts := hour.Add(time.Duration(m) * time.Minute)
		if _, err := pool.Exec(ctx, `insert into meter_samples (ts, project_id, host_id, state, class, disk_alloc, net_tx, net_egress, guestd_ok)
			values ($1, $2, $3, 'running', 'large', 1, $4, $4, true)`, ts, own, host, int64(150)<<30); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into meter_samples (ts, project_id, host_id, state, class, disk_alloc, net_tx, net_egress, guestd_ok)
			values ($1, $2, $3, 'running', 'large', 1, $4, $4, true)`, ts, a.ProjectID, uuid.Nil, int64(512)<<20); err != nil {
			t.Fatal(err)
		}
	}
	r := billing.NewRollup(pool, nop(), quiet())
	r.Now = at(hour.Add(2 * time.Hour))
	if _, err := r.Hour(ctx, hour); err != nil {
		t.Fatal(err)
	}
	egress, err := billing.PeriodEgress(ctx, pool, u.ID, a.Period)
	if err != nil || egress != 1<<30 {
		t.Fatalf("period egress %d (%v), want 1 GB, the servers' alone", egress, err)
	}
}
