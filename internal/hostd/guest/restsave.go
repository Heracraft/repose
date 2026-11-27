package guest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	if m.d.RestSaveAfter > 0 && (m.d.MemoryTight == nil || !m.d.MemoryTight()) {
		// The first step of the rest: a pause. SaveRests saves it once
		// it has rested RestSaveAfter, or at once when macOS is short.
		return nil
	}
	m.dropCleanCache(ctx, g, sock)
	m.saveRest(ctx, g, sock)
	return nil
}

// dropCleanCache asks a paused guest's kernel to drop its clean page
// cache before a save, so the save holds what the guest uses and not the
// files a build read (images of 1.1-2.2 GB and restores of 9-31 s after
// builds, d2-lab 2026-11-27): the VM is resumed for the call and paused
// again. Best effort; a guest that does not answer is saved as it is.
func (m *Manager) dropCleanCache(ctx context.Context, g *state.Guest, sock string) {
	if err := m.d.CH.Resume(ctx, sock); err != nil {
		return
	}
	if sess, err := m.session(g.GuestID); err == nil {
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if r, err := sess.Exec(dctx, []string{"/bin/sh", "-c", "sync; echo 1 > /proc/sys/vm/drop_caches"}, 10, ""); err != nil || r.ExitCode != 0 {
			m.log(g).Warn("page cache not dropped before the rest save", "event", "guest_resting")
		}
		cancel()
	}
	if err := m.d.CH.Pause(ctx, sock); err != nil {
		m.log(g).Warn("guest not paused again after dropping its page cache", "event", "guest_resting", "err", err.Error())
	}
}

// saveRest saves a paused guest to restDir and ends its hypervisor,
// giving its memory back. A save that fails leaves it paused, which is a
// rest as before; it reports whether the guest now rests as a save.
func (m *Manager) saveRest(ctx context.Context, g *state.Guest, sock string) bool {
	fz, ok := m.d.CH.(ch.Parker)
	if !ok {
		return false
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
		return false
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
	return true
}

// restResume is the wake's hypervisor step: a guest resting as a save
// file is restored from it first; then vm.resume as for any rest.
func (m *Manager) restResume(ctx context.Context, g *state.Guest, sock string) error {
	if !m.restSaved(g) {
		return m.resumeVM(ctx, g, sock)
	}
	restored, err := m.restoreRest(ctx, g, sock)
	if err != nil {
		return err
	}
	t0 := m.d.Now()
	if err := m.resumeVM(ctx, g, sock); err != nil {
		return err
	}
	m.log(g).Info("guest restored from its rest save", "event", "guest_resting",
		"restore_ms", restored.Milliseconds(), "total_ms", (restored + m.d.Now().Sub(t0)).Milliseconds())
	return nil
}

// restoreRest starts a guest resting as a save file from it, its vCPUs
// still stopped: afterwards it rests by pause, its memory held, and the
// save is removed. It returns how long the restore took.
func (m *Manager) restoreRest(ctx context.Context, g *state.Guest, sock string) (time.Duration, error) {
	t0 := m.d.Now()
	dir := m.guestDir(g.GuestID)
	for _, s := range []string{"ch.sock", "vsock.sock"} {
		_ = os.Remove(filepath.Join(dir, s)) // the launcher makes them anew
	}
	props := GuestUnitProps(m.classOf(g.Class), m.cfg.GuestUser, m.cfg.GuestsDir, dir, m.d.LVM.DevPath(VolumeName(g.GuestID)), m.unitBundle(g))
	if err := m.d.Systemd.Run(ctx, GuestUnit(g.GuestID), props, ch.RestoreArgs(dir, m.restDir(g.GuestID))); err != nil {
		return 0, err
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
			if cr := m.d.Park.CanRestore; cr != nil && !cr(ctx) {
				m.wakeAtUnlock.Store(g.GuestID, true)
				return 0, ErrLockedRestore
			}
			return 0, errors.New("the hypervisor exited during the restore of the rest save")
		}
		select {
		case <-rctx.Done():
			return 0, errors.New("the rest save was not restored in time")
		case <-time.After(20 * time.Millisecond):
		}
	}
	took := m.d.Now().Sub(t0)
	_ = os.RemoveAll(m.restDir(g.GuestID)) // read into the VM; a later rest writes a new one
	// The login file's bind is renewed at the wake that resumes it (I-1666).
	m.rebindDue.Store(g.GuestID, true)
	return took, nil
}

// ErrLockedRestore is a wake refused because the Mac's screen is locked:
// VZ reads a save file only in the user's unlocked session (I-1662). The
// save stays, and the guest is woken once the session is unlocked
// (DECISIONS I-1682).
var ErrLockedRestore = errors.New("this Mac's screen is locked; the machine wakes once someone unlocks it")

// UnsaveRests is what a Mac does the moment its screen locks: every guest
// resting as a save file is restored, its vCPUs left stopped, so it rests
// by pause through the lock and the first ssh wakes it at once. VZ still
// restores for a few seconds after the lock (lab Mac, 2026-11-27, n=3:
// restored at +0 to +7.6 s, refused from +11.4 s) and never after, so
// without this a machine resting as a save is out of reach until someone
// unlocks the Mac. The guests are restored at once, each on its own wake
// lock; it returns how many were.
func (m *Manager) UnsaveRests(ctx context.Context) int {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return 0
	}
	var n atomic.Int32
	var wg sync.WaitGroup
	for _, g := range gs {
		if !m.restSaved(g) {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			l := m.wakeLock(id)
			l.Lock()
			defer l.Unlock()
			g, err := m.getGuest(id)
			if err != nil || g.State != StateRunning || !m.restSaved(g) {
				return
			}
			took, rerr := m.restoreRest(ctx, g, ch.APISocket(m.guestDir(id)))
			if rerr != nil {
				m.log(g).Warn("rest save not restored at the lock; the machine wakes once the Mac is unlocked", "event", "guest_resting", "err", rerr.Error())
				return
			}
			n.Add(1)
			m.log(g).Info("guest restored from its rest save at the lock; it rests paused", "event", "guest_resting", "restore_ms", took.Milliseconds())
		}(g.GuestID)
	}
	wg.Wait()
	return int(n.Load())
}

