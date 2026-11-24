package httpapi_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/api/hostmgr"
	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
)

// The three routes of api.md "Hosts you own" (DECISIONS I-1366).
//
// Every test runs as a billing-exempt account, because the three hosted
// plans sell no host (`billing.Plan.Hosts` is the zero value on Solo,
// Plus and Pro): an exempt account is allowed billing.ExemptHosts
// whatever the plans say, which is also how the owner adds their own
// Mac, and test hosts beside it, without selling themselves a plan
// (I-1582). TestHostLimit holds the limit on both kinds of account.

// detailOf is the error envelope's detail object.
func detailOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	d, _ := r.body["error"].(map[string]any)["detail"].(map[string]any)
	return d
}

// exempt makes the signed-in account billing-exempt and returns its id.
func (e *env) exempt(t *testing.T, sub string) string {
	t.Helper()
	var uid string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "update users set billing_status = 'exempt' where logto_sub = $1 returning id", sub).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	return uid
}

// TestHostAddListRemove is the whole loop a Mac mini owner walks: add,
// read it back, remove it.
func TestHostAddListRemove(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-mac", "mac")
	e.exempt(t, "sub-mac")

	if r := e.do(t, tok, "GET", "/hosts", nil); r.status != 200 || len(r.list) != 0 {
		t.Fatalf("an account that owns nothing: %d %s, want an empty list", r.status, r.raw)
	}

	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	if r.body["name"] != "mac-mini" || r.body["state"] != "registering" {
		t.Fatalf("add answered %v, want mac-mini registering", r.body)
	}
	token, _ := r.body["join_token"].(string)
	if token == "" || r.body["join_token_expires_at"] == nil {
		t.Fatalf("add answered no join token: %s", r.raw)
	}
	// The install object is always there, and its command is empty until
	// an operator records a line: a client shows the token and the page
	// rather than a line nobody serves.
	install, ok := r.body["install"].(map[string]any)
	if !ok || install["command"] != "" || install["url"] != "https://repose.run/docs/own-hardware" {
		t.Fatalf("add answered install %v, want an empty command and the docs page", r.body["install"])
	}
	id, _ := r.body["id"].(string)

	// Only the hash is kept, and the token is never in a log line.
	var stored string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select coalesce(join_token_hash, '') from hosts where id = $1", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "" || stored == token {
		t.Fatalf("join token stored as %q, want a hash that is not the token", stored)
	}
	if strings.Contains(e.logText(), token) {
		t.Fatal("the join token reached a log line")
	}

	// The list carries the host and never the token.
	r = e.do(t, tok, "GET", "/hosts", nil)
	if r.status != 200 || len(r.list) != 1 {
		t.Fatalf("list: %d %s", r.status, r.raw)
	}
	row, _ := r.list[0].(map[string]any)
	if row["name"] != "mac-mini" || row["projects"] != float64(0) || row["running_projects"] != float64(0) {
		t.Fatalf("list row %v", row)
	}
	if _, ok := row["join_token"]; ok {
		t.Fatalf("the list carries the join token: %v", row)
	}
	if _, ok := row["last_heartbeat_at"]; ok {
		t.Fatalf("a host that never registered has a heartbeat: %v", row)
	}

	// Remove, then remove again: the second answers 204 so a client that
	// lost the first answer can ask again.
	if r := e.do(t, tok, "DELETE", "/hosts/"+id, nil); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "DELETE", "/hosts/"+id, nil); r.status != 204 {
		t.Fatalf("remove again: %d %s, want 204", r.status, r.raw)
	}
	// Retired, with its serials and its unused token cleared, so nothing
	// that machine holds registers it again (I-432).
	var state, serials string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select state, coalesce(cert_serial, '') || coalesce(join_token_hash, '') from hosts where id = $1", id).Scan(&state, &serials); err != nil {
		t.Fatal(err)
	}
	if state != "retired" || serials != "" {
		t.Fatalf("after remove: state %q, serials+token %q", state, serials)
	}
	// The row stays, so the list is empty but the history is not lost.
	if r := e.do(t, tok, "GET", "/hosts", nil); r.status != 200 || len(r.list) != 1 || r.list[0].(map[string]any)["state"] != "retired" {
		t.Fatalf("list after remove: %d %s", r.status, r.raw)
	}
	// And the slot is free again, under the same name: the row the api
	// kept for the history is the row that takes it, because the name is
	// unique per account whatever a host's state, and telling a user the
	// name of the machine they just removed is taken is a refusal they
	// cannot act on.
	r = e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 || r.body["name"] != "mac-mini" || r.body["state"] != "registering" {
		t.Fatalf("adding the removed host's name again: %d %s, want 201 registering", r.status, r.raw)
	}
	if r.body["id"] != id {
		t.Fatalf("the name came back on a new row %v, want the row it kept (%s)", r.body["id"], id)
	}
	// `hosts retire` sets draining, and a revived row that kept it would
	// register, reach `ready` and take no placement.
	var draining bool
	var revivedSerial string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select draining, coalesce(cert_serial, '') from hosts where id = $1", id).Scan(&draining, &revivedSerial); err != nil {
		t.Fatal(err)
	}
	if draining || revivedSerial != "" {
		t.Fatalf("the revived host is draining=%v with serial %q", draining, revivedSerial)
	}
}

