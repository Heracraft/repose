package httpapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/tokens"
)

// TestAPITokens covers DECISIONS I-1314..I-1320 at the api: a token is
// minted once and never shown again; it opens the routes its scopes
// name and no other; a project-bound one sees its one project; it ends
// at expiry and at revoke; and the account's own routes stay closed to
// it.
func TestAPITokens(t *testing.T) {
	e := newEnv(t)
	login := e.signIn(t, "sub-tok", "tok")
	r := e.do(t, login, "POST", "/projects", map[string]any{"name": "pa", "class": "small"})
	pa := r.body["id"].(string)
	e.waitOp(t, r)
	r = e.do(t, login, "POST", "/projects", map[string]any{"name": "pb", "class": "small"})
	pb := r.body["id"].(string)
	e.waitOp(t, r)

	// Mint: the one answer with the token.
	r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "ci", "scopes": []string{"triggers", "read"}})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	secret, _ := r.body["token"].(string)
	if !strings.HasPrefix(secret, "rpt_") || len(secret) != 47 {
		t.Fatalf("token shape: %q", secret)
	}
	if got := r.body["scopes"].([]any); len(got) != 2 || got[0] != "read" || got[1] != "triggers" {
		t.Fatalf("scopes are normalised to AllScopes' order: %v", got)
	}
	if r.body["prefix"] != secret[:12] || r.body["project"] != nil {
		t.Fatalf("row: %s", r.raw)
	}
	r = e.do(t, login, "GET", "/me/tokens", nil)
	if r.status != 200 || len(r.list) != 1 {
		t.Fatalf("list: %d %s", r.status, r.raw)
	}
	if strings.Contains(string(r.raw), secret) || strings.Contains(string(r.raw), `"token"`) {
		t.Fatalf("the list carries the token: %s", r.raw)
	}

	// Reads and the triggers area open; the rest is refused by scope.
	if r = e.do(t, secret, "GET", "/projects", nil); r.status != 200 || len(r.list) != 2 {
		t.Fatalf("read with token: %d %s", r.status, r.raw)
	}
	r = e.do(t, secret, "GET", "/me", nil)
	if r.status != 200 {
		t.Fatalf("me with token: %d %s", r.status, r.raw)
	}
	if tok, _ := r.body["token"].(map[string]any); tok == nil || tok["name"] != "ci" {
		t.Fatalf("GET /me names the token: %s", r.raw)
	}
	r = e.do(t, secret, "POST", "/projects/"+pa+"/triggers", map[string]any{"name": "nightly", "kind": "schedule", "spec": "daily 02:00", "zone": "UTC", "prompt_path": "PROMPT.md"})
	if r.status != 201 {
		t.Fatalf("trigger with token: %d %s", r.status, r.raw)
	}
	r = e.do(t, secret, "POST", "/projects/"+pa+"/stop", nil)
	if r.status != 403 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "token_scope" {
		t.Fatalf("stop needs projects: %d %s", r.status, r.raw)
	}
	if msg := r.body["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "read, triggers") || !strings.Contains(msg, "needs projects") {
		t.Fatalf("the refusal names both: %s", msg)
	}
	// Never with a token: the account, money, SSH, tokens.
	for _, c := range []struct{ method, path string }{
		{"PATCH", "/me"}, {"DELETE", "/me"}, {"POST", "/certs"}, {"GET", "/billing"}, {"GET", "/usage"},
		{"GET", "/me/tokens"}, {"POST", "/me/tokens"}, {"DELETE", "/me/tokens/ci"}, {"POST", "/projects/" + pa + "/browser/view"},
	} {
		r = e.do(t, secret, c.method, c.path, map[string]any{})
		if r.status != 403 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "token_never" {
			t.Fatalf("%s %s with a token: %d %s", c.method, c.path, r.status, r.raw)
		}
	}
	// The last-used mark lands.
	r = e.do(t, login, "GET", "/me/tokens", nil)
	if r.list[0].(map[string]any)["last_used_at"] == nil {
		t.Fatalf("last_used_at: %s", r.raw)
	}

	// A project-bound token: its project, and no other.
	r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "pa-only", "scopes": []string{"read", "agents"}, "project_id": pa})
	if r.status != 201 || r.body["project"].(map[string]any)["slug"] != "pa" {
		t.Fatalf("bound create: %d %s", r.status, r.raw)
	}
	bound := r.body["token"].(string)
	if r = e.do(t, bound, "GET", "/projects", nil); r.status != 200 || len(r.list) != 1 || r.list[0].(map[string]any)["id"] != pa {
		t.Fatalf("bound list: %d %s", r.status, r.raw)
	}
	if r = e.do(t, bound, "GET", "/projects/"+pa, nil); r.status != 200 {
		t.Fatalf("bound own: %d %s", r.status, r.raw)
	}
	r = e.do(t, bound, "GET", "/projects/"+pb, nil)
	if r.status != 403 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "token_project" {
		t.Fatalf("bound other: %d %s", r.status, r.raw)
	}
	r = e.do(t, bound, "GET", "/questions", nil)
	if r.status != 403 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "token_project" {
		t.Fatalf("bound account-wide: %d %s", r.status, r.raw)
	}
	r = e.do(t, bound, "POST", "/projects/"+pa+"/say", map[string]any{"message": "hi"})
	if r.status == 403 {
		t.Fatalf("bound say on its project: %d %s", r.status, r.raw)
	}

	// Names are unique among live tokens, the limit is twenty, and a bad
	// scope or lifetime is refused.
	r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "ci", "scopes": []string{"read"}})
	if r.status != 409 {
		t.Fatalf("duplicate name: %d %s", r.status, r.raw)
	}
	r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "x", "scopes": []string{"root"}})
	if r.status != 400 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "scopes" {
		t.Fatalf("bad scope: %d %s", r.status, r.raw)
	}
	r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "x", "scopes": []string{"read"}, "expires_in_days": 400})
	if r.status != 400 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "expires" {
		t.Fatalf("bad expiry: %d %s", r.status, r.raw)
	}
	r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "Bad Name", "scopes": []string{"read"}})
	if r.status != 400 {
		t.Fatalf("bad name: %d %s", r.status, r.raw)
	}
	for i := 3; i <= tokens.MaxLive; i++ {
		r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "t" + strings.Repeat("x", i), "scopes": []string{"read"}})
		if r.status != 201 {
			t.Fatalf("token %d: %d %s", i, r.status, r.raw)
		}
	}
	r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "one-too-many", "scopes": []string{"read"}})
	if r.status != 400 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "token_limit" {
		t.Fatalf("21st: %d %s", r.status, r.raw)
	}

	// Revoke: at once, the row stays and says so, and the name is free.
	if r = e.do(t, login, "DELETE", "/me/tokens/ci", nil); r.status != 204 {
		t.Fatalf("revoke: %d %s", r.status, r.raw)
	}
	r = e.do(t, secret, "GET", "/projects", nil)
	if r.status != 401 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "token_revoked" {
		t.Fatalf("after revoke: %d %s", r.status, r.raw)
	}
	r = e.do(t, login, "GET", "/me/tokens", nil)
	var sawRevoked bool
	for _, raw := range r.list {
		row := raw.(map[string]any)
		if row["name"] == "ci" && row["revoked_at"] != nil {
			sawRevoked = true
		}
	}
	if !sawRevoked {
		t.Fatalf("revoked row listed: %s", r.raw)
	}
	if r = e.do(t, login, "DELETE", "/me/tokens/ci", nil); r.status != 404 {
		t.Fatalf("revoke twice: %d %s", r.status, r.raw)
	}
	if r = e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "ci", "scopes": []string{"read"}}); r.status != 201 {
		t.Fatalf("name freed: %d %s", r.status, r.raw)
	}

	// A token nobody minted, and a user's token on another account's
	// project.
	if r = e.do(t, "rpt_"+strings.Repeat("A", 43), "GET", "/projects", nil); r.status != 401 {
		t.Fatalf("unknown token: %d %s", r.status, r.raw)
	}
	bob := e.signIn(t, "sub-bob-tok", "bobtok")
	r = e.do(t, bob, "POST", "/me/tokens", map[string]any{"name": "mine", "scopes": []string{"read"}, "project_id": pa})
	if r.status != 404 {
		t.Fatalf("bind to another's project: %d %s", r.status, r.raw)
	}
}

