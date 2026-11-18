package httpapi_test

import (
	"encoding/base64"
	"testing"
	"time"
)

// GET /me carries the account's GitHub answer and PATCH /me sets it
// (DECISIONS I-1340, I-1342): the login-copy switch, which only ever
// subtracts from what a laptop does, and the expiry GitHub gave for the
// account's GH_TOKEN, which is informational and refuses nothing.
// Removing GH_TOKEN clears the expiry, since a warning about a token
// that is gone is worse than none.
func TestPatchMeGitHub(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-gh", "ghuser")
	github := func() map[string]any {
		t.Helper()
		r := e.do(t, tok, "GET", "/me", nil)
		if r.status != 200 {
			t.Fatalf("GET /me: %d %s", r.status, r.raw)
		}
		g, ok := r.body["github"].(map[string]any)
		if !ok {
			t.Fatalf("GET /me has no github: %s", r.raw)
		}
		return g
	}
	// A new account copies the login, as every account has until now,
	// and knows no expiry.
	if g := github(); g["login_copy"] != true || g["token_expires_at"] != nil {
		t.Fatalf("a new account: %v", g)
	}
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"github": map[string]any{"login_copy": false}}); r.status != 200 {
		t.Fatalf("turn the copy off: %d %s", r.status, r.raw)
	}
	if g := github(); g["login_copy"] != false {
		t.Fatalf("after turning the copy off: %v", g)
	}

	// The expiry `repose github setup` records.
	at := time.Now().Add(300 * 24 * time.Hour).UTC().Truncate(time.Second)
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"github": map[string]any{"token_expires_at": at.Format(time.RFC3339)}}); r.status != 200 {
		t.Fatalf("set the expiry: %d %s", r.status, r.raw)
	}
	got, _ := github()["token_expires_at"].(string)
	if parsed, err := time.Parse(time.RFC3339, got); err != nil || !parsed.Equal(at) {
		t.Fatalf("expiry = %q (%v), want %s", got, err, at)
	}
	// Nothing implausible: a time long past, or a decade out, is the
	// client misreading a header and not an expiry.
	for _, bad := range []any{
		time.Now().Add(-48 * time.Hour).Format(time.RFC3339),
		time.Now().AddDate(11, 0, 0).Format(time.RFC3339),
		"not a time",
	} {
		if r := e.do(t, tok, "PATCH", "/me", map[string]any{"github": map[string]any{"token_expires_at": bad}}); r.status != 400 {
			t.Fatalf("token_expires_at %v: %d %s", bad, r.status, r.raw)
		}
	}
	// Still the good one.
	if g := github(); g["token_expires_at"] == nil {
		t.Fatalf("a refused expiry replaced the stored one: %v", g)
	}
	// null clears it.
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"github": map[string]any{"token_expires_at": nil}}); r.status != 200 {
		t.Fatalf("clear the expiry: %d %s", r.status, r.raw)
	}
	if g := github(); g["token_expires_at"] != nil {
		t.Fatalf("after clearing: %v", g)
	}

	// The token's own removal clears it too.
	ghValue := base64.StdEncoding.EncodeToString([]byte("github_pat_11ABCDEFG0example"))
	if r := e.do(t, tok, "PUT", "/me/secrets/GH_TOKEN", map[string]any{"value": ghValue}); r.status != 200 {
		t.Fatalf("store GH_TOKEN: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"github": map[string]any{"token_expires_at": at.Format(time.RFC3339)}}); r.status != 200 {
		t.Fatalf("set the expiry again: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "DELETE", "/me/secrets/GH_TOKEN", nil); r.status != 204 {
		t.Fatalf("remove GH_TOKEN: %d %s", r.status, r.raw)
	}
	if g := github(); g["token_expires_at"] != nil {
		t.Fatalf("the expiry outlived its token: %v", g)
	}
	// Another secret's removal leaves it alone.
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"github": map[string]any{"token_expires_at": at.Format(time.RFC3339)}}); r.status != 200 {
		t.Fatalf("set the expiry a third time: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "PUT", "/me/secrets/OTHER", map[string]any{"value": base64.StdEncoding.EncodeToString([]byte("x"))}); r.status != 200 {
		t.Fatalf("store OTHER: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "DELETE", "/me/secrets/OTHER", nil); r.status != 204 {
		t.Fatalf("remove OTHER: %d %s", r.status, r.raw)
	}
	if g := github(); g["token_expires_at"] == nil {
		t.Fatalf("another secret's removal cleared the expiry: %v", g)
	}
}
