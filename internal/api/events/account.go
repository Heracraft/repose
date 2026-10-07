package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// AccountKinds are the events that name a user and no project (0007,
// I-269; I-291): the emails about the account itself. Each is
// transactional mail, sent whatever notify_email says and without an
// unsubscribe link, because each answers something the user did or is
// about to be charged for (docs/features/notifications.md, "Account
// emails"). Their summary is a small JSON object of the fields the
// template renders, documented there per kind.
var AccountKinds = map[string]bool{
	"welcome":                true,
	"waitlist_joined":        true,
	"waitlist_invited":       true,
	"waitlist_expired":       true,
	"trial_ending":           true,
	"payment_failed":         true,
	"subscription_cancelled": true,
	"subscription_ended":     true,
	"plan_changed":           true,
	"egress_stopped":         true,
	"disk_over_plan":         true,
}

// ErrNotAccountKind is InsertAccount's answer to a kind outside
// AccountKinds: a project event written without a project would reach
// the outbox with no template for it.
var ErrNotAccountKind = errors.New("events: not an account kind")

// InsertAccount writes a user-only event and its email outbox row in the
// caller's transaction (or on the pool), so an invitation, an expiry or a
// new account and its email are one commit. payload is marshalled into
// events.summary; nil writes an empty summary. The outbox row is written
// only when the user has an address: account mail ignores notify_email,
// which the outbox worker knows from the kind. The event id is returned.
func InsertAccount(ctx context.Context, q store.Querier, userID uuid.UUID, ts time.Time, kind string, payload any) (uuid.UUID, error) {
	if !AccountKinds[kind] {
		return uuid.Nil, fmt.Errorf("%w: %s", ErrNotAccountKind, kind)
	}
	summary := ""
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return uuid.Nil, err
		}
		summary = string(b)
	}
	id := store.NewID()
	if _, err := q.Exec(ctx, `insert into events (id, project_id, user_id, ts, ts_second, kind, summary, source) values ($1, null, $2, $3, $4, $5, $6, 'api')`,
		id, userID, ts, ts.Unix(), kind, summary); err != nil {
		return uuid.Nil, err
	}
	var email *string
	if err := q.QueryRow(ctx, "select email from users where id = $1", userID).Scan(&email); err != nil {
		return uuid.Nil, err
	}
	if email != nil && *email != "" {
		if _, err := q.Exec(ctx, "insert into events_outbox (event_id, channel) values ($1, 'email')", id); err != nil {
			return uuid.Nil, err
		}
	}
	return id, nil
}

// Account is InsertAccount on the ingest's pool at its clock, for
// producers outside a transaction (the plan emails of I-291).
func (i *Ingest) Account(ctx context.Context, userID uuid.UUID, kind string, payload any) (uuid.UUID, error) {
	id, err := InsertAccount(ctx, i.pool, userID, i.now(), kind, payload)
	if err == nil {
		i.m.EventsTotal.WithLabelValues(kind).Inc()
	}
	return id, err
}
