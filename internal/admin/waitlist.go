package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/db"
)

// waitlistCmd is `repose-admin waitlist list` and `waitlist admit HANDLE |
// --next N` (DECISIONS I-269, I-290). The api invites the queue by itself
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
			if r.Position > 0 {
				pos = strconv.Itoa(r.Position)
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
			waiting, err := store.ListWaiting(ctx, e.pool)
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
	return fmt.Errorf("%w: waitlist list | waitlist admit HANDLE | waitlist admit --next N", ErrUsage)
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
