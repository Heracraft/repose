package guest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/metrics"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/obs"
)

// fakeCgroups is a host's CPU accounting for the controller's tests: a
// usage figure per cgroup that a test advances by hand, and the cpu.max
// the controller wrote.
type fakeCgroups struct {
	mu    sync.Mutex
	usage map[string]uint64 // cgroup -> cumulative usage_usec
	max   map[string]uint64 // cgroup -> millicores
	fail  map[string]bool
}

func newFakeCgroups() *fakeCgroups {
	return &fakeCgroups{usage: map[string]uint64{}, max: map[string]uint64{}, fail: map[string]bool{}}
}

func (f *fakeCgroups) read(cg string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[cg] {
		return 0, errors.New("no such cgroup")
	}
	return f.usage[cg], nil
}

func (f *fakeCgroups) write(cg string, milli uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.max[cg] = milli
	return nil
}

// burn adds cores x seconds of CPU time to a cgroup and to the host's
// root, the way a guest using that many cores for that long would.
func (f *fakeCgroups) burn(cg string, cores float64, d time.Duration) {
	usec := uint64(cores * float64(d.Microseconds()))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usage[cg] += usec
	f.usage["/"] += usec
}

// burnHostOnly adds CPU time that belongs to no guest: host builds,
// virtiofsd, hostd itself.
func (f *fakeCgroups) burnHostOnly(cores float64, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usage["/"] += uint64(cores * float64(d.Microseconds()))
}

func (f *fakeCgroups) capOf(guestID string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.max[GuestCgroup(guestID)]
}

// burstRig is a Manager with nothing but the state, the clock and the
// cgroups the controller reads and writes.
type burstRig struct {
	t    *testing.T
	m    *Manager
	st   *state.DB
	cg   *fakeCgroups
	now  time.Time
	mu   sync.Mutex
	logs lockedBuffer
}