// TestHostLimit: the Own hardware plan sells any number of hosts
// (I-1594), so a paying Own hardware account adds a second and a third
// host, and /me says the same figure the route counts against. An exempt
// account has the same rule: it was refused its ninth under I-1582's
// eight, and is not now.
func TestHostLimit(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-owner", "owner")
	e.exempt(t, "sub-owner")
	if billing.ExemptHosts != billing.UnlimitedHosts {
		t.Fatalf("billing.ExemptHosts = %d, want UnlimitedHosts", billing.ExemptHosts)
	}
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("e2e-x86-%d", i)
		if r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": name}); r.status != 201 || r.body["name"] != name {
			t.Fatalf("exempt host %d: %d %s, want 201", i, r.status, r.raw)
		}
	}
	if r := e.do(t, tok, "GET", "/hosts", nil); r.status != 200 || len(r.list) != 9 {
		t.Fatalf("list: %d, %d hosts, want 9", r.status, len(r.list))
	}

	// A paying Own hardware account: a second and a third host go in.
	tok = e.signIn(t, "sub-own", "own")
	e.subscribe(t, "sub-own", "own")
	if p, _ := billing.PlanByID("own"); p.Hosts != billing.UnlimitedHosts {
		t.Fatalf("the Own hardware plan sells %d hosts, want UnlimitedHosts", p.Hosts)
	}
	for _, name := range []string{"mac-mini", "box-b", "box-c"} {
		if r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": name}); r.status != 201 || r.body["name"] != name {
			t.Fatalf("Own hardware host %s: %d %s, want 201", name, r.status, r.raw)
		}
	}
	if r := e.do(t, tok, "GET", "/hosts", nil); r.status != 200 || len(r.list) != 3 {
		t.Fatalf("list: %d, %d hosts, want 3", r.status, len(r.list))
	}
	// The route's figure is /me's: a client reading limits.hosts sees
	// the number the count is made against, and it is not 0, which
	// means a plan that sells none.
	r := e.do(t, tok, "GET", "/me", nil)
	limits, _ := r.body["limits"].(map[string]any)
	if r.status != 200 || limits["hosts"] != float64(billing.UnlimitedHosts) {
		t.Fatalf("/me limits: %d %v, want hosts %d", r.status, limits, billing.UnlimitedHosts)
	}
}

// TestCreateWithSeveralOwnHosts: an account with two hosts of its own
// (an exempt one, I-1582) is told about both when neither can take the
// machine, and is placed on the one that can while the other waits.
// Naming only the first said "your host e2e-x86-1 has not registered
// yet" about a placement the second refused too.
func TestCreateWithSeveralOwnHosts(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-owner", "owner")
	uid := e.exempt(t, "sub-owner")
	for _, name := range []string{"e2e-x86-1", "e2e-x86-2"} {
		if r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": name}); r.status != 201 {
			t.Fatalf("add %s: %d %s", name, r.status, r.raw)
		}
	}
	if _, err := e.h.Pool.Exec(ctx, "update hosts set state = 'unreachable', last_heartbeat_at = now() - interval '14 minutes' where owner_account_id = $1 and name = 'e2e-x86-2'", uid); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "small"})
	d := detailOf(t, r)
	if r.status != 503 || d["reason"] != "no_own_host" || d["host"] != "e2e-x86-1" || d["host_state"] != "registering" {
		t.Fatalf("both hosts down: %d %s, want 503 no_own_host naming e2e-x86-1 first", r.status, r.raw)
	}
	msg, _ := r.body["error"].(map[string]any)["message"].(string)
	if want := "none of your hosts can take a small machine (4 GB): e2e-x86-1 has not registered yet; e2e-x86-2 has not answered for 14m"; msg != want {
		t.Fatalf("message %q, want %q", msg, want)
	}

	// A third host of the account's that is online takes the machine,
	// though the oldest two still cannot.
	if _, err := e.h.Pool.Exec(ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "small"})
	if r.status != 201 || r.body["host_id"] != e.h.HostID.String() {
		t.Fatalf("with an online host: %d %s, want 201 on %s", r.status, r.raw, e.h.HostID)
	}
}

