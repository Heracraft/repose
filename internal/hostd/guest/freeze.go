package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/freeze"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/virtiofs"
)

// Guest freeze (DECISIONS I-652, I-653). When the host is about to restart,
// each running guest is paused and Cloud Hypervisor writes its memory and
// device state to an encrypted volume of its own; the hypervisor then
// exits with the guest still paused, so the guest's disk holds exactly
// what its memory expects. At hostd's next start, before the stream
// connects and before reconcile settles anything, each complete image is
// restored and resumed: processes, tmux and agents carry on where they
// were, and only connections that were open notice. Anything that does not
// restore falls through to reconcile as before (L6 starts it cold).
//
// The guest's bbolt state stays `running` across a freeze: there is no
// `frozen` state (the enum in docs/interfaces/README.md is fixed), and a
// frozen guest is a running guest whose hypervisor is between hosts.

// FreezeConfig turns the freeze on and tunes it. The zero value is off.
type FreezeConfig struct {
	Enabled bool
	// RecordDir holds one record per frozen guest (root only):
	// /var/lib/repose/hostd/freeze on a host. Empty turns the freeze off
	// and leaves any image alone.
	RecordDir string
	// Deadline bounds a FreezeAll; guests not frozen by then are left to
	// the shutdown as before. Default 120 s.
	Deadline time.Duration
	// Concurrency is how many snapshots write at once; they share the
	// data disk's bandwidth. Default 2.
	Concurrency int
	// Settle is how long a FreezeAll waits after asking the guests to drop
	// their caches, for free page reporting to hand the pages back so
	// they are not written (only with FreePageReporting). Default 5 s;
	// negative means none.
	Settle time.Duration
	// MaxAge is the oldest image a start restores; an older one is
	// discarded and the guest starts cold. Default 6 h, I-645's window.
	MaxAge time.Duration
	// UnwrapTimeout bounds how long a restore waits for the image key
	// (Key Vault) before the guest starts cold instead. Default 90 s.
	UnwrapTimeout time.Duration
	// RestoreTimeout bounds the hypervisor's restore (reading the image)
	// and guestd's first answer after the resume. Default 180 s. A
	// ThawAll starts no restore later than UnwrapTimeout + RestoreTimeout
	// after it began; the images left are discarded and those guests
	// start cold, so a slow restore never holds Hello back for long.
	RestoreTimeout time.Duration
	// WriteRate is the rate in bytes per second a FreezeAll assumes one
	// snapshot writes at, until its first finished snapshot shows the
	// real one. Default 100 MB/s (host-stg-01's 125 MB/s disk wrote a
	// 4.1 GB image in 36 s with a second snapshot beside it).
	WriteRate uint64
	// ResumeWait bounds how long a guest left paused by a freeze that did
	// not finish (a failed snapshot, or hostd restarted mid-freeze) is
	// retried until it runs again. Default 2 min.
	ResumeWait time.Duration
}

func (c FreezeConfig) withDefaults() FreezeConfig {
	if c.Deadline == 0 {
		c.Deadline = 120 * time.Second
	}
	if c.Concurrency == 0 {
		c.Concurrency = 2
	}
	if c.Settle == 0 {
		c.Settle = 5 * time.Second
	}
	if c.Settle < 0 {
		c.Settle = 0
	}
	if c.MaxAge == 0 {
		c.MaxAge = 6 * time.Hour
	}
	if c.UnwrapTimeout == 0 {
		c.UnwrapTimeout = 90 * time.Second
	}
	if c.RestoreTimeout == 0 {
		c.RestoreTimeout = 180 * time.Second
	}
	if c.WriteRate == 0 {
		c.WriteRate = 100_000_000
	}
	if c.ResumeWait == 0 {
		c.ResumeWait = 2 * time.Minute
	}
	return c
}

// FreezeImage is the image volume (freeze.Image on a host).
type FreezeImage interface {
	Create(ctx context.Context, guestID string, size uint64, key []byte, dir string, uid, gid int) error
	Open(ctx context.Context, guestID string, key []byte, dir string) error
	Close(ctx context.Context, guestID, dir string) error
	Remove(ctx context.Context, guestID, dir string) error
}

// FreezeDeps are the freeze's host facilities.
type FreezeDeps struct {
	Sealer freeze.Sealer
	Image  FreezeImage
	// Fingerprint hashes the start of a guest volume (freeze.Fingerprint).
	Fingerprint func(dev string) (string, error)
	// BootID returns the host's boot id; nil reads the kernel's.
	BootID func() string
	// ImageBytes returns what an image directory holds on disk; nil
	// walks it.
	ImageBytes func(dir string) uint64
}

// FreezeResult is one guest's outcome, for the operator and the log.
type FreezeResult struct {
	GuestID string `json:"guest_id"`
	OK      bool   `json:"ok"`
	// Outcome is a fixed word: frozen, thawed, skipped, failed, discarded.
	Outcome string `json:"outcome"`
	// Reason is a fixed phrase, never tenant text.
	Reason     string `json:"reason,omitempty"`
	Ms         int64  `json:"ms"`
	ImageBytes uint64 `json:"image_bytes,omitempty"`
}

