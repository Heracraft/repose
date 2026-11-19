package httpapi_test

import (
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/api/hostmgr"
	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/store"
)

// The three routes of api.md "Hosts you own" (DECISIONS I-1366).
//
// Every test runs as a billing-exempt account, because the three hosted
// plans sell no host (`billing.Plan.Hosts` is the zero value on Solo,
// Plus and Pro) and 19d's Own hardware plan is not on this branch: an
// exempt account is allowed one host whatever the plans say, which is
// also how the owner adds their own Mac without selling themselves a
// plan.

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

	// A second host is refused: the plan allows one.
	r = e.do(t, tok, "POST", "/hosts", map[string]any{"name": "spare"})
	if r.status != 409 || detailOf(t, r)["reason"] != "host_limit" || detailOf(t, r)["limit"] != float64(1) {
		t.Fatalf("second host: %d %s, want conflict host_limit limit 1", r.status, r.raw)
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
	if r := e.do(t, tok, "POST", "/hosts", nil); r.status != 402 || detailOf(t, r)["reason"] != "own_hardware_plan_required" {
		t.Fatalf("no plan: %d %s", r.status, r.raw)
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
}
