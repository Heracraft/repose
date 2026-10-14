package httpapi_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

// TestProjectReadCarriesTheOpInFlight (DECISIONS I-722). Until this, only
// a create's own 201 carried an op id, so `repose run` on a checkout
// whose project already existed had none: its wait fell back to polling
// states, `waitState` returned the moment the state went to `error`, and
// the next statement started the machine. That is how one user action
// produced two ops on teksafari-org (2026-10-13, create error at
// 15:37:07.848Z, start accepted 0.69 s later) and nobody ever read the
// create's error.
//
// The single-project read now carries the op that is in flight; the list
// read does not (a query per project is an N+1).
func TestProjectReadCarriesTheOpInFlight(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-opid", "opid")

	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "izma", "class": "large", "remote_url": "github.com/opid/izma"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	id, _ := r.body["id"].(string)
	opID, _ := r.body["op_id"].(string)
	if id == "" || opID == "" {
		t.Fatalf("create answered no id or op_id: %s", r.raw)
	}

	// The create op is still open: the read names it, and it is the same
	// op the create's own answer named.
	g := e.do(t, tok, "GET", "/projects/"+id, nil)
	if g.status != 200 {
		t.Fatalf("read: %d %s", g.status, g.raw)
	}
	if got, _ := g.body["op_id"].(string); got != opID {
		t.Fatalf("read op_id %q, want the create's %q", got, opID)
	}

	// The list read carries none, whatever is in flight.
	l := e.do(t, tok, "GET", "/projects", nil)
	if l.status != 200 {
		t.Fatalf("list: %d %s", l.status, l.raw)
	}
	if bytes.Contains(l.raw, []byte("op_id")) {
		t.Fatalf("the list carries op_id: %s", l.raw)
	}

	// Once nothing is open the field is gone, so a client cannot mistake
	// a finished op for one to wait on.
	e.h.WaitOp(uuid.MustParse(opID))
	g = e.do(t, tok, "GET", "/projects/"+id, nil)
	if _, ok := g.body["op_id"]; ok {
		t.Fatalf("op_id survived the op: %s", g.raw)
	}
}
