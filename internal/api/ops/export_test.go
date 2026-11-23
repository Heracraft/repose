package ops

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// HumanErrorForTest is humanError for the external tests.
var HumanErrorForTest = humanError

// PhaseTimeoutForTest is phaseTimeout for the external tests.
var PhaseTimeoutForTest = phaseTimeout

// TimeoutForTest is the timeout checkTimeout applies to op's current
// phase.
func (e *Engine) TimeoutForTest(ctx context.Context, op *store.Op) time.Duration {
	return e.timeoutFor(ctx, op)
}

// RecordGuestAddressForTest is the address write on its own, for the
// external test package (ENG-111, DECISIONS I-1444).
//
// The defect was a window: the Hello reconcile read the project's open
// ops, found none, then wrote the address in a second statement, so an op
// opening in between was invisible to the guard. Reproducing that through
// OnHello needs the op to appear between two statements, which a test can
// only do with a sleep and a hope: it took CI's slower interleaving to
// show it at all, and `-race -count=20` on this box never did.
//
// Calling the write directly with an op already open asks what the window
// asks and answers it the same way every run. Before I-1444 this wrote
// the address; after it the row is left alone.
func (e *Engine) RecordGuestAddressForTest(ctx context.Context, p *store.Project, ip string, cid uint32) {
	e.recordGuestAddress(ctx, p, ip, cid, "hello", nil)
}

// RecordGuestAddressForOpTest is the same write as the op it belongs to
// makes it: a start's result, applied while that start is still running
// (I-1444).
func (e *Engine) RecordGuestAddressForOpTest(ctx context.Context, p *store.Project, ip string, cid uint32, op uuid.UUID) {
	e.recordGuestAddress(ctx, p, ip, cid, "start", &op)
}

// ReleaseGuestAddressForTest is the address *clear* on its own, the other
// writer of guest_ip in the reconcile (I-1444).
func (e *Engine) ReleaseGuestAddressForTest(ctx context.Context, p *store.Project) {
	e.releaseGuestAddress(ctx, p)
}
