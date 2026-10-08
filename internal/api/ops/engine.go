// Package ops drives every long operation (05-control-plane-api.md §5.3,
// §5.4): an op is a row with a kind and a list of phases; each phase is
// one hostd command whose command_id is stored before it is sent, whose
// result lands in the row when it arrives, and which is rebuilt and
// re-sent with the same command_id after an api restart or on the host's
// next Hello. The command builders here are the only callers of
// secrets.DecryptForGuest.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/heracraft/repose/internal/api/buildlog"
	"github.com/heracraft/repose/internal/api/ca"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Sender delivers commands to hosts (hostmgr).
type Sender interface {
	Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error
	Connected(hostID uuid.UUID) bool
}

// EventSink records a platform-originated event (13-notifications.md §2,
// "billing_stopped, base_updated, snapshot_failed, host_moved"). It is nil
// in the admin CLI's ad-hoc engine, where no user should be paged.
type EventSink interface {
	Platform(ctx context.Context, projectID uuid.UUID, kind, summary string) error
}

// Limits are the Build limits (DECISIONS R5-4).
type Limits struct {
	EvalS        uint32
	BuildS       uint32
	Cores        uint32
	ClosureBytes uint64
}

// DefaultLimits are the documented caps.
var DefaultLimits = Limits{EvalS: 60, BuildS: 1800, Cores: 8, ClosureBytes: 20 << 30}

// Config tunes the engine.
type Config struct {
	// BaseRef is the nix/ git revision used when no base_versions row
	// exists yet (dev); `repose-admin base publish` supersedes it.
	BaseRef string
	Limits  Limits
	// HostUnreachableGrace is how long an op waits for a silent host
	// before failing with host unreachable.
	HostUnreachableGrace time.Duration
	// KeyVaultRetry is how long a guest start retries a failed Key Vault.
	KeyVaultRetry time.Duration
	Lang          string
	// PlacementWait is how long a placement that found no host is tried
	// again while a guest being stopped would make room (default 3 min:
	// a stop's 60 s timeout plus heartbeats, I-408).
	PlacementWait time.Duration
	// TickInterval is the loop's poll when nothing wakes it (default
	// 500 ms). Enqueues and results in another process wake it through
	// Postgres NOTIFY instead (DECISIONS I-163).
	TickInterval time.Duration
}

// notifyChannel is the Postgres channel a Kick in a process that is not
// driving the ops raises, so the process holding the ops lock wakes at
// once instead of at its next tick (I-163).
const notifyChannel = "repose_ops"

// Engine is the op driver.
type Engine struct {
	pool   *db.Pool
	send   Sender
	ca     *ca.CA
	sec    *secrets.Store
	logs   *buildlog.Store
	events EventSink
	m      *metrics.M
	log    *slog.Logger
	cfg    Config

	kick    chan struct{}
	driving atomic.Bool
	now     func() time.Time
	mu      sync.Mutex
	waiters map[uuid.UUID][]chan struct{}
	// onFinished is called after an op reaches done or error (events). Set
	// through SetOnFinished: callers install it after Run has started (the
	// base-bump job in app.go and the tests), so the engine goroutine reads
	// it concurrently.
	onFinishedMu sync.RWMutex
	onFinished   func(ctx context.Context, op *store.Op)
	// placeWaiting holds the ops whose placement wait was logged (I-408).
	placeWaiting sync.Map
}

// New builds an engine. events may be nil (the admin CLI's ad-hoc engine).
func New(pool *db.Pool, send Sender, c *ca.CA, sec *secrets.Store, logs *buildlog.Store, events EventSink, m *metrics.M, log *slog.Logger, cfg Config) *Engine {
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits
	}
	if cfg.HostUnreachableGrace == 0 {
		cfg.HostUnreachableGrace = 10 * time.Minute
	}
	if cfg.KeyVaultRetry == 0 {
		cfg.KeyVaultRetry = 30 * time.Minute
	}
	if cfg.Lang == "" {
		cfg.Lang = "C.UTF-8"
	}
	if cfg.TickInterval == 0 {
		cfg.TickInterval = 500 * time.Millisecond
	}
	if cfg.PlacementWait == 0 {
		cfg.PlacementWait = 3 * time.Minute
	}
	return &Engine{pool: pool, send: send, ca: c, sec: sec, logs: logs, events: events, m: m, log: log.With("component", "api"), cfg: cfg,
		kick: make(chan struct{}, 1), now: time.Now, waiters: map[uuid.UUID][]chan struct{}{}}
}