func newBurstRig(t *testing.T, cores uint32, mut func(*Config)) *burstRig {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	r := &burstRig{t: t, st: st, cg: newFakeCgroups(), now: time.Unix(1_762_000_000, 0).UTC()}
	cfg := Config{
		HostID: "host-1", GuestsDir: filepath.Join(t.TempDir(), "guests"), GuestCIDR: "10.64.4.0/22",
		TotalMemBytes: 64 << 30, Burst: true, Cores: cores,
		Lookup: func(string) (int, int, error) { return os.Getuid(), os.Getgid(), nil },
	}
	if mut != nil {
		mut(&cfg)
	}
	m, err := New(cfg, Deps{
		State: st, LVM: lvm.NewFake(), Metrics: metrics.New(), Log: obs.NewTestLogger(t, obs.ComponentHostd, &r.logs),
		Now:             func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		CgroupCPUUsage:  r.cg.read,
		SetCgroupCPUMax: r.cg.write,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.m = m
	return r
}

func (r *burstRig) guest(id, class string) {
	r.t.Helper()
	g := &state.Guest{GuestID: id, ProjectID: id, Class: class, State: StateRunning, CreatedAt: r.now, UpdatedAt: r.now}
	if err := r.st.PutGuest(g); err != nil {
		r.t.Fatal(err)
	}
}

// tick advances the clock by d and runs one pass of the controller. The
// caller has already added whatever CPU time was used over that second.
func (r *burstRig) tick(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	r.m.BurstTickOnce(context.Background())
}

// settle runs n ticks of a second with each guest using every core its
// cap allows, which is what a build inside it looks like.
func (r *burstRig) settle(n int, ids ...string) {
	for i := 0; i < n; i++ {
		for _, id := range ids {
			milli := r.m.BurstCapMillicores(id)
			if milli == 0 {
				g, err := r.st.GetGuest(id)
				if err != nil {
					r.t.Fatal(err)
				}
				milli = uint64(Classes[g.Class].VCPUs) * 1000
			}
			r.cg.burn(GuestCgroup(id), float64(milli)/1000, time.Second)
		}
		r.tick(time.Second)
	}
}

// TestBurstRaisesAGuestToItsCeilingOnAQuietHost is the feature working:
// a large on a 32-core host with nothing else running reaches 6 cores,
// one core a tick, and stops there.
func TestBurstRaisesAGuestToItsCeilingOnAQuietHost(t *testing.T) {
	r := newBurstRig(t, 32, nil)
	r.guest("g-a", "large")
	r.tick(time.Second) // baseline
	r.settle(1, "g-a")
	if got := r.m.BurstCapMillicores("g-a"); got != 3000 {
		t.Fatalf("after one raising tick cap = %d millicores; want 3000 (2 base + the 1-core rise)", got)
	}
	r.settle(10, "g-a")
	if got := r.m.BurstCapMillicores("g-a"); got != 6000 {
		t.Fatalf("cap = %d millicores; want the large ceiling, 6000", got)
	}
	if got := r.cg.capOf("g-a"); got != 6000 {
		t.Fatalf("cpu.max written = %d millicores; want 6000", got)
	}
}

// TestBurstGateHoldsTheHostUnderTheLine is the gate: with the host's own
// work above 0.75 of its cores, every cap is back at its base in one
// tick, and nothing is below the base the class sells.
func TestBurstGateHoldsTheHostUnderTheLine(t *testing.T) {
	r := newBurstRig(t, 8, nil) // the gate is 0.75 x 8 = 6 cores
	r.guest("g-a", "large")
	r.guest("g-b", "small")
	r.tick(time.Second)
	r.settle(8, "g-a", "g-b")
	if r.m.BurstCapMillicores("g-a") <= 2000 || r.m.BurstCapMillicores("g-b") <= 1000 {
		t.Fatalf("the quiet host gave no burst: a=%d b=%d", r.m.BurstCapMillicores("g-a"), r.m.BurstCapMillicores("g-b"))
	}
	// Two host builds start: 7 cores of the host's own work, over the
	// 6-core gate on its own.
	r.cg.burnHostOnly(7, time.Second)
	r.tick(time.Second)
	if got := r.m.BurstCapMillicores("g-a"); got != 2000 {
		t.Fatalf("over the gate, large cap = %d; want its base, 2000", got)
	}
	if got := r.m.BurstCapMillicores("g-b"); got != 1000 {
		t.Fatalf("over the gate, small cap = %d; want its base, 1000", got)
	}
	// And it never goes under: the base is what the class sold.
	r.cg.burnHostOnly(30, time.Second)
	r.tick(time.Second)
	if got := r.cg.capOf("g-a"); got != 2000 {
		t.Fatalf("far over the gate, large cpu.max = %d; want its base, 2000", got)
	}
}

// TestBurstOffLeavesEveryGuestAtItsBase is the flag: a host without it
// runs no controller at all, writes no cpu.max, and boots guests at the
// vCPUs their class sells.
func TestBurstOffLeavesEveryGuestAtItsBase(t *testing.T) {
	r := newBurstRig(t, 32, func(c *Config) { c.Burst = false })
	r.guest("g-a", "large")
	r.tick(time.Second)
	r.settle(10, "g-a")
	if got := r.cg.capOf("g-a"); got != 0 {
		t.Fatalf("a host without the flag wrote cpu.max = %d; want nothing written", got)
	}
	if got := r.m.bootVCPUs(Classes["large"]); got != 2 {
		t.Fatalf("boot vCPUs with the flag off = %d; want the class's 2", got)
	}
	if got := r.m.BurstNSDelta("g-a"); got != 0 {
		t.Fatalf("burst metered with the flag off: %d ns", got)
	}
	if f := Features(false, false); strings.Contains(strings.Join(f, " "), FeatureBurst) {
		t.Fatalf("Hello named %q with the flag off: %v", FeatureBurst, f)
	}
	if f := Features(false, true); !strings.Contains(strings.Join(f, " "), FeatureBurst) {
		t.Fatalf("Hello did not name %q with the flag on: %v", FeatureBurst, f)
	}
}

// TestBurstBootsAtTheCeilingOnlyWithTheFlag pins the hypervisor's vCPU
// count, which is the one thing a running guest cannot change.
func TestBurstBootsAtTheCeilingOnlyWithTheFlag(t *testing.T) {
	on := newBurstRig(t, 32, nil)
	off := newBurstRig(t, 32, func(c *Config) { c.Burst = false })
	for _, tc := range []struct {
		class      string
		want, base uint32
	}{
		{"small", 4, 1}, {"large", 6, 2}, {"xl", 8, 4},
	} {
		if got := on.m.bootVCPUs(Classes[tc.class]); got != tc.want {
			t.Errorf("%s boots with %d vCPUs on a burst host; want %d", tc.class, got, tc.want)
		}
		if got := off.m.bootVCPUs(Classes[tc.class]); got != tc.base {
			t.Errorf("%s boots with %d vCPUs without the flag; want %d", tc.class, got, tc.base)
		}
	}
}

// TestBurstMetersTimeAboveTheBase is the meter: a large held at 6 cores
// for 10 seconds owes 4 x 10 core-seconds of burst, and the figure is
// taken once.
func TestBurstMetersTimeAboveTheBase(t *testing.T) {
	r := newBurstRig(t, 64, nil)
	r.guest("g-a", "large")
	r.tick(time.Second)
	r.settle(6, "g-a") // climb to the ceiling
	_ = r.m.BurstNSDelta("g-a")
	for i := 0; i < 10; i++ {
		r.cg.burn(GuestCgroup("g-a"), 6, time.Second)
		r.tick(time.Second)
	}
	got := r.m.BurstNSDelta("g-a")
	want := uint64(4 * 10 * time.Second) // 4 cores above the base, 10 s
	if got != want {
		t.Fatalf("burst metered = %d ns; want %d (4 cores above base x 10 s)", got, want)
	}
	if again := r.m.BurstNSDelta("g-a"); again != 0 {
		t.Fatalf("the same burst was handed out twice: %d ns", again)
	}
}

// TestBurstMeterDoesNotAverageAwayASpike is why the controller counts
// each second rather than reading a minute's average: a guest that uses
// six cores for half the time and none for the other half owes the burst
// of the busy half, not the burst of the average.
func TestBurstMeterDoesNotAverageAwayASpike(t *testing.T) {
	r := newBurstRig(t, 64, nil)
	r.guest("g-a", "large")
	r.tick(time.Second)
	r.settle(6, "g-a")
	_ = r.m.BurstNSDelta("g-a")
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			r.cg.burn(GuestCgroup("g-a"), 6, time.Second)
		}
		r.tick(time.Second)
	}
	// Five busy seconds at 4 cores above the base. A minute average
	// would read 3 cores used against a base of 2 and bill 10 core-
	// seconds instead of 20.
	if got, want := r.m.BurstNSDelta("g-a"), uint64(4*5*time.Second); got != want {
		t.Fatalf("burst metered = %d ns; want %d", got, want)
	}
}

