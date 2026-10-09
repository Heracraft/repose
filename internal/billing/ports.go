package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
)

// ErrDisabled is returned by every Polar-backed call while billing is not
// configured (DECISIONS I-16, I-289, I-604). The HTTP layer maps it to
// `503 billing_disabled`.
var ErrDisabled = errors.New("billing_disabled")

// Stopper enqueues the stop of a project's guest. The ops engine satisfies
// it; a test can substitute a recorder.
type Stopper interface {
	Enqueue(ctx context.Context, q store.Querier, n ops.NewOp, allowQueue bool) (uuid.UUID, error)
	Kick()
}

// EventSink records a platform event on a project so the notification
// outbox delivers it. events.Ingest.Platform satisfies it.
type EventSink interface {
	Platform(ctx context.Context, projectID uuid.UUID, kind, summary string) error
}

// OverageSender is the one Polar call the overage job makes: the
// period's whole GB over the allowance as one metered event, deduped on
// externalID. The client satisfies it and tests record it.
type OverageSender interface {
	SendOverage(ctx context.Context, userID uuid.UUID, externalID string, gb int64, periodStart time.Time) error
}