// NewOp describes an op to enqueue.
type NewOp struct {
	Kind       string
	ProjectID  *uuid.UUID
	HostID     *uuid.UUID
	Params     map[string]any
	Phases     []string
	RevisionID *uuid.UUID
	SnapshotID *uuid.UUID
	AuditID    *uuid.UUID
}

// ErrOpInProgress is returned when a project already has an open op.
var ErrOpInProgress = errors.New("an operation is in progress")

// Enqueue inserts a pending op inside q. A project with an open op refuses
// a second one unless allowQueue is set (secrets pushes queue behind).
func (e *Engine) Enqueue(ctx context.Context, q store.Querier, n NewOp, allowQueue bool) (uuid.UUID, error) {
	if n.ProjectID != nil && !allowQueue {
		var open int
		if err := q.QueryRow(ctx, "select count(*) from ops where project_id = $1 and state in ('pending','running')", *n.ProjectID).Scan(&open); err != nil {
			return uuid.Nil, err
		}
		if open > 0 {
			return uuid.Nil, ErrOpInProgress
		}
	}
	params := n.Params
	if params == nil {
		params = map[string]any{}
	}
	ph := make([]any, len(n.Phases))
	for i, p := range n.Phases {
		ph[i] = p
	}
	params["phases"] = ph
	id := store.NewID()
	// clock_timestamp, not now(): two ops queued in one transaction (a
	// start and the personal build that follows it, I-490) get distinct,
	// ordered times, and the loop runs a project's ops in that order.
	_, err := q.Exec(ctx, `insert into ops (id, project_id, host_id, kind, state, params, revision_id, snapshot_id, audit_id, created_at) values ($1, $2, $3, $4, 'pending', $5, $6, $7, $8, clock_timestamp())`,
		id, n.ProjectID, n.HostID, n.Kind, params, n.RevisionID, n.SnapshotID, n.AuditID)
	if err != nil {
		return uuid.Nil, err
	}
	e.m.OpsOpen.WithLabelValues(n.Kind).Inc()
	return id, nil
}

// Kick wakes the loop. The api and api-grpc each have an engine and only
// the one holding the ops lock drives; a Kick in the other (an enqueue in
// the api while api-grpc drives, a result in api-grpc while the api
// drives) reaches the driver through NOTIFY.
func (e *Engine) Kick() {
	if !e.driving.Load() {
		go e.notifyDriver()
	}
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Engine) notifyDriver() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := e.pool.Exec(ctx, "select pg_notify($1, '')", notifyChannel); err != nil {
		e.log.Warn("ops notify failed; the driver's tick covers it", "event", "ops_notify_fail", "err", err.Error())
	}
}