// Fixed freeze reasons, the record's Reason.
const (
	FreezeShutdown       = "shutdown"
	FreezeOperator       = "operator"
	FreezeScheduledEvent = "scheduled_event"
)

// imageMount is where a guest's image volume is mounted: inside the guest
// directory, so the hypervisor's sandbox (which shows it only that
// directory, I-51) sees it. A mount made there after the unit started
// reaches the unit through mount propagation; the experiments checked it
// with the unit's own sandbox properties.
func (m *Manager) imageMount(id string) string { return filepath.Join(m.guestDir(id), "freeze") }

// snapDir is the snapshot directory inside the image.
func (m *Manager) snapDir(id string) string { return filepath.Join(m.imageMount(id), "snap") }

func (m *Manager) fcfg() FreezeConfig { return m.cfg.Freeze.withDefaults() }

func (m *Manager) records() freeze.Store { return freeze.Store{Dir: m.fcfg().RecordDir} }

func (m *Manager) bootID() string {
	if m.d.Freeze.BootID != nil {
		return m.d.Freeze.BootID()
	}
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// FreezeEnabled reports whether this host freezes guests.
func (m *Manager) FreezeEnabled() bool {
	return m.cfg.Freeze.Enabled && m.cfg.Freeze.RecordDir != "" && m.d.Freeze.Sealer != nil && m.d.Freeze.Image != nil
}

// FreezeAll freezes every running guest, smallest first, within the
// configured deadline (ctx may end it sooner). It never stops a guest it
// did not freeze: those are left running for whatever comes next (the
// shutdown's clean stop, or nothing on an operator's freeze). One
// FreezeAll, ThawAll or SyncClocks runs at a time: a second freeze must
// not touch the first's images, and a thaw must not take an image the
// hypervisor is still writing.
func (m *Manager) FreezeAll(ctx context.Context, reason string) []FreezeResult {
	if !m.FreezeEnabled() {
		return nil
	}
	fc := m.fcfg()
	start := m.d.Now()
	ctx, cancel := context.WithTimeout(ctx, fc.Deadline)
	defer cancel()
	if !m.lockFreeze(ctx) {
		m.d.Log.Warn("freeze skipped: another freeze or thaw outlasted the deadline", "event", "guest_freeze", "reason", reason)
		return nil
	}
	defer m.unlockFreeze()
	if err := m.d.Freeze.Sealer.Ready(); err != nil {
		m.d.Log.Warn("freeze skipped: no image key", "event", "guest_freeze", "reason", reason, "err", err.Error())
		return nil
	}
	gs, err := m.d.State.ListGuests()
	if err != nil {
		m.d.Log.Warn("freeze skipped: list guests", "event", "guest_freeze", "err", err.Error())
		return nil
	}
	var run []*state.Guest
	for _, g := range gs {
		if g.State != StateRunning {
			continue
		}
		if a, _ := m.d.Systemd.IsActive(ctx, GuestUnit(g.GuestID)); !a {
			continue
		}
		run = append(run, g)
	}
	sort.SliceStable(run, func(i, j int) bool { return Classes[run[i].Class].MemMiB < Classes[run[j].Class].MemMiB })
	m.d.Log.Info("freezing guests", "event", "guest_freeze", "reason", reason, "guests", len(run))

	// Room in the pool: an image takes at most the class's memory; stop
	// adding guests once the pool would pass the refuse threshold.
	room := m.freezeRoom(ctx)
	results := make([]FreezeResult, len(run))
	take := make([]bool, len(run))
	for i, g := range run {
		need := Classes[g.Class].MemMiB << 20
		if room >= 0 && need > uint64(room) {
			results[i] = FreezeResult{GuestID: g.GuestID, Outcome: "skipped", Reason: "pool has no room for the image"}
			continue
		}
		if room >= 0 {
			room -= int64(need)
		}
		take[i] = true
	}

	// Every guest writes back its dirty pages while it still runs, all at
	// once: the sync is worth it whether it is frozen or stopped next. A
	// guest that will be frozen and has the balloon also drops its page
	// cache, so free page reporting hands those pages back and they are
	// not written into the image (a guest that read 2.5 GB wrote a 3.6 GB
	// image without, 1.6 GB with; docs/research/guest-freeze.md). Without
	// the balloon the dropped pages stay in the image anyway, so the
	// guest keeps its cache.
	var wg sync.WaitGroup
	var dropped atomic.Bool
	for i, g := range run {
		drop := take[i] && m.argsHaveBalloon(g.GuestID)
		wg.Add(1)
		go func(g *state.Guest, drop bool) {
			defer wg.Done()
			sess, serr := m.session(g.GuestID)
			if serr != nil {
				return
			}
			dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
			defer dcancel()
			script := "sync"
			if drop {
				script = "sync; echo 3 > /proc/sys/vm/drop_caches"
			}
			if r, err := sess.Exec(dctx, []string{"/bin/sh", "-c", script}, 10, ""); err != nil || r.ExitCode != 0 {
				m.log(g).Warn("guest cache write-back failed before freeze", "event", "guest_freeze")
			} else if drop {
				dropped.Store(true)
			}
		}(g, drop)
	}
	wg.Wait()
	if fc.Settle > 0 && dropped.Load() {
		select {
		case <-time.After(fc.Settle):
		case <-ctx.Done():
		}
	}

	// A snapshot cannot be interrupted, so one that would run past the
	// deadline is not started: the shutdown unit's stop timeout is set
	// from the deadline, and a guest still paused when it passes would be
	// killed rather than stopped cleanly. The write rate starts at the
	// configured one and becomes the slowest this freeze's own snapshots
	// achieved once one has finished. A guest skipped here is left
	// running for the shutdown's clean stop.
	//
	// The slots are taken here, in run's order (smallest first), not by
	// the goroutines: those race for them, and the scheduler runs the
	// newest first, so the largest guest went first and a deadline that
	// could have held the small ones held it alone (host-stg-01,
	// 2026-10-12: 2 of 4 frozen).
	rate := newWriteRate(fc.WriteRate)
	sem := make(chan struct{}, fc.Concurrency)
	for i, g := range run {
		if !take[i] {
			continue
		}
		if ctx.Err() == nil {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
			}
		}
		if ctx.Err() != nil {
			results[i] = FreezeResult{GuestID: g.GuestID, Outcome: "skipped", Reason: "deadline passed"}
			continue
		}
		wg.Add(1)
		go func(i int, g *state.Guest) {
			defer wg.Done()
			id := g.GuestID
			defer func() { <-sem }()
			// The job runs on the guest's worker, after any command already
			// queued for it. Once it has started, its result is waited for
			// even past the deadline (the freeze then fails and resumes the
			// guest), so what is reported is what happened.
			done := make(chan FreezeResult, 1)
			started := make(chan struct{})
			m.enqueue(id, job{internal: func(context.Context) {
				close(started)
				if ctx.Err() != nil {
					done <- FreezeResult{GuestID: id, Outcome: "skipped", Reason: "deadline passed"}
					return
				}
				if dl, ok := ctx.Deadline(); ok {
					if est := rate.estimate(m.memoryBytes(ctx, g)); est > time.Until(dl) {
						m.log(g).Warn("guest not frozen: its snapshot would outlast the deadline", "event", "guest_freeze",
							"reason", reason, "estimate_ms", est.Milliseconds())
						done <- FreezeResult{GuestID: id, Outcome: "skipped", Reason: "snapshot would outlast the deadline"}
						return
					}
				}
				r := m.freezeGuest(ctx, id, reason)
				if r.OK {
					rate.observe(r.ImageBytes, time.Duration(r.Ms)*time.Millisecond)
				}
				done <- r
			}})
			select {
			case <-started:
				results[i] = <-done
			case <-ctx.Done():
				select {
				case <-started:
					results[i] = <-done
				default:
					results[i] = FreezeResult{GuestID: id, Outcome: "skipped", Reason: "deadline passed"}
				}
			}
		}(i, g)
	}
	wg.Wait()
	n := 0
	for _, r := range results {
		if r.OK {
			n++
		}
	}
	m.d.Log.Info("guests frozen", "event", "guest_freeze", "reason", reason, "frozen", n, "guests", len(run),
		"duration_ms", m.d.Now().Sub(start).Milliseconds())
	return results
}

