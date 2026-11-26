package guest

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/hostd/state"
)

// A host the user owns shares its memory between its guests (owner's
// ruling, 2026-11-25; DECISIONS I-1626). On such a host a guest has no
// size: every guest boots with the host's placeable memory and every
// vCPU, the guests share the host, and memory a guest is not using goes
// back to it (free page reporting, the idle guest's page cache dropped,
// the host's compressed swap). When memory truly runs out the kernel
// kills a guest, never the appliance's own services, and the most
// recently started guest first; hostd reports that guest stopped with
// ReasonOOMKilled.

// FeatureShared is the Hello feature an owned host's hostd names: its
// guests share the host and it reports real free memory, so the api
// places on it by that figure and not by class (the api's owned placement, PR #213). An owned host
// without it is a hostd from before I-1626 and keeps class arithmetic.
const FeatureShared = "shared"

// OwnedReserveBytes is what an owned host keeps for itself: what the
// appliance's own processes and the kernel hold with one idle guest
// running, measured on the lab Mac, plus a margin (DECISIONS I-1628):
// about 300 MB measured (the appliance's processes 131 MB, kernel slab,
// stacks and page tables 160 MB, the hypervisor's own 4 MB), 512 MiB
// kept. guests.slice's MemoryMax is the host's memory less this, and so
// is the memory each guest boots with.
const OwnedReserveBytes = 512 << 20

// OwnedStartFreeBytes is the free memory a guest needs to start on an
// owned host: what FreeMemBytes reports, the host's own figure, not a
// reservation (the api's scheduler.OwnedStartFreeBytes, PR #213;
// the two are one number).
const OwnedStartFreeBytes = 1 << 30

// ReasonOOMKilled is the GuestStateChanged reason of a guest the kernel's
// OOM killer stopped on an owned host: the guest is `stopped`, its volume
// intact, and the api names the host and the cause (agreed with the api
// side in the sharing program, 2026-11-25).
const ReasonOOMKilled = "oom_killed"

// The OOM order on an owned host (DECISIONS I-1626). The kernel picks the
// process with the highest oom_score, which is its share of the host's
// memory in thousandths plus oom_score_adj. The newest guest gets
// OOMAdjNewest and each older one OOMAdjStep less, down to OOMAdjFloor:
// the newest goes first unless an older guest holds more than a quarter
// of the host's memory more per place between them. Every guest stays
// above the appliance's services, which keep systemd's 0 and, for hostd,
// -900, and hold a few percent of the host at most.
const (
	OOMAdjNewest = 1000
	OOMAdjStep   = 250
	OOMAdjFloor  = 100
)

// Reclaim on an owned host. A guest is idle when its hypervisor's unit
// used under IdleCPUShare of one vCPU over a sample, host-side: an idle
// 8-vCPU guest on the M4 appliance costs 4.7 percent of one there (the
// lab, 2026-11-25 19:01Z; 1.3 percent inside the guest), and a build or
// an agent at work costs whole vCPUs, so a tenth sits between them; IdleSamples such samples in a
// row drop its page cache once, until it is busy again. FreeMemBytes
// under PressureFreeBytes drops the page cache of every running guest,
// at most once per PressureEvery.
const (
	IdleCPUShare      = 0.10
	IdleSamples       = 5
	PressureFreeBytes = 1 << 30
)

// PressureEvery bounds how often the pressure drop runs.
var PressureEvery = 5 * time.Minute

// dropScript is what dropGuestCache runs in the guest. compact_memory
// exists only in a kernel built with compaction.
const dropScript = "sync; echo 1 > /proc/sys/vm/drop_caches; if [ -w /proc/sys/vm/compact_memory ]; then echo 1 > /proc/sys/vm/compact_memory; fi"

// OwnedNestedVCPUs is the vCPUs each guest of an owned host under a
// hypervisor boots with (DECISIONS I-1640): on the M4 appliance a node
// build was fastest at two, within 1.1-1.3x of the same build run in the
// appliance itself, and two guests at once suffered least at two.
const OwnedNestedVCPUs uint32 = 2

// OwnedSliceCPUWeight is guests.slice's CPU weight on an owned host,
// half of system.slice's default 100, so the appliance's own services
// win the CPU they ask for (DECISIONS I-1640).
const OwnedSliceCPUWeight = 50

// ownedState is the reclaim's memory between samples.
type ownedState struct {
	mu         sync.Mutex
	idle       map[string]int  // guest id -> idle samples in a row
	dropped    map[string]bool // guest id -> cache dropped since it was last busy
	lastPress  time.Time
	lastSample time.Time
	rankMu     sync.Mutex
}

// classOf is the shape a guest of class boots with on this host: the
// class itself on the fleet, the host's whole shape on an owned host.
func (m *Manager) classOf(class string) Class {
	if m.cfg.Owned {
		return m.ownedClass()
	}
	return Classes[class]
}

