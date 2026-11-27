package httpapi_test

import (
	"strings"
	"testing"
)

// DECISIONS I-1648: a create the host refuses for room leaves no project.
// The create's op says why, as before; the list has no row; the project's
// own read is 404 with detail.reason create_refused and a message that
// says it was not created and why, which a page opened on it shows
// instead of a bare "Not found". The name can be used again at once.
func TestCreateRefusedByTheHostReadsAsNotCreated(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-refused", "refused")
	e.h.Fake.SetFail("CreateGuest", "insufficient_capacity")
	e.h.Fake.SetFailMessage("CreateGuest", "not enough free CPU: 8 of 8 vCPUs taken by running machines on this host, a machine starts with 2")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "pocketmail", "class": "small"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	op := e.waitOp(t, r)
	if op.State != "error" {
		t.Fatalf("op %s", op.State)
	}
	o := e.do(t, tok, "GET", "/projects/"+pid+"/ops/"+op.ID.String(), nil)
	oe, _ := o.body["error"].(map[string]any)
	if o.status != 200 || oe["code"] != "insufficient_capacity" || !strings.Contains(oe["message"].(string), "running as many machines as it can") {
		t.Fatalf("op read: %d %s", o.status, o.raw)
	}
	if l := e.do(t, tok, "GET", "/projects", nil); strings.Contains(string(l.raw), pid) {
		t.Fatalf("the refused project is listed: %s", l.raw)
	}
	g := e.do(t, tok, "GET", "/projects/"+pid, nil)
	ge, _ := g.body["error"].(map[string]any)
	det, _ := ge["detail"].(map[string]any)
	if g.status != 404 || errCode(g) != "not_found" || det["reason"] != "create_refused" ||
		ge["message"] != "pocketmail was not created: its host is running as many machines as it can. Stop a machine there" {
		t.Fatalf("project read: %d %s", g.status, g.raw)
	}
	e.h.Fake.SetFail("CreateGuest", "")
	again := e.do(t, tok, "POST", "/projects", map[string]any{"name": "pocketmail", "class": "small"})
	if again.status != 201 {
		t.Fatalf("second create: %d %s", again.status, again.raw)
	}
	if op := e.waitOp(t, again); op.State != "done" {
		t.Fatalf("second create op %s %+v", op.State, op.Error)
	}
}