// lockFreeze takes the manager's freeze lock, or gives up when ctx ends.
func (m *Manager) lockFreeze(ctx context.Context) bool {
	select {
	case m.freezeMu <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *Manager) unlockFreeze() { <-m.freezeMu }

// writeRate is a FreezeAll's estimate of how fast one snapshot writes.
type writeRate struct {
	mu   sync.Mutex
	bps  float64
	seen bool
}

func newWriteRate(bps uint64) *writeRate { return &writeRate{bps: float64(max(bps, 1))} }

// estimate is how long a snapshot of n bytes takes at the current rate.
func (w *writeRate) estimate(n uint64) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Duration(float64(n) / w.bps * float64(time.Second))
}

// observe takes the rate of a finished freeze (its whole duration, so the
// estimate errs long). The slowest seen wins: snapshots share the disk.
func (w *writeRate) observe(n uint64, d time.Duration) {
	if n == 0 || d <= 0 {
		return
	}
	r := float64(n) / d.Seconds()
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.seen || r < w.bps {
		w.bps = r
	}
	w.seen = true
}

// memoryBytes is what a guest's image will hold at most: the memory its
// hypervisor's unit holds (with free page reporting, what the guest
// uses), or the class's memory when that cannot be read.
func (m *Manager) memoryBytes(ctx context.Context, g *state.Guest) uint64 {
	class := (Classes[g.Class].MemMiB + 256) << 20
	props, err := m.d.Systemd.Show(ctx, GuestUnit(g.GuestID), "MemoryCurrent")
	if err != nil {
		return class
	}
	n, err := strconv.ParseUint(props["MemoryCurrent"], 10, 64)
	if err != nil || n == 0 || n > class {
		return class
	}
	return n
}

