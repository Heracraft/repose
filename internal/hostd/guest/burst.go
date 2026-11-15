package guest

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/state"
)

// Burst cores (DECISIONS I-1126). A guest of each class boots with more
// vCPUs than the class sells -- small 4, large 6, xl 8 -- and its cgroup
// still starts at the base quota its class bought. This controller is the
// only thing that raises the quota, and it raises it only while the whole
// host's CPU stays under a gate. The cores a tenant pays for are never
// taken away: no cap this file writes is ever below the base.
//
// The gate is what keeps typing smooth. I-738 measured 0.5 busy guest
// vCPUs per physical core at 2.7 ms p50 and 1.2 per core at 42 ms p90 on
// host-01, and nothing between. BurstGateDefault sits at 0.75 of physical
// cores, inside that unmeasured stretch, which is why the feature ships
// off: the curve that sets this number is proposal §6.2 and has not been
// run (staging is down, 2026-11-11).

// FeatureBurst is the Hello feature that says this host boots guests at
// their class ceiling and runs the controller. An api sends no SetBurst
// to a host without it, and a host that says nothing boots every guest at
// its base, exactly as before.
const FeatureBurst = "burst"

// BurstGateDefault is the share of a host's physical cores that may be
// busy before burst stops: the proposal's starting number, UNMEASURED.
// Above the gate every cap goes to its base in one tick.
const BurstGateDefault = 0.75

// BurstTick is how often the controller reads the host and writes caps.
// One second bounds how long the host stays above the gate after base use
// rises, which is the failure that matters; the raise is slow on purpose.
const BurstTick = time.Second

// BurstRiseMillicores is the most one guest's cap may rise in one tick.
// Cut at once, raise slowly: a host over the line is bounded at about a
// second, and a guest that starts a build reaches its ceiling in a few.
const BurstRiseMillicores = 1000

// burstHungryNum/Den is the share of its current cap a guest must have
// used in the last tick to be offered more: 90 percent. A guest under it
// is not asking for cores, and giving it headroom takes them from one
// that is.
const burstHungryNum, burstHungryDen = 9, 10

// burstState is the controller's memory between ticks.
type burstState struct {
	mu sync.Mutex
	// capMilli is each running guest's cap as the controller last wrote
	// it, in thousandths of a core.
	capMilli map[string]uint64
	// usedUsec is each guest's last cpu.stat usage_usec, and hostUsec the
	// root cgroup's.
	usedUsec map[string]uint64
	hostUsec uint64
	hostSeen bool
	// allowed is the api's per-guest burst permission (SetBurst): false
	// once an account has used its period's allowance with overage off.
	// A guest absent from the map is allowed, which is the state a host
	// is in before the api has said anything.
	allowed map[string]bool
	// burstNS is burst CPU time not yet sent in a sample, in nanoseconds:
	// the time each guest used above its base share, summed per tick.
	burstNS map[string]uint64
	// last is when the previous tick read the counters.
	last time.Time
}

func newBurstState() *burstState {
	return &burstState{
		capMilli: map[string]uint64{},
		usedUsec: map[string]uint64{},
		allowed:  map[string]bool{},
		burstNS:  map[string]uint64{},
	}
}

// BurstAllowed reports whether this guest may burst. An unknown guest may:
// a host that has heard nothing from the api bursts, and the api stops it
// when the account's allowance runs out (DECISIONS I-1128).
func (m *Manager) BurstAllowed(guestID string) bool {
	if m.burst == nil {
		return false
	}
	m.burst.mu.Lock()
	v, ok := m.burst.allowed[guestID]
	m.burst.mu.Unlock()
	if ok {
		return v
	}
	// Not yet in this process's memory: the stored answer, so a hostd
	// restart keeps the api's withdrawal (DECISIONS I-1128).
	g, err := m.d.State.GetGuest(guestID)
	if err != nil || g == nil {
		return true
	}
	return !g.BurstDenied
}

// SetBurstAllowed records the api's permission for one guest and, when it
// is withdrawn, puts the guest back at its base in this call rather than
// at the next tick. It is a state, not an event: the same value twice
// changes nothing, which is what lets the api resend it (CLAUDE.md, "Every
// host and guest operation is idempotent").
func (m *Manager) SetBurstAllowed(guestID string, allowed bool) {
	if err := m.d.State.SetBurstAllowed(guestID, allowed); err != nil {
		m.d.Log.Warn("burst permission not stored", "event", "burst", "guest_id", guestID, "err", err.Error())
	}
	if m.burst == nil {
		return
	}
	m.burst.mu.Lock()
	m.burst.allowed[guestID] = allowed
	m.burst.mu.Unlock()
	if !allowed {
		if g, err := m.d.State.GetGuest(guestID); err == nil && g != nil && g.State == StateRunning {
			m.writeBurstCap(guestID, uint64(Classes[g.Class].VCPUs)*1000)
		}
	}
}

