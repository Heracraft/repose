package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/apidoc"
)

const tok = "test-token"

type resp struct {
	status int
	body   []byte
	header http.Header
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
}

func (r resp) errCode(t *testing.T) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	r.json(t, &env)
	if env.Error.Code == "" || env.Error.Message == "" {
		t.Fatalf("no error envelope in %q", r.body)
	}
	return env.Error.Code
}

func call(t *testing.T, f *Fake, method, path, token string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(b)
		}
	}
	req, err := http.NewRequest(method, f.URL()+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Errorf("%s %s: no X-Request-Id", method, path)
	}
	return resp{status: res.StatusCode, body: b, header: res.Header}
}

func want(t *testing.T, r resp, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d: %s", r.status, status, r.body)
	}
}

func wantErr(t *testing.T, r resp, status int, code string) {
	t.Helper()
	want(t, r, status)
	if got := r.errCode(t); got != code {
		t.Fatalf("code %q, want %q: %s", got, code, r.body)
	}
}

func mkProject(t *testing.T, f *Fake, token, name, remote string) Project {
	t.Helper()
	r := call(t, f, "POST", "/v1/projects", token, map[string]any{"name": name, "remote_url": remote, "class": "large"})
	want(t, r, http.StatusCreated)
	var p Project
	r.json(t, &p)
	return p
}

func getProject(t *testing.T, f *Fake, token, id string) Project {
	t.Helper()
	r := call(t, f, "GET", "/v1/projects/"+id, token, nil)
	want(t, r, http.StatusOK)
	var p Project
	r.json(t, &p)
	return p
}

func opID(t *testing.T, r resp) string {
	t.Helper()
	want(t, r, http.StatusAccepted)
	var v struct {
		OpID string `json:"op_id"`
	}
	r.json(t, &v)
	if v.OpID == "" {
		t.Fatalf("no op_id in %s", r.body)
	}
	return v.OpID
}

func TestRoutesMatchDoc(t *testing.T) {
	routes, err := apidoc.Load()
	if err != nil {
		t.Fatal(err)
	}
	f := New(Options{})
	defer f.Close()
	documented := map[string]bool{}
	for _, r := range routes {
		documented[apidoc.Pattern(r)] = true
	}
	registered := map[string]bool{}
	for _, p := range f.Routes() {
		if registered[p] {
			t.Errorf("registered twice: %s", p)
		}
		registered[p] = true
	}
	for p := range documented {
		if !registered[p] {
			t.Errorf("documented but not registered: %s", p)
		}
	}
	for p := range registered {
		if !documented[p] {
			t.Errorf("registered but not documented: %s", p)
		}
	}
	if len(documented) != len(registered) {
		t.Fatalf("%d documented, %d registered", len(documented), len(registered))
	}
	t.Logf("%d routes documented, %d registered", len(documented), len(registered))
}

