package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// The messages a user sees, as banners, for each refusal
// (06-gateway-edge.md §5.2, §5.3 and §6; docs/interfaces/ssh-gateway.md).
const (
	MsgCertRequired   = "permission denied (certificate required)"
	MsgBadCA          = "permission denied (certificate not signed by the repose CA)"
	MsgExpired        = "permission denied (certificate expired)"
	MsgNotYetValid    = "permission denied (certificate not yet valid)"
	MsgRevoked        = "permission denied (certificate revoked)"
	MsgWrongPrincipal = "certificate not valid for this project"
	MsgControlPlane   = "gateway cannot reach control plane; try again shortly"
	MsgBusy           = "gateway busy"
	MsgUserBusy       = "too many open connections for your account; close some and try again"
	MsgRateLimited    = "too many authentication attempts from your address; try again later"
	MsgNotReady       = "environment is not accepting connections yet"
	// MsgStoppedFmt takes the slug twice.
	MsgStoppedFmt = "%s is stopped; run `repose start %s`"
	// MsgNotFoundFmt takes the login name.
	MsgNotFoundFmt = "no such project: %s"
	// MsgStateFmt takes the slug and a transitional state (creating,
	// building, starting, restoring).
	MsgStateFmt = "%s is %s and not accepting connections yet; try again in a few seconds"
	// MsgDestroyingFmt takes the slug twice.
	MsgDestroyingFmt = "%s is being destroyed; `repose restore %s` brings it back once that is done"
	// MsgErrorFmt takes the slug twice.
	MsgErrorFmt = "%s is in error; `repose status %s` says why"
	// MsgUnreachableHostFmt takes the slug.
	MsgUnreachableHostFmt = "%s is on a host the control plane cannot reach right now; try again shortly"
)

// stateMessage is the banner for a project that is not running (I-189).
func stateMessage(slug, state string) string {
	switch state {
	case "stopped", "stopping":
		return fmt.Sprintf(MsgStoppedFmt, slug, slug)
	case "destroying", "destroyed":
		return fmt.Sprintf(MsgDestroyingFmt, slug, slug)
	case "error":
		return fmt.Sprintf(MsgErrorFmt, slug, slug)
	default:
		return fmt.Sprintf(MsgStateFmt, slug, state)
	}
}

// Config configures a Gateway.
type Config struct {
	API *Client
	// HostKey is presented to clients: a cert signer when the Host CA
	// certificate exists, the plain key otherwise.
	HostKey ssh.Signer
	// GatewayKey is the gateway's own key pair, certified per project by
	// the api for the dial to the guest.
	GatewayKey ssh.Signer
	// GuestPort is the guest sshd port (22).
	GuestPort int

	// MaxConns caps relays (authenticated connections); MaxConnsPerUser
	// caps one user's share of them. MaxPreAuth caps connections still in
	// the handshake, MaxAuthPerIP one source's share of those (I-435).
	MaxConns        int
	MaxConnsPerUser int
	MaxPreAuth      int
	MaxAuthPerIP    int
	AuthTimeout     time.Duration
	MaxAuthTries    int
	DialTimeout     time.Duration
	Keepalive       time.Duration
	SessionCap      time.Duration
	// RevocationRefresh and CARefresh override the §5.2 windows (tests).
	RevocationRefresh time.Duration
	CARefresh         time.Duration

	Log     *slog.Logger
	Metrics *obsmetrics.GatewayMetrics
	Clock   func() time.Time
	// Dial replaces the TCP dial to the guest (tests).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// ServerVersion is the SSH version string; empty means the default.
	ServerVersion string
}

// Gateway is one running gateway.
type Gateway struct {
	cfg     Config
	revoked *revocationCache
	cas     *caCache
	routes  *routeCache
	certs   *certCache
	limiter *limiter
	conns   connCounter
	preAuth connCounter
	users   *keyedCounter
	wg      sync.WaitGroup
	log     *slog.Logger

	// sessions are the open relays, so a revocation can end them (I-436).
	sessMu   sync.Mutex
	sessions map[*session]struct{}
}