// TestBurstStopsForAGuestPastItsAllowance is stage 3's enforcement:
// SetBurst allowed=false puts the guest at its base in that call, keeps
// it there however quiet the host is, and survives a hostd restart
// because it is in bbolt and in Hello.
func TestBurstStopsForAGuestPastItsAllowance(t *testing.T) {
	r := newBurstRig(t, 64, nil)
	r.guest("g-a", "large")
	r.tick(time.Second)
	r.settle(8, "g-a")
	if r.m.BurstCapMillicores("g-a") != 6000 {
		t.Fatalf("setup: cap = %d; want the ceiling", r.m.BurstCapMillicores("g-a"))
	}
	res := r.m.Execute(context.Background(), &hostdv1.Command{CommandId: "c1", Cmd: &hostdv1.Command_SetBurst{
		SetBurst: &hostdv1.SetBurst{GuestId: "g-a", Allowed: false}}})
	if res == nil || !res.Ok {
		t.Fatalf("SetBurst was refused: %v", res)
	}
	if got := r.cg.capOf("g-a"); got != 2000 {
		t.Fatalf("after SetBurst allowed=false, cpu.max = %d; want the base, 2000", got)
	}
	r.settle(10, "g-a")
	if got := r.m.BurstCapMillicores("g-a"); got != 2000 {
		t.Fatalf("a guest past its allowance rose to %d on a quiet host; want its base", got)
	}
	// A resend changes nothing, and the state is in bbolt for Hello.
	if res := r.m.Execute(context.Background(), &hostdv1.Command{CommandId: "c2", Cmd: &hostdv1.Command_SetBurst{
		SetBurst: &hostdv1.SetBurst{GuestId: "g-a", Allowed: false}}}); res == nil || !res.Ok {
		t.Fatalf("the resend was refused: %v", res)
	}
	g, err := r.st.GetGuest("g-a")
	if err != nil || !g.BurstDenied {
		t.Fatalf("burst_denied not stored: %+v %v", g, err)
	}
	var found *hostdv1.GuestStatus
	for _, gs := range r.m.Hello().Guests {
		if gs.GuestId == "g-a" {
			found = gs
		}
	}
	if found == nil || !found.BurstDenied {
		t.Fatalf("Hello did not carry burst_denied: %+v", found)
	}
	// At the next period the api hands the cores back.
	r.m.Execute(context.Background(), &hostdv1.Command{CommandId: "c3", Cmd: &hostdv1.Command_SetBurst{
		SetBurst: &hostdv1.SetBurst{GuestId: "g-a", Allowed: true}}})
	r.settle(8, "g-a")
	if got := r.m.BurstCapMillicores("g-a"); got != 6000 {
		t.Fatalf("after allowed=true the cap is %d; want the ceiling again", got)
	}
}

