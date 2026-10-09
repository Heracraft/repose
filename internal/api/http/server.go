// Package httpapi serves docs/interfaces/api.md: the user routes behind
// Logto JWT verification on the HTTP app, the /internal routes behind
// the gateway's client certificate, and /healthz and /readyz; /metrics is
// the separate metrics listener only (DECISIONS I-136).
// Every response carries X-Request-Id; errors use the documented
// envelope; ids are validated as UUIDs before the database is touched.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/heracraft/repose/internal/api/auth"
	"github.com/heracraft/repose/internal/api/buildlog"
	"github.com/heracraft/repose/internal/api/ca"
	"github.com/heracraft/repose/internal/api/config"
	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/questions"
	"github.com/heracraft/repose/internal/api/ratelimit"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/waitlist"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// Gateway is what POST /certs tells the CLI about the SSH gateway.
type Gateway struct {
	Host string
	Port int
}

// Deps is everything the handlers need.
type Deps struct {
	Pool     *db.Pool
	Verifier *auth.Verifier
	Users    *auth.Provisioner
	CA       *ca.CA
	Secrets  *secrets.Store
	Engine   *ops.Engine
	Logs     *buildlog.Store
	Events   *events.Ingest
	Outbox   *notify.Outbox
	// Unsub verifies the email unsubscribe link (13-notifications.md §5.6);
	// nil makes /notify/unsubscribe answer 503 rather than panic.
	Unsub *notify.Unsubscriber
	// Questions is repose-ask's store (DECISIONS I-245); nil answers the
	// question routes with 500.
	Questions *questions.Service
	Parser    *config.Parser
	Metrics   *metrics.M
	Registry  *prometheus.Registry
	Log       *slog.Logger
	// Billing is the /billing routes' service (DECISIONS I-289); nil makes
	// every one answer 503 billing_disabled.
	Billing *billing.Service
	// Webhooks applies Paddle events; nil makes POST /billing/webhook
	// answer 503 billing_disabled.
	Webhooks *billing.Webhooks
	// Gate is the compute gate; nil builds a disabled one (no Paddle, so
	// every non-exempt user is subscription_required) that honours
	// BillingEnforce.
	Gate *billing.Gate
	// BillingEnforce is BILLING_ENFORCE (§8): false keeps metering but
	// stops blocking starts. Read only when Gate is nil.
	BillingEnforce bool
	Gateway        Gateway
	// Migrations reports pending migrations for /healthz.
	Migrations func(ctx context.Context) (pending int, err error)
	// Limits override the documented per-minute rate limits (tests).
	Limits *RateLimits
	// Seats is the seats waitlist (DECISIONS I-290): POST /billing/waitlist
	// joins it, GET /public/seats and GET /billing read its count, and
	// checkout asks it before a Paddle transaction. nil answers those
	// routes with 500 (tests that do not care).
	Seats *waitlist.Service
}

// RateLimits are the per-user limits from docs/interfaces/api.md.
type RateLimits struct {
	// General is every request that is not a GET.
	General int
	// Reads is GET requests, which a waiting CLI and an open dashboard tab
	// send every second or so; zero means ten times General (I-187).
	Reads  int
	Certs  int
	Config int
}

// ReplyLinkRate caps tries per question on the public reply links.
const ReplyLinkRate = 20

// DefaultRateLimits are 60/min for writes, 600/min for reads, 10/min POST
// /certs, 5/min PUT /config (api.md "Rate limits", DECISIONS I-187).
var DefaultRateLimits = RateLimits{General: 60, Reads: 600, Certs: 10, Config: 5}

// Server holds the routers.
type Server struct {
	d        Deps
	user     *http.ServeMux
	internal *http.ServeMux
	routes   []string
	general  *ratelimit.Limiter
	reads    *ratelimit.Limiter
	certs    *ratelimit.Limiter
	cfg      *ratelimit.Limiter
	replies  *ratelimit.Limiter // per question, on the public reply links
	sessions *sessionTracker
	seats    seatsCache // GET /public/seats, a minute old at most
	ready    bool
	waiters  opWaiters   // held op reads (I-236)
	draining atomic.Bool // SetReady(false): held op reads answer now
	mu       sync.Mutex
}

