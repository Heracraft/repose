package guest

import (
	"context"
	"encoding/hex"
	"slices"
	"sync"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/state"
)

// A fork whose copies land on the host its source runs on is made of thin
// clones of the source's volume, not of a snapshot sent through the store
// and back (DECISIONS I-701). The first Restore of the fork to arrive
// freezes the source, takes the fork point (a thin snapshot named
// fp-<point_id>) and thaws; it and every sibling then clone the point.
// The point lives until each sibling has finished its restore, or
// ForkPointTTL, or the next hostd start, whichever comes first.

// FeatureLocalClone is the Hello feature that says a Restore may carry
// `clone`.
const FeatureLocalClone = "local_clone"

// Features is what this hostd reports in Hello. pauseIdle adds
// FeaturePause: a host that has not been switched on never reports it,
// so the api sends it no PauseGuest and no WakeGuest (DECISIONS I-818).
func Features(pauseIdle bool) []string {
	f := []string{FeatureLocalClone}
	if pauseIdle {
		f = append(f, FeaturePause)
	}
	return f
}

// ForkPointTTL is how long a fork point outlives a fork whose siblings do
// not all arrive (the api failed one before its restore): it pins the
// source's blocks as they were, so the pool pays for every block the
// source writes after it.
var ForkPointTTL = 30 * time.Minute

// ForkPointName is the volume a fork point is kept in.
func ForkPointName(point string) string { return "fp-" + point }

func (m *Manager) forkLock(point string) *sync.Mutex {
	l, _ := m.forkLocks.LoadOrStore(point, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// cloneSource checks a Restore's clone and returns the guest it copies.
func (m *Manager) cloneSource(c *hostdv1.Restore) (*state.Guest, *Error) {
	cl := c.Clone
	if b, err := hex.DecodeString(cl.PointId); err != nil || len(b) != 16 {
		return nil, errf(CodeInvalidArgument, "clone.point_id must be 32 hex digits")
	}
	if cl.SourceGuestId == "" || cl.SourceGuestId == c.GuestId {
		return nil, errf(CodeInvalidArgument, "clone.source_guest_id must name another guest")
	}
	if !slices.Contains(cl.SiblingGuestIds, c.GuestId) {
		return nil, errf(CodeInvalidArgument, "clone.sibling_guest_ids must include the guest being restored")
	}
	src, err := m.getGuest(cl.SourceGuestId)
	if err != nil {
		if err.Code == CodeNotFound {
			return nil, errf(CodeNotFound, "the guest a clone copies, %s, is not on this host", cl.SourceGuestId)
		}
		return nil, err
	}
	if c.VolumeBytes < src.VolumeBytes {
		return nil, errf(CodeInvalidArgument, "volume_bytes %d is smaller than the source's %d", c.VolumeBytes, src.VolumeBytes)
	}
	return src, nil
}

// cloneVolume makes vol a clone of the fork point, taking the point first
// when no sibling has. It returns how long the point (0 when it existed)
// and the clone took.
func (m *Manager) cloneVolume(ctx context.Context, src *state.Guest, cl *hostdv1.RestoreClone, vol string, volumeBytes uint64) (pointMs, cloneMs int64, e *Error) {
	l := m.forkLock(cl.PointId)
	m.withoutSlot(ctx, l.Lock)
	defer l.Unlock()
	fp := ForkPointName(cl.PointId)
	ok, err := m.d.LVM.VolumeExists(ctx, fp)
	if err != nil {
		return 0, 0, lvmErr("lvs", err)
	}
	if !ok {
		start := m.d.Now()
		var e *Error
		m.withoutSlot(ctx, func() { e = m.takeForkPoint(ctx, src.GuestID, fp) })
		if e != nil {
			return 0, 0, e
		}
		pointMs = m.d.Now().Sub(start).Milliseconds()
		point := cl.PointId
		time.AfterFunc(ForkPointTTL, func() { m.dropForkPoint(point, "expired") })
	}
	start := m.d.Now()
	if err := m.d.LVM.Clone(ctx, fp, vol); err != nil {
		return pointMs, 0, lvmErr("lvcreate -s", err)
	}
	if size, _, err := m.d.LVM.VolumeStats(ctx, vol); err == nil && volumeBytes > size {
		if err := m.d.LVM.ExtendVolume(ctx, vol, volumeBytes); err != nil {
			return pointMs, 0, lvmErr("lvextend", err)
		}
	}
	return pointMs, m.d.Now().Sub(start).Milliseconds(), nil
}

// takeForkPoint freezes the source (when it runs), snapshots its volume
// into fp and thaws, on the source's own worker: after any command queued
// for it, never beside its stop or its own snapshot. The job takes no op
// slot (job.noSlot), and the restore waiting for it gives its own back
// (withoutSlot), so a command for the source queued in front of the job
// can take one.
func (m *Manager) takeForkPoint(ctx context.Context, sourceID, fp string) *Error {
	done := make(chan *Error, 1)
	m.enqueue(sourceID, job{noSlot: true, internal: func(context.Context) {
		g, gerr := m.getGuest(sourceID)
		if gerr != nil {
			done <- gerr
			return
		}
		t, e := m.takeSnapshotAs(ctx, g, "fork", fp)
		if e != nil {
			done <- e
			return
		}
		t.log.Info("fork point taken", "event", "fork_point", "freeze_ms", t.freeze.Milliseconds(), "duration_ms", m.d.Now().Sub(t.start).Milliseconds())
		done <- nil
	}})
	select {
	case e := <-done:
		return e
	case <-ctx.Done():
		return errf(CodeInternal, "fork point: %v", ctx.Err())
	}
}

// releaseForkPoint removes the fork point once every sibling has finished
// its restore, well or not: a sibling still restoring, or not here yet,
// keeps it.
func (m *Manager) releaseForkPoint(cl *hostdv1.RestoreClone) {
	for _, id := range cl.SiblingGuestIds {
		g, err := m.getGuest(id)
		if err != nil || g.State == StateRestoring {
			return
		}
	}
	m.dropForkPoint(cl.PointId, "every copy restored")
}

func (m *Manager) dropForkPoint(point, why string) {
	l := m.forkLock(point)
	l.Lock()
	defer l.Unlock()
	fp := ForkPointName(point)
	ok, err := m.d.LVM.VolumeExists(context.Background(), fp)
	if err != nil || !ok {
		return
	}
	if err := m.d.LVM.RemoveVolume(context.Background(), fp); err != nil {
		m.d.Log.Warn("fork point not removed", "event", "fork_point", "volume", fp, "err", err.Error())
		return
	}
	m.d.Log.Info("fork point removed", "event", "fork_point", "volume", fp, "why", why)
}