// TestWaitingHostsAreCapped is ENG-211's per-account half: hosts that
// have not joined yet each hold one of the thousand host addresses every
// account shares, so one account may have ten at once. The eleventh is
// refused with waiting_limit; one that joins, one whose token expired
// and one removed each make room; another account is not touched.
func TestWaitingHostsAreCapped(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-owner", "owner")
	uid := e.exempt(t, "sub-owner")
	for i := 1; i <= hostmgr.MaxWaitingHosts; i++ {
		if r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": fmt.Sprintf("box-%d", i)}); r.status != 201 {
			t.Fatalf("waiting host %d: %d %s, want 201", i, r.status, r.raw)
		}
	}
	refused := func(why string) {
		t.Helper()
		r := e.do(t, tok, "POST", "/hosts", nil)
		d := detailOf(t, r)
		msg, _ := r.body["error"].(map[string]any)["message"].(string)
		if r.status != 409 || d["reason"] != "waiting_limit" || d["limit"] != float64(hostmgr.MaxWaitingHosts) ||
			msg != "10 hosts have not joined yet; install repose on one, or remove one, before you add another" {
			t.Fatalf("%s: %d %s, want 409 waiting_limit", why, r.status, r.raw)
		}
	}
	refused("an eleventh")
	stok := e.signIn(t, "sub-stranger", "stranger")
	e.exempt(t, "sub-stranger")
	if r := e.do(t, stok, "POST", "/hosts", nil); r.status != 201 {
		t.Fatalf("another account: %d %s, want 201", r.status, r.raw)
	}
	for _, c := range []struct{ why, sql string }{
		{"one joined", "update hosts set registered_at = now(), state = 'ready', join_token_hash = null where owner_account_id = $1 and name = 'box-1'"},
		{"one's token expired", "update hosts set join_token_expires_at = now() - interval '1 second' where owner_account_id = $1 and name = 'box-2'"},
		{"one removed", "update hosts set state = 'retired', join_token_hash = null where owner_account_id = $1 and name = 'box-3'"},
	} {
		if _, err := e.h.Pool.Exec(ctx, c.sql, uid); err != nil {
			t.Fatal(err)
		}
		if r := e.do(t, tok, "POST", "/hosts", nil); r.status != 201 {
			t.Fatalf("after %s: %d %s, want 201", c.why, r.status, r.raw)
		}
		refused("after " + c.why + " and one more")
	}
}

// TestCreateOnANamedHost: `host` on POST /projects puts the machine on
// that host of the account's own or refuses it as that host, never on
// another (DECISIONS I-1584). Without it the scheduler picks, as before.
func TestCreateOnANamedHost(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-owner", "owner")
	uid := e.exempt(t, "sub-owner")
	if r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "e2e-x86-b"}); r.status != 201 {
		t.Fatalf("add e2e-x86-b: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update hosts set state = 'unreachable', last_heartbeat_at = now() - interval '14 minutes' where owner_account_id = $1 and name = 'e2e-x86-b'", uid); err != nil {
		t.Fatal(err)
	}
	// The env's online host becomes the account's second, as e2e-x86-a.
	if _, err := e.h.Pool.Exec(ctx, "update hosts set owner_account_id = $1, name = 'e2e-x86-a' where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}

	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "on-b", "class": "small", "host": "e2e-x86-b"})
	d := detailOf(t, r)
	msg, _ := r.body["error"].(map[string]any)["message"].(string)
	if r.status != 503 || d["reason"] != "no_own_host" || d["host"] != "e2e-x86-b" || d["host_state"] != "unreachable" || msg != "your host e2e-x86-b has not answered for 14m" {
		t.Fatalf("offline named host: %d %s, want 503 naming e2e-x86-b", r.status, r.raw)
	}
	var n int
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from projects where name = 'on-b'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused create left %d rows (%v)", n, err)
	}

	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "on-a", "class": "small", "host": "e2e-x86-a"})
	if r.status != 201 || r.body["host_id"] != e.h.HostID.String() || r.body["host_name"] != "e2e-x86-a" {
		t.Fatalf("named online host: %d %s, want 201 on e2e-x86-a", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "by-id", "class": "small", "host": e.h.HostID.String()})
	if r.status != 201 || r.body["host_id"] != e.h.HostID.String() {
		t.Fatalf("host by id: %d %s, want 201 on e2e-x86-a", r.status, r.raw)
	}

	// Full: the named host is refused as that host, with its figure
	// (I-1586). By its size, which only Register writes: the fake host's
	// heartbeat rewrites free_mem_bytes every 100 ms, so a free figure
	// set here could be gone before the pick or between the pick and the
	// sentence (ENG-214). 2 GiB is all reserve, so 0 GB is free.
	if _, err := e.h.Pool.Exec(ctx, "update hosts set mem_bytes = 2::bigint << 30 where id = $1", e.h.HostID); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "full", "class": "small", "host": "e2e-x86-a"})
	msg, _ = r.body["error"].(map[string]any)["message"].(string)
	if r.status != 503 || detailOf(t, r)["host"] != "e2e-x86-a" || !strings.HasPrefix(msg, "your host e2e-x86-a has 0 GB of memory free for machines, and a small machine needs 4 GB") {
		t.Fatalf("full named host: %d %s, want 503 naming e2e-x86-a", r.status, r.raw)
	}

	for _, c := range []struct {
		why  string
		body map[string]any
	}{
		{"a name the account has no host under", map[string]any{"name": "x1", "class": "small", "host": "e2e-x86-z"}},
		{"a host with placement hosted", map[string]any{"name": "x2", "class": "small", "host": "e2e-x86-a", "placement": "hosted"}},
	} {
		r = e.do(t, tok, "POST", "/projects", c.body)
		if r.status != 400 || detailOf(t, r)["reason"] != "host" {
			t.Fatalf("%s: %d %s, want 400 invalid host", c.why, r.status, r.raw)
		}
	}

	// Another account's host, by id or by its name, is no host of this
	// account's: the same 400, which says nothing about the host.
	stok := e.signIn(t, "sub-stranger", "stranger")
	e.exempt(t, "sub-stranger")
	for _, h := range []string{e.h.HostID.String(), "e2e-x86-a"} {
		r = e.do(t, stok, "POST", "/projects", map[string]any{"name": "theirs", "class": "small", "host": h})
		if r.status != 400 || detailOf(t, r)["reason"] != "host" {
			t.Fatalf("stranger naming %s: %d %s, want 400 invalid host", h, r.status, r.raw)
		}
	}
}

