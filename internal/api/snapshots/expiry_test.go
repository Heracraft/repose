package snapshots_test

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/snapshots"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/blob"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func project(t *testing.T, pool *db.Pool, destroyed bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	uid := store.NewID()
	pid := store.NewID()
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, $2)", uid, "u"+uid.String()[24:]); err != nil {
		t.Fatal(err)
	}
	var d *time.Time
	st := "running"
	if destroyed {
		now := time.Now()
		d = &now
		st = "destroyed"
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes, destroyed_at) values ($1, $2, $3, $3, 'large', $4, 1, $5)", pid, uid, "p"+pid.String()[24:], st, d); err != nil {
		t.Fatal(err)
	}
	return pid
}

func snap(t *testing.T, pool *db.Pool, b *blob.Fake, pid uuid.UUID, age time.Duration, expires *time.Time) uuid.UUID {
	t.Helper()
	id := store.NewID()
	path := pid.String() + "/" + id.String() + ".img.zst"
	if _, err := pool.Exec(context.Background(), "insert into snapshots (id, project_id, blob_path, bytes, reason, taken_at, expires_at) values ($1, $2, $3, 1, 'scheduled', $4, $5)", id, pid, path, time.Now().Add(-age), expires); err != nil {
		t.Fatal(err)
	}
	b.Put(path, 1)
	return id
}

func TestExpiryRules(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	b := blob.New()
	e := snapshots.New(pool, b, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	live := project(t, pool, false)
	old1 := snap(t, pool, b, live, 10*24*time.Hour, nil)
	old2 := snap(t, pool, b, live, 8*24*time.Hour, nil)
	fresh := snap(t, pool, b, live, 2*24*time.Hour, nil)
	// A long-stopped project keeps its only, old snapshot forever.
	lonely := project(t, pool, false)
	keep := snap(t, pool, b, lonely, 90*24*time.Hour, nil)
	// A destroyed project's snapshot goes at expires_at.
	gone := project(t, pool, true)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(20 * 24 * time.Hour)
	expired := snap(t, pool, b, gone, 40*24*time.Hour, &past)
	notYet := snap(t, pool, b, gone, 40*24*time.Hour, &future)
	// A snapshot under a running restore is never touched.
	restoring := project(t, pool, false)
	held := snap(t, pool, b, restoring, 30*24*time.Hour, nil)
	_ = snap(t, pool, b, restoring, time.Hour, nil)
	opID := store.NewID()
	if _, err := pool.Exec(ctx, "insert into ops (id, project_id, kind, state, params) values ($1, $2, 'restore', 'running', '{}')", opID, restoring); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update snapshots set restoring_op_id = $2 where id = $1", held, opID); err != nil {
		t.Fatal(err)
	}
	deleted, err := e.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]bool{old1: true, old2: true, expired: true}
	if len(deleted) != len(want) {
		t.Fatalf("deleted %v", deleted)
	}
	for _, d := range deleted {
		if !want[d] {
			t.Fatalf("deleted %s which should stay", d)
		}
	}
	for _, id := range []uuid.UUID{fresh, keep, notYet, held} {
		s, _ := store.GetSnapshot(ctx, pool, id)
		if s.DeletedAt != nil || !b.Exists(s.BlobPath) {
			t.Fatalf("snapshot %s should survive", id)
		}
	}
	for _, id := range []uuid.UUID{old1, old2, expired} {
		s, _ := store.GetSnapshot(ctx, pool, id)
		if s.DeletedAt == nil || b.Exists(s.BlobPath) {
			t.Fatalf("snapshot %s should be gone", id)
		}
	}
	// Blob failure leaves the row undeleted for the next run.
	late := snap(t, pool, b, live, 9*24*time.Hour, nil)
	b.Fail = blob.ErrDown
	if _, err := e.Once(ctx); err == nil {
		t.Fatal("expected a blob error")
	}
	s, _ := store.GetSnapshot(ctx, pool, late)
	if s.DeletedAt != nil {
		t.Fatal("row marked deleted although the blob delete failed")
	}
	b.Fail = nil
	if d, err := e.Once(ctx); err != nil || len(d) != 1 || d[0] != late {
		t.Fatalf("retry: %v %v", d, err)
	}
	e.UpdateAgeGauge(ctx)
}

