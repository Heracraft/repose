package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/ca/testca"
)

// Options configures New. The zero value is a usable fake.
type Options struct {
	// Users maps bearer tokens to users. Nil accepts any non-empty token
	// as the canned user (CannedUser); non-nil rejects every other token.
	Users map[string]User
	// Billing starts the fake with billing on and the account active on
	// Solo (BillingActive) instead of off (BillingOff, 503
	// billing_disabled and an exempt account). SetBilling changes it later.
	Billing bool
	// RateLimit enforces the api's per-user limits with 429: 60 writes and
	// 600 GETs per minute per token (I-187).
	RateLimit bool
	// Now replaces the clock; nil means time.Now in UTC.
	Now func() time.Time
	// CA, when set, backs /certs, /internal/ca and /internal/gateway-certs
	// with real certificates from the test CA (docs/interfaces/
	// ssh-gateway.md "Test CA"), so the gateway relay can be tested end
	// to end. Nil keeps the canned placeholder strings.
	CA *testca.CA
	// CreateDelay makes POST /projects answer state "creating" (and read
	// back as "building" while the op runs, as the engine does) with an
	// op_id whose op finishes (and the guest runs) only after the delay,
	// the way the real engine does; a start meanwhile is a conflict. Zero
	// keeps the instant create.
	CreateDelay time.Duration
	// CreateHold holds POST /projects for the duration before it makes
	// anything; a client that gives up meanwhile creates nothing.
	// CreateReplyDelay makes the project at once and holds only the
	// answer. Both stand in for a create request cut short by Ctrl-C
	// (DECISIONS I-575); other requests are served meanwhile.
	CreateHold       time.Duration
	CreateReplyDelay time.Duration
	// CLILatest, when set, is sent as every answer's X-Repose-CLI-Latest
	// header, the newest CLI release (DECISIONS I-626).
	CLILatest string
	// StartDelay makes POST /projects/:id/start on a stopped project leave
	// the project "starting" and its op "running" (phase start_guest) for
	// the delay, then running and done. Zero keeps the instant start.
	StartDelay time.Duration
	// DestroyDelay makes DELETE /projects/:id leave the project
	// "destroying" and its op "running" for the delay, then destroyed and
	// done, the way the real destroy op does. Zero keeps the instant
	// destroy.
	DestroyDelay time.Duration
	// StopDelay makes POST /projects/:id/stop on a running project leave
	// it "stopping" and its op "running" for the delay, then stopped and
	// done, as the real stop op does while it snapshots and shuts down.
	// Zero keeps the instant stop.
	StopDelay time.Duration
	// StopPickup, with StopDelay, keeps the project "running" for this
	// long after the stop is accepted: the real api reads "stopping" only
	// once its engine has picked the op up.
	StopPickup time.Duration
	// ForkStepDelay makes each fork copy's restore op run its steps the
	// way the engine does: phase restore (project "restoring") for the
	// delay, then, when the copy starts, phase start_guest (project
	// "starting") for the delay times the copy's number, then done. Zero
	// keeps the instant fork.
	ForkStepDelay time.Duration
	// NoLongPoll makes GET /projects/:id/ops/:op_id ignore ?wait and
	// answer without version, phase or project_state, as the api before
	// I-236 did.
	NoLongPoll bool
	// NoProjectOpID makes GET /projects/:id leave out op_id, as the api
	// before I-722 did.
	NoProjectOpID bool
}

// OpWaitMax caps ?wait on the op read, as the api does (I-236).
const OpWaitMax = 20 * time.Second

// LongPollHeader marks an op read whose ?wait was honoured (I-236).
const LongPollHeader = "Repose-Long-Poll"

// CannedUser is the user every token maps to when Options.Users is nil.
var CannedUser = User{
	ID:          "00000000-0000-7000-8000-000000000001",
	Handle:      "heracraft",
	Email:       "dev@example.com",
	GitHubLogin: "heracraft",
}

