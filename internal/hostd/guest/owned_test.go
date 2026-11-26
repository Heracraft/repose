package guest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// ownedHarness is a 16 GiB, 8-CPU host its user owns, with a 1 GiB
// reserve (DECISIONS I-1626, I-1628). avail is MemAvailable and slice
// guests.slice's memory.current; adj records each oom_score_adj written.
type ownedHarness struct {
	*harness
	avail, slice atomic.Uint64
	mu           sync.Mutex
	adj          map[int]int
	// clock, when set, is the manager's now in unix nanoseconds.
	clock atomic.Int64
	// memErr makes /proc/meminfo unreadable.
	memErr atomic.Bool
	// guestMem is each guest's cgroup memory.current; sliceOOM the
	// slice's memory.events oom_kill.
	guestMem sync.Map
	sliceOOM atomic.Uint64
}

func newOwnedHarness(t *testing.T) *ownedHarness {
	t.Helper()
	return newOwnedHarnessWith(t, nil)
}

func newOwnedHarnessWith(t *testing.T, cfg func(*Config)) *ownedHarness {
	t.Helper()
	o := &ownedHarness{adj: map[int]int{}}
	o.avail.Store(12 << 30)
	o.harness = newHarnessWith(t, func(c *Config) {
		c.Owned, c.HostVCPUs = true, 8
		c.TotalMemBytes, c.HostReserveBytes = 16<<30, 1<<30
		c.FreePageReporting = true
		c.OwnedGuardEvery = time.Hour // the tests call guardTick themselves
		if cfg != nil {
			cfg(c)
		}
	}, func(d *Deps) {
		d.MemInfo = func() (uint64, uint64, error) {
			if o.memErr.Load() {
				return 0, 0, errors.New("meminfo unreadable")
			}
			return 16 << 30, o.avail.Load(), nil
		}
		d.GuestsSliceMemory = func() (uint64, error) { return o.slice.Load(), nil }
		d.GuestMemory = func(id string) (uint64, error) {
			if v, ok := o.guestMem.Load(id); ok {
				return v.(uint64), nil
			}
			return 1 << 30, nil
		}
		d.SliceOOMKills = func() (uint64, error) { return o.sliceOOM.Load(), nil }
		d.Now = func() time.Time {
			if n := o.clock.Load(); n != 0 {
				return time.Unix(0, n)
			}
			return time.Now()
		}
		d.SetOOMScoreAdj = func(pid, adj int) error {
			o.mu.Lock()
			defer o.mu.Unlock()
			o.adj[pid] = adj
			return nil
		}
	})
	return o
}

func (o *ownedHarness) createClass(id, class string) {
	o.t.Helper()
	req := createReq(id)
	req.SystemClosure, req.Class = o.closure, class
	o.mustOK(cmd(req))
}

func (o *ownedHarness) adjOf(id string) int {
	o.t.Helper()
	pid := o.sd.Units["guest@"+id].MainPID
	o.mu.Lock()
	defer o.mu.Unlock()
	a, ok := o.adj[pid]
	if !ok {
		o.t.Fatalf("no oom_score_adj written for %s (pid %d): %v", id, pid, o.adj)
	}
	return a
}

// A guest on an owned host boots with the host's shape whatever class the
// api sent: every vCPU, the placeable memory, no disk caps, an equal CPU
// weight and no quota, and the OOM killer's first choice.
func TestOwnedGuestTakesTheHostShape(t *testing.T) {
	for _, class := range []string{"tiny", "xl"} {
		t.Run(class, func(t *testing.T) {
			o := newOwnedHarness(t)
			o.createClass(gid1, class)
			argv, props := o.unitOf(gid1)
			a := strings.Join(argv, " ")
			// (16 GiB - 1 GiB reserve) - 512 MiB overhead = 14848 MiB.
			for _, want := range []string{"--cpus boot=8", "--memory size=14848M,shared=on", "--balloon"} {
				if !strings.Contains(a, want) {
					t.Fatalf("argv lacks %q: %s", want, a)
				}
			}
			for _, cap := range []string{"bw_size", "ops_size"} {
				if strings.Contains(a, cap) {
					t.Fatalf("argv has a class disk cap %q: %s", cap, a)
				}
			}
			for _, want := range []string{"MemoryMax=15360M", "CPUWeight=100", "OOMScoreAdjust=1000"} {
				if !slices.Contains(props, want) {
					t.Fatalf("props lack %q: %v", want, props)
				}
			}
			for _, p := range props {
				if strings.HasPrefix(p, "CPUQuota=") {
					t.Fatalf("owned guest has a quota: %s", p)
				}
			}
			if !slices.Contains(o.m.Features(), "shared") {
				t.Fatalf("owned features %v", o.m.Features())
			}
			if g := o.guest(gid1); g.Class != class {
				t.Fatalf("class on the record %q, the api sent %q", g.Class, class)
			}
		})
	}
}

