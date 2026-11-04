// Package waitlist is the seats waitlist (DECISIONS I-269, amended by
// I-290 and I-739). The fleet has as many seats as its ready, undrained
// hosts deliver, each host the smaller of what its memory divides into
// and what its physical cores can serve (hostseats.go), or as many as
// SEATS_TOTAL says. A seat is held by every live subscription
// (its plan's seats) and by every invitation whose hold has not run out.
// Checkout asks Reserve before it creates a Polar checkout; without a
// free seat the user joins the list and gets `waitlisted`. Every minute
// the Inviter hands free seats to the oldest waiting users, one seat each
// and strictly in order, holding each for 72 hours; a hold that runs out
// unconverted takes the user off the list until they ask again, and says
// so by email (I-1033).
//
// Every email here is an account event through the outbox
// (events.InsertAccount): joined, invited, expired.
package waitlist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// SeatBytes is one seat: the memory a running `large` takes.
const SeatBytes = int64(8) << 30

// HoldDuration is how long an invitation holds a seat.
const HoldDuration = 72 * time.Hour

// legacyCooldown is I-902's cooldown, still written on expiry and still
// honoured, so an api on the code before I-1033 (mid-deploy, or after a
// rollback) reads a lapsed row as a cooling one rather than as the next
// candidate, and a row cooling at the migration keeps the place its
// email named. A lapse is what keeps a user out since I-1033.
const legacyCooldown = 24 * time.Hour

// Event kinds this package raises; each is an account email (I-291).
const (
	KindJoined  = "waitlist_joined"
	KindInvited = "waitlist_invited"
	KindExpired = "waitlist_expired"
)

// Message is the waitlisted sentence, the one builder of it (I-294 (2)):
// the `waitlisted` checkout refusal, the compute gate's
// subscription_required refusal while the user waits, and the fake api all
// call it, and the CLI prints it as it is. The email is the address the
// invitation goes to; without one, the dashboard's plan page is where the
// place shows.
func Message(position int, email string) string {
	if email == "" {
		return fmt.Sprintf("repose is full right now. You're number %d on the waitlist. The dashboard's plan page shows your place.", position)
	}
	return fmt.Sprintf("repose is full right now. You're number %d on the waitlist; we'll email %s when there's a seat.", position, email)
}

// Service implements Seats on Postgres. Total is SEATS_TOTAL; 0 derives
// the count from the hosts. Now is the clock (tests).
type Service struct {
	Pool *db.Pool
	// Total is SEATS_TOTAL when the operator set it; 0 derives the count
	// from the ready, undrained hosts.
	Total int
	M     *metrics.M
	Log   *slog.Logger
	Now   func() time.Time
}

var _ Seats = (*Service)(nil)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Source says where a Count's Total came from.
type Source string

// Sources of the total.
const (
	SourceConfig Source = "SEATS_TOTAL"
	SourceHosts  Source = "hosts"
)

// Derivation is how a derived total was reached, for repose-admin seats:
// the ready, undrained hosts counted and what each ceiling allowed
// across them. Total is the smaller of the two per host and then summed,
// so on a mixed fleet neither figure need equal it. Zero when the total
// came from SEATS_TOTAL.
type Derivation struct {
	Hosts  int
	Memory int
	CPU    int
	// NoCores counts the hosts among them that reported no physical
	// cores, whose CPU ceiling is therefore unknown and counted at their
	// memory ceiling. After migration 0027 and I-735's write path that is
	// only a host which reported no vcpus either.
	NoCores int
}

// CountSource is Count with where Total came from and, when it came from
// the hosts, how, for repose-admin seats.
func (s *Service) CountSource(ctx context.Context) (Count, Source, Derivation, error) {
	c, d, err := count(ctx, s.Pool, s.Total, s.now())
	src := SourceHosts
	if s.Total > 0 {
		src = SourceConfig
	}
	return c, src, d, err
}

// Count implements Seats.
func (s *Service) Count(ctx context.Context) (Count, error) {
	c, _, err := count(ctx, s.Pool, s.Total, s.now())
	if err == nil {
		s.gauges(c)
	}
	return c, err
}

