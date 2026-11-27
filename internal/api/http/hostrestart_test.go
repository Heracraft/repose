package httpapi_test

import (
	"testing"
)

// POST /hosts/{id}/restart (DECISIONS I-1690): only a ready host of the
// account's own whose hostd parks (`restart` in its features) is
// restarted; a repeated press answers the open op; its op is read at
// GET /hosts/{id}/ops/{op_id} and nowhere else.
func TestHostRestart(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-rs", "rs")
	e.exempt(t, "sub-rs")
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	id, _ := r.body["id"].(string)

	// Not registered: nothing to ask.
	r = e.do(t, tok, "POST", "/hosts/"+id+"/restart", nil)
	if r.status != 409 || detailOf(t, r)["reason"] != "host_not_ready" {
		t.Fatalf("a registering host: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set state = 'ready', last_heartbeat_at = now(), features = '{shared}' where id = $1", id); err != nil {
		t.Fatal(err)
	}
	// A hostd that cannot park: refused, and the list offers no Restart.
	r = e.do(t, tok, "POST", "/hosts/"+id+"/restart", nil)
	if r.status != 409 || detailOf(t, r)["reason"] != "restart_unsupported" {
		t.Fatalf("a host without restart: %d %s", r.status, r.raw)
	}
	if l := e.do(t, tok, "GET", "/hosts", nil); len(l.list) != 1 || l.list[0].(map[string]any)["can_restart"] != nil {
		t.Fatalf("list offers can_restart for a host without it: %s", l.raw)
	}

	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set features = '{shared,restart}' where id = $1", id); err != nil {
		t.Fatal(err)
	}
	if l := e.do(t, tok, "GET", "/hosts", nil); len(l.list) != 1 || l.list[0].(map[string]any)["can_restart"] != true {
		t.Fatalf("list: %s, want can_restart", l.raw)
	}
	r = e.do(t, tok, "POST", "/hosts/"+id+"/restart", map[string]any{"force": true})
	if r.status != 202 {
		t.Fatalf("restart: %d %s", r.status, r.raw)
	}
	opID, _ := r.body["op_id"].(string)
	var kind, hostID string
	var force bool
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select kind, host_id::text, (params->>'force')::boolean from ops where id = $1", opID).Scan(&kind, &hostID, &force); err != nil {
		t.Fatal(err)
	}
	if kind != "host_restart" || hostID != id || !force {
		t.Fatalf("op %s %s force=%v", kind, hostID, force)
	}
	// A second press while it is open is the same restart.
	if again := e.do(t, tok, "POST", "/hosts/"+id+"/restart", nil); again.status != 202 || again.body["op_id"] != opID {
		t.Fatalf("second press: %d %s, want op %s", again.status, again.raw, opID)
	}
	if g := e.do(t, tok, "GET", "/hosts/"+id+"/ops/"+opID, nil); g.status != 200 || g.body["kind"] != "host_restart" {
		t.Fatalf("host op: %d %s", g.status, g.raw)
	}

	// Another account sees neither the host nor its op.
	other := e.signIn(t, "sub-rs2", "rs2")
	if g := e.do(t, other, "POST", "/hosts/"+id+"/restart", nil); g.status != 404 {
		t.Fatalf("another account's restart: %d", g.status)
	}
	if g := e.do(t, other, "GET", "/hosts/"+id+"/ops/"+opID, nil); g.status != 404 {
		t.Fatalf("another account's op read: %d", g.status)
	}
}
