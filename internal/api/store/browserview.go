package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The dashboard browser view's two tables (0042, DECISIONS I-1216).
//
// A ticket authenticates one connect to one project's desktop and lives
// fifteen minutes; the page mints a fresh one through the authenticated
// POST on every connect and reconnect, so expiry is the revocation and
// nothing has to be revoked by hand when a machine stops. The row holds
// sha256(ticket) and never the ticket.

// ViewTicket is a browser_view_tickets row. Hash is sha256 of the ticket;
// the ticket itself exists only in the mint's answer and the cookie it
// sets.
type ViewTicket struct {
	ID         uuid.UUID  `db:"id"`
	Hash       []byte     `db:"hash"`
	ProjectID  uuid.UUID  `db:"project_id"`
	UserID     uuid.UUID  `db:"user_id"`
	CreatedAt  time.Time  `db:"created_at"`
	ExpiresAt  time.Time  `db:"expires_at"`
	LastUsedAt *time.Time `db:"last_used_at"`
	// Bytes is what the stream has already spent of the ticket's budget,
	// flushed as it pipes. On the row and not in memory so a reconnect
	// cannot start the budget again.
	Bytes int64 `db:"bytes"`
}

const viewTicketCols = "id, hash, project_id, user_id, created_at, expires_at, last_used_at, bytes"

// InsertViewTicket stores a minted ticket's hash and returns the row's id,
// which is the only thing about a ticket that may be logged.
func InsertViewTicket(ctx context.Context, q Querier, hash []byte, projectID, userID uuid.UUID, expires time.Time) (uuid.UUID, error) {
	id := NewID()
	_, err := q.Exec(ctx, "insert into browser_view_tickets (id, hash, project_id, user_id, expires_at) values ($1, $2, $3, $4, $5)",
		id, hash, projectID, userID, expires)
	return id, err
}

// GetViewTicket returns a live ticket by its hash, or db.ErrNotFound. An
// expired row is not found: expiry is this credential's revocation, so the
// stream route needs no second check and cannot forget one.
func GetViewTicket(ctx context.Context, q Querier, hash []byte) (*ViewTicket, error) {
	return one[ViewTicket](ctx, q, "select "+viewTicketCols+" from browser_view_tickets where hash = $1 and expires_at > now()", hash)
}

// TouchViewTicket adds addBytes to the ticket's spend and returns the new
// total, so the caller can close on the budget. The addition is in the
// database rather than read-modify-write in the api: two replicas piping
// the same ticket both count.
func TouchViewTicket(ctx context.Context, q Querier, hash []byte, addBytes int64) (spent int64, err error) {
	err = q.QueryRow(ctx, "update browser_view_tickets set last_used_at = now(), bytes = bytes + $2 where hash = $1 returning bytes", hash, addBytes).Scan(&spent)
	return spent, err
}

// DeleteExpiredViewTickets drops the rows that can no longer authenticate
// anything. Every mint calls it, so the table needs no janitor job.
func DeleteExpiredViewTickets(ctx context.Context, q Querier) error {
	_, err := q.Exec(ctx, "delete from browser_view_tickets where expires_at <= now()")
	return err
}

// LastViewWake is when a mint last woke this project for the view, and
// whether it ever did.
func LastViewWake(ctx context.Context, q Querier, projectID uuid.UUID) (time.Time, bool, error) {
	var at time.Time
	err := q.QueryRow(ctx, "select at from browser_view_wakes where project_id = $1", projectID).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return at, err == nil, err
}

// MarkViewWake records a wake the mint has just asked for.
func MarkViewWake(ctx context.Context, q Querier, projectID uuid.UUID) error {
	_, err := q.Exec(ctx, "insert into browser_view_wakes (project_id, at) values ($1, now()) on conflict (project_id) do update set at = now()", projectID)
	return err
}
