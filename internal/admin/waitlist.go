package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/db"
)

// waitlistCmd is `repose-admin waitlist list`, `waitlist stale` and
// `waitlist admit HANDLE | --next N` (DECISIONS I-269, I-290, I-1092). The api invites the queue by itself
// as seats free up; admit is for inviting someone ahead of that (a tester,
// a partner) or the next N by hand after a host is added. Admit now means
// invite: the user gets the invitation email and a 72-hour seat hold, and
// the audit kind stays waitlist_admit.
func (e *Env) waitlistCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		rows, err := store.ListWaitlist(ctx, e.pool)
		if err != nil {
			return err
		}
		now := time.Now()
		out := [][]string{{"POSITION", "HANDLE", "JOINED", "INVITED", "HOLD", "CONVERTED", "EXPIRED", "BY"}}
		for _, r := range rows {
			pos := "-"
			switch {
			case r.Position > 0:
				pos = strconv.Itoa(r.Position)
			case r.LapsedAt != nil && r.ConvertedAt == nil:
				pos = "lapsed"
			}
			by := ""
			if r.InvitedBy != nil {
				by = *r.InvitedBy
			}
			hold := fmtTime(r.HoldUntil)
			if r.HoldUntil != nil && r.ConvertedAt == nil && !r.HoldUntil.After(now) {
				hold += " (ran out)"
			}
			out = append(out, []string{pos, r.Handle, fmtTime(&r.JoinedAt), fmtTime(r.InvitedAt), hold, fmtTime(r.ConvertedAt), strconv.Itoa(r.ExpiredInvites), orDash(by)})
		}
		e.table(out)
		return nil
	case "stale":
		return e.waitlistStale(ctx, args[1:])
	case "admit":
		var nextFlag *int
		fs, err := flagsFor("waitlist admit", args[1:], func(fs *flag.FlagSet) {
			nextFlag = fs.Int("next", 0, "invite the next N waiting users")
		})
		if err != nil {
			return err
		}
		next := *nextFlag
		if (next > 0) == (fs.NArg() == 1) || fs.NArg() > 1 || next < 0 {
			return fmt.Errorf("%w: waitlist admit HANDLE | waitlist admit --next N", ErrUsage)
		}
		var handles []string
		if next > 0 {
			waiting, err := store.ListWaiting(ctx, e.pool, time.Now())
			if err != nil {
				return err
			}
			for i := 0; i < next && i < len(waiting); i++ {
				handles = append(handles, waiting[i].Handle)
			}
			if len(handles) == 0 {
				_, _ = fmt.Fprintln(e.Stdout, "nobody is waiting") // stdout
				return nil
			}
		} else {
			handles = []string{fs.Arg(0)}
		}
		for _, h := range handles {
			u, err := e.findUser(ctx, h)
			if err != nil {
				return err
			}
			ok, err := waitlist.Invite(ctx, e.pool, u.ID, e.Actor, time.Now())
			if err != nil {
				return err
			}
			if !ok {
				cur, err := store.GetWaitlistEntry(ctx, e.pool, u.ID)
				switch {
				case errors.Is(err, db.ErrNotFound):
					_, _ = fmt.Fprintf(e.Stdout, "%s is not on the waitlist\n", u.Handle) // stdout
				case err != nil:
					return err
				default:
					_, _ = fmt.Fprintf(e.Stdout, "%s was already invited at %s\n", u.Handle, fmtTime(cur.InvitedAt)) // stdout
				}
				continue
			}
			if _, err := e.audited(ctx, "waitlist_admit", u.Handle, nil); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.Stdout, "%s invited; a seat is held 72 hours and the email goes out from the api's outbox\n", u.Handle) // stdout
		}
		return nil
	}
	return fmt.Errorf("%w: waitlist list | waitlist stale [--before TIME] [--lapse] | waitlist admit HANDLE | waitlist admit --next N", ErrUsage)
}

// getFixed is when v0.1.44 reached prod, the release where GET /billing
// stopped joining the waitlist (I-1033). A waiting row older than this
// may have come from a page view rather than a choice, which is what
// `waitlist stale` lists (I-1092).
const getFixed = "2026-11-06T02:00:00Z"

