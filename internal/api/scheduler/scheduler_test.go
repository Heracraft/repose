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

func setPoolUnavailable(t *testing.T, pool *db.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "update hosts set pool_unavailable = true where id = $1", id); err != nil {
		t.Fatal(err)
	}
}

// DECISIONS I-644: a host whose thin pool is down is never chosen, however
// much memory it has; with the pool bytes it reports then at 0 an older
// api skips it too, and this filter does not depend on that.
func TestPickSkipsPoolUnavailable(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now()
	a := addHost(t, pool, host{"a", "ready", false, 64 << 30, 50 << 30, 1 << 40, now})
	b := addHost(t, pool, host{"b", "ready", false, 64 << 30, 20 << 30, 1 << 40, now})
	setPoolUnavailable(t, pool, a)
	if p, err := pick(t, pool, "large", 1<<30, 40<<30); err != nil || p.Name != "b" {
		t.Fatalf("got %+v %v, want b", p, err)
	}
	setPoolUnavailable(t, pool, b)
	if p, err := pick(t, pool, "large", 1<<30, 40<<30); !errors.Is(err, scheduler.ErrNoCapacity) {
		t.Fatalf("got %+v %v, want no capacity", p, err)
	}
}

// Freeing waits only for hosts PickHost could choose.
func TestFreeingSkipsPoolUnavailable(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	a := addHost(t, pool, host{"a", "ready", false, 64 << 30, 4 << 30, 1 << 40, now})
	uid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'u')", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, 'p', 'p', 'large', 'stopping', $3, 1)`,
		uuid.Must(uuid.NewV7()), uid, a); err != nil {
		t.Fatal(err)
	}
	if ok, err := scheduler.Freeing(ctx, pool, "large", 1<<30, 40<<30, now); err != nil || !ok {
		t.Fatalf("freeing %v %v, want true with a large guest stopping", ok, err)
	}
	setPoolUnavailable(t, pool, a)
	if ok, err := scheduler.Freeing(ctx, pool, "large", 1<<30, 40<<30, now); err != nil || ok {
		t.Fatalf("freeing %v %v on a host whose pool is down, want false", ok, err)
	}
}

// addHostCores adds h with physical cores reported. 0 cores is a host
// that registered before I-735 and has not rotated since, which the
// Register path would have given vcpus / 2 had it reported threads.
func addHostCores(t *testing.T, pool *db.Pool, h host, cores int) uuid.UUID {
	t.Helper()
	id := addHost(t, pool, h)
	if _, err := pool.Exec(context.Background(), "update hosts set cores = $2 where id = $1", id, cores); err != nil {
		t.Fatal(err)
	}
	return id
}

// commit places a guest of class on host in a state that holds it, so
// its vCPUs and memory are committed there.
func commit(t *testing.T, pool *db.Pool, hostID uuid.UUID, class, slug string) {
	t.Helper()
	ctx := context.Background()
	var uid uuid.UUID
	if err := pool.QueryRow(ctx, "select id from users limit 1").Scan(&uid); err != nil {
		uid = uuid.Must(uuid.NewV7())
		if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'u')", uid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, $3, $3, $4, 'running', $5, 1)`,
		uuid.Must(uuid.NewV7()), uid, slug, class, hostID); err != nil {
		t.Fatal(err)
	}
}

// The incident of 2026-10-14, reproduced: host-01 is a D16s_v7, 8
// physical cores, with memory to spare. PlacementVCPUPct allows 12
// committed guest vCPUs there, so the second xl -- the 16 guest vCPUs on
// 8 cores that took keystrokes from 2.7 ms to 17 ms -- is refused, and
// the refusal says it was CPU and not memory (DECISIONS I-736).
func TestPickCountsCommittedVCPUsAgainstCores(t *testing.T) {
	pool := testdb.Open(t)
	h := addHostCores(t, pool, host{"host-01", "ready", false, 64 << 30, 56 << 30, 1 << 40, time.Now()}, 8)

	commit(t, pool, h, "xl", "kanali") // 8 of 12
	if p, err := pick(t, pool, "large", 1<<30, 40<<30); err != nil || p.Name != "host-01" {
		t.Fatalf("a large on 8 of 12 committed vCPUs: %+v %v, want host-01", p, err)
	}
	commit(t, pool, h, "large", "s2-sync") // 12 of 12, the ceiling
	if p, err := pick(t, pool, "small", 1<<30, 20<<30); !errors.Is(err, scheduler.ErrNoCPUCapacity) {
		t.Fatalf("a small at the ceiling: %+v %v, want ErrNoCPUCapacity", p, err)
	}
	if p, err := pick(t, pool, "xl", 1<<30, 80<<30); !errors.Is(err, scheduler.ErrNoCapacity) {
		t.Fatalf("an xl at the ceiling: %+v %v, want a capacity error", p, err)
	}
	// Memory was never the reason: the host has 56 GB free and the xl
	// needs 16.
	var free int64
	if err := pool.QueryRow(context.Background(), "select free_mem_bytes from hosts where id = $1", h).Scan(&free); err != nil || free < 16<<30 {
		t.Fatalf("free memory %d, want room for the xl that was refused", free)
	}
}