// New builds the server and registers every route.
func New(d Deps) *Server {
	if d.Gate == nil {
		d.Gate = billing.NewGate(d.Pool, billing.Config{Enforce: d.BillingEnforce}, d.Metrics, d.Log)
	}
	if d.Gateway.Host == "" {
		d.Gateway = Gateway{Host: "ssh.repose.herakraft.co", Port: 22}
	}
	lim := DefaultRateLimits
	if d.Limits != nil {
		lim = *d.Limits
	}
	if lim.Reads == 0 {
		lim.Reads = 10 * lim.General
	}
	s := &Server{d: d, user: http.NewServeMux(), internal: http.NewServeMux(),
		general: ratelimit.New(lim.General), reads: ratelimit.New(lim.Reads), certs: ratelimit.New(lim.Certs), cfg: ratelimit.New(lim.Config), replies: ratelimit.New(ReplyLinkRate), sessions: newSessionTracker(d.Pool)}
	s.registerUserRoutes()
	s.registerInternalRoutes()
	s.route(s.user, "GET /v1/notify/unsubscribe", s.unsubscribeGet)
	s.route(s.user, "POST /v1/notify/unsubscribe", s.unsubscribePost)
	// Reply links carry their own signed token, like the unsubscribe link.
	s.route(s.user, "GET /v1/questions/reply", s.replyGet)
	s.route(s.user, "POST /v1/questions/reply", s.replyPost)
	s.user.HandleFunc("GET /healthz", s.healthz)
	s.user.HandleFunc("GET /readyz", s.readyz)
	// /metrics is not on this mux. The user listener sits behind the public
	// proxy, so anything mounted here is on the internet; the metrics
	// listener (MetricsHandler, API_METRICS_LISTEN) is the only place the
	// registry is served (DECISIONS I-136).
	return s
}

// Handler is the user-facing router with middleware.
func (s *Server) Handler() http.Handler { return s.wrap(s.user, "http") }

// InternalHandler is the /internal router; the listener enforces the
// gateway's client certificate.
func (s *Server) InternalHandler() http.Handler { return s.wrap(s.internal, "internal") }

// MetricsHandler serves /metrics alone (the :9103 listener).
func (s *Server) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(s.d.Registry, promhttp.HandlerOpts{})
}

// Routes lists every registered "METHOD /v1/path" pattern.
func (s *Server) Routes() []string {
	out := append([]string(nil), s.routes...)
	sort.Strings(out)
	return out
}

// SetReady flips /readyz. false is the start of a drain: held op reads
// (I-236) answer at their next check instead of holding the shutdown.
func (s *Server) SetReady(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = v
	s.draining.Store(!v)
}

type handler func(w http.ResponseWriter, r *http.Request) error

func (s *Server) route(mux *http.ServeMux, pattern string, h handler) {
	s.routes = append(s.routes, pattern)
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

// --- errors -----------------------------------------------------------

// Error is the documented envelope.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func withDetail(e *Error, detail map[string]any) *Error {
	e.Detail = detail
	return e
}

func statusOf(code string) int {
	switch code {
	case "unauthenticated":
		return http.StatusUnauthorized
	case "forbidden":
		return http.StatusForbidden
	case "not_found":
		return http.StatusNotFound
	case "invalid":
		return http.StatusBadRequest
	case "conflict":
		return http.StatusConflict
	case "payment_required":
		return http.StatusPaymentRequired
	case "capacity", "waitlisted", "billing_disabled":
		return http.StatusServiceUnavailable
	case "rate_limited":
		return http.StatusTooManyRequests
	}
	return http.StatusInternalServerError
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	switch {
	case errors.As(err, &e):
	case errors.Is(err, db.ErrNotFound):
		e = errf("not_found", "not found")
	case errors.Is(err, ops.ErrOpInProgress):
		e = errf("conflict", "an operation is in progress")
	case errors.Is(err, secrets.ErrKeyServiceUnavailable):
		e = errf("internal", "key service unavailable")
	case errors.Is(err, billing.ErrDisabled):
		e = errf("billing_disabled", "billing is not configured")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		e = errf("internal", "request cancelled")
	default:
		obs.Logger(r.Context(), s.d.Log).Error("request failed", "event", "request_error", "err", err.Error())
		e = errf("internal", "internal error")
	}
	writeJSON(w, statusOf(e.Code), map[string]any{"error": e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the client went away; nothing to report
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errf("invalid", "request body too large")
		}
		return errf("invalid", "malformed body: %v", err)
	}
	return nil
}

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, errf("invalid", "%s is not a valid id", name)
	}
	return id, nil
}