// argsHaveBalloon reports whether the guest's hypervisor was started with
// the free page reporting balloon, from the argv hostd wrote at its boot.
func (m *Manager) argsHaveBalloon(id string) bool {
	b, err := os.ReadFile(filepath.Join(m.guestDir(id), "ch.args"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "--balloon")
}

// freezeRoom is how many bytes images may take before the pool passes its
// refuse threshold; -1 when the pool cannot be read (no limit is applied:
// a failed image write fails that guest's freeze alone).
func (m *Manager) freezeRoom(ctx context.Context) int64 {
	size, free, err := m.d.LVM.PoolStats(ctx)
	if err != nil || size == 0 {
		return -1
	}
	used := size - free
	limit := uint64(float64(size) * m.cfg.PoolRefusePct / 100)
	if used >= limit {
		return 0
	}
	return int64(limit - used)
}

// freezeGuest freezes one guest. It runs on the guest's worker.
func (m *Manager) freezeGuest(ctx context.Context, id, reason string) FreezeResult {
	start := m.d.Now()
	res := FreezeResult{GuestID: id, Outcome: "failed"}
	g, gerr := m.getGuest(id)
	if gerr != nil {
		res.Reason = "guest not found"
		return res
	}
	if g.State != StateRunning {
		res.Outcome, res.Reason = "skipped", "guest is not running"
		return res
	}
	// Checked again here, on the guest's worker: a guest frozen since it
	// was listed has no hypervisor and a record, and a second freeze would
	// re-create (that is, remove) its finished image.
	if a, _ := m.d.Systemd.IsActive(ctx, GuestUnit(id)); !a {
		res.Outcome, res.Reason = "skipped", "guest is not running"
		return res
	}
	if r, _ := m.records().Get(id); r != nil {
		res.Outcome, res.Reason = "skipped", "guest already frozen"
		return res
	}
	fz, ok := m.d.CH.(ch.Freezer)
	if !ok {
		res.Reason = "hypervisor client cannot snapshot"
		return res
	}
	dir := m.guestDir(id)
	sock := ch.APISocket(dir)
	recs := m.records()
	key, err := freeze.NewKey()
	if err != nil {
		res.Reason = "no randomness for the image key"
		return res
	}
	defer freeze.Wipe(key)
	wrapped, keyID, err := m.d.Freeze.Sealer.Wrap(key)
	if err != nil {
		res.Reason = "image key could not be wrapped"
		m.log(g).Warn("freeze failed", "event", "guest_freeze", "reason", res.Reason, "err", err.Error())
		return res
	}
	chv, _ := m.d.CH.Version(ctx) // empty is recorded as unknown and compared as such
	rec := &freeze.Record{
		GuestID: id, State: freeze.StateWriting, Reason: reason, CreatedAt: m.d.Now().UTC(), HostBootID: m.bootID(),
		Closure: g.SystemClosure, Class: g.Class, VolumeBytes: g.VolumeBytes, CHVersion: chv,
		AuthShare: m.argsHaveAuthShare(id), KeyID: keyID, WrappedKey: wrapped, KeyCheck: freeze.KeyCheck(key),
	}
	if err := recs.Put(rec); err != nil {
		res.Reason = "record not written"
		m.log(g).Warn("freeze failed", "event", "guest_freeze", "reason", res.Reason, "err", err.Error())
		return res
	}
	fail := func(why string, err error) FreezeResult {
		m.discardImage(context.WithoutCancel(ctx), id, why)
		res.Reason = why
		res.Ms = m.d.Now().Sub(start).Milliseconds()
		attrs := []any{"event", "guest_freeze", "reason", why}
		if err != nil {
			attrs = append(attrs, "err", err.Error())
		}
		m.log(g).Warn("freeze failed; guest keeps running", attrs...)
		return res
	}
	lookup := m.cfg.Lookup
	if lookup == nil {
		lookup = LookupUser
	}
	uid, gid, err := lookup(m.cfg.GuestUser)
	if err != nil {
		return fail("guest user unknown", err)
	}
	size := (Classes[g.Class].MemMiB + 256) << 20
	if err := m.d.Freeze.Image.Create(ctx, id, size, key, m.imageMount(id), uid, gid); err != nil {
		return fail("image volume not made", err)
	}
	if err := ensureDir(m.imageMount(id), 0o700, uid, gid); err != nil {
		return fail("image directory not made", err)
	}
	if err := ensureDir(m.snapDir(id), 0o700, uid, gid); err != nil {
		return fail("image directory not made", err)
	}

	// From here the guest is paused. A snapshot that fails resumes it: the
	// freeze is abandoned and the guest carries on as if nothing happened.
	if err := m.d.CH.Pause(ctx, sock); err != nil {
		return fail("pause failed", err)
	}
	pausedAt := m.d.Now()
	// The hypervisor cannot be interrupted mid-snapshot, so the call is not
	// tied to the freeze's deadline: giving up on it early only leaves the
	// hypervisor busy and the guest paused. It has its own, generous bound.
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotTimeout)
	err = fz.Snapshot(sctx, sock, m.snapDir(id))
	scancel()
	if err != nil {
		if !m.resumeAfterFailedSnapshot(context.WithoutCancel(ctx), g, sock) {
			// The hypervisor may still be writing into the image: it is not
			// removed under it. The `writing` record stays, so the next
			// ThawAll (hostd's next start, or `hostd thaw`) resumes the
			// guest and removes the image then.
			res.Reason = "snapshot failed; guest still paused"
			res.Ms = m.d.Now().Sub(start).Milliseconds()
			m.log(g).Error("freeze failed and the guest did not resume", "event", "guest_freeze", "err", err.Error())
			return res
		}
		return fail("snapshot failed", err)
	}
	snapMs := m.d.Now().Sub(pausedAt).Milliseconds()
	// The image is written; the rest (end the hypervisor, close the image,
	// mark the record) runs to the end even if the deadline has passed,
	// so the record never says less than what happened.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	// The hypervisor goes with the VM still paused; the monitor goes first
	// so the exit is not taken for a crash. Console capture stays until
	// the hypervisor is gone (I-186).
	var stopConsole func()
	if mon := m.removeMonitor(id); mon != nil {
		stopConsole = mon.stopKeepConsole()
	}
	_ = m.d.CH.Shutdown(ctx, sock) // the wait below kills the unit if this did nothing
	// A paused VM's hypervisor need not exit on vmm.shutdown; its memory is
	// in the image and its disk untouched since the pause, so it is killed
	// after a short wait instead of the stop path's 15 s.
	wctx, wcancel := context.WithTimeout(ctx, 3*time.Second)
	if err := m.d.Systemd.WaitInactive(wctx, GuestUnit(id)); err != nil {
		_ = m.d.Systemd.Kill(ctx, GuestUnit(id)) // the VM is paused and its snapshot written; nothing is lost
	}
	wcancel()
	if stopConsole != nil {
		stopConsole()
	}
	m.teardown(ctx, g)

	if m.d.Freeze.Fingerprint != nil {
		fp, err := m.d.Freeze.Fingerprint(m.d.LVM.DevPath(VolumeName(id)))
		if err != nil {
			// The image stays, but without a fingerprint it is never restored;
			// the guest is down now either way and starts cold.
			m.log(g).Warn("volume fingerprint not read; the image will not be restored", "event", "guest_freeze", "err", err.Error())
		}
		rec.Fingerprint = fp
	}
	rec.ImageBytes = m.imageBytes(m.snapDir(id))
	// Close flushes the image (umount) and drops its key from the kernel.
	if err := m.d.Freeze.Image.Close(ctx, id, m.imageMount(id)); err != nil {
		m.log(g).Warn("image not closed", "event", "guest_freeze", "err", err.Error())
		return fail("image not closed", err)
	}
	rec.State, rec.DoneAt = freeze.StateComplete, m.d.Now().UTC()
	if err := recs.Put(rec); err != nil {
		return fail("record not written", err)
	}
	res.OK, res.Outcome, res.Reason = true, "frozen", ""
	res.ImageBytes = rec.ImageBytes
	res.Ms = m.d.Now().Sub(start).Milliseconds()
	m.log(g).Info("guest frozen", "event", "guest_freeze", "reason", reason, "class", g.Class,
		"snapshot_ms", snapMs, "duration_ms", res.Ms, "image_bytes", rec.ImageBytes)
	return res
}