// The fleet keeps the class's shape: the owned branch must not leak.
func TestFleetGuestKeepsItsClass(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	argv, props := h.unitOf(gid1)
	a := strings.Join(argv, " ")
	if !strings.Contains(a, "--cpus boot=2") || !strings.Contains(a, "--memory size=8192M,shared=on") {
		t.Fatalf("large guest argv %s", a)
	}
	for _, want := range []string{"CPUQuota=200%", "CPUWeight=100", "MemoryMax=8704M"} {
		if !slices.Contains(props, want) {
			t.Fatalf("props lack %q: %v", want, props)
		}
	}
	for _, p := range props {
		if strings.HasPrefix(p, "OOMScoreAdjust=") {
			t.Fatalf("fleet guest has %s", p)
		}
	}
	if slices.Contains(h.m.Features(), "shared") {
		t.Fatalf("fleet features %v", h.m.Features())
	}
}

// Free memory on an owned host is the host's own figure, bounded by the
// room left under guests.slice, and a start needs a gigabyte of it.
func TestOwnedStartNeedsRealFreeMemory(t *testing.T) {
	o := newOwnedHarness(t)
	if got := o.m.FreeMemBytes(); got != 12<<30 {
		t.Fatalf("free %d, want MemAvailable 12 GiB", got)
	}
	o.slice.Store(14 << 30) // 15 GiB of room, 14 held: 1 GiB left
	if got := o.m.FreeMemBytes(); got != 1<<30 {
		t.Fatalf("free %d, want the slice's 1 GiB", got)
	}
	o.slice.Store(0)
	o.avail.Store(512 << 20)
	req := createReq(gid1)
	req.SystemClosure = o.closure
	res := o.mustFail(cmd(req), CodeInsufficientCapacity)
	if !strings.Contains(res.Error.Message, "not enough free memory") || !strings.Contains(res.Error.Message, "512 MiB free") {
		t.Fatalf("refusal %q", res.Error.Message)
	}
	o.avail.Store(1 << 30)
	o.createClass(gid1, "large")
	o.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	o.avail.Store(900 << 20)
	res = o.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeInsufficientCapacity)
	if !strings.Contains(res.Error.Message, "not enough free memory") {
		t.Fatalf("start refusal %q", res.Error.Message)
	}
	// No class arithmetic: a second large and an xl both start while the
	// host has the memory, which the fleet's reservation would refuse.
	o.avail.Store(12 << 30)
	o.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	o.createClass(gid2, "xl")
}

// Under a hypervisor (the Mac appliance), guests together boot with no
// more vCPUs than the host has: on the M4, four guests of four vCPUs on
// the appliance's eight hung every one of them (the lab, 2026-11-25
// 23:49Z). The refusal names CPU, not memory; a stopped guest frees its
// vCPUs (DECISIONS I-1640).
const (
	gid4 = "0192f0a4-4444-7000-8000-000000000004"
	gid5 = "0192f0a5-5555-7000-8000-000000000005"
)

func TestOwnedStartBoundedByHostVCPUs(t *testing.T) {
	o := newOwnedHarnessWith(t, func(c *Config) { c.HostVCPUs, c.OwnedMaxVCPUs = 2, 8 })
	ids := []string{gid1, gid2, gid3, gid4}
	for _, id := range ids {
		o.createClass(id, "small")
	}
	if argv, _ := o.unitOf(gid1); !strings.Contains(strings.Join(argv, " "), "--cpus boot=2") {
		t.Fatalf("argv %q lacks --cpus boot=2", argv)
	}
	req := createReq(gid5)
	req.SystemClosure = o.closure
	res := o.mustFail(cmd(req), CodeInsufficientCapacity)
	if !strings.Contains(res.Error.Message, "not enough free CPU") || !strings.Contains(res.Error.Message, "8 of 8 vCPUs") {
		t.Fatalf("refusal %q", res.Error.Message)
	}
	o.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	o.createClass(gid5, "small")
	res = o.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeInsufficientCapacity)
	if !strings.Contains(res.Error.Message, "not enough free CPU") {
		t.Fatalf("start refusal %q", res.Error.Message)
	}
}

