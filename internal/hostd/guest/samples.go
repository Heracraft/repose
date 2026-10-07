package guest

import (
	"context"
	"strconv"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/virtiofs"
	"github.com/heracraft/repose/internal/obs"
)

// levelNotice sits between Info and Warn, for audit lines. obs owns the
// level and the name the JSON carries (NOTICE); this is the local spelling so
// the call sites read the same as before.
const levelNotice = obs.LevelNotice

// sampleCursor is a running guest's last reading of its cumulative
// counters: the hypervisor unit's CPU time and its tap's bytes, and the two
// CPU pressure totals (DECISIONS I-493), each with its own seen flag since
// either can be missing while the others read.
type sampleCursor struct {
	cpu, rx, tx  uint64
	seen         bool
	pressure     uint64
	pressureSeen bool
	wait         uint64
	waitSeen     bool
}

// maxPressureDelta bounds the guest-written pressure counter's move in one
// sample. PSI cannot pass wall time and samples are a minute apart, so five
// minutes is room for a late tick, and a guest that writes a huge total
// moves nobody's chart past it (the bound of I-446 for this field).
const maxPressureDelta = 300_000_000

// maxGuestMem bounds the guest-written memory figure: no class comes near
// a tebibyte, so a larger value is a guest's invention.
const maxGuestMem = 1 << 40

// boundRootFS keeps the guest-written root filesystem figures (DECISIONS
// I-567) when they can be true: a size no larger than the volume, and used
// no larger than the size. Anything else is a guest's invention and both
// go as 0, which the api reads as "the guest did not say".
func boundRootFS(used, size, volume uint64) (uint64, uint64) {
	if size == 0 || size > volume || used > size {
		return 0, 0
	}
	return used, size
}

// advancePressure moves a guest's CPU pressure cursor to total, the
// guest's /proc/pressure/cpu "some" total, and returns the bounded delta.
// The first reading after hostd starts sets the baseline. A zero total is
// a guest without PSI and leaves the cursor alone.
func (m *Manager) advancePressure(guestID string, total uint64) uint64 {
	if total == 0 {
		return 0
	}
	m.curMu.Lock()
	defer m.curMu.Unlock()
	cur := m.last[guestID]
	var d uint64
	if cur.pressureSeen {
		d = min(counterDelta(cur.pressure, total), maxPressureDelta)
	}
	cur.pressure, cur.pressureSeen = total, true
	m.last[guestID] = cur
	return d
}

// advanceWait moves a guest's host CPU wait cursor to total, the
// hypervisor unit cgroup's cpu.pressure "some" total, and returns the delta.
func (m *Manager) advanceWait(guestID string, total uint64) uint64 {
	m.curMu.Lock()
	defer m.curMu.Unlock()
	cur := m.last[guestID]
	var d uint64
	if cur.waitSeen {
		d = counterDelta(cur.wait, total)
	}
	cur.wait, cur.waitSeen = total, true
	m.last[guestID] = cur
	return d
}

// counterDelta is how far a cumulative counter moved since prev. A reading
// below prev is a counter that started again from zero (a new tap or a new
// unit) and counts in full.
func counterDelta(prev, cur uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}

// resetCursor starts a guest's counters at zero when its tap is created, so
// its first sample counts every byte since boot instead of setting a
// baseline (DECISIONS I-448).
func (m *Manager) resetCursor(guestID string) {
	m.curMu.Lock()
	m.last[guestID] = sampleCursor{seen: true}
	m.curMu.Unlock()
}

// advanceNet reads a guest's tap counters and moves its cursor past them,
// returning what the guest received and sent since the previous reading.
// The read and the move happen under curMu, so a tick and the final
// reading at teardown never count the same bytes twice. ok is false when
// the tap could not be read; a cursor not yet seen (hostd restarted under
// a running guest) takes the reading as its baseline.
func (m *Manager) advanceNet(guestID, tap string) (drx, dtx uint64, ok bool) {
	m.curMu.Lock()
	defer m.curMu.Unlock()
	rx, tx, err := m.d.Net.TapStats(tap)
	if err != nil {
		return 0, 0, false
	}
	cur := m.last[guestID]
	if cur.seen {
		drx, dtx = counterDelta(cur.rx, rx), counterDelta(cur.tx, tx)
	}
	cur.rx, cur.tx, cur.seen = rx, tx, true
	m.last[guestID] = cur
	return drx, dtx, true
}

// advanceCPU moves a guest's CPU cursor to cpu and returns the delta.
func (m *Manager) advanceCPU(guestID string, cpu uint64) uint64 {
	m.curMu.Lock()
	defer m.curMu.Unlock()
	cur := m.last[guestID]
	var d uint64
	if cur.seen {
		d = counterDelta(cur.cpu, cpu)
	}
	cur.cpu, cur.seen = cpu, true
	m.last[guestID] = cur
	return d
}

