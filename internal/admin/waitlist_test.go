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