// count is the seat arithmetic of I-290 at now, on q (a pool or the
// locked transaction). The Derivation is empty when Total is SEATS_TOTAL.
func count(ctx context.Context, q store.Querier, total int, now time.Time) (Count, Derivation, error) {
	var c Count
	var d Derivation
	if total > 0 {
		c.Total = total
	} else {
		// The seats each ready, undrained host delivers, per host and
		// then summed, never the fleet's memory and cores summed first:
		// a project is pinned to its host for life (DESIGN §4), so a
		// guest cannot borrow an idle core on a neighbour and the risk
		// of a host being asked for more than it can serve is a fact
		// about that host alone (I-739).
		rows, err := q.Query(ctx, `select h.mem_bytes, h.cores
			from hosts h where h.state = 'ready' and not h.draining`)
		if err != nil {
			return c, d, err
		}
		// Drained fully before anything else runs on q: count is called
		// inside the advisory-locked transaction (Reserve, Inviter.Run),
		// where open rows would block the next query on it.
		for rows.Next() {
			var memBytes int64
			var cores int
			if err := rows.Scan(&memBytes, &cores); err != nil {
				rows.Close()
				return c, d, err
			}
			mem, cpu := HostCeilings(memBytes, cores)
			d.Hosts++
			if cores <= 0 {
				d.NoCores++
			}
			d.Memory += mem
			d.CPU += cpu
			c.Total += min(mem, cpu)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return c, d, err
		}
	}
	err := q.QueryRow(ctx, `select
		(select coalesce(sum(seats), 0) from subscriptions where status in ('trialing','active','past_due'))::integer
		+ (select count(*) from waitlist where invited_at is not null and converted_at is null and hold_until > $1)::integer,
		(select count(*) from waitlist w join users u on u.id = w.user_id
		  where w.invited_at is null and w.lapsed_at is null and (w.cooldown_until is null or w.cooldown_until <= $1)
		    and u.suspended_at is null and u.cancelled_at is null and u.deleted_at is null)::integer`, now).Scan(&c.Held, &c.Waiting)
	if err != nil {
		return c, d, err
	}
	c.Free = max(0, c.Total-c.Held)
	return c, d, nil
}

func (s *Service) gauges(c Count) {
	if s.M == nil {
		return
	}
	s.M.SeatsTotal.Set(float64(c.Total))
	s.M.SeatsHeld.Set(float64(c.Held))
	s.M.WaitlistWaiting.Set(float64(c.Waiting))
}

// lock takes the waitlist advisory lock for the transaction: Reserve,
// Join, the Inviter and repose-admin all count and write under it, so two
// checkouts cannot both take the last seat.
func lock(ctx context.Context, tx db.Tx) error {
	_, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", db.LockWaitlist)
	return err
}

// fits is the arithmetic Reserve and Fits share: Free, plus one for this
// user's own unexpired invitation, covers seats. q is the pool (Fits) or
// the locked transaction (Reserve).
func (s *Service) fits(ctx context.Context, q store.Querier, uid uuid.UUID, seats int, now time.Time) (bool, Count, error) {
	if seats < 1 {
		seats = 1
	}
	c, _, err := count(ctx, q, s.Total, now)
	if err != nil {
		return false, c, err
	}
	own := 0
	cur, err := store.GetWaitlistEntry(ctx, q, uid)
	switch {
	case errors.Is(err, db.ErrNotFound):
	case err != nil:
		return false, c, err
	case cur.Holding(now):
		own = 1
	}
	return c.Free+own >= seats, c, nil
}

// Fits implements Seats: Reserve's answer without the join, for GET
// /billing, which only reads. A page that joined whoever opened it would
// also undo a lapse (I-1033) without the user asking. It takes no lock,
// so a checkout that follows may still find the seat gone; Reserve
// decides.
func (s *Service) Fits(ctx context.Context, userID string, seats int) (bool, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false, fmt.Errorf("waitlist: user id: %w", err)
	}
	ok, _, err := s.fits(ctx, s.Pool, uid, seats, s.now())
	return ok, err
}

// Reserve implements Seats: ok when Free, plus one for this user's own
// unexpired invitation, covers seats; otherwise the user is on the list
// afterwards (joining is idempotent) and place is their position.
func (s *Service) Reserve(ctx context.Context, userID string, seats int) (bool, *Place, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false, nil, fmt.Errorf("waitlist: user id: %w", err)
	}
	now := s.now()
	var (
		ok    bool
		place *Place
		c     Count
	)
	err = db.InTx(ctx, s.Pool, func(tx db.Tx) error {
		if err := lock(ctx, tx); err != nil {
			return err
		}
		var err error
		ok, c, err = s.fits(ctx, tx, uid, seats, now)
		if err != nil || ok {
			return err
		}
		place, err = s.join(ctx, tx, uid, now)
		return err
	})
	if err != nil {
		return false, nil, err
	}
	if ok {
		s.gauges(c)
		return true, nil, nil
	}
	return false, place, nil
}