// TestHostNames: the api names a host the caller did not name, a bad
// name is refused, and a name is free per account rather than globally
// (migration 0048) -- two users may both call their machine mac-mini and
// neither is told the other exists.
func TestHostNames(t *testing.T) {
	e := newEnv(t)
	a := e.signIn(t, "sub-a", "ana")
	e.exempt(t, "sub-a")
	b := e.signIn(t, "sub-b", "bo")
	e.exempt(t, "sub-b")

	if r := e.do(t, a, "POST", "/hosts", map[string]any{"name": "Mac Mini"}); r.status != 400 || detailOf(t, r)["reason"] != "name" {
		t.Fatalf("a bad name: %d %s, want invalid name", r.status, r.raw)
	}
	r := e.do(t, a, "POST", "/hosts", nil)
	if r.status != 201 || r.body["name"] != "host-1" {
		t.Fatalf("no name: %d %s, want host-1", r.status, r.raw)
	}
	if r := e.do(t, a, "POST", "/hosts", map[string]any{"name": "host-1"}); r.status != 409 || detailOf(t, r)["reason"] != "name_taken" {
		t.Fatalf("the same name twice: %d %s, want conflict name_taken", r.status, r.raw)
	}
	// Another account may take the name, and its first unnamed host is
	// host-1 too.
	r = e.do(t, b, "POST", "/hosts", map[string]any{"name": "host-1"})
	if r.status != 201 {
		t.Fatalf("another account's host-1: %d %s, want 201", r.status, r.raw)
	}
}

// TestHostIsNotAnotherAccounts: a host that is not the caller's is not
// found, so the route says nothing at all about another account's
// machines, and cannot be removed by its id.
func TestHostIsNotAnotherAccounts(t *testing.T) {
	e := newEnv(t)
	a := e.signIn(t, "sub-a", "ana")
	e.exempt(t, "sub-a")
	b := e.signIn(t, "sub-b", "bo")
	e.exempt(t, "sub-b")
	r := e.do(t, a, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	id, _ := r.body["id"].(string)
	if r := e.do(t, b, "GET", "/hosts", nil); r.status != 200 || len(r.list) != 0 {
		t.Fatalf("another account's list: %d %s, want empty", r.status, r.raw)
	}
	if r := e.do(t, b, "DELETE", "/hosts/"+id, nil); r.status != 404 {
		t.Fatalf("another account's remove: %d %s, want 404", r.status, r.raw)
	}
	// The host that repose runs is on nobody's list either: these routes
	// are about hardware a user owns, and the fleet is the operator's.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "insert into hosts (id, name, state) values ($1, 'fleet-01', 'ready')", store.NewID()); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, a, "GET", "/hosts", nil); r.status != 200 || len(r.list) != 1 {
		t.Fatalf("list with a fleet host present: %d %s, want only the owned one", r.status, r.raw)
	}
}