func TestWalkthrough(t *testing.T) {
	f := New(Options{})
	defer f.Close()

	// Unauthenticated user routes answer the envelope.
	wantErr(t, call(t, f, "GET", "/v1/projects", "", nil), 401, "unauthenticated")
	wantErr(t, call(t, f, "GET", "/v1/nope", tok, nil), 404, "not_found")

	// Create.
	p := mkProject(t, f, tok, "todo-app", "github.com/heracraft/todo-app")
	if p.ID != "01900000-0000-7000-8000-000000000001" {
		t.Fatalf("first id %q is not deterministic", p.ID)
	}
	if p.State != "running" || p.HostID == "" || !strings.HasPrefix(p.GuestIP, "10.64.4.") || p.Slug != "todo-app" {
		t.Fatalf("created project: %+v", p)
	}
	if p.ConfigRevisionID == "" || p.VolumeBytes == 0 || p.Signals == nil || p.StartedAt == nil {
		t.Fatalf("missing fields: %+v", p)
	}
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "todo-app", "class": "small"}), 409, "conflict")
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "other", "remote_url": "github.com/heracraft/todo-app", "class": "small"}), 409, "conflict")
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "bad name!", "class": "small"}), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ok", "class": "huge"}), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "ok", "class": "small", "colour": "red"}), 400, "invalid")

	var list []Project
	r := call(t, f, "GET", "/v1/projects", tok, nil)
	want(t, r, 200)
	r.json(t, &list)
	if len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("list: %+v", list)
	}
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/route", tok, nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"guest_ip":"`+p.GuestIP+`"`) {
		t.Fatalf("route: %s", r.body)
	}

	// Secrets: names only, never values.
	val := base64.StdEncoding.EncodeToString([]byte("hunter2"))
	want(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/secrets/OPENAI_API_KEY", tok, map[string]any{"value": val}), 200)
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/secrets/lower", tok, map[string]any{"value": val}), 400, "invalid")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/secrets/user_ca.pub", tok, map[string]any{"value": val}), 400, "invalid")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/secrets/X", tok, map[string]any{"value": "not base64!"}), 400, "invalid")
	big := base64.StdEncoding.EncodeToString(make([]byte, 64<<10+1))
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/secrets/BIG", tok, map[string]any{"value": big}), 400, "invalid")
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/secrets", tok, nil)
	want(t, r, 200)
	var secrets []map[string]any
	r.json(t, &secrets)
	if len(secrets) != 1 || secrets[0]["name"] != "OPENAI_API_KEY" {
		t.Fatalf("secrets: %s", r.body)
	}
	if _, has := secrets[0]["value"]; has || strings.Contains(string(r.body), "hunter2") || strings.Contains(string(r.body), val) {
		t.Fatalf("secret value leaked: %s", r.body)
	}
	want(t, call(t, f, "DELETE", "/v1/projects/"+p.ID+"/secrets/OPENAI_API_KEY", tok, nil), 204)
	wantErr(t, call(t, f, "DELETE", "/v1/projects/"+p.ID+"/secrets/OPENAI_API_KEY", tok, nil), 404, "not_found")

	// Certificates.
	r = call(t, f, "POST", "/v1/certs", tok, map[string]any{"public_key": "ssh-ed25519 AAAAC3 me@laptop", "project_ids": []string{p.ID}})
	want(t, r, 200)
	var certRes struct {
		Certificate string    `json:"certificate"`
		Serial      uint64    `json:"serial"`
		ExpiresAt   time.Time `json:"expires_at"`
		Gateway     struct {
			Host      string `json:"host"`
			Port      int    `json:"port"`
			HostCAPub string `json:"host_ca_pub"`
		} `json:"gateway"`
	}
	r.json(t, &certRes)
	if !strings.HasPrefix(certRes.Certificate, "ssh-ed25519-cert-v01@openssh.com ") || !strings.HasSuffix(certRes.Certificate, "serial 1") {
		t.Fatalf("certificate: %q", certRes.Certificate)
	}
	if certRes.Gateway.Host != "ssh.repose.herakraft.co" || certRes.Gateway.Port != 22 || !strings.HasPrefix(certRes.Gateway.HostCAPub, "ssh-ed25519 ") {
		t.Fatalf("gateway: %+v", certRes.Gateway)
	}
	if d := time.Until(certRes.ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("expires_at %v is not 24h ahead", certRes.ExpiresAt)
	}
	wantErr(t, call(t, f, "POST", "/v1/certs", tok, map[string]any{"public_key": "rsa junk", "project_ids": []string{p.ID}}), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/certs", tok, map[string]any{"public_key": "ssh-ed25519 AAAA", "project_ids": []string{}}), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/certs", tok, map[string]any{"public_key": "ssh-ed25519 AAAA", "project_ids": []string{"01900000-0000-7000-8000-000000009999"}}), 404, "not_found")

	r = call(t, f, "GET", "/v1/internal/revoked", "", nil)
	want(t, r, 200)
	if strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("revoked before revoke: %s", r.body)
	}
	wantErr(t, call(t, f, "POST", "/v1/certs/revoke", tok, map[string]any{}), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/certs/revoke", tok, map[string]any{"serial": 999}), 404, "not_found")
	want(t, call(t, f, "POST", "/v1/certs/revoke", tok, map[string]any{"serial": certRes.Serial}), 200)
	r = call(t, f, "GET", "/v1/internal/revoked?since="+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "", nil)
	want(t, r, 200)
	var revoked []uint64
	r.json(t, &revoked)
	if len(revoked) != 1 || revoked[0] != certRes.Serial {
		t.Fatalf("revoked: %s", r.body)
	}
	want(t, call(t, f, "POST", "/v1/certs", tok, map[string]any{"public_key": "ssh-ed25519 AAAA", "project_ids": []string{p.ID}}), 200)
	want(t, call(t, f, "POST", "/v1/certs/revoke", tok, map[string]any{"all": true}), 200)
	r = call(t, f, "GET", "/v1/internal/revoked", "", nil)
	r.json(t, &revoked)
	if len(revoked) != 2 {
		t.Fatalf("revoked after all: %s", r.body)
	}

	// Stop, start, destroy.
	id := opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/stop", tok, nil))
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+id, tok, nil)
	want(t, r, 200)
	var o Op
	r.json(t, &o)
	if o.State != "done" || o.Error != "" {
		t.Fatalf("op: %+v", o)
	}
	p = getProject(t, f, tok, p.ID)
	if p.State != "stopped" || p.GuestIP != "" || p.LastSnapshotAt == nil || p.Signals != nil {
		t.Fatalf("after stop: %+v", p)
	}
	var snaps []Snapshot
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/snapshots", tok, nil)
	want(t, r, 200)
	r.json(t, &snaps)
	if len(snaps) != 1 || snaps[0].Reason != "stop" {
		t.Fatalf("snapshots after stop: %+v", snaps)
	}
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/01900000-0000-7000-8000-000000009999", tok, nil), 404, "not_found")

	// Class change while stopped is allowed, while running it is not.
	want(t, call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"class": "xl", "hold_base_updates": true}), 200)
	opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/start", tok, nil))
	p = getProject(t, f, tok, p.ID)
	if p.State != "running" || p.GuestIP == "" || p.Class != "xl" || !p.HoldBaseUpdates {
		t.Fatalf("after start: %+v", p)
	}
	wantErr(t, call(t, f, "PATCH", "/v1/projects/"+p.ID, tok, map[string]any{"class": "small"}), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/resize", tok, map[string]any{"volume_bytes": 1}), 400, "invalid")
	opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/resize", tok, map[string]any{"volume_bytes": p.VolumeBytes * 2}))

	opID(t, call(t, f, "DELETE", "/v1/projects/"+p.ID, tok, nil))
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID, tok, nil), 404, "not_found")
	r = call(t, f, "GET", "/v1/projects", tok, nil)
	want(t, r, 200)
	if strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("list after destroy: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/snapshots", tok, nil)
	want(t, r, 200)
	r.json(t, &snaps)
	if len(snaps) != 2 || snaps[1].Reason != "stop" || snaps[1].ExpiresAt == nil {
		t.Fatalf("snapshots kept after destroy: %+v", snaps)
	}
	// Listed as restorable (I-167).
	r = call(t, f, "GET", "/v1/projects/destroyed", tok, nil)
	want(t, r, 200)
	var gone []DestroyedProject
	r.json(t, &gone)
	if len(gone) != 1 || gone[0].Slug != "todo-app" || !gone[0].NameFree || gone[0].Snapshot.ID != snaps[1].ID || gone[0].RestorableUntil == nil {
		t.Fatalf("destroyed list: %s", r.body)
	}
	// The name is free again.
	mkProject(t, f, tok, "todo-app", "github.com/heracraft/todo-app")
	// Restoring by name now meets the live todo-app, which has its own
	// snapshot-less history: nothing to restore under that name...
	wantErr(t, call(t, f, "POST", "/v1/projects/restore", tok, map[string]any{"slug": "todo-app"}), 404, "not_found")
	// ...but the destroyed one's id restores under another name.
	r = call(t, f, "POST", "/v1/projects/restore", tok, map[string]any{"project_id": p.ID})
	wantErr(t, r, 409, "conflict")
	r = call(t, f, "POST", "/v1/projects/restore", tok, map[string]any{"project_id": p.ID, "name": "todo-back"})
	want(t, r, 202)
	var res struct {
		OpID       string `json:"op_id"`
		ProjectID  string `json:"project_id"`
		Slug       string `json:"slug"`
		SnapshotID string `json:"snapshot_id"`
	}
	r.json(t, &res)
	if res.OpID == "" || res.Slug != "todo-back" || res.SnapshotID != snaps[1].ID {
		t.Fatalf("restore by id: %s", r.body)
	}
}

