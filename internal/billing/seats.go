package billing

import (
	"context"

	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/db"
)

// SubscriptionSeats is the stand-in waitlist.Seats until the seats
// workstream's implementation (I-290) takes its place at merge: it counts
// the seats live subscriptions hold against SEATS_TOTAL (0 = unlimited)
// and never waitlists anyone. ok is free >= seats and place is nil.
type SubscriptionSeats struct {
	Pool  *db.Pool
	Total int
}

// Reserve says whether the plan's seats are free.
func (s *SubscriptionSeats) Reserve(ctx context.Context, _ string, seats int) (bool, *waitlist.Place, error) {
	if s.Total <= 0 {
		return true, nil, nil
	}
	c, err := s.Count(ctx)
	if err != nil {
		return false, nil, err
	}
	return c.Free >= seats, nil, nil
}

// Fits is Reserve: this stub never waitlists, so the two agree.
func (s *SubscriptionSeats) Fits(ctx context.Context, userID string, seats int) (bool, error) {
	ok, _, err := s.Reserve(ctx, userID, seats)
	return ok, err
}

// Converted is a no-op: this stub keeps no invitations.
func (s *SubscriptionSeats) Converted(context.Context, string) error { return nil }

// Count is the fleet's seat count from subscriptions alone.
func (s *SubscriptionSeats) Count(ctx context.Context) (waitlist.Count, error) {
	c := waitlist.Count{Total: s.Total}
	if err := s.Pool.QueryRow(ctx, "select coalesce(sum(seats), 0)::integer from subscriptions where status in ('trialing','active','past_due')").Scan(&c.Held); err != nil {
		return c, err
	}
	if err := s.Pool.QueryRow(ctx, "select count(*)::integer from waitlist where invited_at is null and converted_at is null and lapsed_at is null").Scan(&c.Waiting); err != nil {
		return c, err
	}
	if s.Total > 0 {
		c.Free = max(s.Total-c.Held, 0)
	}
	return c, nil
}
