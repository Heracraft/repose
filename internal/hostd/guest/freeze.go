package guest

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
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
	// Deadline bounds a FreezeAll: it is how much guest memory a host
	// restart can save, WriteRate times this, less the sync and the
	// settle. Guests that do not fit are left to the shutdown and lose
	// everything they were running, which is counted and alerted
	// (I-796). Default 240 s, which at host-01's measured 400 MB/s is
	// 96 GB, more than any memory its 64 GiB can place; the cost of
	// raising it is shutdown time, bounded by StopGrace below.
	Deadline time.Duration
	// StopGrace is how long past Deadline a FreezeAll may still be
	// finishing snapshots it started, before it gives up waiting and
	// reports. It is the hard wall: a snapshot cannot be interrupted, so
	// without it a freeze waits snapshotTimeout per snapshot and
	// systemd's TimeoutStopSec cuts the ExecStop instead -- which kills
	// hostd before it counts or records anything, the one failure that
	// makes the whole count silent. Default 120 s; no flag, because
	// freeze.nix derives repose-guest-freeze.service's TimeoutStopSec
	// from the deadline and a 150 s margin, which this has to stay
	// inside.
	StopGrace time.Duration
	// Concurrency is how many snapshots write at once. Default 4: a
	// snapshot of a compressed image (I-685) spends its time in one core
	// compressing rather than waiting on the disk, so four at once finish
	// sooner than two (host-stg-01: four loaded guests froze in 38.8 s
	// against 45.3 s). Each guest is paused a little longer, and every one
	// of them is frozen sooner, which is what its gap is made of.
	Concurrency int
	// RestoreConcurrency is how many images are read back at once.
	// Default 4, and never fewer than Concurrency: a restore decompresses
	// in one core per image, so a host whose disk made it lower the
	// freeze's concurrency still reads four back at once.
	RestoreConcurrency int
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
	// and guestd's first answer after the resume. Default 180 s;
	// `--freeze-restore-timeout` (repose.host.freeze.restoreTimeout)
	// raises it on a host whose images are larger or whose disk is
	// slower than that.
	//
	// A ThawAll starts no restore later than UnwrapTimeout plus the
	// longer of this and what its own images take to read at WriteRate
	// after it began; the images left are discarded and those guests
	// start cold, so a slow restore never holds Hello back for long. The
	// read has to be budgeted from the same arithmetic as the write, or
	// a freeze spends the shutdown writing images the next boot throws
	// away and the guest cold-boots anyway, after a longer restart
	// instead of a shorter one (I-796).
	RestoreTimeout time.Duration
	// WriteRate is the rate in bytes per second a FreezeAll assumes its
	// snapshots write guest memory at, until one of its own has finished
	// and shown the real one. It is measured on the disk the guest thin
	// pool is on, where the images are written: host-01's is 450 MB/s
	// (`dd oflag=direct` on a scratch thin volume, 2026-10-18) and it is
	// configured at 400; host-stg-01's is 125.
	//
	// The default of 100 MB/s is a floor for a host nobody has measured,
	// not a safe guess: understating the rate is what *skips* guests,
	// and a skipped guest loses everything it was running. Overstating
	// it risks a freeze that outlasts its deadline, which StopGrace
	// bounds and the counter reports. `--freeze-write-rate`
	// (repose.host.freeze.writeRateMBs) is how a host says its own
	// (I-796).
	WriteRate uint64
	// ResumeWait bounds how long a guest left paused by a freeze that did
	// not finish (a failed snapshot, or hostd restarted mid-freeze) is
	// retried until it runs again. Default 2 min.
	ResumeWait time.Duration
}