// ownedClass is every vCPU the host has and its placeable memory: the
// host's memory less the reserve and the hypervisor's own overhead,
// rounded down to 128 MiB. No disk caps: the volume shares the pool.
func (m *Manager) ownedClass() Class {
	v := m.cfg.HostVCPUs
	if v == 0 {
		v = 1
	}
	var mib uint64
	if room := m.cfg.TotalMemBytes; room > m.cfg.HostReserveBytes+(OverheadMiB+128)<<20 {
		mib = (room-m.cfg.HostReserveBytes)>>20 - OverheadMiB
		mib -= mib % 128
	} else {
		mib = 128
	}
	return Class{VCPUs: v, MilliVCPUs: v * 1000, CeilingVCPUs: v, MemMiB: mib, Shared: true}
}

// ownedFreeBytes is the host's real free memory (DECISIONS I-1626): the
// kernel's MemAvailable, bounded by the room left under guests.slice's
// MemoryMax (total less the reserve, less what the slice holds).
// MemAvailable already leaves out what the appliance's processes hold, so
// the reserve is not taken from it a second time.
func (m *Manager) ownedFreeBytes() uint64 {
	total := m.cfg.TotalMemBytes
	var avail uint64
	if m.d.MemInfo != nil {
		t, a, err := m.d.MemInfo()
		if err != nil {
			return 0
		}
		if t > 0 {
			total = t
		}
		avail = a
	}
	if total <= m.cfg.HostReserveBytes {
		return 0
	}
	room := total - m.cfg.HostReserveBytes
	if m.d.GuestsSliceMemory != nil {
		if used, err := m.d.GuestsSliceMemory(); err == nil {
			if used >= room {
				return 0
			}
			room -= used
		}
	}
	return min(avail, room)
}

// ownedRoom refuses a start or a create on an owned host that has less
// than OwnedStartFreeBytes free. The words "not enough free memory" are
// what the api maps to its sentence naming the host.
func (m *Manager) ownedRoom() *Error {
	if free := m.FreeMemBytes(); free < OwnedStartFreeBytes {
		return errf(CodeInsufficientCapacity, "not enough free memory: %d MiB free on this host, a machine starts with %d MiB", free>>20, uint64(OwnedStartFreeBytes)>>20)
	}
	return m.ownedCPURoom()
}

// ownedCPURoom refuses a start or a create on a nested owned host when the
// guests that hold vCPUs (every one not stopped, failed or destroyed)
// and the new one would boot with more vCPUs than the host has: on the
// M4 appliance sixteen guest vCPUs on eight hung every guest, and they
// stayed hung once the load was gone (the lab, 2026-11-25 23:49Z;
// DECISIONS I-1640). Each counts at today's per-guest figure.
func (m *Manager) ownedCPURoom() *Error {
	limit, per := m.cfg.OwnedMaxVCPUs, m.ownedClass().VCPUs
	if limit == 0 {
		return nil
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return errf(CodeInternal, "listing guests: %v", err)
	}
	var taken uint32
	for _, g := range gs {
		switch g.State {
		case StateStopped, StateError, StateDestroying, StateDestroyed:
			continue
		}
		taken += per
	}
	if taken+per > limit {
		return errf(CodeInsufficientCapacity, "not enough free CPU: %d of %d vCPUs taken by running machines on this host, a machine starts with %d", taken, limit, per)
	}
	return nil
}

// memRoom is the memory refusal of a create, a restore and a start: a
// class's reservation on the fleet, real free memory on an owned host.
func (m *Manager) memRoom(class string) *Error {
	if m.cfg.Owned {
		return m.ownedRoom()
	}
	if m.FreeMemBytes() < (Classes[class].MemMiB+OverheadMiB)<<20 {
		return errf(CodeInsufficientCapacity, "not enough free memory for a %s guest", class)
	}
	return nil
}

// rankOOM sets each running guest's hypervisor oom_score_adj by start
// order, newest highest (OOMAdjNewest, OOMAdjStep, OOMAdjFloor). A guest
// starts with OOMAdjNewest from its unit; this lowers the older ones.
// hostd is root, so the kernel also raises each process's floor to what
// is written and the unprivileged hypervisor cannot lower itself.
func (m *Manager) rankOOM(ctx context.Context) {
	if !m.cfg.Owned || m.d.SetOOMScoreAdj == nil {
		return
	}
	m.owned.rankMu.Lock()
	defer m.owned.rankMu.Unlock()
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return
	}
	type ranked struct {
		id  string
		pid int
		at  uint64
	}
	var run []ranked
	for _, g := range gs {
		if g.State != StateRunning {
			continue
		}
		props, err := m.d.Systemd.Show(ctx, GuestUnit(g.GuestID), "MainPID", "ActiveEnterTimestampMonotonic")
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(props["MainPID"])
		at, _ := strconv.ParseUint(props["ActiveEnterTimestampMonotonic"], 10, 64)
		if pid <= 0 {
			continue
		}
		run = append(run, ranked{id: g.GuestID, pid: pid, at: at})
	}
	sort.SliceStable(run, func(i, j int) bool {
		if run[i].at != run[j].at {
			return run[i].at > run[j].at
		}
		return run[i].id < run[j].id
	})
	for i, r := range run {
		adj := max(OOMAdjFloor, OOMAdjNewest-i*OOMAdjStep)
		if err := m.d.SetOOMScoreAdj(r.pid, adj); err != nil {
			m.d.Log.Warn("oom order not set", "event", "guest_oom_order", "guest_id", r.id, "err", err.Error())
			continue
		}
		m.d.Log.Debug("oom order set", "event", "guest_oom_order", "guest_id", r.id, "oom_score_adj", adj)
	}
}

