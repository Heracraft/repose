// Package resume starts again the machines a host restart stopped
// (DECISIONS I-645). hostd marks each guest that was running or starting
// when its host went down, and its Hello carries the mark; OnHello writes
// one guest_resumes row per (project, boot); the tick decides each row
// once, in its own transaction with the start it enqueues through
// lifecycle.Start, so a machine the api starts by itself passes every
// rule a user's start does. Each user hears once per host restart which
// machines run again and which did not, and why.
//
// It sits above ops, billing and abuse (through lifecycle), and is wired
// in app.go after the engine's own Hello handling.
package resume

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/lifecycle"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/multiplexer"
)

// The rules of DECISIONS I-645.
const (
	// Window is how long after hostd recorded the interruption a machine
	// is still started; past it the owner has likely moved on.
	Window = 6 * time.Hour
	// NoticeAfter is when a user's notice goes out with rows still open.
	NoticeAfter = 10 * time.Minute
	// PerHostPerTick bounds the starts one tick enqueues on one host, so a
	// restart does not boot every guest at once.
	PerHostPerTick = 3
	// TickInterval is the tick's period when nothing kicks it.
	TickInterval = time.Minute
	// ReadyRetries is how many times a start that timed out waiting for
	// the machine is tried again (kanali alone took 13 s to boot).
	ReadyRetries = 1
)

// Outcomes of a guest_resumes row. Pending and Starting are open; the
// rest close it.
const (
	OutcomePending        = "pending"
	OutcomeStarting       = "starting"
	OutcomeStarted        = "started"
	OutcomeAlreadyRunning = "already_running"
	OutcomeUserStopped    = "user_stopped"
	OutcomeDestroyed      = "destroyed"
	OutcomeTempExpired    = "temp_expired"
	OutcomeWindowPassed   = "window_passed"
	OutcomeRefused        = "refused"
	OutcomeFailed         = "failed"
)

// Outcomes is every closed outcome, the label set of
// repose_api_guest_resumes_total.
var Outcomes = []string{OutcomeStarted, OutcomeAlreadyRunning, OutcomeUserStopped, OutcomeDestroyed, OutcomeTempExpired, OutcomeWindowPassed, OutcomeRefused, OutcomeFailed}

// EnvSwitch is the environment variable whose value "off" holds every
// resume: rows stay pending, nothing is started (RUNBOOK "Host
// rebooted"). The tick reads it each time, so the app's restart that a
// changed variable takes is the only step.
const EnvSwitch = "REPOSE_RESUME"

// EventHostRestarted is the project event, notifying nobody, that `repose
// events` shows for each machine the restart stopped.
const EventHostRestarted = "host_restarted"

// NoticeKind is the user notice.
const NoticeKind = "machines_restarted"

// Resumer is the resume tick and its Hello handler.
type Resumer struct {
	pool   *db.Pool
	engine *ops.Engine
	gate   *billing.Gate
	m      *metrics.M
	log    *slog.Logger
	// Now is the clock; Enabled reads the off switch; Start is
	// lifecycle.Start (a test replaces it to fail after enqueueing).
	Now     func() time.Time
	Enabled func() bool
	Start   func(ctx context.Context, tx db.Tx, d lifecycle.Deps, u *store.User, projectID uuid.UUID, o lifecycle.Options) (*lifecycle.Result, error)

	kick    chan struct{}
	driving atomic.Bool
}

// New builds the resumer.
func New(pool *db.Pool, engine *ops.Engine, gate *billing.Gate, m *metrics.M, log *slog.Logger) *Resumer {
	if m != nil {
		for _, o := range Outcomes {
			m.GuestResumesTotal.WithLabelValues(o)
		}
	}
	return &Resumer{pool: pool, engine: engine, gate: gate, m: m, log: log.With("component", "api"), Now: time.Now,
		Enabled: func() bool { return !strings.EqualFold(strings.TrimSpace(os.Getenv(EnvSwitch)), "off") },
		Start:   lifecycle.Start, kick: make(chan struct{}, 1)}
}

