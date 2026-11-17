package triggers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Sender sends a command down a host's stream. Only the API_MODE=grpc
// process holds one (I-87, I-163), which is why the clock and the worker
// live there and a fire travels as a row (I-1162).
type Sender interface {
	Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error
}

// Waker is internal/api/resting's Wake (I-818): a frozen machine is woken
// before its session starts, the way a delivery and an answer wake one.
type Waker interface {
	Wake(ctx context.Context, projectID uuid.UUID, reason string) (*hostdv1.WakeResult, error)
}

// Starter starts a stopped machine for a trigger that says --start. It is
// the owner's own start path, so the plan's memory and an abuse hold
// refuse it exactly as they refuse `repose start` (the webhook rule,
// I-1114).
type Starter interface {
	StartForTrigger(ctx context.Context, projectID uuid.UUID) (started bool, reason string, err error)
}

// NotifyChannel carries "a run is waiting" from the http process to the
// worker in the grpc one. The worker's tick covers a lost NOTIFY.
const NotifyChannel = "repose_trigger_runs"

// TickInterval is the clock's pass. A fire lands within a minute of its
// time, which is what features/triggers.md §6 promises.
const TickInterval = time.Minute

// ClaimBatch bounds one pass, so a fleet's 07:00 is spread over the
// minute rather than sent in one burst (I-1252).
const ClaimBatch = 50

// Clock is the tick and the worker: it claims the schedules that are due
// and sends every pending run to its machine.
type Clock struct {
	store *Store
	send  Sender
	log   *slog.Logger

	// Wake, Start and Push are set by the app; each may be nil, and the
	// run then skips with the reason that says why.
	Wake  Waker
	Start Starter

	// Interval and Batch are the pass; tests shorten them.
	Interval time.Duration
	Batch    int

	kicks chan struct{}
}

// NewClock builds the clock.
func NewClock(store *Store, send Sender, log *slog.Logger) *Clock {
	return &Clock{store: store, send: send, log: log.With("component", "api"),
		Interval: TickInterval, Batch: ClaimBatch, kicks: make(chan struct{}, 1)}
}

// Kick tells the worker a run is waiting: this process's, if it runs one,
// and the other process's through NOTIFY.
func (c *Clock) Kick(ctx context.Context) {
	select {
	case c.kicks <- struct{}{}:
	default:
	}
	if _, err := c.store.pool.Exec(ctx, "select pg_notify($1, '')", NotifyChannel); err != nil {
		c.log.Warn("trigger notify failed; the worker's tick covers it", "event", "trigger_run", "err", err.Error())
	}
}