// snapshotTimeout bounds one vm.snapshot: an xl guest's 16 GiB at 100 MB/s
// is under 3 minutes.
const snapshotTimeout = 10 * time.Minute

// resumeAfterFailedSnapshot brings a guest back from the pause of a freeze
// that failed, or that a hostd restart cut short. The hypervisor may still
// be busy with the snapshot (its API answers nothing until it is done), so
// the resume is retried until vm.info says Running, for up to ResumeWait.
// It reports whether the guest runs.
func (m *Manager) resumeAfterFailedSnapshot(ctx context.Context, g *state.Guest, sock string) bool {
	deadline := m.d.Now().Add(m.fcfg().ResumeWait)
	var last error
	for m.d.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		last = m.d.CH.Resume(rctx, sock)
		info, ierr := m.d.CH.Info(rctx, sock)
		cancel()
		if ierr == nil && strings.Contains(string(info), `"Running"`) {
			m.log(g).Info("guest resumed after a freeze that did not finish", "event", "guest_freeze")
			return true
		}
		if ierr != nil {
			last = ierr
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	msg := "no answer"
	if last != nil {
		msg = last.Error()
	}
	m.log(g).Error("guest left paused after a freeze that did not finish", "event", "guest_freeze", "err", msg)
	return false
}

// argsHaveAuthShare reports whether the guest's hypervisor was started
// with the login share, from the argv hostd wrote at its boot.
func (m *Manager) argsHaveAuthShare(id string) bool {
	b, err := os.ReadFile(filepath.Join(m.guestDir(id), "ch.args"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "tag="+m.cfg.AuthTag+",")
}

func (m *Manager) imageBytes(dir string) uint64 {
	if m.d.Freeze.ImageBytes != nil {
		return m.d.Freeze.ImageBytes(dir)
	}
	var n uint64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error { // a size for the log; unreadable entries count zero
		if err == nil && fi.Mode().IsRegular() {
			n += diskBytes(fi)
		}
		return nil
	})
	return n
}

// discardImage removes a guest's image and its record; why is a fixed
// phrase for the log.
func (m *Manager) discardImage(ctx context.Context, id, why string) {
	if m.d.Freeze.Image != nil {
		if err := m.d.Freeze.Image.Remove(ctx, id, m.imageMount(id)); err != nil {
			m.d.Log.Warn("frozen image not removed", "event", "freeze_discard", "guest_id", id, "err", err.Error())
			return
		}
	}
	_ = os.Remove(m.imageMount(id)) // the empty mount point; absent is fine
	if err := m.records().Delete(id); err != nil {
		m.d.Log.Warn("freeze record not removed", "event", "freeze_discard", "guest_id", id, "err", err.Error())
		return
	}
	m.d.Log.Info("frozen image discarded", "event", "freeze_discard", "guest_id", id, "reason", why)
}

// ThawAll restores every frozen guest that can be. Reconcile calls it
// first, so the guests it restores are running when the stream sends
// Hello; the rest are discarded and reconcile treats them as it always
// has. Without the freeze turned on it still cleans up images a host left
// behind, and restores none.
//
// It runs in two steps, so its length does not grow with the number of
// images when Key Vault is slow or down: every image is checked and its
// key unwrapped at once, under one UnwrapTimeout; then the images are
// restored, Concurrency at a time, and an image whose restore has not
// started UnwrapTimeout + RestoreTimeout after ThawAll began is discarded
// (that guest starts cold).
func (m *Manager) ThawAll(ctx context.Context) []FreezeResult {
	if m.cfg.Freeze.RecordDir == "" {
		return nil
	}
	if !m.lockFreeze(ctx) {
		return nil
	}
	defer m.unlockFreeze()
	fc := m.fcfg()
	begin := time.Now()
	recs := m.records()
	ids, err := recs.List()
	if err != nil {
		m.d.Log.Warn("freeze records not listed", "event", "guest_thaw", "err", err.Error())
		return nil
	}
	m.sweepImageVolumes(ctx, ids)
	if len(ids) == 0 {
		return nil
	}
	results := make([]FreezeResult, len(ids))
	preps := make([]*thawPrep, len(ids))
	uctx, ucancel := context.WithTimeout(ctx, fc.UnwrapTimeout)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			preps[i], results[i] = m.prepareThaw(ctx, uctx, id)
		}(i, id)
	}
	wg.Wait()
	ucancel()

	window, wcancel := context.WithDeadline(ctx, begin.Add(fc.UnwrapTimeout+fc.RestoreTimeout))
	defer wcancel()
	sem := make(chan struct{}, fc.Concurrency)
	for i, p := range preps {
		if p == nil {
			continue
		}
		wg.Add(1)
		go func(i int, p *thawPrep) {
			defer wg.Done()
			defer freeze.Wipe(p.key)
			select {
			case sem <- struct{}{}:
			case <-window.Done():
				m.discardImage(ctx, p.g.GuestID, "restore window passed")
				results[i] = FreezeResult{GuestID: p.g.GuestID, Outcome: "discarded", Reason: "restore window passed"}
				return
			}
			defer func() { <-sem }()
			if window.Err() != nil {
				m.discardImage(ctx, p.g.GuestID, "restore window passed")
				results[i] = FreezeResult{GuestID: p.g.GuestID, Outcome: "discarded", Reason: "restore window passed"}
				return
			}
			results[i] = m.thawGuest(ctx, p)
		}(i, p)
	}
	wg.Wait()
	n := 0
	for _, r := range results {
		if r.OK {
			n++
		}
	}
	m.d.Log.Info("frozen guests restored", "event", "guest_thaw", "restored", n, "images", len(ids),
		"duration_ms", time.Since(begin).Milliseconds())
	return results
}

