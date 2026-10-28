package guest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/storeview"
	"github.com/heracraft/repose/internal/hostd/virtiofs"
)

// Reconcile aligns bbolt with the host at start: a guest recorded as
// running whose unit is gone becomes stopped, a guest mid-transition is
// settled by what its unit says, running guests get their monitors back,
// and leftovers of stopped guests are torn down. Units for guests bbolt
// does not know are logged as orphans and adopted from guest.json when
// one exists.
//
// When the host's boot id differs from the one the last Reconcile
// recorded, the host restarted in between: a guest bbolt says was running
// or starting, with no unit now, is marked Interrupted, which Hello
// carries so the api starts it again (DECISIONS I-645). The boot id is
// written after the guests, so a Reconcile cut short marks the same
// guests when it runs again; the first Reconcile that records one marks
// nothing.
func (m *Manager) Reconcile(ctx context.Context) error {
	// Frozen guests come back first, so the units listed below include
	// them and the Hello after this says they run (I-652).
	m.ThawAll(ctx)
	units, err := m.d.Systemd.ListUnits(ctx, "guest@*")
	if err != nil {
		return err
	}
	bootID, rebooted := m.hostBoot()
	stoppedAt, _ := m.d.State.HostdStoppedAt() // unreadable reads as no graceful stop: nothing extra is marked
	active := map[string]bool{}
	for _, u := range units {
		id := strings.TrimSuffix(strings.TrimPrefix(u, "guest@"), ".service")
		active[id] = true
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return err
	}
	// The pool is read once: after a host reboot it may still be down (the
	// pool unit failed or is retrying), and then a guest whose unit is
	// gone is stopped, never error (DECISIONS I-644).
	poolUp, pool := m.poolAvailable(ctx)
	m.setPoolGauge(poolUp)
	if !poolUp {
		m.d.Log.Warn("thin pool unavailable at reconcile", "event", "pool_unavailable", "reason", pool.Reason())
	}
	known := map[string]bool{}
	for _, g := range gs {
		known[g.GuestID] = true
		if rebooted && !active[g.GuestID] {
			m.markInterrupted(g, bootID, stoppedAt)
		}
		m.reconcileGuest(ctx, g, active[g.GuestID], poolUp)
	}
	for id := range active {
		if known[id] {
			continue
		}
		m.d.Log.Warn("orphan guest unit", "event", "reconcile_orphan", "guest_id", id)
		if g := m.guestFromDisk(id); g != nil {
			if _, err := m.d.State.AllocIndex(g.GuestID, m.maxIndex); err == nil {
				_ = m.d.State.PutGuest(g) // adopted from guest.json; a failed write is retried at the next reconcile
				m.reconcileGuest(ctx, g, true, poolUp)
			}
		}
	}
	m.recordBoot(bootID)
	if !stoppedAt.IsZero() {
		_ = m.d.State.SetHostdStoppedAt(time.Time{}) // read once; a failed clear only widens the next reboot's look back
	}
	m.pruneInterrupted()
	m.flagWholeStore(ctx)
	// A down pool has no snapshot volumes to remove, and removing would
	// only fail; the next reconcile with the pool up sweeps them.
	if poolUp {
		m.sweepStaleSnapshots(ctx)
	}
	m.SweepAuthShares(ctx)
	m.refreshGuestGauge()
	return nil
}

// InterruptedRecent is how long after a host restart an interrupted guest
// counts in repose_host_guests_interrupted: the api's window for starting
// it again (DECISIONS I-645).
const InterruptedRecent = 6 * time.Hour

// InterruptedKeep is how long a mark nothing cleared is kept.
const InterruptedKeep = 7 * 24 * time.Hour