// ownedReclaim drops page caches on an owned host after a sample: once
// for a guest idle IdleSamples samples in a row, and for every running
// guest when the host's free memory is under PressureFreeBytes. cpu is
// each sampled guest's CPU time since the last sample, for the running
// guests that answered one. Each drop runs on its own goroutine: the
// sampler never waits on a guest.
func (m *Manager) ownedReclaim(ctx context.Context, cpu map[string]uint64) {
	if !m.cfg.Owned {
		return
	}
	o := &m.owned
	now := m.d.Now()
	press := m.FreeMemBytes() < PressureFreeBytes
	o.mu.Lock()
	secs := now.Sub(o.lastSample).Seconds()
	first := o.lastSample.IsZero()
	o.lastSample = now
	if first || secs <= 0 {
		// The first sample's CPU deltas start from nothing.
		o.mu.Unlock()
		return
	}
	if press && now.Sub(o.lastPress) < PressureEvery {
		press = false
	}
	if press {
		o.lastPress = now
	}
	var idle, all []string
	for id, ns := range cpu {
		all = append(all, id)
		if float64(ns) < IdleCPUShare*secs*1e9 {
			o.idle[id]++
		} else {
			o.idle[id], o.dropped[id] = 0, false
		}
		if o.idle[id] >= IdleSamples && !o.dropped[id] {
			o.dropped[id] = true
			idle = append(idle, id)
		}
	}
	for id := range o.idle {
		if _, ok := cpu[id]; !ok {
			delete(o.idle, id)
			delete(o.dropped, id)
		}
	}
	o.mu.Unlock()
	targets, why := idle, "idle"
	if press {
		targets, why = all, "pressure"
	}
	sort.Strings(targets)
	for _, id := range targets {
		go m.dropGuestCache(ctx, id, why)
	}
}

// dropGuestCache asks a guest with the balloon to write back and drop its
// page cache and then compact its free memory, so free page reporting
// hands the pages back to the host (the pause's write-back, pause.go).
// The compaction is the larger half: free page reporting returns only
// free blocks of 2 MiB or more (page_reporting_order 9), and on the M4 a
// large guest idle after builds held 6.28 GiB on the host, 4.24 after the
// drop and 1.82 after the compaction (the lab, 2026-11-25). A guest
// without the balloon keeps its cache: its dropped pages would stay with
// the hypervisor anyway.
func (m *Manager) dropGuestCache(ctx context.Context, id, why string) {
	if !m.argsHaveBalloon(id) || m.restingMark(id) != nil {
		return
	}
	sess, err := m.session(id)
	if err != nil {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, PrepareTimeout)
	defer cancel()
	r, xerr := sess.Exec(dctx, []string{"/bin/sh", "-c", dropScript}, uint32(PrepareTimeout/time.Second), "")
	result := "ok"
	if xerr != nil || r.GetExitCode() != 0 {
		result = "failed"
	}
	m.d.Log.Info("guest page cache dropped", "event", "guest_reclaim", "guest_id", id, "reason", why, "result", result)
}

// oomExit reports whether a guest unit that is no longer active was ended
// by the OOM killer: systemd sets Result=oom-kill when the kernel killed
// a process of the unit, whichever OOM it was (the guest's own, the
// slice's or the host's).
func (m *Manager) oomExit(ctx context.Context, g *state.Guest) bool {
	props, err := m.d.Systemd.Show(ctx, GuestUnit(g.GuestID), "Result")
	return err == nil && props["Result"] == "oom-kill"
}

// oomStopped records a guest the OOM killer took on an owned host: torn
// down like any dead guest, and `stopped` with ReasonOOMKilled rather
// than `error`, since nothing is wrong with it and a start brings it back.
func (m *Manager) oomStopped(ctx context.Context, id string) {
	g, err := m.d.State.GetGuest(id)
	if err != nil || g.State != StateRunning {
		return
	}
	if m.Stopping(ctx) {
		m.log(g).Warn("guest stopped by the host shutdown", "event", "guest_interrupted")
		return
	}
	m.log(g).Warn("guest stopped by the OOM killer", "event", "guest_state", "reason", ReasonOOMKilled)
	m.teardown(ctx, g)
	_ = m.setState(g, StateStopped, ReasonOOMKilled) // logged inside; nothing else to do on failure
}
