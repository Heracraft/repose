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
	"github.com/heracraft/repose/internal/db"
)

// DECISIONS I-290: seats are what the fleet's hosts deliver (or
// SEATS_TOTAL), the smaller of memory and cores per host since I-739,
// held by live subscriptions and unexpired invitations. Reserve says
// whether a checkout may proceed and otherwise puts the user on the list;
// POST /billing/waitlist joins it idempotently; GET /public/seats counts
// it; the minute tick invites oldest first, one seat each, strictly in
// order, one email each however often it runs; a hold that runs out moves
// the user to the back; POST /projects no longer gates.
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
	// 72 hours pass: C's hold runs out, C goes to the back behind D with
	// the expiry email saying so, and the freed seat goes to D.
	clock = invitedC.Add(72*time.Hour + time.Minute)
	n, x, err = inv.Run(ctx, clock)
	if err != nil || x != 1 || n != 1 {
		t.Fatalf("expiry tick: invited %d expired %d %v", n, x, err)
	}
	cEntry, _ = store.GetWaitlistEntry(ctx, e.h.Pool, idC)
	if cEntry.InvitedAt != nil || cEntry.HoldUntil != nil || cEntry.ExpiredInvites != 1 || !cEntry.JoinedAt.Equal(clock) || cEntry.Position != 1 {
		t.Fatalf("C after expiry: %+v", cEntry)
	}
	d, _ := store.GetWaitlistEntry(ctx, e.h.Pool, idD)
	if d.InvitedAt == nil || d.Position != 0 {
		t.Fatalf("D not invited after C's expiry: %+v", d)
	}
	_ = e.h.Pool.QueryRow(ctx, "select summary from events where user_id = $1 and kind = 'waitlist_expired'", idC).Scan(&summary)
	if summary != `{"position":2}` {
		t.Fatalf("expired payload %q (C was behind D when the hold ran out)", summary)
	}
	if n := count("select count(*) from events_outbox o join events ev on ev.id = o.event_id where ev.kind = 'waitlist_expired' and ev.user_id = $1", idC); n != 1 {
		t.Fatalf("expired email queued %d", n)
	}
	// Strict order and SEATS_TOTAL: with the total set to ten, everyone
	// waiting is invited, and no one twice. C's hold has just run out,
	// so C waits out the cooldown first (I-902) and is not a candidate
	// on the next tick even with seven seats free.
	big := &waitlist.Inviter{Pool: e.h.Pool, Total: 10}
	if n, _, err := big.Run(ctx, clock.Add(time.Minute)); err != nil || n != 0 {
		t.Fatalf("SEATS_TOTAL=10 a minute after C's expiry invited %d %v, want none", n, err)
	}
	clock = clock.Add(waitlist.CooldownDuration + time.Minute)
	if n, _, err := big.Run(ctx, clock); err != nil || n != 1 {
		t.Fatalf("SEATS_TOTAL=10 past the cooldown invited %d %v, want C", n, err)
	}
	if ws, err := store.ListWaiting(ctx, e.h.Pool, clock); err != nil || len(ws) != 0 {
		t.Fatalf("still waiting: %+v %v", ws, err)
	}
	c, src, der, err := (&waitlist.Service{Pool: e.h.Pool, Total: 10, Now: func() time.Time { return clock.Add(time.Minute) }}).CountSource(ctx)
	// Held: B's subscription, D's hold, C's hold. A total that came from
	// SEATS_TOTAL was derived from nothing, so it carries no ceilings.
	if err != nil || src != waitlist.SourceConfig || c != (waitlist.Count{Total: 10, Held: 3, Free: 7, Waiting: 0}) || der != (waitlist.Derivation{}) {
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

// The lone waiting user is not re-invited by the tick that expired
// their own hold (I-902). I-290 moves an expired hold to the back of the
// list and I-294 (4) expires before it invites, so with one candidate
// the back of the list was also its front: the same minute freed the
// seat, counted the user as waiting and invited them again, sending
// "your hold ran out" and "a seat is yours" together, every 72 hours,
// with nothing to end it. The two-user handover above is covered; only
// `Waiting == 1` reached the loop, and no test had one.
func TestLoneWaiterIsNotReInvitedByItsOwnExpiry(t *testing.T) {
	clock := time.Now().Truncate(time.Second)
	e := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		d.Seats = &waitlist.Service{Pool: d.Pool, M: d.Metrics, Log: d.Log, Now: func() time.Time { return clock }}
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
	// One seat in the fleet, nobody holding it, one user on the list.
	inv := &waitlist.Inviter{Pool: e.h.Pool, Total: 1}
	tok := e.signIn(t, "sub-lone", "lone")
	e.subscribe(t, "sub-lone", "")
	id := uuid.MustParse(e.do(t, tok, "GET", "/me", nil).body["id"].(string))
	if r := e.do(t, tok, "POST", "/billing/waitlist", nil); r.status != 200 || r.body["position"] != float64(1) {
		t.Fatalf("join: %d %s", r.status, r.raw)
	}
	// The free seat is theirs.
	if n, x, err := inv.Run(ctx, clock); err != nil || n != 1 || x != 0 {
		t.Fatalf("first tick: invited %d expired %d %v", n, x, err)
	}
	// 72 hours later the hold runs out. Before I-902 this one call both
	// expired it and handed it straight back.
	clock = clock.Add(waitlist.HoldDuration + time.Minute)
	n, x, err := inv.Run(ctx, clock)
	if err != nil || x != 1 || n != 0 {
		t.Fatalf("the expiry tick: invited %d expired %d %v, want 0 invited", n, x, err)
	}
	entry, err := store.GetWaitlistEntry(ctx, e.h.Pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if entry.InvitedAt != nil || entry.HoldUntil != nil {
		t.Fatalf("re-invited by its own expiry: %+v", entry)
	}
	if entry.CooldownUntil == nil || !entry.CooldownUntil.Equal(clock.Add(waitlist.CooldownDuration)) {
		t.Fatalf("cooldown %v, want %v", entry.CooldownUntil, clock.Add(waitlist.CooldownDuration))
	}
	// They keep their place and can still read it.
	if entry.Position != 1 {
		t.Fatalf("position %d while cooling, want 1", entry.Position)
	}
	// Ticks through the cooldown invite nobody and send nothing more.
	for _, d := range []time.Duration{time.Minute, time.Hour, waitlist.CooldownDuration - time.Minute} {
		if n, x, err := inv.Run(ctx, clock.Add(d)); err != nil || n != 0 || x != 0 {
			t.Fatalf("tick at +%s: invited %d expired %d %v", d, n, x, err)
		}
	}
	if n := count("select count(*) from events where user_id = $1 and kind = 'waitlist_invited'", id); n != 1 {
		t.Fatalf("%d invitation emails during the cooldown, want the first one only", n)
	}
	if n := count("select count(*) from events where user_id = $1 and kind = 'waitlist_expired'", id); n != 1 {
		t.Fatalf("%d expiry emails, want 1", n)
	}
	// Past the cooldown the seat is offered again, once.
	after := clock.Add(waitlist.CooldownDuration + time.Minute)
	if n, x, err := inv.Run(ctx, after); err != nil || n != 1 || x != 0 {
		t.Fatalf("past the cooldown: invited %d expired %d %v", n, x, err)
	}
	if n := count("select count(*) from events where user_id = $1 and kind = 'waitlist_invited'", id); n != 2 {
		t.Fatalf("%d invitation emails after the cooldown, want 2", n)
	}
	// A user who comes back and asks is not kept waiting: joining again
	// clears the cooldown, which is only there for silence.
	clock = after
	tok2 := e.signIn(t, "sub-returning", "returning")
	e.subscribe(t, "sub-returning", "")
	id2 := uuid.MustParse(e.do(t, tok2, "GET", "/me", nil).body["id"].(string))
	if _, err := e.h.Pool.Exec(ctx, `insert into waitlist (user_id, joined_at, invited_at, hold_until, invited_by, cooldown_until)
		values ($1, $2, $2, $3, 'auto', $4)`, id2, after, after.Add(-time.Minute), after.Add(waitlist.CooldownDuration)); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok2, "POST", "/billing/waitlist", nil); r.status != 200 {
		t.Fatalf("the returning user joins: %d %s", r.status, r.raw)
	}
	back, err := store.GetWaitlistEntry(ctx, e.h.Pool, id2)
	if err != nil {
		t.Fatal(err)
	}
	if back.CooldownUntil != nil {
		t.Fatalf("asking again left the cooldown on: %+v", back)
	}
}
