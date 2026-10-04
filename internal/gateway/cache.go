package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/heracraft/repose/internal/ca/sshca"
)

// The refresh windows of 06-gateway-edge.md §5.2, §5.3 and §6.
const (
	RevocationRefresh = 30 * time.Second
	CARefresh         = time.Hour
	// StaleAfter is how long the gateway keeps authenticating against a
	// CA and revocation list it could not refresh; past it new
	// connections are refused (§6 "api unreachable").
	StaleAfter = time.Hour
	RouteTTL   = 5 * time.Second
	CertTTL    = 4 * time.Minute
	// revocationKeep is how long a serial stays in the in-memory set after
	// the gateway first sees it. A revoked certificate was issued before
	// its revocation, so it expires within sshca.UserCertTTL of the gateway
	// learning of it; the extra hour covers clock skew between the api
	// that signs and the gateway that checks. A certificate whose validity
	// window is longer than this is refused outright (maxCertSpan), so no
	// certificate can outlive its serial's entry (I-434).
	revocationKeep = sshca.UserCertTTL + time.Hour
	// maxCertSpan is the longest ValidBefore-ValidAfter the gateway
	// accepts: the api issues UserCertTTL plus the minute of backdating.
	maxCertSpan = revocationKeep
	// revocationOverlap is re-fetched on every refresh so a revocation
	// that landed while a request was in flight is not missed.
	revocationOverlap = 30 * time.Second
)

// ErrStale is returned when the cached CA or revocation list is older than
// StaleAfter.
var ErrStale = errors.New("gateway: control plane data older than the stale window")

// revocationCache is the set of revoked serials, refreshed every 30 s from
// /internal/revoked?since=<last>.
type revocationCache struct {
	mu       sync.RWMutex
	serials  map[uint64]time.Time
	lastOK   time.Time // last successful refresh
	lastFrom time.Time // the since= of the next refresh
	primed   bool
	clock    func() time.Time
}

func newRevocationCache(clock func() time.Time) *revocationCache {
	return &revocationCache{serials: map[uint64]time.Time{}, clock: clock}
}

// Refresh fetches revocations since the last refresh (all on the first).
func (r *revocationCache) Refresh(ctx context.Context, api *Client) error {
	r.mu.RLock()
	since := r.lastFrom
	r.mu.RUnlock()
	started := r.clock()
	serials, err := api.Revoked(ctx, since)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock()
	for _, s := range serials {
		if _, seen := r.serials[s]; !seen {
			r.serials[s] = now
		}
	}
	for s, at := range r.serials {
		if now.Sub(at) > revocationKeep {
			delete(r.serials, s)
		}
	}
	r.lastOK = now
	r.lastFrom = started.Add(-revocationOverlap)
	r.primed = true
	return nil
}

// Push adds a serial without waiting for the next refresh.
func (r *revocationCache) Push(serial uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.serials[serial] = r.clock()
}

// IsRevoked reports whether a serial is in the set.
func (r *revocationCache) IsRevoked(serial uint64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.serials[serial]
	return ok
}

// Age is the time since the last successful refresh; a cache that never
// refreshed reports an age past every window.
func (r *revocationCache) Age() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.primed {
		return 100 * 365 * 24 * time.Hour
	}
	return r.clock().Sub(r.lastOK)
}

// Len is the number of serials held.
func (r *revocationCache) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.serials)
}

// caCache holds the two CA public keys from /internal/ca, refreshed hourly.
type caCache struct {
	mu     sync.RWMutex
	user   ssh.PublicKey
	host   ssh.PublicKey
	lastOK time.Time
	clock  func() time.Time
}

func newCACache(clock func() time.Time) *caCache { return &caCache{clock: clock} }

// Refresh fetches the keys.
func (c *caCache) Refresh(ctx context.Context, api *Client) error {
	keys, err := api.CA(ctx)
	if err != nil {
		return err
	}
	return c.Set(keys.UserCAPub, keys.HostCAPub)
}