// TestHostWithMachinesIsNotRemoved: a machine moves host only by
// restoring a snapshot (DESIGN.md §6), so retiring a host under its
// machines would orphan them. The refusal names them.
func TestHostWithMachinesIsNotRemoved(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-mac", "mac")
	uid := e.exempt(t, "sub-mac")
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	id, _ := r.body["id"].(string)
	if _, err := e.h.Pool.Exec(e.h.Ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes)
		values ($1, $2, 'todo', 'todo', 'large', 'running', $3, 1)`, store.NewID(), uid, id); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "DELETE", "/hosts/"+id, nil)
	if r.status != 409 || detailOf(t, r)["reason"] != "host_has_projects" {
		t.Fatalf("remove with a machine on it: %d %s, want conflict host_has_projects", r.status, r.raw)
	}
	slugs, _ := detailOf(t, r)["projects"].([]any)
	if len(slugs) != 1 || slugs[0] != "todo" {
		t.Fatalf("the refusal named %v, want [todo]", detailOf(t, r)["projects"])
	}
	// The list says so too, which is what the dashboard's Remove button
	// reads before it offers to remove anything.
	if r := e.do(t, tok, "GET", "/hosts", nil); r.list[0].(map[string]any)["running_projects"] != float64(1) {
		t.Fatalf("list %v, want running_projects 1", r.list[0])
	}
}

// TestHostNeedsAPlanThatSellsOne: the three hosted plans sell no host,
// so a subscriber on one of them is refused, and the figure comes from
// the plan rather than from this route (DECISIONS I-1366).
func TestHostNeedsAPlanThatSellsOne(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-plus", "plus") // signIn subscribes to Plus
	r := e.do(t, tok, "POST", "/hosts", nil)
	if r.status != 402 || detailOf(t, r)["reason"] != "own_hardware_plan_required" {
		t.Fatalf("a Plus subscriber: %d %s, want payment_required own_hardware_plan_required", r.status, r.raw)
	}
	if detailOf(t, r)["limit"] != float64(0) {
		t.Fatalf("the refusal's limit is %v, want 0", detailOf(t, r)["limit"])
	}
	// An account with no plan at all is refused the same way: there is
	// nothing to read a host count off.
	e.subscribe(t, "sub-plus", "")
	r = e.do(t, tok, "POST", "/hosts", nil)
	if r.status != 402 || detailOf(t, r)["reason"] != "own_hardware_plan_required" {
		t.Fatalf("no plan: %d %s", r.status, r.raw)
	}
	// The sentence names the price, from the plan table. Until the
	// public copy is unheld this refusal is the only place the product
	// tells a buyer what a host costs, and the customer review found one
	// who committed hardware without ever being told (ENG-39). The
	// figure is billing.Own's; a literal here would be a second price.
	msg, _ := r.body["error"].(map[string]any)["message"].(string)
	if want := billing.Dollars(billing.Own.PriceCents) + " a month for any number of hosts"; !strings.Contains(msg, want) {
		t.Fatalf("the refusal %q does not say %q", msg, want)
	}
}

// TestPlacementField: POST /projects takes it, the project carries it,
// and a bad value is refused. Where it sends a machine is
// internal/api/scheduler's test; this is the contract api.md states.
func TestPlacementField(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-mac", "mac")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "one", "class": "small"})
	if r.status != 201 || r.body["placement"] != "own" {
		t.Fatalf("default placement: %d %s, want own", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "two", "class": "small", "placement": "hosted"})
	if r.status != 201 || r.body["placement"] != "hosted" {
		t.Fatalf("placement hosted: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "three", "class": "small", "placement": "somewhere"})
	if r.status != 400 || detailOf(t, r)["reason"] != "placement" {
		t.Fatalf("a bad placement: %d %s, want invalid placement", r.status, r.raw)
	}
}

// TestHostAddPeersTheHubBeforeRegistration: the install step's argument
// is the join bundle with the token and the hub material in it, and the
// edge's host list carries the new host from the moment it is added, so
// the tunnel exists before the machine first boots (DECISIONS I-1380).
func TestHostAddPeersTheHubBeforeRegistration(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-hub", "hub")
	e.exempt(t, "sub-hub")
	for k, v := range map[string]string{
		hostmgr.SettingEdgeWGPubkey:   "dyP2l2byrPpr9ehOwYga0Ya9/qkUChpSCR0vJVRa0f8=",
		hostmgr.SettingEdgeWGEndpoint: "edge.example:51820",
		httpapi.SettingHostInstall:    "curl -fsSL https://repose.run/mac | sh -s -- %s",
	} {
		if err := store.SetSetting(e.h.Ctx, e.h.Pool, k, v); err != nil {
			t.Fatal(err)
		}
	}
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	token, _ := r.body["join_token"].(string)
	install, _ := r.body["install"].(map[string]any)
	arg, _ := install["argument"].(string)
	command, _ := install["command"].(string)
	if arg == "" || command != "curl -fsSL https://repose.run/mac | sh -s -- "+arg {
		t.Fatalf("install %v: want the command with the bundle where %%s was", install)
	}
	if strings.Contains(command, token) {
		t.Fatal("the bare token is in the install step; the bundle carries it")
	}
	b, err := hostmgr.DecodeJoinBundle(arg)
	if err != nil || b.Token != token || b.WG == nil || b.WG.EdgeEndpoint != "edge.example:51820" || b.WG.PrivateKey == "" {
		t.Fatalf("bundle %+v (%v): want the token and the hub material", b, err)
	}
	// Nothing of the bundle reaches a log line: it is the machine's key.
	if strings.Contains(e.logText(), b.WG.PrivateKey) || strings.Contains(e.logText(), arg) {
		t.Fatal("the hub bootstrap reached a log line")
	}

	// The edge's list, which wgsync peers from, has the host already.
	ih := e.internalDo(t, "GET", "/internal/hosts", nil)
	if ih.status != 200 {
		t.Fatalf("internal hosts: %d %s", ih.status, ih.raw)
	}
	var found map[string]any
	for _, row := range ih.list {
		if m, _ := row.(map[string]any); m != nil && m["name"] == "mac-mini" {
			found = m
		}
	}
	if found == nil || found["wg_pubkey"] == "" || found["wg_ip"] == nil || found["guest_cidr"] == nil || found["state"] != "registering" {
		t.Fatalf("the unregistered host is not a peer: %v", found)
	}
	if b.WG.Address != found["wg_ip"].(string)+"/16" {
		t.Fatalf("bundle address %s, listed wg_ip %v", b.WG.Address, found["wg_ip"])
	}

	// A bundle nobody used dies with its token: once the token has
	// expired, or the host was removed, the edge stops peering the key.
	listed := func() bool {
		r := e.internalDo(t, "GET", "/internal/hosts", nil)
		for _, row := range r.list {
			if m, _ := row.(map[string]any); m != nil && m["name"] == "mac-mini" {
				return true
			}
		}
		return false
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set join_token_expires_at = now() - interval '1 minute' where id = $1", r.body["id"]); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Fatal("an unregistered host whose token expired is still a peer")
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set join_token_expires_at = now() + interval '1 hour' where id = $1", r.body["id"]); err != nil {
		t.Fatal(err)
	}
	if !listed() {
		t.Fatal("a host with a live token is not a peer")
	}
	if rr := e.do(t, tok, "DELETE", "/hosts/"+r.body["id"].(string), nil); rr.status != 204 {
		t.Fatalf("remove: %d %s", rr.status, rr.raw)
	}
	if listed() {
		t.Fatal("a removed host is still a peer")
	}
}

// TestCreateRefusedBeforeTheRowIsWritten is the shape I-1390 settles for
// ENG-66: placement answers inside the create request, so a project no
// host can take is refused with the sentence the op used to fail with,
// and nothing is written. Until this the row went in as `creating`, the
// op refused it afterwards, and a customer whose Mac had not registered
// yet stranded one project in `error` per attempt. The way out the
// refusal names, `placement: "hosted"`, then makes the project on
// repose's own host.
func TestCreateRefusedBeforeTheRowIsWritten(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-mac", "mac")
	uid := e.exempt(t, "sub-mac")
	if r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"}); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	rows := func() int {
		var n int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from projects where user_id = $1", uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for attempt := 1; attempt <= 2; attempt++ {
		r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "small"})
		d := detailOf(t, r)
		if r.status != 503 || d["reason"] != "no_own_host" || d["host"] != "mac-mini" || d["host_state"] != "registering" || d["hosted_allowed"] != true {
			t.Fatalf("attempt %d while the host waits: %d %s, want 503 capacity no_own_host for mac-mini", attempt, r.status, r.raw)
		}
		if msg, _ := r.body["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "your host mac-mini has not registered yet") {
			t.Fatalf("attempt %d: message %q does not name the host and its state", attempt, msg)
		}
		if n := rows(); n != 0 {
			t.Fatalf("attempt %d stranded %d project row(s)", attempt, n)
		}
	}
	var ops int
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from ops o join projects p on p.id = o.project_id where p.user_id = $1", uid).Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if ops != 0 {
		t.Fatalf("a refused create enqueued %d op(s)", ops)
	}
	// The way out: repose's own host, named in the 201 as host_id.
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "small", "placement": "hosted"})
	if r.status != 201 || r.body["host_id"] != e.h.HostID.String() || r.body["placement"] != "hosted" {
		t.Fatalf("hosted: %d %s, want 201 on %s", r.status, r.raw, e.h.HostID)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("hosted create: %s %+v", op.State, op.Error)
	}
	if n := rows(); n != 1 {
		t.Fatalf("%d rows after one accepted create", n)
	}
	// host_name beside host_id (I-1392): absent on a host repose runs,
	// the name the account gave it once the host is theirs.
	pid := r.body["id"].(string)
	if _, has := r.body["host_name"]; has {
		t.Fatalf("a host repose runs carried host_name: %s", r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid, nil); r.status != 200 || r.body["host_name"] != "host-01" {
		t.Fatalf("own host: %d %s, want host_name host-01", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/projects", nil); r.status != 200 || len(r.list) != 1 || r.list[0].(map[string]any)["host_name"] != "host-01" {
		t.Fatalf("list: %d %s, want host_name host-01", r.status, r.raw)
	}
}

// TestRemovingAHostClearsWhatItLeftOnTheMachines is ENG-33, from the
// customer review: adding a host captured every new project, the host
// never registered, the creates failed with `your host host-1 has not
// registered yet`, and removing the host left five project rows naming
// a host Settings → Hosts said the customer did not have.
//
// A placement failure frees the project's host and leaves the sentence
// on the row (ops/phases.go), so the sentence outlives the host. It is
// the api's to clear: the op keeps the history, and a row that asserts
// something false about hardware the customer no longer owns is the one
// thing they cannot act on.
func TestRemovingAHostClearsWhatItLeftOnTheMachines(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-mac", "mac")
	uid := e.exempt(t, "sub-mac")
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "host-1"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	id, _ := r.body["id"].(string)

	// Two machines the host's absence left in error, with the host freed
	// as a failed placement frees it, and one left in error by something
	// else, which is none of the host's business.
	mk := func(slug, lastErr string) {
		t.Helper()
		if _, err := e.h.Pool.Exec(e.h.Ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes, last_error)
			values ($1, $2, $3, $3, 'large', 'error', 1, $4)`, store.NewID(), uid, slug, lastErr); err != nil {
			t.Fatal(err)
		}
	}
	mk("todo", "capacity: your host host-1 has not registered yet")
	mk("notes", "capacity: your host host-1 has not registered yet")
	mk("api", "build_failed: nixpkgs has no package \"ghc9999\"")

	if r := e.do(t, tok, "DELETE", "/hosts/"+id, nil); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.raw)
	}

	errs := map[string]*string{}
	rows, err := e.h.Pool.Query(e.h.Ctx, "select slug, last_error from projects where user_id = $1", uid)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var slug string
		var last *string
		if err := rows.Scan(&slug, &last); err != nil {
			t.Fatal(err)
		}
		errs[slug] = last
	}
	rows.Close()
	for _, slug := range []string{"todo", "notes"} {
		if errs[slug] != nil {
			t.Errorf("%s still says %q after its host was removed", slug, *errs[slug])
		}
	}
	if errs["api"] == nil || !strings.Contains(*errs["api"], "ghc9999") {
		t.Errorf("a machine in error for its own reasons lost it: %v", errs["api"])
	}
}

// A destroy says whether it will take a final snapshot, because the CLI
// prints what is kept the moment this route answers, before any phase
// runs. On a host nobody expects back every phase including the snapshot
// is skipped (I-924), so a destroy there keeps nothing, and the CLI said
// "its final snapshot is kept until <date>" anyway -- twice, on the
// owner's own account, for a machine whose snapshot count was 0 and had
// never been anything else (ENG-119, DECISIONS I-1418).
func TestDestroySaysWhetherASnapshotIsKept(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-mac", "mac")
	uid := e.exempt(t, "sub-mac")
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	hostID, _ := r.body["id"].(string)

	newProject := func(slug string) string {
		id := store.NewID()
		if _, err := e.h.Pool.Exec(e.h.Ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, guest_id, volume_bytes)
			values ($1, $2, $3, $3, 'small', 'stopped', $4, $5, 1)`, id, uid, slug, hostID, store.NewID()); err != nil {
			t.Fatal(err)
		}
		return id.String()
	}

	// The host is there: the snapshot phase runs and the promise is true.
	live := newProject("live")
	r = e.do(t, tok, "DELETE", "/projects/"+live, nil)
	if r.status != 202 {
		t.Fatalf("destroy on a live host: %d %s", r.status, r.raw)
	}
	if r.body["final_snapshot"] != true {
		t.Errorf("a destroy on a host that is there says final_snapshot %v, want true: %s", r.body["final_snapshot"], r.raw)
	}

	// The user removed the Mac. Nobody expects it back, so the snapshot
	// phase is skipped and nothing is kept.
	gone := newProject("gone")
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set state = 'lost' where id = $1", hostID); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "DELETE", "/projects/"+gone, nil)
	if r.status != 202 {
		t.Fatalf("destroy on a lost host: %d %s", r.status, r.raw)
	}
	if r.body["final_snapshot"] != false {
		t.Errorf("a destroy whose host is gone says final_snapshot %v, want false; the CLI promises a snapshot on that answer: %s", r.body["final_snapshot"], r.raw)
	}
}

