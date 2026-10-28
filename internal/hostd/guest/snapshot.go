package guest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/snapshot"
	"github.com/heracraft/repose/internal/hostd/state"
)

// FreezeWarnAfter is the freeze window that produces a warning.
const FreezeWarnAfter = 2 * time.Second

func (m *Manager) snapshotCmd(ctx context.Context, c *hostdv1.Snapshot) (*hostdv1.SnapshotResult, *Error) {
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		return nil, gerr
	}
	switch c.Reason {
	case "scheduled", "stop", "manual":
	case "":
		c.Reason = "manual"
	default:
		return nil, errf(CodeInvalidArgument, "reason %q must be scheduled, stop or manual", c.Reason)
	}
	return m.snapshotGuest(ctx, g, c.Reason)
}

// BlobPath is <user_id>/<project_id>/<ts>.img.zst; a guest created without
// a user_id (an old CreateGuest shape) falls under its project only.
//
// The timestamp carries microseconds because it is the whole of the
// path's uniqueness: at milliseconds, two snapshots of one project in
// the same millisecond wrote the same blob, and the second overwrote
// the first silently. A stop that snapshots followed at once by a
// manual snapshot does exactly that, and the api is then holding two
// snapshot rows pointing at one blob, so retiring either takes the
// other's bytes with it and the survivor cannot be restored (I-910).
// Old paths are stored per snapshot and keep working.
func BlobPath(g *state.Guest, ts time.Time) string {
	name := ts.UTC().Format("2006-01-02T15-04-05.000000Z") + ".img.zst"
	if g.UserID == "" {
		return path.Join(g.ProjectID, name)
	}
	return path.Join(g.UserID, g.ProjectID, name)
}

// snapshotGuest freezes (when running), takes the LVM snapshot, thaws,
// streams it to the store and removes the LVM snapshot whatever happened.
func (m *Manager) snapshotGuest(ctx context.Context, g *state.Guest, reason string) (*hostdv1.SnapshotResult, *Error) {
	t, err := m.takeSnapshot(ctx, g, reason)
	if err != nil {
		return nil, err
	}
	return m.uploadSnapshot(ctx, g, t)
}

// takenSnapshot is an LVM snapshot taken and not yet uploaded.
type takenSnapshot struct {
	lv     string // the LVM snapshot volume
	reason string
	start  time.Time
	freeze time.Duration // freeze, lvcreate -s and thaw; 0 for a stopped guest
	fail   func(*Error) (*hostdv1.SnapshotResult, *Error)
	log    *slog.Logger
}

// takeSnapshot freezes (when running), takes the LVM snapshot and thaws.
// From its return the snapshot is a fixed point in time, whatever the
// guest does next; uploadSnapshot streams and removes it.
func (m *Manager) takeSnapshot(ctx context.Context, g *state.Guest, reason string) (*takenSnapshot, *Error) {
	return m.takeSnapshotAs(ctx, g, reason, "")
}