// TestBurstResetAtStartDropsAStaleCap is the restart rule: hostd writes
// every running guest's base before its first tick, so a hostd that died
// with caps raised does not leave the host oversubscribed.
func TestBurstResetAtStartDropsAStaleCap(t *testing.T) {
	r := newBurstRig(t, 64, nil)
	r.guest("g-a", "xl")
	r.guest("g-b", "small")
	// A previous hostd left both at their ceiling.
	_ = r.cg.write(GuestCgroup("g-a"), 8000)
	_ = r.cg.write(GuestCgroup("g-b"), 4000)
	r.m.BurstResetAll(context.Background())
	if got := r.cg.capOf("g-a"); got != 4000 {
		t.Fatalf("xl cap after the reset = %d; want its base, 4000", got)
	}
	if got := r.cg.capOf("g-b"); got != 1000 {
		t.Fatalf("small cap after the reset = %d; want its base, 1000", got)
	}
}

// TestBurstHeadroomGoesByClassWeight is the council's priority, which is
// free: when two guests both want cores and there are not enough for
// both ceilings, the xl's burst outranks the small's in the same ratio
// as their I-737 weights.
func TestBurstHeadroomGoesByClassWeight(t *testing.T) {
	// 8 cores: the gate is 6, the two bases take 5, so one core of
	// headroom is shared.
	r := newBurstRig(t, 8, nil)
	r.guest("g-xl", "xl")
	r.guest("g-sm", "small")
	r.tick(time.Second)
	r.settle(4, "g-xl", "g-sm")
	xl, sm := r.m.BurstCapMillicores("g-xl"), r.m.BurstCapMillicores("g-sm")
	if xl <= 4000 {
		t.Fatalf("the xl got no burst: %d", xl)
	}
	if xl-4000 <= sm-1000 {
		t.Fatalf("the xl's burst (%d) did not outrank the small's (%d)", xl-4000, sm-1000)
	}
	if sm < 1000 || xl < 4000 {
		t.Fatalf("a base was taken away: xl=%d sm=%d", xl, sm)
	}
}

// TestBurstUnreadableHostCgroupCutsToBase: a controller that cannot see
// the host's own CPU time must hand out nothing.
func TestBurstUnreadableHostCgroupCutsToBase(t *testing.T) {
	r := newBurstRig(t, 64, nil)
	r.guest("g-a", "large")
	r.tick(time.Second)
	r.settle(8, "g-a")
	if r.m.BurstCapMillicores("g-a") != 6000 {
		t.Fatalf("setup: cap = %d", r.m.BurstCapMillicores("g-a"))
	}
	r.cg.mu.Lock()
	r.cg.fail["/"] = true
	r.cg.mu.Unlock()
	r.tick(time.Second)
	if got := r.cg.capOf("g-a"); got != 2000 {
		t.Fatalf("with the host's cpu.stat unreadable the cap is %d; want the base", got)
	}
}

// TestBurstSampleDeltaIsBounded: a counter that moved further than the
// controller can have produced is clamped before billing reads it.
func TestBurstSampleDeltaIsBounded(t *testing.T) {
	for class, want := range map[string]uint64{
		"small": 3 * 66 * uint64(time.Second),
		"large": 4 * 66 * uint64(time.Second),
		"xl":    4 * 66 * uint64(time.Second),
	} {
		if got := maxBurstDelta(class); got != want {
			t.Errorf("maxBurstDelta(%q) = %d; want %d", class, got, want)
		}
	}
	if got := maxBurstDelta("nonesuch"); got != 0 {
		t.Errorf("maxBurstDelta of an unknown class = %d; want 0", got)
	}
}