// A host that is not answering can be removed although it still holds
// machines, because refusing left the account with no way out at all:
// the host could not be removed while it held a project, and the project
// could not be destroyed while the host was not gone (ENG-119, DECISIONS
// I-1420). A host that IS answering keeps the refusal, because its
// machines are really there.
func TestAHostThatIsGoneCanBeRemovedWithItsMachines(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-mac", "mac")
	uid := e.exempt(t, "sub-mac")
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-rig2"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	id, _ := r.body["id"].(string)
	if _, err := e.h.Pool.Exec(e.h.Ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes)
		values ($1, $2, 'rig1', 'rig1', 'small', 'error', $3, 1)`, store.NewID(), uid, id); err != nil {
		t.Fatal(err)
	}

	// Still answering: the refusal stands.
	if r := e.do(t, tok, "DELETE", "/hosts/"+id, nil); r.status != 409 || detailOf(t, r)["reason"] != "host_has_projects" {
		t.Fatalf("a host that answers: %d %s, want 409 host_has_projects", r.status, r.raw)
	}

	// The user removed the appliance, so the host stopped answering.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set state = 'unreachable' where id = $1", id); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "DELETE", "/hosts/"+id, nil); r.status != 204 {
		t.Fatalf("a host that is not answering: %d %s, want 204; the account has no other way out", r.status, r.raw)
	}

	// Retired, and its credentials cleared, which is what makes this
	// safe: the host cannot come back on them, so the guest whose volume
	// I-924 worried about cannot reappear under a destroyed record.
	var state string
	var serial, token *string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select state, cert_serial, join_token_hash from hosts where id = $1", id).Scan(&state, &serial, &token); err != nil {
		t.Fatal(err)
	}
	if state != "retired" || serial != nil || token != nil {
		t.Errorf("after removal: state %q, cert_serial %v, join_token_hash %v; want retired and both cleared", state, serial, token)
	}
}

// GET /hosts reports the CPU architecture from hosts.arch and never leaks a
// /nix/store/... closure path from hosts.nixos_system (ENG-89, DECISIONS
// I-1448). Before this fix, hostJSON sliced nixos_system on `-`, turning
// `/nix/store/<hash>-nixos-system-...` into `arch: "/nix/store/<hash>"`.
func TestHostArchNeverReturnsStorePath(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-arch", "archuser")
	e.exempt(t, "sub-arch")
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-clean"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	id, _ := r.body["id"].(string)

	// Legacy row: registered before I-1448, nixos_system holds the closure
	// path and arch is null. GET /hosts must omit arch rather than emit the
	// store path.
	storePath := "/nix/store/b9khbvcckcvs6mmjrb77kp0yhznh41s1-nixos-system-repose-appliance-25.05"
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set state = 'ready', nixos_system = $2, arch = null where id = $1", id, storePath); err != nil {
		t.Fatal(err)
	}
	list := e.do(t, tok, "GET", "/hosts", nil)
	if list.status != 200 || len(list.list) != 1 {
		t.Fatalf("list: %d %s", list.status, list.raw)
	}
	row := list.list[0].(map[string]any)
	if got, has := row["arch"]; has {
		t.Fatalf("legacy row with store path emitted arch=%q, want arch omitted", got)
	}

	// Once hostd reports arch (Register/Hello/Rotate), GET /hosts returns it
	// even though nixos_system still holds the closure path.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set arch = 'aarch64' where id = $1", id); err != nil {
		t.Fatal(err)
	}
	list = e.do(t, tok, "GET", "/hosts", nil)
	row = list.list[0].(map[string]any)
	if row["arch"] != "aarch64" {
		t.Fatalf("registered host arch = %v, want aarch64", row["arch"])
	}

	// Old-shape compatibility: a non-path Nix system tuple in nixos_system
	// when arch is null still yields its architecture prefix.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set arch = null, nixos_system = 'x86_64-linux' where id = $1", id); err != nil {
		t.Fatal(err)
	}
	list = e.do(t, tok, "GET", "/hosts", nil)
	row = list.list[0].(map[string]any)
	if row["arch"] != "x86_64" {
		t.Fatalf("tuple nixos_system arch = %v, want x86_64", row["arch"])
	}
}

// TestCreateOnAFullOwnHostNamesTheFigure is the x86 pass's F3 refusal
// (DECISIONS I-1586): a host of the account's own that is full answers
// with its name, the memory it has and the memory the machine needs, and
// not with "no host with capacity", which the CLI turned into "repose has
// no room" about servers the customer does not use.
func TestCreateOnAFullOwnHostNamesTheFigure(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-owner", "owner")
	uid := e.exempt(t, "sub-owner")
	if _, err := e.h.Pool.Exec(ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	var name string
	var mem int64
	if err := e.h.Pool.QueryRow(ctx, "select name, mem_bytes from hosts where id = $1", e.h.HostID).Scan(&name, &mem); err != nil {
		t.Fatal(err)
	}
	// Running xl machines until less than a large's 8 GiB is left.
	placeable := mem - scheduler.HostReserve(mem)
	for i := 0; placeable >= 8<<30; i++ {
		if _, err := e.h.Pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes)
			values ($1, $2, $3, $3, 'xl', 'running', $4, 1)`, store.NewID(), uid, fmt.Sprintf("big-%d", i), e.h.HostID); err != nil {
			t.Fatal(err)
		}
		placeable -= 16 << 30
	}
	var free int64
	if err := e.h.Pool.QueryRow(ctx, "select greatest(least(free_mem_bytes, $2::bigint - (select coalesce(sum(reserved_bytes), 0) from host_reservations where host_id = $1)), 0) from hosts where id = $1",
		e.h.HostID, mem-scheduler.HostReserve(mem)).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free >= 8<<30 {
		t.Fatalf("free %d after filling, want under 8 GiB", free)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "large"})
	if d := detailOf(t, r); r.status != 503 || d["reason"] != "no_own_host" || d["host"] != name || d["host_state"] != "ready" {
		t.Fatalf("a large on a full own host: %d %s, want 503 no_own_host naming %s", r.status, r.raw, name)
	}
	gb := strings.TrimSuffix(strconv.FormatFloat(float64(free)/(1<<30), 'f', 1, 64), ".0")
	want := "your host " + name + " has " + gb + " GB of memory free for machines, and a large machine needs 8 GB. A machine holds its memory while it runs, so stopping one frees room for it"
	if msg, _ := r.body["error"].(map[string]any)["message"].(string); msg != want {
		t.Fatalf("message %q, want %q", msg, want)
	}
}