const (
	gatewayHost = "ssh.repose.herakraft.co"
	hostCAPub   = "ssh-ed25519 AAAA...fakeca"
	userCAPub   = "ssh-ed25519 AAAA...fakeuserca"
	baseVersion = "2026.09.15"
	hostID      = "host-01"
	// FakeHostName is the name GET /projects/:id/route reports for hostID.
	FakeHostName = "fake-host-01"
	logPattern   = "GET /v1/projects/{id}/ops/{op_id}/log"
	opPattern    = "GET /v1/projects/{id}/ops/{op_id}"
	rateWindow   = time.Minute
	rateBurst    = 60
)

// Fake is the running fake api.
type Fake struct {
	Server *httptest.Server

	// startOutcome is how the next start of a project ends, when set:
	// the op fails with an error, or ends done with a warning the way a
	// start whose new system did not boot does (DECISIONS I-590).
	startOutcome map[string]startOutcome
	// stopOutcome is how the next stop of a project ends, when set.
	stopOutcome map[string]stopOutcome
	// console is each project's console lines from failed boots, what
	// GET /logs?kind=console serves (I-592).
	console map[string][]string

	opts   Options
	mux    *http.ServeMux
	routes []string

	mu       sync.Mutex
	seq      int64
	reqSeq   int64
	ipSeq    int
	serial   uint64
	tokens   map[string]string // token -> user id
	users    map[string]*userRec
	projects map[string]*project
	ops      map[string]*op
	forks    map[string][]forkRec // by user id and request_id (I-254)
	// forkNoClone makes a fork without snapshot_id answer 409
	// snapshot_needed, as the api does when the source's host cannot hold
	// the copies (I-701); oldFork answers it, and a snapshot's reason, as
	// an api before I-701 and I-703 did.
	forkNoClone, oldFork bool
	certs                map[uint64]*cert
	revoked              []revocation
	failOnce             []failRule
	failAlways           map[string]string
	hits                 map[string][]time.Time
	hosts                []Host // nil means the one canned host
	gatewayCerts         int    // POST /internal/gateway-certs calls, for cache tests
	sessions             []SessionReport
	questions            []*Question // newest last (DECISIONS I-245)
	// bill is the billing mode, plan, seats and waitlist (billing.go); the
	// one account the fake has is the one they describe.
	bill billingState
	// herdrMinBase is the first base with herdr; empty refuses herdr
	// (SetHerdrMinBase, I-502).
	herdrMinBase string
	// onGet runs after each GET /v1/projects/{id} of a project is
	// answered (OnGet).
	onGet map[string]func(*Project) bool
}

type failRule struct {
	key  string
	code string
}

// New starts the fake. Close it when done.
func New(opts Options) *Fake {
	f := &Fake{
		opts:       opts,
		mux:        http.NewServeMux(),
		tokens:     map[string]string{},
		users:      map[string]*userRec{},
		projects:   map[string]*project{},
		ops:        map[string]*op{},
		forks:      map[string][]forkRec{},
		certs:      map[uint64]*cert{},
		failAlways: map[string]string{},
		hits:       map[string][]time.Time{},
	}
	f.addUser(CannedUser)
	for tok, u := range opts.Users {
		f.addUser(u)
		f.tokens[tok] = u.ID
	}
	f.initBilling()
	if opts.Billing {
		f.setMode(BillingActive)
	}
	f.register()
	f.Server = httptest.NewServer(f)
	return f
}

// URL is the server's base URL without the /v1 prefix.
func (f *Fake) URL() string { return f.Server.URL }

// Close stops the server.
func (f *Fake) Close() { f.Server.Close() }

// Routes lists the registered "METHOD /v1/path" patterns.
func (f *Fake) Routes() []string {
	out := make([]string, len(f.routes))
	copy(out, f.routes)
	return out
}

func (f *Fake) addUser(u User) {
	if _, ok := f.users[u.ID]; ok {
		return
	}
	f.users[u.ID] = &userRec{User: u, TZ: "UTC", CreatedAt: f.now(), NotifyEmail: true}
}