// A host that reports no cores is exempt: 0 means unknown, and refusing
// every create on a one-host fleet because a field is missing is worse
// than placing on it (DECISIONS I-736).
func TestPickIgnoresCPUWhenAHostReportsNoCores(t *testing.T) {
	pool := testdb.Open(t)
	h := addHostCores(t, pool, host{"a", "ready", false, 64 << 30, 56 << 30, 1 << 40, time.Now()}, 0)
	commit(t, pool, h, "xl", "one")
	commit(t, pool, h, "xl", "two")
	commit(t, pool, h, "large", "three") // 20 vCPUs, past any ratio
	if p, err := pick(t, pool, "small", 1<<30, 20<<30); err != nil || p.Name != "a" {
		t.Fatalf("host with unknown cores: %+v %v, want a placement", p, err)
	}
}

// An empty host always takes one guest, so a host with fewer cores than
// the class asks for can still run something; the next guest is refused.
func TestPickTakesOneGuestOnAHostSmallerThanTheClass(t *testing.T) {
	pool := testdb.Open(t)
	h := addHostCores(t, pool, host{"small-host", "ready", false, 64 << 30, 56 << 30, 1 << 40, time.Now()}, 1)
	if p, err := pick(t, pool, "xl", 1<<30, 80<<30); err != nil || p.Name != "small-host" {
		t.Fatalf("first guest on a 1-core host: %+v %v, want a placement", p, err)
	}
	commit(t, pool, h, "xl", "first")
	if p, err := pick(t, pool, "small", 1<<30, 20<<30); !errors.Is(err, scheduler.ErrNoCPUCapacity) {
		t.Fatalf("second guest on a 1-core host: %+v %v, want ErrNoCPUCapacity", p, err)
	}
}

// Freeing refuses a host that is at its CPU ceiling even while memory
// is coming back on it, and allows it once the vCPUs are what is being
// freed: the reservation view counts a `stopping` guest, so the term
// subtracts it (DECISIONS I-736).
func TestFreeingCountsTheVCPUsOfAGuestBeingStopped(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	h := addHostCores(t, pool, host{"a", "ready", false, 64 << 30, 56 << 30, 1 << 40, now}, 8)
	uid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'u')", uid); err != nil {
		t.Fatal(err)
	}
	commit(t, pool, h, "xl", "kanali")     // 8 of the 12 cores allow
	commit(t, pool, h, "large", "s2-sync") // 12 of 12, the ceiling
	// A guest being destroyed frees memory -- hostd still counts it, so
	// Freeing is asked at all -- but the reservation view never counted
	// its vCPUs, so nothing is freed on the CPU side and a create that
	// waits for this host would be refused again.
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, 'gone', 'gone', 'large', 'destroying', $3, 1)`,
		uuid.Must(uuid.NewV7()), uid, h); err != nil {
		t.Fatal(err)
	}
	if ok, err := scheduler.Freeing(ctx, pool, "small", 1<<30, 20<<30, now); err != nil || ok {
		t.Fatalf("freeing %v %v at the CPU ceiling, want false: memory frees, cores do not", ok, err)
	}
	// Stop the xl instead: its 8 vCPUs are in the view and come back, so
	// the small fits and the create waits rather than failing.
	if _, err := pool.Exec(ctx, "update projects set state = 'stopping' where slug = 'kanali'"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scheduler.Freeing(ctx, pool, "small", 1<<30, 20<<30, now); err != nil || !ok {
		t.Fatalf("freeing %v %v with the xl stopping, want true", ok, err)
	}
}