// New validates the configuration and applies the defaults of §5.2.
func New(cfg Config) (*Gateway, error) {
	if cfg.API == nil {
		return nil, errors.New("gateway: api client is required")
	}
	if cfg.HostKey == nil || cfg.GatewayKey == nil {
		return nil, errors.New("gateway: host key and gateway key are required")
	}
	if cfg.GuestPort == 0 {
		cfg.GuestPort = 22
	}
	if cfg.MaxConns == 0 {
		cfg.MaxConns = DefaultMaxConns
	}
	if cfg.MaxConnsPerUser == 0 {
		cfg.MaxConnsPerUser = DefaultMaxConnsPerUser
	}
	if cfg.MaxPreAuth == 0 {
		cfg.MaxPreAuth = DefaultMaxPreAuth
	}
	if cfg.MaxAuthPerIP == 0 {
		cfg.MaxAuthPerIP = DefaultMaxAuthPerIP
	}
	if cfg.AuthTimeout == 0 {
		cfg.AuthTimeout = DefaultAuthTimeout
	}
	if cfg.MaxAuthTries == 0 {
		cfg.MaxAuthTries = DefaultMaxAuthTries
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.Keepalive == 0 {
		cfg.Keepalive = 30 * time.Second
	}
	if cfg.SessionCap == 0 {
		cfg.SessionCap = 24 * time.Hour
	}
	if cfg.RevocationRefresh == 0 {
		cfg.RevocationRefresh = RevocationRefresh
	}
	if cfg.CARefresh == 0 {
		cfg.CARefresh = CARefresh
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = obs.Nop(obs.ComponentGateway)
	}
	if cfg.Metrics == nil {
		cfg.Metrics = obsmetrics.NewGatewayMetrics(obsmetrics.New(obs.ComponentGateway))
	}
	if cfg.Dial == nil {
		cfg.Dial = (&net.Dialer{}).DialContext
	}
	g := &Gateway{
		cfg:      cfg,
		revoked:  newRevocationCache(cfg.Clock),
		cas:      newCACache(cfg.Clock),
		routes:   newRouteCache(cfg.Clock),
		certs:    newCertCache(cfg.Clock),
		limiter:  newLimiter(cfg.MaxAuthPerIP, cfg.Clock),
		conns:    connCounter{max: cfg.MaxConns},
		preAuth:  connCounter{max: cfg.MaxPreAuth},
		users:    newKeyedCounter(cfg.MaxConnsPerUser),
		log:      cfg.Log,
		sessions: map[*session]struct{}{},
	}
	return g, nil
}

// Prime fetches the CA keys and the revocation list once. Serve works
// without it (every connection is refused with MsgControlPlane until a
// refresh succeeds), so a start with the api down still binds the port.
func (g *Gateway) Prime(ctx context.Context) error {
	if err := g.cas.Refresh(ctx, g.cfg.API); err != nil {
		return fmt.Errorf("ca: %w", err)
	}
	if err := g.revoked.Refresh(ctx, g.cfg.API); err != nil {
		return fmt.Errorf("revoked: %w", err)
	}
	return nil
}

// SetCAs installs CA keys without the api (tests, and a hostdev edge).
func (g *Gateway) SetCAs(userLine, hostLine string) error { return g.cas.Set(userLine, hostLine) }

// Revoke pushes a serial into the revocation set immediately and ends the
// relays authenticated with it.
func (g *Gateway) Revoke(serial uint64) {
	g.revoked.Push(serial)
	g.endRevoked()
}

// endRevoked ends every open relay whose certificate is now revoked
// (I-436). The relay's own teardown closes the guest connection, then the
// client's.
func (g *Gateway) endRevoked() {
	g.sessMu.Lock()
	defer g.sessMu.Unlock()
	for s := range g.sessions {
		if g.revoked.IsRevoked(s.serial) {
			s.end(endRevoked)
		}
	}
}

func (g *Gateway) addSession(s *session) {
	g.sessMu.Lock()
	defer g.sessMu.Unlock()
	g.sessions[s] = struct{}{}
}

func (g *Gateway) removeSession(s *session) {
	g.sessMu.Lock()
	defer g.sessMu.Unlock()
	delete(g.sessions, s)
}

// RefreshLoop keeps the revocation list (every 30 s) and the CA keys
// (hourly) fresh until ctx ends. Failures are logged and counted in the
// cache age; auth refuses once the age passes StaleAfter.
func (g *Gateway) RefreshLoop(ctx context.Context) {
	revTick := time.NewTicker(g.cfg.RevocationRefresh)
	caTick := time.NewTicker(g.cfg.CARefresh)
	defer revTick.Stop()
	defer caTick.Stop()
	refreshRev := func() {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := g.revoked.Refresh(rctx, g.cfg.API); err != nil {
			g.log.Warn("revocation refresh failed", "event", "route_fail", "reason", "revoked_refresh", "age_ms", g.revoked.Age().Milliseconds(), "err", err.Error())
		} else {
			g.endRevoked()
		}
		g.cfg.Metrics.RevocationCacheAge.Set(g.revoked.Age().Seconds())
	}
	refreshCA := func() {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := g.cas.Refresh(rctx, g.cfg.API); err != nil {
			g.log.Warn("ca refresh failed", "event", "route_fail", "reason", "ca_refresh", "age_ms", g.cas.Age().Milliseconds(), "err", err.Error())
		}
	}
	if g.cas.Age() > g.cfg.CARefresh {
		refreshCA()
	}
	if g.revoked.Age() > g.cfg.RevocationRefresh {
		refreshRev()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-revTick.C:
			refreshRev()
			// A CA that never loaded is retried with the revocations.
			if g.cas.Age() > g.cfg.CARefresh {
				refreshCA()
			}
		case <-caTick.C:
			refreshCA()
		}
	}
}

