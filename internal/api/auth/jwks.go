// Package auth verifies Logto access tokens against the endpoint's JWKS,
// creates users on first sign-in with a handle derived from the GitHub
// login, and is the middleware every user route sits behind
// (05-control-plane-api.md §5.2).
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrIdentityProviderUnavailable is `unauthenticated: identity provider
// unavailable`: no usable JWKS.
var ErrIdentityProviderUnavailable = errors.New("identity provider unavailable")

// ErrInvalidToken covers every verification failure.
var ErrInvalidToken = errors.New("invalid token")

// Claims is what a verified token yields.
type Claims struct {
	Sub string
	Exp time.Time
}

// Verifier checks tokens.
type Verifier struct {
	issuer   string
	audience string
	http     *http.Client

	mu        sync.Mutex
	keys      map[string]any
	fetched   time.Time
	attempted time.Time // last fetch started, successful or not
	cacheTTL  time.Duration
	staleMax  time.Duration
	nowFunc   func() time.Time
	lastError error

	// refreshMu lets one fetch run at a time; callers that queued behind
	// it find attempted recent and use what it fetched.
	refreshMu sync.Mutex
}

// MinRefreshInterval is the least time between two JWKS fetches. The kid is
// read before the signature is checked, so without it any request with an
// unseen kid would make the api fetch the JWKS: a key Logto rotates in is
// picked up at most this long after the previous fetch.
const MinRefreshInterval = 30 * time.Second

// NewVerifier makes a verifier for the issuer and API resource.
func NewVerifier(issuer, audience string, client *http.Client) *Verifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Verifier{issuer: strings.TrimRight(issuer, "/"), audience: audience, http: client, keys: map[string]any{}, cacheTTL: time.Hour, staleMax: 24 * time.Hour, nowFunc: time.Now}
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func decodeBig(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

func (k jwk) public() (any, error) {
	switch k.Kty {
	case "RSA":
		n, err := decodeBig(k.N)
		if err != nil {
			return nil, err
		}
		e, err := decodeBig(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve %s", k.Crv)
		}
		xb, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		yb, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		size := (curve.Params().BitSize + 7) / 8
		if len(xb) > size || len(yb) > size {
			return nil, errors.New("jwk: coordinate too long")
		}
		point := make([]byte, 1+2*size)
		point[0] = 4
		copy(point[1+size-len(xb):], xb)
		copy(point[1+2*size-len(yb):], yb)
		return ecdsa.ParseUncompressedPublicKey(curve, point)
	}
	return nil, fmt.Errorf("unsupported key type %s", k.Kty)
}

// refresh fetches the JWKS; on failure the cache is served up to staleMax.
func (v *Verifier) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.issuer+"/oidc/jwks", nil)
	if err != nil {
		return err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // body drained below
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("jwks: %w", err)
	}
	keys := map[string]any{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, err := k.public()
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("jwks: no usable keys")
	}
	v.mu.Lock()
	v.keys, v.fetched, v.lastError = keys, v.nowFunc(), nil
	v.mu.Unlock()
	return nil
}

func (v *Verifier) key(ctx context.Context, kid string) (any, error) {
	v.mu.Lock()
	now := v.nowFunc()
	fresh := now.Sub(v.fetched) < v.cacheTTL
	k, ok := v.keys[kid]
	v.mu.Unlock()
	if ok && fresh {
		return k, nil
	}
	err := v.maybeRefresh(ctx)
	v.mu.Lock()
	defer v.mu.Unlock()
	if err != nil {
		if len(v.keys) == 0 || now.Sub(v.fetched) > v.staleMax {
			return nil, fmt.Errorf("%w: %v", ErrIdentityProviderUnavailable, err)
		}
	}
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown key id", ErrInvalidToken)
}

// maybeRefresh fetches the JWKS unless a fetch started less than
// MinRefreshInterval ago, in which case it reports that fetch's result.
// The fetch runs detached from the caller's context, so one client giving
// up does not fail the requests queued behind it; the http client's
// timeout bounds it.
func (v *Verifier) maybeRefresh(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	v.mu.Lock()
	now := v.nowFunc()
	if !v.attempted.IsZero() && now.Sub(v.attempted) >= 0 && now.Sub(v.attempted) < MinRefreshInterval {
		err := v.lastError
		v.mu.Unlock()
		return err
	}
	v.attempted = now
	v.mu.Unlock()
	err := v.refresh(context.WithoutCancel(ctx))
	if err != nil {
		v.mu.Lock()
		v.lastError = err
		v.mu.Unlock()
	}
	return err
}

// Verify checks signature, issuer, audience and expiry.
//
// The configured issuer is the Logto endpoint (https://accounts.example);
// the tokens it signs carry iss = <endpoint>/oidc, the same base the JWKS
// and token URLs above hang off. Until 2026-09-20 this compared iss with
// the bare endpoint, which only internal/fakes/logto ever produced: a real
// Logto token would have been refused as "invalid issuer" (DECISIONS I-85).
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	var claims jwt.RegisteredClaims
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256", "ES256", "ES384", "ES512"}), jwt.WithIssuer(v.issuer+"/oidc"), jwt.WithAudience(v.audience), jwt.WithExpirationRequired(), jwt.WithLeeway(30*time.Second))
	_, err := parser.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.key(ctx, kid)
	})
	if err != nil {
		if errors.Is(err, ErrIdentityProviderUnavailable) {
			return Claims{}, err
		}
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if claims.Subject == "" {
		return Claims{}, fmt.Errorf("%w: no subject", ErrInvalidToken)
	}
	return Claims{Sub: claims.Subject, Exp: claims.ExpiresAt.Time}, nil
}
