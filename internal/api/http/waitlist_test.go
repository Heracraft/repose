package httpapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
)

// DECISIONS I-290: seats are what the fleet's hosts deliver (or
// SEATS_TOTAL), the smaller of memory and cores per host since I-739,
// held by live subscriptions and unexpired invitations. Reserve says
// whether a checkout may proceed and otherwise puts the user on the list;
// POST /billing/waitlist joins it idempotently; GET /public/seats counts
// it; the minute tick invites oldest first, one seat each, strictly in
// order, one email each however often it runs; a hold that runs out
// lapses the user off the list (I-1033); POST /projects no longer gates.
func TestSeatsWaitlistAndInvitations(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	var svc *waitlist.Service
	e := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		svc = &waitlist.Service{Pool: d.Pool, M: d.Metrics, Log: d.Log, Now: func() time.Time { return clock }}
		d.Seats = svc
	})
	ctx := e.h.Ctx
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.h.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := e.h.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// The harness host is all reserve (8 GB); a second host of 24 GB has
	// two 8 GB blocks after its reserve, and 32 physical cores to serve
	// them, so memory is what bounds the fleet: it has two seats.
	exec("update hosts set mem_bytes = $2, cores = 32 where id = $1", e.h.HostID, int64(8)<<30)
	exec("insert into hosts (id, name, state, mem_bytes, cores) values ($1, 'host-wl', 'ready', $2, 32)", store.NewID(), int64(24)<<30)
	filler := e.h.NewUser("filler")
	exec("insert into subscriptions (id, user_id, customer_id, plan, status, seats) values ('sub_filler', $1, 'ctm_filler', 'plus', 'active', 2)", filler.ID)

	c, err := svc.Count(ctx)
	if err != nil || c != (waitlist.Count{Total: 2, Held: 2, Free: 0, Waiting: 0}) {
		t.Fatalf("count %+v %v", c, err)
	}

	// B signs in (the welcome email is queued with the row) and joins.
	// signIn gives every test user a Plus plan so compute works; B and C
	// are here for a seat, so theirs go.
	tokB := e.signIn(t, "sub-wl-b", "wlb")
	e.subscribe(t, "sub-wl-b", "")
	idB := uuid.MustParse(e.do(t, tokB, "GET", "/me", nil).body["id"].(string))
	if n := count("select count(*) from events ev join events_outbox o on o.event_id = ev.id where ev.user_id = $1 and ev.kind = 'welcome' and o.channel = 'email'", idB); n != 1 {
		t.Fatalf("welcome events queued: %d", n)
	}
	r := e.do(t, tokB, "POST", "/billing/waitlist", nil)
	if r.status != 200 || r.body["position"] != float64(1) {
		t.Fatalf("join: %d %s", r.status, r.raw)
	}
	joined := r.body["joined_at"]
	// Idempotent: the same place and join time.
	r = e.do(t, tokB, "POST", "/billing/waitlist", nil)
	if r.status != 200 || r.body["position"] != float64(1) || r.body["joined_at"] != joined {
		t.Fatalf("rejoin: %d %s", r.status, r.raw)
	}
	me := e.do(t, tokB, "GET", "/me", nil)
	wl, _ := me.body["waitlist"].(map[string]any)
	if wl["position"] != float64(1) || wl["invited_at"] != nil || wl["hold_until"] != nil {
		t.Fatalf("me: %s", me.raw)
	}
	if n := count("select count(*) from events where user_id = $1 and kind = 'waitlist_joined'", idB); n != 1 {
		t.Fatalf("joined events %d, want 1", n)
	}
	if !strings.Contains(e.logs.String(), `"event":"waitlist_join"`) || strings.Contains(e.logs.String(), "wlb@example.com") {
		t.Fatalf("join log: %s", e.logs.String())
	}

	// C's checkout asks Reserve: no seat, so C is on the list behind B.
	tokC := e.signIn(t, "sub-wl-c", "wlc")
	e.subscribe(t, "sub-wl-c", "")
	idC := uuid.MustParse(e.do(t, tokC, "GET", "/me", nil).body["id"].(string))
	ok, place, err := svc.Reserve(ctx, idC.String(), 1)
	if err != nil || ok || place == nil || place.Position != 2 || place.Email != "wlc@example.com" {
		t.Fatalf("reserve C: ok=%v place=%+v err=%v", ok, place, err)
	}
	if msg := waitlist.Message(place.Position, place.Email); msg != "repose is full right now. You're number 2 on the waitlist; we'll email wlc@example.com when there's a seat." {
		t.Fatalf("message %q", msg)
	}

	// The public count, no token, cached.
	r = e.do(t, "", "GET", "/public/seats", nil)
	if r.status != 200 || r.body["total"] != float64(2) || r.body["free"] != float64(0) || r.body["waiting"] != float64(2) {
		t.Fatalf("public seats: %d %s", r.status, r.raw)
	}
	// Without billing there is no founding offer: founding_left is null
	// (DECISIONS I-672).
	if left, has := r.body["founding_left"]; !has || left != nil {
		t.Fatalf("public seats without billing: %s", r.raw)
	}

	// POST /projects is not the waitlist's gate any more: B, with no plan,
	// is refused compute with subscription_required (not 503 waitlisted),
	// and the detail carries B's place for the CLI's sentence.
	r = e.do(t, tokB, "POST", "/projects", map[string]any{"name": "first", "class": "small"})
	detail, _ := r.body["error"].(map[string]any)["detail"].(map[string]any)
	wlDetail, _ := detail["waitlist"].(map[string]any)
	if r.status != 402 || r.body["error"].(map[string]any)["code"] != "payment_required" || detail["reason"] != "subscription_required" || wlDetail["position"] != float64(1) {
		t.Fatalf("create without a plan while the fleet is full: %d %s", r.status, r.raw)
	}

	// Full: the tick invites nobody. Run as the api's loop runs it, under
	// the tick's lock; under LockWaitlist it waited on itself for good
	// (I-357), so a bounded context turns that hang into a failure.
	inv := &waitlist.Inviter{Pool: e.h.Pool, M: e.h.Metrics, Log: e.h.Log}
	release, ok, err := db.TryLock(ctx, e.h.Pool, db.LockWaitlistTick)
	if err != nil || !ok {
		t.Fatalf("tick lock: %v %v", ok, err)
	}
	tickCtx, cancelTick := context.WithTimeout(ctx, 10*time.Second)
	n, x, err := inv.Run(tickCtx, clock)
	cancelTick()
	release()
	if err != nil || n != 0 || x != 0 {
		t.Fatalf("full fleet invited %d expired %d %v", n, x, err)
	}
	// One seat frees: B is invited and holds it; C waits, first in line.
	exec("update subscriptions set seats = 1 where id = 'sub_filler'")
	clock = clock.Add(time.Minute)
	if n, _, err := inv.Run(ctx, clock); err != nil || n != 1 {
		t.Fatalf("invited %d %v, want 1", n, err)
	}
	b, err := store.GetWaitlistEntry(ctx, e.h.Pool, idB)
	if err != nil || b.InvitedAt == nil || b.InvitedBy == nil || *b.InvitedBy != "auto" || b.HoldUntil == nil || !b.HoldUntil.Equal(clock.Add(72*time.Hour)) || b.Position != 0 {
		t.Fatalf("B not invited: %+v %v", b, err)
	}
	cEntry, err := store.GetWaitlistEntry(ctx, e.h.Pool, idC)
	if err != nil || cEntry.InvitedAt != nil || cEntry.Position != 1 {
		t.Fatalf("C: %+v %v", cEntry, err)
	}
	c, _ = svc.Count(ctx)
	if c != (waitlist.Count{Total: 2, Held: 2, Free: 0, Waiting: 1}) {
		t.Fatalf("count after invite %+v", c)
	}
	me = e.do(t, tokB, "GET", "/me", nil)
	wl, _ = me.body["waitlist"].(map[string]any)
	if wl == nil || wl["invited_at"] == nil || wl["hold_until"] == nil || wl["position"] != float64(0) {
		t.Fatalf("me while holding: %s", me.raw)
	}
	// Again, from a second inviter (another replica, a restart), and by
	// hand: nothing more, one email.
	if n, _, err := inv.Run(ctx, clock.Add(time.Minute)); err != nil || n != 0 {
		t.Fatalf("rerun invited %d %v", n, err)
	}
	if n, _, err := (&waitlist.Inviter{Pool: e.h.Pool}).Run(ctx, clock.Add(2*time.Minute)); err != nil || n != 0 {
		t.Fatalf("second inviter invited %d %v", n, err)
	}
	if ok, err := waitlist.Invite(ctx, e.h.Pool, idB, "admin:test", clock); err != nil || ok {
		t.Fatalf("re-invite: %v %v", ok, err)
	}
	if n := count("select count(*) from events ev join events_outbox o on o.event_id = ev.id where ev.user_id = $1 and ev.kind = 'waitlist_invited'", idB); n != 1 {
		t.Fatalf("invited events queued %d, want 1", n)
	}
	var summary string
	_ = e.h.Pool.QueryRow(ctx, "select summary from events where user_id = $1 and kind = 'waitlist_invited'", idB).Scan(&summary)
	if !strings.Contains(summary, `"hold_until":"`+clock.Add(72*time.Hour).UTC().Format(time.RFC3339)) {
		t.Fatalf("invited payload %q", summary)
	}

	// B's own hold counts toward B's checkout: Solo (1 seat) may proceed,
	// Plus (2) may not, and B keeps the hold rather than rejoining.
	if ok, _, err := svc.Reserve(ctx, idB.String(), 1); err != nil || !ok {
		t.Fatalf("reserve B solo: %v %v", ok, err)
	}
	ok, place, err = svc.Reserve(ctx, idB.String(), 2)
	if err != nil || ok || place == nil || place.InvitedAt == nil || place.Position != 0 {
		t.Fatalf("reserve B plus: ok=%v place=%+v err=%v", ok, place, err)
	}
	// B's subscription arrives: converted, the row stays, the seat is the
	// subscription's now.
	exec("insert into subscriptions (id, user_id, customer_id, plan, status, seats) values ('sub_b', $1, 'ctm_b', 'solo', 'trialing', 1)", idB)
	if err := svc.Converted(ctx, idB.String()); err != nil {
		t.Fatal(err)
	}
	b, _ = store.GetWaitlistEntry(ctx, e.h.Pool, idB)
	if b.ConvertedAt == nil || b.Holding(clock) {
		t.Fatalf("B after conversion: %+v", b)
	}
	c, _ = svc.Count(ctx)
	if c != (waitlist.Count{Total: 2, Held: 2, Free: 0, Waiting: 1}) {
		t.Fatalf("count after conversion %+v", c)
	}
	if err := svc.Converted(ctx, filler.ID.String()); err != nil {
		t.Fatalf("converted for a user never on the list: %v", err)
	}

	// The emails go out through the outbox with notify_email off and no
	// unsubscribe link: welcome, joined and invited for B.
	exec("update users set notify_email = false where id = $1", idB)
	var got []notify.Message
	sender := notify.SenderFunc(func(_ context.Context, m notify.Message) error {
		if m.Email == "wlb@example.com" {
			got = append(got, m)
		}
		return nil
	})
	o := notify.New(e.h.Pool, map[string]notify.Sender{"email": sender}, e.h.Metrics, e.h.Log)
	o.Unsub = e.unsub
	if _, err := o.Once(ctx); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, m := range got {
		kinds[m.Kind] = true
		if m.Project != "" || m.Unsubscribe != "" {
			t.Fatalf("account mail with a project or unsubscribe link: %+v", m)
		}
	}
	if len(got) != 3 || !kinds["welcome"] || !kinds["waitlist_joined"] || !kinds["waitlist_invited"] {
		t.Fatalf("sent %+v", got)
	}
	if n, _ := o.Once(ctx); n != 0 {
		t.Fatalf("outbox resent %d", n)
	}

	// The filler cancels: a seat frees, C is invited. D joins an hour
	// later, behind the hold.
	exec("update subscriptions set status = 'canceled' where id = 'sub_filler'")
	clock = clock.Add(time.Minute)
	if n, _, err := inv.Run(ctx, clock); err != nil || n != 1 {
		t.Fatalf("C invited %d %v", n, err)
	}
	invitedC := clock
	clock = clock.Add(time.Hour)
	tokD := e.signIn(t, "sub-wl-d", "wld")
	e.subscribe(t, "sub-wl-d", "")
	idD := uuid.MustParse(e.do(t, tokD, "GET", "/me", nil).body["id"].(string))
	if r := e.do(t, tokD, "POST", "/billing/waitlist", nil); r.status != 200 || r.body["position"] != float64(1) {
		t.Fatalf("D joins: %d %s", r.status, r.raw)
	}
	// Nothing free while C holds: D waits.
	if n, x, err := inv.Run(ctx, clock); err != nil || n != 0 || x != 0 {
		t.Fatalf("while C holds: invited %d expired %d %v", n, x, err)
	}
	// 72 hours pass: C's hold runs out, C lapses off the list with the
	// expiry email saying so (I-1033), and the freed seat goes to D.
	clock = invitedC.Add(72*time.Hour + time.Minute)
	n, x, err = inv.Run(ctx, clock)
	if err != nil || x != 1 || n != 1 {
		t.Fatalf("expiry tick: invited %d expired %d %v", n, x, err)
	}
	cEntry, _ = store.GetWaitlistEntry(ctx, e.h.Pool, idC)
	if cEntry.InvitedAt != nil || cEntry.HoldUntil != nil || cEntry.ExpiredInvites != 1 || cEntry.LapsedAt == nil || !cEntry.LapsedAt.Equal(clock) || cEntry.Position != 0 {
		t.Fatalf("C after expiry: %+v", cEntry)
	}
	d, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idD)
	if d.InvitedAt == nil || d.Position != 0 {
		t.Fatalf("D not invited after C's expiry: %+v", d)
	}
	_ = e.h.Pool.QueryRow(ctx, "select summary from events where user_id = $1 and kind = 'waitlist_expired'", idC).Scan(&summary)
	if summary != `{}` {
		t.Fatalf("expired payload %q (a lapsed user has no place to name)", summary)
	}
	if n := count("select count(*) from events_outbox o join events ev on ev.id = o.event_id where ev.kind = 'waitlist_expired' and ev.user_id = $1", idC); n != 1 {
		t.Fatalf("expired email queued %d", n)
	}
	// Strict order and SEATS_TOTAL: with the total set to ten, everyone
	// waiting is invited, and no one twice. C lapsed, so C is not a
	// candidate however many seats are free, before or after the hour
	// I-902's cooldown would have ended (D's hold still runs).
	big := &waitlist.Inviter{Pool: e.h.Pool, Total: 10}
	for _, after := range []time.Duration{time.Minute, 25 * time.Hour} {
		if n, _, err := big.Run(ctx, clock.Add(after)); err != nil || n != 0 {
			t.Fatalf("SEATS_TOTAL=10 %s after C's expiry invited %d %v, want none", after, n, err)
		}
	}
	if ws, err := store.ListWaiting(ctx, e.h.Pool, clock); err != nil || len(ws) != 0 {
		t.Fatalf("still waiting: %+v %v", ws, err)
	}
	c, src, der, err := (&waitlist.Service{Pool: e.h.Pool, Total: 10, Now: func() time.Time { return clock.Add(time.Minute) }}).CountSource(ctx)
	// Held: B's subscription and D's hold. A total that came from
	// SEATS_TOTAL was derived from nothing, so it carries no ceilings.
	if err != nil || src != waitlist.SourceConfig || c != (waitlist.Count{Total: 10, Held: 2, Free: 8, Waiting: 0}) || der != (waitlist.Derivation{}) {
		t.Fatalf("count from config %+v %s %+v %v", c, src, der, err)
	}
	// The CPU ceiling binds below the memory ceiling (I-739): 64 GB is
	// seven 8 GB blocks, but eight physical cores serve four busy seats
	// at two vCPUs each (I-784), so the fleet is four seats and
	// `repose-admin seats` has both figures.
	exec("update hosts set mem_bytes = 64::bigint<<30, cores = 8 where name = 'host-wl'")
	c, src, der, err = svc.CountSource(ctx)
	// Both hosts are counted: the harness host's 32 cores could serve 24
	// seats and its memory none, which is why one enum could not say
	// which ceiling bound the fleet.
	if err != nil || src != waitlist.SourceHosts || c.Total != 4 || der != (waitlist.Derivation{Hosts: 2, Memory: 7, CPU: 28}) {
		t.Fatalf("CPU-bound host: %+v %s %+v %v", c, src, der, err)
	}
	// A seat count with no ready host at all is zero, not an error.
	exec("update hosts set state = 'unreachable'")
	if c, _ := svc.Count(ctx); c.Total != 0 || c.Free != 0 {
		t.Fatalf("no hosts: %+v", c)
	}
}

