package scheduler_test

import (
	"context"
	"errors"
	"os"
	"strconv"
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

// fleetTarget is a placement for an account that owns no host and whose
// plan sells fleet memory: the only kind of placement there was before
// DECISIONS I-1366, which is what the tests below that do not care about
// ownership are asking about.
func fleetTarget() scheduler.Target {
	return scheduler.Target{Account: uuid.Must(uuid.NewV7()), Fleet: true}
}

func pick(t *testing.T, pool *db.Pool, class string, hold, vol int64) (scheduler.Pick, error) {
	t.Helper()
	return pickFor(t, pool, fleetTarget(), class, hold, vol)
}

func pickFor(t *testing.T, pool *db.Pool, target scheduler.Target, class string, hold, vol int64) (scheduler.Pick, error) {
	t.Helper()
	var p scheduler.Pick
	err := db.InTx(context.Background(), pool, func(tx pgx.Tx) error {
		var err error
		p, err = scheduler.PickHost(context.Background(), tx, target, class, hold, vol, time.Now())
		return err
	})
	return p, err
}

// tableHost is a host for the table below, whose heartbeat is an age at
// the moment its case runs rather than an absolute time. PickHost ignores
// a host whose last heartbeat is older than HeartbeatWindow, 90 seconds,
// and it is given time.Now(); a table built from one time.Now() outside
// the loop therefore passes only while the whole table finishes inside
// that window. Under -race on a loaded box it took 117 s, and the last
// case failed with "no host with capacity" against a host the table
// meant to be fresh (2026-11-20).
type tableHost struct {
	name     string
	state    string
	draining bool
	mem      int64
	free     int64
	pool     int64
	age      time.Duration // how long ago this host last heartbeated
}

func TestPickTable(t *testing.T) {
	const stale = 2 * time.Minute
	cases := []struct {
		name  string
		hosts []tableHost
		class string
		want  string // host name or "" for capacity error
	}{
		{"most free wins", []tableHost{{"a", "ready", false, 64 << 30, 20 << 30, 1 << 40, 0}, {"b", "ready", false, 64 << 30, 40 << 30, 1 << 40, 0}}, "large", "b"},
		{"draining skipped", []tableHost{{"a", "ready", true, 64 << 30, 50 << 30, 1 << 40, 0}, {"b", "ready", false, 64 << 30, 20 << 30, 1 << 40, 0}}, "large", "b"},
		{"unreachable skipped", []tableHost{{"a", "unreachable", false, 64 << 30, 50 << 30, 1 << 40, 0}, {"b", "ready", false, 64 << 30, 20 << 30, 1 << 40, 0}}, "large", "b"},
		{"stale heartbeat skipped", []tableHost{{"a", "ready", false, 64 << 30, 50 << 30, 1 << 40, stale}, {"b", "ready", false, 64 << 30, 20 << 30, 1 << 40, 0}}, "large", "b"},
		{"full host skipped", []tableHost{{"a", "ready", false, 64 << 30, 7 << 30, 1 << 40, 0}}, "large", ""},
		{"pool full skipped", []tableHost{{"a", "ready", false, 64 << 30, 50 << 30, 0, 0}}, "large", ""},
		{"reserve respected", []tableHost{{"a", "ready", false, 16 << 30, 16 << 30, 1 << 40, 0}}, "xl", ""},
		{"xl fits above reserve", []tableHost{{"a", "ready", false, 32 << 30, 32 << 30, 1 << 40, 0}}, "xl", "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testdb.Open(t)
			for _, h := range c.hosts {
				addHost(t, pool, host{h.name, h.state, h.draining, h.mem, h.free, h.pool, time.Now().Add(-h.age)})
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
				p, err := scheduler.PickHost(ctx, tx, scheduler.Target{Account: uid, Fleet: true}, "large", 1<<30, 40<<30, time.Now())
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
	if ok, err := scheduler.Freeing(ctx, pool, fleetTarget(), "large", 1<<30, 40<<30, now); err != nil || !ok {
		t.Fatalf("freeing %v %v, want true with a large guest stopping", ok, err)
	}
	setPoolUnavailable(t, pool, a)
	if ok, err := scheduler.Freeing(ctx, pool, fleetTarget(), "large", 1<<30, 40<<30, now); err != nil || ok {
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

// host-01 is a D16s_v7, 8 physical cores, with memory to spare.
// PlacementVCPUPct allows 12 committed guest vCPUs there, so the fourth
// xl is refused and the refusal says it was CPU and not memory
// (DECISIONS I-736).
//
// The three guests of the 2026-10-14 incident -- two xl and a large --
// committed 20 vCPUs here and are 10 since I-784, so that exact host
// now accepts another large. That is the change, not a bug in it: the
// ratio is against the vCPUs a class gets, and a class gets half as
// many for the same memory. What has not been measured is whether the
// latency boundary tracks guest vCPUs or guest count (I-784, OWED).
func TestPickCountsCommittedVCPUsAgainstCores(t *testing.T) {
	pool := testdb.Open(t)
	h := addHostCores(t, pool, host{"host-01", "ready", false, 64 << 30, 56 << 30, 1 << 40, time.Now()}, 8)

	commit(t, pool, h, "xl", "kanali")  // 4 of 12
	commit(t, pool, h, "xl", "toolbar") // 8 of 12
	if p, err := pick(t, pool, "large", 1<<30, 40<<30); err != nil || p.Name != "host-01" {
		t.Fatalf("a large on 8 of 12 committed vCPUs: %+v %v, want host-01", p, err)
	}
	commit(t, pool, h, "large", "s2-sync") // 10 of 12
	commit(t, pool, h, "small", "lat")     // 11 of 12
	commit(t, pool, h, "small", "probe")   // 12 of 12, the ceiling
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
	// Memory is never the reason here: 256 GB, 240 free, against four xl.
	h := addHostCores(t, pool, host{"a", "ready", false, 256 << 30, 240 << 30, 1 << 40, time.Now()}, 0)
	commit(t, pool, h, "xl", "one")
	commit(t, pool, h, "xl", "two")
	commit(t, pool, h, "xl", "three")
	commit(t, pool, h, "xl", "four") // 16 vCPUs, past the ratio of any core count a D16s has
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
	commit(t, pool, h, "xl", "kanali")     // 4 of the 12 the cores allow
	commit(t, pool, h, "xl", "toolbar")    // 8 of 12
	commit(t, pool, h, "large", "s2-sync") // 10 of 12
	commit(t, pool, h, "small", "probe")   // 11 of 12
	commit(t, pool, h, "small", "lat")     // 12 of 12, the ceiling
	// A guest being destroyed frees memory -- hostd still counts it, so
	// Freeing is asked at all -- but the reservation view never counted
	// its vCPUs, so nothing is freed on the CPU side and a create that
	// waits for this host would be refused again.
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, 'gone', 'gone', 'large', 'destroying', $3, 1)`,
		uuid.Must(uuid.NewV7()), uid, h); err != nil {
		t.Fatal(err)
	}
	if ok, err := scheduler.Freeing(ctx, pool, fleetTarget(), "small", 1<<30, 20<<30, now); err != nil || ok {
		t.Fatalf("freeing %v %v at the CPU ceiling, want false: memory frees, cores do not", ok, err)
	}
	// Stop the xl instead: its 4 vCPUs are in the view and come back, so
	// the small fits and the create waits rather than failing.
	if _, err := pool.Exec(ctx, "update projects set state = 'stopping' where slug = 'kanali'"); err != nil {
		t.Fatal(err)
	}
	if ok, err := scheduler.Freeing(ctx, pool, fleetTarget(), "small", 1<<30, 20<<30, now); err != nil || !ok {
		t.Fatalf("freeing %v %v with the xl stopping, want true", ok, err)
	}
}

// The host reserve is an eighth of the host's memory with a 2 GiB floor
// and a 16 GiB ceiling (DECISIONS I-724). The two sizes repose runs today
// keep the figures the flat steps gave them, and an 8 GB host — which the
// flat 8 GiB left unable to start anything — keeps 6 GB for guests, enough
// for a small one.
func TestHostReserveIsAnEighthWithAFloorAndACeiling(t *testing.T) {
	gib := int64(1) << 30
	for _, c := range []struct{ total, want int64 }{
		{4 * gib, 2 * gib},
		{8 * gib, 2 * gib},
		{16 * gib, 2 * gib},
		{32 * gib, 4 * gib},
		{64 * gib, 8 * gib},
		{128 * gib, 16 * gib},
		{256 * gib, 16 * gib},
		{1024 * gib, 16 * gib},
	} {
		if got := scheduler.HostReserve(c.total); got != c.want {
			t.Errorf("HostReserve(%d GiB) = %d GiB, want %d GiB", c.total/gib, got/gib, c.want/gib)
		}
	}
	// A small guest (4 GiB + 512 MiB) fits on an 8 GB host.
	if 8*gib-scheduler.HostReserve(8*gib) < 4*gib+512<<20 {
		t.Error("an 8 GB host cannot start a small guest")
	}
}

// The class table, in the one Go place the api holds it: 4 GB of the
// class's memory per guest vCPU, uniformly across the three (DECISIONS
// I-784 amending R3-2). The uniformity is the point of the figure, not a
// tidiness: a host's placeable memory fixes its guest-vCPU count
// whatever mix of classes is sold, so two `small` on one 8 GB seat
// commit exactly what one `large` does. An unknown class is a large, as
// every other class function treats it.
func TestClassVCPUIsFourGBPerVCPU(t *testing.T) {
	for _, c := range []struct {
		class string
		milli int
		gb    int64
	}{
		{"tiny", 500, 2},
		{"small", 1000, 4},
		{"large", 2000, 8},
		{"xl", 4000, 16},
		{"", 2000, 8},
	} {
		if got := scheduler.ClassMilliVCPU(c.class); got != c.milli {
			t.Errorf("ClassMilliVCPU(%q) = %d, want %d", c.class, got, c.milli)
		}
		if got := scheduler.ClassRAM(c.class); got != c.gb<<30 {
			t.Errorf("ClassRAM(%q) = %d, want %d GB", c.class, got, c.gb)
		}
		// The ratio in thousandths, which is the only unit that can state
		// it for a class under 4 GB (I-1168): 2 GB over 500 is 4.
		if gbPer := c.gb * 1000 / int64(c.milli); gbPer != 4 {
			t.Errorf("%q: %d GB over %d milli-vCPU is %d GB per vCPU, want 4", c.class, c.gb, c.milli, gbPer)
		}
	}
	// Two `small`, or four `tiny`, are one seat of memory and one seat of
	// CPU. The hole I-784 closes is this not holding: at 2 GB per vCPU two
	// `small` committed 4 vCPUs against a `large`'s 4 for half the memory,
	// so a seat count derived from memory overstated a host by 2x for
	// anyone running smalls. A `tiny` with a whole vCPU would have
	// reopened it fourfold.
	if 2*scheduler.ClassMilliVCPU("small") != scheduler.ClassMilliVCPU("large") {
		t.Error("two small are not one large in CPU: the seat count overstates a host that sells smalls")
	}
	if 4*scheduler.ClassMilliVCPU("tiny") != scheduler.ClassMilliVCPU("large") {
		t.Error("four tiny are not one large in CPU: the seat count overstates a host that sells tinies")
	}
	if got := scheduler.ClassVCPUs("tiny"); got != 0.5 {
		t.Errorf("ClassVCPUs(tiny) = %v, want 0.5", got)
	}
}

// The ClassMilliVCPU figure in SQL, and that the view in migration 0041 sums
// the same numbers. The view is the one copy of the table that Go cannot
// reach, which is how it would have been left at 0027's 2-GB-per-vCPU
// figures while placement used the new ones: committed vCPUs would then
// read double on every host.
func TestHostReservationsVCPUsMatchClassVCPU(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	h := addHostCores(t, pool, host{"a", "ready", false, 256 << 30, 240 << 30, 1 << 40, time.Now()}, 32)
	want := 0
	for i, class := range []string{"tiny", "small", "large", "xl", "large"} {
		commit(t, pool, h, class, "p"+strconv.Itoa(i))
		want += scheduler.ClassMilliVCPU(class)
	}
	var got, guests int
	if err := pool.QueryRow(ctx, "select vcpu_milli, guests from host_reservations where host_id = $1", h).Scan(&got, &guests); err != nil {
		t.Fatal(err)
	}
	if guests != 5 || got != want {
		t.Fatalf("host_reservations: %d guests, %d milli-vcpus; want 5 and %d from scheduler.ClassMilliVCPU", guests, got, want)
	}
	// And the exported SQL agrees with the Go function class by class,
	// which is what internal/api/http/samples.go and internal/api/abuse
	// divide a guest's CPU by: a chart drawn against the wrong
	// denominator shows half or double the CPU the machine used.
	for _, class := range []string{"tiny", "small", "large", "xl", "unknown"} {
		var n int
		if err := pool.QueryRow(ctx, "select "+scheduler.ClassMilliVCPUSQL("$1::text"), class).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != scheduler.ClassMilliVCPU(class) {
			t.Errorf("ClassMilliVCPUSQL(%q) = %d, ClassMilliVCPU = %d", class, n, scheduler.ClassMilliVCPU(class))
		}
	}
}

// --- ownership (DECISIONS I-1366) -------------------------------------

// addAccount makes a user to own hosts and projects.
func addAccount(t *testing.T, pool *db.Pool, handle string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(), "insert into users (id, handle) values ($1, $2)", id, handle); err != nil {
		t.Fatal(err)
	}
	return id
}

// own gives the host to an account.
func own(t *testing.T, pool *db.Pool, hostID, owner uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "update hosts set owner_account_id = $2 where id = $1", hostID, owner); err != nil {
		t.Fatal(err)
	}
}

// TestOwnershipPlacesBothWays is the checklist row of
// docs/workstreams/19-mac-mini.md §7: "a test places an owner's project
// on their host and refuses a stranger's project there, shown red with
// the ownership filter removed".
//
// Both directions, because each fails differently and only one of them
// is a security incident: a stranger's project on somebody's Mac mini
// puts one user's code on another user's machine, and an owner's project
// on the fleet is a user paying for hardware that is not being used.
// The red run with `ownedSQL` replaced by `true` is in the commit
// message.
func TestOwnershipPlacesBothWays(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now()
	// One host repose runs, with more free memory than the owned one, so
	// "most free wins" would choose it if ownership did not speak first.
	addHost(t, pool, host{"fleet-01", "ready", false, 64 << 30, 60 << 30, 1 << 40, now})
	mac := addHost(t, pool, host{"mac-mini", "ready", false, 24 << 30, 20 << 30, 1 << 40, now})
	owner := addAccount(t, pool, "owner")
	stranger := addAccount(t, pool, "stranger")
	own(t, pool, mac, owner)

	// Subtests, so a run with the ownership term removed reports both
	// directions rather than stopping at the first.
	t.Run("the owner's project goes to the owner's host", func(t *testing.T) {
		// Not to the emptier host repose runs, which "most free wins"
		// would otherwise choose.
		if p, err := pickFor(t, pool, scheduler.Target{Account: owner, Fleet: true}, "large", 1<<30, 40<<30); err != nil || p.Name != "mac-mini" {
			t.Fatalf("owner placed on %+v %v, want mac-mini", p, err)
		}
	})
	t.Run("a stranger's project goes to the fleet", func(t *testing.T) {
		if p, err := pickFor(t, pool, scheduler.Target{Account: stranger, Fleet: true}, "large", 1<<30, 40<<30); err != nil || p.Name != "fleet-01" {
			t.Fatalf("stranger placed on %+v %v, want fleet-01", p, err)
		}
	})
	t.Run("an owned host is not a fallback for a stranger", func(t *testing.T) {
		// With the fleet host gone there is nowhere for the stranger:
		// the Mac is not spare capacity, it is somebody's machine.
		if _, err := pool.Exec(context.Background(), "update hosts set state = 'retired' where name = 'fleet-01'"); err != nil {
			t.Fatal(err)
		}
		if p, err := pickFor(t, pool, scheduler.Target{Account: stranger, Fleet: true}, "large", 1<<30, 40<<30); !errors.Is(err, scheduler.ErrNoCapacity) {
			t.Fatalf("stranger placed on %+v %v with only an owned host left, want no capacity", p, err)
		}
	})
}

// TestOwnHardwarePlanNeverReachesTheFleet is the second filter
// (DECISIONS I-1366): an Own hardware plan sells no memory on
// repose's own hosts, so placement is the only thing between such an
// account and the fleet. Shown red with the `$9` term of ownedSQL
// removed.
func TestOwnHardwarePlanNeverReachesTheFleet(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now()
	addHost(t, pool, host{"fleet-01", "ready", false, 64 << 30, 60 << 30, 1 << 40, now})
	owner := addAccount(t, pool, "owner")

	// No host of their own yet, and no fleet memory bought: nothing is
	// eligible, and the error is the one that makes the api say "your
	// host has not registered yet" rather than "no host with capacity".
	if p, err := pickFor(t, pool, scheduler.Target{Account: owner}, "large", 1<<30, 40<<30); !errors.Is(err, scheduler.ErrNoOwnHost) {
		t.Fatalf("own hardware account placed on %+v %v, want ErrNoOwnHost", p, err)
	}
	// Asking for the fleet outright does not get it either: the field is
	// a request, the plan is the answer.
	if p, err := pickFor(t, pool, scheduler.Target{Account: owner, Hosted: true}, "large", 1<<30, 40<<30); !errors.Is(err, scheduler.ErrNoCapacity) {
		t.Fatalf("own hardware account asked for hosted and got %+v %v, want no capacity", p, err)
	}
	// Their own host, once it is up, takes it.
	mac := addHost(t, pool, host{"mac-mini", "ready", false, 24 << 30, 20 << 30, 1 << 40, now})
	own(t, pool, mac, owner)
	if p, err := pickFor(t, pool, scheduler.Target{Account: owner}, "large", 1<<30, 40<<30); err != nil || p.Name != "mac-mini" {
		t.Fatalf("own hardware account placed on %+v %v, want mac-mini", p, err)
	}
}

// TestOwnerAsksForTheFleet: `placement: "hosted"` on an account that has
// both a host of its own and a plan that sells fleet memory goes to the
// fleet, which is the escape hatch of docs/workstreams/19-mac-mini.md
// item 19a ("unless the owner asks for hosted").
func TestOwnerAsksForTheFleet(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Now()
	addHost(t, pool, host{"fleet-01", "ready", false, 64 << 30, 60 << 30, 1 << 40, now})
	mac := addHost(t, pool, host{"mac-mini", "ready", false, 24 << 30, 20 << 30, 1 << 40, now})
	owner := addAccount(t, pool, "owner")
	own(t, pool, mac, owner)
	if p, err := pickFor(t, pool, scheduler.Target{Account: owner, Hosted: true, Fleet: true}, "large", 1<<30, 40<<30); err != nil || p.Name != "fleet-01" {
		t.Fatalf("owner asked for hosted and got %+v %v, want fleet-01", p, err)
	}
}

// TestOwnHostOffIsNotAFleetPlacement: a machine that is merely switched
// off must not silently push its owner's next project onto hardware they
// are not paying for, and a host that is gone for good must not trap
// them on it either (DECISIONS I-1366).
func TestOwnHostOffIsNotAFleetPlacement(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	addHost(t, pool, host{"fleet-01", "ready", false, 64 << 30, 60 << 30, 1 << 40, now})
	mac := addHost(t, pool, host{"mac-mini", "unreachable", false, 24 << 30, 20 << 30, 1 << 40, now})
	owner := addAccount(t, pool, "owner")
	own(t, pool, mac, owner)
	target := scheduler.Target{Account: owner, Fleet: true}
	if p, err := pickFor(t, pool, target, "large", 1<<30, 40<<30); !errors.Is(err, scheduler.ErrNoOwnHost) {
		t.Fatalf("owner with an unreachable Mac placed on %+v %v, want ErrNoOwnHost", p, err)
	}
	// Retired: the host is not theirs any more, so the fleet is theirs
	// again.
	if _, err := pool.Exec(ctx, "update hosts set state = 'retired' where id = $1", mac); err != nil {
		t.Fatal(err)
	}
	if p, err := pickFor(t, pool, target, "large", 1<<30, 40<<30); err != nil || p.Name != "fleet-01" {
		t.Fatalf("owner whose host is retired placed on %+v %v, want fleet-01", p, err)
	}
}

// TestFreeingRespectsOwnership: the three-minute placement wait
// (I-408) asks the same question, so a stranger does not wait for a
// guest to stop on somebody's Mac.
func TestFreeingRespectsOwnership(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	mac := addHost(t, pool, host{"mac-mini", "ready", false, 24 << 30, 4 << 30, 1 << 40, now})
	owner := addAccount(t, pool, "owner")
	stranger := addAccount(t, pool, "stranger")
	own(t, pool, mac, owner)
	if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, 'p', 'p', 'large', 'stopping', $3, 1)`,
		uuid.Must(uuid.NewV7()), owner, mac); err != nil {
		t.Fatal(err)
	}
	if ok, err := scheduler.Freeing(ctx, pool, scheduler.Target{Account: owner, Fleet: true}, "large", 1<<30, 40<<30, now); err != nil || !ok {
		t.Fatalf("freeing %v %v for the owner, want true", ok, err)
	}
	if ok, err := scheduler.Freeing(ctx, pool, scheduler.Target{Account: stranger, Fleet: true}, "large", 1<<30, 40<<30, now); err != nil || ok {
		t.Fatalf("freeing %v %v for a stranger on an owned host, want false", ok, err)
	}
}

// TestPinnedHost: a project that names one of its account's hosts goes
// there or is refused as that host, never placed on another (DECISIONS
// I-1584). The x86 pass of 2026-11-23 measured the defect: `--host a`
// landed on b half the time, and `--host <offline b>` landed on a.
func TestPinnedHost(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	addHost(t, pool, host{"fleet-01", "ready", false, 64 << 30, 60 << 30, 1 << 40, now})
	// b has more free memory, so "most free wins" picks b without a pin.
	a := addHost(t, pool, host{"box-a", "ready", false, 64 << 30, 30 << 30, 1 << 40, now})
	b := addHost(t, pool, host{"box-b", "ready", false, 64 << 30, 50 << 30, 1 << 40, now})
	owner := addAccount(t, pool, "owner")
	stranger := addAccount(t, pool, "stranger")
	own(t, pool, a, owner)
	own(t, pool, b, owner)

	t.Run("no pin is the scheduler's pick", func(t *testing.T) {
		if p, err := pickFor(t, pool, scheduler.Target{Account: owner, Fleet: true}, "large", 1<<30, 40<<30); err != nil || p.Name != "box-b" {
			t.Fatalf("placed on %+v %v, want box-b", p, err)
		}
	})
	t.Run("a pin wins over most free", func(t *testing.T) {
		if p, err := pickFor(t, pool, scheduler.Target{Account: owner, Fleet: true, Host: a}, "large", 1<<30, 40<<30); err != nil || p.Name != "box-a" {
			t.Fatalf("placed on %+v %v, want box-a", p, err)
		}
	})
	t.Run("a full pinned host is refused as that host", func(t *testing.T) {
		// xl needs 16 GiB; a has 30 free: fill it to 8.
		if _, err := pool.Exec(ctx, "update hosts set free_mem_bytes = $2 where id = $1", a, int64(8<<30)); err != nil {
			t.Fatal(err)
		}
		if p, err := pickFor(t, pool, scheduler.Target{Account: owner, Fleet: true, Host: a}, "xl", 1<<30, 40<<30); !errors.Is(err, scheduler.ErrPinnedHost) {
			t.Fatalf("placed on %+v %v, want ErrPinnedHost", p, err)
		}
	})
	t.Run("an offline pinned host is refused, not swapped for the other", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "update hosts set state = 'unreachable' where id = $1", b); err != nil {
			t.Fatal(err)
		}
		p, err := pickFor(t, pool, scheduler.Target{Account: owner, Fleet: true, Host: b}, "small", 1<<30, 40<<30)
		if !errors.Is(err, scheduler.ErrPinnedHost) || !errors.Is(err, scheduler.ErrNoOwnHost) || !errors.Is(err, scheduler.ErrNoCapacity) {
			t.Fatalf("placed on %+v %v, want ErrPinnedHost wrapping ErrNoOwnHost", p, err)
		}
	})
	t.Run("a pin on another account's host takes nothing", func(t *testing.T) {
		// The api never sends one; ownership still holds if it did.
		if p, err := pickFor(t, pool, scheduler.Target{Account: stranger, Fleet: true, Host: a}, "small", 1<<30, 40<<30); !errors.Is(err, scheduler.ErrPinnedHost) {
			t.Fatalf("stranger placed on %+v %v, want ErrPinnedHost", p, err)
		}
	})
	t.Run("freeing asks about the pinned host only", func(t *testing.T) {
		// A guest stopping on a would free room there and not on b.
		if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, 'p', 'p', 'xl', 'stopping', $3, 1)`,
			uuid.Must(uuid.NewV7()), owner, a); err != nil {
			t.Fatal(err)
		}
		if ok, err := scheduler.Freeing(ctx, pool, scheduler.Target{Account: owner, Fleet: true, Host: a}, "xl", 1<<30, 40<<30, now); err != nil || !ok {
			t.Fatalf("freeing on a %v %v, want true", ok, err)
		}
		if ok, err := scheduler.Freeing(ctx, pool, scheduler.Target{Account: owner, Fleet: true, Host: b}, "xl", 1<<30, 40<<30, now); err != nil || ok {
			t.Fatalf("freeing on offline b %v %v, want false", ok, err)
		}
	})
}