func TestOpLogSSE(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "sse", "")
	id := opID(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"fragment": "{ }"}))
	path := "/v1/projects/" + p.ID + "/ops/" + id + "/log"

	r := call(t, f, "GET", path, tok, nil)
	want(t, r, 200)
	if ct := r.header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	body := string(r.body)
	for _, wantLine := range []string{"id: 1\n", "id: 2\n", "id: 3\n", `"line":"evaluating configuration"`, `"line":"building"`, `"line":"built"`, "event: done\n"} {
		if !strings.Contains(body, wantLine) {
			t.Fatalf("missing %q in %q", wantLine, body)
		}
	}
	if !strings.HasSuffix(body, "event: done\ndata: {\"state\":\"done\"}\n\n") {
		t.Fatalf("done is not last: %q", body)
	}

	r = call(t, f, "GET", path+"?since=2", tok, nil)
	want(t, r, 200)
	body = string(r.body)
	if strings.Contains(body, "id: 1\n") || strings.Contains(body, "id: 2\n") || !strings.Contains(body, "id: 3\n") {
		t.Fatalf("since=2: %q", body)
	}
	wantErr(t, call(t, f, "GET", path+"?since=x", tok, nil), 400, "invalid")

	// EventSource cannot set headers: access_token works here and only here.
	want(t, call(t, f, "GET", path+"?access_token="+tok, "", nil), 200)
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID+"?access_token="+tok, "", nil), 401, "unauthenticated")
	wantErr(t, call(t, f, "GET", path, "", nil), 401, "unauthenticated")

	// Ops of a destroyed project keep their log.
	opID(t, call(t, f, "DELETE", "/v1/projects/"+p.ID, tok, nil))
	want(t, call(t, f, "GET", path, tok, nil), 200)
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/ops/"+id, tok, nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"log_url":"`+f.URL()+path+`"`) {
		t.Fatalf("log_url: %s", r.body)
	}
}