func (f *Fake) now() time.Time {
	if f.opts.Now != nil {
		return f.opts.Now().UTC()
	}
	return time.Now().UTC().Truncate(time.Second)
}

// AddEventAs records an event with an agent and its tmux window, for the
// admin switch's mixed seeding of a project's events (I-662). AddEvent
// is the same with neither.
func (f *Fake) AddEventAs(projectID string, ts time.Time, kind, agent, window, summary string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.events = append(p.events, &Event{ID: f.nextID(), TS: ts, Kind: kind, Agent: agent, Window: window, Summary: summary})
	}
}

// BackdateCreation moves a project's creation to ts when it is newer: its
// created_at, a started_at equal to it, and the events written at create
// (project.created and the guest_state_changed beside it), so events
// seeded into the past still come after the project's first one, as on a
// real project.
func (f *Fake) BackdateCreation(projectID string, ts time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[projectID]
	if !ok || !p.CreatedAt.After(ts) {
		return
	}
	at := p.CreatedAt
	ts = ts.UTC()
	for _, ev := range p.events {
		if ev.TS.Equal(at) && (ev.Kind == "project.created" || ev.Kind == "guest_state_changed") {
			ev.TS = ts
		}
	}
	if p.StartedAt != nil && p.StartedAt.Equal(at) {
		p.StartedAt = &ts
	}
	p.CreatedAt = ts
}

// SetSessions sets the SSH sessions and tmux clients a running project's
// signals report.
func (f *Fake) SetSessions(projectID string, ssh, tmux int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		if p.Signals == nil {
			p.Signals = &Signals{GuestdOK: true}
		}
		p.Signals.SSHSessions, p.Signals.TmuxClients = ssh, tmux
	}
}

// nextID returns the next deterministic UUIDv7-looking id. Callers hold mu.
func (f *Fake) nextID() string {
	f.seq++
	return fmt.Sprintf("01900000-0000-7000-8000-%012d", f.seq)
}

// The error switch.

// FailNext makes the next request matching method and path (an exact
// request path such as /v1/projects/<id>, or a pattern such as
// /projects/:id or /v1/projects/{id}) answer the given error code once.
func (f *Fake) FailNext(method, path, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOnce = append(f.failOnce, failRule{key: normalizeKey(method, path), code: code})
}

// Fail makes every request matching pattern ("METHOD path", the path in
// any of the forms FailNext accepts) answer the given error code until
// Unfail.
func (f *Fake) Fail(pattern, code string) {
	method, path, _ := strings.Cut(strings.TrimSpace(pattern), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAlways[normalizeKey(method, path)] = code
}

// Unfail removes a Fail rule.
func (f *Fake) Unfail(pattern string) {
	method, path, _ := strings.Cut(strings.TrimSpace(pattern), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failAlways, normalizeKey(method, path))
}

// normalizeKey turns any accepted method/path spelling into the
// "METHOD /v1/path" form the mux reports, with :x params as {x}.
func normalizeKey(method, path string) string {
	path = strings.TrimSpace(path)
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = "{" + p[1:] + "}"
		}
	}
	path = strings.Join(parts, "/")
	if path != "/v1" && !strings.HasPrefix(path, "/v1/") {
		path = "/v1" + path
	}
	return strings.ToUpper(strings.TrimSpace(method)) + " " + path
}

// forced returns the error code a switch has queued for this request, or
// "". Callers hold mu.
func (f *Fake) forced(r *http.Request, pattern string) string {
	keys := []string{normalizeKey(r.Method, r.URL.Path)}
	if pattern != "" {
		keys = append(keys, pattern)
	}
	for i, rule := range f.failOnce {
		for _, k := range keys {
			if rule.key == k {
				f.failOnce = append(f.failOnce[:i], f.failOnce[i+1:]...)
				return rule.code
			}
		}
	}
	for _, k := range keys {
		if code, ok := f.failAlways[k]; ok {
			return code
		}
	}
	return ""
}