// BurstNSDelta takes the burst nanoseconds counted for a guest since the
// last call and resets its counter. The sampler calls it once a minute, so
// a sample carries the whole minute's burst and nothing is counted twice.
func (m *Manager) BurstNSDelta(guestID string) uint64 {
	if m.burst == nil {
		return 0
	}
	m.burst.mu.Lock()
	defer m.burst.mu.Unlock()
	v := m.burst.burstNS[guestID]
	delete(m.burst.burstNS, guestID)
	return v
}

// BurstCapMillicores is the cap the controller last wrote for a guest, in
// thousandths of a core; 0 when it has written none.
func (m *Manager) BurstCapMillicores(guestID string) uint64 {
	if m.burst == nil {
		return 0
	}
	m.burst.mu.Lock()
	defer m.burst.mu.Unlock()
	return m.burst.capMilli[guestID]
}

// forgetBurst drops a gone guest's controller state, so a destroyed id
// does not keep a cursor or an unsent burst delta for ever.
func (m *Manager) forgetBurst(guestID string) {
	if m.burst == nil {
		return
	}
	m.burst.mu.Lock()
	delete(m.burst.capMilli, guestID)
	delete(m.burst.usedUsec, guestID)
	delete(m.burst.burstNS, guestID)
	delete(m.burst.allowed, guestID)
	m.burst.mu.Unlock()
}

// BurstResetAll puts every running guest at its base cap. hostd calls it
// once at start, before the first tick: a hostd that died with caps raised
// left them raised, and the host must not stay oversubscribed across a
// restart that is meant to be invisible.
func (m *Manager) BurstResetAll(ctx context.Context) {
	if m.burst == nil {
		return
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		m.d.Log.Error("burst: state read failed", "event", "burst", "err", err.Error())
		return
	}
	n := 0
	for _, g := range gs {
		if g.State != StateRunning {
			continue
		}
		if m.writeBurstCap(g.GuestID, uint64(Classes[g.Class].VCPUs)*1000) {
			n++
		}
	}
	m.d.Log.Info("burst caps reset to base", "event", "burst", "count", n)
}

// BurstLoop runs the controller until ctx ends. Nothing runs on a host
// without the feature: the caller does not start it.
func (m *Manager) BurstLoop(ctx context.Context) {
	m.BurstResetAll(ctx)
	t := time.NewTicker(BurstTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.BurstTickOnce(ctx)
		}
	}
}

// burstGuest is one running guest as a tick sees it.
type burstGuest struct {
	id       string
	class    string
	base     uint64 // millicores the class sells
	ceiling  uint64 // millicores the class may reach
	weight   uint64 // the class's cgroup weight (I-737), which ranks who gets headroom
	cap      uint64 // the cap in force now
	used     uint64 // millicores used over the last tick
	mayBurst bool   // the api allows it and it is not resting
	eligible bool   // mayBurst, and it used 90 percent of its cap
}