// Without the bound (bare metal), the vCPU count limits nothing.
func TestOwnedBareMetalHasNoVCPUBound(t *testing.T) {
	o := newOwnedHarness(t)
	for _, id := range []string{gid1, gid2, gid3} {
		o.createClass(id, "small")
	}
}

// A guest the OOM killer took on an owned host is stopped with the agreed
// reason, torn down, and starts again; on the fleet it stays an error.
func TestOwnedOOMKillStopsTheGuest(t *testing.T) {
	o := newOwnedHarness(t)
	o.createClass(gid1, "large")
	tap := o.guest(gid1).Tap
	o.sd.OOMKill("guest@" + gid1)
	o.waitState(gid1, StateStopped)
	if g := o.guest(gid1); g.Reason != ReasonOOMKilled {
		t.Fatalf("reason %q", g.Reason)
	}
	// setState stores the state before it sends the event, so the state
	// can read stopped a moment before the event is there.
	sent := false
	for deadline := time.Now().Add(3 * time.Second); !sent && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		o.rec.mu.Lock()
		for _, e := range o.rec.events {
			if s := e.GetGuestStateChanged(); s != nil && s.GuestId == gid1 && s.State == StateStopped && s.Reason == "oom_killed" {
				sent = true
			}
		}
		o.rec.mu.Unlock()
	}
	if !sent {
		t.Fatal("no guest_state_changed stopped/oom_killed sent")
	}
	if left := o.net.Leftovers(gid1, tap); len(left) != 0 {
		t.Fatalf("leftovers %v", left)
	}
	o.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if o.guest(gid1).State != StateRunning {
		t.Fatal("no start after the kill")
	}

	h := newHarness(t, nil)
	h.create(gid1)
	h.sd.OOMKill("guest@" + gid1)
	h.waitState(gid1, StateError)
	if r := h.guest(gid1).Reason; r != "hypervisor exited 9" {
		t.Fatalf("fleet reason %q", r)
	}
}

// A guest the OOM killer took while hostd was down is reported the same
// way by the reconcile at hostd's next start.
func TestOwnedOOMKillWhileHostdWasDown(t *testing.T) {
	o := newOwnedHarness(t)
	o.createClass(gid1, "large")
	o.restart()
	o.sd.OOMKill("guest@" + gid1)
	if err := o.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	g := o.guest(gid1)
	if g.State != StateStopped || g.Reason != ReasonOOMKilled {
		t.Fatalf("after reconcile %s %q", g.State, g.Reason)
	}
}

// The newest guest is the OOM killer's first choice; each older one sits a
// step lower, and none below the floor.
func TestOwnedOOMOrderNewestFirst(t *testing.T) {
	o := newOwnedHarness(t)
	o.createClass(gid1, "large")
	if a := o.adjOf(gid1); a != OOMAdjNewest {
		t.Fatalf("lone guest adj %d", a)
	}
	o.createClass(gid2, "tiny")
	if a, b := o.adjOf(gid2), o.adjOf(gid1); a != 1000 || b != 750 {
		t.Fatalf("newest %d, older %d; want 1000 and 750", a, b)
	}
	// A restart of the older guest makes it the newest.
	o.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	o.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if a, b := o.adjOf(gid1), o.adjOf(gid2); a != 1000 || b != 750 {
		t.Fatalf("after the restart: gid1 %d, gid2 %d", a, b)
	}
	for i, want := range map[int]int{0: 1000, 1: 750, 2: 500, 3: 250, 4: 100, 9: 100} {
		if got := max(OOMAdjFloor, OOMAdjNewest-i*OOMAdjStep); got != want {
			t.Fatalf("place %d: %d, want %d", i, got, want)
		}
	}
}

// An idle guest's page cache is dropped once, until it is busy again; a
// host under pressure drops every guest's, at most once per PressureEvery.
func TestOwnedReclaim(t *testing.T) {
	o := newOwnedHarness(t)
	var drops atomic.Int32
	o.gopts.OnExec = func(argv []string) *guestdv1.ExecResult {
		if len(argv) == 3 && strings.Contains(argv[2], "drop_caches") && strings.Contains(argv[2], "compact_memory") {
			drops.Add(1)
		}
		return nil
	}
	o.createClass(gid1, "large")
	now := time.Unix(1_800_000_000, 0)
	tick := func(cpu uint64) {
		now = now.Add(time.Minute)
		o.clock.Store(now.UnixNano())
		o.m.ownedReclaim(context.Background(), map[string]uint64{gid1: cpu})
	}
	waitDrops := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for drops.Load() < want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		if got := drops.Load(); got != want {
			t.Fatalf("drops %d, want %d", got, want)
		}
	}
	tick(0) // the first sample has nothing to compare with
	for range IdleSamples - 1 {
		tick(0)
	}
	waitDrops(0)
	tick(0)
	waitDrops(1)
	tick(0)
	tick(0)
	waitDrops(1) // once per idle stretch
	tick(30e9)   // half a core for a minute: busy
	for range IdleSamples {
		tick(0)
	}
	waitDrops(2)

	o.avail.Store(512 << 20)
	tick(30e9)
	waitDrops(3)
	tick(30e9)
	waitDrops(3) // inside PressureEvery
	now = now.Add(PressureEvery)
	tick(30e9)
	waitDrops(4)
}

