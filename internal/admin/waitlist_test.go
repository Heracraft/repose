package admin_test

import (
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/apitest"
)

// DECISIONS I-269, I-290: `waitlist list` shows the queue with the hold
// columns, `waitlist admit` invites one user or the next N (a 72-hour seat
// hold and one email each), audited as waitlist_admit; `seats` prints the
// count and where the total came from.
func TestWaitlistCommands(t *testing.T) {
	h := apitest.New(t, apitest.Options{NoHost: true})
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	now := time.Now()
	for i, handle := range []string{"ann", "bob", "cyd"} {
		u := h.NewUser(handle)
		if _, err := h.Pool.Exec(h.Ctx, "insert into waitlist (user_id, joined_at) values ($1, $2)", u.ID, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, e, "waitlist", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "1 ") || !strings.Contains(lines[1], "ann") || !strings.Contains(lines[3], "cyd") || !strings.Contains(lines[0], "HOLD") || !strings.Contains(lines[0], "EXPIRED") {
		t.Fatalf("list:\n%s", out)
	}
	if out, err := run(t, e, "waitlist", "admit", "bob"); err != nil || !strings.Contains(out, "bob invited") {
		t.Fatalf("admit bob: %s %v", out, err)
	}
	if out, err := run(t, e, "waitlist", "admit", "bob"); err != nil || !strings.Contains(out, "bob was already invited") {
		t.Fatalf("admit bob again: %s %v", out, err)
	}
	if out, err := run(t, e, "waitlist", "admit", "--next", "5"); err != nil || !strings.Contains(out, "ann invited") || !strings.Contains(out, "cyd invited") {
		t.Fatalf("admit --next: %s %v", out, err)
	}
	if out, err := run(t, e, "waitlist", "admit", "--next", "1"); err != nil || !strings.Contains(out, "nobody is waiting") {
		t.Fatalf("admit --next on an empty queue: %s %v", out, err)
	}
	if _, err := run(t, e, "waitlist", "admit"); err == nil {
		t.Fatal("admit with neither a handle nor --next was accepted")
	}
	if _, err := run(t, e, "waitlist", "admit", "ann", "--next", "1"); err == nil {
		t.Fatal("admit with both a handle and --next was accepted")
	}
	var audits, emails int
	if err := h.Pool.QueryRow(h.Ctx, "select (select count(*) from audit_log where action = 'waitlist_admit' and actor = 'admin:test'), (select count(*) from events_outbox o join events ev on ev.id = o.event_id where ev.kind = 'waitlist_invited')").Scan(&audits, &emails); err != nil {
		t.Fatal(err)
	}
	if audits != 3 || emails != 3 {
		t.Fatalf("audits %d emails %d, want 3 and 3", audits, emails)
	}
	var holds int
	if err := h.Pool.QueryRow(h.Ctx, "select count(*) from waitlist where invited_at is not null and hold_until = invited_at + interval '72 hours' and converted_at is null").Scan(&holds); err != nil || holds != 3 {
		t.Fatalf("holds %d %v", holds, err)
	}
	out, _ = run(t, e, "waitlist", "list")
	if !strings.Contains(out, "admin:test") || strings.Contains(out, "\n1 ") {
		t.Fatalf("list after invitation:\n%s", out)
	}
	// seats: no ready host in this harness, so the total is 0 from the
	// hosts, both ceilings are 0 with it (I-739) and the three holds are
	// held; with SEATS_TOTAL the source and the free count change.
	out, err = run(t, e, "seats")
	if err != nil || !strings.Contains(out, "TOTAL") || !strings.Contains(out, "\n0 ") || !strings.Contains(out, "total from 0 ready, undrained hosts: memory allows 0, cores allow 0") {
		t.Fatalf("seats:\n%s %v", out, err)
	}
	t.Setenv("SEATS_TOTAL", "30")
	out, err = run(t, e, "seats")
	if err != nil || !strings.Contains(out, "\n30 ") || !strings.Contains(out, " 27 ") || !strings.Contains(out, "total from SEATS_TOTAL") {
		t.Fatalf("seats from config:\n%s %v", out, err)
	}
	if _, err := run(t, e, "seats", "extra"); err == nil {
		t.Fatal("seats with an argument was accepted")
	}
}

// I-1092: `waitlist stale` lists the waiting rows that joined before the
// release where GET /billing stopped joining, reads by default, and with
// --lapse takes exactly those rows out of the queue with no email.
func TestWaitlistStale(t *testing.T) {
	h := apitest.New(t, apitest.Options{NoHost: true})
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	cut := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	old := h.NewUser("olga")
	oldSub := h.NewUser("oona")
	recent := h.NewUser("rita")
	invited := h.NewUser("ivan")
	for _, c := range []struct {
		id     any
		joined time.Time
	}{{old.ID, cut.Add(-48 * time.Hour)}, {oldSub.ID, cut.Add(-24 * time.Hour)}, {recent.ID, cut.Add(time.Hour)}, {invited.ID, cut.Add(-72 * time.Hour)}} {
		if _, err := h.Pool.Exec(h.Ctx, "insert into waitlist (user_id, joined_at) values ($1, $2)", c.id, c.joined); err != nil {
			t.Fatal(err)
		}
	}
	// Ivan was invited after joining, so his row is not waiting; Oona
	// subscribed since, which the listing shows and does not act on.
	if _, err := h.Pool.Exec(h.Ctx, "update waitlist set invited_at = $2::timestamptz, hold_until = $2::timestamptz + interval '72 hours' where user_id = $1", invited.ID, cut); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "insert into subscriptions (id, user_id, customer_id, plan, status, seats) values ('sub_oona', $1, 'ctm_oona', 'solo', 'active', 1)", oldSub.ID); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, e, "waitlist", "stale")
	if err != nil || !strings.Contains(out, "olga") || !strings.Contains(out, "oona") || !strings.Contains(out, "2 waiting rows joined before 2026-11-06T02:00:00Z") {
		t.Fatalf("stale:\n%s %v", out, err)
	}
	if strings.Contains(out, "rita") || strings.Contains(out, "ivan") {
		t.Fatalf("stale listed a recent or an invited row:\n%s", out)
	}
	if !strings.Contains(out, "true") || !strings.Contains(out, "false") {
		t.Fatalf("stale did not say who ever subscribed:\n%s", out)
	}
	var waiting int
	if err := h.Pool.QueryRow(h.Ctx, "select count(*) from waitlist where lapsed_at is null").Scan(&waiting); err != nil || waiting != 4 {
		t.Fatalf("the listing wrote: %d %v", waiting, err)
	}
	// A different cut covers Ivan's join time but not his row, which is
	// no longer waiting.
	if out, err := run(t, e, "waitlist", "stale", "--before", "2026-11-03T00:00:00Z"); err != nil || !strings.Contains(out, "no waiting row joined before 2026-11-03T00:00:00Z") {
		t.Fatalf("an earlier cut:\n%s %v", out, err)
	}
	if _, err := run(t, e, "waitlist", "stale", "--before", "yesterday"); err == nil {
		t.Fatal("a cut that is not a time was accepted")
	}
	if _, err := run(t, e, "waitlist", "stale", "extra"); err == nil {
		t.Fatal("stale with an argument was accepted")
	}

	out, err = run(t, e, "waitlist", "stale", "--lapse")
	if err != nil || !strings.Contains(out, "2 lapsed, no email sent") {
		t.Fatalf("stale --lapse:\n%s %v", out, err)
	}
	var lapsed, audits, events int
	if err := h.Pool.QueryRow(h.Ctx, `select (select count(*) from waitlist where lapsed_at is not null),
		(select count(*) from audit_log where action = 'waitlist_lapse' and actor = 'admin:test'),
		(select count(*) from events)`).Scan(&lapsed, &audits, &events); err != nil {
		t.Fatal(err)
	}
	if lapsed != 2 || audits != 2 || events != 0 {
		t.Fatalf("after --lapse: lapsed %d audited %d events %d", lapsed, audits, events)
	}
	if out, err := run(t, e, "waitlist", "stale"); err != nil || !strings.Contains(out, "no waiting row joined before") {
		t.Fatalf("stale again:\n%s %v", out, err)
	}
	// Rita keeps her place and Ivan his invitation.
	out, err = run(t, e, "waitlist", "list")
	if err != nil || !strings.Contains(out, "lapsed    olga") || !strings.Contains(out, "lapsed    oona") || !strings.Contains(out, "1         rita") {
		t.Fatalf("list after --lapse:\n%s %v", out, err)
	}
}