// hostBoot reads the current boot id and whether it differs from the one
// recorded: "" and false when either is unknown or none was recorded.
func (m *Manager) hostBoot() (string, bool) {
	if m.d.BootID == nil {
		return "", false
	}
	cur, err := m.d.BootID()
	if err != nil {
		m.d.Log.Warn("host boot id unreadable; no guest is marked interrupted", "event", "host_boot", "err", err.Error())
		return "", false
	}
	prev, err := m.d.State.HostBootID()
	if err != nil {
		m.d.Log.Warn("recorded host boot id unreadable; no guest is marked interrupted", "event", "host_boot", "err", err.Error())
		return cur, false
	}
	return cur, prev != "" && prev != cur
}

// markInterrupted records that a host restart took g down, before
// reconcileGuest settles its state, so both are written together. Only a
// guest that was running or starting is marked: one creating or restoring
// has an open op whose command the api resends, and one stopping was
// being stopped by its owner. A guest in `error` is marked too when
// hostd's monitor saw it exit at most ShutdownRace before hostd itself
// stopped gracefully (stoppedAt): the host's shutdown stopped its unit
// while hostd still ran. That guest was running; the L5 shutdown flag
// (I-650) keeps such a guest `running` instead, and this is the fallback.
func (m *Manager) markInterrupted(g *state.Guest, bootID string, stoppedAt time.Time) {
	prev := g.State
	switch {
	case g.State == StateRunning, g.State == StateStarting:
	case g.State == StateError && exitedAtShutdown(g, stoppedAt):
		prev = StateRunning
	default:
		return
	}
	if g.Interrupted != nil && g.Interrupted.BootID == bootID {
		return
	}
	g.Interrupted = &state.Interrupted{BootID: bootID, At: m.d.Now().UTC(), PrevState: prev}
	m.log(g).Warn("guest interrupted by a host restart", "event", "guest_interrupted", "boot_id", bootID, "prev", prev)
	if g.State == StateError {
		// reconcileGuest writes nothing for a guest in error.
		if err := m.d.State.PutGuest(g); err != nil {
			m.log(g).Warn("interrupted mark not written", "event", "guest_interrupted", "err", err.Error())
			return
		}
		m.writeGuestJSON(g)
	}
}

// ShutdownRace is how long before hostd's graceful stop a guest's exit
// counts as the host shutdown's: guest@ units get 90 s to stop.
const ShutdownRace = 2 * time.Minute

// The reasons unexpectedExit (create.go) records.
const (
	ReasonHypervisorExited = "hypervisor exited"
	ReasonVirtiofsdExited  = "virtiofsd exited"
)

// exitedAtShutdown reports whether g went to error because hostd's
// monitor saw its hypervisor or virtiofsd exit (unexpectedExit's
// reasons) within ShutdownRace before hostd stopped at stoppedAt.
func exitedAtShutdown(g *state.Guest, stoppedAt time.Time) bool {
	if stoppedAt.IsZero() || (!strings.HasPrefix(g.Reason, ReasonHypervisorExited) && g.Reason != ReasonVirtiofsdExited) {
		return false
	}
	return !g.UpdatedAt.Before(stoppedAt.Add(-ShutdownRace)) && !g.UpdatedAt.After(stoppedAt)
}

// RecordStop records that hostd is stopping gracefully, after Close, so
// the next Reconcile can tell a guest exit during a host shutdown from a
// crash before it.
func (m *Manager) RecordStop() {
	if err := m.d.State.SetHostdStoppedAt(time.Now().UTC()); err != nil {
		m.d.Log.Warn("hostd stop time not recorded", "event", "host_boot", "err", err.Error())
	}
}

// recordBoot writes the boot id this Reconcile ran under, once per boot.
func (m *Manager) recordBoot(bootID string) {
	if bootID == "" {
		return
	}
	prev, err := m.d.State.HostBootID()
	if err != nil || prev == bootID {
		return
	}
	if err := m.d.State.SetHostBootID(bootID); err != nil {
		m.d.Log.Warn("host boot id not recorded", "event", "host_boot", "err", err.Error())
		return
	}
	m.d.Log.Info("host boot recorded", "event", "host_boot", "boot_id", bootID, "prev_boot_id", prev)
}