// TestAPITokenExpires: a token past its end is refused with the reason.
func TestAPITokenExpires(t *testing.T) {
	e := newEnv(t)
	login := e.signIn(t, "sub-exp", "exp")
	r := e.do(t, login, "POST", "/me/tokens", map[string]any{"name": "short", "scopes": []string{"read"}, "expires_in_days": 1})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	secret := r.body["token"].(string)
	if r = e.do(t, secret, "GET", "/projects", nil); r.status != 200 {
		t.Fatalf("before expiry: %d %s", r.status, r.raw)
	}
	e.tokensNow(func() time.Time { return time.Now().Add(25 * time.Hour) })
	r = e.do(t, secret, "GET", "/projects", nil)
	if r.status != 401 || r.body["error"].(map[string]any)["detail"].(map[string]any)["reason"] != "token_expired" {
		t.Fatalf("after expiry: %d %s", r.status, r.raw)
	}
}

// TestEveryUserRouteHasATokenPolicy: a route the policy does not name
// is closed to tokens, which is safe, but it is also a forgotten line;
// this keeps the map complete.
func TestEveryUserRouteHasATokenPolicy(t *testing.T) {
	e := newEnv(t)
	missing := e.srv.TokenPolicyGaps()
	if len(missing) > 0 {
		t.Fatalf("user routes without a token policy (tokens.go): %s", strings.Join(missing, ", "))
	}
}
