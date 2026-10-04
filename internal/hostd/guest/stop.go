package guest

import (
	"context"
	"os"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/state"
)

func (m *Manager) stopCmd(ctx context.Context, c *hostdv1.StopGuest) (*hostdv1.StopResult, *Error) {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	res := &hostdv1.StopResult{}
	switch g.State {
	case StateStopped:
		return res, nil
	case StateRunning, StateStarting, StateError, StateStopping:
	default:
		return nil, errf(CodeInvalidArgument, "guest is %s; stop needs running", g.State)
	}
	if !c.SnapshotFirst || g.State != StateRunning {
		if err := m.stopGuest(ctx, g, c.TimeoutS); err != nil {
			return nil, err
		}
		return res, nil
	}
	// The snapshot is fixed once taken, so the guest shuts down while it
	// uploads: the stop waits for the longer of the two, not their sum,
	// and the guest's hours end at the freeze instead of after the upload
	// (DECISIONS I-404). A freeze that fails stops nothing, as before.
	t, err := m.takeSnapshot(ctx, g, "stop")
	if err != nil {
		return nil, err
	}
	type upload struct {
		sr  *hostdv1.SnapshotResult
		err *Error
	}
	up := make(chan upload, 1)
	go func() {
		sr, err := m.uploadSnapshot(ctx, g, t)
		up <- upload{sr, err}
	}()
	stopErr := m.stopGuest(ctx, g, c.TimeoutS)
	u := <-up
	if u.err != nil && stopErr == nil {
		// The guest is down now: snapshot the stopped volume instead, the
		// path a stop takes when guestd cannot freeze (I-158).
		m.log(g).Warn("snapshot during stop failed; snapshotting the stopped volume", "event", "snapshot_fail", "code", u.err.Code)
		u.sr, u.err = m.snapshotGuest(ctx, g, "stop")
	}
	if stopErr != nil {
		return nil, stopErr
	}
	if u.err != nil {
		return nil, u.err
	}
	res.SnapshotId, res.BlobPath, res.Bytes, res.Sha256 = u.sr.SnapshotId, u.sr.BlobPath, u.sr.Bytes, u.sr.Sha256
	return res, nil
}

// stopGuest shuts a guest down: guestd Shutdown, then the CH API, then
// SIGKILL, then teardown. The volume, root and address remain.
func (m *Manager) stopGuest(ctx context.Context, g *state.Guest, timeoutS uint32) *Error {
	if timeoutS == 0 {
		timeoutS = m.cfg.StopTimeoutS
	}
	if err := m.setState(g, StateStopping, ""); err != nil {
		return errf(CodeInternal, "%v", err)
	}
	m.log(g).Info("stopping guest", "event", "guest_stop", "timeout_s", timeoutS)
	unit := GuestUnit(g.GuestID)
	// The monitor must not mistake this for an unexpected exit. Console
	// capture stays until the hypervisor is gone: ending it while the guest
	// prints its shutdown killed Cloud Hypervisor's serial thread, stalled
	// the guest's console and with it PID 1, and every such stop waited out
	// the whole timeout (DECISIONS I-186).
	var stopConsole func()
	if mon := m.removeMonitor(g.GuestID); mon != nil {
		if sess := mon.current(); sess != nil {
			if err := sess.Shutdown(ctx, timeoutS); err != nil {
				m.log(g).Warn("guestd shutdown request failed", "event", "guest_stop", "err", err.Error())
			}
		}
		stopConsole = mon.stopKeepConsole()
	}
	defer func() {
		if stopConsole != nil {
			stopConsole()
		}
	}()
	if active, _ := m.d.Systemd.IsActive(ctx, unit); active {
		wctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
		err := m.d.Systemd.WaitInactive(wctx, unit)
		cancel()
		if err != nil {
			m.log(g).Warn("guest did not power off; asking the hypervisor", "event", "guest_stop")
			_ = m.d.CH.Shutdown(ctx, ch.APISocket(m.guestDir(g.GuestID))) // the next step kills the unit if this did nothing
			wctx, cancel = context.WithTimeout(ctx, 15*time.Second)
			err = m.d.Systemd.WaitInactive(wctx, unit)
			cancel()
			if err != nil {
				m.log(g).Warn("killing hypervisor", "event", "guest_stop")
				if err := m.d.Systemd.Kill(ctx, unit); err != nil {
					return errf(CodeInternal, "systemctl kill: %v", err)
				}
			}
		}
	}
	if stopConsole != nil {
		stopConsole()
		stopConsole = nil
	}
	m.teardown(ctx, g)
	if err := m.setState(g, StateStopped, ""); err != nil {
		return errf(CodeInternal, "%v", err)
	}
	return nil
}

