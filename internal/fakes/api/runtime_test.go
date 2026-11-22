package api

import (
	"net/http"
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
	f.SetPendingBase(e.ID, "2026.11.29.1")
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