// waitlistStale is `repose-admin waitlist stale [--before TIME]
// [--lapse]` (I-1092): the waiting rows that joined before TIME, which
// defaults to the deploy that fixed the joining GET. It reads by
// default; --lapse sets lapsed_at on exactly the rows it listed, which
// takes them out of the queue with no email, as the invite tick's
// expiry would send one. A user whose row this lapses comes back with
// one click on /billing, at the back of the list (I-1033).
func (e *Env) waitlistStale(ctx context.Context, args []string) error {
	var beforeFlag *string
	var lapseFlag *bool
	fs, err := flagsFor("waitlist stale", args, func(fs *flag.FlagSet) {
		beforeFlag = fs.String("before", getFixed, "list rows that joined before this RFC3339 time")
		lapseFlag = fs.Bool("lapse", false, "lapse the rows listed, with no email")
	})
	if err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: waitlist stale [--before TIME] [--lapse]", ErrUsage)
	}
	before, err := time.Parse(time.RFC3339, *beforeFlag)
	if err != nil {
		return fmt.Errorf("--before must be an RFC3339 time: %w", err)
	}
	type row struct {
		id         uuid.UUID
		handle     string
		joined     time.Time
		status     string
		subscribed bool
	}
	rows, err := e.pool.Query(ctx, `select w.user_id, u.handle, w.joined_at, u.billing_status,
		exists (select 1 from subscriptions s where s.user_id = w.user_id)
		from waitlist w join users u on u.id = w.user_id
		where w.invited_at is null and w.converted_at is null and w.lapsed_at is null and w.joined_at < $1
		order by w.joined_at`, before.UTC())
	if err != nil {
		return err
	}
	var stale []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.handle, &r.joined, &r.status, &r.subscribed); err != nil {
			rows.Close()
			return err
		}
		stale = append(stale, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(stale) == 0 {
		_, _ = fmt.Fprintf(e.Stdout, "no waiting row joined before %s\n", before.UTC().Format(time.RFC3339)) // stdout
		return nil
	}
	out := [][]string{{"HANDLE", "JOINED", "BILLING", "SUBSCRIBED"}}
	for _, r := range stale {
		out = append(out, []string{r.handle, fmtTime(&r.joined), r.status, strconv.FormatBool(r.subscribed)})
	}
	e.table(out)
	if !*lapseFlag {
		_, _ = fmt.Fprintf(e.Stdout, "%d waiting rows joined before %s\n", len(stale), before.UTC().Format(time.RFC3339)) // stdout
		return nil
	}
	now := time.Now().UTC()
	var lapsed []string
	err = db.InTx(ctx, e.pool, func(tx db.Tx) error {
		// The same advisory lock Reserve, Join and the tick take: a row
		// invited between the listing and here keeps its invitation,
		// since the update insists it is still waiting.
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", db.LockWaitlist); err != nil {
			return err
		}
		lapsed = nil
		for _, r := range stale {
			tag, err := tx.Exec(ctx, `update waitlist set lapsed_at = $2
				where user_id = $1 and invited_at is null and converted_at is null and lapsed_at is null`, r.id, now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				lapsed = append(lapsed, r.handle)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, h := range lapsed {
		if _, err := e.audited(ctx, "waitlist_lapse", h, nil); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(e.Stdout, "%d lapsed, no email sent\n", len(lapsed)) // stdout
	return nil
}

// seatsCmd is `repose-admin seats`: the fleet's seat count as GET
// /public/seats and the invite tick see it (DECISIONS I-290), and where
// the total came from -- for a derived total, the two ceilings it is the
// smaller of per host, which is what tells an operator whether the next
// host should bring memory or cores (I-739). SEATS_TOTAL in this
// process's environment is what the api would use; with none set the
// count comes from the hosts.
func (e *Env) seatsCmd(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%w: seats takes no arguments", ErrUsage)
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	total := 0
	if v := os.Getenv("SEATS_TOTAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return errors.New("SEATS_TOTAL must be a whole number")
		}
		total = n
	}
	svc := &waitlist.Service{Pool: e.pool, Total: total}
	c, src, d, err := svc.CountSource(ctx)
	if err != nil {
		return err
	}
	hosts := "hosts"
	if d.Hosts == 1 {
		hosts = "host"
	}
	from := fmt.Sprintf("%d ready, undrained %s: memory allows %d, cores allow %d", d.Hosts, hosts, d.Memory, d.CPU)
	if d.NoCores > 0 {
		// Without cores there is no CPU ceiling to report, and the
		// number above counted such a host at its memory ceiling: say so
		// rather than let it read as a CPU figure (I-735, I-739).
		from = fmt.Sprintf("%s (%d reported no cores, counted by memory)", from, d.NoCores)
	}
	if src == waitlist.SourceConfig {
		from = "SEATS_TOTAL"
	}
	e.table([][]string{
		{"TOTAL", "HELD", "FREE", "WAITING"},
		{strconv.Itoa(c.Total), strconv.Itoa(c.Held), strconv.Itoa(c.Free), strconv.Itoa(c.Waiting)},
	})
	_, _ = fmt.Fprintf(e.Stdout, "total from %s\n", from) // stdout
	return nil
}
