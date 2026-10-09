package cli

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// openViaGet simulates a browser: it just follows the redirect chain
// starting at the authorization URL, the way a browser would after a user
// who is already signed in clicks through instantly.
func openViaGet(url string) error {
	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func TestLoginPKCE(t *testing.T) {
	oidc := newFakeOIDC()
	defer oidc.Close()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()

	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.APIURL = fake.URL() + "/v1"
	cfg.LogtoIssuer = oidc.Issuer()

	err := runLogin(context.Background(), dir, cfg, http.DefaultClient, loginOptions{Browser: true, GOOS: "linux", Display: ":0", Open: openViaGet})
	if err != nil {
		t.Fatalf("runLogin: %v", err)
	}

	creds, ok, err := loadCredentials(dir)
	if err != nil || !ok {
		t.Fatalf("loadCredentials: ok=%v err=%v", ok, err)
	}
	if creds.AccessToken == "" || creds.RefreshToken == "" {
		t.Fatalf("credentials incomplete: %+v", creds)
	}
	if creds.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expires_at in the past: %v", creds.ExpiresAt)
	}
}

func TestLoginDeviceCode(t *testing.T) {
	oidc := newFakeOIDC()
	defer oidc.Close()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()

	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.APIURL = fake.URL() + "/v1"
	cfg.LogtoIssuer = oidc.Issuer()

	err := runLogin(context.Background(), dir, cfg, http.DefaultClient, loginOptions{NoBrowser: true, GOOS: "linux", Stdout: devNull(t)})
	if err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	creds, ok, err := loadCredentials(dir)
	if err != nil || !ok || creds.RefreshToken == "" {
		t.Fatalf("loadCredentials: ok=%v err=%v creds=%+v", ok, err, creds)
	}
}

func TestOIDCTokenSourceRefreshes(t *testing.T) {
	oidc := newFakeOIDC()
	defer oidc.Close()
	dir := t.TempDir()

	// Seed credentials with an already-expired access token so
	// AccessToken must refresh.
	creds := Credentials{RefreshToken: "seed", AccessToken: "stale", ExpiresAt: time.Now().Add(-time.Minute), LogtoIssuer: oidc.Issuer()}
	oidc.refresh["seed"] = "user-1"

	src := newOIDCTokenSource(dir, http.DefaultClient, creds)
	tok, err := src.AccessToken(context.Background(), false)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "access-user-1" {
		t.Fatalf("got token %q", tok)
	}

	// A second call with a still-valid token must not hit the network
	// (deleting the refresh token would break a real refresh).
	delete(oidc.refresh, "seed")
	tok2, err := src.AccessToken(context.Background(), false)
	if err != nil || tok2 != tok {
		t.Fatalf("cached AccessToken: tok=%q err=%v", tok2, err)
	}
}

func TestRunLogoutRemovesCredentialsAndCert(t *testing.T) {
	// runLogout removes ~/.ssh/repose/id_ed25519-cert.pub under the real
	// HOME unless it is redirected; without this line the suite deleted an
	// operator's live certificate (m3 integration, 2026-09-20).
	withHome(t)
	oidc := newFakeOIDC()
	defer oidc.Close()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()

	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.APIURL = fake.URL() + "/v1"
	cfg.LogtoIssuer = oidc.Issuer()

	if err := runLogin(context.Background(), dir, cfg, http.DefaultClient, loginOptions{NoBrowser: true, GOOS: "linux", Stdout: devNull(t)}); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	if err := runLogout(context.Background(), dir, cfg, http.DefaultClient, false, io.Discard); err != nil {
		t.Fatalf("runLogout: %v", err)
	}
	if _, ok, _ := loadCredentials(dir); ok {
		t.Fatal("credentials still present after logout")
	}
}

func devNull(t *testing.T) *os.File {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// With a browser to open, the device login opens its link and still
// prints it; with --no-browser it opens nothing (DECISIONS I-627).
func TestLoginDeviceCodeOpensTheLink(t *testing.T) {
	withHome(t)
	oidc := newFakeOIDC()
	defer oidc.Close()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	cfg := defaultConfig()
	cfg.APIURL = fake.URL() + "/v1"
	cfg.LogtoIssuer = oidc.Issuer()
	for _, noBrowser := range []bool{false, true} {
		var opened []string
		open := func(u string) error { opened = append(opened, u); return nil }
		err := runLogin(context.Background(), t.TempDir(), cfg, http.DefaultClient, loginOptions{NoBrowser: noBrowser, GOOS: "linux", Display: ":0", Open: open, Stdout: devNull(t)})
		if err != nil {
			t.Fatalf("runLogin: %v", err)
		}
		if noBrowser && len(opened) != 0 {
			t.Errorf("--no-browser opened %q", opened)
		}
		if !noBrowser && (len(opened) != 1 || !strings.HasSuffix(opened[0], "/device")) {
			t.Errorf("opened %q, want the device link", opened)
		}
	}
}