// Error envelope.

type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
}

type errEnvelope struct {
	Error apiError `json:"error"`
}

var statusOf = map[string]int{
	"unauthenticated":  http.StatusUnauthorized,
	"forbidden":        http.StatusForbidden,
	"not_found":        http.StatusNotFound,
	"invalid":          http.StatusBadRequest,
	"conflict":         http.StatusConflict,
	"payment_required": http.StatusPaymentRequired,
	"capacity":         http.StatusServiceUnavailable,
	"waitlisted":       http.StatusServiceUnavailable,
	"rate_limited":     http.StatusTooManyRequests,
	"internal":         http.StatusInternalServerError,
	"billing_disabled": http.StatusServiceUnavailable,
}

func errf(code, format string, args ...any) *apiError {
	return &apiError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func invalid(format string, args ...any) *apiError { return errf("invalid", format, args...) }
func notFound(what string) *apiError               { return errf("not_found", "%s not found", what) }

func (e *apiError) withDetail(detail map[string]any) *apiError {
	e.Detail = detail
	return e
}

func writeError(w http.ResponseWriter, e *apiError) {
	status, ok := statusOf[e.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, errEnvelope{Error: *e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":{"code":"internal","message":"encoding response"}}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil {
		return // the client went away; there is nobody left to tell
	}
}

// decodeBody decodes a JSON body into v, rejecting unknown fields. An
// empty body is accepted only when optional is set.
func decodeBody(r *http.Request, v any, optional bool) *apiError {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) && optional {
			return nil
		}
		return invalid("body: %v", err)
	}
	return nil
}

// Request pipeline.

type handler func(w http.ResponseWriter, r *http.Request) *apiError

type ctxKey int

const ctxUser ctxKey = 1

func userFrom(r *http.Request) *userRec {
	u, _ := r.Context().Value(ctxUser).(*userRec) // internal routes carry no user; handlers that need one are never internal
	return u
}

func (f *Fake) handle(pattern string, h handler) {
	f.routes = append(f.routes, pattern)
	f.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if e := h(w, r); e != nil {
			writeError(w, e)
		}
	})
}

// ServeHTTP stamps X-Request-Id, applies the error switch, authenticates
// user routes, rate-limits when asked, and then dispatches. Handlers other
// than the SSE log run under mu so they never lock themselves.
//
// CORS headers on every response, and OPTIONS answered without touching the
// mux (which has no registered OPTIONS handlers): the dashboard fetches
// this fake cross-origin in the Playwright suite exactly like it fetches
// the real api cross-origin in production (I-79).
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Expose-Headers", "Repose-Event-Count, X-Request-Id")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	isCreate := r.Method == http.MethodPost && r.URL.Path == "/v1/projects"
	if isCreate && f.opts.CreateHold > 0 {
		select {
		case <-time.After(f.opts.CreateHold):
		case <-r.Context().Done():
			return
		}
	}
	f.mu.Lock()
	f.reqSeq++
	w.Header().Set("X-Request-Id", fmt.Sprintf("req-%06d", f.reqSeq))
	if f.opts.CLILatest != "" {
		w.Header().Set("X-Repose-CLI-Latest", f.opts.CLILatest)
	}
	_, pattern := f.mux.Handler(r)
	if pattern == "" {
		f.mu.Unlock()
		writeError(w, notFound("route"))
		return
	}
	if code := f.forced(r, pattern); code != "" {
		f.mu.Unlock()
		writeError(w, errf(code, "forced by the test's error switch"))
		return
	}
	// The unsubscribe and reply links carry their own signed token; the
	// public seats count (I-290) has none.
	if !strings.HasPrefix(r.URL.Path, "/v1/internal/") && !strings.HasPrefix(r.URL.Path, "/v1/public/") && r.URL.Path != "/v1/notify/unsubscribe" && r.URL.Path != "/v1/questions/reply" {
		u, tok, ok := f.authenticate(r, pattern)
		if !ok {
			f.mu.Unlock()
			writeError(w, errf("unauthenticated", "missing or unknown bearer token"))
			return
		}
		if retry := f.rateLimited(tok, r.Method == http.MethodGet); retry > 0 {
			f.mu.Unlock()
			w.Header().Set("Retry-After", fmt.Sprint(retry))
			writeError(w, errf("rate_limited", "too many requests"))
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxUser, u))
	}
	if pattern == logPattern || (pattern == opPattern && r.URL.Query().Get("wait") != "") {
		// The log streams and a long-poll of an op waits: both take mu
		// themselves, for their reads only.
		f.mu.Unlock()
		f.mux.ServeHTTP(w, r)
		return
	}
	if isCreate && f.opts.CreateReplyDelay > 0 {
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, r)
		f.mu.Unlock()
		select {
		case <-time.After(f.opts.CreateReplyDelay):
		case <-r.Context().Done():
			return
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	defer f.mu.Unlock()
	f.mux.ServeHTTP(w, r)
}