// sweepImageVolumes removes image volumes with no record: a hostd that
// died between lvcreate and the record, or a record removed by hand.
func (m *Manager) sweepImageVolumes(ctx context.Context, ids []string) {
	if m.d.Freeze.Image == nil {
		return
	}
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
	}
	vols, err := m.d.LVM.ListVolumes(ctx)
	if err != nil {
		return
	}
	for _, v := range vols {
		id, ok := strings.CutPrefix(v, "frz-")
		if !ok || have[id] {
			continue
		}
		m.discardImage(ctx, id, "no record")
	}
}

// thawPrep is an image that passed its checks, with its key unwrapped.
type thawPrep struct {
	g     *state.Guest
	rec   *freeze.Record
	key   []byte
	start time.Time
}

// prepareThaw checks one image and unwraps its key (uctx bounds the
// unwrap). It returns the prep for a restore, or nil and the image's
// result when it was discarded or kept.
func (m *Manager) prepareThaw(ctx, uctx context.Context, id string) (*thawPrep, FreezeResult) {
	start := m.d.Now()
	res := FreezeResult{GuestID: id, Outcome: "discarded"}
	fc := m.fcfg()
	rec, err := m.records().Get(id)
	if err != nil || rec == nil {
		m.discardImage(ctx, id, "record unreadable")
		res.Reason = "record unreadable"
		return nil, res
	}
	discard := func(why string) (*thawPrep, FreezeResult) {
		m.discardImage(ctx, id, why)
		res.Reason = why
		res.Ms = m.d.Now().Sub(start).Milliseconds()
		return nil, res
	}
	g, gerr := m.getGuest(id)
	// A guest whose hypervisor still runs was never frozen to the end: hostd
	// stopped mid-freeze (crash, OOM, a switch restarting it), or a
	// snapshot failed and the resume did not take. Its VM may be paused,
	// and its hypervisor may still be writing the image. It is resumed
	// first; the image is removed only once the guest runs, and otherwise
	// kept with its record for the next start to try again.
	if a, _ := m.d.Systemd.IsActive(ctx, GuestUnit(id)); a {
		if !m.resumeAfterFailedSnapshot(ctx, g, ch.APISocket(m.guestDir(id))) {
			res.Outcome, res.Reason = "skipped", "guest still paused; image kept"
			res.Ms = m.d.Now().Sub(start).Milliseconds()
			return nil, res
		}
		return discard("guest already running")
	}
	switch {
	case gerr != nil:
		return discard("guest no longer on this host")
	case !m.FreezeEnabled():
		return discard("freeze turned off")
	case rec.State != freeze.StateComplete:
		return discard("image incomplete")
	case m.d.Now().Sub(rec.DoneAt) > fc.MaxAge:
		return discard("image older than the restore window")
	case g.State != StateRunning:
		// Anything that ran on the guest since (stop, start, destroy,
		// restore) went through a state that is not running.
		return discard("guest state changed since the freeze")
	case rec.Closure != g.SystemClosure:
		return discard("closure changed since the freeze")
	case rec.Class != g.Class:
		return discard("class changed since the freeze")
	case rec.VolumeBytes != g.VolumeBytes:
		return discard("volume resized since the freeze")
	case rec.Fingerprint == "":
		return discard("no volume fingerprint")
	}
	if chv, _ := m.d.CH.Version(ctx); chv != rec.CHVersion {
		return discard("hypervisor version changed since the freeze")
	}
	if ok, err := m.d.LVM.VolumeExists(ctx, VolumeName(id)); err != nil || !ok {
		return discard("guest volume unavailable")
	}
	if err := m.d.LVM.Activate(ctx, VolumeName(id)); err != nil {
		return discard("guest volume unavailable")
	}
	if m.d.Freeze.Fingerprint == nil {
		return discard("no volume fingerprint")
	}
	if fp, err := m.d.Freeze.Fingerprint(m.d.LVM.DevPath(VolumeName(id))); err != nil || fp != rec.Fingerprint {
		return discard("guest volume changed since the freeze")
	}
	if _, ok := m.d.CH.(ch.Freezer); !ok {
		return discard("hypervisor client cannot restore")
	}
	key, err := m.unwrapWithRetry(uctx, rec)
	if err != nil {
		m.log(g).Warn("image key not unwrapped", "event", "guest_thaw", "err", err.Error())
		return discard("image key unavailable")
	}
	if freeze.KeyCheck(key) != rec.KeyCheck {
		freeze.Wipe(key)
		return discard("image key does not match")
	}
	return &thawPrep{g: g, rec: rec, key: key, start: start}, FreezeResult{}
}