// Serve accepts connections until the listener closes or ctx ends, then
// waits for open relays to finish (they end when ctx ends). Closing the
// listener while ctx lives is a drain: no new connection is taken, and the
// open relays run until they end on their own (a handover, I-471).
func (g *Gateway) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close() // unblocks Accept; the error is the listener's, not ours
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				g.wg.Wait()
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			g.wg.Wait()
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.HandleConn(ctx, c)
		}()
	}
}

// Open is the number of relays open.
func (g *Gateway) Open() int { return g.conns.open() }

// connState is what authentication decides for one connection.
type connState struct {
	// accepted holds what the public key callback decided for each
	// certificate it accepted, by serial. ssh calls the callback for every
	// key the client asks about, and a query without a signature is not an
	// authentication, so nothing here is used until the handshake says
	// which certificate signed (I-435).
	accepted map[uint64]*authRecord
	// the last failure, for the log line
	result string
	// told is the last banner sent on this connection. ssh offers the
	// certificate and then the plain key on one connection; each refusal's
	// banner used to be printed back to back with no newline between
	// them ("…not accepting connections yetpermission denied (certificate
	// required)…"). A banner now ends in a newline, the plain key's
	// "certificate required" after a banner already sent is not shown, and
	// neither is the same banner twice (I-189).
	told string
}

// authRecord is one accepted certificate's route and identity.
type authRecord struct {
	route       *Route
	slug        string
	handle      string
	serial      uint64
	user        string
	validBefore time.Time // zero for a certificate without expiry
}

// maxAcceptedKeys bounds the certificates one connection may have
// accepted while it authenticates.
const maxAcceptedKeys = 8

