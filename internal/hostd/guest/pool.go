package guest

import (
	"context"
	"errors"

	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/state"
)

// CodeVolumeUnavailable is a command refused because the host's thin pool
// cannot serve the guest's volume, or the volume will not activate
// (DECISIONS I-644). The disk is intact; a start leaves the project
// stopped and works again once the pool is back.
const CodeVolumeUnavailable = "volume_unavailable"

// lvmErr turns an LVM failure into the command's error: volume_unavailable
// for an unavailable pool, internal with what failed otherwise.
func lvmErr(what string, err error) *Error {
	if errors.Is(err, lvm.ErrPoolUnavailable) {
		return errf(CodeVolumeUnavailable, "%s: the host's guest pool is unavailable: %v", what, err)
	}
	return errf(CodeInternal, "%s: %v", what, err)
}

// poolAvailable reads the pool once. A read that fails for any reason but
// ErrPoolUnavailable (an lvs row hostd cannot parse) counts as available:
// it is a bug to fix, not a pool to page about, and the commands that need
// the pool still fail on it.
func (m *Manager) poolAvailable(ctx context.Context) (bool, lvm.PoolState) {
	p, err := m.d.LVM.PoolStatus(ctx)
	if errors.Is(err, lvm.ErrPoolUnavailable) {
		return false, p
	}
	return true, p
}

// PoolUnavailable is the flag Hello and Heartbeat carry (I-644).
func (m *Manager) PoolUnavailable() bool {
	ok, _ := m.poolAvailable(m.ctx)
	m.setPoolGauge(ok)
	return !ok
}

// PoolStatusText is `hostd status`'s pool line: active, or unavailable
// with the reason.
func (m *Manager) PoolStatusText() string {
	ok, p := m.poolAvailable(m.ctx)
	if ok {
		return "active"
	}
	if r := p.Reason(); r != "" && p.Size > 0 {
		return "unavailable (" + r + ")"
	}
	return "unavailable (missing)"
}

func (m *Manager) setPoolGauge(ok bool) {
	if m.d.Metrics == nil {
		return
	}
	if ok {
		m.d.Metrics.PoolAvailable.Set(1)
	} else {
		m.d.Metrics.PoolAvailable.Set(0)
	}
}

// ensureActive activates a guest's volume that exists but is inactive,
// once: after a host reboot the pool unit activates every volume, and one
// that did not come up is retried here rather than failing the pool for
// every guest (I-643). A failure is volume_unavailable.
func (m *Manager) ensureActive(ctx context.Context, g *state.Guest, vol string) *Error {
	active, err := m.d.LVM.VolumeActive(ctx, vol)
	if err != nil {
		return lvmErr("lvs", err)
	}
	if active {
		return nil
	}
	m.log(g).Warn("volume inactive; activating it", "event", "volume_activate", "volume", vol)
	if err := m.d.LVM.Activate(ctx, vol); err != nil {
		m.log(g).Error("volume did not activate", "event", "volume_activate", "volume", vol, "err", err.Error())
		if errors.Is(err, lvm.ErrPoolUnavailable) {
			return lvmErr("lvchange -ay", err)
		}
		return errf(CodeVolumeUnavailable, "volume %s did not activate: %v", vol, err)
	}
	return nil
}