// The owned reserve replaces the fleet's eighth (I-1628), and burst is off
// on an owned host whatever the flag says.
func TestOwnedDefaults(t *testing.T) {
	c := Config{Owned: true, TotalMemBytes: 12 << 30, Burst: true}.Defaults()
	if c.HostReserveBytes != OwnedReserveBytes || OwnedReserveBytes != 512<<20 {
		t.Fatalf("owned reserve %d", c.HostReserveBytes)
	}
	if c.Burst {
		t.Fatal("burst on an owned host")
	}
	if f := (Config{TotalMemBytes: 12 << 30}).Defaults(); f.HostReserveBytes != 2<<30 {
		t.Fatalf("fleet reserve %d", f.HostReserveBytes)
	}
}

// An idle 8-vCPU guest on the M4 appliance costs its hypervisor 2.85 s of
// host CPU a minute (the lab, 19:01Z: 4.7 percent of one vCPU, the
// guest itself 1.3 percent), which the first threshold of 2 percent never
// called idle. It is idle; a build is not.
func TestOwnedIdleCountsTheHypervisorsOwnIdleCost(t *testing.T) {
	o := newOwnedHarness(t)
	var drops atomic.Int32
	o.gopts.OnExec = func(argv []string) *guestdv1.ExecResult {
		if len(argv) == 3 && strings.Contains(argv[2], "drop_caches") {
			drops.Add(1)
		}
		return nil
	}
	o.createClass(gid1, "large")
	now := time.Unix(1_800_000_000, 0)
	tick := func(cpu uint64) {
		now = now.Add(time.Minute)
		o.clock.Store(now.UnixNano())
		o.m.ownedReclaim(context.Background(), map[string]uint64{gid1: cpu})
	}
	tick(0)
	for range IdleSamples {
		tick(2_850_000_000)
	}
	deadline := time.Now().Add(2 * time.Second)
	for drops.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if drops.Load() != 1 {
		t.Fatalf("an idle guest at 2.85 s a minute was not dropped (drops %d)", drops.Load())
	}
	for range IdleSamples + 1 {
		tick(60_000_000_000) // a build: a whole vCPU
	}
	time.Sleep(50 * time.Millisecond)
	if drops.Load() != 1 {
		t.Fatalf("a busy guest was dropped (drops %d)", drops.Load())
	}
}

// DECISIONS I-1650: on the M4 appliance a guests.slice at its MemoryMax
// with THP-backed guest RAM swapped nothing and OOM-killed nothing; every
// guest hung and stayed hung (the lab, 2026-11-26 01:46Z). hostd does not
// wait for the kernel: free memory under OwnedGuardFloorBytes for
// OwnedGuardHold stops the newest running guest itself, killed and
// reported stopped/oom_killed as an OOM kill is; then nothing more for
// OwnedGuardCooldown, so what the stop freed is seen first.
func TestOwnedGuardStopsTheNewestBeforeTheLivelock(t *testing.T) {
	o := newOwnedHarness(t)
	o.createClass(gid1, "large")
	o.createClass(gid2, "large")
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0)
	at := func(d time.Duration) { o.clock.Store(t0.Add(d).UnixNano()); o.m.guardTick(ctx) }
	killed := func(id string) bool { return o.sd.HasOp("kill guest@" + id) }
	o.avail.Store(100 << 20)
	at(0)
	at(2 * time.Second)
	// Back above the floor: the hold starts again.
	o.avail.Store(2 << 30)
	at(3 * time.Second)
	o.avail.Store(100 << 20)
	at(4 * time.Second)
	at(8 * time.Second)
	if killed(gid1) || killed(gid2) {
		t.Fatal("a guest was stopped before free memory stayed under the floor for the hold")
	}
	at(9 * time.Second)
	o.waitState(gid2, StateStopped)
	if g := o.guest(gid2); g.Reason != ReasonOOMKilled || !killed(gid2) {
		t.Fatalf("newest: reason %q killed %v", g.Reason, killed(gid2))
	}
	if g := o.guest(gid1); g.State != StateRunning {
		t.Fatalf("older guest %s", g.State)
	}
	// Still short: the cooldown first, then the next at once, before the
	// kernel would act (it did 9-13 s after a stop, pass-2 B1; I-1650).
	at(11 * time.Second)
	at(13 * time.Second)
	if killed(gid1) {
		t.Fatal("a second guest stopped within the cooldown")
	}
	at(14 * time.Second)
	o.waitState(gid1, StateStopped)
	// A meminfo that cannot be read stops nothing.
	o.avail.Store(12 << 30)
	o.createClass(gid3, "large")
	o.avail.Store(100 << 20)
	o.memErr.Store(true)
	for s := 50; s < 120; s += 5 {
		at(time.Duration(s) * time.Second)
	}
	if killed(gid3) {
		t.Fatal("stopped a guest on an unreadable /proc/meminfo")
	}
}

