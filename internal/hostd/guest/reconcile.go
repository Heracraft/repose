package guest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

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
func (m *Manager) Reconcile(ctx context.Context) error {
	units, err := m.d.Systemd.ListUnits(ctx, "guest@*")
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, u := range units {
		id := strings.TrimSuffix(strings.TrimPrefix(u, "guest@"), ".service")
		active[id] = true
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, g := range gs {
		known[g.GuestID] = true
		m.reconcileGuest(ctx, g, active[g.GuestID])
	}
	for id := range active {
		if known[id] {
			continue
		}
		m.d.Log.Warn("orphan guest unit", "event", "reconcile_orphan", "guest_id", id)
		if g := m.guestFromDisk(id); g != nil {
			if _, err := m.d.State.AllocIndex(g.GuestID, m.maxIndex); err == nil {
				_ = m.d.State.PutGuest(g) // adopted from guest.json; a failed write is retried at the next reconcile
				m.reconcileGuest(ctx, g, true)
			}
		}
	}
	m.flagWholeStore(ctx)
	m.sweepStaleSnapshots(ctx)
	m.SweepAuthShares(ctx)
	m.refreshGuestGauge()
	return nil
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
// died between `lvcreate -s` and `lvremove` (DECISIONS I-68). At start no
// snapshot is in flight, so every `snap-*` volume is an orphan: the api
// re-sends the interrupted Snapshot command and the re-run makes its own.
func (m *Manager) sweepStaleSnapshots(ctx context.Context) {
	vols, err := m.d.LVM.ListVolumes(ctx)
	if err != nil {
		m.d.Log.Warn("could not list volumes for the snapshot sweep", "event", "reconcile_snapshots", "err", err.Error())
		return
	}
	for _, v := range vols {
		if !strings.HasPrefix(v, "snap-") {
			continue
		}
		if err := m.d.LVM.RemoveVolume(ctx, v); err != nil {
			m.d.Log.Warn("stale snapshot volume not removed", "event", "reconcile_snapshots", "volume", v, "err", err.Error())
			continue
		}
		m.d.Log.Warn("removed stale snapshot volume", "event", "reconcile_snapshots", "volume", v)
	}
}

func (m *Manager) reconcileGuest(ctx context.Context, g *state.Guest, unitActive bool) {
	m.reconcileGood(g, g.State == StateRunning && unitActive)
	switch g.State {
	case StateRunning, StateStarting, StateCreating, StateStopping:
		if unitActive {
			if g.State != StateRunning {
				_ = m.setState(g, StateRunning, "reconciled: hypervisor running") // logged inside; nothing else to do on failure
			}
			m.reshape(ctx, g)
			m.startMonitor(g)
			return
		}
		m.teardown(ctx, g)
		if vol, _ := m.d.LVM.VolumeExists(ctx, VolumeName(g.GuestID)); !vol {
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