func (f *Fake) authenticate(r *http.Request, pattern string) (*userRec, string, bool) {
	tok := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if tok == "" && pattern == logPattern {
		tok = r.URL.Query().Get("access_token")
	}
	if tok == "" {
		return nil, "", false
	}
	id, ok := f.tokens[tok]
	if !ok {
		if f.opts.Users != nil {
			return nil, "", false
		}
		id = CannedUser.ID
		f.tokens[tok] = id
	}
	u, ok := f.users[id]
	if !ok {
		return nil, "", false
	}
	return u, tok, true
}

// rateLimited records a hit and returns the Retry-After seconds when the
// token is over budget, else 0.
func (f *Fake) rateLimited(tok string, read bool) int {
	if !f.opts.RateLimit {
		return 0
	}
	key, burst := tok, rateBurst
	if read {
		key, burst = tok+" reads", 10*rateBurst
	}
	now := f.now()
	kept := f.hits[key][:0]
	for _, t := range f.hits[key] {
		if now.Sub(t) < rateWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= burst {
		f.hits[key] = kept
		wait := rateWindow - now.Sub(kept[0])
		return int(math.Max(1, math.Ceil(wait.Seconds())))
	}
	f.hits[key] = append(kept, now)
	return 0
}

// register wires every route of docs/interfaces/api.md.
func (f *Fake) register() {
	// Users.
	f.handle("GET /v1/me", f.getMe)
	f.handle("PATCH /v1/me", f.patchMe)
	f.handle("DELETE /v1/me", f.deleteMe)
	f.handle("POST /v1/me/notify-test", f.notifyTest)
	f.handle("GET /v1/me/config", f.getPersonal)
	f.handle("PUT /v1/me/config", f.putPersonal)
	f.handle("GET /v1/me/config/revisions", f.listPersonal)
	f.handle("GET /v1/notify/unsubscribe", f.notifyUnsubscribe)
	f.handle("POST /v1/notify/unsubscribe", f.notifyUnsubscribe)
	// Projects.
	f.handle("GET /v1/projects", f.listProjects)
	f.handle("POST /v1/projects", f.createProject)
	f.handle("GET /v1/projects/destroyed", f.listDestroyed)
	f.handle("POST /v1/projects/restore", f.restoreByName)
	f.handle("GET /v1/projects/{id}", f.getProject)
	f.handle("PATCH /v1/projects/{id}", f.patchProject)
	f.handle("DELETE /v1/projects/{id}", f.destroyProject)
	f.handle("POST /v1/projects/{id}/start", f.startProject)
	f.handle("POST /v1/projects/{id}/stop", f.stopProject)
	f.handle(opPattern, f.getOp)
	f.handle(logPattern, f.opLog)
	f.handle("POST /v1/projects/{id}/resize", f.resizeProject)
	f.handle("GET /v1/projects/{id}/route", f.projectRoute)
	f.handle("GET /v1/projects/{id}/samples", f.projectSamples)
	// Config.
	f.handle("GET /v1/projects/{id}/config", f.getConfig)
	f.handle("PUT /v1/projects/{id}/config", f.putConfig)
	f.handle("GET /v1/projects/{id}/config/revisions", f.listRevisions)
	f.handle("POST /v1/projects/{id}/config/revisions/{rev}/apply", f.applyRevision)
	f.handle("GET /v1/catalog", f.getCatalog)
	// Certificates.
	f.handle("POST /v1/certs", f.issueCert)
	f.handle("POST /v1/certs/revoke", f.revokeCerts)
	// Secrets.
	f.handle("GET /v1/projects/{id}/secrets", f.listSecrets)
	f.handle("PUT /v1/projects/{id}/secrets/{name}", f.putSecret)
	f.handle("DELETE /v1/projects/{id}/secrets/{name}", f.deleteSecret)
	f.handle("GET /v1/me/secrets", f.listAccountSecrets)
	f.handle("PUT /v1/me/secrets/{name}", f.putAccountSecret)
	f.handle("DELETE /v1/me/secrets/{name}", f.deleteAccountSecret)
	// Snapshots.
	f.handle("GET /v1/projects/{id}/snapshots", f.listSnapshots)
	f.handle("POST /v1/projects/{id}/snapshots", f.createSnapshot)
	f.handle("POST /v1/projects/{id}/snapshots/{sid}/restore", f.restoreSnapshot)
	f.handle("POST /v1/projects/{id}/fork", f.forkProject)
	// Events and logs.
	f.handle("GET /v1/projects/{id}/events", f.listEvents)
	f.handle("GET /v1/projects/{id}/logs", f.projectLogs)
	// Questions (DECISIONS I-245).
	f.handle("GET /v1/questions", f.listQuestions)
	f.handle("GET /v1/projects/{id}/questions", f.listProjectQuestions)
	f.handle("POST /v1/projects/{id}/questions/{qid}/answer", f.answerQuestion)
	f.handle("POST /v1/projects/{id}/questions/{qid}/cancel", f.cancelQuestion)
	f.handle("GET /v1/questions/reply", f.replyQuestion)
	f.handle("POST /v1/questions/reply", f.replyQuestion)
	// Usage and billing.
	f.handle("GET /v1/usage", f.getUsage)
	f.handle("GET /v1/billing", f.getBilling)
	f.handle("POST /v1/billing/checkout", f.billingCheckout)
	f.handle("POST /v1/billing/waitlist", f.billingWaitlist)
	f.handle("POST /v1/billing/plan", f.billingPlan)
	f.handle("POST /v1/billing/cancel", f.billingCancel)
	f.handle("POST /v1/billing/resume", f.billingResume)
	f.handle("POST /v1/billing/portal", f.billingPortal)
	f.handle("GET /v1/billing/invoices", f.billingInvoices)
	f.handle("POST /v1/billing/webhook", f.billingWebhook)
	f.handle("GET /v1/public/seats", f.publicSeats)
	// Internal (gateway).
	f.handle("GET /v1/internal/route", f.internalRoute)
	f.handle("GET /v1/internal/revoked", f.internalRevoked)
	f.handle("GET /v1/internal/ca", f.internalCA)
	f.handle("POST /v1/internal/sessions", f.internalSessions)
	f.handle("GET /v1/internal/hosts", f.internalHosts)
	f.handle("POST /v1/internal/gateway-certs", f.internalGatewayCerts)
	f.handle("POST /v1/internal/events", f.internalEvents)
}

type stopOutcome struct {
	failMsg, snapshotFailMsg string
}

// SetStopFailure makes the next stop op of the project end in error with
// message, leaving the project in error with the reason the api writes
// (DECISIONS I-710).
func (f *Fake) SetStopFailure(projectID, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopOutcome == nil {
		f.stopOutcome = map[string]stopOutcome{}
	}
	f.stopOutcome[projectID] = stopOutcome{failMsg: message}
}

// SetStopSnapshotFailure makes the next stop of the project take no
// snapshot and leave it stopped with last_error "snapshot_failed:
// message", as a stop whose upload failed does (I-158).
func (f *Fake) SetStopSnapshotFailure(projectID, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopOutcome == nil {
		f.stopOutcome = map[string]stopOutcome{}
	}
	f.stopOutcome[projectID] = stopOutcome{snapshotFailMsg: message}
}

// endStop stops p for op o, applying a stop outcome set for it. Under
// f.mu.
func (f *Fake) endStop(p *project, o *op, snapshot bool) {
	out, ok := f.stopOutcome[p.ID]
	delete(f.stopOutcome, p.ID)
	switch {
	case ok && out.failMsg != "":
		o.State, o.Error = "error", out.failMsg
		p.State = "error"
		le := "stop_failed: stopping " + p.Slug + " failed: " + out.failMsg + ". `repose stop " + p.Slug + "` tries again"
		p.LastError = &le
		return
	case ok && out.snapshotFailMsg != "":
		f.stop(p, false)
		le := "snapshot_failed: " + out.snapshotFailMsg
		p.LastError = &le
	default:
		// The api's stop op carries the snapshot it recorded, as a
		// snapshot op does.
		if s := f.stop(p, snapshot); s != nil {
			o.Result = map[string]any{"snapshot_id": s.ID}
		}
	}
	o.State = "done"
}

// busyStopping is the api's 409 for an op sent while a stop op is open on
// the project: Engine.Enqueue's ErrOpInProgress.
func busyStopping(p *project) *apiError {
	if p.State != "stopping" {
		return nil
	}
	return errf("conflict", "An operation is in progress")
}

type startOutcome struct {
	failCode, failMsg string
	warnCode, warnMsg string
}

// SetForkClone sets what a fork without snapshot_id gets: clones (the
// default), 409 snapshot_needed (noClone), or an api before I-701's
// "snapshot_id is required" (old, which also records every snapshot a
// user asks for as manual, as an api before I-703 did).
func (f *Fake) SetForkClone(noClone, old bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forkNoClone, f.oldFork = noClone, old
}

// SetStartFailure makes the next start op of the project end in error
// with code and message, leaving the project in error.
func (f *Fake) SetStartFailure(projectID, code, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startOutcome == nil {
		f.startOutcome = map[string]startOutcome{}
	}
	f.startOutcome[projectID] = startOutcome{failCode: code, failMsg: message}
}

// SetStartWarning makes the next start op of the project end done with
// result.warning {code, message} and last_error "code: message", the
// project running (DECISIONS I-590).
func (f *Fake) SetStartWarning(projectID, code, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startOutcome == nil {
		f.startOutcome = map[string]startOutcome{}
	}
	f.startOutcome[projectID] = startOutcome{warnCode: code, warnMsg: message}
}

// endStart applies a start outcome set for p to its op. Under f.mu.
func (f *Fake) endStart(p *project, o *op) {
	out, ok := f.startOutcome[p.ID]
	if !ok {
		return
	}
	delete(f.startOutcome, p.ID)
	switch {
	case out.failCode != "":
		o.State = "error"
		o.Error = out.failMsg
		p.State = "error"
		le := out.failCode + ": " + out.failMsg
		p.LastError = &le
	case out.warnCode != "":
		o.Result = map[string]any{"warning": map[string]any{"code": out.warnCode, "message": out.warnMsg}}
		le := out.warnCode + ": " + out.warnMsg
		p.LastError = &le
	}
}

// SetConsole sets the console lines GET /projects/:id/logs?kind=console
// serves for the project: what hostd kept of its boots that failed.
func (f *Fake) SetConsole(projectID string, lines ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.console == nil {
		f.console = map[string][]string{}
	}
	f.console[projectID] = lines
}