// DECISIONS I-290: an expired or converted row re-queues on a new
// checkout, so a user whose plan ended and who does not fit any more waits
// like a newcomer, at the back.
func TestWaitlistRejoinAfterConversion(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	e := newEnv(t)
	ctx := e.h.Ctx
	svc := &waitlist.Service{Pool: e.h.Pool, Total: 1, Now: func() time.Time { return clock }}
	a := e.h.NewUser("rejoin-a")
	b := e.h.NewUser("rejoin-b")
	holder := e.h.NewUser("rejoin-holder")
	if _, err := e.h.Pool.Exec(ctx, "insert into subscriptions (id, user_id, customer_id, plan, status, seats) values ('sub_hold', $1, 'ctm', 'solo', 'active', 1)", holder.ID); err != nil {
		t.Fatal(err)
	}
	if ok, p, err := svc.Reserve(ctx, a.ID.String(), 1); err != nil || ok || p.Position != 1 {
		t.Fatalf("A: %v %+v %v", ok, p, err)
	}
	if ok, err := waitlist.Invite(ctx, e.h.Pool, a.ID, "admin:test", clock); err != nil || !ok {
		t.Fatalf("invite A: %v %v", ok, err)
	}
	if err := svc.Converted(ctx, a.ID.String()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	if ok, p, err := svc.Reserve(ctx, b.ID.String(), 1); err != nil || ok || p.Position != 1 {
		t.Fatalf("B: %v %+v %v", ok, p, err)
	}
	// A's plan ended; A asks again and queues behind B.
	clock = clock.Add(time.Hour)
	ok, p, err := svc.Reserve(ctx, a.ID.String(), 1)
	if err != nil || ok || p.Position != 2 || p.InvitedAt != nil {
		t.Fatalf("A rejoin: %v %+v %v", ok, p, err)
	}
	row, _ := store.GetWaitlistEntry(ctx, e.h.Pool, a.ID)
	if row.ConvertedAt != nil || row.ExpiredInvites != 0 || !row.JoinedAt.Equal(clock) {
		t.Fatalf("A row after rejoin: %+v", row)
	}
}

// A hold that runs out unconverted ends the invitations until the user
// asks again (I-1033). I-290 put an expired user at the back of the list,
// and with one person waiting the back is the front: invited, expired and
// invited again in one minute. I-902's 24-hour cooldown only slowed it to
// two emails every four days. With two people waiting they took turns.
// Here one waiter, then two, see one invitation each across a month of
// ticks; reading /billing neither rejoins nor joins anyone; asking again
// (POST /billing/waitlist, a checkout that does not fit, an operator's
// admit) is what puts a lapsed user back, at the back.
func TestAnExpiredHoldEndsTheInvitations(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	f := newFakePolar(t)
	var svc *waitlist.Service
	e := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		svc = &waitlist.Service{Pool: d.Pool, Total: 1, M: d.Metrics, Log: d.Log, Now: func() time.Time { return clock }}
		d.Seats = svc
		d.Billing = billing.NewService(d.Pool, billing.NewPolar(f.cfg, d.Log), f.cfg, svc, billing.NewOverage(d.Pool, billing.NewPolar(f.cfg, d.Log), f.cfg, d.Engine, d.Metrics, d.Log), d.Log)
		d.Gate = billing.NewGate(d.Pool, f.cfg, d.Metrics, d.Log)
	})
	ctx := e.h.Ctx
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := e.h.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	emails := func(id uuid.UUID, kind string) int {
		t.Helper()
		return count("select count(*) from events where user_id = $1 and kind = $2", id, kind)
	}
	inv := &waitlist.Inviter{Pool: e.h.Pool, Total: 1}
	// month runs the minute tick from clock to 30 days on, hourly, and
	// answers how many it invited and expired.
	month := func() (invited, expired int) {
		t.Helper()
		end := clock.Add(30 * 24 * time.Hour)
		for ; clock.Before(end); clock = clock.Add(time.Hour) {
			n, x, err := inv.Run(ctx, clock)
			if err != nil {
				t.Fatal(err)
			}
			invited += n
			expired += x
		}
		return invited, expired
	}
	user := func(handle string) (string, uuid.UUID) {
		t.Helper()
		tok := e.signIn(t, "sub-"+handle, handle)
		e.subscribe(t, "sub-"+handle, "")
		return tok, uuid.MustParse(e.do(t, tok, "GET", "/me", nil).body["id"].(string))
	}

	// One waiter, one seat, nobody else: one invitation, one expiry, and
	// nothing more for a month.
	tokA, idA := user("lapse-a")
	if r := e.do(t, tokA, "POST", "/billing/waitlist", nil); r.status != 200 || r.body["position"] != float64(1) {
		t.Fatalf("A joins: %d %s", r.status, r.raw)
	}
	if n, x := month(); n != 1 || x != 1 {
		t.Fatalf("a month with one waiter: invited %d expired %d, want 1 and 1", n, x)
	}
	if emails(idA, "waitlist_invited") != 1 || emails(idA, "waitlist_expired") != 1 {
		t.Fatalf("A's emails: invited %d expired %d", emails(idA, "waitlist_invited"), emails(idA, "waitlist_expired"))
	}
	a, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idA)
	if a.LapsedAt == nil || a.Position != 0 || a.InvitedAt != nil || a.ExpiredInvites != 1 {
		t.Fatalf("A after the month: %+v", a)
	}
	// A lapsed user holds no place: /billing shows none, the seat they
	// left is free to buy, and nobody counts as waiting.
	r := e.do(t, tokA, "GET", "/billing", nil)
	if r.status != 200 || r.body["waitlist"] != nil {
		t.Fatalf("A's /billing while lapsed: %d %s", r.status, r.raw)
	}
	if s := r.body["seats"].(map[string]any); s["free"] != float64(1) || s["waiting"] != float64(0) {
		t.Fatalf("seats while A lapsed: %v", s)
	}
	if p := r.body["plans"].([]any)[0].(map[string]any); p["id"] != "solo" || p["available"] != true {
		t.Fatalf("solo while the one seat is free: %v", p)
	}

	// Two waiters who never answer: one invitation each, then quiet. B
	// and C join behind a holder who takes the one seat, so both wait.
	holder := e.h.NewUser("lapse-holder")
	if _, err := e.h.Pool.Exec(ctx, "insert into subscriptions (id, user_id, customer_id, plan, status, seats) values ('sub_lapse_holder', $1, 'ctm_lh', 'solo', 'active', 1)", holder.ID); err != nil {
		t.Fatal(err)
	}
	tokB, idB := user("lapse-b")
	tokC, idC := user("lapse-c")
	for _, tok := range []string{tokB, tokC} {
		if r := e.do(t, tok, "POST", "/billing/waitlist", nil); r.status != 200 {
			t.Fatalf("join: %d %s", r.status, r.raw)
		}
	}
	// Reading /billing while full joins nobody and undoes no lapse: A
	// stays lapsed, and D, who never asked, is not on the list.
	tokD, idD := user("lapse-d")
	for _, tok := range []string{tokA, tokD} {
		if r := e.do(t, tok, "GET", "/billing", nil); r.status != 200 || r.body["waitlist"] != nil {
			t.Fatalf("/billing while full: %d %s", r.status, r.raw)
		}
	}
	if _, err := store.GetWaitlistEntry(ctx, e.h.Pool, idD); err == nil {
		t.Fatal("GET /billing put D on the waitlist")
	}
	if a, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idA); a.LapsedAt == nil {
		t.Fatalf("GET /billing undid A's lapse: %+v", a)
	}
	// A lapsed user ahead in time takes no place in front of anyone.
	if r := e.do(t, tokB, "GET", "/billing", nil); r.body["waitlist"].(map[string]any)["position"] != float64(1) {
		t.Fatalf("B's place with A lapsed ahead: %s", r.raw)
	}
	if b, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idB); b.Position != 1 {
		t.Fatalf("B's store position with A lapsed ahead: %+v", b)
	}
	if _, err := e.h.Pool.Exec(ctx, "update subscriptions set status = 'canceled' where id = 'sub_lapse_holder'"); err != nil {
		t.Fatal(err)
	}
	if n, x := month(); n != 2 || x != 2 {
		t.Fatalf("a month with two waiters: invited %d expired %d, want 2 and 2", n, x)
	}
	for _, id := range []uuid.UUID{idB, idC} {
		if emails(id, "waitlist_invited") != 1 || emails(id, "waitlist_expired") != 1 {
			t.Fatalf("%s took turns: invited %d expired %d", id, emails(id, "waitlist_invited"), emails(id, "waitlist_expired"))
		}
	}
	if c, err := svc.Count(ctx); err != nil || c != (waitlist.Count{Total: 1, Held: 0, Free: 1, Waiting: 0}) {
		t.Fatalf("count with everyone lapsed: %+v %v", c, err)
	}

	// Asking again is what puts a lapsed user back, at the back, with no
	// second joined email and the expiry not counted twice. C asks first,
	// then B by a checkout that does not fit, then A from the plan page.
	clock = clock.Add(time.Minute)
	if r := e.do(t, tokC, "POST", "/billing/waitlist", nil); r.status != 200 || r.body["position"] != float64(1) {
		t.Fatalf("C asks again: %d %s", r.status, r.raw)
	}
	if n, _, err := inv.Run(ctx, clock); err != nil || n != 1 {
		t.Fatalf("C invited again: %d %v", n, err)
	}
	clock = clock.Add(time.Minute)
	if r := e.do(t, tokB, "POST", "/billing/checkout", map[string]any{"plan": "solo"}); r.status != 503 || errCode(r) != "waitlisted" {
		t.Fatalf("B's checkout while C holds: %d %s", r.status, r.raw)
	}
	clock = clock.Add(time.Minute)
	if r := e.do(t, tokA, "POST", "/billing/waitlist", nil); r.status != 200 || r.body["position"] != float64(2) {
		t.Fatalf("A asks again behind B: %d %s", r.status, r.raw)
	}
	for id, want := range map[uuid.UUID]int{idA: 2, idB: 1} {
		w, _ := store.GetWaitlistEntry(ctx, e.h.Pool, id)
		if w.LapsedAt != nil || w.Position != want || w.ExpiredInvites != 1 || w.JoinedAt.Before(clock.Add(-2*time.Minute)) {
			t.Fatalf("%s after asking again: %+v, want position %d", id, w, want)
		}
	}
	for _, id := range []uuid.UUID{idA, idB, idC} {
		if n := emails(id, "waitlist_joined"); n != 1 {
			t.Fatalf("%s got %d joined emails, want the first only", id, n)
		}
	}
	// An operator's admit is the other way past a lapse (I-294 (5)).
	_, idE := user("lapse-e")
	if _, err := e.h.Pool.Exec(ctx, "insert into waitlist (user_id, joined_at, expired_invites, lapsed_at) values ($1, $2, 1, $2)", idE, clock); err != nil {
		t.Fatal(err)
	}
	if ok, err := waitlist.Invite(ctx, e.h.Pool, idE, "admin:test", clock); err != nil || !ok {
		t.Fatalf("admit a lapsed user: %v %v", ok, err)
	}
	if w, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idE); w.LapsedAt != nil || w.InvitedAt == nil {
		t.Fatalf("E after admit: %+v", w)
	}
}