// takeSnapshotAs is takeSnapshot into the volume lv ("" for a snap-*
// name of its own).
func (m *Manager) takeSnapshotAs(ctx context.Context, g *state.Guest, reason, lv string) (*takenSnapshot, *Error) {
	switch g.State {
	case StateRunning, StateStopped, StateError:
	default:
		return nil, errf(CodeInvalidArgument, "guest is %s; snapshot needs running or stopped", g.State)
	}
	start := m.d.Now()
	if lv == "" {
		lv = fmt.Sprintf("snap-%s-%d", g.GuestID, start.UnixMilli())
	}
	t := &takenSnapshot{lv: lv, reason: reason, start: start}
	// From here the volume is this snapshot's, so the reconcile sweep
	// must not take it (I-906). Every path out of takeSnapshotAs and
	// uploadSnapshot releases it: the failure paths below through
	// t.fail, and the success path through uploadSnapshot's defer.
	m.snapStarted(t.lv)
	vol := VolumeName(g.GuestID)
	t.log = m.log(g).With("reason", reason)
	t.log.Info("snapshot start", "event", "snapshot_start")
	t.fail = func(e *Error) (*hostdv1.SnapshotResult, *Error) {
		m.snapDone(t.lv)
		t.log.Error("snapshot failed", "event", "snapshot_fail", "code", e.Code, "reason", e.Message)
		if m.d.Metrics != nil {
			m.d.Metrics.SnapshotDuration.WithLabelValues(reason, e.Code).Observe(m.d.Now().Sub(start).Seconds())
		}
		return nil, e
	}
	failed := func(e *Error) (*takenSnapshot, *Error) {
		_, e = t.fail(e)
		return nil, e
	}
	if g.State == StateRunning {
		// The fsfreeze needs guestd, so a resting guest is woken first:
		// without this every resting guest produced one failed snapshot
		// and one freeze_timeout warning a night (I-818).
		sess, serr := m.sessionAwake(ctx, g)
		if serr != nil {
			return failed(serr)
		}
		fstart := m.d.Now()
		if err := sess.Freeze(ctx); err != nil {
			return failed(errf(CodeGuestUnresponsive, "guestd Freeze: %v", err))
		}
		lvErr := m.d.LVM.Snapshot(ctx, vol, t.lv)
		thawErr := sess.Thaw(ctx)
		window := m.d.Now().Sub(fstart)
		t.freeze = window
		if m.d.Metrics != nil {
			m.d.Metrics.SnapshotFreezeSeconds.Observe(window.Seconds())
		}
		if thawErr != nil {
			// guestd's watchdog thaws after 10 s; say so.
			m.Warn("freeze_timeout", "guest "+g.GuestID+": thaw request failed; guestd watchdog will thaw")
			if lvErr == nil {
				_ = m.d.LVM.RemoveVolume(ctx, t.lv) // the snapshot is unusable without a clean thaw record
			}
			return failed(errf(CodeInternal, "snapshot: thaw failed: %v", thawErr))
		}
		if window > FreezeWarnAfter {
			t.log.Warn("freeze window long", "event", "snapshot_start", "freeze_ms", window.Milliseconds())
		}
		if lvErr != nil {
			return failed(lvmErr("lvcreate -s", lvErr))
		}
	} else {
		if err := m.d.LVM.Snapshot(ctx, vol, t.lv); err != nil {
			return failed(lvmErr("lvcreate -s", err))
		}
	}
	return t, nil
}