// Kick asks for a tick now.
func (r *Resumer) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// OnHello writes a row for each guest the host reports a host restart
// interrupted, at most once per (project, boot). A guest counts only on
// its own host (I-447). An error refuses the Hello: hostmgr closes the
// stream and the host sends it again on reconnect, so a mark is never
// dropped on a database hiccup.
func (r *Resumer) OnHello(ctx context.Context, hostID uuid.UUID, h *hostdv1.Hello) error {
	lost := hostmgr.LastHeardBefore(ctx)
	var bootAt *time.Time
	if h.HostBootAt > 0 {
		t := time.Unix(h.HostBootAt, 0).UTC()
		bootAt = &t
	}
	inserted := 0
	for _, g := range h.Guests {
		if g.InterruptedBootId == "" || g.State == "running" {
			continue
		}
		gid, err := uuid.Parse(g.GuestId)
		if err != nil {
			continue
		}
		p, err := store.GetProjectOnHost(ctx, r.pool, gid, hostID)
		if errors.Is(err, store.ErrOtherHost) || errors.Is(err, db.ErrNotFound) {
			continue // engine.OnHello logs and counts both
		}
		if err != nil {
			return fmt.Errorf("resume: project of guest: %w", err)
		}
		if p.DestroyedAt != nil {
			continue
		}
		at := time.Unix(g.InterruptedAt, 0).UTC()
		if g.InterruptedAt == 0 {
			at = r.Now().UTC()
		}
		err = db.InTx(ctx, r.pool, func(tx db.Tx) error {
			tag, err := tx.Exec(ctx, `insert into guest_resumes (id, project_id, user_id, host_id, guest_id, boot_id, host_boot_at, interrupted_at, host_lost_at, hello_state)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) on conflict (project_id, boot_id) do nothing`,
				store.NewID(), p.ID, p.UserID, hostID, gid, g.InterruptedBootId, bootAt, at, lost, g.State)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			inserted++
			r.log.Info("machine stopped by a host restart", "event", "guest_resume", "project_id", p.ID.String(), "host_id", hostID.String(), "outcome", OutcomePending)
			return events.InsertProjectQuiet(ctx, tx, p.ID, r.Now(), EventHostRestarted, restartedSummary(bootAt, at))
		})
		if err != nil {
			return fmt.Errorf("resume: record interrupted guest: %w", err)
		}
	}
	if inserted > 0 {
		r.Kick()
	}
	return nil
}

func restartedSummary(bootAt *time.Time, at time.Time) string {
	t := at
	if bootAt != nil {
		t = *bootAt
	}
	return "the server running this machine restarted; it was back at " + t.UTC().Format("15:04") + " UTC"
}

