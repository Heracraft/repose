package scheduler_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

type host struct {
	name      string
	state     string
	draining  bool
	mem       int64
	free      int64
	pool      int64
	heartbeat time.Time
}

func addHost(t *testing.T, pool *db.Pool, h host) uuid.UUID {
	t.Helper()
	return addHostPool(t, pool, h, h.pool)
}

// addHostPool adds h with a thin pool of size bytes, h.pool of them free;
// size 0 is a hostd that reports no pool size.
func addHostPool(t *testing.T, pool *db.Pool, h host, size int64) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(context.Background(), `insert into hosts (id, name, state, draining, mem_bytes, free_mem_bytes, pool_free_bytes, pool_bytes, last_heartbeat_at) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, h.name, h.state, h.draining, h.mem, h.free, h.pool, size, h.heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func pick(t *testing.T, pool *db.Pool, class string, hold, vol int64) (scheduler.Pick, error) {
	t.Helper()
	var p scheduler.Pick
	err := db.InTx(context.Background(), pool, func(tx pgx.Tx) error {
		var err error
		p, err = scheduler.PickHost(context.Background(), tx, class, hold, vol, time.Now())
		return err
	})
	return p, err
}

func TestPickTable(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		hosts []host
		class string
		want  string // host name or "" for capacity error
	}{
		{"most free wins", []host{{"a", "ready", false, 64 << 30, 20 << 30, 1 << 40, now}, {"b", "ready", false, 64 << 30, 40 << 30, 1 << 40, now}}, "large", "b"},
		{"draining skipped", []host{{"a", "ready", true, 64 << 30, 50 << 30, 1 << 40, now}, {"b", "ready", false, 64 << 30, 20 << 30, 1 << 40, now}}, "large", "b"},
		{"unreachable skipped", []host{{"a", "unreachable", false, 64 << 30, 50 << 30, 1 << 40, now}, {"b", "ready", false, 64 << 30, 20 << 30, 1 << 40, now}}, "large", "b"},
		{"stale heartbeat skipped", []host{{"a", "ready", false, 64 << 30, 50 << 30, 1 << 40, now.Add(-2 * time.Minute)}, {"b", "ready", false, 64 << 30, 20 << 30, 1 << 40, now}}, "large", "b"},
		{"full host skipped", []host{{"a", "ready", false, 64 << 30, 7 << 30, 1 << 40, now}}, "large", ""},
		{"pool full skipped", []host{{"a", "ready", false, 64 << 30, 50 << 30, 0, now}}, "large", ""},
		{"reserve respected", []host{{"a", "ready", false, 16 << 30, 16 << 30, 1 << 40, now}}, "xl", ""},
		{"xl fits above reserve", []host{{"a", "ready", false, 32 << 30, 32 << 30, 1 << 40, now}}, "xl", "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testdb.Open(t)
			for _, h := range c.hosts {
				addHost(t, pool, h)
			}
			p, err := pick(t, pool, c.class, 1<<30, 40<<30)
			if c.want == "" {
				if !errors.Is(err, scheduler.ErrNoCapacity) {
					t.Fatalf("expected capacity error, got %v %+v", err, p)
				}
				return
			}
			if err != nil || p.Name != c.want {
				t.Fatalf("got %+v %v want %s", p, err, c.want)
			}
		})
	}
}

// 100 parallel creates on a host with room for 30 large guests yield
// exactly 30 placements and 70 capacity errors.
func TestConcurrentPlacementsReserveExactly(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	// 256 GB host: reserve 16 GB, 240 GB free for 30 large (8 GB) guests.
	hid := addHost(t, pool, host{"big", "ready", false, 256 << 30, 256 << 30, 1 << 40, time.Now()})
	uid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'u')", uid); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	placed, capacity := 0, 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
				p, err := scheduler.PickHost(ctx, tx, "large", 1<<30, 40<<30, time.Now())
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, $3, $3, 'large', 'creating', $4, 1)`,
					uuid.Must(uuid.NewV7()), uid, uuid.NewString()[:8], p.HostID)
				return err
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				placed++
			case errors.Is(err, scheduler.ErrNoCapacity):
				capacity++
			default:
				t.Errorf("create %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if placed != 30 || capacity != 70 {
		t.Fatalf("placed=%d capacity=%d", placed, capacity)
	}
	var reserved int64
	if err := pool.QueryRow(ctx, "select reserved_bytes from host_reservations where host_id = $1", hid).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 30*(8<<30) {
		t.Fatalf("reserved %d", reserved)
	}
}