func TestErrorSwitch(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "sw", "")

	f.FailNext("GET", "/v1/projects/"+p.ID, "capacity")
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID, tok, nil), 503, "capacity")
	want(t, call(t, f, "GET", "/v1/projects/"+p.ID, tok, nil), 200)

	f.FailNext("GET", "/projects/:id", "payment_required")
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID, tok, nil), 402, "payment_required")
	want(t, call(t, f, "GET", "/v1/projects/"+p.ID, tok, nil), 200)

	f.Fail("POST /v1/certs", "rate_limited")
	wantErr(t, call(t, f, "POST", "/v1/certs", tok, nil), 429, "rate_limited")
	wantErr(t, call(t, f, "POST", "/v1/certs", tok, nil), 429, "rate_limited")
	f.Unfail("POST /certs")
	wantErr(t, call(t, f, "POST", "/v1/certs", tok, nil), 400, "invalid")

	// The switch fires before auth, so a dashboard can rehearse 500s.
	f.Fail("GET /v1/projects/{id}", "internal")
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID, "", nil), 500, "internal")
	f.Unfail("GET /v1/projects/{id}")
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID, "", nil), 401, "unauthenticated")

	f.FailNext("GET", "/v1/me", "forbidden")
	wantErr(t, call(t, f, "GET", "/v1/me", tok, nil), 403, "forbidden")
	f.FailNext("GET", "/v1/me", "not_found")
	wantErr(t, call(t, f, "GET", "/v1/me", tok, nil), 404, "not_found")
}