// Run ticks until ctx ends, on the replica that holds LockResume.
func (r *Resumer) Run(ctx context.Context) {
	for {
		release, ok, err := db.TryLock(ctx, r.pool, db.LockResume)
		if err != nil && ctx.Err() == nil {
			r.log.Error("resume lock", "event", "guest_resume", "err", err.Error())
		}
		if ok {
			r.driving.Store(true)
			t := time.NewTicker(TickInterval)
			for {
				if err := r.Tick(ctx); err != nil && ctx.Err() == nil {
					r.log.Error("resume tick", "event", "guest_resume", "err", err.Error())
				}
				select {
				case <-ctx.Done():
					t.Stop()
					r.driving.Store(false)
					release()
					return
				case <-t.C:
				case <-r.kick:
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

type row struct {
	id            uuid.UUID
	projectID     uuid.UUID
	userID        uuid.UUID
	hostID        uuid.UUID
	bootID        string
	interruptedAt time.Time
	hostLostAt    *time.Time
	helloState    string
	outcome       string
	opID          *uuid.UUID
	attempts      int
	timeouts      int
	createdAt     time.Time
}

const rowCols = "id, project_id, user_id, host_id, boot_id, interrupted_at, host_lost_at, hello_state, outcome, op_id, attempts, timeouts, created_at"

func scanRow(sc interface{ Scan(...any) error }) (row, error) {
	var w row
	err := sc.Scan(&w.id, &w.projectID, &w.userID, &w.hostID, &w.bootID, &w.interruptedAt, &w.hostLostAt, &w.helloState, &w.outcome, &w.opID, &w.attempts, &w.timeouts, &w.createdAt)
	return w, err
}

// Tick decides every open row, oldest first, then sends the notices that
// are due.
func (r *Resumer) Tick(ctx context.Context) error {
	rs, err := r.pool.Query(ctx, "select "+rowCols+" from guest_resumes where decided_at is null order by created_at, id")
	if err != nil {
		return err
	}
	var open []row
	for rs.Next() {
		w, err := scanRow(rs)
		if err != nil {
			rs.Close()
			return err
		}
		open = append(open, w)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return err
	}
	started := map[uuid.UUID]int{}
	enqueued := false
	for _, w := range open {
		did, err := r.decide(ctx, w, started)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			r.log.Error("resume row not decided; the next tick tries again", "event", "guest_resume", "project_id", w.projectID.String(), "err", err.Error())
			continue
		}
		enqueued = enqueued || did
	}
	if enqueued {
		r.engine.Kick()
	}
	if err := r.notices(ctx); err != nil {
		return err
	}
	return r.gauge(ctx)
}

func (r *Resumer) gauge(ctx context.Context) error {
	if r.m == nil {
		return nil
	}
	var n int
	if err := r.pool.QueryRow(ctx, "select count(*) from guest_resumes where decided_at is null").Scan(&n); err != nil {
		return err
	}
	r.m.GuestResumesPending.Set(float64(n))
	return nil
}

// errLeave is a row left as it is this tick.
var errLeave = errors.New("left pending")

// decide settles one row in its own transaction and reports whether it
// enqueued a start.
func (r *Resumer) decide(ctx context.Context, w row, started map[uuid.UUID]int) (bool, error) {
	enqueued := false
	var closed, reason string
	err := db.InTx(ctx, r.pool, func(tx db.Tx) error {
		// The row, then the project: lifecycle.Start and a user's start
		// and rm (Enqueue) take the project's lock too, so the second of
		// them sees the first's op.
		cur, err := scanRow(tx.QueryRow(ctx, "select "+rowCols+" from guest_resumes where id = $1 and decided_at is null for update", w.id))
		if db.IsNoRows(err) {
			return errLeave
		}
		if err != nil {
			return err
		}
		w = cur
		p, err := store.GetProjectForUpdate(ctx, tx, w.projectID)
		if err != nil {
			return err
		}
		if w.outcome == OutcomeStarting {
			closed, reason, err = r.followStart(ctx, tx, w)
			if err != nil || closed == "" {
				return err
			}
			return r.close(ctx, tx, w, closed, reason)
		}
		closed, reason, err = r.settled(ctx, tx, w, p)
		if err != nil {
			return err
		}
		if closed != "" {
			return r.close(ctx, tx, w, closed, reason)
		}
		// Waiting, not counted: what clears by itself.
		h, err := store.GetHost(ctx, tx, w.hostID)
		if err != nil {
			return err
		}
		if h.State != "ready" && h.State != "draining" && h.State != "retired" && h.State != "lost" {
			return errLeave
		}
		if hostPoolUnavailable(ctx, tx, w.hostID) {
			return errLeave
		}
		if !r.Enabled() {
			return errLeave
		}
		if started[w.hostID] >= PerHostPerTick {
			return errLeave
		}
		u, err := store.GetUser(ctx, tx, w.userID)
		if err != nil {
			return err
		}
		// hostd's word: the guest is down (Hello), whatever the project
		// row says, since Hello and the guest's own events leave a project
		// with an open op alone (DECISIONS I-645).
		if p.State != "stopped" && p.State != "error" {
			if err := store.SetProjectState(ctx, tx, p.ID, downState(w.helloState)); err != nil {
				return err
			}
		}
		res, err := r.Start(ctx, tx, lifecycle.Deps{Pool: r.pool, Engine: r.engine, Gate: r.gate}, u, p.ID, lifecycle.Options{Resume: true})
		var ref *lifecycle.Refusal
		switch {
		case errors.As(err, &ref):
			if !ref.Terminal() {
				return errLeave
			}
			closed, reason = OutcomeRefused, ref.Message
			return r.close(ctx, tx, w, closed, reason)
		case errors.Is(err, ops.ErrOpInProgress):
			return errLeave
		case err != nil:
			return err
		}
		if _, err := tx.Exec(ctx, "update guest_resumes set outcome = $2, op_id = $3, attempts = attempts + 1 where id = $1", w.id, OutcomeStarting, res.OpID); err != nil {
			return err
		}
		started[w.hostID]++
		enqueued = true
		r.log.Info("starting a machine a host restart stopped", "event", "guest_resume", "project_id", p.ID.String(), "host_id", w.hostID.String(), "op_id", res.OpID.String(), "outcome", OutcomeStarting)
		return nil
	})
	if errors.Is(err, errLeave) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if closed != "" {
		if r.m != nil {
			r.m.GuestResumesTotal.WithLabelValues(closed).Inc()
		}
		r.log.Info("resume decided", "event", "guest_resume", "project_id", w.projectID.String(), "host_id", w.hostID.String(), "outcome", closed)
	}
	return enqueued, nil
}

func downState(hello string) string {
	if hello == "error" {
		return "error"
	}
	return "stopped"
}

// hostPoolUnavailable reads hosts.pool_unavailable (DECISIONS I-644,
// migration 0021): a start would only fail with volume_unavailable.
func hostPoolUnavailable(ctx context.Context, tx db.Tx, hostID uuid.UUID) bool {
	var v bool
	err := tx.QueryRow(ctx, `select pool_unavailable from hosts where id = $1`, hostID).Scan(&v)
	return err == nil && v
}

// settled closes a pending row whose answer no longer depends on
// starting: the owner acted, the machine runs, or the rules say no.
func (r *Resumer) settled(ctx context.Context, tx db.Tx, w row, p *store.Project) (string, string, error) {
	if p.DestroyedAt != nil || p.State == "destroyed" || p.State == "destroying" {
		return OutcomeDestroyed, "", nil
	}
	// Any stop, destroy, restore or resize asked for after the host went
	// quiet, by the owner or the platform, whatever its result: the owner
	// (or billing, abuse or the temp reaper) wanted it down or changed.
	// hostd reports the interruption in whole seconds (Hello's
	// interrupted_at), so an op from earlier in that same second would
	// look "after". Take the end of the second; the api's own host-lost
	// time is exact and wins when it exists.
	since := w.interruptedAt.Truncate(time.Second).Add(time.Second - time.Nanosecond)
	if w.hostLostAt != nil {
		since = *w.hostLostAt
	}
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from ops where project_id = $1 and created_at > $2 and kind in ('stop', 'destroy', 'restore', 'resize')`, p.ID, since).Scan(&n); err != nil {
		return "", "", err
	}
	if n > 0 {
		return OutcomeUserStopped, "", nil
	}
	var newest string
	err := tx.QueryRow(ctx, `select kind from ops where project_id = $1 order by created_at desc, id desc limit 1`, p.ID).Scan(&newest)
	if err != nil && !db.IsNoRows(err) {
		return "", "", err
	}
	if newest == ops.KindDestroy {
		return OutcomeUserStopped, "", nil
	}
	var open int
	if err := tx.QueryRow(ctx, `select count(*) from ops where project_id = $1 and state in ('pending', 'running')`, p.ID).Scan(&open); err != nil {
		return "", "", err
	}
	if open > 0 {
		// A base-sweep build or apply, a personal build, an exec or a
		// snapshot: decided again when it ends.
		return "", "", errLeave
	}
	// Its owner started it already: a start done since the Hello, also
	// one asked for before the host went down that finished after.
	if err := tx.QueryRow(ctx, ownerStarted, p.ID, w.createdAt).Scan(&n); err != nil {
		return "", "", err
	}
	if n > 0 {
		return OutcomeAlreadyRunning, "", nil
	}
	now := r.Now()
	if now.Sub(w.interruptedAt) > Window {
		return OutcomeWindowPassed, "", nil
	}
	if p.ExpiresAt != nil && !now.Before(*p.ExpiresAt) {
		return OutcomeTempExpired, "", nil
	}
	return "", "", nil
}

// ownerStarted counts the starts, not the resume's, that finished after
// a row was written ($2): the machine runs because its owner started it.
const ownerStarted = `select count(*) from ops where project_id = $1 and finished_at > $2 and kind in ('start', 'create') and state = 'done' and not coalesce((params->>'resume')::boolean, false)`

// followStart reads the start a row enqueued: still running leaves the
// row; done closes it started; a failure that clears by itself, or a
// first timeout, puts it back to pending for the next tick.
func (r *Resumer) followStart(ctx context.Context, tx db.Tx, w row) (string, string, error) {
	if w.opID == nil {
		_, err := tx.Exec(ctx, "update guest_resumes set outcome = $2 where id = $1", w.id, OutcomePending)
		return "", "", err
	}
	op, err := store.GetOp(ctx, tx, *w.opID)
	if err != nil {
		return "", "", err
	}
	switch op.State {
	case "done":
		return OutcomeStarted, "", nil
	case "error":
	default:
		return "", "", errLeave
	}
	code, msg := "", ""
	if op.Error != nil {
		code, _ = op.Error["code"].(string)
		msg, _ = op.Error["message"].(string)
	}
	retry, timeout := false, 0
	switch code {
	case "insufficient_capacity", "capacity", "host_unreachable", "volume_unavailable":
		retry = true // waits, not counted
	case "guest_unresponsive", "timeout":
		// Only Ready timeouts use up the one more try: a start that first
		// found the host full and then booted slowly still gets it.
		retry, timeout = w.timeouts < ReadyRetries, 1
	}
	if retry && r.Now().Sub(w.interruptedAt) <= Window {
		_, err := tx.Exec(ctx, "update guest_resumes set outcome = $2, op_id = null, timeouts = timeouts + $3 where id = $1", w.id, OutcomePending, timeout)
		return "", "", err
	}
	if msg == "" {
		msg = "the start failed"
	}
	return OutcomeFailed, msg, nil
}

func (r *Resumer) close(ctx context.Context, tx db.Tx, w row, outcome, reason string) error {
	var rp *string
	if reason != "" {
		rp = &reason
	}
	_, err := tx.Exec(ctx, "update guest_resumes set outcome = $2, reason = $3, decided_at = $4 where id = $1", w.id, outcome, rp, r.Now())
	return err
}

// OnOpFinished keeps a project's state true to its guest when an op that
// was open at the Hello ends: Hello and the guest's events leave such a
// project alone, so a build or apply that ran across the restart left it
// "running" while hostd has it stopped. It then kicks the tick, which
// decides the row the op held up.
func (r *Resumer) OnOpFinished(ctx context.Context, op *store.Op) {
	if op.ProjectID == nil {
		return
	}
	var helloState string
	err := r.pool.QueryRow(ctx, `select hello_state from guest_resumes where project_id = $1 and decided_at is null and outcome = 'pending' order by created_at desc limit 1`, *op.ProjectID).Scan(&helloState)
	if err != nil {
		return
	}
	switch op.Kind {
	case ops.KindStart, ops.KindCreate, ops.KindRestore, ops.KindStop, ops.KindDestroy, ops.KindResize:
		// These set the state themselves.
	default:
		// Not when its owner started it since the Hello: it runs.
		if _, err := r.pool.Exec(ctx, `update projects set state = $2 where id = $1 and state in ('running', 'starting')
			and not exists (select 1 from ops where project_id = $1 and state in ('pending', 'running'))
			and not exists (select 1 from guest_resumes g join ops o on o.project_id = g.project_id
				where g.project_id = $1 and g.decided_at is null and o.finished_at > g.created_at
				and o.kind in ('start', 'create') and o.state = 'done' and not coalesce((o.params->>'resume')::boolean, false))`,
			*op.ProjectID, downState(helloState)); err != nil {
			r.log.Warn("project state not set from the host's Hello", "event", "guest_resume", "project_id", op.ProjectID.String(), "err", err.Error())
		}
	}
	r.Kick()
}

// --- the notice -----------------------------------------------------------

// Line is one machine of a notice: its slug, the row's outcome and
// reason, and its multiplexer (a herdr machine names no agent turn).
type Line struct {
	Slug, Outcome, Reason, Mux string
}

// notices sends each (user, host, boot) its one machines_restarted
// notice: when every row of it is closed, or NoticeAfter after the first
// row, listing what is still open as waiting.
func (r *Resumer) notices(ctx context.Context) error {
	rs, err := r.pool.Query(ctx, `select g.user_id, g.host_id, g.boot_id, min(g.created_at), count(*) filter (where g.decided_at is null)
		from guest_resumes g
		where not exists (select 1 from resume_notices n where n.user_id = g.user_id and n.host_id = g.host_id and n.boot_id = g.boot_id)
		group by 1, 2, 3`)
	if err != nil {
		return err
	}
	type group struct {
		user, host uuid.UUID
		boot       string
		first      time.Time
		open       int
	}
	var due []group
	for rs.Next() {
		var g group
		if err := rs.Scan(&g.user, &g.host, &g.boot, &g.first, &g.open); err != nil {
			rs.Close()
			return err
		}
		if g.open == 0 || r.Now().Sub(g.first) >= NoticeAfter {
			due = append(due, g)
		}
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return err
	}
	for _, g := range due {
		if err := r.notify(ctx, g.user, g.host, g.boot); err != nil {
			r.log.Error("resume notice not sent; the next tick tries again", "event", "guest_resume", "user_id", g.user.String(), "err", err.Error())
		}
	}
	return nil
}

func (r *Resumer) notify(ctx context.Context, userID, hostID uuid.UUID, bootID string) error {
	return db.InTx(ctx, r.pool, func(tx db.Tx) error {
		tag, err := tx.Exec(ctx, "insert into resume_notices (user_id, host_id, boot_id, sent_at) values ($1, $2, $3, $4) on conflict do nothing", userID, hostID, bootID, r.Now())
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		rs, err := tx.Query(ctx, `select p.slug, g.outcome, coalesce(g.reason, ''), p.multiplexer, g.host_boot_at, g.created_at
			from guest_resumes g join projects p on p.id = g.project_id
			where g.user_id = $1 and g.host_id = $2 and g.boot_id = $3 order by p.slug`, userID, hostID, bootID)
		if err != nil {
			return err
		}
		var lines []Line
		var bootAt, firstRow *time.Time
		for rs.Next() {
			var l Line
			var b *time.Time
			var created time.Time
			if err := rs.Scan(&l.Slug, &l.Outcome, &l.Reason, &l.Mux, &b, &created); err != nil {
				rs.Close()
				return err
			}
			if b != nil {
				bootAt = b
			}
			if firstRow == nil || created.Before(*firstRow) {
				firstRow = &created
			}
			lines = append(lines, l)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return err
		}
		// The host's boot time is when it was back; a hostd older than
		// I-645 sends none, and the first row (its Hello) stands in.
		back := time.Time{}
		if bootAt != nil {
			back = *bootAt
		} else if firstRow != nil {
			back = *firstRow
		}
		text := NoticeText(back, lines...)
		if text == "" {
			return nil // every machine was stopped or removed by its owner
		}
		id, err := events.InsertUserNotice(ctx, tx, userID, r.Now(), NoticeKind, text)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "update resume_notices set event_id = $4 where user_id = $1 and host_id = $2 and boot_id = $3", userID, hostID, bootID, id)
		if err == nil {
			r.log.Info("resume notice queued", "event", "guest_resume", "user_id", userID.String(), "host_id", hostID.String(), "machines", len(lines))
		}
		return err
	})
}

// NoticeText is the machines_restarted text: one sentence for the
// restart, then a line per machine that ran again or did not, in slug
// order. A machine its owner stopped or removed meanwhile gets no line;
// with none left, the text is empty and no notice is sent.
func NoticeText(back time.Time, lines ...Line) string {
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Slug < lines[j].Slug })
	var out []string
	for _, l := range lines {
		switch l.Outcome {
		case OutcomeStarted, OutcomeAlreadyRunning:
			if l.Mux == multiplexer.Herdr || l.Outcome == OutcomeAlreadyRunning {
				out = append(out, l.Slug+": running again.")
			} else {
				out = append(out, l.Slug+": running again; its last agent turn was interrupted.")
			}
		case OutcomePending, OutcomeStarting:
			out = append(out, l.Slug+": waiting to start. `repose start "+l.Slug+"` starts it now.")
		case OutcomeWindowPassed:
			out = append(out, l.Slug+": not started: the server was back more than 6 hours after it stopped. `repose start "+l.Slug+"` starts it.")
		case OutcomeTempExpired:
			out = append(out, l.Slug+": not started: it was temporary and its time ran out.")
		case OutcomeRefused, OutcomeFailed:
			reason := strings.TrimSuffix(strings.TrimSpace(l.Reason), ".")
			if reason == "" {
				reason = "the start failed"
			}
			out = append(out, l.Slug+": not started: "+reason+". `repose start "+l.Slug+"` starts it.")
		}
	}
	if len(out) == 0 {
		return ""
	}
	head := "The server running your machines restarted"
	if !back.IsZero() {
		head += "; it was back at " + back.UTC().Format("15:04") + " UTC"
	}
	return head + ".\n" + strings.Join(out, "\n")
}