// --- middleware -----------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// corsAllowedHeaders and corsAllowedMethods cover every header and verb the
// dashboard and CLI send from a browser (I-79): the SSE log route is a GET
// with no custom headers, so it needs no preflight at all, and every other
// route sends at most a bearer token and a JSON body.
const corsAllowedHeaders = "Authorization, Content-Type"
const corsAllowedMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"

func (s *Server) wrap(mux *http.ServeMux, component string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if component == "http" {
			// The dashboard (repose.herakraft.co) and the api
			// (api.repose.herakraft.co) are different origins, and every
			// route here is authenticated by a bearer token, never a
			// cookie, so a wildcard origin leaks no ambient credential
			// (I-79). /internal is never called from a browser and gets
			// no CORS headers at all.
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		start := time.Now()
		rid := r.Header.Get("X-Request-Id")
		if rid == "" || len(rid) > 64 {
			rid = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", rid)
		log := s.d.Log.With("request_id", rid)
		// info is filled in by authed further down; the request the mux
		// sees is a copy, so neither its Pattern nor its context reaches
		// back here without it (every route logged as "unmatched" and no
		// request line carried a user_id before this).
		info := &reqInfo{}
		ctx := context.WithValue(obs.WithLogger(r.Context(), log), reqInfoKey, info)
		req := r.WithContext(ctx)
		sw := &statusWriter{ResponseWriter: w, status: 200}
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("handler panicked", "event", "request_panic", "route", req.Pattern)
				if sw.status == 200 {
					writeJSON(sw, http.StatusInternalServerError, map[string]any{"error": errf("internal", "internal error")})
				}
			}
			route := req.Pattern
			if route == "" {
				route = "unmatched"
			}
			s.d.Metrics.RequestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(sw.status)).Inc()
			s.d.Metrics.RequestDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
			attrs := []any{"event", "request", "method", r.Method, "route", route, "status", sw.status, "duration_ms", time.Since(start).Milliseconds(), "client", clientKind(r)}
			if info.user != nil {
				attrs = append(attrs, "user_id", info.user.ID.String())
			}
			log.Info("request", attrs...)
		}()
		mux.ServeHTTP(sw, req)
	})
}

type ctxKey int

const (
	userKey ctxKey = iota + 1
	reqInfoKey
)

// reqInfo carries what the handlers learn about a request back to the
// request log line in wrap.
type reqInfo struct{ user *store.User }

// clientKind names which of our clients sent a request, for the request
// log: the CLI identifies itself, a browser is the dashboard. The
// User-Agent itself is never logged (docs/ops/OBSERVABILITY.md).
func clientKind(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	switch {
	case strings.HasPrefix(ua, "repose-cli"):
		return "cli"
	case strings.HasPrefix(ua, "Mozilla/"):
		return "dashboard"
	default:
		return "other"
	}
}

func userFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(userKey).(*store.User)
	return u
}