// BurstTickOnce is one pass of the controller: read the host and every
// running guest's CPU time, count the burst each used, and write the caps
// the gate allows. Exported so a test can drive it tick by tick.
func (m *Manager) BurstTickOnce(ctx context.Context) {
	if m.burst == nil || m.d.CgroupCPUUsage == nil || m.d.SetCgroupCPUMax == nil {
		return
	}
	now := m.d.Now()
	gs, err := m.d.State.ListGuests()
	if err != nil {
		m.d.Log.Error("burst: state read failed", "event", "burst", "err", err.Error())
		return
	}
	hostUsec, herr := m.d.CgroupCPUUsage("/")
	if herr != nil {
		// Without the host's own figure there is no gate, and a
		// controller that cannot see the host must not hand out cores.
		m.d.Log.Warn("burst: host cpu.stat unread", "event", "burst", "err", herr.Error())
		m.burstAllToBase(gs)
		return
	}

	m.burst.mu.Lock()
	dt := time.Duration(0)
	if !m.burst.last.IsZero() {
		dt = now.Sub(m.burst.last)
	}
	hostDelta := uint64(0)
	if m.burst.hostSeen {
		hostDelta = counterDelta(m.burst.hostUsec, hostUsec)
	}
	m.burst.hostUsec, m.burst.hostSeen = hostUsec, true
	m.burst.last = now
	first := dt <= 0
	m.burst.mu.Unlock()

	// The first tick after a start has no interval to divide by, so it
	// only takes the baselines.
	if first {
		for _, g := range gs {
			if g.State != StateRunning {
				continue
			}
			if v, err := m.d.CgroupCPUUsage(GuestCgroup(g.GuestID)); err == nil {
				m.burst.mu.Lock()
				m.burst.usedUsec[g.GuestID] = v
				m.burst.mu.Unlock()
			}
		}
		return
	}
	dtUsec := uint64(dt.Microseconds())
	if dtUsec == 0 {
		return
	}

	var list []burstGuest
	var sumBurstMilli uint64
	for _, g := range gs {
		if g.State != StateRunning {
			m.forgetBurstUsage(g.GuestID)
			continue
		}
		class := Classes[g.Class]
		base := uint64(class.MilliVCPUs)
		ceiling := uint64(class.CeilingVCPUs) * 1000
		if ceiling < base {
			ceiling = base
		}
		v, err := m.d.CgroupCPUUsage(GuestCgroup(g.GuestID))
		if err != nil {
			// A guest whose cgroup cannot be read is left at its base:
			// its own time is invisible, so it is also missing from the
			// host figure's correction below, and guessing either way
			// hands out cores against an unknown.
			m.writeBurstCap(g.GuestID, base)
			continue
		}
		m.burst.mu.Lock()
		prev, seen := m.burst.usedUsec[g.GuestID]
		m.burst.usedUsec[g.GuestID] = v
		capMilli, capSeen := m.burst.capMilli[g.GuestID]
		allowed, allowedSeen := m.burst.allowed[g.GuestID]
		if !allowedSeen {
			// The api's standing answer, from bbolt: a hostd restart
			// must not hand an account past its allowance its cores
			// back before the api's next hourly tick (I-1128).
			allowed = !g.BurstDenied
			m.burst.allowed[g.GuestID] = allowed
		}
		var usedMilli uint64
		if seen {
			d := counterDelta(prev, v)
			usedMilli = d * 1000 / dtUsec
			if d*1000 > base*dtUsec {
				// Burst is time above the base share, counted each tick
				// rather than from a minute's average: a guest that uses
				// three cores for thirty seconds and none for thirty
				// averages 1.5, which bills a small for half what it
				// used (proposal §8, DECISIONS I-1128).
				m.burst.burstNS[g.GuestID] += d*1000 - base*dtUsec
			}
		}
		m.burst.mu.Unlock()
		if !capSeen {
			capMilli = base
		}
		if usedMilli > base {
			sumBurstMilli += usedMilli - base
		}
		mayBurst := allowed && m.restingMark(g.GuestID) == nil
		list = append(list, burstGuest{
			id: g.GuestID, class: g.Class, base: base, ceiling: ceiling,
			weight: uint64(class.MilliVCPUs) * CPUWeightPerVCPU / 1000,
			cap:    capMilli, used: usedMilli, mayBurst: mayBurst,
			eligible: mayBurst && ceiling > base && usedMilli*burstHungryDen >= capMilli*burstHungryNum,
		})
	}

	// The host's busy cores over the tick, and the headroom the gate
	// leaves once the burst already running is given back: burst is what
	// may be taken away, so it does not count against the gate when
	// deciding how much there is.
	hostMilli := hostDelta * 1000 / dtUsec
	targetMilli := uint64(math.Round(m.burstGate() * float64(m.cfg.Cores) * 1000))
	baseMilli := uint64(0)
	if hostMilli > sumBurstMilli {
		baseMilli = hostMilli - sumBurstMilli
	}
	headroom := uint64(0)
	if targetMilli > baseMilli {
		headroom = targetMilli - baseMilli
	}

	if headroom == 0 {
		for _, g := range list {
			m.writeBurstCap(g.id, g.base)
		}
		m.gateMetric(false)
		return
	}
	m.gateMetric(true)

	// Hungry guests share the headroom in proportion to their class
	// weight, so on a busy host a Pro guest's burst ranks above a Plus
	// guest's and a Plus above a Solo's, which is free: the weights are
	// I-737's and the base share never loses to another guest's burst.
	sort.Slice(list, func(i, j int) bool {
		if list[i].weight != list[j].weight {
			return list[i].weight > list[j].weight
		}
		return list[i].id < list[j].id
	})
	var totalWeight uint64
	for _, g := range list {
		if g.eligible {
			totalWeight += g.weight
		}
	}
	for _, g := range list {
		want := g.base
		if g.eligible && totalWeight > 0 {
			want = g.base + headroom*g.weight/totalWeight
		} else if g.eligible {
			want = g.base
		} else if g.cap > g.base {
			// Not hungry, and not forbidden: keep what it holds rather
			// than take cores from a guest between two bursts. A guest
			// that is forbidden or resting is cut to its base.
			want = g.cap
			if g.ceiling <= g.base || !g.mayBurst {
				want = g.base
			}
		}
		if want > g.cap+BurstRiseMillicores {
			want = g.cap + BurstRiseMillicores
		}
		if want > g.ceiling {
			want = g.ceiling
		}
		if want < g.base {
			want = g.base
		}
		m.writeBurstCap(g.id, want)
	}
}