// SaveRests gives back the memory of guests resting by pause wherever
// RestBySave now allows it: what a Mac does once its screen has been
// unlocked a while, so the machines UnsaveRests held through the lock
// rest as save files again while the user is at the Mac. It returns how
// many were saved.
func (m *Manager) SaveRests(ctx context.Context) int {
	if m.d.RestBySave == nil {
		return 0
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return 0
	}
	tight := m.d.MemoryTight != nil && m.d.MemoryTight()
	n := 0
	for _, g := range gs {
		if g.Resting == nil || g.State != StateRunning || m.restSaved(g) || !m.d.RestBySave(ctx, g.GuestID) {
			continue
		}
		if !tight && m.d.RestSaveAfter > 0 && m.d.Now().Sub(g.Resting.Since) < m.d.RestSaveAfter {
			continue
		}
		id := g.GuestID
		l := m.wakeLock(id)
		l.Lock()
		cur, err := m.getGuest(id)
		if err == nil && cur.Resting != nil && cur.State == StateRunning && !m.restSaved(cur) && m.unitRunning(ctx, cur) {
			sock := ch.APISocket(m.guestDir(id))
			m.dropCleanCache(ctx, cur, sock)
			if m.saveRest(ctx, cur, sock) {
				n++
			}
		}
		l.Unlock()
	}
	return n
}

// WakeRefusedAtLock wakes the guests whose wake was refused because the
// Mac's screen was locked (ErrLockedRestore), once it is unlocked: the
// connection that asked has given up, and the next one finds the machine
// awake. It returns how many it woke.
func (m *Manager) WakeRefusedAtLock(ctx context.Context) int {
	n := 0
	m.wakeAtUnlock.Range(func(k, _ any) bool {
		id := k.(string)
		m.wakeAtUnlock.Delete(id)
		if _, err := m.WakeGuest(ctx, id, WakeReasonConnect); err == nil {
			n++
		}
		return true
	})
	return n
}

// waitsFor is what a guest's memory waits for on a Mac (DECISIONS
// I-1682): "unlock" while it is in a save file (a rest by save, or a held
// park) and the user's session exists but is locked, "login" while it is
// in one and nobody is logged in, "" otherwise and on every other host.
func (m *Manager) waitsFor(ctx context.Context, g *state.Guest) string {
	if m.d.Park.CanRestore == nil || m.d.SessionState == nil || (!m.restSaved(g) && !m.isHeld(g.GuestID)) {
		return ""
	}
	switch m.d.SessionState(ctx) {
	case SessionLocked:
		return WaitsForUnlock
	case SessionNone:
		return WaitsForLogin
	}
	return ""
}

// What a guest's memory waits for (GuestSample.waits_for, I-1682).
const (
	WaitsForUnlock = "unlock"
	WaitsForLogin  = "login"
)

// The user's session on a Mac, as Deps.SessionState reads it.
const (
	SessionUnlocked = "unlocked"
	SessionLocked   = "locked"
	SessionNone     = "none"
)

// ReasonRestTooLong is the stop reason of a machine whose rest save
// passed the park's age limit while the Mac could not restore it: its
// memory is gone and its next start boots it (DECISIONS I-1682).
const ReasonRestTooLong = "rested as a save more than 6 hours while this Mac was locked; it starts fresh"

// ExpireRestSaves applies the park's age limit (6 hours, I-653) to rest
// saves, which hold a guest's memory on disk as a park image does: one
// that old is restored into a paused machine where the session can read
// it, so its memory stays in memory and the file goes; where it cannot,
// the file is removed and the machine stopped with ReasonRestTooLong.
func (m *Manager) ExpireRestSaves(ctx context.Context) {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return
	}
	maxAge := m.fcfg().MaxAge
	for _, g := range gs {
		if !m.restSaved(g) {
			continue
		}
		st, err := os.Stat(filepath.Join(m.restDir(g.GuestID), "repose-vm.json"))
		if err != nil || m.d.Now().Sub(st.ModTime()) <= maxAge {
			continue
		}
		id := g.GuestID
		l := m.wakeLock(id)
		l.Lock()
		cur, gerr := m.getGuest(id)
		if gerr != nil || !m.restSaved(cur) {
			l.Unlock()
			continue
		}
		if cr := m.d.Park.CanRestore; cr == nil || cr(ctx) {
			_, rerr := m.restoreRest(ctx, cur, ch.APISocket(m.guestDir(id)))
			if rerr == nil {
				m.log(cur).Info("rest save past the age limit restored; the machine rests paused", "event", "guest_resting")
				l.Unlock()
				continue
			}
			m.log(cur).Warn("rest save past the age limit not restored", "event", "guest_resting", "err", rerr.Error())
		}
		_ = os.RemoveAll(m.restDir(id))
		m.teardown(ctx, cur)
		cur.Resting = nil
		if err := m.setState(cur, StateStopped, ReasonRestTooLong); err != nil {
			m.log(cur).Warn("rest save past the age limit: machine not stopped", "event", "guest_resting", "err", err.Error())
		} else {
			m.log(cur).Warn("rest save past the age limit removed; its memory is not kept", "event", "guest_resting")
		}
		l.Unlock()
	}
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
