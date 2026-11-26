package guest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/state"
)

// Rest by save (design 2 on a Mac, DECISIONS I-1666). VZ never gives a
// running guest's memory back to macOS, so on a host that sets
// Deps.RestBySave an idle pause goes one step further: the paused VM is
// saved to restDir and its process ends, which returns all of its memory;
// the wake restores it from that file and resumes it. Everything else
// about resting is unchanged: the resting mark, the api's terms, the
// timer wake, the refusals. A host without RestBySave pauses, as always.

// restDir holds a guest's rest save: inside its directory, beside the
// park's image.
func (m *Manager) restDir(id string) string { return filepath.Join(m.guestDir(id), "rest") }

// restSaved reports whether a guest rests as a save file, with no
// hypervisor running: reconcile and the unit monitor read it as resting,
// not as a hypervisor that died.
func (m *Manager) restSaved(g *state.Guest) bool {
	if m.d.RestBySave == nil || g.Resting == nil {
		return false
	}
	_, err := os.Stat(filepath.Join(m.restDir(g.GuestID), "repose-vm.json"))
	return err == nil
}

// restPause is the idle pause's hypervisor step: vm.pause, and on a host
// that rests by save, the save and the end of the hypervisor. A save that
// fails leaves the guest paused, which is a rest as before.
func (m *Manager) restPause(ctx context.Context, g *state.Guest, sock string) error {
	if err := m.d.CH.Pause(ctx, sock); err != nil {
		return err
	}
	if m.d.RestBySave == nil || !m.d.RestBySave(ctx, g.GuestID) {
		return nil
	}
	fz, ok := m.d.CH.(ch.Parker)
	if !ok {
		return nil
	}
	t0 := m.d.Now()
	dir := m.restDir(g.GuestID)
	_ = os.RemoveAll(dir) // a leftover of a rest that never woke; this save replaces it
	lookup := m.cfg.Lookup
	if lookup == nil {
		lookup = LookupUser
	}
	uid, gid, err := lookup(m.cfg.GuestUser)
	if err == nil {
		err = ensureDir(dir, 0o700, uid, gid)
	}
	if err == nil {
		err = fz.Snapshot(ctx, sock, dir)
	}
	if err != nil {
		_ = os.RemoveAll(dir) // a partial save is no rest; the guest stays paused
		m.log(g).Warn("rest by save failed; the guest rests paused", "event", "guest_resting", "err", err.Error())
		return nil
	}
	saved := m.d.Now()
	_ = m.d.CH.Shutdown(ctx, sock) // the VM is paused and saved; the wait below kills what does not end
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := m.d.Systemd.WaitInactive(wctx, GuestUnit(g.GuestID)); err != nil {
		_ = m.d.Systemd.Kill(ctx, GuestUnit(g.GuestID))
	}
	cancel()
	if d, ok := m.d.Net.(interface{ Detach(tap string) }); ok {
		d.Detach(g.Tap)
	}
	m.log(g).Info("guest rested by save", "event", "guest_resting", "save_ms", saved.Sub(t0).Milliseconds(),
		"total_ms", m.d.Now().Sub(t0).Milliseconds(), "image_bytes", m.imageBytes(dir))
	return nil
}

// restResume is the wake's hypervisor step: a guest resting as a save
// file is restored from it first; then vm.resume as for any rest.
func (m *Manager) restResume(ctx context.Context, g *state.Guest, sock string) error {
	if !m.restSaved(g) {
		return m.resumeVM(ctx, g, sock)
	}
	t0 := m.d.Now()
	dir := m.guestDir(g.GuestID)
	for _, s := range []string{"ch.sock", "vsock.sock"} {
		_ = os.Remove(filepath.Join(dir, s)) // the launcher makes them anew
	}
	props := GuestUnitProps(m.classOf(g.Class), m.cfg.GuestUser, m.cfg.GuestsDir, dir, m.d.LVM.DevPath(VolumeName(g.GuestID)), m.unitBundle(g))
	if err := m.d.Systemd.Run(ctx, GuestUnit(g.GuestID), props, ch.RestoreArgs(dir, m.restDir(g.GuestID))); err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, m.fcfg().RestoreTimeout)
	defer cancel()
	for {
		if _, err := m.d.CH.Info(rctx, sock); err == nil {
			break
		}
		if a, _ := m.d.Systemd.IsActive(rctx, GuestUnit(g.GuestID)); !a {
			// The save file stays: a wake once the session is unlocked
			// can still read it (a locked restore fails, I-1662).
			return errors.New("the hypervisor exited during the restore of the rest save")
		}
		select {
		case <-rctx.Done():
			return errors.New("the rest save was not restored in time")
		case <-time.After(20 * time.Millisecond):
		}
	}
	restored := m.d.Now()
	if err := m.resumeVM(ctx, g, sock); err != nil {
		return err
	}
	_ = os.RemoveAll(m.restDir(g.GuestID)) // read into the VM; a later rest writes a new one
	m.log(g).Info("guest restored from its rest save", "event", "guest_resting",
		"restore_ms", restored.Sub(t0).Milliseconds(), "total_ms", m.d.Now().Sub(t0).Milliseconds())
	return nil
}

// WakeTap wakes the resting guest behind a switch port: what a Mac's
// userspace switch calls when a connection arrives for a guest whose VM
// rests as a save file (d2-net's unet OnWake). A guest that is not
// resting is left alone.
func (m *Manager) WakeTap(ctx context.Context, tap string) (*hostdv1.WakeResult, error) {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		if g.Tap == tap {
			return m.WakeGuest(ctx, g.GuestID, WakeReasonConnect)
		}
	}
	return nil, errors.New("no guest on that port")
}
