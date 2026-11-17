package store_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func newProject(t *testing.T, pool *db.Pool) (userID, projectID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid := store.NewID()
	pid := store.NewID()
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, $2)", uid, "h-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'p', $3, 'small', 'running', 1)",
		pid, uid, "p-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	return uid, pid
}

func hashOf(ticket string) []byte {
	sum := sha256.Sum256([]byte(ticket))
	return sum[:]
}

// A minted ticket is found by its hash and carries the project and user it
// was minted for; the row's id is what a log line may name.
func TestViewTicketRoundTrip(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	id, err := store.InsertViewTicket(ctx, pool, hashOf("ticket-a"), pid, uid, time.Now().Add(15*time.Minute))
	if err != nil || id == uuid.Nil {
		t.Fatalf("insert: %v %v", id, err)
	}
	got, err := store.GetViewTicket(ctx, pool, hashOf("ticket-a"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.ProjectID != pid || got.UserID != uid || got.Bytes != 0 || got.LastUsedAt != nil {
		t.Fatalf("row: %+v", got)
	}
	if _, err := store.GetViewTicket(ctx, pool, hashOf("another-ticket")); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("an unknown hash: %v", err)
	}
}

// An expired row authenticates nothing: expiry is this credential's
// revocation, so the lookup itself must refuse it.
func TestExpiredViewTicketIsNotFound(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	if _, err := store.InsertViewTicket(ctx, pool, hashOf("stale"), pid, uid, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetViewTicket(ctx, pool, hashOf("stale")); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("an expired ticket was found: %v", err)
	}
}

// The budget accumulates across calls, which is the whole point of holding
// it on the row: a stream that reconnects on the same ticket finds the
// bytes it already spent, so reconnecting cannot hand it a fresh 2 GiB.
func TestTouchViewTicketAccumulatesAcrossConnects(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, err := store.InsertViewTicket(ctx, pool, hashOf("t"), pid, uid, time.Now().Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var spent int64
	for i := 0; i < 3; i++ {
		var err error
		spent, err = store.TouchViewTicket(ctx, pool, hashOf("t"), 16<<20)
		if err != nil {
			t.Fatal(err)
		}
	}
	if spent != 3*(16<<20) {
		t.Fatalf("spent %d, want %d", spent, 3*(16<<20))
	}
	row, err := store.GetViewTicket(ctx, pool, hashOf("t"))
	if err != nil || row.Bytes != spent || row.LastUsedAt == nil {
		t.Fatalf("row %+v err %v", row, err)
	}
}

// Every mint sweeps, so the table needs no janitor: the dead rows go and
// the live ones stay.
func TestDeleteExpiredViewTicketsLeavesLiveRows(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid := newProject(t, pool)
	if _, err := store.InsertViewTicket(ctx, pool, hashOf("dead"), pid, uid, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertViewTicket(ctx, pool, hashOf("live"), pid, uid, time.Now().Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteExpiredViewTickets(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from browser_view_tickets where project_id = $1", pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d rows left, want the live one only", n)
	}
	if _, err := store.GetViewTicket(ctx, pool, hashOf("live")); err != nil {
		t.Fatalf("the live ticket went: %v", err)
	}
}

// The wake row is an upsert, so the second wake moves the time forward;
// this is the rule that has to survive an api restart.
func TestViewWakeUpsertKeepsTheLaterTime(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	_, pid := newProject(t, pool)

	if _, ok, err := store.LastViewWake(ctx, pool, pid); ok || err != nil {
		t.Fatalf("a project that never woke: ok=%v err=%v", ok, err)
	}
	if err := store.MarkViewWake(ctx, pool, pid); err != nil {
		t.Fatal(err)
	}
	first, ok, err := store.LastViewWake(ctx, pool, pid)
	if err != nil || !ok {
		t.Fatalf("after one wake: ok=%v err=%v", ok, err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.MarkViewWake(ctx, pool, pid); err != nil {
		t.Fatal(err)
	}
	second, _, err := store.LastViewWake(ctx, pool, pid)
	if err != nil {
		t.Fatal(err)
	}
	if !second.After(first) {
		t.Fatalf("the second wake did not move the row: %v then %v", first, second)
	}
}