func TestCrossUser(t *testing.T) {
	alice := User{ID: "00000000-0000-7000-8000-00000000000a", Handle: "alice", Email: "alice@example.com", GitHubLogin: "alice"}
	bob := User{ID: "00000000-0000-7000-8000-00000000000b", Handle: "bob", Email: "bob@example.com", GitHubLogin: "bob"}
	f := New(Options{Users: map[string]User{"tok-alice": alice, "tok-bob": bob}})
	defer f.Close()

	wantErr(t, call(t, f, "GET", "/v1/me", "stranger", nil), 401, "unauthenticated")
	r := call(t, f, "GET", "/v1/me", "tok-alice", nil)
	want(t, r, 200)
	var me User
	r.json(t, &me)
	if me.Handle != "alice" {
		t.Fatalf("me: %+v", me)
	}

	p := mkProject(t, f, "tok-alice", "shared-name", "github.com/alice/x")
	// Same name for another user is fine; nothing of alice's is visible to bob.
	mkProject(t, f, "tok-bob", "shared-name", "github.com/alice/x")
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID, "tok-bob", nil), 404, "not_found")
	wantErr(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/stop", "tok-bob", nil), 404, "not_found")
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID+"/secrets", "tok-bob", nil), 404, "not_found")
	wantErr(t, call(t, f, "DELETE", "/v1/projects/"+p.ID, "tok-bob", nil), 404, "not_found")
	wantErr(t, call(t, f, "POST", "/v1/certs", "tok-bob", map[string]any{"public_key": "ssh-ed25519 AAAA", "project_ids": []string{p.ID}}), 404, "not_found")
	r = call(t, f, "POST", "/v1/certs", "tok-alice", map[string]any{"public_key": "ssh-ed25519 AAAA", "project_ids": []string{p.ID}})
	want(t, r, 200)
	var c struct {
		Serial uint64 `json:"serial"`
	}
	r.json(t, &c)
	wantErr(t, call(t, f, "POST", "/v1/certs/revoke", "tok-bob", map[string]any{"serial": c.Serial}), 404, "not_found")

	// The gateway resolves the login of each.
	r = call(t, f, "GET", "/v1/internal/route?login=shared-name.alice", "", nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"project_id":"`+p.ID+`"`) {
		t.Fatalf("route: %s", r.body)
	}
	want(t, call(t, f, "GET", "/v1/internal/route?login=shared-name.bob", "", nil), 200)
	wantErr(t, call(t, f, "GET", "/v1/internal/route?login=shared-name.carol", "", nil), 404, "not_found")
}

func TestConfig(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "cfg", "")

	r := call(t, f, "GET", "/v1/projects/"+p.ID+"/config", tok, nil)
	want(t, r, 200)
	var cfg struct {
		RevisionID  string `json:"revision_id"`
		Fragment    string `json:"fragment"`
		BaseVersion string `json:"base_version"`
		AppliedAt   string `json:"applied_at"`
	}
	r.json(t, &cfg)
	if cfg.RevisionID != p.ConfigRevisionID || cfg.BaseVersion == "" || cfg.AppliedAt == "" {
		t.Fatalf("initial config: %s", r.body)
	}

	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{}), 400, "invalid")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"fragment": "x", "menu": map[string]any{}}), 400, "invalid")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"fragment": strings.Repeat("x", 256<<10+1)}), 400, "invalid")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"menu": []map[string]any{{"id": "nosuchpkg"}}}), 400, "invalid")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"menu": []map[string]any{{"package": "a;b"}}}), 400, "invalid")
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, `{"fragment": "x", "extra": 1}`), 400, "invalid")

	// The menu, with the real catalog and renderer (internal/menu), a
	// catalog entry and a nixpkgs package (DECISIONS I-220).
	r = call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"menu": []map[string]any{{"id": "bun"}, {"id": "postgresql"}, {"package": "gcc"}}})
	want(t, r, 202)
	var put struct {
		RevisionID string `json:"revision_id"`
		OpID       string `json:"op_id"`
	}
	r.json(t, &put)
	if put.RevisionID == "" || put.OpID == "" {
		t.Fatalf("put: %s", r.body)
	}
	first := put.RevisionID
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/config", tok, nil)
	want(t, r, 200)
	body := string(r.body)
	if !strings.Contains(body, `"menu":[{"id":"bun"},{"id":"postgresql"},{"package":"gcc"}]`) || !strings.Contains(body, "services.postgresql") || !strings.Contains(body, `(nixpkg [ \"gcc\" ])`) {
		t.Fatalf("menu config: %s", body)
	}

	// A hand-written fragment takes over; the menu then answers conflict.
	r = call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.bun ]; }"})
	want(t, r, 202)
	wantErr(t, call(t, f, "PUT", "/v1/projects/"+p.ID+"/config", tok, map[string]any{"menu": []map[string]any{{"id": "bun"}}}), 409, "conflict")

	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/config/revisions", tok, nil)
	want(t, r, 200)
	var revs []Revision
	r.json(t, &revs)
	if len(revs) != 3 || revs[1].ID != first || revs[2].Status != "applied" {
		t.Fatalf("revisions: %s", r.body)
	}
	opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/config/revisions/"+first+"/apply", tok, nil))
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/config", tok, nil)
	r.json(t, &cfg)
	if cfg.RevisionID != first || !strings.Contains(string(r.body), `"menu"`) {
		t.Fatalf("after apply: %s", r.body)
	}
	wantErr(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/config/revisions/nope/apply", tok, nil), 404, "not_found")

	r = call(t, f, "GET", "/v1/catalog", tok, nil)
	want(t, r, 200)
	var cat []CatalogItem
	r.json(t, &cat)
	if len(cat) < 3 || cat[0].ID == "" || cat[0].Label == "" || cat[0].Group == "" || cat[0].Description == "" {
		t.Fatalf("catalog: %s", r.body)
	}
}