// One blob the store refuses (production 2026-09-21: 403 on the first
// delete) must not stop the sweep: the rest are deleted in the same run,
// the refused row stays for the next one, and the run reports the failure
// (I-130).
func TestExpiryContinuesPastOneFailedBlob(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	b := blob.New()
	e := snapshots.New(pool, b, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	live := project(t, pool, false)
	stuck := snap(t, pool, b, live, 10*24*time.Hour, nil)
	old := snap(t, pool, b, live, 8*24*time.Hour, nil)
	_ = snap(t, pool, b, live, time.Hour, nil)
	s, _ := store.GetSnapshot(ctx, pool, stuck)
	b.FailPaths = map[string]error{s.BlobPath: blob.ErrDown}

	deleted, err := e.Once(ctx)
	if err == nil {
		t.Fatal("a refused blob must make the run report failure")
	}
	if len(deleted) != 1 || deleted[0] != old {
		t.Fatalf("the other expired snapshot should have gone in the same run: %v", deleted)
	}
	if s, _ := store.GetSnapshot(ctx, pool, stuck); s.DeletedAt != nil || !b.Exists(s.BlobPath) {
		t.Fatal("refused row marked deleted or blob gone")
	}
	if s, _ := store.GetSnapshot(ctx, pool, old); s.DeletedAt == nil || b.Exists(s.BlobPath) {
		t.Fatal("the deletable snapshot survived")
	}

	b.FailPaths = nil
	if d, err := e.Once(ctx); err != nil || len(d) != 1 || d[0] != stuck {
		t.Fatalf("retry: %v %v", d, err)
	}
}

// A fork's own snapshot (reason fork, I-703) goes at its expires_at even
// on a live project and even when it is the newest, and it never counts
// as the newest: the project's last other snapshot stays.
func TestForkSnapshotExpires(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	b := blob.New()
	e := snapshots.New(pool, b, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	live := project(t, pool, false)
	last := snap(t, pool, b, live, 30*24*time.Hour, nil) // the project's only other snapshot, old
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)
	forkGone := snap(t, pool, b, live, 25*time.Hour, &past)
	forkKept := snap(t, pool, b, live, time.Hour, &future)
	if _, err := pool.Exec(ctx, "update snapshots set reason = 'fork' where id = any($1)", []uuid.UUID{forkGone, forkKept}); err != nil {
		t.Fatal(err)
	}
	deleted, err := e.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != forkGone {
		t.Fatalf("deleted %v, want only the expired fork snapshot %s", deleted, forkGone)
	}
	for _, id := range []uuid.UUID{last, forkKept} {
		if s, _ := store.GetSnapshot(ctx, pool, id); s.DeletedAt != nil {
			t.Fatalf("snapshot %s should survive", id)
		}
	}
}

// A project keeps seven snapshots, which is the number R4-11 and every
// public line promise. Only the seven-day window was enforced, so a
// project stopped ten times a day held seventy full images: `stop` is
// the one snapshot a user can fire at will (I-900).
func TestLiveProjectKeepsSeven(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	b := blob.New()
	e := snapshots.New(pool, b, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	busy := project(t, pool, false)
	// Twenty stops inside one day, newest first by age.
	var ids []uuid.UUID
	for i := 20; i >= 1; i-- {
		ids = append(ids, snap(t, pool, b, busy, time.Duration(i)*time.Hour, nil))
	}
	// A project with fewer than seven loses none of them.
	calm := project(t, pool, false)
	var calmIDs []uuid.UUID
	for i := 3; i >= 1; i-- {
		calmIDs = append(calmIDs, snap(t, pool, b, calm, time.Duration(i)*time.Hour, nil))
	}
	if _, err := e.Once(ctx); err != nil {
		t.Fatal(err)
	}
	// The newest seven of the twenty survive; the thirteen behind them go.
	newest := ids[len(ids)-snapshots.MaxPerProject:]
	older := ids[:len(ids)-snapshots.MaxPerProject]
	for _, id := range newest {
		s, _ := store.GetSnapshot(ctx, pool, id)
		if s.DeletedAt != nil || !b.Exists(s.BlobPath) {
			t.Fatalf("snapshot %s is within the newest %d and should survive", id, snapshots.MaxPerProject)
		}
	}
	for _, id := range older {
		s, _ := store.GetSnapshot(ctx, pool, id)
		if s.DeletedAt == nil || b.Exists(s.BlobPath) {
			t.Fatalf("snapshot %s is past the cap and should be gone", id)
		}
	}
	for _, id := range calmIDs {
		s, _ := store.GetSnapshot(ctx, pool, id)
		if s.DeletedAt != nil || !b.Exists(s.BlobPath) {
			t.Fatalf("snapshot %s is one of three and should survive", id)
		}
	}
	// The sweep is idempotent: a second run has nothing left to take.
	if d, err := e.Once(ctx); err != nil || len(d) != 0 {
		t.Fatalf("second run: %v %v", d, err)
	}
}

// A fork's snapshot does not count toward the seven and is not retired
// by the cap: it expires on its own expires_at (I-703, I-900).
func TestForkSnapshotIsNotOneOfTheSeven(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	b := blob.New()
	e := snapshots.New(pool, b, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	pid := project(t, pool, false)
	for i := snapshots.MaxPerProject; i >= 1; i-- {
		snap(t, pool, b, pid, time.Duration(i)*time.Hour, nil)
	}
	future := time.Now().Add(20 * 24 * time.Hour)
	forkSnap := snap(t, pool, b, pid, 30*time.Hour, &future)
	if _, err := pool.Exec(ctx, "update snapshots set reason = 'fork' where id = $1", forkSnap); err != nil {
		t.Fatal(err)
	}
	if d, err := e.Once(ctx); err != nil || len(d) != 0 {
		t.Fatalf("seven dailies and a fork snapshot: deleted %v (%v)", d, err)
	}
	s, _ := store.GetSnapshot(ctx, pool, forkSnap)
	if s.DeletedAt != nil || !b.Exists(s.BlobPath) {
		t.Fatal("the fork's snapshot was retired by the count cap")
	}
}

// TestSnapshotAgeCountsFromTheLaterOfSnapshotAndStart (I-1244): a project
// that sat stopped for days and started an hour ago is an hour stale,
// not days; a running project with no snapshot counts from its start;
// and the gauge is the worst case over running projects. Probe: with the
// old query (min of the newest snapshot alone) the first check reads
// about 4 days, and the second reads 0 because a project with no
// snapshot was not counted.
func TestSnapshotAgeCountsFromTheLaterOfSnapshotAndStart(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	b := blob.New()
	m := metrics.New(prometheus.NewRegistry())
	e := snapshots.New(pool, b, m, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	restarted := project(t, pool, false)
	snap(t, pool, b, restarted, 4*24*time.Hour, nil) // the stop's snapshot, four days ago
	if _, err := pool.Exec(ctx, "update projects set started_at = now() - interval '1 hour' where id = $1", restarted); err != nil {
		t.Fatal(err)
	}
	e.UpdateAgeGauge(ctx)
	if got := testutil.ToFloat64(m.SnapshotAgeSeconds); got < 3500 || got > 3700 {
		t.Fatalf("age after a restart = %.0fs, want about an hour", got)
	}
	// Never snapshotted, running for two hours: the worst case now.
	fresh := project(t, pool, false)
	if _, err := pool.Exec(ctx, "update projects set started_at = now() - interval '2 hours' where id = $1", fresh); err != nil {
		t.Fatal(err)
	}
	e.UpdateAgeGauge(ctx)
	if got := testutil.ToFloat64(m.SnapshotAgeSeconds); got < 7100 || got > 7300 {
		t.Fatalf("age with an unsnapshotted project = %.0fs, want about two hours", got)
	}
	// A snapshot newer than the start counts from the snapshot.
	snap(t, pool, b, fresh, 10*time.Minute, nil)
	e.UpdateAgeGauge(ctx)
	if got := testutil.ToFloat64(m.SnapshotAgeSeconds); got < 3500 || got > 3700 {
		t.Fatalf("age once the fresh project is snapshotted = %.0fs, want the restarted project's hour", got)
	}
}

// hostDeletes records the deletes the expiry sends to hosts.
type hostDeletes struct {
	away map[uuid.UUID]bool
	sent map[uuid.UUID][]string
}

func (h *hostDeletes) DeleteOnHost(_ context.Context, host uuid.UUID, path string) error {
	if h.away[host] {
		return snapshots.ErrHostAway
	}
	h.sent[host] = append(h.sent[host], path)
	return nil
}

// An owned host's snapshot lives on that host's disk (I-1372), so its
// expiry is a DeleteSnapshot to that host, not a Blob delete (I-1636). A
// host that is away keeps the row for the next run without failing this
// one; a host that was removed took the file with it.
func TestOwnedHostSnapshotsExpireOnTheirHost(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	b := blob.New()
	hd := &hostDeletes{away: map[uuid.UUID]bool{}, sent: map[uuid.UUID][]string{}}
	e := snapshots.New(pool, b, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	e.Hosts = hd
	live := project(t, pool, false)
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, "select user_id from projects where id = $1", live).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	mac, away, fleet := store.NewID(), store.NewID(), store.NewID()
	for _, h := range []struct {
		id    uuid.UUID
		owner *uuid.UUID
	}{{mac, &owner}, {away, &owner}, {fleet, nil}} {
		if _, err := pool.Exec(ctx, "insert into hosts (id, name, state, owner_account_id) values ($1, $2, 'ready', $3)", h.id, "h"+h.id.String()[24:], h.owner); err != nil {
			t.Fatal(err)
		}
	}
	hd.away[away] = true
	onHost := func(age time.Duration, host uuid.UUID) uuid.UUID {
		id := snap(t, pool, b, live, age, nil)
		if _, err := pool.Exec(ctx, "update snapshots set host_id = $2 where id = $1", id, host); err != nil {
			t.Fatal(err)
		}
		return id
	}
	onMac := onHost(10*24*time.Hour, mac)
	onAway := onHost(9*24*time.Hour, away)
	onFleet := onHost(8*24*time.Hour, fleet)
	_ = onHost(time.Hour, mac) // the newest stays
	macPath, _ := store.GetSnapshot(ctx, pool, onMac)

	deleted, err := e.Once(ctx)
	if err != nil {
		t.Fatalf("a host that is away must not fail the run: %v", err)
	}
	if len(deleted) != 2 || !slices.Contains(deleted, onMac) || !slices.Contains(deleted, onFleet) {
		t.Fatalf("deleted %v, want the mac's and the fleet's", deleted)
	}
	if got := hd.sent[mac]; len(got) != 1 || got[0] != macPath.BlobPath {
		t.Fatalf("sent to the mac %v", got)
	}
	if !b.Exists(macPath.BlobPath) {
		t.Fatal("an owned host's snapshot was deleted through Blob")
	}
	if s, _ := store.GetSnapshot(ctx, pool, onFleet); b.Exists(s.BlobPath) {
		t.Fatal("the fleet's snapshot was not deleted through Blob")
	}
	if s, _ := store.GetSnapshot(ctx, pool, onAway); s.DeletedAt != nil || len(hd.sent[away]) != 0 {
		t.Fatal("the away host's row was marked deleted")
	}
	hd.away[away] = false
	if d, err := e.Once(ctx); err != nil || len(d) != 1 || d[0] != onAway {
		t.Fatalf("back: %v %v", d, err)
	}
}
