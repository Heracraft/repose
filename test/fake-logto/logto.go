// Package fakelogto is a minimal browser-facing OIDC provider for the
// dashboard's Playwright suite (docs/workstreams/08-dashboard.md §7): just
// enough of authorization-code-with-PKCE for @logto/browser to complete a
// real sign-in redirect round trip against, so the fake API's bearer check
// (any non-empty token) has something realistic to receive.
//
// internal/fakes/logto is a different fake for a different consumer: it
// speaks client-credentials and the Management API for workstream 05's
// server-to-server tests and has no browser-facing authorization endpoint.
package fakelogto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Identity is the one canned user every approved sign-in becomes. It
// intentionally matches internal/fakes/api.CannedUser's literal values
// without importing that package, so the two fakes stay independent.
var Identity = struct {
	Subject     string
	Email       string
	Username    string
	GitHubLogin string
}{
	Subject:     "00000000-0000-7000-8000-000000000001",
	Email:       "dev@example.com",
	Username:    "heracraft",
	GitHubLogin: "heracraft",
}

type authRequest struct {
	clientID      string
	redirectURI   string
	codeChallenge string
	nonce         string
	resources     []string
	createdAt     time.Time
}

type issuedToken struct {
	subject   string
	resources []string
	expires   time.Time
}

// Fake is the running server.
type Fake struct {
	listener net.Listener
	// issuer is the URL the browser reaches this fake at, when a proxy in
	// front of it (a preview's HTTPS) makes that differ from the listener.
	issuer string
	server *http.Server
	key    *rsa.PrivateKey
	kid    string

	mu            sync.Mutex
	codes         map[string]authRequest
	refreshTokens map[string]issuedToken
}

// New starts the fake on 127.0.0.1 with a random port and returns once it
// is accepting connections.
func New() (*Fake, error) { return NewOn("127.0.0.1:0") }