// HandleConn runs one connection: limits, SSH handshake with certificate
// authentication, then the relay.
func (g *Gateway) HandleConn(ctx context.Context, c net.Conn) {
	src := sourceKey(c.RemoteAddr())
	prefix := sourcePrefix(c.RemoteAddr())
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)                  // best effort; the SSH keepalive is the real check
		_ = tc.SetKeepAlivePeriod(g.cfg.Keepalive) // same
	}
	// The pre-auth budgets are checked before the handshake: a banned or
	// over-limit source, or a gateway with every pre-auth slot taken, is
	// answered with one plain line and closed at once, holding nothing
	// (I-435).
	if !g.limiter.beginAuth(src) {
		g.refuseEarly(ctx, c, ResultRateLimited, MsgRateLimited, prefix)
		return
	}
	if !g.preAuth.acquire() {
		g.limiter.endAuth(src, false)
		g.refuseEarly(ctx, c, ResultBusy, MsgBusy, prefix)
		return
	}
	st := &connState{accepted: map[uint64]*authRecord{}}

	_ = c.SetDeadline(time.Now().Add(g.cfg.AuthTimeout)) // a conn that cannot take a deadline fails the handshake instead
	sconn, chans, reqs, err := ssh.NewServerConn(c, g.serverConfig(ctx, st))
	// The per-source slot covers the authentication phase only (§5.2: N
	// concurrent auth *attempts*), not the whole relay; release it here so a
	// user's own many connections are not throttled against each other.
	g.preAuth.release()
	g.limiter.endAuth(src, err != nil)
	if err != nil {
		_ = c.Close() // already failing; nothing to report to
		if st.result == "" {
			st.result = "handshake"
		}
		if st.result != ResultNoCert || g.log.Enabled(ctx, slog.LevelDebug) {
			g.log.Info("authentication failed", "event", "auth_fail", "reason", st.result, "source_prefix", prefix)
		}
		return
	}
	_ = c.SetDeadline(time.Time{}) // same conn as above; a failure here surfaces on the next read
	// The certificate that signed is the one in the permissions ssh
	// returns; the records of keys only asked about are dropped.
	rec := st.accepted[permSerial(sconn.Permissions)]
	if rec == nil {
		_ = sconn.Close() // cannot happen: every accepted key has a record
		return
	}
	sess := &session{
		gw:        g,
		conn:      sconn,
		chans:     chans,
		reqs:      reqs,
		route:     rec.route,
		slug:      rec.slug,
		handle:    rec.handle,
		serial:    rec.serial,
		validTo:   rec.validBefore,
		id:        newSessionID(),
		prefix:    prefix,
		startedAt: g.cfg.Clock(),
		log:       g.log,
	}
	// The relay budgets (I-435), taken once per connection, after the
	// handshake. Over either, every session the client opens is told why
	// on stderr with exit 255, as for a guest that cannot be reached.
	if !g.conns.acquire() {
		g.refuseRelay(ctx, sess, MsgBusy, prefix)
		return
	}
	defer g.conns.release()
	if !g.users.acquire(rec.user) {
		g.refuseRelay(ctx, sess, MsgUserBusy, prefix)
		return
	}
	defer g.users.release(rec.user)
	sess.run(ctx)
}

// refuseRelay answers an authenticated connection that has no relay slot.
func (g *Gateway) refuseRelay(ctx context.Context, sess *session, message, prefix string) {
	g.cfg.Metrics.AuthFailTotal.WithLabelValues(ResultBusy).Inc()
	g.log.Info("authentication failed", "event", "auth_fail", "reason", ResultBusy, "source_prefix", prefix)
	sess.refuse(ctx, message)
}

// permSerial is the certificate serial authenticate put in the
// permissions; 0 when there is none.
func permSerial(p *ssh.Permissions) uint64 {
	if p == nil {
		return 0
	}
	n, _ := strconv.ParseUint(p.Extensions["repose-cert-serial"], 10, 64) // written by authenticate; 0 matches no record
	return n
}