// thawGuest restores one checked image, or discards it.
func (m *Manager) thawGuest(ctx context.Context, p *thawPrep) FreezeResult {
	g, rec, id := p.g, p.rec, p.g.GuestID
	res := FreezeResult{GuestID: id, Outcome: "discarded"}
	discard := func(why string) FreezeResult {
		m.discardImage(ctx, id, why)
		res.Reason = why
		res.Ms = m.d.Now().Sub(p.start).Milliseconds()
		return res
	}
	if err := m.d.Freeze.Image.Open(ctx, id, p.key, m.imageMount(id)); err != nil {
		m.log(g).Warn("image not opened", "event", "guest_thaw", "err", err.Error())
		return discard("image volume not opened")
	}
	if err := m.restoreGuest(ctx, g, rec); err != nil {
		m.log(g).Warn("restore failed; the guest starts cold", "event", "guest_thaw", "err", err.Error())
		m.teardown(context.WithoutCancel(ctx), g)
		return discard("restore failed")
	}
	m.discardImage(ctx, id, "restored")
	res.OK, res.Outcome, res.Reason = true, "thawed", ""
	res.Ms = m.d.Now().Sub(p.start).Milliseconds()
	m.log(g).Info("guest restored from its frozen image", "event", "guest_thaw", "class", g.Class,
		"duration_ms", res.Ms, "frozen_s", int64(m.d.Now().Sub(rec.DoneAt).Seconds()), "image_bytes", rec.ImageBytes)
	return res
}