// NewOn is New on addr, which is also the issuer the browser is sent to:
// a preview for someone on another machine listens on an address that
// machine reaches.
func NewOn(addr string) (*Fake, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("fake-logto: generating key: %w", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("fake-logto: listen: %w", err)
	}

	f := &Fake{
		listener:      ln,
		key:           key,
		kid:           "fake-logto-1",
		codes:         map[string]authRequest{},
		refreshTokens: map[string]issuedToken{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("GET /oidc/.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("GET /oidc/jwks", f.jwks)
	mux.HandleFunc("GET /oidc/auth", f.authorize)
	mux.HandleFunc("POST /oidc/auth/approve", f.approve)
	mux.HandleFunc("POST /oidc/token", f.token)
	mux.HandleFunc("GET /oidc/me", f.userinfo)
	mux.HandleFunc("GET /oidc/session/end", f.sessionEnd)

	f.server = &http.Server{Handler: cors(mux)}
	go func() { _ = f.server.Serve(ln) }()
	return f, nil
}

// Issuer is this fake's base URL (what the dashboard's PUBLIC_LOGTO_ENDPOINT
// should be set to).
func (f *Fake) Issuer() string {
	if f.issuer != "" {
		return f.issuer
	}
	return "http://" + f.listener.Addr().String()
}

// SetIssuer makes the fake answer as `url`, the address a proxy in front
// of it serves it at, in its discovery document and its tokens.
func (f *Fake) SetIssuer(url string) { f.issuer = strings.TrimRight(url, "/") }

// Close stops the server.
func (f *Fake) Close() error { return f.server.Close() }

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // fake server; nothing to do if the client went away
}

func (f *Fake) discovery(w http.ResponseWriter, r *http.Request) {
	base := f.Issuer()
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base + "/oidc",
		"authorization_endpoint":                base + "/oidc/auth",
		"token_endpoint":                        base + "/oidc/token",
		"jwks_uri":                              base + "/oidc/jwks",
		"userinfo_endpoint":                     base + "/oidc/me",
		"end_session_endpoint":                  base + "/oidc/session/end",
		"revocation_endpoint":                   base + "/oidc/token/revocation",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "offline_access"},
	})
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *Fake) jwks(w http.ResponseWriter, r *http.Request) {
	pub := f.key.PublicKey
	writeJSON(w, http.StatusOK, map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA", "kid": f.kid, "use": "sig", "alg": "RS256",
			"n": b64url(pub.N.Bytes()), "e": b64url(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

// authorize renders a page with a single visible "Continue as heracraft"
// button so Playwright can drive a real cross-page redirect round trip,
// rather than being redirected back invisibly.
func (f *Fake) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	if q.Get("response_type") != "code" || redirectURI == "" {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if v := q.Get("code_challenge_method"); v != "" && v != "S256" {
		redirectWithError(w, r, redirectURI, state, "invalid_request")
		return
	}

	var hidden []string
	add := func(name, value string) {
		hidden = append(hidden, fmt.Sprintf(`<input type="hidden" name=%q value=%q>`, name, value))
	}
	add("client_id", q.Get("client_id"))
	add("redirect_uri", redirectURI)
	add("state", state)
	add("code_challenge", q.Get("code_challenge"))
	add("nonce", q.Get("nonce"))
	for _, res := range q["resource"] {
		add("resource", res)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
<h1>Sign in to repose (fake Logto)</h1>
<form method="post" action="/oidc/auth/approve">%s
<button type="submit">Continue as heracraft</button>
</form>
</body></html>`, joinStrings(hidden))
}

func joinStrings(parts []string) string {
	out := ""
	for _, p := range parts {
		out += "\n" + p
	}
	return out
}

func redirectWithError(w http.ResponseWriter, r *http.Request, redirectURI, state, code string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, code, http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return b64url(b)
}

func (f *Fake) approve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	redirectURI := r.Form.Get("redirect_uri")
	code := randomToken()

	f.mu.Lock()
	f.codes[code] = authRequest{
		clientID:      r.Form.Get("client_id"),
		redirectURI:   redirectURI,
		codeChallenge: r.Form.Get("code_challenge"),
		nonce:         r.Form.Get("nonce"),
		resources:     r.Form["resource"],
		createdAt:     time.Now(),
	}
	f.mu.Unlock()

	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if state := r.Form.Get("state"); state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func codeVerifierMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return b64url(sum[:]) == challenge
}

func (f *Fake) mintTokens(subject string, resources []string) (accessToken, idToken, refreshToken string, err error) {
	audience := ""
	if len(resources) > 0 {
		audience = resources[0]
	}
	now := time.Now()

	access := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": f.Issuer() + "/oidc", "aud": audience, "sub": subject,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"scope": "openid profile email offline_access",
	})
	access.Header["kid"] = f.kid
	accessToken, err = access.SignedString(f.key)
	if err != nil {
		return "", "", "", err
	}

	refreshToken = randomToken()
	f.mu.Lock()
	f.refreshTokens[refreshToken] = issuedToken{subject: subject, resources: resources, expires: now.Add(30 * 24 * time.Hour)}
	f.mu.Unlock()

	return accessToken, "", refreshToken, nil
}

func (f *Fake) mintIDToken(clientID, nonce string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": f.Issuer() + "/oidc", "aud": clientID, "sub": Identity.Subject,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"name": Identity.Username, "email": Identity.Email, "username": Identity.Username,
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.kid
	return tok.SignedString(f.key)
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		code := r.Form.Get("code")
		f.mu.Lock()
		req, ok := f.codes[code]
		if ok {
			delete(f.codes, code)
		}
		f.mu.Unlock()
		if !ok || req.redirectURI != r.Form.Get("redirect_uri") || req.clientID != r.Form.Get("client_id") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		if !codeVerifierMatches(r.Form.Get("code_verifier"), req.codeChallenge) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		accessToken, _, refreshToken, err := f.mintTokens(Identity.Subject, req.resources)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
			return
		}
		idToken, err := f.mintIDToken(req.clientID, req.nonce)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": accessToken, "id_token": idToken, "refresh_token": refreshToken,
			"token_type": "Bearer", "expires_in": 3600, "scope": "openid profile email offline_access",
		})

	case "refresh_token":
		rt := r.Form.Get("refresh_token")
		f.mu.Lock()
		issued, ok := f.refreshTokens[rt]
		f.mu.Unlock()
		if !ok || time.Now().After(issued.expires) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		accessToken, _, _, err := f.mintTokens(issued.subject, issued.resources)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": accessToken, "refresh_token": rt,
			"token_type": "Bearer", "expires_in": 3600, "scope": "openid profile email offline_access",
		})

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (f *Fake) userinfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"sub": Identity.Subject, "name": Identity.Username, "username": Identity.Username,
		"email": Identity.Email, "picture": "",
	})
}

func (f *Fake) sessionEnd(w http.ResponseWriter, r *http.Request) {
	if redirect := r.URL.Query().Get("post_logout_redirect_uri"); redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}