// authed wraps a user route: bearer token, first-sign-in provisioning,
// suspension, and the general rate limit. queryToken allows
// ?access_token= (the SSE log route only, I-7).
func (s *Server) authed(h handler, queryToken bool) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		token := ""
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(ah, "Bearer "))
		}
		if token == "" && queryToken {
			token = r.URL.Query().Get("access_token")
		}
		if token == "" {
			return errf("unauthenticated", "bearer token required")
		}
		claims, err := s.d.Verifier.Verify(r.Context(), token)
		if err != nil {
			if errors.Is(err, auth.ErrIdentityProviderUnavailable) {
				return errf("unauthenticated", "identity provider unavailable")
			}
			return errf("unauthenticated", "invalid token")
		}
		u, err := s.d.Users.EnsureUser(r.Context(), claims.Sub)
		if err != nil {
			if errors.Is(err, auth.ErrProvisionFailed) {
				return errf("internal", "identity provider unavailable")
			}
			return err
		}
		// A suspended account may only read itself and its billing, and
		// reach the portal to pay (api.md "Usage and billing"); every other
		// route answers the gate's 402 suspended, whose message says how to
		// lift it.
		if u.SuspendedAt != nil && r.Pattern != "GET /v1/me" && r.Pattern != "GET /v1/billing" && r.Pattern != "POST /v1/billing/portal" {
			ref := s.d.Gate.Suspended()
			return withDetail(errf("payment_required", "%s", ref.Message), ref.Detail)
		}
		// Reads have their own, larger bucket: two CLI polls a second
		// through a long build (I-154) used the whole 60/min general budget
		// that writes and the user's dashboard share, and the command
		// ended "rate_limited: too many requests" (I-187).
		bucket := s.general
		if r.Method == http.MethodGet {
			bucket = s.reads
		}
		if ok, retry := bucket.Allow(u.ID.String()); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
			return errf("rate_limited", "too many requests")
		}
		if info, ok := r.Context().Value(reqInfoKey).(*reqInfo); ok {
			info.user = u
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = obs.WithLogger(ctx, obs.Logger(ctx, s.d.Log).With("user_id", u.ID.String()))
		return h(w, r.WithContext(ctx))
	}
}

func (s *Server) limited(l *ratelimit.Limiter, h handler) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		u := userFrom(r.Context())
		if ok, retry := l.Allow(u.ID.String()); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
			return errf("rate_limited", "too many requests")
		}
		return h(w, r)
	}
}

// SweepLimiters drops idle rate-limit buckets.
func (s *Server) SweepLimiters() {
	s.general.Sweep(10 * time.Minute)
	s.reads.Sweep(10 * time.Minute)
	s.certs.Sweep(10 * time.Minute)
	s.cfg.Sweep(10 * time.Minute)
	s.replies.Sweep(10 * time.Minute)
}

// --- health -------------------------------------------------------------

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.d.Pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "db unreachable"})
		return
	}
	if s.d.Migrations != nil {
		pending, err := s.d.Migrations(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "migration state unknown"})
			return
		}
		if pending > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "migrations pending", "pending": pending})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "starting"})
		return
	}
	s.healthz(w, r)
}

// --- gateway sessions -------------------------------------------------------

type sessionTracker struct {
	pool *db.Pool
}

func newSessionTracker(pool *db.Pool) *sessionTracker { return &sessionTracker{pool: pool} }

// update records one relay's open or close. A row is one relay
// (project, certificate serial, the gateway's session id), so two
// connections under one certificate are two sessions and closing one
// leaves the other (DECISIONS I-176). A gateway older than I-176 sends no
// session id; its reports share the id "" and count one per certificate,
// as before.
func (t *sessionTracker) update(ctx context.Context, project uuid.UUID, serial int64, sessionID string, opened bool) error {
	if opened {
		_, err := t.pool.Exec(ctx, "insert into gateway_sessions (project_id, cert_serial, session_id) values ($1, $2, $3) on conflict (project_id, cert_serial, session_id) do update set opened_at = now()", project, serial, sessionID)
		return err
	}
	_, err := t.pool.Exec(ctx, "delete from gateway_sessions where project_id = $1 and cert_serial = $2 and session_id = $3", project, serial, sessionID)
	return err
}

// Count returns the open gateway sessions of a project reported in the
// last day (a gateway that died never sends its closes).
func (t *sessionTracker) Count(ctx context.Context, project uuid.UUID) int {
	var n int
	if err := t.pool.QueryRow(ctx, "select count(*) from gateway_sessions where project_id = $1 and opened_at > now() - interval '1 day'", project).Scan(&n); err != nil {
		return 0
	}
	return n
}