// sampleTs is the timestamp for a Samples message: now, but never at or
// before the last one sent, since the api keys samples on (project, second)
// and drops a second row for the same second (DECISIONS I-448).
func (m *Manager) sampleTs() int64 {
	m.curMu.Lock()
	defer m.curMu.Unlock()
	ts := m.d.Now().Unix()
	if ts <= m.lastTs {
		ts = m.lastTs + 1
	}
	m.lastTs = ts
	return ts
}

// finalSample reads a guest's tap one last time before teardown deletes it
// and sends what the guest sent and received since the last tick, so bytes
// between the last tick and the stop are metered (DECISIONS I-448). The
// sample's state is stopping: the api's hours count running samples only.
// The cursor goes with the tap; the next boot starts a new one.
func (m *Manager) finalSample(g *state.Guest) {
	drx, dtx, ok := m.advanceNet(g.GuestID, g.Tap)
	m.curMu.Lock()
	delete(m.last, g.GuestID)
	m.curMu.Unlock()
	if !ok || (drx == 0 && dtx == 0) || m.d.Emit == nil {
		return
	}
	m.d.Emit.Samples(&hostdv1.Samples{Ts: m.sampleTs(), Guests: []*hostdv1.GuestSample{{
		GuestId: g.GuestID, State: StateStopping, Class: g.Class, DiskAllocBytes: g.VolumeBytes,
		NetRxBytesDelta: drx, NetTxBytesDelta: dtx, Signals: &hostdv1.GuestSignals{},
	}}, Host: m.hostSample()})
	if m.d.Metrics != nil {
		m.d.Metrics.GuestNetBytesTotal.WithLabelValues("rx").Add(float64(drx))
		m.d.Metrics.GuestNetBytesTotal.WithLabelValues("tx").Add(float64(dtx))
	}
}

func (m *Manager) hostSample() *hostdv1.HostSample {
	hs := &hostdv1.HostSample{PoolFree: m.PoolFreeBytes()}
	if m.d.MemInfo != nil {
		if _, avail, err := m.d.MemInfo(); err == nil {
			hs.MemFree = avail
		}
	}
	if m.d.Load1 != nil {
		hs.Load1 = m.d.Load1()
	}
	m.mu.Lock()
	hs.BuildsRunning = uint32(m.buildRun)
	m.mu.Unlock()
	return hs
}

// CollectSamples builds one Samples message for every guest on the host.
func (m *Manager) CollectSamples(ctx context.Context) *hostdv1.Samples {
	s := &hostdv1.Samples{Ts: m.sampleTs(), Host: m.hostSample()}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		m.d.Log.Error("samples: state read failed", "event", "samples", "err", err.Error())
		return s
	}
	lost := 0
	for _, g := range gs {
		gsm := &hostdv1.GuestSample{GuestId: g.GuestID, State: g.State, Class: g.Class, DiskAllocBytes: g.VolumeBytes, Signals: &hostdv1.GuestSignals{}}
		if size, used, err := m.d.LVM.VolumeStats(ctx, VolumeName(g.GuestID)); err == nil {
			gsm.DiskAllocBytes, gsm.DiskUsedBytes = size, used
		}
		if g.State == StateRunning {
			if props, err := m.d.Systemd.Show(ctx, GuestUnit(g.GuestID), "CPUUsageNSec", "MemoryCurrent", "ControlGroup"); err == nil {
				cpu, _ := strconv.ParseUint(props["CPUUsageNSec"], 10, 64)
				mem, _ := strconv.ParseUint(props["MemoryCurrent"], 10, 64)
				gsm.MemRssBytes = mem
				gsm.CpuNsDelta = m.advanceCPU(g.GuestID, cpu)
				if cg := props["ControlGroup"]; cg != "" && m.d.CgroupCPUPressure != nil {
					if total, err := m.d.CgroupCPUPressure(cg); err == nil {
						gsm.HostCpuWaitUsDelta = m.advanceWait(g.GuestID, total)
					}
				}
			}
			if drx, dtx, ok := m.advanceNet(g.GuestID, g.Tap); ok {
				gsm.NetRxBytesDelta, gsm.NetTxBytesDelta = drx, dtx
			}
			if sess, err := m.session(g.GuestID); err == nil {
				sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				sr, err := sess.Sample(sctx)
				cancel()
				if err == nil && sr != nil {
					// Guest-written: bounded before it joins the host's batch (I-446).
					gsm.Signals = cleanSignals(sr.Signals)
					gsm.Signals.GuestdOk = true
					gsm.Procs = cleanProcs(sr.Procs)
					gsm.CpuPressureUsDelta = m.advancePressure(g.GuestID, sr.GetCpuPressureUsTotal())
					gsm.GuestMemUsedBytes = min(sr.GetMemUsedBytes(), maxGuestMem)
					gsm.RootUsedBytes, gsm.RootSizeBytes = boundRootFS(sr.GetRootUsedBytes(), sr.GetRootSizeBytes(), gsm.DiskAllocBytes)
				} else {
					lost++
				}
			} else {
				lost++
			}
			if m.d.Metrics != nil {
				m.d.Metrics.GuestCPUSecondsTotal.WithLabelValues(g.Class).Add(float64(gsm.CpuNsDelta) / 1e9)
				m.d.Metrics.GuestNetBytesTotal.WithLabelValues("rx").Add(float64(gsm.NetRxBytesDelta))
				m.d.Metrics.GuestNetBytesTotal.WithLabelValues("tx").Add(float64(gsm.NetTxBytesDelta))
			}
		}
		s.Guests = append(s.Guests, gsm)
	}
	if m.d.Metrics != nil {
		m.d.Metrics.GuestdLost.Set(float64(lost))
		m.d.Metrics.PoolFreeBytes.Set(float64(s.Host.PoolFree))
		if size, _, err := m.d.LVM.PoolStats(ctx); err == nil {
			m.d.Metrics.PoolBytes.Set(float64(size))
		}
		m.d.Metrics.MemFreeBytes.Set(float64(m.FreeMemBytes()))
		m.d.Metrics.MemReservedBytes.Set(float64(m.reservedBytes()))
	}
	m.sampleBlocked(ctx)
	m.poolWarning()
	return s
}

