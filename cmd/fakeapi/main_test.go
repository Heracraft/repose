package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/heracraft/repose/internal/fakes/api"
)

func post(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res
}

func apiCall(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

// The admin listener reaches the No-Nix states (council 2026-11-22,
// finding 8): POST /runtime makes a billed customer's new machines
// Ubuntu, and POST /pending-base sets a project's waiting base.
func TestAdminRuntimeAndPendingBase(t *testing.T) {
	f := api.New(api.Options{Billing: true})
	defer f.Close()
	admin, err := newAdminServer(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	if res := post(t, admin.URL+"/runtime", map[string]string{"default": "ubuntu"}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("/runtime with a bad value: %d", res.StatusCode)
	}
	if res := post(t, admin.URL+"/runtime", map[string]string{"default": "erofs"}); res.StatusCode != http.StatusOK {
		t.Fatalf("/runtime: %d", res.StatusCode)
	}
	var p struct {
		ID                 string  `json:"id"`
		GuestRuntime       string  `json:"guest_runtime"`
		PendingBaseVersion *string `json:"pending_base_version"`
	}
	if code := apiCall(t, "POST", f.URL()+"/v1/projects", map[string]any{"name": "app", "class": "small"}, &p); code != http.StatusCreated || p.GuestRuntime != "erofs" {
		t.Fatalf("a billed create after /runtime erofs: %d %+v", code, p)
	}
	if res := post(t, admin.URL+"/pending-base", map[string]string{"project_id": p.ID, "version": "2026.11.29.1"}); res.StatusCode != http.StatusOK {
		t.Fatalf("/pending-base: %d", res.StatusCode)
	}
	apiCall(t, "GET", f.URL()+"/v1/projects/"+p.ID, nil, &p)
	if p.PendingBaseVersion == nil || *p.PendingBaseVersion != "2026.11.29.1" {
		t.Fatalf("pending after /pending-base: %+v", p.PendingBaseVersion)
	}
	if res := post(t, admin.URL+"/pending-base", map[string]string{"project_id": "nope", "version": "x"}); res.StatusCode != http.StatusNotFound {
		t.Fatalf("/pending-base on an unknown project: %d", res.StatusCode)
	}
}
