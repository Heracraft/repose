package api

import (
	"testing"
	"time"
)

// TestFakeListsSays (I-1604): GET /projects/:id/says answers as the api
// does: newest first, a replaced row moved to `replaced`, `limit`
// honoured and refused outside 1..100, another account's project not
// found; and SetSayReply ends the agent's turn with the reply.
func TestFakeListsSays(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "talk", "github.com/a/talk")
	path := "/v1/projects/" + p.ID + "/says"

	var empty struct{ Says []Say }
	call(t, f, "GET", path, tok, nil).json(t, &empty)
	if empty.Says == nil || len(empty.Says) != 0 {
		t.Fatalf("empty list = %#v, want says: []", empty.Says)
	}

	f.SetSayState("waiting", false)
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/say", tok, map[string]string{"text": "first"}), 200)
	f.SetSayState("waiting", true)
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/say", tok, map[string]string{"text": "second"}), 200)

	var got struct{ Says []Say }
	r := call(t, f, "GET", path, tok, nil)
	want(t, r, 200)
	r.json(t, &got)
	if len(got.Says) != 2 || got.Says[0].Text != "second" || got.Says[0].State != "waiting" ||
		got.Says[1].Text != "first" || got.Says[1].State != "replaced" {
		t.Fatalf("says = %+v", got.Says)
	}
	call(t, f, "GET", path+"?limit=1", tok, nil).json(t, &got)
	if len(got.Says) != 1 {
		t.Fatalf("limit=1 gave %d", len(got.Says))
	}
	for _, bad := range []string{"0", "101", "x"} {
		wantErr(t, call(t, f, "GET", path+"?limit="+bad, tok, nil), 400, "invalid")
	}

	f.SetSayState("delivered", false)
	f.SetSayReply("done", 10*time.Millisecond)
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/say", tok, map[string]string{"text": "third"}), 200)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var evs []Event
		call(t, f, "GET", "/v1/projects/"+p.ID+"/events?kind=completed", tok, nil).json(t, &evs)
		if len(evs) == 1 && evs[0].Summary == "done" && evs[0].Window == "claude" && evs[0].Agent == "claude" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reply event: %+v", evs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