// Set installs the keys from authorized_keys lines.
func (c *caCache) Set(userLine, hostLine string) error {
	user, err := sshca.ParsePublicKey(userLine)
	if err != nil {
		return fmt.Errorf("user ca: %w", err)
	}
	host, err := sshca.ParsePublicKey(hostLine)
	if err != nil {
		return fmt.Errorf("host ca: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.user, c.host = user, host
	c.lastOK = c.clock()
	return nil
}

// Keys returns the CAs, or ErrStale when they were never fetched or are
// older than StaleAfter.
func (c *caCache) Keys() (user, host ssh.PublicKey, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.user == nil || c.clock().Sub(c.lastOK) > StaleAfter {
		return nil, nil, ErrStale
	}
	return c.user, c.host, nil
}

// Age is the time since the last successful refresh.
func (c *caCache) Age() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.user == nil {
		return 100 * 365 * 24 * time.Hour
	}
	return c.clock().Sub(c.lastOK)
}

// routeCache remembers /internal/route answers for RouteTTL per login,
// so a burst of connections (VS Code opens several) is one lookup. Errors
// other than "not found" are not cached.
type routeCache struct {
	mu      sync.Mutex
	entries map[string]routeEntry
	clock   func() time.Time
}

type routeEntry struct {
	route *Route
	err   error
	at    time.Time
}

func newRouteCache(clock func() time.Time) *routeCache {
	return &routeCache{entries: map[string]routeEntry{}, clock: clock}
}

// Lookup returns the cached route or asks the api.
func (rc *routeCache) Lookup(ctx context.Context, api *Client, login string) (*Route, error) {
	now := rc.clock()
	rc.mu.Lock()
	if e, ok := rc.entries[login]; ok && now.Sub(e.at) < RouteTTL {
		rc.mu.Unlock()
		return e.route, e.err
	}
	rc.mu.Unlock()
	r, err := api.Route(ctx, login)
	if err == nil || errors.Is(err, ErrNotFound) {
		rc.mu.Lock()
		rc.entries[login] = routeEntry{route: r, err: err, at: now}
		// Keep the map bounded under a login scan.
		if len(rc.entries) > 10000 {
			for k, e := range rc.entries {
				if now.Sub(e.at) >= RouteTTL {
					delete(rc.entries, k)
				}
			}
		}
		rc.mu.Unlock()
	}
	return r, err
}

// certCache holds the gateway-issued certificate per project for CertTTL
// (one minute less than its validity), so a burst of connections to one
// project is one api call.
type certCache struct {
	mu      sync.Mutex
	entries map[string]certEntry
	clock   func() time.Time
}

type certEntry struct {
	signer ssh.Signer
	at     time.Time
}

func newCertCache(clock func() time.Time) *certCache {
	return &certCache{entries: map[string]certEntry{}, clock: clock}
}

// Signer returns a signer for the gateway's key carrying a certificate
// for the project, fetching one when the cached one is older than
// CertTTL.
func (cc *certCache) Signer(ctx context.Context, api *Client, key ssh.Signer, projectID string) (ssh.Signer, error) {
	now := cc.clock()
	cc.mu.Lock()
	if e, ok := cc.entries[projectID]; ok && now.Sub(e.at) < CertTTL {
		cc.mu.Unlock()
		return e.signer, nil
	}
	cc.mu.Unlock()
	line, err := api.GatewayCert(ctx, publicKeyLine(key.PublicKey()), projectID)
	if err != nil {
		return nil, err
	}
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, fmt.Errorf("gateway certificate: %w", err)
	}
	cert, ok := pk.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("gateway certificate: api returned a plain key")
	}
	signer, err := ssh.NewCertSigner(cert, key)
	if err != nil {
		return nil, fmt.Errorf("gateway certificate: %w", err)
	}
	cc.mu.Lock()
	cc.entries[projectID] = certEntry{signer: signer, at: now}
	for id, e := range cc.entries {
		if now.Sub(e.at) >= CertTTL {
			delete(cc.entries, id)
		}
	}
	cc.mu.Unlock()
	return signer, nil
}

// publicKeyLine renders a public key as one authorized_keys line without a
// trailing newline.
func publicKeyLine(pk ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
}