func (m *Manager) poolWarning() {
	pct, err := m.poolUsedPct()
	if err == nil && pct >= m.cfg.PoolWarnPct && m.d.Now().Sub(m.poolWarned) >= 10*time.Minute {
		m.poolWarned = m.d.Now()
		m.d.Log.Warn("thin pool high", "event", "pool_warning", "pct", int(pct))
		m.Warn("pool_high", strconv.Itoa(int(pct))+"% of the thin pool is used")
	}
	spct, ok := m.storeUsedPct()
	if ok && spct >= m.cfg.StoreHighPct && m.d.Now().Sub(m.storeWarned) >= 10*time.Minute {
		m.storeWarned = m.d.Now()
		m.d.Log.Warn("store high", "event", "store_warning", "pct", int(spct))
		m.Warn("store_high", strconv.Itoa(int(spct))+"% of the host store filesystem is used")
	}
}

// storeUsedPct is the host store filesystem's usage; ok is false when it
// cannot be read (no StoreStat in tests).
func (m *Manager) storeUsedPct() (float64, bool) {
	if m.d.StoreStat == nil {
		return 0, false
	}
	total, used, err := m.d.StoreStat()
	if err != nil || total == 0 {
		return 0, false
	}
	if m.d.Metrics != nil {
		m.d.Metrics.StoreBytes.Set(float64(used))
	}
	return float64(used) / float64(total) * 100, true
}

// SnapshotAll enqueues a scheduled Snapshot for every running guest
// without the api (the nightly timer). Returns the guest ids queued.
func (m *Manager) SnapshotAll(reason string) ([]string, error) {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, g := range gs {
		if g.State != StateRunning {
			continue
		}
		id := "local-" + reason + "-" + g.GuestID + "-" + strconv.FormatInt(m.d.Now().Unix(), 10)
		m.Dispatch(&hostdv1.Command{CommandId: id, Cmd: &hostdv1.Command_Snapshot{Snapshot: &hostdv1.Snapshot{GuestId: g.GuestID, Reason: reason}}})
		ids = append(ids, g.GuestID)
	}
	return ids, nil
}

// Status is the operator view of a guest.
type Status struct {
	GuestID   string `json:"guest_id"`
	ProjectID string `json:"project_id"`
	Class     string `json:"class"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	IP        string `json:"ip"`
	CID       uint32 `json:"vsock_cid"`
	Unit      string `json:"unit"`
	Virtiofsd string `json:"virtiofsd"`
	// Store is what the guest's virtiofsd shares: "view" (its own
	// closure, I-463) or "whole-store" (started before I-463; restart the
	// guest), empty when it is not running.
	Store    string `json:"store,omitempty"`
	GuestdOK bool   `json:"guestd_ok"`
	Closure  string `json:"system_closure"`
}

// Guests lists every guest with its live unit states.
func (m *Manager) Guests(ctx context.Context) ([]Status, error) {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return nil, err
	}
	var out []Status
	for _, g := range gs {
		st := Status{GuestID: g.GuestID, ProjectID: g.ProjectID, Class: g.Class, State: g.State, Reason: g.Reason, IP: g.IP, CID: g.CID, Closure: g.SystemClosure, Unit: "inactive", Virtiofsd: "inactive"}
		if a, _ := m.d.Systemd.IsActive(ctx, GuestUnit(g.GuestID)); a {
			st.Unit = "active"
		}
		if a, _ := m.d.Systemd.IsActive(ctx, virtiofs.Unit(g.GuestID)); a {
			st.Virtiofsd = "active"
			if m.d.View != nil {
				st.Store, _ = m.d.View.Serves(ctx, virtiofs.Unit(g.GuestID)) // a column, best effort
			}
		}
		if mon := m.monitorOf(g.GuestID); mon != nil && mon.current() != nil {
			st.GuestdOK = true
		}
		out = append(out, st)
	}
	return out, nil
}
