package auth_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/auth"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/logto"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

const aud = "https://api.repose.herakraft.co"

func TestVerify(t *testing.T) {
	f := logto.New(aud)
	defer f.Close()
	v := auth.NewVerifier(f.Issuer(), aud, nil)
	ctx := context.Background()
	c, err := v.Verify(ctx, f.Token("sub1"))
	if err != nil || c.Sub != "sub1" {
		t.Fatalf("%+v %v", c, err)
	}
	for name, tok := range map[string]string{
		"wrong audience": f.TokenWith("sub1", "https://other", time.Hour),
		"expired":        f.TokenWith("sub1", aud, -time.Hour),
		"wrong key":      f.TokenWrongKey("sub1"),
		"garbage":        "not.a.jwt",
	} {
		if _, err := v.Verify(ctx, tok); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// One JWKS fetch serves an hour of verifications.
	for i := 0; i < 20; i++ {
		if _, err := v.Verify(ctx, f.Token("sub1")); err != nil {
			t.Fatal(err)
		}
	}
	if f.JWKSHits() > 3 {
		t.Fatalf("jwks fetched %d times", f.JWKSHits())
	}
}

// A token with a kid nobody issued must not make the api fetch the JWKS
// each time: the kid is read before the signature is checked.
func TestUnknownKidDoesNotRefetchEachRequest(t *testing.T) {
	f := logto.New(aud)
	defer f.Close()
	v := auth.NewVerifier(f.Issuer(), aud, nil)
	ctx := context.Background()
	if _, err := v.Verify(ctx, f.Token("s")); err != nil {
		t.Fatal(err)
	}
	base := f.JWKSHits()
	forge := func(kid string) string {
		h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT","kid":"` + kid + `"}`))
		p := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x","exp":9999999999}`))
		return h + "." + p + ".c2ln"
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := v.Verify(ctx, forge(fmt.Sprintf("kid-%d", i))); !errors.Is(err, auth.ErrInvalidToken) {
				t.Errorf("forged kid %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := f.JWKSHits() - base; got > 1 {
		t.Fatalf("100 unknown kids caused %d JWKS fetches, want at most 1", got)
	}
	// Past the interval, one more unknown kid may fetch once (a key
	// rotated in is picked up).
	auth.SetClockForTest(v, time.Now().Add(auth.MinRefreshInterval+time.Second))
	for i := 0; i < 10; i++ {
		_, _ = v.Verify(ctx, forge("later"))
	}
	if got := f.JWKSHits() - base; got > 2 {
		t.Fatalf("after the interval: %d fetches, want at most 2", got)
	}
	// Known kids still verify throughout.
	if _, err := v.Verify(ctx, f.Token("s")); err != nil {
		t.Fatal(err)
	}
}

func TestJWKSOutageServesStaleThenFails(t *testing.T) {
	f := logto.New(aud)
	defer f.Close()
	v := auth.NewVerifier(f.Issuer(), aud, nil)
	ctx := context.Background()
	if _, err := v.Verify(ctx, f.Token("s")); err != nil {
		t.Fatal(err)
	}
	f.JWKSDown = true
	// Cache still fresh: fine. Force a refresh by making the cache old.
	auth.SetClockForTest(v, time.Now().Add(2*time.Hour))
	if _, err := v.Verify(ctx, f.Token("s")); err != nil {
		t.Fatalf("stale cache within 24 h should serve: %v", err)
	}
	auth.SetClockForTest(v, time.Now().Add(30*time.Hour))
	if _, err := v.Verify(ctx, f.Token("s")); !errors.Is(err, auth.ErrIdentityProviderUnavailable) {
		t.Fatalf("expected identity provider unavailable, got %v", err)
	}
	v2 := auth.NewVerifier(f.Issuer(), aud, nil)
	if _, err := v2.Verify(ctx, f.Token("s")); !errors.Is(err, auth.ErrIdentityProviderUnavailable) {
		t.Fatalf("cold cache with jwks down: %v", err)
	}
}

func TestDeriveHandle(t *testing.T) {
	for in, want := range map[string]string{
		"Heracraft": "heracraft", "Foo_Bar.Baz": "foo-bar-baz", "--x--": "x", "a  b": "a-b",
		"ThisIsAVeryLongGitHubLoginNameIndeedYes": "thisisaverylonggithubloginnamein", "": "user", "日本": "user",
	} {
		if got := auth.DeriveHandle(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestFirstSignInCreatesUserAndCollisionsSuffix(t *testing.T) {
	pool := testdb.Open(t)
	f := logto.New(aud)
	defer f.Close()
	f.AddUser("sub-a", logto.User{Email: "a@example.com", GithubLogin: "Octo_Cat"})
	f.AddUser("sub-b", logto.User{Email: "b@example.com", GithubLogin: "octo-cat"})
	f.AddUser("sub-c", logto.User{Email: "c@example.com", GithubLogin: "OCTO.CAT"})
	f.AddUser("sub-legacy", logto.User{Email: "legacy@example.com", GithubLogin: "legacy-cat", LegacyShape: true})
	f.AddUser("sub-mail", logto.User{Email: "First.Last+repose@example.com"})
	f.AddUser("sub-mail-2", logto.User{Email: "first-last@example.org"})
	f.AddUser("sub-kanji", logto.User{Email: "日本@example.com"})
	p := auth.NewProvisioner(pool, auth.NewLogtoManagement(f.Issuer(), "m2m", "secret", nil))
	ctx := context.Background()
	a, err := p.EnsureUser(ctx, "sub-a")
	if err != nil {
		t.Fatal(err)
	}
	// A new account has no plan and no credit (I-289); the row's limits are
	// Solo's project count and no xl, for the exempt path.
	if a.Handle != "octo-cat" || *a.Email != "a@example.com" || a.TrialCreditCents != 0 || a.ProjectLimit != 10 || a.XLLimit != 0 || a.BillingStatus != "none" || a.HasCard {
		t.Fatalf("%+v", a)
	}
	again, err := p.EnsureUser(ctx, "sub-a")
	if err != nil || again.ID != a.ID {
		t.Fatalf("second sign-in: %+v %v", again, err)
	}
	b, err := p.EnsureUser(ctx, "sub-b")
	if err != nil || b.Handle != "octo-cat-2" {
		t.Fatalf("collision: %+v %v", b, err)
	}
	// An older connector's flat details.login still works. An email sign-in
	// with no GitHub identity takes the address's name before any +tag
	// (I-299), collides like a login, and an address with nothing usable
	// before the @ still gets the user-<sub> fallback (I-100).
	if d, err := p.EnsureUser(ctx, "sub-legacy"); err != nil || d.Handle != "legacy-cat" {
		t.Fatalf("legacy shape: %+v %v", d, err)
	}
	if e, err := p.EnsureUser(ctx, "sub-mail"); err != nil || e.Handle != "first-last" || e.GithubLogin != nil {
		t.Fatalf("no github identity: %+v %v", e, err)
	}
	if e, err := p.EnsureUser(ctx, "sub-mail-2"); err != nil || e.Handle != "first-last-2" {
		t.Fatalf("email collision: %+v %v", e, err)
	}
	if e, err := p.EnsureUser(ctx, "sub-kanji"); err != nil || e.Handle != "user-sub-kanji" {
		t.Fatalf("nothing usable before the @: %+v %v", e, err)
	}
	c, err := p.EnsureUser(ctx, "sub-c")
	if err != nil || c.Handle != "octo-cat-3" {
		t.Fatalf("second collision: %+v %v", c, err)
	}
	f.MgmtDown = true
	if _, err := p.EnsureUser(ctx, "sub-d"); !errors.Is(err, auth.ErrProvisionFailed) {
		t.Fatalf("management api down: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, "select count(*) from users where logto_sub = 'sub-d'").Scan(&n)
	if n != 0 {
		t.Fatal("a user row was created despite the provider failure")
	}
}