// Join puts the user on the list without a checkout (POST
// /billing/waitlist), idempotently: a second call answers the same place.
func (s *Service) Join(ctx context.Context, userID uuid.UUID) (*Place, error) {
	now := s.now()
	var place *Place
	err := db.InTx(ctx, s.Pool, func(tx db.Tx) error {
		if err := lock(ctx, tx); err != nil {
			return err
		}
		var err error
		place, err = s.join(ctx, tx, userID, now)
		return err
	})
	return place, err
}

// join is the idempotent insert under the lock. A row whose hold ran out
// is re-queued here rather than waiting for the tick; so is a lapsed row
// (I-1033) and a converted one (a plan that has since ended), since the
// user is asking again. Each goes to the back. A row that is waiting or
// holding keeps its place. A new row raises the joined email.
func (s *Service) join(ctx context.Context, tx db.Tx, userID uuid.UUID, now time.Time) (*Place, error) {
	tag, err := tx.Exec(ctx, "insert into waitlist (user_id, joined_at) values ($1, $2) on conflict (user_id) do nothing", userID, now)
	if err != nil {
		return nil, err
	}
	joined := tag.RowsAffected() == 1
	if !joined {
		// Re-queue an expired hold, a lapsed row or a converted row;
		// leave a waiting or holding row alone. A hold that ran out before
		// the tick saw it counts as expired here; a lapsed one was counted
		// when it lapsed.
		if _, err := tx.Exec(ctx, `update waitlist set joined_at = $2, invited_at = null, hold_until = null, invited_by = null, converted_at = null, lapsed_at = null, cooldown_until = null,
			expired_invites = expired_invites + case when invited_at is not null and converted_at is null then 1 else 0 end
			where user_id = $1 and (lapsed_at is not null or (invited_at is not null and (converted_at is not null or hold_until <= $2)))`, userID, now); err != nil {
			return nil, err
		}
	}
	e, err := store.GetWaitlistEntry(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	var email *string
	if err := tx.QueryRow(ctx, "select email from users where id = $1", userID).Scan(&email); err != nil {
		return nil, err
	}
	p := place(e, email)
	if joined {
		if _, err := events.InsertAccount(ctx, tx, userID, now, KindJoined, JoinedPayload{Position: p.Position}); err != nil {
			return nil, err
		}
		if s.M != nil {
			s.M.WaitlistJoinedTotal.Inc()
		}
		if s.Log != nil {
			s.Log.Info("user joined the waitlist", "event", "waitlist_join", "user_id", userID.String(), "position", p.Position)
		}
	}
	return p, nil
}

func place(e *store.WaitlistEntry, email *string) *Place {
	p := &Place{Position: e.Position, JoinedAt: e.JoinedAt, InvitedAt: e.InvitedAt, HoldUntil: e.HoldUntil}
	if email != nil {
		p.Email = *email
	}
	return p
}

// Converted implements Seats: the invited user's subscription arrived.
// The row stays, converted_at set, so the count keeps it and the hold
// stops counting. A user never on the list is not an error.
func (s *Service) Converted(ctx context.Context, userID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("waitlist: user id: %w", err)
	}
	tag, err := s.Pool.Exec(ctx, "update waitlist set converted_at = $2 where user_id = $1 and converted_at is null", uid, s.now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 && s.M != nil {
		s.M.WaitlistConvertedTotal.Inc()
	}
	return nil
}

// JoinedPayload is waitlist_joined's summary.
type JoinedPayload struct {
	Position int `json:"position"`
}

// ExpiredPayload is waitlist_expired's summary: empty since I-1033, as a
// lapsed user holds no place to name.
type ExpiredPayload struct{}

// InvitedPayload is waitlist_invited's summary.
type InvitedPayload struct {
	HoldUntil time.Time `json:"hold_until"`
}

// Invite hands one seat to a waiting user: invited_at, the 72-hour hold
// and the invitation email in one transaction guarded by `invited_at is
// null`, so each invitation sends one email whoever runs it, however
// often. It reports false when the user was not waiting (already invited,
// or never on the list). The caller holds the waitlist lock, or accepts
// that a seat may be promised twice (repose-admin does: an operator
// letting someone in ahead of the queue means it).
func Invite(ctx context.Context, pool *db.Pool, userID uuid.UUID, by string, now time.Time) (bool, error) {
	invited := false
	err := db.InTx(ctx, pool, func(tx db.Tx) error {
		return inviteTx(ctx, tx, userID, by, now, &invited)
	})
	return invited, err
}

func inviteTx(ctx context.Context, tx db.Tx, userID uuid.UUID, by string, now time.Time, invited *bool) error {
	until := now.Add(HoldDuration)
	// The invitation clears a lapse: an operator admitting a lapsed user
	// by handle (I-294 (5)) is the one way past it besides the user
	// asking (I-1033).
	tag, err := tx.Exec(ctx, "update waitlist set invited_at = $2, hold_until = $3, invited_by = $4, lapsed_at = null, cooldown_until = null where user_id = $1 and invited_at is null", userID, now, until, by)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	*invited = true
	_, err = events.InsertAccount(ctx, tx, userID, now, KindInvited, InvitedPayload{HoldUntil: until.UTC()})
	return err
}

// Inviter is the minute tick: expiries first, then invitations while a
// seat is free. The caller holds db.LockWaitlistTick (TryLock) so one
// replica runs it; the transaction lock inside (LockWaitlist) keeps
// Reserve out meanwhile. The caller must not hold LockWaitlist: its
// session lock blocks these transactions, which run on other
// connections, for good (I-357).
type Inviter struct {
	Pool  *db.Pool
	Total int
	M     *metrics.M
	Log   *slog.Logger
}

// Run expires the holds that ran out, then invites the oldest waiting
// users while Free > 0, strictly in order and one seat each. It returns
// how many it invited and how many holds it expired.
func (a *Inviter) Run(ctx context.Context, now time.Time) (invited, expired int, err error) {
	expired, err = a.expire(ctx, now)
	if err != nil {
		return 0, expired, err
	}
	for {
		var more bool
		err = db.InTx(ctx, a.Pool, func(tx db.Tx) error {
			if err := lock(ctx, tx); err != nil {
				return err
			}
			c, _, err := count(ctx, tx, a.Total, now)
			if err != nil {
				return err
			}
			if a.M != nil {
				a.M.SeatsTotal.Set(float64(c.Total))
				a.M.SeatsHeld.Set(float64(c.Held))
				a.M.WaitlistWaiting.Set(float64(c.Waiting))
			}
			if c.Free <= 0 || c.Waiting == 0 {
				return nil
			}
			waiting, err := store.ListWaiting(ctx, tx, now)
			if err != nil || len(waiting) == 0 {
				return err
			}
			var ok bool
			if err := inviteTx(ctx, tx, waiting[0].UserID, "auto", now, &ok); err != nil {
				return err
			}
			more = ok
			return nil
		})
		if err != nil || !more {
			return invited, expired, err
		}
		invited++
		if a.M != nil {
			a.M.WaitlistInvitedTotal.Inc()
		}
	}
}

// expire lapses every unconverted hold that ran out, one transaction
// each, with the email saying how to ask again (I-1033). A lapsed user is
// off the list until they join again: re-queueing them, at once (I-290)
// or after a cooldown (I-902), offered the seat back by itself with one
// or two people waiting, for as long as nobody took it.
func (a *Inviter) expire(ctx context.Context, now time.Time) (int, error) {
	rows, err := a.Pool.Query(ctx, "select user_id from waitlist where invited_at is not null and converted_at is null and hold_until <= $1 order by hold_until", now)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		err := db.InTx(ctx, a.Pool, func(tx db.Tx) error {
			if err := lock(ctx, tx); err != nil {
				return err
			}
			// Guarded again under the lock: a checkout that converted the
			// user meanwhile keeps its row.
			// joined_at and cooldown_until as I-902 wrote them, for an
			// api on the old code (legacyCooldown); lapsed_at is what
			// this code reads.
			tag, err := tx.Exec(ctx, `update waitlist set invited_at = null, hold_until = null, invited_by = null, expired_invites = expired_invites + 1,
				lapsed_at = $2, joined_at = $2, cooldown_until = $3
				where user_id = $1 and invited_at is not null and converted_at is null and hold_until <= $2`, id, now, now.Add(legacyCooldown))
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			if _, err := events.InsertAccount(ctx, tx, id, now, KindExpired, ExpiredPayload{}); err != nil {
				return err
			}
			n++
			return nil
		})
		if err != nil {
			return n, err
		}
	}
	if n > 0 && a.M != nil {
		a.M.WaitlistExpiredTotal.Add(float64(n))
	}
	return n, nil
}
