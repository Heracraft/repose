package guest

import (
	"bufio"
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

// The idle pause (DECISIONS I-818). A machine nobody is using has its
// vCPUs stopped and its memory left resident on the host: every process,
// socket and open file survives, the hypervisor, virtiofsd, tap and vsock
// stay up, and the guest comes back in milliseconds. Nothing is written
// anywhere, so a paused guest's secrets stay where they were and there is
// no fourth home for one (CLAUDE.md, "Secrets have three homes").
//
// The api decides which machine may rest and hostd executes (S1 of
// docs/workstreams/18-idle-pause.md): two of the rule's terms live only in
// Postgres. hostd keeps the safeties only it can see -- a command in
// flight for the guest, the host going down, a user timer about to fire --
// and it wakes a guest itself before anything that needs guestd.
//
// `resting` is a marker on a guest whose state stays `running` (S2). A
// new state string would stop billing counting the minute, break the
// ten-value enum of docs/interfaces/README.md and the label set of
// repose_host_guests, and be dropped by the api's Hello reconcile.

// FeaturePause is the Hello feature that says this hostd takes PauseGuest
// and WakeGuest.
const FeaturePause = "pause"

// PauseHold is how close the next user timer inside the guest may be
// before a pause is refused. A machine whose cron is about to run is a
// machine in use; waking for it is the armed wake below, and 15 minutes
// is the same quiet window the api's rule reads.
var PauseHold = 15 * time.Minute

// TimerWakeLead is how far ahead of a user timer's elapse hostd wakes the
// guest, so the guest is running, its clock set and systemd's own timers
// recalculated before the moment arrives.
var TimerWakeLead = time.Minute

// PrepareTimeout bounds each of the pause's two Execs in the guest (the
// write-back and the timer read), as the freeze's write-back is bounded
// (freeze.go).
var PrepareTimeout = 10 * time.Second

// WakeResumeWait is how long vm.resume is retried until vm.info says the
// VM runs, and WakeGuestdWait how long guestd then has to answer. Both
// are inside the gateway's 10 s dial budget together.
var (
	WakeResumeWait = 5 * time.Second
	WakeGuestdWait = 5 * time.Second
	wakeResumePoll = 50 * time.Millisecond
	// maxWakeResumePoll caps the backoff resumeVM grows from
	// wakeResumePoll, so a resume that takes its whole budget costs a
	// handful of vm.info calls rather than a hundred (I-920).
	maxWakeResumePoll = 800 * time.Millisecond
)

// why_not words of PauseResult. A refusal is not a failure: the api reads
// the word and tries again at its next tick.
const (
	whyNotRunning      = "not_running"
	whyAlreadyResting  = "already_resting"
	whyCommandInFlight = "command_in_flight"
	whyHostStopping    = "host_stopping"
	// whyFrozen is the host-restart freeze (I-652), not the idle pause: a
	// guest with a freeze record is mid-freeze or waiting for a thaw. The
	// word a USER reads for an idle pause is "frozen" (I-826); these are
	// hostd's own log and metric words, and `already_resting` is the one
	// that means "this feature has it".
	whyFrozen           = "frozen"
	whyTimerDue         = "timer_due"
	whyGuestUnreachable = "guest_unreachable"
	// whyInUse: a session or a tmux client appeared between the api's
	// newest sample and this pause.
	whyInUse = "in_use"
)

// Pause reasons hostd raises itself.
const (
	PauseReasonIdle     = "idle"
	PauseReasonOperator = "operator"
	WakeReasonKeepAwake = "keep_awake"
	WakeReasonTimer     = "timer"
	WakeReasonCommand   = "command"
	// The api's words (internal/api/resting): a session through the
	// gateway, an op's wake phase, an answer carried to a waiting agent.
	WakeReasonConnect = "connect"
	WakeReasonOp      = "op"
	WakeReasonAnswer  = "answer"
)

// pauseReason bounds a caller's reason to the label enum of
// repose_host_guest_pause_total: a reason string from the api must not
// grow the series without bound.
func pauseReason(reason string) string {
	switch reason {
	case PauseReasonIdle, PauseReasonOperator, WakeReasonKeepAwake, WakeReasonTimer, WakeReasonCommand,
		// internal/api/resting's own words for a wake. They are most of
		// the wakes there are, so collapsing them into `other` would
		// leave the label saying nothing (metrics.PauseReasons).
		WakeReasonConnect, WakeReasonOp, WakeReasonAnswer:
		return reason
	}
	return "other"
}

// restingMark is the guest's marker, from the Manager's own map rather
// than bbolt: the sampler, the monitor and every session caller read it,
// and bbolt is the record, not the hot path. It is loaded at Reconcile and
// written only by setResting and setState. restMu, not mu: the monitor
// goroutine calls this, and Close holds mu while it waits for that
// goroutine (manager.go).
func (m *Manager) restingMark(id string) *state.Resting {
	m.restMu.Lock()
	defer m.restMu.Unlock()
	return m.restingNow[id]
}

// Resting reports whether the guest's vCPUs are stopped (the operator
// view and the sampler).
func (m *Manager) Resting(id string) bool { return m.restingMark(id) != nil }

// setResting writes the marker and tells the api. It is deliberately not
// setState: the guest's state does not change, and a state change would
// move repose_host_guests, emit a guest_state event the api routes
// through its state-change path, and cancel the project's open questions.
func (m *Manager) setResting(g *state.Guest, r *state.Resting, reason string) error {
	g.Resting = r
	if err := m.d.State.PutGuest(g); err != nil {
		return err
	}
	m.writeGuestJSON(g)
	m.noteResting(g.GuestID, r)
	at := m.d.Now()
	if r != nil {
		at = r.Since
	}
	m.log(g).Info("guest resting changed", "event", "guest_resting", "resting", r != nil, "reason", reason)
	m.emitEvent(&hostdv1.Event_GuestResting{GuestResting: &hostdv1.GuestResting{
		GuestId: g.GuestID, Resting: r != nil, At: at.Unix(), Reason: reason,
	}})
	return nil
}

// noteResting keeps the in-memory map and the resting gauge in step with
// bbolt. A nil mark also disarms the guest's timer wake.
func (m *Manager) noteResting(id string, r *state.Resting) {
	m.restMu.Lock()
	if r == nil {
		delete(m.restingNow, id)
		if t := m.timerWakes[id]; t != nil {
			t.Stop()
			delete(m.timerWakes, id)
		}
	} else {
		m.restingNow[id] = r
	}
	n := len(m.restingNow)
	m.restMu.Unlock()
	if m.d.Metrics != nil {
		m.d.Metrics.GuestsResting.Set(float64(n))
	}
}

// wakeLock serialises the wakes of one guest: the command, hostd's own
// wake before a session caller, and the armed timer wake may all arrive
// at once, and only one may drive vm.resume.
func (m *Manager) wakeLock(id string) *sync.Mutex {
	l, _ := m.wakeLocks.LoadOrStore(id, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// --- pause ------------------------------------------------------------

// pause answers a PauseGuest. Every refusal is `ok` with paused=false and
// a lower_snake why_not; only a guest hostd cannot act on at all is an
// error.
func (m *Manager) pause(ctx context.Context, commandID string, c *hostdv1.PauseGuest) (*hostdv1.PauseResult, *Error) {
	reason := c.Reason
	if reason == "" {
		reason = PauseReasonIdle
	}
	g, gerr := m.getGuest(c.GuestId)
	if gerr != nil {
		m.countPause(reason, "failed")
		return nil, gerr
	}
	refuse := func(why string) (*hostdv1.PauseResult, *Error) {
		m.countPause(reason, "refused")
		m.log(g).Info("pause refused", "event", "guest_resting", "reason", reason, "why_not", why)
		return &hostdv1.PauseResult{Paused: false, WhyNot: why}, nil
	}
	switch {
	case m.Stopping(ctx):
		return refuse(whyHostStopping)
	case g.State != StateRunning:
		return refuse(whyNotRunning)
	case g.Resting != nil:
		return refuse(whyAlreadyResting)
	case m.commandInFlight(g.GuestID, commandID):
		return refuse(whyCommandInFlight)
	}
	// A guest with a freeze record is mid-freeze or waiting for a thaw;
	// its hypervisor is paused or gone and the freeze owns it (S7).
	if m.fcfg().RecordDir != "" {
		if r, _ := m.records().Get(g.GuestID); r != nil {
			return refuse(whyFrozen)
		}
	}
	sess, serr := m.session(g.GuestID)
	if serr != nil {
		return refuse(whyGuestUnreachable)
	}
	// Every refusal comes before anything is done TO the guest. The api
	// asks once a minute, so a guest that is refused every time -- one
	// with a timer due inside the hold, which is a guest with a timer
	// every few minutes -- would otherwise have its page cache dropped
	// once a minute for ever (the write-back below is what drops it).
	next := m.nextUserTimer(ctx, g, sess)
	if !next.IsZero() && next.Sub(m.d.Now()) < PauseHold {
		return refuse(whyTimerDue)
	}
	// Nothing opened a session since the api read its samples. The api's
	// newest sample can be a minute old and its own terms are the
	// authority on what is idle, but a pause that lands on a session
	// opened in that minute freezes a user mid-keystroke, and the
	// gateway's relay then kills their session on its keepalive rather
	// than waking anything. A fresh read costs one vsock round trip.
	if sr, err := sess.Sample(ctx); err == nil && sr != nil {
		if sg := sr.GetSignals(); sg.GetSshSessions() > 0 || sg.GetTmuxClients() > 0 {
			return refuse(whyInUse)
		}
	}
	// The write-back the freeze already does before it pauses
	// (freeze.go): every dirty page reaches the volume while the guest
	// still runs, and a guest with the balloon also drops its page cache
	// so free page reporting hands those pages back to the host instead
	// of holding them for the whole rest.
	script := "sync"
	if m.argsHaveBalloon(g.GuestID) {
		script = "sync; echo 3 > /proc/sys/vm/drop_caches"
	}
	dctx, dcancel := context.WithTimeout(ctx, PrepareTimeout)
	r, err := sess.Exec(dctx, []string{"/bin/sh", "-c", script}, uint32(PrepareTimeout/time.Second), "")
	dcancel()
	if err != nil || r.GetExitCode() != 0 {
		// The same treatment the freeze gives it: the pause is worth
		// having without the write-back, since nothing is written to
		// disk and the guest keeps running.
		m.log(g).Warn("guest cache write-back failed before the pause", "event", "guest_resting")
	}
	sock := ch.APISocket(m.guestDir(g.GuestID))
	// The record goes down BEFORE the pause, the way the freeze writes
	// its own record before it pauses (freeze.go) and for the same
	// reason: a hostd that dies between the two leaves a guest whose
	// vCPUs are stopped, and the recoverable direction is a mark with no
	// pause, not a pause with no mark. Reconcile reads vm.info for a
	// marked guest and clears a mark the hypervisor contradicts, so a
	// write that lands and a pause that then fails costs one skipped
	// guest-side sample; the other order costs a guest nothing would
	// ever wake and a `repose start` that cold-boots it (I-824).
	at := m.d.Now()
	mark := &state.Resting{Since: at, Reason: pauseReason(reason), NextTimer: next}
	if err := m.setResting(g, mark, reason); err != nil {
		m.countPause(reason, "failed")
		m.log(g).Error("resting mark not written; guest keeps running", "event", "guest_resting", "err", err.Error())
		return nil, errf(CodeInternal, "state write: %v", err)
	}
	if err := m.d.CH.Pause(ctx, sock); err != nil {
		// The mark is down and the guest runs. Take it off; a mark the
		// hypervisor contradicts is cleared at the next reconcile too,
		// but until then the sampler would skip this guest's own
		// signals, and a wake would be sent for a guest that never
		// paused.
		if cerr := m.setResting(g, nil, "pause_failed"); cerr != nil {
			// Now the record says resting and the guest is not, and
			// nothing here can fix it. Reconcile corrects it at hostd's
			// next start; until then this guest's samples carry the
			// signals it had, so the operator is told rather than left
			// to read a stale sample as the truth.
			m.Warn("guest_resting_stale", "guest "+g.GuestID+": marked resting but the hypervisor never paused")
			m.log(g).Error("resting mark not cleared after a failed pause", "event", "guest_resting", "err", cerr.Error())
		}
		m.countPause(reason, "failed")
		m.log(g).Error("pause failed; guest keeps running", "event", "guest_resting", "err", err.Error())
		return nil, errf(CodeInternal, "cloud-hypervisor vm.pause: %v", err)
	}
	m.armTimerWake(g)
	m.countPause(reason, "paused")
	return &hostdv1.PauseResult{Paused: true, At: at.Unix()}, nil
}

func (m *Manager) countPause(reason, result string) {
	if m.d.Metrics != nil {
		m.d.Metrics.GuestPause.WithLabelValues(pauseReason(reason), result).Inc()
	}
}

// commandInFlight reports whether any command other than exceptID is in
// flight for the guest -- admitted by the api and either running or
// queued on the guest's worker. A machine the api has work for is not a
// machine to pause, and a pause that queued behind that work would pause
// a guest the api expects to be running.
func (m *Manager) commandInFlight(guestID, exceptID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.inflight {
		if id != exceptID && m.inflightTarget[id] == guestID {
			return true
		}
	}
	return false
}

// nextUserTimer is the soonest wall-clock elapse of a systemd timer in the
// guest, zero when there is none or the guest did not say. Both managers
// are read -- the system one and `dev`'s, which is where a user's own
// timer lives -- with `systemctl show`, whose NextElapseUSecRealtime is
// microseconds since the epoch, rather than `list-timers`, whose columns
// are localised prose.
//
// Only the realtime elapse is read. A monotonic timer (OnBootSec,
// OnUnitActiveSec) counts the guest's own uptime, which stops with its
// vCPUs: it keeps its remaining time across a rest and nothing is missed.
func (m *Manager) nextUserTimer(ctx context.Context, g *state.Guest, sess vsockclient.Session) time.Time {
	const script = `systemctl show '*.timer' --property=NextElapseUSecRealtime 2>/dev/null; ` +
		`systemctl --user -M dev@ show '*.timer' --property=NextElapseUSecRealtime 2>/dev/null`
	tctx, cancel := context.WithTimeout(ctx, PrepareTimeout)
	defer cancel()
	r, err := sess.Exec(tctx, []string{"/bin/sh", "-c", script}, uint32(PrepareTimeout/time.Second), "")
	if err != nil {
		// Unknown is not "none": a guest that cannot say keeps its
		// pause, since the armed wake is a courtesy and the user's own
		// first keystroke wakes it anyway.
		m.log(g).Warn("next timer elapse not read before the pause", "event", "guest_resting")
		return time.Time{}
	}
	return soonestElapse(r.GetStdout())
}

// soonestElapse reads the soonest NextElapseUSecRealtime of a `systemctl
// show` output. 0 and an unparseable value mean that unit has no next
// realtime elapse.
func soonestElapse(out []byte) time.Time {
	var soonest time.Time
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		_, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		us, err := strconv.ParseUint(v, 10, 64)
		if err != nil || us == 0 {
			continue
		}
		t := time.UnixMicro(int64(us))
		if soonest.IsZero() || t.Before(soonest) {
			soonest = t
		}
	}
	return soonest
}

// armTimerWake wakes the guest TimerWakeLead before the user timer the
// pause read, so a machine whose cron fires during a rest runs it. The
// api's rule pauses nothing with a timer inside PauseHold, so this only
// ever fires for a timer further out than that.
func (m *Manager) armTimerWake(g *state.Guest) {
	if g.Resting == nil || g.Resting.NextTimer.IsZero() {
		return
	}
	in := g.Resting.NextTimer.Sub(m.d.Now()) - TimerWakeLead
	if in < 0 {
		in = 0
	}
	id := g.GuestID
	m.restMu.Lock()
	if old := m.timerWakes[id]; old != nil {
		old.Stop()
	}
	m.timerWakes[id] = time.AfterFunc(in, func() {
		// On the guest's own worker, taking no op slot: a wake is
		// milliseconds and must not queue behind the host's work.
		m.enqueue(id, job{noSlot: true, internal: func(ctx context.Context) {
			if _, err := m.wakeByID(ctx, id, WakeReasonTimer); err != nil {
				m.d.Log.Warn("timer wake failed", "event", "guest_resting", "guest_id", id, "code", err.Code)
			}
		}})
	})
	m.restMu.Unlock()
	m.log(g).Info("timer wake armed", "event", "guest_resting", "in_s", int64(in.Seconds()))
}

// --- wake -------------------------------------------------------------

// wake answers a WakeGuest. It is idempotent: a guest with no resting
// mark is `ok` with was_resting false, whatever its state, since there is
// nothing to resume.
func (m *Manager) wake(ctx context.Context, c *hostdv1.WakeGuest) (*hostdv1.WakeResult, *Error) {
	reason := c.Reason
	if reason == "" {
		reason = WakeReasonCommand
	}
	return m.wakeByID(ctx, c.GuestId, reason)
}

func (m *Manager) wakeByID(ctx context.Context, id, reason string) (*hostdv1.WakeResult, *Error) {
	if id == "" {
		return nil, errf(CodeInvalidArgument, "guest_id required")
	}
	l := m.wakeLock(id)
	l.Lock()
	defer l.Unlock()
	// Read the record under the lock: a wake that waited behind another
	// one must see what that one left.
	g, gerr := m.getGuest(id)
	if gerr != nil {
		return nil, gerr
	}
	if g.Resting == nil {
		return &hostdv1.WakeResult{WasResting: false}, nil
	}
	if g.State != StateRunning {
		// A guest whose state moved is not resting; the mark is stale.
		if err := m.setResting(g, nil, reason); err != nil {
			m.log(g).Warn("stale resting mark not cleared", "event", "guest_resting", "err", err.Error())
		}
		m.countWakeFailed("state")
		return nil, errf(CodeInvalidArgument, "guest is %s; wake needs running", g.State)
	}
	start := m.d.Now()
	sock := ch.APISocket(m.guestDir(id))
	if err := m.resumeVM(ctx, g, sock); err != nil {
		m.countWakeFailed("resume")
		m.Warn("guest_wake_failed", "guest "+id+": the hypervisor did not resume")
		m.log(g).Error("wake failed", "event", "guest_resting", "phase", "resume", "err", err.Error())
		return nil, errf(CodeGuestUnresponsive, "cloud-hypervisor vm.resume: %v", err)
	}
	// The clock step, and the desktop's stamps with it, through the Exec
	// that already exists rather than a new guestd request (S5).
	//
	// The step corrects nothing on this path and is kept as a safety net:
	// measured on host-stg-01 (2026-10-21, CH 53), a plain vm.pause does
	// not stop the clock the guest reads -- it stops the vCPUs, not the
	// TSC -- so across a 120 s and a 2,101 s pause the guest's
	// CLOCK_REALTIME stayed inside a host bracket and its uptime advanced
	// by the full wall time. I-233's 10 s of lag is a snapshot and
	// restore, where the clock comes back with the image; that is the
	// freeze's path, not this one.
	// From here the vCPUs are running, so the guest is not resting
	// whatever guestd does. The mark goes now rather than at the end:
	// left behind, it makes the sampler repeat the pre-pause signals for
	// a running guest for ever and the api call a working machine frozen.
	// A guestd that does not answer is a failed wake and is reported as
	// one, but it is not a resting guest.
	if err := m.setResting(g, nil, reason); err != nil {
		m.log(g).Warn("resting mark not cleared after the resume", "event", "guest_resting", "err", err.Error())
	}
	sess, serr := m.awaitSession(ctx, id, WakeGuestdWait, nil)
	if serr != nil {
		m.countWakeFailed("guestd")
		m.Warn("guest_wake_failed", "guest "+id+": no answer from the guest after the resume")
		m.log(g).Error("wake failed", "event", "guest_resting", "phase", "guestd")
		return nil, serr
	}
	sec := strconv.FormatFloat(float64(m.d.Now().UnixNano())/1e9, 'f', 6, 64)
	cctx, ccancel := context.WithTimeout(ctx, PrepareTimeout)
	// The clock step and the desktop's idle stamps go in one call: the
	// step is what makes the stamps wrong (DECISIONS I-822). The
	// desktop's per-minute check ages last-client and last-cdp against
	// the wall clock and stops the viewer and the agents' browser at 30
	// minutes idle; after a rest of an hour those stamps read an hour
	// old the moment the clock moves, and the check would kill the
	// browser -- its tabs, its logins and the agents' CDP connections --
	// within a minute of the user coming back -- not because the clock
	// jumped, which it does not (above), but because the time really
	// passed and nothing in the guest could touch a stamp while it did.
	// Nobody was idle while the machine was asleep, so the wake restores
	// the baseline.
	//
	// The stamps are touched by path and left dev's, which is what
	// repose-desktop-stamp does (I-727): that command is in the desktop
	// units' own runtimeInputs and not on any PATH hostd's Exec gets, so
	// calling it by name did nothing. A guest with no desktop has no such
	// directory, which is not a failed wake -- but the clock step's own
	// status is kept, because the RUNBOOK tells an operator to grep for
	// the line it logs.
	script := "date -u -s @" + sec + "; rc=$?; " +
		`d=/run/repose/desktop; if [ -d "$d" ]; then touch "$d/last-client" "$d/last-cdp" && chown dev:dev "$d/last-client" "$d/last-cdp"; fi; exit $rc`
	if r, err := sess.Exec(cctx, []string{"/bin/sh", "-c", script}, 10, ""); err != nil || r.GetExitCode() != 0 {
		m.log(g).Warn("guest clock not set after the wake", "event", "guest_resting")
	}
	ccancel()
	pctx, pcancel := context.WithTimeout(ctx, WakeGuestdWait)
	_, perr := sess.Ping(pctx)
	pcancel()
	if perr != nil {
		m.countWakeFailed("guestd")
		m.Warn("guest_wake_failed", "guest "+id+": no answer from the guest after the resume")
		m.log(g).Error("wake failed", "event", "guest_resting", "phase", "guestd", "err", perr.Error())
		return nil, errf(CodeGuestUnresponsive, "guestd did not answer after the resume: %v", perr)
	}
	took := m.d.Now().Sub(start)
	if m.d.Metrics != nil {
		m.d.Metrics.GuestWakeSeconds.Observe(took.Seconds())
	}
	m.log(g).Info("guest woken", "event", "guest_resting", "reason", reason, "wake_ms", took.Milliseconds())
	return &hostdv1.WakeResult{WasResting: true, WakeMs: uint32(took.Milliseconds())}, nil
}

func (m *Manager) countWakeFailed(reason string) {
	if m.d.Metrics != nil {
		m.d.Metrics.GuestWakeFailed.WithLabelValues(reason).Inc()
	}
}

// resumeVM resumes the hypervisor and confirms it against vm.info, the
// shape resumeAfterFailedSnapshot uses: vm.resume of a VM that already
// runs is refused by Cloud Hypervisor, so what counts is the state, not
// the call's answer.
// It sends vm.resume once and then polls vm.info alone, backing off
// from wakeResumePoll. It used to re-send both every 50 ms, which is up
// to a hundred turns and two hundred API calls against a socket that
// accepts ten connections at once (I-920): the loop meant to recover a
// slow wake was the thing most likely to make the guest unreachable for
// good. Re-sending the resume bought nothing anyway -- as the comment
// above says, what counts is the state, not the call's answer.
func (m *Manager) resumeVM(ctx context.Context, g *state.Guest, sock string) error {
	deadline := m.d.Now().Add(WakeResumeWait)
	rctx, cancel := context.WithTimeout(ctx, WakeResumeWait)
	defer cancel()
	last := m.d.CH.Resume(rctx, sock)
	wait := wakeResumePoll
	for {
		info, ierr := m.d.CH.Info(rctx, sock)
		if ierr == nil && ch.VMState(info) == ch.StateRunning {
			return nil
		}
		if ierr != nil {
			last = ierr
			// A full API connection cap is the one answer no amount of
			// asking again can improve, and every ask holds another
			// slot. Stop at once and say so (I-920).
			if isConnCapFull(ierr) {
				return errf(CodeGuestUnresponsive, "the hypervisor's API is out of connections: %v", ierr)
			}
		}
		if !m.d.Now().Before(deadline) {
			if last == nil {
				last = errf(CodeGuestUnresponsive, "the hypervisor did not report Running")
			}
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if wait < maxWakeResumePoll {
			wait *= 2
		}
	}
}

// isConnCapFull is Cloud Hypervisor answering that its API has all the
// connections it accepts. The body is the only place it is said, which
// is why I-912 had to put it into the error in the first place.
func isConnCapFull(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Too many open connections")
}

// wakeIfResting brings a resting guest back before hostd uses it. Every
// caller that needs guestd goes through it: a paused guest keeps its vsock
// session open, so the call would not fail, it would wait out its own
// timeout (30 s, and ten minutes for a Switch).
//
// The caller's own record is cleared too: the wake wrote bbolt from its
// own copy, and a caller that writes its stale one afterwards would put
// the mark back on a guest that runs.
func (m *Manager) wakeIfResting(ctx context.Context, g *state.Guest, reason string) *Error {
	if g.Resting == nil && m.restingMark(g.GuestID) == nil {
		return nil
	}
	if _, err := m.wakeByID(ctx, g.GuestID, reason); err != nil {
		return err
	}
	g.Resting = nil
	return nil
}

// sessionAwake is session() for a caller that needs the guest to answer:
// a resting guest is woken first. The sampler is the one caller that must
// not wake a guest and keeps session().
func (m *Manager) sessionAwake(ctx context.Context, g *state.Guest) (vsockclient.Session, *Error) {
	if err := m.wakeIfResting(ctx, g, WakeReasonCommand); err != nil {
		return nil, err
	}
	return m.session(g.GuestID)
}

// --- operator -------------------------------------------------------

// PauseGuest and WakeGuest are the control socket's `hostd pause|wake`.
// Both run on the guest's own worker, so they serialise with the api's
// commands for that guest exactly as the commands do, and the wake takes
// no op slot.
func (m *Manager) PauseGuest(ctx context.Context, id, reason string) (*hostdv1.PauseResult, error) {
	type out struct {
		res *hostdv1.PauseResult
		err *Error
	}
	done := make(chan out, 1)
	m.enqueue(id, job{internal: func(jctx context.Context) {
		res, err := m.pause(jctx, "", &hostdv1.PauseGuest{GuestId: id, Reason: reason})
		done <- out{res, err}
	}})
	select {
	case o := <-done:
		if o.err != nil {
			return nil, o.err
		}
		return o.res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) WakeGuest(ctx context.Context, id, reason string) (*hostdv1.WakeResult, error) {
	type out struct {
		res *hostdv1.WakeResult
		err *Error
	}
	done := make(chan out, 1)
	m.enqueue(id, job{noSlot: true, internal: func(jctx context.Context) {
		res, err := m.wakeByID(jctx, id, reason)
		done <- out{res, err}
	}})
	select {
	case o := <-done:
		if o.err != nil {
			return nil, o.err
		}
		return o.res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