// uploadSnapshot streams a taken snapshot to the store and removes the
// LVM snapshot whatever happened. It reads only what takeSnapshot fixed,
// so it may run while the guest stops (stopCmd) or keeps running.
func (m *Manager) uploadSnapshot(ctx context.Context, g *state.Guest, t *takenSnapshot) (*hostdv1.SnapshotResult, *Error) {
	defer func() {
		_ = m.d.LVM.RemoveVolume(context.Background(), t.lv) // always, even on upload failure; a leftover snapshot is found by reconcile
		m.snapDone(t.lv)                                     // after the remove, so the sweep never races it
	}()
	if err := m.d.LVM.Activate(ctx, t.lv); err != nil {
		return t.fail(lvmErr("lvchange -ay -K", err))
	}
	r, err := m.d.Stream.Read(ctx, m.d.LVM.DevPath(t.lv))
	if err != nil {
		return t.fail(errf(CodeInternal, "snapshot stream: %v", err))
	}
	blobPath := BlobPath(g, t.start)
	meta := map[string]string{"guest_id": g.GuestID, "class": g.Class, "volume_bytes": fmt.Sprint(g.VolumeBytes), "reason": t.reason}
	// The digest of exactly the bytes handed to the store goes back to the
	// api, which keeps it in Postgres; a restore checks the blob against
	// it, so bytes changed in the store are never restored (I-462).
	h := sha256.New()
	n, uerr := m.d.Blob.Upload(ctx, blobPath, io.TeeReader(r, h), meta)
	cerr := r.Close()
	if uerr == nil && cerr != nil {
		uerr = cerr
	}
	if uerr != nil {
		return t.fail(errf(CodeInternal, "snapshot upload failed: %v", uerr))
	}
	// The store must hold what was sent before the api records it.
	info, ok, serr := m.d.Blob.Stat(ctx, blobPath)
	if serr != nil {
		return t.fail(errf(CodeInternal, "snapshot upload check: %v", serr))
	}
	if !ok || info.Size != int64(n) {
		return t.fail(errf(CodeInternal, "snapshot upload check: the store holds %d bytes of %d", info.Size, n))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if m.d.Metrics != nil {
		m.d.Metrics.SnapshotBytesTotal.Add(float64(n))
		m.d.Metrics.SnapshotBytes.Set(float64(n))
		m.d.Metrics.SnapshotDuration.WithLabelValues(t.reason, "ok").Observe(m.d.Now().Sub(t.start).Seconds())
	}
	// The format and the used bytes say what the duration was spent on:
	// an extent snapshot reads what the filesystem uses, a raw one reads
	// the whole volume (I-164).
	format, why, used, replayed := "raw", "", uint64(0), false
	if md, ok := r.(snapshot.Moder); ok {
		mo := md.Mode()
		format, why, used, replayed = mo.Format, mo.Why, mo.UsedBytes, mo.JournalReplayed
	}
	// read_wait_ms is how long the stream waited for the device (0 when
	// unknown): close to duration_ms means the disk set the pace; well
	// under it, zstd or the upload did (DECISIONS I-571).
	var readWaitMs int64
	if rt, ok := r.(snapshot.ReadTimer); ok {
		readWaitMs = rt.ReadDuration().Milliseconds()
	}
	t.log.Info("snapshot done", "event", "snapshot_done", "bytes", n, "duration_ms", m.d.Now().Sub(t.start).Milliseconds(),
		"freeze_ms", t.freeze.Milliseconds(), "read_wait_ms", readWaitMs,
		"format", format, "raw_reason", why, "journal_replayed", replayed, "used_bytes", used, "volume_bytes", g.VolumeBytes)
	m.emitEvent(&hostdv1.Event_SnapshotDone{SnapshotDone: &hostdv1.SnapshotDone{GuestId: g.GuestID, BlobPath: blobPath, Bytes: n, Sha256: sum}})
	return &hostdv1.SnapshotResult{BlobPath: blobPath, Bytes: n, Sha256: sum}, nil
}

func (m *Manager) restore(ctx context.Context, c *hostdv1.Restore) (*hostdv1.CreateResult, *Error) {
	clone := c.Clone
	if c.GuestId == "" || c.ProjectId == "" || (c.BlobPath == "" && clone == nil) {
		return nil, errf(CodeInvalidArgument, "guest_id, project_id and blob_path (or clone) required")
	}
	if c.BlobPath != "" && clone != nil {
		return nil, errf(CodeInvalidArgument, "a restore names blob_path or clone, not both")
	}
	want := strings.ToLower(c.Sha256)
	if want != "" {
		if b, err := hex.DecodeString(want); err != nil || len(b) != sha256.Size {
			return nil, errf(CodeInvalidArgument, "sha256 must be 64 hex digits")
		}
	}
	if _, err := hexPrefix(c.GuestId); err != nil {
		return nil, err.(*Error)
	}
	existing, gerr := m.getGuest(c.GuestId)
	if gerr != nil && gerr.Code != CodeNotFound {
		return nil, gerr
	}
	if existing != nil && existing.State != StateRestoring && existing.State != StateError {
		if existing.State == StateStopped {
			return &hostdv1.CreateResult{GuestIp: existing.IP, VsockCid: existing.CID}, nil
		}
		return nil, errf(CodeAlreadyExists, "guest %s exists in state %s", c.GuestId, existing.State)
	}
	var src *state.Guest
	if clone != nil {
		var e *Error
		if src, e = m.cloneSource(c); e != nil {
			return nil, e
		}
	}
	if err := m.capacityCheck(c.GuestId, c.Class, c.VolumeBytes, false); err != nil {
		return nil, err
	}
	var info snapshot.Info
	if clone == nil {
		var ok bool
		var err error
		info, ok, err = m.d.Blob.Stat(ctx, c.BlobPath)
		if err != nil {
			return nil, errf(CodeInternal, "blob: %v", err)
		} else if !ok {
			return nil, errf(CodeNotFound, "snapshot %s not in the store", c.BlobPath)
		}
	}
	if c.SystemClosure != "" {
		if ok, err := m.d.Nix.PathExists(ctx, c.SystemClosure); err != nil {
			return nil, errf(CodeInternal, "nix path-info: %v", err)
		} else if !ok {
			return nil, errf(CodeNotFound, "system closure %s is not in the host store", c.SystemClosure)
		}
	}
	idx, err := m.d.State.AllocIndex(c.GuestId, m.maxIndex)
	if err != nil {
		return nil, errf(CodeInsufficientCapacity, "%v", err)
	}
	tap, _ := TapName(c.GuestId)
	mac, _ := MACAddr(c.GuestId)
	g := &state.Guest{
		GuestID: c.GuestId, ProjectID: c.ProjectId, UserID: c.UserId, ProjectSlug: c.ProjectSlug, RemoteURL: c.RemoteUrl,
		Class: c.Class, VolumeBytes: c.VolumeBytes, SystemClosure: c.SystemClosure,
		IP: m.ipForIndex(idx), MAC: mac, Tap: tap, CID: cidForIndex(idx), IPIndex: idx,
		Env: c.Env, Principals: c.Principals, SSHCAPub: c.SshCaPub, HooksConfig: c.HooksConfig, ProjectJSON: c.ProjectJson,
	}
	if err := m.setState(g, StateRestoring, ""); err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	log := m.log(g)
	log.Info("restoring guest", "event", "guest_create", "class", g.Class)
	start := m.d.Now()
	vol := VolumeName(g.GuestID)
	fail := func(e *Error) (*hostdv1.CreateResult, *Error) {
		log.Error("restore failed", "event", "restore_fail", "code", e.Code, "reason", e.Message, "duration_ms", m.d.Now().Sub(start).Milliseconds())
		// A half-written volume is never left for a start to boot (I-461).
		if err := m.d.LVM.RemoveVolume(context.Background(), vol); err != nil {
			log.Warn("removing the volume of a failed restore", "event", "restore_fail", "err", err.Error())
		}
		_ = m.setState(g, StateError, "restore: "+e.Message) // e is what the api sees; the state write is logged inside
		if clone != nil {
			m.releaseForkPoint(clone)
		}
		return nil, e
	}
	// A volume left by an earlier attempt at this guest holds a partial
	// write (or a clone of a point that may be gone); an extent stream
	// writes only used blocks, so it starts over on a fresh one.
	if existing != nil {
		if err := m.d.LVM.RemoveVolume(ctx, vol); err != nil {
			return fail(lvmErr("lvremove", err))
		}
	}
	if clone != nil {
		pointMs, cloneMs, e := m.cloneVolume(ctx, src, clone, vol, c.VolumeBytes)
		if e != nil {
			return fail(e)
		}
		// No check follows: the volume is the source's own blocks at a
		// moment its filesystem was frozen, never bytes that came through
		// a store, and the guest's mount replays its journal as the
		// source's would (I-701).
		log.Info("restore done", "event", "restore_done", "source", "clone", "duration_ms", m.d.Now().Sub(start).Milliseconds(),
			"point_ms", pointMs, "clone_ms", cloneMs, "volume_bytes", g.VolumeBytes)
	} else {
		if err := m.d.LVM.CreateVolume(ctx, vol, g.VolumeBytes); err != nil {
			return fail(lvmErr("lvcreate", err))
		}
		// One download: the blob is hashed as it is written into the new
		// volume, and a digest that differs fails the restore and removes
		// the volume before anything can start it (I-700, which amends
		// I-462's check before the write). A snapshot from before I-462
		// has no digest and is restored unchecked.
		pr, pw := io.Pipe()
		dlErr := make(chan error, 1)
		var downloaded countingWriter
		wh := sha256.New()
		// With a digest to check, a write that fails lets the download
		// run on into the hash alone, so a blob changed in the store is
		// reported as that and not as whatever its bytes made the write
		// fail with.
		feed := &pipeFeed{w: pw, keepOn: want != ""}
		go func() {
			err := m.d.Blob.Download(ctx, c.BlobPath, info.Version, io.MultiWriter(&downloaded, wh, feed))
			_ = pw.CloseWithError(err) // the reader sees err; CloseWithError itself cannot fail
			dlErr <- err
		}()
		werr := m.d.Stream.Write(ctx, m.d.LVM.DevPath(vol), pr)
		_ = pr.Close() // stops the feed if it is still going; the error is the download's
		if err := <-dlErr; err != nil {
			return fail(errf(CodeInternal, "snapshot download failed: %v", err))
		}
		if want != "" && hex.EncodeToString(wh.Sum(nil)) != want {
			return fail(checksumMismatch(log))
		}
		if werr != nil {
			return fail(errf(CodeInternal, "restore stream: %v", werr))
		}
		written := m.d.Now()
		code, err := m.d.LVM.Fsck(ctx, vol)
		if err != nil {
			return fail(errf(CodeInternal, "e2fsck: %v", err))
		}
		if code > 1 {
			return fail(errf(CodeInternal, "filesystem check failed after restore (e2fsck exit %d)", code))
		}
		// The stages a slow restore spent its time on: the download and
		// the write overlap, the check follows (I-403). verify_ms stays,
		// 0, for the dashboards that read it (I-700).
		log.Info("restore done", "event", "restore_done", "source", "blob", "bytes", uint64(downloaded), "duration_ms", m.d.Now().Sub(start).Milliseconds(),
			"verify_ms", 0, "verified", want != "", "write_ms", written.Sub(start).Milliseconds(), "fsck_ms", m.d.Now().Sub(written).Milliseconds(), "volume_bytes", g.VolumeBytes)
	}
	if g.SystemClosure != "" {
		if err := m.d.Roots.Set(g.GuestID, g.SystemClosure); err != nil {
			return fail(errf(CodeInternal, "gcroot: %v", err))
		}
	}
	if err := m.cacheSecrets(g.GuestID, c.Secrets, c.SshCaPub, c.HostKey, c.HostCert); err != nil {
		return fail(err)
	}
	if err := m.setState(g, StateStopped, ""); err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	if clone != nil {
		m.releaseForkPoint(clone)
	}
	return &hostdv1.CreateResult{GuestIp: g.IP, VsockCid: g.CID}, nil
}

// checksumMismatch is the failure of a restore whose blob is not the one
// the snapshot recorded (I-462): the store's copy was changed.
func checksumMismatch(log *slog.Logger) *Error {
	log.Error("snapshot checksum mismatch", "event", "restore_fail", "code", CodeInternal)
	return errf(CodeInternal, "snapshot checksum mismatch: the stored snapshot is not the one that was taken; nothing was restored")
}

// pipeFeed hands the download to the stream's pipe. Once the stream has
// stopped reading (the pipe is closed), keepOn swallows the error so the
// download goes on to its end for the hash; without it the error stops
// the download.
type pipeFeed struct {
	w      *io.PipeWriter
	keepOn bool
	closed bool
}

func (f *pipeFeed) Write(p []byte) (int, error) {
	if f.closed {
		return len(p), nil
	}
	if _, err := f.w.Write(p); err != nil {
		if !f.keepOn {
			return 0, err
		}
		f.closed = true
	}
	return len(p), nil
}

// countingWriter counts the bytes written through it.
type countingWriter uint64

func (c *countingWriter) Write(p []byte) (int, error) {
	*c += countingWriter(len(p))
	return len(p), nil
}

// snapStarted marks a snapshot volume as this host's work in progress,
// and snapDone releases it. The reconcile sweep skips what is marked
// (I-906); a mark left by a hostd that died is gone with the process,
// which is what makes the sweep at start right to take everything.
func (m *Manager) snapStarted(lv string) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	m.snapInFlight[lv] = true
}

func (m *Manager) snapDone(lv string) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	delete(m.snapInFlight, lv)
}

// snapBusy reports whether lv is being taken or uploaded right now.
func (m *Manager) snapBusy(lv string) bool {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	return m.snapInFlight[lv]
}
