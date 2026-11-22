package api

import (
	"net/http"
	"strings"
	"time"
	"testing"
)

// The fake serves guest_runtime and pending_base_version and refuses what
// the api refuses (DECISIONS I-1462): the other runtime from an account
// that is not exempt, and a Nix file on an erofs machine.
func TestGuestRuntimeFake(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "nixy", "")
	if p.GuestRuntime != "nix" || p.PendingBaseVersion != nil {
		t.Fatalf("default: %+v", p)
	}
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ero", "class": "large", "guest_runtime": "erofs"})
	want(t, r, http.StatusCreated) // billing off: the account is exempt
	var e Project
	r.json(t, &e)
	if e.GuestRuntime != "erofs" {
		t.Fatalf("exempt erofs: %+v", e)
	}
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+e.ID+"/config", tok, map[string]any{"fragment": "{ }"}), http.StatusConflict, "nix_config_retired")
	if err := f.SetPendingBase(e.ID, "2026.11.29.1"); err != nil {
		t.Fatal(err)
	}
	if got := getProject(t, f, tok, e.ID); got.PendingBaseVersion == nil || *got.PendingBaseVersion != "2026.11.29.1" {
		t.Fatalf("pending: %+v", got.PendingBaseVersion)
	}
	f.SetBilling("active")
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ero2", "class": "large", "guest_runtime": "erofs"}), http.StatusForbidden, "forbidden")
	f.SetBilling("off")
	f.SetDefaultGuestRuntime("erofs")
	r = call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ero3", "class": "large"})
	want(t, r, http.StatusCreated)
	r.json(t, &e)
	if e.GuestRuntime != "erofs" {
		t.Fatalf("default erofs: %+v", e)
	}
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ero4", "class": "large", "guest_runtime": "x"}), http.StatusBadRequest, "invalid")
}

// An erofs menu PUT validates apt names and logs ApplyMenu's lines.
func TestErofsMenuFake(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ero", "class": "large", "guest_runtime": "erofs"})
	want(t, r, http.StatusCreated)
	var p Project
	r.json(t, &p)
	path := "/v1/projects/" + p.ID + "/config"
	wantErr(t, call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{{"package": "python312Packages.black"}}}), http.StatusBadRequest, "invalid")
	logOf := func(r resp) string {
		id := opID(t, r)
		return string(call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+id+"/log", tok, nil).body)
	}
	log := logOf(call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{{"id": "postgresql"}, {"package": "python3.12-venv"}}}))
	for _, l := range []string{"installing postgresql", "postgresql ready", "installing python3.12-venv", "python3.12-venv installed"} {
		if !strings.Contains(log, l) {
			t.Fatalf("log lacks %q: %s", l, log)
		}
	}
	if strings.Contains(log, "evaluating configuration") {
		t.Fatalf("erofs log has the Nix build's lines: %s", log)
	}
	log = logOf(call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{{"package": FailMenuPackage}}}))
	for _, l := range []string{"removing postgresql", "postgresql removed", FailMenuPackage + " failed: "} {
		if !strings.Contains(log, l) {
			t.Fatalf("log lacks %q: %s", l, log)
		}
	}
}

// ConfigDelay keeps a config op running while its log streams, then ends
// it in the state it was decided with.
func TestConfigDelay(t *testing.T) {
	f := New(Options{ConfigDelay: 300 * time.Millisecond})
	defer f.Close()
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ero", "class": "large", "guest_runtime": "erofs"})
	want(t, r, http.StatusCreated)
	var p Project
	r.json(t, &p)
	id := opID(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"menu": []map[string]any{{"package": FailMenuPackage}}}))
	op := call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+id, tok, nil)
	if !strings.Contains(string(op.body), `"running"`) || !strings.Contains(string(op.body), "apply_config") {
		t.Fatalf("op at first read: %s", op.body)
	}
	log := string(call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+id+"/log", tok, nil).body)
	if !strings.Contains(log, FailMenuPackage+" failed") || !strings.Contains(log, `{"state":"error"}`) {
		t.Fatalf("streamed log: %s", log)
	}
	if op := call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+id, tok, nil); !strings.Contains(string(op.body), `"error"`) {
		t.Fatalf("op after the delay: %s", op.body)
	}
}