// pruneInterrupted drops marks older than InterruptedKeep that nothing
// cleared: the api's window has long passed.
func (m *Manager) pruneInterrupted() {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return
	}
	for _, g := range gs {
		if g.Interrupted == nil || m.d.Now().Sub(g.Interrupted.At) < InterruptedKeep {
			continue
		}
		g.Interrupted = nil
		if err := m.d.State.PutGuest(g); err != nil {
			m.log(g).Warn("old interrupted mark not pruned", "event", "guest_interrupted", "err", err.Error())
			continue
		}
		m.writeGuestJSON(g)
	}
}

// flagWholeStore logs every running guest whose virtiofsd still shares the
// whole host store: one started before I-463, which keeps that export until
// it restarts. The host switch runbook restarts them (`hostd guests` shows
// them as whole-store); this line is how a missed one is found.
func (m *Manager) flagWholeStore(ctx context.Context) {
	if m.cfg.StoreExport != storeview.Dir || m.d.View == nil {
		return
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return
	}
	for _, g := range gs {
		if g.State != StateRunning {
			continue
		}
		if s, err := m.d.View.Serves(ctx, virtiofs.Unit(g.GuestID)); err == nil && s == storeview.ServesWhole {
			m.log(g).Warn("guest shares the whole store; restart it", "event", "store_view_restart_needed")
		}
	}
}

// sweepStaleSnapshots removes LVM snapshot volumes left by a hostd that
// died between `lvcreate -s` and `lvremove` (DECISIONS I-68): the api
// re-sends the interrupted Snapshot command and the re-run makes its own.
// A fork point (`fp-*`, I-701) is one too: its expiry timer died with the
// hostd that took it, and a sibling restore sent again takes a new one.
//
// It skips the volumes this hostd is taking or uploading now (I-906).
// The rule used to be "at start no snapshot is in flight, so every
// `snap-*` volume is an orphan", which was read as true on every
// reconcile; Reconcile is reachable while one is in flight, from the
// operator's `POST /reconcile` and from the one after a thaw, and the
// sweep would have pulled a live snapshot out from under its own
// upload. A mark cannot outlive the process that holds it, so after a
// hostd that died there is nothing marked and the sweep still takes
// everything.
//
// Running it on every reconcile, rather than only at start, is the
// other half: a RemoveVolume that failed was logged once and then left,
// and a thin snapshot kept behind accrues pool blocks for as long as
// the origin diverges from it, with nothing to come back for it on a
// hostd that never restarts.
func (m *Manager) sweepStaleSnapshots(ctx context.Context) {
	vols, err := m.d.LVM.ListVolumes(ctx)
	if err != nil {
		m.d.Log.Warn("could not list volumes for the snapshot sweep", "event", "reconcile_snapshots", "err", err.Error())
		return
	}
	for _, v := range vols {
		if !strings.HasPrefix(v, "snap-") && !strings.HasPrefix(v, "fp-") {
			continue
		}
		if m.snapBusy(v) {
			continue
		}
		if err := m.d.LVM.RemoveVolume(ctx, v); err != nil {
			m.d.Log.Warn("stale snapshot volume not removed", "event", "reconcile_snapshots", "volume", v, "err", err.Error())
			continue
		}
		m.d.Log.Warn("removed stale snapshot volume", "event", "reconcile_snapshots", "volume", v)
	}
}