// refuseEarly answers a connection refused before its handshake: one line
// ahead of any SSH version string, which clients skip or show with -v, then
// close. No key exchange is spent on it.
func (g *Gateway) refuseEarly(ctx context.Context, c net.Conn, result, message, prefix string) {
	g.cfg.Metrics.AuthFailTotal.WithLabelValues(result).Inc()
	_ = c.SetWriteDeadline(time.Now().Add(time.Second)) // best effort: the line is a courtesy
	_, _ = c.Write([]byte("repose gateway: " + message + "\r\n"))
	_ = c.Close() // refused; nothing more to say
	if g.log.Enabled(ctx, slog.LevelDebug) {
		g.log.Debug("connection refused before handshake", "event", "auth_fail", "reason", result, "source_prefix", prefix)
	}
}

// serverConfig is the per-connection SSH server configuration.
func (g *Gateway) serverConfig(ctx context.Context, st *connState) *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		MaxAuthTries:  g.cfg.MaxAuthTries,
		ServerVersion: g.cfg.ServerVersion,
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return g.authenticate(ctx, st, conn, key)
		},
		BannerCallback: func(conn ssh.ConnMetadata) string {
			// The login name arrives with the first auth request; a bad one
			// is told before any key is tried.
			if _, _, err := ParseLogin(conn.User()); err != nil {
				return BadLoginMessage
			}
			return ""
		},
	}
	cfg.AddHostKey(g.cfg.HostKey)
	return cfg
}

// fail records the result and returns the banner error.
func (g *Gateway) fail(st *connState, result, message string) (*ssh.Permissions, error) {
	st.result = result
	g.cfg.Metrics.AuthFailTotal.WithLabelValues(result).Inc()
	if message == st.told || (st.told != "" && result == ResultNoCert) {
		message = ""
	} else {
		st.told = message
		message += "\n"
	}
	return nil, &ssh.BannerError{Err: errors.New(result), Message: message}
}