func TestMe(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	r := call(t, f, "GET", "/v1/me", tok, nil)
	want(t, r, 200)
	var me struct {
		ID      string `json:"id"`
		Handle  string `json:"handle"`
		Email   string `json:"email"`
		TZ      string `json:"tz"`
		Billing struct {
			Status string `json:"status"`
		} `json:"billing"`
		Limits struct {
			Projects int `json:"projects"`
			XL       int `json:"xl"`
		} `json:"limits"`
	}
	r.json(t, &me)
	if me.ID != CannedUser.ID || me.Handle != "heracraft" || me.Email != "dev@example.com" || me.Billing.Status != "exempt" || me.Limits.Projects != ProjectCap || me.Limits.XL != 1 {
		t.Fatalf("me: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/me/notify-test", tok, nil)
	want(t, r, 200)
	if string(bytes.TrimSpace(r.body)) != `{"email":"ok"}` {
		t.Fatalf("notify-test: %s", r.body)
	}
	wantErr(t, call(t, f, "PATCH", "/v1/me", tok, map[string]any{"tz": "Mars/Olympus"}), 400, "invalid")
	wantErr(t, call(t, f, "PATCH", "/v1/me", tok, map[string]any{"handle": "x"}), 400, "invalid")
	r = call(t, f, "PATCH", "/v1/me", tok, map[string]any{"tz": "Europe/Paris", "notify": map[string]any{"email": false, "ntfy_url": "https://ntfy.sh/repose"}})
	want(t, r, 200)
	r.json(t, &me)
	if me.TZ != "Europe/Paris" {
		t.Fatalf("tz: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/me/notify-test", tok, nil)
	if string(bytes.TrimSpace(r.body)) != `{"ntfy":"ok"}` {
		t.Fatalf("notify-test: %s", r.body)
	}
	want(t, call(t, f, "PATCH", "/v1/me", tok, `{"notify": {"ntfy_url": null}}`), 200)
	r = call(t, f, "POST", "/v1/me/notify-test", tok, nil)
	if string(bytes.TrimSpace(r.body)) != `{}` {
		t.Fatalf("notify-test after clearing: %s", r.body)
	}

	p := mkProject(t, f, tok, "cancel-me", "")
	want(t, call(t, f, "DELETE", "/v1/me", tok, nil), 200)
	p = getProject(t, f, tok, p.ID)
	if p.State != "stopped" {
		t.Fatalf("after DELETE /me: %+v", p)
	}
}

func TestInternal(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "gw", "")

	r := call(t, f, "GET", "/v1/internal/ca", "", nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"user_ca_pub":"ssh-ed25519 `) || !strings.Contains(string(r.body), `"host_ca_pub":"ssh-ed25519 `) {
		t.Fatalf("ca: %s", r.body)
	}
	r = call(t, f, "GET", "/v1/internal/hosts", "", nil)
	want(t, r, 200)
	var hosts []Host
	r.json(t, &hosts)
	if len(hosts) != 1 || hosts[0].HostID != p.HostID || hosts[0].GuestCIDR == "" || hosts[0].WGPubkey == "" {
		t.Fatalf("hosts: %s", r.body)
	}

	r = call(t, f, "GET", "/v1/internal/route?login=gw.heracraft", "", nil)
	want(t, r, 200)
	var route struct {
		ProjectID  string   `json:"project_id"`
		GuestIP    string   `json:"guest_ip"`
		State      string   `json:"state"`
		Principals []string `json:"principals"`
	}
	r.json(t, &route)
	if route.ProjectID != p.ID || route.GuestIP != p.GuestIP || route.State != "running" || len(route.Principals) != 1 || route.Principals[0] != p.ID {
		t.Fatalf("route: %s", r.body)
	}
	wantErr(t, call(t, f, "GET", "/v1/internal/route?login=nodot", "", nil), 400, "invalid")
	wantErr(t, call(t, f, "GET", "/v1/internal/route?login=nope.heracraft", "", nil), 404, "not_found")

	want(t, call(t, f, "POST", "/v1/internal/sessions", "", map[string]any{"project_id": p.ID, "event": "opened", "cert_serial": 1}), 200)
	want(t, call(t, f, "POST", "/v1/internal/sessions", "", map[string]any{"project_id": p.ID, "event": "opened", "cert_serial": 2}), 200)
	want(t, call(t, f, "POST", "/v1/internal/sessions", "", map[string]any{"project_id": p.ID, "event": "closed", "cert_serial": 1}), 200)
	wantErr(t, call(t, f, "POST", "/v1/internal/sessions", "", map[string]any{"project_id": p.ID, "event": "paused", "cert_serial": 1}), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/internal/sessions", "", map[string]any{"project_id": "nope", "event": "opened", "cert_serial": 1}), 404, "not_found")
	p = getProject(t, f, tok, p.ID)
	if p.Signals == nil || p.Signals.SSHSessions != 1 {
		t.Fatalf("signals: %+v", p.Signals)
	}

	r = call(t, f, "POST", "/v1/internal/gateway-certs", "", map[string]any{"public_key": "ssh-ed25519 AAAA gw", "project_id": p.ID})
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"certificate":"ssh-ed25519-cert-v01@openssh.com `) || !strings.Contains(string(r.body), ":via-gateway") {
		t.Fatalf("gateway cert: %s", r.body)
	}
	wantErr(t, call(t, f, "POST", "/v1/internal/gateway-certs", "", map[string]any{"public_key": "ssh-ed25519 AAAA", "project_id": "nope"}), 404, "not_found")

	ev := map[string]any{"source_ip": p.GuestIP, "agent": "claude", "kind": "agent.waiting", "summary": "needs input"}
	r = call(t, f, "POST", "/v1/internal/events", "", ev)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"deduped":false`) {
		t.Fatalf("event: %s", r.body)
	}
	r = call(t, f, "POST", "/v1/internal/events", "", ev)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"deduped":true`) {
		t.Fatalf("duplicate event: %s", r.body)
	}
	wantErr(t, call(t, f, "POST", "/v1/internal/events", "", map[string]any{"source_ip": "10.99.99.99", "agent": "claude", "kind": "x", "summary": "y"}), 404, "not_found")
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/events", tok, nil)
	want(t, r, 200)
	var events []Event
	r.json(t, &events)
	var hook []Event
	for _, e := range events {
		if e.Kind == "agent.waiting" {
			hook = append(hook, e)
		}
	}
	if len(hook) != 1 || hook[0].Agent != "claude" {
		t.Fatalf("events: %s", r.body)
	}
	newest := events[0].TS
	for _, e := range events {
		if e.TS.After(newest) {
			newest = e.TS
		}
	}
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/events?since="+newest.Format(time.RFC3339Nano), tok, nil)
	want(t, r, 200)
	if strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("events since last: %s", r.body)
	}
	// since is a time or nothing, as on the api (I-609).
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID+"/events?since="+hook[0].ID, tok, nil), 400, "invalid")
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID+"/logs?since=7d", tok, nil), 400, "invalid")
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/logs?kind=ops", tok, nil)
	want(t, r, 200)
	for _, l := range strings.Split(strings.TrimSpace(string(r.body)), "\n") {
		if l == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(l), &line); err != nil {
			t.Fatal(err)
		}
		if line["op_id"] == nil || line["state"] == nil || line["line"] != nil {
			t.Fatalf("ops line is not the api's shape: %s", l)
		}
	}
	r = call(t, f, "GET", "/v1/projects/"+p.ID+"/logs?kind=build", tok, nil)
	want(t, r, 200)
	if lines := strings.Split(strings.TrimSpace(string(r.body)), "\n"); len(lines) != 3 || !strings.Contains(lines[2], "built") {
		t.Fatalf("logs: %s", r.body)
	}
	wantErr(t, call(t, f, "GET", "/v1/projects/"+p.ID+"/logs?kind=audio", tok, nil), 400, "invalid")
}