func (m *Manager) destroy(ctx context.Context, c *hostdv1.DestroyGuest) *Error {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	if g.State == StateRunning || g.State == StateStarting || g.State == StateStopping {
		if err := m.stopGuest(ctx, g, 0); err != nil {
			return err
		}
	}
	if err := m.setState(g, StateDestroying, ""); err != nil {
		return errf(CodeInternal, "%v", err)
	}
	m.log(g).Info("destroying guest", "event", "guest_destroy", "keep_volume", c.KeepVolume)
	// The guest's last bytes were metered by the stop's final reading of
	// its tap (finalSample, DECISIONS I-448); the nft counter is not read
	// again, since its bytes since the last tick are the same bytes.
	if err := m.d.Net.DelCounter(ctx, g.GuestID); err != nil {
		return errf(CodeInternal, "nft delete counter: %v", err)
	}
	if !c.KeepVolume {
		if err := m.d.LVM.RemoveVolume(ctx, VolumeName(g.GuestID)); err != nil {
			return errf(CodeInternal, "lvremove: %v", err)
		}
	}
	if err := m.d.Roots.Remove(g.GuestID); err != nil {
		return errf(CodeInternal, "gcroot: %v", err)
	}
	// The project's revision roots go with the guest (host-conventions.md:
	// "removed on destroy"; DECISIONS I-115). A restore of a destroyed
	// project rebuilds its closure instead of expecting it in the store.
	if _, err := m.d.Roots.PruneRevisions(g.ProjectID, 0); err != nil {
		return errf(CodeInternal, "gcroot revisions: %v", err)
	}
	if err := m.d.State.ReleaseIndex(g.GuestID); err != nil {
		return errf(CodeInternal, "release address: %v", err)
	}
	if err := os.RemoveAll(m.guestDir(g.GuestID)); err != nil {
		return errf(CodeInternal, "remove guest dir: %v", err)
	}
	m.forgetSecrets(g.GuestID)
	m.curMu.Lock()
	delete(m.last, g.GuestID)
	m.curMu.Unlock()
	g.State = StateDestroyed
	m.emitEvent(&hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{GuestId: g.GuestID, State: StateDestroyed}})
	m.log(g).Info("guest destroyed", "event", "guest_state", "state", StateDestroyed)
	if err := m.d.State.DeleteGuest(g.GuestID); err != nil {
		return errf(CodeInternal, "state delete: %v", err)
	}
	m.refreshGuestGauge()
	return nil
}

func (m *Manager) resize(ctx context.Context, c *hostdv1.ResizeVolume) *Error {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return gerr
	}
	if c.NewBytes < g.VolumeBytes {
		return errf(CodeInvalidArgument, "volumes only grow")
	}
	if c.NewBytes > m.cfg.MaxVolumeBytes {
		return errf(CodeInvalidArgument, "volume_bytes %d above the %d maximum", c.NewBytes, m.cfg.MaxVolumeBytes)
	}
	if c.NewBytes > g.VolumeBytes {
		if err := m.poolBudget(VolumeName(g.GuestID), c.NewBytes); err != nil {
			return err
		}
		if err := m.d.LVM.ExtendVolume(ctx, VolumeName(g.GuestID), c.NewBytes); err != nil {
			return errf(CodeInternal, "lvextend: %v", err)
		}
		g.VolumeBytes = c.NewBytes
		if err := m.d.State.PutGuest(g); err != nil {
			return errf(CodeInternal, "state write: %v", err)
		}
		m.writeGuestJSON(g)
	}
	if g.State == StateRunning {
		// The hypervisor read the volume's size when the guest started;
		// tell it about the new one before asking the guest to grow.
		if err := m.d.CH.ResizeDisk(ctx, ch.APISocket(m.guestDir(g.GuestID)), ch.DiskID, c.NewBytes); err != nil {
			return errf(CodeInternal, "cloud-hypervisor resize-disk: %v", err)
		}
		sess, err := m.session(g.GuestID)
		if err != nil {
			return err
		}
		if _, err := sess.GrowFs(ctx); err != nil {
			return errf(CodeInternal, "guestd GrowFs: %v", err)
		}
	}
	m.log(g).Info("volume resized", "event", "guest_resize", "volume_bytes", c.NewBytes)
	return nil
}

func (m *Manager) drain(context.Context) *Error {
	if err := m.d.State.SetDraining(true); err != nil {
		return errf(CodeInternal, "state write: %v", err)
	}
	m.d.Log.Info("host draining", "event", "host_drain")
	return nil
}

// Undrain clears the drain flag (operator subcommand).
func (m *Manager) Undrain() error {
	return m.d.State.SetDraining(false)
}
