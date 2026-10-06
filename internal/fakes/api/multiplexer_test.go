package api

import (
	"net/http"
	"testing"
)

// TestMultiplexerGate: the fake answers the multiplexer field and the
// base gate as api.md documents them (I-502), so the CLI's tests can drive
// every branch of its pick order against it.
func TestMultiplexerGate(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "plain", "")
	if p.Multiplexer != "tmux" {
		t.Fatalf("default multiplexer %q", p.Multiplexer)
	}
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "s", "class": "small", "multiplexer": "screen"}), http.StatusBadRequest, "invalid")
	wantErr(t, call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"multiplexer": "screen"}), http.StatusBadRequest, "invalid")

	gate := func(r resp) map[string]any {
		t.Helper()
		var env struct {
			Error struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Detail  map[string]any `json:"detail"`
			} `json:"error"`
		}
		r.json(t, &env)
		if r.status != http.StatusConflict || env.Error.Code != "conflict" || env.Error.Detail["reason"] != "base_update_needed" {
			t.Fatalf("not the gate: %d %s", r.status, r.body)
		}
		env.Error.Detail["message"] = env.Error.Message
		return env.Error.Detail
	}
	// No min base: herdr refused with needs "", tmux never.
	if d := gate(call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "h", "class": "small", "multiplexer": "herdr"})); d["needs"] != "" || d["message"] != "herdr is not available yet." {
		t.Fatalf("no min base: %v", d)
	}
	if d := gate(call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"multiplexer": "herdr"})); d["needs"] != "" {
		t.Fatalf("PATCH, no min base: %v", d)
	}
	want(t, call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"multiplexer": "tmux"}), http.StatusOK)

	// A min base newer than the project's.
	f.SetHerdrMinBase("2026.10.06")
	d := gate(call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"multiplexer": "herdr"}))
	if d["base_version"] != baseVersion || d["needs"] != "2026.10.06" || d["message"] != "plain runs base "+baseVersion+"; herdr needs 2026.10.06 or newer." {
		t.Fatalf("old base: %v", d)
	}
	// The project moves to the min base: the switch is stored and read back.
	f.SetBaseVersion(p.ID, "2026.10.06")
	r := call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"multiplexer": "herdr"})
	want(t, r, http.StatusOK)
	if got := getProject(t, f, tok, p.ID); got.Multiplexer != "herdr" {
		t.Fatalf("after PATCH: %q", got.Multiplexer)
	}
	// A min base at or before the fake's newest base lets a POST through.
	f.SetHerdrMinBase(baseVersion)
	r = call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "h", "class": "small", "multiplexer": "herdr"})
	want(t, r, http.StatusCreated)
	var h Project
	r.json(t, &h)
	if h.Multiplexer != "herdr" {
		t.Fatalf("POST herdr: %q", h.Multiplexer)
	}
}

// TestMultiplexerCopyGatesOnSourceBase: a fork and a restore as new keep
// the source's base, as the real api's insertRestored does, so a herdr
// source on a base older than the min base gives tmux copies, and one on
// the min base gives herdr copies.
func TestMultiplexerCopyGatesOnSourceBase(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetHerdrMinBase(baseVersion)
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "src", "class": "small", "multiplexer": "herdr"})
	want(t, r, http.StatusCreated)
	var src Project
	r.json(t, &src)
	opID(t, call(t, f, "POST", "/v1/projects/"+src.ID+"/snapshots", tok, nil))
	r = call(t, f, "GET", "/v1/projects/"+src.ID+"/snapshots", tok, nil)
	var snaps []Snapshot
	r.json(t, &snaps)
	sid := snaps[0].ID

	copies := func(tag string) []Project {
		t.Helper()
		r := call(t, f, "POST", "/v1/projects/"+src.ID+"/fork", tok, map[string]any{"snapshot_id": sid, "name": "f-" + tag})
		want(t, r, http.StatusAccepted)
		var forked struct {
			Projects []struct {
				ProjectID string `json:"project_id"`
			} `json:"projects"`
		}
		r.json(t, &forked)
		r = call(t, f, "POST", "/v1/projects/"+src.ID+"/snapshots/"+sid+"/restore", tok, map[string]any{"as_new_project": "r-" + tag})
		want(t, r, http.StatusAccepted)
		var restored struct {
			ProjectID string `json:"project_id"`
		}
		r.json(t, &restored)
		return []Project{getProject(t, f, tok, forked.Projects[0].ProjectID), getProject(t, f, tok, restored.ProjectID)}
	}
	// Source on the min base: both copies keep herdr and the source's base.
	for _, c := range copies("new") {
		if c.Multiplexer != "herdr" || c.BaseVersion != baseVersion {
			t.Fatalf("source on min base: %s got %q on %q", c.Name, c.Multiplexer, c.BaseVersion)
		}
	}
	// Source held on an older base: both copies run that base, so tmux.
	f.SetBaseVersion(src.ID, "2026.01.01")
	for _, c := range copies("old") {
		if c.Multiplexer != "tmux" || c.BaseVersion != "2026.01.01" {
			t.Fatalf("source behind min base: %s got %q on %q", c.Name, c.Multiplexer, c.BaseVersion)
		}
	}
}