// burstGate is the share of physical cores that may be busy before burst
// stops.
func (m *Manager) burstGate() float64 {
	if m.cfg.BurstGate > 0 {
		return m.cfg.BurstGate
	}
	return BurstGateDefault
}

func (m *Manager) gateMetric(open bool) {
	if m.d.Metrics == nil {
		return
	}
	v := 0.0
	if open {
		v = 1
	}
	m.d.Metrics.BurstGateOpen.Set(v)
}

// burstAllToBase cuts every running guest to its base, the state the
// controller falls back to whenever it cannot see what it needs.
func (m *Manager) burstAllToBase(gs []*state.Guest) {
	for _, g := range gs {
		if g.State == StateRunning {
			m.writeBurstCap(g.GuestID, uint64(Classes[g.Class].VCPUs)*1000)
		}
	}
	m.gateMetric(false)
}

func (m *Manager) forgetBurstUsage(guestID string) {
	m.burst.mu.Lock()
	delete(m.burst.usedUsec, guestID)
	delete(m.burst.capMilli, guestID)
	m.burst.mu.Unlock()
}

// writeBurstCap writes a guest's cpu.max when it differs from the cap in
// force, and records it. It reports whether it wrote.
func (m *Manager) writeBurstCap(guestID string, milli uint64) bool {
	if m.burst == nil || m.d.SetCgroupCPUMax == nil {
		return false
	}
	m.burst.mu.Lock()
	cur, seen := m.burst.capMilli[guestID]
	m.burst.mu.Unlock()
	if seen && cur == milli {
		return false
	}
	if err := m.d.SetCgroupCPUMax(GuestCgroup(guestID), milli); err != nil {
		m.d.Log.Warn("burst: cpu.max not written", "event", "burst", "guest_id", guestID, "err", err.Error())
		return false
	}
	m.burst.mu.Lock()
	m.burst.capMilli[guestID] = milli
	m.burst.mu.Unlock()
	if m.d.Metrics != nil {
		m.d.Metrics.BurstCapMillicores.Set(float64(milli))
	}
	return true
}

// GuestCgroup is a guest unit's cgroup path under the unified hierarchy.
// guest@<id> sets Slice=guests.slice (unit.go), so the path is fixed and
// the controller does not spend a `systemctl show` per guest per second to
// learn it.
func GuestCgroup(guestID string) string { return "/guests.slice/" + GuestUnit(guestID) + ".service" }

// bootVCPUs is the vCPU count a guest of this class boots with: its
// ceiling on a host that bursts, the base its class sells on every other
// host (DECISIONS I-1126). The hypervisor takes one number and cannot
// change it on a running guest, so the ceiling has to be there from the
// first boot and the cgroup is what holds the guest to its base. A guest
// started before the flag went on keeps the count it has until its next
// cold start -- a park and restore carries the saved state's count, as
// I-784 recorded -- and runs at its base, which is what it has today.
func (m *Manager) bootVCPUs(class Class) uint32 {
	if m.cfg.Burst && class.CeilingVCPUs > class.VCPUs {
		return class.CeilingVCPUs
	}
	return class.VCPUs
}

// setBurst answers the api's SetBurst (DECISIONS I-1128). A host without
// the feature refuses it, so an api that sends one to a host whose Hello
// never named `burst` learns so rather than believing the guest is held
// at its base. An unknown guest is not found; everything else is the
// state write, which is idempotent.
func (m *Manager) setBurst(c *hostdv1.SetBurst) *Error {
	if c.GuestId == "" {
		return errf(CodeInvalidArgument, "guest_id is required")
	}
	if m.burst == nil {
		return errf(CodeInvalidArgument, "this host does not burst")
	}
	g, err := m.d.State.GetGuest(c.GuestId)
	if err != nil || g == nil {
		return errf(CodeNotFound, "no such guest")
	}
	m.SetBurstAllowed(c.GuestId, c.Allowed)
	m.d.Log.Info("burst permission set", "event", "burst", "guest_id", c.GuestId, "allowed", c.Allowed)
	return nil
}