// authenticate is the decision chain of §5.2: certificate, CA, validity,
// revocation, route, principal, state.
func (g *Gateway) authenticate(ctx context.Context, st *connState, conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	slug, handle, err := ParseLogin(conn.User())
	if err != nil {
		return g.fail(st, ResultBadLogin, BadLoginMessage)
	}
	cert, ok := key.(*ssh.Certificate)
	if !ok || cert.CertType != ssh.UserCert {
		return g.fail(st, ResultNoCert, MsgCertRequired)
	}
	userCA, _, err := g.cas.Keys()
	if err != nil {
		return g.fail(st, ResultRouteError, MsgControlPlane)
	}
	if g.revoked.Age() > StaleAfter {
		return g.fail(st, ResultRouteError, MsgControlPlane)
	}
	if !keysEqual(cert.SignatureKey, userCA) {
		return g.fail(st, ResultBadCA, MsgBadCA)
	}
	now := g.cfg.Clock().Unix()
	if now < int64(cert.ValidAfter) {
		return g.fail(st, ResultExpired, MsgNotYetValid)
	}
	if cert.ValidBefore != uint64(ssh.CertTimeInfinity) && now >= int64(cert.ValidBefore) {
		return g.fail(st, ResultExpired, MsgExpired)
	}
	// The revocation set keeps a serial for revocationKeep; a certificate
	// valid for longer, or forever, could outlive its entry (I-434).
	if cert.ValidBefore == uint64(ssh.CertTimeInfinity) || cert.ValidBefore < cert.ValidAfter ||
		time.Duration(cert.ValidBefore-cert.ValidAfter)*time.Second > maxCertSpan {
		return g.fail(st, ResultBadCA, MsgBadCA)
	}
	if g.revoked.IsRevoked(cert.Serial) {
		return g.fail(st, ResultRevoked, MsgRevoked)
	}
	// A login under another user's handle gets the same answer whether or
	// not that project exists, and costs no route lookup (I-437). The
	// handle is the one the api signed into key_id; after a handle rename
	// the CLI's one re-issue on this banner brings the new one. The
	// gateway's own certificates (key_id ending :via-gateway, I-431) are
	// for the gateway's dial to a guest and do not split, so a client that
	// presents one is refused here too.
	userID, certHandle, ok := certUser(cert.KeyId)
	if !ok || certHandle != handle {
		return g.fail(st, ResultWrongPrincipal, MsgWrongPrincipal)
	}
	login := slug + "." + handle
	started := g.cfg.Clock()
	route, err := g.routes.Lookup(ctx, g.cfg.API, login)
	g.cfg.Metrics.RouteDuration.Observe(g.cfg.Clock().Sub(started).Seconds())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return g.fail(st, ResultNotFound, fmt.Sprintf(MsgNotFoundFmt, login))
		}
		g.log.Warn("route lookup failed", "event", "route_fail", "reason", "api", "err", err.Error())
		return g.fail(st, ResultRouteError, MsgControlPlane)
	}
	if len(cert.ValidPrincipals) == 0 || !contains(cert.ValidPrincipals, route.ProjectID) {
		return g.fail(st, ResultWrongPrincipal, MsgWrongPrincipal)
	}
	switch route.State {
	case "running":
		if route.GuestIP == "" || route.HostUnreachable {
			return g.fail(st, ResultRouteError, fmt.Sprintf(MsgUnreachableHostFmt, slug))
		}
	default:
		return g.fail(st, ResultStopped, stateMessage(slug, route.State))
	}
	// The authoritative check: signature, critical options, validity and
	// revocation again, with the project id as the principal.
	checker := &ssh.CertChecker{
		SupportedCriticalOptions: []string{"source-address"},
		IsUserAuthority:          func(auth ssh.PublicKey) bool { return keysEqual(auth, userCA) },
		IsRevoked:                func(c *ssh.Certificate) bool { return g.revoked.IsRevoked(c.Serial) },
		Clock:                    g.cfg.Clock,
	}
	if err := checker.CheckCert(route.ProjectID, cert); err != nil {
		return g.fail(st, ResultBadCA, MsgBadCA)
	}
	if _, seen := st.accepted[cert.Serial]; !seen && len(st.accepted) >= maxAcceptedKeys {
		return g.fail(st, ResultRateLimited, MsgRateLimited)
	}
	rec := &authRecord{route: route, slug: slug, handle: handle, serial: cert.Serial, user: userID}
	if cert.ValidBefore != uint64(ssh.CertTimeInfinity) {
		rec.validBefore = time.Unix(int64(cert.ValidBefore), 0)
	}
	st.accepted[cert.Serial] = rec
	st.result = ResultOK
	perms := &ssh.Permissions{
		CriticalOptions: cert.CriticalOptions,
		Extensions: map[string]string{
			"repose-project-id":  route.ProjectID,
			"repose-guest-ip":    route.GuestIP,
			"repose-cert-serial": strconv.FormatUint(cert.Serial, 10),
		},
	}
	return perms, nil
}

// certUser splits a user certificate's key_id, "<user_id>:<handle>"
// (docs/interfaces/ssh-gateway.md). The gateway's own certificates carry a
// third part and do not split.
func certUser(keyID string) (userID, handle string, ok bool) {
	parts := strings.Split(keyID, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func keysEqual(a, b ssh.PublicKey) bool {
	return a != nil && b != nil && a.Type() == b.Type() && bytes.Equal(a.Marshal(), b.Marshal())
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sourcePrefix is the source address truncated to /24 (IPv4) or /48
// (IPv6), the only form of it that is logged (§5.5).
func sourcePrefix(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "unknown"
	}
	if v4 := ip.To4(); v4 != nil {
		return (&net.IPNet{IP: v4.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}).String()
	}
	return (&net.IPNet{IP: ip.Mask(net.CIDRMask(48, 128)), Mask: net.CIDRMask(48, 128)}).String()
}
