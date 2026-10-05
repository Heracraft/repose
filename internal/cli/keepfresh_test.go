package cli

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// A second repose process whose access token has expired takes the one
// another process (an attach keeping it fresh) already wrote, with no
// call to Logto (I-491).
func TestTokenSourceTakesAnotherProcessesRefresh(t *testing.T) {
	oidc := newFakeOIDC()
	defer oidc.Close()
	dir := t.TempDir()
	oidc.refresh["seed"] = "user-1"
	expired := Credentials{RefreshToken: "seed", AccessToken: "stale", ExpiresAt: time.Now().Add(-time.Minute), LogtoIssuer: oidc.Issuer()}
	if err := saveCredentials(dir, expired); err != nil {
		t.Fatal(err)
	}

	a := newOIDCTokenSource(dir, http.DefaultClient, expired)
	if _, err := a.AccessToken(context.Background(), false); err != nil {
		t.Fatalf("first process: %v", err)
	}
	// Logto rotated the refresh token: "seed" is spent, and no token
	// request may be needed for the second process.
	oidc.mu.Lock()
	oidc.refresh = map[string]string{}
	oidc.mu.Unlock()

	b := newOIDCTokenSource(dir, http.DefaultClient, expired)
	tok, err := b.AccessToken(context.Background(), false)
	if err != nil || tok != "access-user-1" {
		t.Fatalf("second process: tok=%q err=%v", tok, err)
	}
}

// When the token on disk is about to expire too, the refresh uses the
// rotated refresh token on disk, not the spent one in memory.
func TestTokenSourceRefreshesWithRotatedToken(t *testing.T) {
	oidc := newFakeOIDC()
	defer oidc.Close()
	dir := t.TempDir()
	oidc.refresh["rotated"] = "user-1"
	disk := Credentials{RefreshToken: "rotated", AccessToken: "short", ExpiresAt: time.Now().Add(30 * time.Second), LogtoIssuer: oidc.Issuer()}
	if err := saveCredentials(dir, disk); err != nil {
		t.Fatal(err)
	}
	mem := Credentials{RefreshToken: "spent", AccessToken: "stale", ExpiresAt: time.Now().Add(-time.Minute), LogtoIssuer: oidc.Issuer()}
	tok, err := newOIDCTokenSource(dir, http.DefaultClient, mem).AccessToken(context.Background(), false)
	if err != nil || tok != "access-user-1" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
}

func TestKeepFreshRefreshesBeforeExpiry(t *testing.T) {
	old := keepFreshTick
	keepFreshTick = 10 * time.Millisecond
	t.Cleanup(func() { keepFreshTick = old })
	oidc := newFakeOIDC()
	defer oidc.Close()
	dir := t.TempDir()
	oidc.refresh["seed"] = "user-1"
	creds := Credentials{RefreshToken: "seed", AccessToken: "soon", ExpiresAt: time.Now().Add(5 * time.Minute), LogtoIssuer: oidc.Issuer()}
	if err := saveCredentials(dir, creds); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		newOIDCTokenSource(dir, http.DefaultClient, creds).KeepFresh(ctx)
		close(stopped)
	}()
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok, _ := loadCredentials(dir); ok && c.AccessToken == "access-user-1" && time.Until(c.ExpiresAt) > 50*time.Minute {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the token on disk was not refreshed")
}

// A logout while attached ends the loop instead of writing the session
// back.
func TestKeepFreshStopsAfterLogout(t *testing.T) {
	old := keepFreshTick
	keepFreshTick = 10 * time.Millisecond
	t.Cleanup(func() { keepFreshTick = old })
	oidc := newFakeOIDC()
	defer oidc.Close()
	dir := t.TempDir()
	oidc.refresh["seed"] = "user-1"
	creds := Credentials{RefreshToken: "seed", AccessToken: "soon", ExpiresAt: time.Now().Add(5 * time.Minute), LogtoIssuer: oidc.Issuer()}

	done := make(chan struct{})
	go func() {
		newOIDCTokenSource(dir, http.DefaultClient, creds).KeepFresh(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("KeepFresh kept running with no credentials file")
	}
	if _, ok, _ := loadCredentials(dir); ok {
		t.Fatal("KeepFresh wrote credentials after a logout")
	}
}