// TestBurstHostBootsTheCeilingAndKeepsTheBaseQuota is the shape the
// whole design rests on, read off a real create: the hypervisor gets the
// ceiling, because a running guest cannot gain a vCPU, and the unit still
// starts at the base its class sold, because the controller is the only
// thing that may raise it.
func TestBurstHostBootsTheCeilingAndKeepsTheBaseQuota(t *testing.T) {
	for _, tc := range []struct {
		name      string
		burst     bool
		wantCPUs  string
		wantQuota string
	}{
		{"burst host", true, "--cpus boot=6", "CPUQuota=200%"},
		{"plain host", false, "--cpus boot=2", "CPUQuota=200%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(c *Config) { c.Burst = tc.burst })
			h.create(gid1)
			u := h.sd.Units["guest@"+gid1]
			if u == nil {
				t.Fatal("no unit")
			}
			if argv := strings.Join(u.Argv, " "); !strings.Contains(argv, tc.wantCPUs) {
				t.Fatalf("argv %v; want %s", u.Argv, tc.wantCPUs)
			}
			if u.Props[2] != tc.wantQuota {
				t.Fatalf("unit props %v; want %s at index 2", u.Props, tc.wantQuota)
			}
			// The class weight is untouched: I-737's ranking is what
			// decides who gets the headroom, and burst does not change it.
			if u.Props[3] != "CPUWeight=100" {
				t.Fatalf("unit props %v; want CPUWeight=100 at index 3", u.Props)
			}
		})
	}
}

// TestBurstDenialSurvivesAHostdRestart: the api's withdrawal is in
// bbolt, so a hostd that restarts does not hand an account past its
// allowance its cores back before the api's next hourly tick.
func TestBurstDenialSurvivesAHostdRestart(t *testing.T) {
	r := newBurstRig(t, 64, nil)
	r.guest("g-a", "large")
	if err := r.st.SetBurstAllowed("g-a", false); err != nil {
		t.Fatal(err)
	}
	// A fresh Manager on the same state: nothing in memory, the record
	// says denied.
	m, err := New(r.m.cfg, r.m.d)
	if err != nil {
		t.Fatal(err)
	}
	if m.BurstAllowed("g-a") {
		t.Fatal("a fresh hostd read a denied guest as allowed")
	}
	m.BurstResetAll(context.Background())
	if got := r.cg.capOf("g-a"); got != 2000 {
		t.Fatalf("the start reset wrote %d; want the base, 2000", got)
	}
	r.m = m
	r.tick(time.Second)
	r.settle(10, "g-a")
	if got := m.BurstCapMillicores("g-a"); got != 2000 {
		t.Fatalf("after a restart the denied guest rose to %d; want its base", got)
	}
}

// TestBurstKeepsACapBetweenTwoBursts: a guest that goes quiet for a tick
// keeps the cap it holds rather than being cut to its base, so a build
// that pauses for a second does not have to climb again. A guest the api
// has denied is cut, quiet or not.
//
// The probe that proves this test would fail without its fix: with
// `mayBurst` left out of the burstGuest literal (so it reads false for
// every guest), the first assertion below reads "a quiet guest was cut
// to 2000" -- which is also what a denied guest must read, so without
// both halves the bug is invisible.
func TestBurstKeepsACapBetweenTwoBursts(t *testing.T) {
	r := newBurstRig(t, 64, nil)
	r.guest("g-a", "large")
	r.tick(time.Second)
	r.settle(8, "g-a")
	if got := r.m.BurstCapMillicores("g-a"); got != 6000 {
		t.Fatalf("setup: cap = %d; want the ceiling", got)
	}
	// Two ticks using almost nothing: the guest is between two builds.
	for i := 0; i < 2; i++ {
		r.cg.burn(GuestCgroup("g-a"), 0.05, time.Second)
		r.tick(time.Second)
	}
	if got := r.m.BurstCapMillicores("g-a"); got != 6000 {
		t.Fatalf("a quiet guest was cut to %d; want it to keep its 6000", got)
	}
	// The same guest, denied: quiet or not, it goes to its base.
	r.m.SetBurstAllowed("g-a", false)
	r.cg.burn(GuestCgroup("g-a"), 0.05, time.Second)
	r.tick(time.Second)
	if got := r.m.BurstCapMillicores("g-a"); got != 2000 {
		t.Fatalf("a denied quiet guest is at %d; want its base, 2000", got)
	}
}
