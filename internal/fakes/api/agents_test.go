package api

import "testing"

// TestFakeStartsAgentsAsTheApi: POST /projects/{id}/agents and a create
// with start_agent, with the api's answers and refusals (I-1702).
func TestFakeStartsAgentsAsTheApi(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	var p Project
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "dash", "class": "small", "agent_default": "codex", "start_agent": true})
	want(t, r, 201)
	r.json(t, &p)
	if p.OpID == "" || p.Signals == nil || len(p.Signals.Agents) != 1 || p.Signals.Agents[0].Agent != "codex" {
		t.Fatalf("create with start_agent: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/projects/"+p.ID+"/agents", tok, nil)
	want(t, r, 202)
	var op struct {
		OpID  string `json:"op_id"`
		Agent string `json:"agent"`
	}
	r.json(t, &op)
	var o Op
	call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+op.OpID, tok, nil).json(t, &o)
	if op.Agent != "codex" || o.Result["existed"] != true {
		t.Fatalf("the default agent again: %+v %+v", op, o)
	}
	r = call(t, f, "POST", "/v1/projects/"+p.ID+"/agents", tok, map[string]any{"agent": "codex", "remote_control": true})
	wantErr(t, r, 400, "invalid")
	if detailOf(t, r)["reason"] != "remote_control" {
		t.Fatalf("detail %v", detailOf(t, r))
	}
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/stop", tok, nil), 202)
	r = call(t, f, "POST", "/v1/projects/"+p.ID+"/agents", tok, nil)
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "not_running" {
		t.Fatalf("detail %v", detailOf(t, r))
	}
}