// listen turns NOTIFYs on notifyChannel into kicks until ctx ends. A lost
// connection only costs latency: the ticker still drives every op.
func (e *Engine) listen(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := e.pool.Acquire(ctx)
		if err != nil {
			return
		}
		if _, err := conn.Exec(ctx, "listen "+notifyChannel); err == nil {
			for {
				if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				select {
				case e.kick <- struct{}{}:
				default:
				}
			}
		}
		// A connection that failed mid-wait is not returned to the pool
		// still listening.
		_ = conn.Conn().Close(context.Background())
		conn.Release()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

// Run drives ops until ctx ends. Only one replica drives at a time
// (advisory lock); the others poll for the lock.
func (e *Engine) Run(ctx context.Context) {
	for {
		release, ok, err := db.TryLock(ctx, e.pool, db.LockOps)
		if err != nil {
			e.log.Error("ops lock", "event", "ops_lock_fail", "err", err.Error())
		}
		if ok {
			e.loop(ctx)
			release()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (e *Engine) loop(ctx context.Context) {
	e.driving.Store(true)
	defer e.driving.Store(false)
	lctx, stopListen := context.WithCancel(ctx)
	defer stopListen()
	go e.listen(lctx)
	t := time.NewTicker(e.cfg.TickInterval)
	defer t.Stop()
	for {
		e.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.kick:
		}
	}
}

// Tick advances every op that has work: pending, a result waiting, or a
// phase not yet sent; then applies timeouts. Exposed for tests.
func (e *Engine) Tick(ctx context.Context) {
	rows, err := e.pool.Query(ctx, `select distinct on (coalesce(project_id, id)) id from ops
		where state in ('pending','running') order by coalesce(project_id, id), created_at, id`)
	if err != nil {
		e.log.Error("ops query", "event", "ops_query_fail", "err", err.Error())
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		op, err := store.GetOp(ctx, e.pool, id)
		if err != nil {
			continue
		}
		if op.State == "running" && op.CommandID != nil && op.CommandResult == nil {
			e.checkTimeout(ctx, op)
			continue
		}
		e.advance(ctx, op)
	}
}

func phaseTimeout(phase string) time.Duration {
	switch phase {
	case "build":
		return 40 * time.Minute
	case "restore":
		return 60 * time.Minute
	case "stop_guest", "snapshot":
		return 30 * time.Minute
	default:
		return 10 * time.Minute
	}
}

func phases(op *store.Op) []string {
	raw, _ := op.Params["phases"].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func currentPhase(op *store.Op) string {
	ph := phases(op)
	if op.Step < len(ph) {
		return ph[op.Step]
	}
	return ""
}

func (e *Engine) checkTimeout(ctx context.Context, op *store.Op) {
	if op.SentAt == nil {
		return
	}
	age := e.now().Sub(*op.SentAt)
	if op.HostID != nil {
		h, err := store.GetHost(ctx, e.pool, *op.HostID)
		if err == nil && h.State == "unreachable" && age > e.cfg.HostUnreachableGrace {
			e.fail(ctx, op, "host_unreachable", "host unreachable")
			return
		}
	}
	if age > phaseTimeout(currentPhase(op)) {
		e.fail(ctx, op, "timeout", fmt.Sprintf("%s did not complete in %s", currentPhase(op), phaseTimeout(currentPhase(op))))
	}
}

// advance processes a waiting result and sends the next phase.
func (e *Engine) advance(ctx context.Context, op *store.Op) {
	log := e.log.With("op_id", op.ID.String(), "kind", op.Kind)
	if op.ProjectID != nil {
		log = log.With("project_id", op.ProjectID.String())
	}
	if op.State == "pending" {
		if _, err := e.pool.Exec(ctx, "update ops set state = 'running', started_at = now() where id = $1 and state = 'pending'", op.ID); err != nil {
			log.Error("op start", "event", "op_fail", "err", err.Error())
			return
		}
		op.State = "running"
	}
	if op.CommandResult != nil {
		res, err := decodeResult(op.CommandResult)
		if err != nil {
			e.fail(ctx, op, "internal", "malformed command result")
			return
		}
		if op.CommandID != nil {
			e.logs.Unbind(op.CommandID.String())
		}
		e.m.CommandsTotal.WithLabelValues(currentPhase(op), resultLabel(res)).Inc()
		if !res.Ok && res.Error != nil && res.Error.Code == "not_found" && op.Kind == KindRestore && currentPhase(op) == PhaseDestroyGuest {
			// The guest a restore replaces is already gone (a restore
			// that failed before hostd recorded its guest): what the
			// phase was for is done (DECISIONS I-461).
			res = &hostdv1.Result{CommandId: res.CommandId, Ok: true}
		}
		if !res.Ok {
			code, msg, line, pline := "internal", "command failed", 0, 0
			if res.Error != nil {
				code, msg, line, pline = res.Error.Code, res.Error.Message, int(res.Error.FragmentLine), int(res.Error.PersonalLine)
			}
			// A boot that never reached Ready carries the end of the
			// guest's console, which `repose logs --kind console` shows
			// its owner (I-592); it is never logged.
			if tail := res.Error.GetConsoleTail(); tail != "" {
				if err := e.setResult(ctx, op, map[string]any{"console": tail}); err != nil {
					log.Warn("console tail not stored", "event", "op_console", "err", err.Error())
				}
			}
			if e.recoverFrom(ctx, op, code) {
				return
			}
			if e.skipFailedApply(ctx, op, code, msg) {
				return
			}
			e.failWithLines(ctx, op, code, msg, line, pline)
			return
		}
		ph := currentPhase(op)
		if err := e.onResult(ctx, op, ph, res); err != nil {
			log.Error("result handling failed", "event", "op_fail", "phase", ph, "err", err.Error())
			e.fail(ctx, op, "internal", "result handling failed: "+err.Error())
			return
		}
		op.Step++
		op.CommandID, op.CommandResult, op.SentAt = nil, nil, nil
		if _, err := e.pool.Exec(ctx, "update ops set step = $2, command_id = null, command_result = null, sent_at = null where id = $1", op.ID, op.Step); err != nil {
			log.Error("op step", "event", "op_fail", "err", err.Error())
			return
		}
	}
	for {
		ph := currentPhase(op)
		if ph == "" {
			e.finish(ctx, op)
			return
		}
		cmd, hostID, skip, err := e.buildCommand(ctx, op, ph)
		if err != nil {
			var oe *opError
			if errors.As(err, &oe) {
				e.failWithLine(ctx, op, oe.code, oe.msg, oe.line)
				return
			}
			if errors.Is(err, errPlacementWait) {
				return // next tick places again
			}
			if errors.Is(err, secrets.ErrKeyServiceUnavailable) {
				e.m.KeyVaultErrorsTotal.Inc()
				if op.StartedAt != nil && e.now().Sub(*op.StartedAt) > e.cfg.KeyVaultRetry {
					e.fail(ctx, op, "internal", "key service unavailable")
					return
				}
				log.Warn("key service unavailable; retrying", "event", "op_retry", "phase", ph)
				return // next tick retries
			}
			log.Error("command build failed", "event", "op_fail", "phase", ph, "err", err.Error())
			e.fail(ctx, op, "internal", err.Error())
			return
		}
		if skip {
			op.Step++
			if _, err := e.pool.Exec(ctx, "update ops set step = $2 where id = $1", op.ID, op.Step); err != nil {
				return
			}
			continue
		}
		if op.CommandID == nil {
			id := uuid.MustParse(cmd.CommandId)
			op.CommandID = &id
		}
		cmd.CommandId = op.CommandID.String()
		op.HostID = &hostID
		now := e.now()
		op.SentAt = &now
		if _, err := e.pool.Exec(ctx, "update ops set command_id = $2, host_id = $3, sent_at = $4, params = $5 where id = $1", op.ID, *op.CommandID, hostID, now, op.Params); err != nil {
			log.Error("op send record", "event", "op_fail", "err", err.Error())
			return
		}
		if ph == "build" {
			e.logs.Bind(cmd.CommandId, op.ID)
		}
		if ph == PhaseApplyConfig && op.Kind == KindBuild {
			// The build's log goes on into its apply, so a client
			// streaming it learns the phase changed (DECISIONS I-320).
			if err := e.logs.Note(ctx, op.ID, ApplyLogLine); err != nil {
				log.Warn("build log note failed", "event", "buildlog_note_fail", "err", err.Error())
			}
		}
		if err := e.send.Send(ctx, hostID, cmd); err != nil {
			log.Warn("host not connected; command queued for the next Hello", "event", "command_queued", "host_id", hostID.String(), "phase", ph)
		}
		return
	}
}

func resultLabel(r *hostdv1.Result) string {
	if r.Ok {
		return "ok"
	}
	if r.Error != nil && r.Error.Code != "" {
		return r.Error.Code
	}
	return "error"
}

func decodeResult(m map[string]any) (*hostdv1.Result, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	r := &hostdv1.Result{}
	if err := protojson.Unmarshal(b, r); err != nil {
		return nil, err
	}
	return r, nil
}

// OnResult stores a host's result on its op and wakes the loop. A result
// for an unknown or already finished command is ignored (duplicate).
func (e *Engine) OnResult(ctx context.Context, hostID uuid.UUID, r *hostdv1.Result) {
	id, err := uuid.Parse(r.CommandId)
	if err != nil {
		return
	}
	b, err := protojson.Marshal(r)
	if err != nil {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return
	}
	tag, err := e.pool.Exec(ctx, "update ops set command_result = $2 where command_id = $1 and host_id = $3 and state = 'running' and command_result is null", id, m, hostID)
	if err != nil {
		e.log.Error("result store", "event", "command_result", "command_id", r.CommandId, "err", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		e.log.Info("duplicate or unknown result ignored", "event", "command_result", "command_id", r.CommandId, "host_id", hostID.String())
		return
	}
	e.log.Info("command result", "event", "command_result", "command_id", r.CommandId, "host_id", hostID.String(), "result", resultLabel(r))
	e.Kick()
}

// OnHello reconciles guest states from the host's view and re-sends every
// unfinished command on that host with its stored command_id.
func (e *Engine) OnHello(ctx context.Context, hostID uuid.UUID, h *hostdv1.Hello) {
	known := map[uuid.UUID]bool{}
	for _, g := range h.Guests {
		gid, err := uuid.Parse(g.GuestId)
		if err != nil {
			continue
		}
		known[gid] = true
		// A host's word counts only for its own guests (I-447).
		p, err := store.GetProjectOnHost(ctx, e.pool, gid, hostID)
		if errors.Is(err, store.ErrOtherHost) {
			if e.m != nil {
				e.m.HostReportsRefused.WithLabelValues("foreign_guest").Inc()
			}
			e.log.Warn("host reports a guest placed elsewhere", "event", "foreign_guest", "host_id", hostID.String(), "guest_id", g.GuestId)
			continue
		}
		if err != nil {
			e.log.Warn("host reports a guest the api does not know", "event", "reconcile_orphan", "host_id", hostID.String(), "guest_id", g.GuestId, "state", g.State)
			continue
		}
		open, _ := store.OpenOpsForProject(ctx, e.pool, p.ID)
		if len(open) > 0 {
			continue // the op's result settles the state
		}
		if settled(g.State) && settled(p.State) && g.State != p.State {
			e.log.Info("reconciling project state from Hello", "event", "reconcile", "project_id", p.ID.String(), "from", p.State, "to", g.State)
			if err := store.SetProjectState(ctx, e.pool, p.ID, g.State); err != nil {
				e.log.Error("reconcile update", "event", "reconcile", "err", err.Error())
			}
		}
	}
	projects, _ := store.ListProjectsOnHost(ctx, e.pool, hostID)
	for _, p := range projects {
		if p.GuestID != nil && !known[*p.GuestID] && p.State == "running" {
			open, _ := store.OpenOpsForProject(ctx, e.pool, p.ID)
			if len(open) == 0 {
				e.log.Warn("host no longer has a running project's guest", "event", "reconcile_missing", "project_id", p.ID.String())
				_ = store.SetProjectState(ctx, e.pool, p.ID, "error") // best effort; the next start re-creates
			}
		}
		// A project left holding the address of a guest a failed in-place
		// restore destroyed (rows from before I-461) lets go of it. Hello
		// alone is not the evidence, since a partial Hello or a hostd that
		// lost its state would also leave a live guest out: the project's
		// newest restore must have finished its destroy_guest phase and
		// failed after it, which is also what makes start refuse it.
		if p.GuestID != nil && !known[*p.GuestID] && p.State == "error" && p.GuestIP != nil {
			open, _ := store.OpenOpsForProject(ctx, e.pool, p.ID)
			rf, err := unfinishedRestore(ctx, e.pool, p.ID)
			if len(open) == 0 && err == nil && rf.destroyedOld {
				e.log.Warn("releasing the address of a guest a failed restore destroyed", "event", "reconcile_missing", "project_id", p.ID.String())
				_, _ = e.pool.Exec(ctx, "update projects set guest_ip = null, vsock_cid = null where id = $1 and guest_id = $2 and state = 'error'", p.ID, *p.GuestID) // best effort; the next Hello tries again
			}
		}
	}
	rows, err := e.pool.Query(ctx, "select id from ops where host_id = $1 and state = 'running' and command_id is not null and command_result is null order by created_at", hostID)
	if err != nil {
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		op, err := store.GetOp(ctx, e.pool, id)
		if err != nil {
			continue
		}
		cmd, hid, skip, err := e.buildCommand(ctx, op, currentPhase(op))
		if err != nil || skip || hid != hostID {
			continue
		}
		cmd.CommandId = op.CommandID.String()
		if currentPhase(op) == "build" {
			e.logs.Bind(cmd.CommandId, op.ID)
		}
		e.log.Info("re-sending unfinished command after Hello", "event", "command_resend", "op_id", op.ID.String(), "command_id", cmd.CommandId)
		_ = e.send.Send(ctx, hostID, cmd) // a failed send is retried on the next Hello
	}
	e.Kick()
}

func settled(state string) bool {
	return state == "running" || state == "stopped" || state == "error"
}

type opError struct {
	code, msg string
	line      int
}

func (o *opError) Error() string { return o.code + ": " + o.msg }

func errCapacity() error { return &opError{code: "capacity", msg: "no host with capacity"} }

// errPlacementWait is a placement that found no host now but will on a
// later tick: a guest being stopped holds the memory it needs. The op
// stays where it is and the next tick places it again (I-408).
var errPlacementWait = errors.New("placement waits for a guest being stopped")

// placementWaits reports whether op's failed placement is tried again:
// for up to PlacementWait from the op's creation, while a guest being
// stopped would make room (scheduler.Freeing). The first wait is logged
// once per op.
func (e *Engine) placementWaits(ctx context.Context, op *store.Op, p *store.Project) bool {
	if e.now().Sub(op.CreatedAt) >= e.cfg.PlacementWait {
		return false
	}
	ok, err := scheduler.Freeing(ctx, e.pool, p.Class, p.HeldBytes(), p.VolumeBytes, e.now())
	if err != nil || !ok {
		return false
	}
	if _, logged := e.placeWaiting.LoadOrStore(op.ID, true); !logged {
		e.log.Info("placement waits for a guest being stopped", "event", "schedule_wait",
			"op_id", op.ID.String(), "project_id", p.ID.String(), "class", p.Class)
	}
	return true
}

func (e *Engine) fail(ctx context.Context, op *store.Op, code, msg string) {
	e.failWithLine(ctx, op, code, msg, 0)
}

func (e *Engine) failWithLine(ctx context.Context, op *store.Op, code, msg string, line int) {
	e.failWithLines(ctx, op, code, msg, line, 0)
}

// failWithLines is failWithLine with the line in the personal layer
// (machine.nix, DECISIONS I-490) beside the fragment's.
func (e *Engine) failWithLines(ctx context.Context, op *store.Op, code, msg string, line, personalLine int) {
	if op.CommandID != nil {
		e.logs.Unbind(op.CommandID.String())
	}
	// A failed build's message carries the tail of the builder's log,
	// which goes through the same redaction as the log lines.
	msg = e.logs.Redact(op.ID, msg)
	e.logs.ClearRedactions(op.ID)
	e.placeWaiting.Delete(op.ID)
	errObj := map[string]any{"code": code, "message": msg}
	// The user reads message; the host's own wording names internal ids
	// and stays in detail for operators (I-159).
	if human, ok := humanError(op.Kind, currentPhase(op), code, msg); ok {
		errObj["message"], errObj["detail"] = human, msg
		msg = human
	}
	if line > 0 {
		errObj["fragment_line"] = line
	}
	if personalLine > 0 {
		errObj["personal_line"] = personalLine
	}
	// Side effects first (project state, revision status), then the op row:
	// clients poll the op row and read the project the moment it says
	// error, so the reverse order let them see a failed op on a project
	// still "creating" (CI, 2026-09-20).
	e.onFail(ctx, op, code, msg, line, personalLine)
	if _, err := e.pool.Exec(ctx, "update ops set state = 'error', error = $2, finished_at = now() where id = $1", op.ID, errObj); err != nil {
		e.log.Error("op fail record", "event", "op_fail", "op_id", op.ID.String(), "err", err.Error())
	}
	e.log.Warn("op failed", "event", "op_fail", "op_id", op.ID.String(), "kind", op.Kind, "phase", currentPhase(op), "code", code)
	e.m.OpsTotal.WithLabelValues(op.Kind, "error").Inc()
	e.m.OpsOpen.WithLabelValues(op.Kind).Dec()
	if code == "capacity" {
		e.m.ScheduleTotal.WithLabelValues("capacity").Inc()
	}
	op.State = "error"
	e.notifyWaiters(op)
	if f := e.finishedHook(); f != nil {
		f(ctx, op)
	}
}

func (e *Engine) finish(ctx context.Context, op *store.Op) {
	e.logs.ClearRedactions(op.ID)
	e.placeWaiting.Delete(op.ID)
	if op.Kind == KindDestroy && op.ProjectID != nil {
		// A destroy with no phases (no guest to stop or destroy) still
		// ends with the project destroyed (I-124).
		if err := e.markDestroyed(ctx, op, *op.ProjectID); err != nil {
			e.log.Error("destroy finalise", "event", "op_done", "op_id", op.ID.String(), "err", err.Error())
		}
	}
	if _, err := e.pool.Exec(ctx, "update ops set state = 'done', finished_at = now(), result = coalesce(result, '{}'::jsonb), reboot_required = $2 where id = $1", op.ID, op.RebootRequired); err != nil {
		e.log.Error("op done record", "event", "op_done", "op_id", op.ID.String(), "err", err.Error())
	}
	e.log.Info("op done", "event", "op_done", "op_id", op.ID.String(), "kind", op.Kind)
	e.m.OpsTotal.WithLabelValues(op.Kind, "done").Inc()
	e.m.OpsOpen.WithLabelValues(op.Kind).Dec()
	if op.Kind == "build" || op.Kind == "create" {
		if op.StartedAt != nil {
			e.m.BuildDuration.WithLabelValues("ok").Observe(e.now().Sub(*op.StartedAt).Seconds())
		}
	}
	op.State = "done"
	e.notifyWaiters(op)
	if f := e.finishedHook(); f != nil {
		f(ctx, op)
	}
}

func (e *Engine) notifyWaiters(op *store.Op) {
	e.mu.Lock()
	ws := e.waiters[op.ID]
	delete(e.waiters, op.ID)
	e.mu.Unlock()
	for _, w := range ws {
		close(w)
	}
}

// Wait blocks until the op finishes or ctx ends and returns the row.
func (e *Engine) Wait(ctx context.Context, id uuid.UUID) (*store.Op, error) {
	for {
		op, err := store.GetOp(ctx, e.pool, id)
		if err != nil {
			return nil, err
		}
		if op.State == "done" || op.State == "error" {
			return op, nil
		}
		ch := make(chan struct{})
		e.mu.Lock()
		e.waiters[id] = append(e.waiters[id], ch)
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-ch:
		case <-time.After(time.Second):
		}
	}
}

// setResult merges fields into ops.result.
func (e *Engine) setResult(ctx context.Context, op *store.Op, fields map[string]any) error {
	_, err := e.pool.Exec(ctx, "update ops set result = coalesce(result, '{}'::jsonb) || $2::jsonb where id = $1", op.ID, fields)
	return err
}

// SetOnFinished installs the hook called after an op reaches done or error.
// Safe to call while the engine is running.
func (e *Engine) SetOnFinished(f func(ctx context.Context, op *store.Op)) {
	e.onFinishedMu.Lock()
	defer e.onFinishedMu.Unlock()
	e.onFinished = f
}

func (e *Engine) finishedHook() func(ctx context.Context, op *store.Op) {
	e.onFinishedMu.RLock()
	defer e.onFinishedMu.RUnlock()
	return e.onFinished
}
