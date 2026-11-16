package fakelogto

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func newVerifier() (verifier, challenge string) {
	verifier = "test-code-verifier-0123456789abcdefghijklmno"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestDiscovery(t *testing.T) {
	f, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	res, err := http.Get(f.Issuer() + "/oidc/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

// runFlow drives the whole authorization-code-with-PKCE exchange as a
// browser would: GET /oidc/auth, submit the approve form (follows the
// redirect to the client's own redirect_uri, which this test does not
// actually serve, so it stops the jar right after the code lands in the
// query string), then POST /oidc/token.
func runFlow(t *testing.T, f *Fake, verifier, challenge, redirectURI string) map[string]any {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	authURL := f.Issuer() + "/oidc/auth?" + url.Values{
		"client_id":             {"dashboard-test"},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid profile email offline_access"},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"nonce":                 {"n-0S6_WzA2Mj"},
		"resource":              {"https://api.repose.run"},
	}.Encode()

	res, err := client.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /oidc/auth status = %d", res.StatusCode)
	}

	// Approve directly with the same hidden-field values the page would
	// have submitted, since extracting them from HTML is not this test's
	// job.
	form := url.Values{
		"client_id":      {"dashboard-test"},
		"redirect_uri":   {redirectURI},
		"state":          {"xyz"},
		"code_challenge": {challenge},
		"nonce":          {"n-0S6_WzA2Mj"},
		"resource":       {"https://api.repose.run"},
	}
	res2, err := client.PostForm(f.Issuer()+"/oidc/auth/approve", form)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusFound {
		t.Fatalf("POST /oidc/auth/approve status = %d", res2.StatusCode)
	}
	loc, err := res2.Location()
	if err != nil {
		t.Fatalf("no redirect location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %s", loc)
	}
	if loc.Query().Get("state") != "xyz" {
		t.Fatalf("state not echoed: %s", loc)
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"client_id":     {"dashboard-test"},
	}
	res3, err := http.PostForm(f.Issuer()+"/oidc/token", tokenForm)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res3.Body.Close() }()
	if res3.StatusCode != http.StatusOK {
		t.Fatalf("POST /oidc/token status = %d", res3.StatusCode)
	}
	var body map[string]any
	if err := decodeJSON(res3, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestAuthorizationCodeFlow(t *testing.T) {
	f, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	verifier, challenge := newVerifier()
	body := runFlow(t, f, verifier, challenge, "https://dashboard.test/callback")

	access, _ := body["access_token"].(string)
	idTok, _ := body["id_token"].(string)
	if access == "" || idTok == "" {
		t.Fatalf("missing tokens: %v", body)
	}

	claims := verifyAndParse(t, f, idTok)
	if claims["nonce"] != "n-0S6_WzA2Mj" {
		t.Errorf("nonce = %v", claims["nonce"])
	}
	if claims["sub"] != Identity.Subject {
		t.Errorf("sub = %v", claims["sub"])
	}

	accessClaims := verifyAndParse(t, f, access)
	if accessClaims["aud"] != "https://api.repose.run" {
		t.Errorf("access token aud = %v", accessClaims["aud"])
	}
}

func TestWrongVerifierRejected(t *testing.T) {
	f, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	_, challenge := newVerifier()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := url.Values{
		"client_id": {"dashboard-test"}, "redirect_uri": {"https://dashboard.test/callback"},
		"state": {"s"}, "code_challenge": {challenge},
	}
	res, err := client.PostForm(f.Issuer()+"/oidc/auth/approve", form)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	loc, _ := res.Location()
	code := loc.Query().Get("code")

	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {"not-the-right-verifier"},
		"redirect_uri":  {"https://dashboard.test/callback"}, "client_id": {"dashboard-test"},
	}
	res2, err := http.PostForm(f.Issuer()+"/oidc/token", tokenForm)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res2.StatusCode)
	}
	var body map[string]any
	_ = decodeJSON(res2, &body)
	if body["error"] != "invalid_grant" {
		t.Fatalf("error = %v", body["error"])
	}
}

func TestRefreshToken(t *testing.T) {
	f, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	verifier, challenge := newVerifier()
	body := runFlow(t, f, verifier, challenge, "https://dashboard.test/callback")
	refresh, _ := body["refresh_token"].(string)
	if refresh == "" {
		t.Fatal("no refresh_token")
	}

	res, err := http.PostForm(f.Issuer()+"/oidc/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var body2 map[string]any
	if err := decodeJSON(res, &body2); err != nil {
		t.Fatal(err)
	}
	if body2["access_token"] == "" {
		t.Fatal("no access_token from refresh")
	}
}

// verifyAndParse checks the token's signature against the fake's own JWKS
// (fetched fresh, as a real client would) and returns its claims.
func verifyAndParse(t *testing.T, f *Fake, token string) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(tok *jwt.Token) (any, error) {
		return &f.key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("token did not verify against the fake's own key: %v", err)
	}
	return claims
}

func decodeJSON(res *http.Response, v any) error {
	return json.NewDecoder(res.Body).Decode(v)
}