func TestSnapshotsAndRestore(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p := mkProject(t, f, tok, "snap", "")
	opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots", tok, nil))
	r := call(t, f, "GET", "/v1/projects/"+p.ID+"/snapshots", tok, nil)
	want(t, r, 200)
	var snaps []Snapshot
	r.json(t, &snaps)
	if len(snaps) != 1 || snaps[0].Reason != "manual" {
		t.Fatalf("snapshots: %s", r.body)
	}
	sid := snaps[0].ID
	wantErr(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots/"+sid+"/restore", tok, nil), 400, "invalid")
	wantErr(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots/nope/restore", tok, nil), 404, "not_found")
	opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots/"+sid+"/restore", tok, map[string]any{"as_new_project": "snap-copy"}))
	opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/stop", tok, map[string]any{"snapshot": false}))
	opID(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/snapshots/"+sid+"/restore", tok, nil))
	r = call(t, f, "GET", "/v1/projects", tok, nil)
	var list []Project
	r.json(t, &list)
	names := []string{}
	for _, q := range list {
		names = append(names, q.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "snap,snap-copy" {
		t.Fatalf("projects: %v", names)
	}
}

func TestUsageAndBilling(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	r := call(t, f, "GET", "/v1/usage?from=2026-09-01&to=2026-09-20", tok, nil)
	want(t, r, 200)
	if strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("usage: %s", r.body)
	}
	wantErr(t, call(t, f, "GET", "/v1/usage?from=yesterday", tok, nil), 400, "invalid")
	// Billing off: every billing route is 503 (api.md "Usage and billing").
	for _, rt := range [][2]string{{"GET", "/v1/billing"}, {"POST", "/v1/billing/checkout"}, {"POST", "/v1/billing/waitlist"}, {"POST", "/v1/billing/plan"}, {"POST", "/v1/billing/cancel"}, {"POST", "/v1/billing/resume"}, {"POST", "/v1/billing/portal"}, {"GET", "/v1/billing/invoices"}} {
		r := call(t, f, rt[0], rt[1], tok, nil)
		wantErr(t, r, 503, "billing_disabled")
		if !strings.Contains(string(r.body), "billing is not configured") {
			t.Fatalf("%s: %s", rt[1], r.body)
		}
	}
	// The public count needs no token and no billing.
	r = call(t, f, "GET", "/v1/public/seats", "", nil)
	want(t, r, 200)
	if string(bytes.TrimSpace(r.body)) != `{"total":30,"free":18,"waiting":0}` {
		t.Fatalf("seats: %s", r.body)
	}

	g := New(Options{Billing: true})
	defer g.Close()
	r = call(t, g, "POST", "/v1/billing/portal", tok, nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"url":"https://customer-portal.paddle.com/`) {
		t.Fatalf("portal: %s", r.body)
	}
	r = call(t, g, "POST", "/v1/billing/portal", tok, map[string]string{"for": "payment_method"})
	want(t, r, 200)
	if !strings.Contains(string(r.body), `update-payment-method`) {
		t.Fatalf("portal payment_method: %s", r.body)
	}
	r = call(t, g, "GET", "/v1/billing/invoices", tok, nil)
	want(t, r, 200)
	if !strings.Contains(string(r.body), `"number":"REPOSE-0001"`) || !strings.Contains(string(r.body), `"pdf_url":"https://`) {
		t.Fatalf("invoices: %s", r.body)
	}
	r = call(t, g, "GET", "/v1/me", tok, nil)
	if !strings.Contains(string(r.body), `"status":"active"`) || !strings.Contains(string(r.body), `"plan":"solo"`) || !strings.Contains(string(r.body), `"has_card":true`) {
		t.Fatalf("me with billing: %s", r.body)
	}
	wantErr(t, call(t, g, "POST", "/v1/billing/webhook", tok, `{}`), 400, "invalid")
}

func TestRateLimit(t *testing.T) {
	f := New(Options{RateLimit: true})
	defer f.Close()
	// GETs have a bucket ten times the writes' (I-187).
	for i := 0; i < 600; i++ {
		want(t, call(t, f, "GET", "/v1/me", tok, nil), 200)
	}
	r := call(t, f, "GET", "/v1/me", tok, nil)
	wantErr(t, r, 429, "rate_limited")
	for i := 0; i < 60; i++ {
		want(t, call(t, f, "POST", "/v1/me/notify-test", tok, nil), 200)
	}
	wantErr(t, call(t, f, "POST", "/v1/me/notify-test", tok, nil), 429, "rate_limited")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// Another token has its own budget; internal routes have none.
	want(t, call(t, f, "GET", "/v1/me", "other", nil), 200)
	want(t, call(t, f, "GET", "/v1/internal/ca", "", nil), 200)

	g := New(Options{})
	defer g.Close()
	for i := 0; i < 70; i++ {
		want(t, call(t, g, "GET", "/v1/me", tok, nil), 200)
	}
}

// TestDestroyDelay: with DestroyDelay the project reads "destroying" until
// the op ends, keeps its name meanwhile, and no longer counts toward the
// project limit (I-300); then it is gone.
func TestDestroyDelay(t *testing.T) {
	f := New(Options{DestroyDelay: 300 * time.Millisecond})
	defer f.Close()
	a := mkProject(t, f, tok, "a", "")
	c := mkProject(t, f, tok, "c", "")
	// Fill the account to its cap (I-569).
	for i := 2; i < ProjectCap; i++ {
		mkProject(t, f, tok, "p"+strconv.Itoa(i), "")
	}
	opID(t, call(t, f, "POST", "/v1/projects/"+a.ID+"/snapshots", tok, nil))
	r := call(t, f, "GET", "/v1/projects/"+a.ID+"/snapshots", tok, nil)
	var snaps []Snapshot
	r.json(t, &snaps)
	fork := map[string]any{"snapshot_id": snaps[0].ID, "name": "a-fork"}
	wantErr(t, call(t, f, "POST", "/v1/projects/"+a.ID+"/fork", tok, fork), 400, "invalid")
	opID(t, call(t, f, "DELETE", "/v1/projects/"+c.ID, tok, nil))
	if p := getProject(t, f, tok, c.ID); p.State != "destroying" || p.OpID == "" {
		t.Fatalf("while destroying: %+v", p)
	}
	wantErr(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "c", "class": "large"}), 409, "conflict")
	want(t, call(t, f, "POST", "/v1/projects/"+a.ID+"/fork", tok, fork), http.StatusAccepted)
	deadline := time.Now().Add(5 * time.Second)
	for call(t, f, "GET", "/v1/projects/"+c.ID, tok, nil).status != 404 {
		if time.Now().After(deadline) {
			t.Fatal("still not destroyed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