// A host takes a new project while its thin pool, with the new volume's
// first bytes, stays at most PlacementPoolPct used (DECISIONS I-586), not
// while the volume's whole size is free: sizes are ceilings and the pool
// is overcommitted by design. A hostd that reports no pool size is
// placed on as before, by the whole size free.
func TestPickByPoolRoom(t *testing.T) {
	const tib = int64(1) << 40
	now := time.Now()
	cases := []struct {
		name       string
		free, size int64
		hold, vol  int64
		want       bool
	}{
		{"60 percent used takes a new project", tib * 4 / 10, tib, 1 << 30, 40 << 30, true},
		{"just under 70 percent after the new bytes", tib*3/10 + 2<<30, tib, 1 << 30, 40 << 30, true},
		{"past 70 percent after the new bytes", tib*3/10 + 512<<20, tib, 1 << 30, 40 << 30, false},
		{"75 percent used takes nothing", tib / 4, tib, 1 << 30, 40 << 30, false},
		{"a restore counts what its source holds", tib, tib, 800 << 30, 800 << 30, false},
		{"a large volume on an empty small pool fits by what it holds", 20 << 30, 20 << 30, 1 << 30, 40 << 30, true},
		{"no pool size: the whole volume free", 50 << 30, 0, 1 << 30, 40 << 30, true},
		{"no pool size: less than the volume free", 30 << 30, 0, 1 << 30, 40 << 30, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testdb.Open(t)
			addHostPool(t, pool, host{"a", "ready", false, 64 << 30, 50 << 30, c.free, now}, c.size)
			p, err := pick(t, pool, "large", c.hold, c.vol)
			if c.want {
				if err != nil || p.Name != "a" {
					t.Fatalf("got %+v %v, want host a", p, err)
				}
				return
			}
			if !errors.Is(err, scheduler.ErrNoCapacity) {
				t.Fatalf("got %+v %v, want no capacity", p, err)
			}
		})
	}
}

// A host takes at most HostGuestSlots projects, running or stopped: each
// keeps a guest address from the host's /22 (DECISIONS I-586). Destroyed
// projects free theirs.
func TestPickCountsGuestSlots(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	hid := addHost(t, pool, host{"a", "ready", false, 64 << 30, 50 << 30, 1 << 40, time.Now()})
	uid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'slots')", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes)
		select gen_random_uuid(), $1, 'p' || g, 'p' || g, 'small', 'stopped', $2, 1 from generate_series(1, $3::int) g`, uid, hid, scheduler.HostGuestSlots-1); err != nil {
		t.Fatal(err)
	}
	if _, err := pick(t, pool, "small", 1<<30, 20<<30); err != nil {
		t.Fatalf("one slot left: %v", err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values (gen_random_uuid(), $1, 'last', 'last', 'small', 'stopped', $2, 1)", uid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := pick(t, pool, "small", 1<<30, 20<<30); !errors.Is(err, scheduler.ErrNoCapacity) {
		t.Fatalf("every slot taken: %v", err)
	}
	if _, err := pool.Exec(ctx, "update projects set destroyed_at = now() where slug = 'last'"); err != nil {
		t.Fatal(err)
	}
	if _, err := pick(t, pool, "small", 1<<30, 20<<30); err != nil {
		t.Fatalf("a destroyed project's slot: %v", err)
	}
}