// Run holds the clock's advisory lock and ticks until the context ends,
// so one replica fires (I-1252).
func (c *Clock) Run(ctx context.Context) {
	for {
		release, ok, err := db.TryLock(ctx, c.store.pool, db.LockTriggers)
		if err != nil && ctx.Err() == nil {
			c.log.Error("trigger clock lock", "event", "trigger_run", "err", err.Error())
		}
		if ok {
			c.loop(ctx)
			release()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Clock) loop(ctx context.Context) {
	lctx, stop := context.WithCancel(ctx)
	defer stop()
	go c.listen(lctx)
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	for {
		if err := c.Once(ctx); err != nil && ctx.Err() == nil {
			c.log.Error("trigger pass", "event", "trigger_run", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.kicks:
		}
	}
}

// listen turns a NOTIFY from the http process into a pass.
func (c *Clock) listen(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := c.store.pool.Acquire(ctx)
		if err != nil {
			return
		}
		if _, err := conn.Exec(ctx, "listen "+NotifyChannel); err == nil {
			for {
				if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				select {
				case c.kicks <- struct{}{}:
				default:
				}
			}
		}
		_ = conn.Conn().Close(context.Background())
		conn.Release()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// Once is one pass: claim what the clock owes, fail what went stale, and
// send every pending run. It reconciles, so a missed tick costs a minute
// and nothing else.
func (c *Clock) Once(ctx context.Context) error {
	if _, err := c.store.Claim(ctx, c.Batch); err != nil {
		return err
	}
	if n, err := c.store.FailStale(ctx); err != nil {
		return err
	} else if n > 0 {
		c.log.Warn("trigger runs nobody delivered", "event", "trigger_run", "count", n)
	}
	runs, err := c.store.TakePending(ctx, c.Batch)
	if err != nil {
		return err
	}
	for i := range runs {
		if err := c.dispatch(ctx, runs[i]); err != nil && ctx.Err() == nil {
			c.log.Error("trigger dispatch", "event", "trigger_run",
				"run_id", runs[i].ID.String(), "err", err.Error())
		}
	}
	return nil
}

// dispatch sends one claimed run to its machine, starting or waking it
// first where the trigger says so. Every refusal is written on the row,
// which is the only place a user can read it.
func (c *Clock) dispatch(ctx context.Context, run Run) error {
	if run.TriggerID == nil {
		return c.store.Answered(ctx, run.ID, StateSkipped, "", "error")
	}
	t, err := c.store.byID(ctx, *run.TriggerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return c.store.Answered(ctx, run.ID, StateSkipped, "", "error")
		}
		return err
	}

	var guestID, hostID *uuid.UUID
	var state string
	var resting bool
	err = c.store.pool.QueryRow(ctx,
		`select state, guest_id, host_id, resting_since is not null from projects where id = $1`,
		run.ProjectID).Scan(&state, &guestID, &hostID, &resting)
	if err != nil {
		return err
	}

	switch {
	case state == "stopped":
		if !t.AllowStart || c.Start == nil {
			return c.store.Answered(ctx, run.ID, StateSkipped, "", "stopped")
		}
		started, why, err := c.Start.StartForTrigger(ctx, run.ProjectID)
		if err != nil {
			return err
		}
		if !started {
			reason := "stopped"
			if why == "plan_limit" {
				reason = "plan_limit"
			}
			return c.store.Answered(ctx, run.ID, StateSkipped, "", reason)
		}
		// The start is an op of its own and takes 12 to 14 seconds. The
		// run stays in the hand-off and the next pass sends it, which is
		// also what makes a start that fails end as a skip rather than a
		// run that never happened.
		return c.store.Requeue(ctx, run.ID)
	case state != "running" || guestID == nil:
		return c.store.Answered(ctx, run.ID, StateSkipped, "", "busy")
	case hostID == nil:
		return c.store.Answered(ctx, run.ID, StateSkipped, "", "error")
	}

	if resting && c.Wake != nil {
		if _, err := c.Wake.Wake(ctx, run.ProjectID, "trigger"); err != nil {
			c.log.Warn("trigger wake failed", "event", "trigger_run",
				"run_id", run.ID.String(), "err", err.Error())
		}
	}

	rt := &hostdv1.RunTrigger{
		GuestId:     guestID.String(),
		Trigger:     t.Name,
		RunId:       run.ID.String(),
		Source:      run.Source,
		PayloadPath: run.PayloadPath,
		DryRun:      run.DryRun,
	}
	if run.PayloadRun != nil {
		rt.PayloadRun = run.PayloadRun.String()
	}
	cmd := &hostdv1.Command{
		CommandId: run.ID.String(),
		Cmd:       &hostdv1.Command_RunTrigger{RunTrigger: rt},
	}
	if err := c.send.Send(ctx, *hostID, cmd); err != nil {
		return c.store.Answered(ctx, run.ID, StateSkipped, "", "unreachable")
	}
	return nil
}

// OnResult is the host's answer to a RunTrigger, which app.go hands every
// result to. It reports whether the result was one of ours.
func (c *Clock) OnResult(ctx context.Context, res *hostdv1.Result) bool {
	id, err := uuid.Parse(res.GetCommandId())
	if err != nil {
		return false
	}
	payload, ok := res.GetPayload().(*hostdv1.Result_TriggerRun)
	if !ok {
		return false
	}
	state, window, reason := payload.TriggerRun.GetState(), payload.TriggerRun.GetWindow(), payload.TriggerRun.GetReason()
	if !res.GetOk() {
		state, reason = StateFailed, "unreachable"
		if res.GetError() != nil && Reasons[res.GetError().GetCode()] {
			reason = res.GetError().GetCode()
		}
	}
	if err := c.store.Answered(ctx, id, state, window, reason); err != nil && ctx.Err() == nil {
		c.log.Error("trigger answer", "event", "trigger_run", "run_id", id.String(), "err", err.Error())
	}
	return true
}

// byID reads one trigger without its project, for the worker.
func (s *Store) byID(ctx context.Context, id uuid.UUID) (Trigger, error) {
	rows, err := s.pool.Query(ctx, selectTrigger+` where t.id = $1`, id)
	if err != nil {
		return Trigger{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Trigger{}, err
		}
		return Trigger{}, ErrNotFound
	}
	return scanTrigger(rows)
}

// Requeue puts a claimed run back in the hand-off, for the one case that
// has to wait for something else: a machine the fire is starting.
func (s *Store) Requeue(ctx context.Context, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `update trigger_runs set state = $2 where id = $1 and state = $3`,
		runID, StatePending, StateSending)
	return err
}
