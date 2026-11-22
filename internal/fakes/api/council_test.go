package api

import (
	"net/http"
	"testing"
	"time"
)

// The states the No-Nix program adds, which the council's second pass
// reviews against this fake (council 2026-11-22 findings 6 and 8, CLI
// C3). Each mirrors the real api.

func mkErofs(t *testing.T, f *Fake, name string) Project {
	t.Helper()
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": name, "class": "large", "guest_runtime": "erofs"})
	want(t, r, http.StatusCreated)
	var p Project
	r.json(t, &p)
	return p
}

// A restore copies the source's runtime (internal/api/http/restore.go,
// store.NormalizeRuntime(src.GuestRuntime)): as a new project from a
// snapshot, and by name after the source is gone.
func TestFakeRestoreKeepsRuntime(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkErofs(t, f, "app")
	r := call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots", tok, nil)
	var snap struct {
		SnapshotID string `json:"snapshot_id"`
		ID         string `json:"id"`
	}
	r.json(t, &snap)
	sid := snap.SnapshotID
	if sid == "" {
		sid = snap.ID
	}
	snaps := call(t, f, "GET", "/v1/projects/"+p.ID+"/snapshots", tok, nil)
	var list []Snapshot
	snaps.json(t, &list)
	if len(list) == 0 {
		t.Fatalf("no snapshot: %s", snaps.body)
	}
	r = call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots/"+list[0].ID+"/restore", tok, map[string]any{"as_new_project": "app-fork"})
	want(t, r, http.StatusAccepted)
	var res struct {
		ProjectID string `json:"project_id"`
	}
	r.json(t, &res)
	if got := getProject(t, f, tok, res.ProjectID); got.GuestRuntime != "erofs" {
		t.Fatalf("restored as new: guest_runtime %q, want erofs", got.GuestRuntime)
	}
	want(t, call(t, f, "DELETE", "/v1/projects/"+p.ID, tok, nil), http.StatusAccepted)
	r = call(t, f, "POST", "/v1/projects/restore", tok, map[string]any{"slug": "app"})
	want(t, r, http.StatusAccepted)
	r.json(t, &res)
	if got := getProject(t, f, tok, res.ProjectID); got.GuestRuntime != "erofs" {
		t.Fatalf("restored by name: guest_runtime %q, want erofs", got.GuestRuntime)
	}
}

// A machine.nix save reaches Nix machines only (internal/api/ops
// personal.go, guest_runtime = 'nix').
func TestFakePersonalSkipsErofs(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	nix := mkProject(t, f, tok, "legacy", "")
	ero := mkErofs(t, f, "app")
	r := call(t, f, "PUT", "/v1/me/config", tok, map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.jq ]; }"})
	want(t, r, http.StatusOK)
	var out struct {
		Projects []struct {
			ProjectID string `json:"project_id"`
		} `json:"projects"`
	}
	r.json(t, &out)
	seen := map[string]bool{}
	for _, c := range out.Projects {
		seen[c.ProjectID] = true
	}
	if !seen[nix.ID] || seen[ero.ID] {
		t.Fatalf("machine.nix fanned out to %v; want the nix project only", out.Projects)
	}
}

// A start boots the pending base: base_version moves to it and the mark
// clears (internal/api/ops onErofsBooted).
func TestFakeStartMovesPendingBase(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkErofs(t, f, "app")
	if err := f.SetPendingBase(p.ID, "2026.11.29.1"); err != nil {
		t.Fatal(err)
	}
	if got := getProject(t, f, tok, p.ID); got.PendingBaseVersion == nil || *got.PendingBaseVersion != "2026.11.29.1" {
		t.Fatalf("pending not set: %+v", got.PendingBaseVersion)
	}
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/stop", tok, map[string]any{"snapshot": false}), http.StatusAccepted)
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/start", tok, nil), http.StatusAccepted)
	got := getProject(t, f, tok, p.ID)
	if got.BaseVersion != "2026.11.29.1" || got.PendingBaseVersion != nil {
		t.Fatalf("after start: base %q pending %v", got.BaseVersion, got.PendingBaseVersion)
	}
}

// POST /certs names a gateway on 127.0.0.1 at a port nothing listens on,
// so a CLI pointed at the fake never dials the production gateway.
func TestFakeGatewayIsLocal(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "gw", "")
	r := call(t, f, "POST", "/v1/certs", tok, map[string]any{"public_key": "ssh-ed25519 AAAAC3 me@laptop", "project_ids": []string{p.ID}})
	want(t, r, http.StatusOK)
	var c struct {
		Gateway struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"gateway"`
	}
	r.json(t, &c)
	if c.Gateway.Host != "127.0.0.1" || c.Gateway.Port != FakeGatewayPort {
		t.Fatalf("gateway %+v, want 127.0.0.1:%d", c.Gateway, FakeGatewayPort)
	}
}

// A config PUT while another config op runs is refused as the api
// refuses it (Engine.Enqueue: 409 conflict "a build is in progress").
func TestFakeConfigRefusedWhileOneRuns(t *testing.T) {
	f := New(Options{ConfigDelay: 400 * time.Millisecond})
	defer f.Close()
	p := mkErofs(t, f, "app")
	path := "/v1/projects/" + p.ID + "/config"
	want(t, call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{{"id": "nats"}}}), http.StatusAccepted)
	wantErr(t, call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{}}), http.StatusConflict, "conflict")
}

// A stale tab (DECISIONS I-1528): base_revision_id other than the current
// revision is 409 menu_stale; the current one, or none, is taken.
func TestFakeMenuStale(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkErofs(t, f, "app")
	path := "/v1/projects/" + p.ID + "/config"
	var cfg struct {
		RevisionID string `json:"revision_id"`
	}
	call(t, f, "GET", path, tok, nil).json(t, &cfg)
	seen := cfg.RevisionID
	want(t, call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{{"id": "nats"}}, "base_revision_id": seen}), http.StatusAccepted)
	r := call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{}, "base_revision_id": seen})
	wantErr(t, r, http.StatusConflict, "menu_stale")
	call(t, f, "GET", path, tok, nil).json(t, &cfg)
	want(t, call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{}, "base_revision_id": cfg.RevisionID}), http.StatusAccepted)
	want(t, call(t, f, "PUT", path, tok, map[string]any{"menu": []map[string]any{{"id": "redis"}}}), http.StatusAccepted)
	// A Nix project takes the field too.
	n := mkProject(t, f, tok, "legacy", "")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+n.ID+"/config", tok, map[string]any{"fragment": "{ }", "base_revision_id": "01900000-0000-7000-8000-000000000001"}), http.StatusConflict, "menu_stale")
}