// TestStartRefusedOnAFullOwnHostNamesTheFigure: a start that hostd
// refuses for memory on a host the user owns names the host and hostd's
// figures, not "the host has no free memory" (I-1586). A start keeps its
// host, so placement never answers it; hostd does.
func TestStartRefusedOnAFullOwnHostNamesTheFigure(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-owner", "owner")
	uid := e.exempt(t, "sub-owner")
	if _, err := e.h.Pool.Exec(ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "small"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/stop", nil); r.status != 202 {
		t.Fatalf("stop: %d %s", r.status, r.raw)
	} else if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("stop op: %+v", op.Error)
	}
	e.h.Fake.SetFail("StartGuest", "insufficient_capacity")
	e.h.Fake.SetFailMessage("StartGuest", "not enough free memory for a small guest")
	var name string
	if err := e.h.Pool.QueryRow(ctx, "select name from hosts where id = $1", e.h.HostID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	st := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	if st.status != 202 {
		t.Fatalf("start: %d %s", st.status, st.raw)
	}
	op := e.waitOp(t, st)
	// The free figure is the fake hostd's last heartbeat, which this
	// test does not pin; the host and the need are the point.
	msg, _ := op.Error["message"].(string)
	if op.State != "error" || !strings.HasPrefix(msg, "your host "+name+" has ") ||
		!strings.Contains(msg, " GB of memory free for machines, and a small machine needs 4.5 GB there. A machine holds its memory while it runs") {
		t.Fatalf("start op %s %v, want the host's name and figures", op.State, op.Error)
	}
}