func (c FreezeConfig) withDefaults() FreezeConfig {
	if c.Deadline == 0 {
		c.Deadline = 240 * time.Second
	}
	if c.StopGrace == 0 {
		c.StopGrace = 120 * time.Second
	}
	if c.Concurrency == 0 {
		c.Concurrency = 4
	}
	if c.RestoreConcurrency == 0 {
		c.RestoreConcurrency = 4
	}
	if c.RestoreConcurrency < c.Concurrency {
		c.RestoreConcurrency = c.Concurrency
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
	Open(ctx context.Context, guestID string, key []byte, dir, fs string) error
	// FS names the filesystem Create makes, which the record keeps so a
	// restore mounts what is there (I-685).
	FS() string
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
	// The hard wall. ctx bounds what a freeze *starts*; this bounds what
	// it waits for, snapshots already running included, so the ExecStop
	// always returns before systemd's TimeoutStopSec cuts it. hostd
	// killed there would count nothing and write no tally, which is the
	// one way to lose a guest and have nothing say so (I-796).
	wall := start.Add(fc.Deadline + fc.StopGrace)
	ctx, cancel := context.WithTimeout(ctx, fc.Deadline)
	defer cancel()
	if !m.lockFreeze(ctx) {
		m.d.Log.Error("freeze skipped: another freeze or thaw outlasted the deadline", "event", "guest_freeze", "reason", reason)
		m.countUnfrozen(ctx, reason, "busy", nil)
		return nil
	}
	defer m.unlockFreeze()
	// Listed before the key is checked, so a freeze that cannot run still
	// counts the guests it leaves unfrozen (I-796).
	gs, err := m.d.State.ListGuests()
	if err != nil {
		// Nothing is counted here: without the list there is no telling
		// whether a running guest exists. HostdDown and the journal are
		// what report this one.
		m.d.Log.Error("freeze skipped: list guests", "event", "guest_freeze", "err", err.Error())
		return nil
	}
	var run []*state.Guest
	for _, g := range gs {
		if g.State != StateRunning {
			continue
		}
		if !m.unitRunning(ctx, g) {
			continue
		}
		run = append(run, g)
	}
	if err := m.d.Freeze.Sealer.Ready(); err != nil {
		m.d.Log.Error("freeze skipped: no image key", "event", "guest_freeze", "reason", reason, "err", err.Error())
		m.countUnfrozen(ctx, reason, "no_key", run)
		return nil
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
			// A resting guest (I-818) synced, and dropped its cache when
			// it has the balloon, at its pause: it cannot answer now, and
			// waiting out its 10 s would come off this freeze's deadline
			// and cost a later guest its image.
			if m.restingMark(g.GuestID) != nil {
				return
			}
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
	// What a snapshot is admitted against is every snapshot already
	// admitted plus its own, not its own alone: Concurrency of them
	// share one disk, so four that each fit the deadline on their own
	// finish well past it together. Up to Concurrency start at the same
	// instant with the whole deadline ahead of them, so the rate a
	// finished snapshot teaches cannot correct that first batch -- on a
	// host with no more running guests than Concurrency, which is every
	// host repose runs today, nothing corrected it at all and the
	// deadline was enforced by systemd's SIGKILL (I-796).
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
				mem := m.memoryBytes(ctx, g)
				if dl, ok := ctx.Deadline(); ok {
					if est, ok := rate.admit(mem, time.Until(dl)); !ok {
						m.log(g).Error("guest not frozen: its snapshot would outlast the deadline", "event", "guest_freeze",
							"reason", reason, "estimate_ms", est.Milliseconds(), "left_ms", time.Until(dl).Milliseconds())
						done <- FreezeResult{GuestID: id, Outcome: "skipped", Reason: "snapshot would outlast the deadline"}
						return
					}
				}
				r := m.freezeGuest(ctx, id, reason, wall)
				rate.release(mem)
				if r.OK {
					// The rate learned is memory bytes over the
					// snapshot's whole duration, the units the estimate
					// is in. The image is compressed to about a fifth
					// (I-685), so a rate taken from image bytes would
					// teach a fifth of the truth and skip every guest
					// after the first.
					rate.observe(mem, time.Duration(r.Ms)*time.Millisecond)
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
	left := m.reportUnfrozen(reason, run, results)
	m.d.Log.Info("guests frozen", "event", "guest_freeze", "reason", reason, "frozen", n, "guests", len(run),
		"unfrozen", left, "duration_ms", m.d.Now().Sub(start).Milliseconds())
	return results
}

// A freeze that leaves a running guest unfrozen costs that guest
// everything it was running: a host restart then boots it cold, and no
// agent, process or tmux window survives. It used to be a Warn line and a
// row in a table nobody reads, and on 2026-10-18 host-01's xl guest was
// skipped and nobody was told (DECISIONS I-796). Every such path now
// counts repose_host_guest_freeze_unfrozen_total, which GuestLeftUnfrozen
// alerts on, and logs at Error.

// unfrozenReason maps one result to the counter's reason, or "" when the
// guest kept its memory (frozen) or had none to keep (not running, or
// already frozen with an image of its own).
func unfrozenReason(r FreezeResult) string {
	if r.OK {
		return ""
	}
	switch r.Outcome {
	case "failed":
		return "failed"
	case "skipped":
		switch r.Reason {
		case "pool has no room for the image":
			return "no_room"
		case "deadline passed":
			return "deadline"
		case "snapshot would outlast the deadline":
			return "estimate"
		}
	}
	return ""
}

// reportUnfrozen counts and logs the guests a finished FreezeAll left
// unfrozen, and returns how many there were. The sites that already log
// their own line (the estimate, a failure) are not logged twice.
func (m *Manager) reportUnfrozen(freezeReason string, run []*state.Guest, results []FreezeResult) int {
	logged := map[string]bool{"estimate": true, "failed": true}
	counts := map[string]int{}
	left := 0
	for i, r := range results {
		why := unfrozenReason(r)
		if why == "" {
			continue
		}
		counts[why]++
		left++
		if !logged[why] && i < len(run) {
			m.log(run[i]).Error("guest left unfrozen; it starts cold and loses what it was running",
				"event", "guest_freeze", "reason", freezeReason, "cause", why)
		}
	}
	m.count(freezeReason, counts)
	return left
}

// countUnfrozen counts one reason for every guest in run (all of them,
// for a freeze that could not start at all). With run nil it lists the
// running guests itself, which is what the bail before the list needs.
func (m *Manager) countUnfrozen(ctx context.Context, freezeReason, why string, run []*state.Guest) {
	if run == nil {
		// The bail that needs this is the one where the deadline passed
		// before the lock was taken, so ctx is already done and would
		// fail every IsActive: the listing gets its own short window.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		gs, err := m.d.State.ListGuests()
		if err != nil {
			return
		}
		for _, g := range gs {
			if g.State != StateRunning {
				continue
			}
			if !m.unitRunning(lctx, g) {
				continue
			}
			run = append(run, g)
		}
	}
	if len(run) == 0 {
		// Still recorded, so a freeze that left nothing behind clears
		// what an earlier one said (count's doc comment).
		m.count(freezeReason, nil)
		return
	}
	m.count(freezeReason, map[string]int{why: len(run)})
}

// count adds a freeze's tally to hostd's counter and to the running total
// beside the records. The total is what the next start publishes, because
// this process's counter never reaches a scrape: the host is going down
// behind a shutdown freeze. An operator's freeze is scraped like anything
// else, so it only moves the counter.
//
// It is called for every non-operator freeze, counts or none: a freeze
// that saved every guest is what clears what an earlier one left to say
// (a scheduled event that was then called off, whose tally would
// otherwise be read as a loss at a later reboot).
func (m *Manager) count(freezeReason string, counts map[string]int) {
	if m.d.Metrics != nil {
		for why, n := range counts {
			m.d.Metrics.GuestFreezeUnfrozen.WithLabelValues(why).Add(float64(n))
		}
	}
	if freezeReason == FreezeOperator {
		return
	}
	last := freeze.FreezeTally{HostBootID: m.bootID(), At: m.d.Now().UTC(), Reason: freezeReason, Counts: counts}
	if err := m.records().AddUnfrozen(last); err != nil {
		m.d.Log.Warn("unfrozen tally not written", "event", "guest_freeze", "err", err.Error())
	}
}

// countDiscarded adds a thaw's losses to the counter and to the running
// total, for the same reason the freeze's are kept: a thaw runs before
// the metrics listener is up, so the first scrape of a new boot already
// has the loss in it and a per-process count would read the same at
// every reboot.
func (m *Manager) countDiscarded(counts map[string]int) {
	if m.d.Metrics != nil {
		for why, n := range counts {
			m.d.Metrics.GuestThawDiscarded.WithLabelValues(why).Add(float64(n))
		}
	}
	if len(counts) == 0 {
		return
	}
	if err := m.records().AddDiscarded(counts); err != nil {
		m.d.Log.Warn("discarded tally not written", "event", "guest_thaw", "err", err.Error())
	}
}

// replayTally publishes the running totals this host has kept, so the
// counters say what the host has lost rather than what this process saw.
// Once per process: hostd's start is the thaw that publishes them, and an
// operator's later `hostd thaw` must not add what this process counted.
// A start that only added the last freeze's counts would publish the same
// number after every reboot that lost the same guest, and increase() sees
// a repeat as no rise at all -- which is the silence the alert exists to
// end (I-796).
func (m *Manager) replayTally() {
	m.tallyOnce.Do(m.publishTally)
}

func (m *Manager) publishTally() {
	t, err := m.records().LoadTally()
	if err != nil {
		m.d.Log.Warn("freeze tally not read", "event", "guest_freeze", "err", err.Error())
		return
	}
	if m.d.Metrics != nil {
		for why, n := range t.Unfrozen {
			if n > 0 {
				m.d.Metrics.GuestFreezeUnfrozen.WithLabelValues(why).Add(float64(n))
			}
		}
		for why, n := range t.Discarded {
			if n > 0 {
				m.d.Metrics.GuestThawDiscarded.WithLabelValues(why).Add(float64(n))
			}
		}
	}
	// The host did restart after that freeze, so the guests it left
	// behind started cold. A freeze under this same boot id lost nothing
	// to a restart (a called-off event), and an empty one lost nothing
	// at all.
	if t.LastFreeze.HostBootID == m.bootID() {
		return
	}
	said := false
	for why, n := range t.LastFreeze.Counts {
		if n <= 0 {
			continue
		}
		m.d.Log.Error("guests the last freeze left unfrozen started cold after the host restarted",
			"event", "guest_freeze", "reason", t.LastFreeze.Reason, "cause", why, "guests", n)
		said = true
	}
	// Said once: the totals stay, the counts behind this line go, or
	// every later start repeats a loss that happened at one restart.
	if said {
		if err := m.records().MarkLastFreezeReported(); err != nil {
			m.d.Log.Warn("freeze tally not written", "event", "guest_freeze", "err", err.Error())
		}
	}
}

// unitRunning reports whether a guest's hypervisor unit is up. A unit
// that cannot be asked -- dbus under a shutdown's load is where this
// happens -- counts as running: bbolt already says the guest is, and
// dropping it here is how one leaves a freeze neither frozen nor counted
// nor logged, which reads to the operator as a success (I-796).
func (m *Manager) unitRunning(ctx context.Context, g *state.Guest) bool {
	a, err := m.d.Systemd.IsActive(ctx, GuestUnit(g.GuestID))
	if err != nil {
		m.log(g).Warn("guest unit state unreadable; taken as running for the freeze", "event", "guest_freeze", "err", err.Error())
		return true
	}
	return a
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

// writeRate is a FreezeAll's estimate of how fast its snapshots write
// guest memory, and how much writing it has already committed to.
type writeRate struct {
	mu  sync.Mutex
	bps float64
	// owed is the memory bytes of admitted snapshots that have not
	// finished. The images share one disk, so this is the queue a new
	// snapshot waits behind.
	owed uint64
}

func newWriteRate(bps uint64) *writeRate { return &writeRate{bps: float64(max(bps, 1))} }

// dur is how long n bytes take at the current rate; the caller holds mu.
func (w *writeRate) dur(n uint64) time.Duration {
	return time.Duration(float64(n) / w.bps * float64(time.Second))
}

// admit reserves n bytes of writing when everything already admitted,
// plus this, still finishes within left; it returns the estimate either
// way. Refusing is what keeps the freeze inside its deadline: the guest
// is left running and counted.
func (w *writeRate) admit(n uint64, left time.Duration) (time.Duration, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	est := w.dur(w.owed + n)
	if est > left {
		return est, false
	}
	w.owed += n
	return est, true
}

// release gives back what a snapshot reserved, whatever became of it.
func (w *writeRate) release(n uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.owed >= n {
		w.owed -= n
	} else {
		w.owed = 0
	}
}

// observe takes the rate a finished snapshot achieved: n bytes of guest
// memory over its whole duration, so the estimate errs long. It only ever
// lowers the rate -- the snapshots share one disk, so the slowest wins,
// and the host's own measurement is the ceiling. A snapshot that went
// faster than configured usually only means its guest's memory
// compressed well (I-685), which says nothing about the next guest's.
func (w *writeRate) observe(n uint64, d time.Duration) {
	if n == 0 || d <= 0 {
		return
	}
	r := float64(n) / d.Seconds()
	w.mu.Lock()
	defer w.mu.Unlock()
	if r < w.bps {
		w.bps = r
	}
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
// pausedAlready reports whether the guest's vCPUs are already stopped, so
// a freeze must not pause it again (I-818, S7). vm.info is the authority,
// since it is the hypervisor that refuses the second pause; the resting
// mark answers for a hypervisor that cannot be asked.
func (m *Manager) pausedAlready(ctx context.Context, g *state.Guest, sock string) bool {
	info, err := m.d.CH.Info(ctx, sock)
	if err == nil {
		return ch.VMState(info) == ch.StatePaused
	}
	return m.restingMark(g.GuestID) != nil || g.Resting != nil
}

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

// freezeGuest freezes one guest. It runs on the guest's worker. wall is
// the hard wall of the FreezeAll it belongs to (zero for none): nothing
// here waits past it, so the unit's ExecStop returns and the freeze gets
// to count and record what it left behind.
func (m *Manager) freezeGuest(ctx context.Context, id, reason string, wall time.Time) FreezeResult {
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
	// re-create (that is, remove) its finished image. A unit that cannot
	// be asked is taken as running, as at the listing: the freeze then
	// fails on the pause and is counted, rather than leaving the guest
	// out of the freeze and out of the tally.
	if !m.unitRunning(ctx, g) {
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
		Closure: g.SystemClosure, Class: g.Class, VolumeBytes: g.VolumeBytes, CHVersion: chv, FS: m.d.Freeze.Image.FS(),
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
		m.log(g).Error("freeze failed; guest keeps running", attrs...)
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
	if err := m.d.Freeze.Image.Create(ctx, id, imageVolumeBytes(g.Class), key, m.imageMount(id), uid, gid); err != nil {
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
	//
	// A guest the idle pause left resting (I-818) is paused already.
	// Cloud Hypervisor refuses a second vm.pause, which here would
	// discard the image, count the guest unfrozen and leave it to
	// cold-boot and lose every process at the next host restart --
	// I-796's own incident, re-created by the pause. Such a guest is
	// snapshotted as it is.
	if !m.pausedAlready(ctx, g, sock) {
		if err := m.d.CH.Pause(ctx, sock); err != nil {
			return fail("pause failed", err)
		}
	}
	pausedAt := m.d.Now()
	// The hypervisor cannot be interrupted mid-snapshot, so the call is not
	// tied to the freeze's deadline: giving up on it early only leaves the
	// hypervisor busy and the guest paused. It is bounded by the freeze's
	// wall instead, and by snapshotTimeout when there is none -- waiting
	// past the wall is what gets hostd killed with nothing counted.
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), until(m.d.Now(), wall, snapshotTimeout, 0))
	err = fz.Snapshot(sctx, sock, m.snapDir(id))
	scancel()
	if err != nil {
		if !m.resumeAfterFailedSnapshot(context.WithoutCancel(ctx), g, sock, wall) {
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
	// The image is whole once it is on disk: recorded as such, a hostd
	// killed from here on (before the close below) leaves an image the
	// next start can finish and restore rather than discard.
	if err := syncDir(m.snapDir(id)); err != nil {
		m.log(g).Warn("image not synced after the snapshot", "event", "guest_freeze", "err", err.Error())
	} else {
		rec.State = freeze.StateWritten
		if err := recs.Put(rec); err != nil {
			m.log(g).Warn("record not marked written", "event", "guest_freeze", "err", err.Error())
		}
	}
	// The image is written; the rest (end the hypervisor, close the image,
	// mark the record) runs to the end even if the deadline has passed,
	// so the record never says less than what happened -- but not past
	// the wall. A record left at `written` is finished at the next start
	// (finishWrittenImage); a freeze systemd killed here is not.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), until(m.d.Now(), wall, 60*time.Second, 10*time.Second))
	defer cancel()

	if m.cutFreeze != nil && m.cutFreeze(id, "snapshot") {
		res.Reason = "cut"
		return res
	}

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
	if m.cutFreeze != nil && m.cutFreeze(id, "stopped") {
		res.Reason = "cut"
		return res
	}

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
	// A guest the idle pause had left resting is not resting any more
	// (I-818): its memory is in the image, not resident, and a wake
	// cannot bring it back in milliseconds. The thaw's restore puts it
	// back to plain running, and the api's rule rests it again if it is
	// still idle.
	if g.Resting != nil {
		if err := m.setResting(g, nil, "frozen"); err != nil {
			m.log(g).Warn("resting mark not cleared by the freeze", "event", "guest_resting", "err", err.Error())
		}
	}
	res.OK, res.Outcome, res.Reason = true, "frozen", ""
	res.ImageBytes = rec.ImageBytes
	res.Ms = m.d.Now().Sub(start).Milliseconds()
	m.log(g).Info("guest frozen", "event", "guest_freeze", "reason", reason, "class", g.Class,
		"snapshot_ms", snapMs, "duration_ms", res.Ms, "image_bytes", rec.ImageBytes)
	return res
}

// imageVolumeBytes is the size of a guest's image volume: its memory, plus
// the device state, plus room for the filesystem itself. The volume is
// thin, so what it does not write costs nothing in the pool, and the
// headroom is what keeps the pathological image -- a guest whose memory is
// full of incompressible bytes -- from running the volume out of space:
// btrfs allocates data in chunks of up to 1 GiB and needs a metadata and a
// system chunk besides (I-685).
func imageVolumeBytes(class string) uint64 {
	return (Classes[class].MemMiB + 1536) << 20
}

// snapshotTimeout bounds one vm.snapshot where no wall does: an xl
// guest's 16 GiB at 100 MB/s is under 3 minutes.
const snapshotTimeout = 10 * time.Minute

// atRate is how long n bytes take at bps bytes a second; zero bps reads
// as the default rate rather than forever.
func atRate(n, bps uint64) time.Duration {
	if bps == 0 {
		bps = 100_000_000
	}
	return time.Duration(float64(n) / float64(bps) * float64(time.Second))
}

// until is how long is left of wall, at most d and at least floor; d when
// wall is zero. It keeps a wait inside the freeze's hard wall. A floor of
// zero means a wait reached past the wall gets no time at all, which is
// the point; a floor above it buys the little a wait needs to finish
// something the next start would otherwise redo.
func until(now time.Time, wall time.Time, d, floor time.Duration) time.Duration {
	if wall.IsZero() {
		return d
	}
	left := wall.Sub(now)
	if left > d {
		left = d
	}
	if left < floor {
		left = floor
	}
	return left
}

// resumeAfterFailedSnapshot brings a guest back from the pause of a freeze
// that failed, or that a hostd restart cut short. The hypervisor may still
// be busy with the snapshot (its API answers nothing until it is done), so
// the resume is retried until vm.info says Running, for up to ResumeWait
// and never past wall (zero for none). It reports whether the guest runs;
// one left paused keeps its `writing` record and the next start resumes
// or finishes it.
func (m *Manager) resumeAfterFailedSnapshot(ctx context.Context, g *state.Guest, sock string, wall time.Time) bool {
	deadline := m.d.Now().Add(until(m.d.Now(), wall, m.fcfg().ResumeWait, 0))
	var last error
	for m.d.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		last = m.d.CH.Resume(rctx, sock)
		info, ierr := m.d.CH.Info(rctx, sock)
		cancel()
		if ierr == nil && strings.Contains(string(info), `"Running"`) {
			m.log(g).Info("guest resumed after a freeze that did not finish", "event", "guest_freeze")
			// A guest the idle pause had left resting runs again now
			// (I-818): the mark would otherwise silence its samples for
			// a guest whose vCPUs are going. The api's rule pauses it
			// again at its next tick if it is still idle.
			if g.Resting != nil {
				if err := m.setResting(g, nil, "resumed"); err != nil {
					m.log(g).Warn("resting mark not cleared after the resume", "event", "guest_resting", "err", err.Error())
				}
			}
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
	m.replayTally()
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

	// What a thaw has to read back is what the freeze wrote, so its
	// window comes from the same arithmetic: RestoreTimeout for the
	// surroundings of every restore (the shares, the image open, guestd's
	// first answer) plus however long the images themselves take to read
	// at the host's rate. A window fixed while the freeze side's budget
	// grew is how a host spends its shutdown writing images the next boot
	// throws away (I-796).
	var total uint64
	for _, p := range preps {
		if p != nil {
			total += p.rec.ImageBytes
		}
	}
	window, wcancel := context.WithDeadline(ctx, begin.Add(fc.UnwrapTimeout+fc.RestoreTimeout+atRate(total, fc.WriteRate)))
	defer wcancel()
	sem := make(chan struct{}, fc.RestoreConcurrency)
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
	m.reportDiscarded(results)
	m.d.Log.Info("frozen guests restored", "event", "guest_thaw", "restored", n, "images", len(ids),
		"duration_ms", time.Since(begin).Milliseconds())
	return results
}

// A guest whose image a thaw discards loses exactly what a guest the
// freeze never took loses: the host comes back, the api starts it cold,
// and its agents, tmux windows and open files are gone. The freeze half
// of that was counted and the restore half was a Warn line, so the
// incident I-796 exists to end stayed silent whenever the loss happened
// on the way back.

// thawDiscardedReason maps one thaw result to the counter's reason, or ""
// when nothing was lost: a guest that stopped, moved host or came back
// running on its own had no memory owed to it.
func thawDiscardedReason(r FreezeResult) string {
	if r.OK || r.Outcome != "discarded" {
		return ""
	}
	switch r.Reason {
	case "image incomplete", "record unreadable":
		return "incomplete"
	case "image older than the restore window":
		return "too_old"
	case "closure changed since the freeze", "class changed since the freeze",
		"volume resized since the freeze", "guest volume changed since the freeze",
		"no volume fingerprint", "hypervisor version changed since the freeze":
		return "changed"
	case "image key unavailable", "image key does not match":
		return "no_key"
	case "restore window passed":
		return "window"
	case "restore failed", "image volume not opened", "guest volume unavailable",
		"hypervisor client cannot restore":
		return "failed"
	}
	// "guest already running" (it was resumed), "guest no longer on this
	// host", "guest state changed since the freeze" (it was stopped or
	// destroyed meanwhile), "freeze turned off", "restored".
	return ""
}

// reportDiscarded counts and logs the guests a thaw could not give their
// memory back to.
func (m *Manager) reportDiscarded(results []FreezeResult) {
	counts := map[string]int{}
	for _, r := range results {
		why := thawDiscardedReason(r)
		if why == "" {
			continue
		}
		counts[why]++
		m.d.Log.Error("frozen guest not restored; it starts cold and loses what it was running",
			"event", "guest_thaw", "guest_id", r.GuestID, "cause", why, "detail", r.Reason)
	}
	m.countDiscarded(counts)
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
	// and its hypervisor may still be writing the image. A VM a snapshot
	// has been taken of must not simply be resumed: its virtio-fs stops
	// answering (Cloud Hypervisor 53 with virtiofsd 1.14, host-stg-01
	// 2026-10-12: every program run from /nix/store hung until the guest
	// was restored from an image). So once the hypervisor answers and the
	// VM is paused, its freeze is done again from the start of the
	// snapshot and the guest restored below. Only when that cannot be
	// done is it resumed; the image is removed once the guest runs, and
	// otherwise kept with its record for the next start to try again.
	refrozen := gerr == nil && m.FreezeEnabled() && m.refreezePaused(ctx, g, rec)
	if a, _ := m.d.Systemd.IsActive(ctx, GuestUnit(id)); a && !refrozen {
		if !m.resumeAfterFailedSnapshot(ctx, g, ch.APISocket(m.guestDir(id)), time.Time{}) {
			res.Outcome, res.Reason = "skipped", "guest still paused; image kept"
			res.Ms = m.d.Now().Sub(start).Milliseconds()
			return nil, res
		}
		return discard("guest already running")
	}
	if gerr == nil && rec.State == freeze.StateWritten && m.FreezeEnabled() {
		if why := m.finishWrittenImage(ctx, g, rec); why != "" {
			return discard(why)
		}
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

// refreezePaused finishes the freeze of a guest whose hypervisor outlived
// hostd mid-freeze with the VM paused: it snapshots again into the image
// still mounted from that freeze, stops the hypervisor and completes the
// record, so the guest is restored from the image rather than resumed. It
// reports whether it did; false leaves the guest to the resume.
func (m *Manager) refreezePaused(ctx context.Context, g *state.Guest, rec *freeze.Record) bool {
	id := g.GuestID
	if a, _ := m.d.Systemd.IsActive(ctx, GuestUnit(id)); !a {
		return false
	}
	fz, ok := m.d.CH.(ch.Freezer)
	if !ok {
		return false
	}
	// The image is still mounted only in the boot that made it.
	if fi, err := os.Stat(m.snapDir(id)); err != nil || !fi.IsDir() {
		return false
	}
	sock := ch.APISocket(m.guestDir(id))
	// A hypervisor mid-snapshot answers nothing until it is done.
	deadline := m.d.Now().Add(m.fcfg().ResumeWait)
	paused := false
	for m.d.Now().Before(deadline) {
		ictx, cancel := context.WithTimeout(ctx, 10*time.Second)
		info, err := m.d.CH.Info(ictx, sock)
		cancel()
		if err == nil {
			paused = strings.Contains(string(info), `"Paused"`)
			break
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !paused {
		return false
	}
	lookup := m.cfg.Lookup
	if lookup == nil {
		lookup = LookupUser
	}
	uid, gid, err := lookup(m.cfg.GuestUser)
	if err != nil {
		return false
	}
	if err := os.RemoveAll(m.snapDir(id)); err != nil {
		return false
	}
	if err := ensureDir(m.snapDir(id), 0o700, uid, gid); err != nil {
		return false
	}
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotTimeout)
	err = fz.Snapshot(sctx, sock, m.snapDir(id))
	scancel()
	if err != nil {
		m.log(g).Warn("snapshot after a freeze cut short failed", "event", "guest_thaw", "err", err.Error())
		return false
	}
	if err := syncDir(m.snapDir(id)); err != nil {
		return false
	}
	rec.State = freeze.StateWritten
	if err := m.records().Put(rec); err != nil {
		return false
	}
	if mon := m.removeMonitor(id); mon != nil {
		mon.stop()
	}
	_ = m.d.CH.Shutdown(ctx, sock)
	wctx, wcancel := context.WithTimeout(ctx, 3*time.Second)
	if err := m.d.Systemd.WaitInactive(wctx, GuestUnit(id)); err != nil {
		_ = m.d.Systemd.Kill(ctx, GuestUnit(id))
	}
	wcancel()
	m.log(g).Info("paused guest frozen again after hostd restarted", "event", "guest_thaw")
	return true
}

// finishWrittenImage does what a freeze killed after its snapshot did
// not: the hypervisor is gone (checked by the caller), so the guest's
// leftovers go, the volume is fingerprinted (nothing wrote to it since the
// pause), the image is closed and the record marked complete. It returns
// why the image cannot be used, or "".
func (m *Manager) finishWrittenImage(ctx context.Context, g *state.Guest, rec *freeze.Record) string {
	id := g.GuestID
	m.teardown(ctx, g)
	if rec.ImageBytes == 0 {
		rec.ImageBytes = m.imageBytes(m.snapDir(id))
	}
	if err := m.d.Freeze.Image.Close(ctx, id, m.imageMount(id)); err != nil {
		m.log(g).Warn("image not closed", "event", "guest_thaw", "err", err.Error())
		return "image not closed"
	}
	if m.d.Freeze.Fingerprint == nil {
		return "no volume fingerprint"
	}
	if err := m.d.LVM.Activate(ctx, VolumeName(id)); err != nil {
		return "guest volume unavailable"
	}
	fp, err := m.d.Freeze.Fingerprint(m.d.LVM.DevPath(VolumeName(id)))
	if err != nil {
		return "no volume fingerprint"
	}
	rec.Fingerprint = fp
	rec.State, rec.DoneAt = freeze.StateComplete, m.d.Now().UTC()
	if err := m.records().Put(rec); err != nil {
		return "record not written"
	}
	m.log(g).Info("freeze finished after hostd restarted", "event", "guest_thaw")
	return ""
}

// syncDir flushes the filesystem holding dir (syncfs).
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only handle
	return unix.Syncfs(int(f.Fd()))
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
	if err := m.d.Freeze.Image.Open(ctx, id, p.key, m.imageMount(id), rec.FS); err != nil {
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
	// Phase timings, so a slow restore says which part was slow; they are
	// durations and ids only (OBSERVABILITY.md).
	t0 := m.d.Now()
	phases := make([]any, 0, 12)
	phase := func(name string) {
		now := m.d.Now()
		phases = append(phases, name+"_ms", now.Sub(t0).Milliseconds())
		t0 = now
	}
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
	phase("net")
	vcfg := virtiofs.Config{SharedDir: m.cfg.StoreExport, User: m.cfg.VirtiofsUser, Group: m.cfg.VirtiofsUser, SocketGroup: m.cfg.GuestUser, Binary: m.cfg.VirtiofsBinary}
	if err := virtiofs.Start(ctx, m.d.Systemd, vcfg, g.GuestID, ch.VirtiofsSocket(dir)); err != nil {
		return fmt.Errorf("virtiofsd: %w", err)
	}
	if err := m.waitSocket(ctx, virtiofs.Unit(g.GuestID), ch.VirtiofsSocket(dir)); err != nil {
		return fmt.Errorf("virtiofsd: %w", err)
	}
	phase("virtiofsd")
	if err := m.extendView(ctx, g.GuestID, m.viewClosures(g)); err != nil {
		return fmt.Errorf("store view: %w", err)
	}
	phase("store_view")
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
	rctx, rcancel := context.WithTimeout(ctx, fc.RestoreTimeout+atRate(rec.ImageBytes, fc.WriteRate))
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
	phase("image_read")
	// The image has been read into the guest's memory: close it now, so
	// its key leaves the kernel before the guest runs again.
	if err := m.d.Freeze.Image.Close(ctx, g.GuestID, m.imageMount(g.GuestID)); err != nil {
		m.log(g).Warn("image not closed after the restore", "event", "guest_thaw", "err", err.Error())
	}
	if err := m.d.CH.Resume(ctx, sock); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	phase("resume")
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
	phase("guestd")
	m.log(g).Info("restore phases", append([]any{"event", "guest_thaw"}, phases...)...)
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
		// A resting guest cannot answer, and does not need to: its clock
		// is set by its wake (I-818), which is the only thing that lets
		// anything in it read a clock again.
		if m.restingMark(g.GuestID) != nil {
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