func (m *Manager) reconcileGuest(ctx context.Context, g *state.Guest, unitActive, poolUp bool) {
	m.reconcileGood(g, g.State == StateRunning && unitActive)
	switch g.State {
	case StateRunning, StateStarting, StateCreating, StateStopping:
		if unitActive {
			if g.State != StateRunning {
				_ = m.setState(g, StateRunning, "reconciled: hypervisor running") // logged inside; nothing else to do on failure
			}
			m.reconcileResting(ctx, g)
			m.reshape(ctx, g)
			m.startMonitor(g)
			return
		}
		m.teardown(ctx, g)
		if !poolUp {
			_ = m.setState(g, StateStopped, "reconciled: thin pool unavailable") // see above
			return
		}
		// error only when lvs says the volume is not there; an lvs that
		// failed (a VG gone mid-reconcile) leaves the guest stopped.
		if vol, err := m.d.LVM.VolumeExists(ctx, VolumeName(g.GuestID)); err == nil && !vol {
			_ = m.setState(g, StateError, "reconciled: volume missing") // see above
			return
		}
		_ = m.setState(g, StateStopped, "reconciled: hypervisor not running after hostd restart") // see above
	case StateRestoring:
		_ = m.setState(g, StateError, "restore interrupted by hostd restart; api must retry Restore") // see above
	case StateDestroying:
		_ = m.setState(g, StateError, "destroy interrupted by hostd restart; api must resend DestroyGuest") // see above
	case StateStopped, StateError:
		if unitActive {
			// The hypervisor outlived a state write; it is running, say so.
			_ = m.setState(g, StateRunning, "reconciled: hypervisor running") // see above
			m.reshape(ctx, g)
			m.startMonitor(g)
			return
		}
		if ok, _ := m.d.Net.TapExists(ctx, g.Tap); ok {
			m.teardown(ctx, g)
		}
		if v, _ := m.d.Systemd.IsActive(ctx, virtiofs.Unit(g.GuestID)); v {
			_ = virtiofs.Stop(ctx, m.d.Systemd, g.GuestID) // a stray virtiofsd; nothing depends on it
		}
		if v, _ := m.d.Systemd.IsActive(ctx, virtiofs.AuthUnit(g.GuestID)); v {
			_ = virtiofs.StopAuth(ctx, m.d.Systemd, g.GuestID) // the same for the login share
		}
	}
}

// reconcileResting settles the idle pause's marker against the
// hypervisor at hostd's start (I-818), so a guest that was resting is
// still reported resting in Hello and its timer wake is armed again.
// vm.info is read for a guest the record says rests: one whose vCPUs run
// lost its pause (a freeze resumed it, or a `hostd thaw` did), and the
// mark goes, because a mark nothing cleared would silence that guest's
// samples for good.
//
// A guest whose vm.info says Paused with NO mark is adopted as resting
// rather than left. Two ways it happens, and both are a tenant's
// processes: hostd killed between vm.pause returning and the record being
// written (a host switch's `systemctl restart hostd`, an OOM kill, a
// panic), and the leftover of a freeze that did not finish. ThawAll runs
// at the top of Reconcile and owns the second, so by here it has had its
// turn; what is left is a paused guest nothing would ever resume, which
// reported as plain `running` ends as a `repose start` that cold-boots
// it. Adopting it costs nothing if it was the freeze's: the next thaw or
// wake clears the mark either way.
func (m *Manager) reconcileResting(ctx context.Context, g *state.Guest) {
	if g.State != StateRunning {
		return
	}
	sock := ch.APISocket(m.guestDir(g.GuestID))
	if g.Resting == nil {
		info, err := m.d.CH.Info(ctx, sock)
		if err != nil || ch.VMState(info) != ch.StatePaused {
			return
		}
		mark := &state.Resting{Since: m.d.Now(), Reason: "adopted"}
		if err := m.setResting(g, mark, "adopted"); err != nil {
			m.log(g).Warn("paused guest not marked at reconcile", "event", "guest_resting", "err", err.Error())
			return
		}
		m.armTimerWake(g)
		m.log(g).Warn("adopted a paused guest with no record; it rests until something wakes it",
			"event", "guest_resting", "resting", true, "reason", "adopted")
		return
	}
	info, err := m.d.CH.Info(ctx, sock)
	if err == nil && ch.VMState(info) != ch.StatePaused {
		if err := m.setResting(g, nil, "reconciled"); err != nil {
			m.log(g).Warn("resting mark not cleared at reconcile", "event", "guest_resting", "err", err.Error())
		}
		return
	}
	// Still paused, or a hypervisor that cannot be asked: the mark
	// stands, and the next sample and the next pause read it again.
	m.noteResting(g.GuestID, g.Resting)
	m.armTimerWake(g)
	m.log(g).Info("guest is still resting", "event", "guest_resting", "resting", true, "reason", "reconciled")
}

