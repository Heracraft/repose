package ops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// WakeWait is how long Wake waits for its op. A pause's wake answers in
// well under a second once sent, a Mac's rest by save in up to tens of
// seconds (I-1696); the rest is the op reaching the process that
// drives it, and an op the project already had open, which the wake
// queues behind.
const WakeWait = 120 * time.Second

// ErrWakeNoHost is a wake whose host is not connected: the phase was
// skipped and the machine still rests.
var ErrWakeNoHost = errors.New("the machine's host is not connected")

// ErrWakeTimeout is a wake op that did not finish within WakeWait. It
// stays queued and finishes on its own.
var ErrWakeTimeout = errors.New("the wake did not finish in time")

// WakeError is a wake the host answered with an error.
type WakeError struct{ Message string }

func (e *WakeError) Error() string { return "the wake failed: " + e.Message }

// Wake wakes a machine that rests through an op, the way a user's
// `repose start` reaches its host (DECISIONS I-1642). It is the wake for
// a route in the http process: that process holds no host's stream, so
// internal/api/resting's Wake, which sends on this process's stream,
// cannot reach the host from there. The process that holds the stream
// drives the op, and this one waits on the op's row.
//
// The op queues behind any op the project already has open, as a
// secrets push does: the wake is idempotent and the user is waiting.
func (e *Engine) Wake(ctx context.Context, projectID uuid.UUID, reason string) (*hostdv1.WakeResult, error) {
	res, err := e.wake(ctx, projectID, reason)
	result := "failed"
	switch {
	case err != nil:
	case res.GetWasResting():
		result = "woken"
	default:
		result = "awake"
	}
	e.m.RestingWakeTotal.WithLabelValues(reason, result).Inc()
	if err != nil {
		cause := "refused"
		switch {
		case errors.Is(err, ErrWakeNoHost):
			cause = "host_not_connected"
		case errors.Is(err, ErrWakeTimeout):
			cause = "timeout"
		}
		e.log.Warn("wake failed", "event", "guest_wake_fail", "project_id", projectID.String(), "reason", reason, "cause", cause)
	}
	return res, err
}

func (e *Engine) wake(ctx context.Context, projectID uuid.UUID, reason string) (*hostdv1.WakeResult, error) {
	id, err := e.Enqueue(ctx, e.pool, NewOp{Kind: KindWake, ProjectID: &projectID, Params: map[string]any{"reason": reason}, Phases: PlanWake()}, true)
	if err != nil {
		return nil, err
	}
	e.Kick()
	wctx, cancel := context.WithTimeout(ctx, WakeWait)
	defer cancel()
	op, err := e.Wait(wctx, id)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, ErrWakeTimeout
		}
		return nil, err
	}
	if op.State == "error" {
		msg, _ := op.Error["message"].(string)
		if msg == "" {
			msg, _ = op.Error["code"].(string)
		}
		return nil, &WakeError{Message: msg}
	}
	was, sent := op.Result["was_resting"].(bool)
	if !sent {
		// The phase was skipped. A machine that stopped or lost its
		// guest meanwhile has nothing to wake; one that still rests sat
		// on a host that is away.
		var rests bool
		if err := e.pool.QueryRow(ctx, "select resting_since is not null and state = 'running' from projects where id = $1", projectID).Scan(&rests); err != nil {
			return nil, err
		}
		if rests {
			return nil, ErrWakeNoHost
		}
		return &hostdv1.WakeResult{}, nil
	}
	ms, _ := op.Result["wake_ms"].(float64)
	return &hostdv1.WakeResult{WasResting: was, WakeMs: uint32(ms)}, nil
}