func (m *Manager) unwrapWithRetry(ctx context.Context, rec *freeze.Record) ([]byte, error) {
	var last error
	for {
		k, err := m.d.Freeze.Sealer.Unwrap(ctx, rec.WrappedKey, rec.KeyID)
		if err == nil {
			return k, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, errors.Join(last, ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}

// restoreGuest brings back the hypervisor's surroundings as boot does
// (tap, rules, shape, the store share, the login share), starts the
// hypervisor from the image, resumes the VM and waits for guestd.
func (m *Manager) restoreGuest(ctx context.Context, g *state.Guest, rec *freeze.Record) error {
	fc := m.fcfg()
	dir := m.guestDir(g.GuestID)
	if err := m.prepareGuestDir(dir); err != nil {
		return err
	}
	if err := m.d.Net.AddTap(ctx, g.Tap); err != nil {
		return fmt.Errorf("tap: %w", err)
	}
	m.resetCursor(g.GuestID)
	if err := m.d.Net.AddGuestRules(ctx, g.GuestID, g.IP, g.MAC, g.Tap); err != nil {
		return fmt.Errorf("guest rules: %w", err)
	}
	if err := m.d.Net.Shape(ctx, g.Tap, m.cfg.EgressMbit, m.cfg.DownloadMbit); err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	vcfg := virtiofs.Config{SharedDir: m.cfg.StoreExport, User: m.cfg.VirtiofsUser, Group: m.cfg.VirtiofsUser, SocketGroup: m.cfg.GuestUser, Binary: m.cfg.VirtiofsBinary}
	if err := virtiofs.Start(ctx, m.d.Systemd, vcfg, g.GuestID, ch.VirtiofsSocket(dir)); err != nil {
		return fmt.Errorf("virtiofsd: %w", err)
	}
	if err := m.waitSocket(ctx, virtiofs.Unit(g.GuestID), ch.VirtiofsSocket(dir)); err != nil {
		return fmt.Errorf("virtiofsd: %w", err)
	}
	if err := m.extendView(ctx, g.GuestID, m.viewClosures(g)); err != nil {
		return fmt.Errorf("store view: %w", err)
	}
	if rec.AuthShare && !m.startAuthShare(ctx, g, dir) {
		return errors.New("the login share the image has did not start")
	}
	for _, s := range []string{"ch.sock", "vsock.sock"} {
		_ = os.Remove(filepath.Join(dir, s)) // the hypervisor makes them anew; a stale one fails its bind
	}
	props := GuestUnitProps(Classes[g.Class], m.cfg.GuestUser, m.cfg.GuestsDir, dir, m.d.LVM.DevPath(VolumeName(g.GuestID)))
	argv := ch.RestoreArgs(dir, m.snapDir(g.GuestID))
	if err := m.d.Systemd.Run(ctx, GuestUnit(g.GuestID), props, argv); err != nil {
		return fmt.Errorf("hypervisor: %w", err)
	}
	sock := ch.APISocket(dir)
	rctx, rcancel := context.WithTimeout(ctx, fc.RestoreTimeout)
	defer rcancel()
	for {
		if _, err := m.d.CH.Info(rctx, sock); err == nil {
			break
		}
		if a, _ := m.d.Systemd.IsActive(rctx, GuestUnit(g.GuestID)); !a {
			return errors.New("hypervisor exited during the restore")
		}
		select {
		case <-rctx.Done():
			return errors.New("restore did not finish in time")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// The image has been read into the guest's memory: close it now, so
	// its key leaves the kernel before the guest runs again.
	if err := m.d.Freeze.Image.Close(ctx, g.GuestID, m.imageMount(g.GuestID)); err != nil {
		m.log(g).Warn("image not closed after the restore", "event", "guest_thaw", "err", err.Error())
	}
	if err := m.d.CH.Resume(ctx, sock); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	mon := m.startMonitor(g)
	sess, err := mon.waitReady(rctx, fc.RestoreTimeout)
	if err != nil {
		mon.stop()
		m.removeMonitor(g.GuestID)
		return fmt.Errorf("guestd after the resume: %w", err)
	}
	// The guest's clock stopped with it; set it to the host's (I-233 saw it
	// 10 s behind after a short freeze; after a host restart it is minutes).
	now := m.d.Now()
	sec := strconv.FormatFloat(float64(now.UnixNano())/1e9, 'f', 6, 64)
	if r, err := sess.Exec(ctx, []string{"date", "-u", "-s", "@" + sec}, 10, ""); err != nil || r.ExitCode != 0 {
		m.log(g).Warn("guest clock not set after the restore", "event", "guest_thaw")
	}
	m.markGood(g, g.SystemClosure)
	return m.setState(g, StateRunning, "restored from a frozen image")
}

// SyncClocks sets every running guest's clock to the host's, after the
// host itself was paused (an Azure Freeze event): the guests were paused
// with it and their clocks fell behind by the same time.
func (m *Manager) SyncClocks(ctx context.Context) {
	if !m.lockFreeze(ctx) {
		return
	}
	defer m.unlockFreeze()
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return
	}
	for _, g := range gs {
		if g.State != StateRunning {
			continue
		}
		sess, serr := m.session(g.GuestID)
		if serr != nil {
			continue
		}
		sec := strconv.FormatFloat(float64(m.d.Now().UnixNano())/1e9, 'f', 6, 64)
		if r, err := sess.Exec(ctx, []string{"date", "-u", "-s", "@" + sec}, 10, ""); err != nil || r.ExitCode != 0 {
			m.log(g).Warn("guest clock not set", "event", "clock_sync")
		}
	}
}

// diskBytes is what a file holds on disk (its blocks), not its length:
// the memory file is as long as the guest's RAM and mostly holes.
func diskBytes(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Blocks) * 512
	}
	return uint64(fi.Size())
}