// reconcileGood keeps a guest's last good closure rooted (I-590), and
// gives a guest found running with none, one running across the hostd
// upgrade that added it, the closure it runs: guestd answered on it.
func (m *Manager) reconcileGood(g *state.Guest, running bool) {
	if g.LastGoodClosure == "" {
		if !running || g.SystemClosure == "" {
			return
		}
		m.markGood(g, g.SystemClosure)
		if g.LastGoodClosure != "" {
			if err := m.d.State.PutGuest(g); err != nil {
				m.log(g).Warn("last good closure not recorded", "event", "boot_fallback", "err", err.Error())
				return
			}
			m.writeGuestJSON(g)
		}
		return
	}
	if t, err := m.d.Roots.Get(goodRoot(g.GuestID)); err == nil && t == g.LastGoodClosure {
		return
	}
	if err := m.d.Roots.Set(goodRoot(g.GuestID), g.LastGoodClosure); err != nil {
		m.log(g).Warn("last good closure not rooted", "event", "boot_fallback", "err", err.Error())
	}
}

// reshape re-applies a running guest's egress shape, so a hostd upgrade
// that changes it (DECISIONS I-217 moved it from the tap's root to its
// ingress) reaches guests that were already running. Shape replaces in
// place; connections survive. A failure leaves the old shape and is
// retried at the next start of hostd. The guest's nft rules and counters
// are re-applied the same way (AddGuestRules adds only what is missing),
// so a guest running across the upgrade that added the blocked-attempt
// counters (DECISIONS I-238..I-240) is counted without a restart; the
// blocks themselves are the host's and apply to it already.
func (m *Manager) reshape(ctx context.Context, g *state.Guest) {
	if err := m.d.Net.Shape(ctx, g.Tap, m.cfg.EgressMbit, m.cfg.DownloadMbit); err != nil {
		m.d.Log.Warn("egress shape not re-applied", "event", "reconcile_shape", "guest_id", g.GuestID, "err", err.Error())
	}
	if err := m.d.Net.AddGuestRules(ctx, g.GuestID, g.IP, g.MAC, g.Tap); err != nil {
		m.d.Log.Warn("guest rules not re-applied", "event", "reconcile_rules", "guest_id", g.GuestID, "err", err.Error())
	}
}

func (m *Manager) guestFromDisk(id string) *state.Guest {
	b, err := os.ReadFile(filepath.Join(m.guestDir(id), "guest.json"))
	if err != nil {
		return nil
	}
	g := &state.Guest{}
	if err := json.Unmarshal(b, g); err != nil || g.GuestID != id {
		return nil
	}
	return g
}

// Rebuild reconstructs the guest table from disk (guest.json in every
// guest directory, volumes, units) for `hostd reconcile` after a lost
// state.db. Returns the ids restored.
func (m *Manager) Rebuild(ctx context.Context) ([]string, error) {
	des, err := os.ReadDir(m.cfg.GuestsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var ids []string
	for _, d := range des {
		if !d.IsDir() {
			continue
		}
		g := m.guestFromDisk(d.Name())
		if g == nil {
			continue
		}
		if _, err := m.d.State.GetGuest(g.GuestID); err == nil {
			continue
		}
		if err := m.d.State.ClaimIndex(g.GuestID, g.IPIndex); err != nil {
			m.d.Log.Warn("rebuild: address in use", "event", "reconcile_rebuild", "guest_id", g.GuestID, "err", err.Error())
			continue
		}
		if err := m.d.State.PutGuest(g); err != nil {
			return ids, err
		}
		ids = append(ids, g.GuestID)
	}
	return ids, m.Reconcile(ctx)
}
