package api

import (
	"strings"
	"testing"
)

// The fake mirrors the api's refused create (DECISIONS I-1648,
// internal/api/http TestCreateRefusedByTheHostReadsAsNotCreated): 201 with
// its op, the op in error with the sentence, no row, the project's read a
// 404 create_refused naming it, and the name free again.
func TestRefusedCreateLeavesNoProject(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	const why = "the host has as many machines running as its cores allow; stopping a running machine frees room"
	f.RefuseNextCreate(why)
	p := mkProject(t, f, tok, "pocketmail", "")
	if p.OpID == "" {
		t.Fatal("the refused create carries no op_id")
	}
	r := call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+p.OpID, tok, nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"state":"error"`) || !strings.Contains(string(r.body), why) {
		t.Fatalf("op: %s", r.body)
	}
	if l := call(t, f, "GET", "/v1/projects", tok, nil); strings.Contains(string(l.body), p.ID) {
		t.Fatalf("listed: %s", l.body)
	}
	g := call(t, f, "GET", "/v1/projects/"+p.ID, tok, nil)
	wantErr(t, g, 404, "not_found")
	if !strings.Contains(string(g.body), `"reason":"create_refused"`) || !strings.Contains(string(g.body), "pocketmail was not created: "+why) {
		t.Fatalf("read: %s", g.body)
	}
	again := mkProject(t, f, tok, "pocketmail", "")
	if got := getProject(t, f, tok, again.ID); got.State != "running" {
		t.Fatalf("second create %s", got.State)
	}
}
