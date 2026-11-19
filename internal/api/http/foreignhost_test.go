package httpapi_test

import (
	"testing"
)

// TestRouteCarriesTheHostOwner (DECISIONS I-1368): GET /internal/route
// tells the gateway who owns the project's host, null for a host repose
// runs, and whose project it is, so the gateway can refuse a route to a
// host the connecting account does not own. Probe: without the fields
// this test fails with "route carries no host_owner_id".
func TestRouteCarriesTheHostOwner(t *testing.T) {
	e := newEnv(t)
	u := e.h.NewUser("alice-dev")
	p := e.h.CreateRunning(u, "todo-app")
	pid := p.ID.String()
	route := func() map[string]any {
		ir := e.internalDo(t, "GET", "/internal/route?login=todo-app.alice-dev", nil)
		if ir.status != 200 {
			t.Fatalf("route: %d %s", ir.status, ir.raw)
		}
		return ir.body
	}
	r := route()
	if _, ok := r["host_owner_id"]; !ok {
		t.Fatalf("route carries no host_owner_id: %v", r)
	}
	if r["host_owner_id"] != nil {
		t.Fatalf("a host repose runs has an owner: %v", r["host_owner_id"])
	}
	if r["user_id"] != u.ID.String() {
		t.Fatalf("user_id %v, want %s", r["user_id"], u.ID)
	}
	// The owner's own host.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set owner_account_id = $1 where id = (select host_id from projects where id = $2)", u.ID, pid); err != nil {
		t.Fatal(err)
	}
	if r := route(); r["host_owner_id"] != u.ID.String() {
		t.Fatalf("host_owner_id %v, want %s", r["host_owner_id"], u.ID)
	}
	// The project_id form carries the same two fields.
	ir := e.internalDo(t, "GET", "/internal/route?project_id="+pid, nil)
	if ir.status != 200 || ir.body["host_owner_id"] != u.ID.String() || ir.body["user_id"] != u.ID.String() {
		t.Fatalf("route by id: %d %s", ir.status, ir.raw)
	}
}