// A row that was cooling under I-902 when migration 0034 ran keeps the
// place its expiry email named, sits out the rest of the cooldown, gets
// one more invitation and then lapses for good (I-1033). A lapse also
// writes I-902's columns, so an api on the old code reads it as a
// cooling row at the back, not as the next candidate.
func TestACoolingRowGetsOneMoreTurnThenLapses(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	e := newEnv(t)
	ctx := e.h.Ctx
	inv := &waitlist.Inviter{Pool: e.h.Pool, Total: 1}
	u := e.h.NewUser("cooling")
	if _, err := e.h.Pool.Exec(ctx, "insert into waitlist (user_id, joined_at, expired_invites, cooldown_until) values ($1, $2, 1, $3)", u.ID, clock, clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if w, _ := store.GetWaitlistEntry(ctx, e.h.Pool, u.ID); w.Position != 1 || w.LapsedAt != nil {
		t.Fatalf("cooling row after 0034: %+v", w)
	}
	if n, _, err := inv.Run(ctx, clock.Add(time.Minute)); err != nil || n != 0 {
		t.Fatalf("invited during the cooldown: %d %v", n, err)
	}
	invited, expired := 0, 0
	for at, end := clock.Add(time.Hour), clock.Add(30*24*time.Hour); at.Before(end); at = at.Add(time.Hour) {
		n, x, err := inv.Run(ctx, at)
		if err != nil {
			t.Fatal(err)
		}
		invited += n
		expired += x
	}
	if invited != 1 || expired != 1 {
		t.Fatalf("after the cooldown: invited %d expired %d, want one more turn", invited, expired)
	}
	w, _ := store.GetWaitlistEntry(ctx, e.h.Pool, u.ID)
	if w.LapsedAt == nil || w.Position != 0 || w.ExpiredInvites != 2 {
		t.Fatalf("after the second hold: %+v", w)
	}
	// What the old code reads: at the back, cooling.
	if w.CooldownUntil == nil || !w.CooldownUntil.Equal(w.LapsedAt.Add(24*time.Hour)) || !w.JoinedAt.Equal(*w.LapsedAt) {
		t.Fatalf("I-902's columns on a lapse: %+v", w)
	}
}

// A billing-exempt account has no plan to choose and no place to wait
// for, so neither POST route puts it on the waitlist, and a row an
// older release left behind takes no place in the queue (I-1090). The
// dashboard shows such an account no plan cards and no Join button, so
// what this guards is the api; the inviter case is what the bug cost
// before the fix -- a 72-hour hold on a seat nobody could convert.
func TestAnExemptAccountCannotJoin(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	f := newFakePolar(t)
	var svc *waitlist.Service
	e := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		svc = &waitlist.Service{Pool: d.Pool, Total: 1, M: d.Metrics, Log: d.Log, Now: func() time.Time { return clock }}
		d.Seats = svc
		d.Billing = billing.NewService(d.Pool, billing.NewPolar(f.cfg, d.Log), f.cfg, svc, billing.NewOverage(d.Pool, billing.NewPolar(f.cfg, d.Log), f.cfg, d.Engine, d.Metrics, d.Log), d.Log)
		d.Gate = billing.NewGate(d.Pool, f.cfg, d.Metrics, d.Log)
	})
	ctx := e.h.Ctx
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.h.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := e.h.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	user := func(handle string) (string, uuid.UUID) {
		t.Helper()
		tok := e.signIn(t, "sub-"+handle, handle)
		e.subscribe(t, "sub-"+handle, "")
		return tok, uuid.MustParse(e.do(t, tok, "GET", "/me", nil).body["id"].(string))
	}

	// The fleet's one seat is held, so a checkout would waitlist anyone
	// who is not refused first.
	holder := e.h.NewUser("exempt-holder")
	exec("insert into subscriptions (id, user_id, customer_id, plan, status, seats) values ('sub_exempt_holder', $1, 'ctm_eh', 'solo', 'active', 1)", holder.ID)
	tokX, idX := user("exempt-x")
	exec("update users set billing_status = 'exempt' where id = $1", idX)

	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/billing/checkout", map[string]any{"plan": "solo"}},
		{"/billing/waitlist", nil},
	} {
		r := e.do(t, tokX, "POST", c.path, c.body)
		if r.status != 409 || errCode(r) != "conflict" {
			t.Fatalf("%s as an exempt account: %d %s", c.path, r.status, r.raw)
		}
		d := r.body["error"].(map[string]any)["detail"].(map[string]any)
		if d["reason"] != "exempt" {
			t.Fatalf("%s detail: %v", c.path, d)
		}
		if msg := r.body["error"].(map[string]any)["message"]; msg != "This account is billing-exempt, so it has no plan to choose and no place to wait for." {
			t.Fatalf("%s message: %v", c.path, msg)
		}
	}
	if n := count("select count(*) from waitlist"); n != 0 {
		t.Fatalf("waitlist rows after two refusals: %d", n)
	}

	// A row from before the refusal existed: it is in front by time and
	// takes no place, is not counted as waiting, and the tick passes it
	// over for the account behind it.
	exec("insert into waitlist (user_id, joined_at) values ($1, $2)", idX, clock.Add(-time.Hour))
	if c, err := svc.Count(ctx); err != nil || c.Waiting != 0 {
		t.Fatalf("waiting with one exempt row: %+v %v", c, err)
	}
	if w, err := store.GetWaitlistEntry(ctx, e.h.Pool, idX); err != nil || w.Position != 0 {
		t.Fatalf("the exempt row's position: %+v %v", w, err)
	}
	if r := e.do(t, tokX, "GET", "/me", nil); r.body["waitlist"] != nil {
		t.Fatalf("an exempt account's /me carries a place: %s", r.raw)
	}
	tokP, idP := user("exempt-plain")
	if r := e.do(t, tokP, "POST", "/billing/waitlist", nil); r.status != 200 || r.body["position"] != float64(1) {
		t.Fatalf("the plain account joins behind the exempt row: %d %s", r.status, r.raw)
	}
	exec("update subscriptions set status = 'canceled' where id = 'sub_exempt_holder'")
	inv := &waitlist.Inviter{Pool: e.h.Pool, Total: 1}
	if n, x, err := inv.Run(ctx, clock); err != nil || n != 1 || x != 0 {
		t.Fatalf("one tick with one free seat: invited %d expired %d %v", n, x, err)
	}
	if w, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idX); w.InvitedAt != nil {
		t.Fatalf("the tick invited the exempt account: %+v", w)
	}
	if w, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idP); w.InvitedAt == nil {
		t.Fatalf("the tick skipped the account behind it: %+v", w)
	}
}
