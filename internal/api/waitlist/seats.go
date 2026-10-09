package waitlist

import (
	"context"
	"time"
)

// Place is a user's place on the waitlist, as `503 waitlisted` and
// `GET /billing` report it (DECISIONS I-290).
type Place struct {
	Position  int
	JoinedAt  time.Time
	Email     string
	InvitedAt *time.Time
	HoldUntil *time.Time
}

// Count is the fleet's seats: Total from the ready, undrained hosts or
// SEATS_TOTAL, Held by live subscriptions and unexpired invitations, Free
// their difference (never negative), Waiting the users on the list.
type Count struct {
	Total   int
	Held    int
	Free    int
	Waiting int
}

// Seats is what checkout asks before it creates a Polar checkout, and
// what the webhook tells when a subscription arrives (DECISIONS I-289,
// I-290). The billing package consumes it; this package implements it.
type Seats interface {
	// Reserve says whether a checkout for `seats` seats may proceed for
	// this user right now; an invited user's own hold counts toward it.
	// When it may not, the user is on the waitlist afterwards (joining is
	// idempotent) and place describes their position.
	Reserve(ctx context.Context, userID string, seats int) (ok bool, place *Place, err error)
	// Converted records that the user's subscription arrived, freeing the
	// hold and keeping the row for the count. A user who was never on the
	// list is not an error.
	Converted(ctx context.Context, userID string) error
	// Count is the fleet's seat count for GET /billing and /public/seats.
	Count(ctx context.Context) (Count, error)
}