// Guest RAM on an owned host keeps the hypervisor's 2 MiB pages by
// default (I-1574; 4 KiB pages were unusable on the M4, I-1650);
// OwnedGuestNoTHP boots it as 4 KiB shmem (thp=off), which reclaim can
// swap. The fleet is unchanged.
func TestOwnedGuestRAMHugePagesAreASwitch(t *testing.T) {
	o := newOwnedHarness(t)
	o.createClass(gid1, "small")
	if argv, _ := o.unitOf(gid1); strings.Contains(strings.Join(argv, " "), "thp=") {
		t.Fatalf("owned default argv %q names thp", argv)
	}
	off := newOwnedHarnessWith(t, func(c *Config) { c.OwnedGuestNoTHP = true })
	off.createClass(gid1, "small")
	if argv, _ := off.unitOf(gid1); !strings.Contains(strings.Join(argv, " "), ",shared=on,thp=off") {
		t.Fatalf("THP off: argv %q lacks thp=off", argv)
	}
	f := newHarness(t, nil)
	req := createReq(gid1)
	req.SystemClosure = f.closure
	f.mustOK(cmd(req))
	if b, _ := os.ReadFile(filepath.Join(f.cfg.GuestsDir, gid1, "ch.args")); strings.Contains(string(b), "thp=") {
		t.Fatalf("fleet argv names thp:\n%s", b)
	}
}

// The guard stops the machine whose memory grew most over its recent
// window, not the newest (pass-2 B1, 2026-11-26: a runaway in the oldest
// machine; the guard took the newest, idle one, and the kernel then
// OOM-killed the runaway's anyway, so two machines lost their memory).
// With no machine grown it falls back to the newest (DECISIONS I-1650).
func TestOwnedGuardStopsTheMachineThatGrew(t *testing.T) {
	o := newOwnedHarness(t)
	o.createClass(gid1, "large")
	o.createClass(gid2, "large")
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0)
	at := func(d time.Duration) { o.clock.Store(t0.Add(d).UnixNano()); o.m.guardTick(ctx) }
	o.guestMem.Store(gid2, uint64(1<<30))
	for i, gib := range []uint64{1, 3, 5, 7, 9, 9, 9, 9, 9, 9} {
		o.guestMem.Store(gid1, gib<<30)
		if i >= 3 {
			o.avail.Store(100 << 20)
		}
		at(time.Duration(i) * time.Second)
	}
	o.waitState(gid1, StateStopped)
	if o.sd.HasOp("kill guest@" + gid2) {
		t.Fatal("the idle newest guest was stopped")
	}
	if g := o.guest(gid1); g.Reason != ReasonOOMKilled {
		t.Fatalf("the guest that grew: reason %q", g.Reason)
	}
}

// A guest the appliance kernel's memory cgroup OOM-killed is stopped with
// the memory reason, not an error: on pass-2 B1 its hypervisor exited 0,
// systemd's Result said nothing, and only guests.slice's memory.events
// oom_kill counted it (DECISIONS I-1650).
func TestOwnedKernelOOMKillReadsAsOOM(t *testing.T) {
	o := newOwnedHarness(t)
	o.createClass(gid1, "large")
	o.sliceOOM.Add(1)
	o.sd.Exit("guest@"+gid1, 0)
	o.waitState(gid1, StateStopped)
	if g := o.guest(gid1); g.Reason != ReasonOOMKilled {
		t.Fatalf("reason %q, want %q", g.Reason, ReasonOOMKilled)
	}
	// A later exit with no new kill is an ordinary death.
	o.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	o.sd.Exit("guest@"+gid1, 3)
	o.waitState(gid1, StateError)
}
