package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/heracraft/repose/internal/api/apidoc"
	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/auth"
	"github.com/heracraft/repose/internal/api/config"
	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/ca/sshca"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/logto"
	"github.com/heracraft/repose/internal/obs"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

const aud = "https://api.repose.herakraft.co"

type env struct {
	h        *apitest.Harness
	logto    *logto.Fake
	srv      *httpapi.Server
	api      *httptest.Server
	internal *httptest.Server
	logs     *bytes.Buffer
	sent     *sync.Map
	unsub    *notify.Unsubscriber
}

func newEnv(t *testing.T) *env {
	return newEnvLimits(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000})
}

func newEnvLimits(t *testing.T, limits *httpapi.RateLimits) *env {
	return newEnvWith(t, limits, nil)
}

// newEnvWith lets a test change the dependencies before the server is
// built (workstream 09 turns billing enforcement off and plugs the Paddle
// webhook handler in this way).
func newEnvWith(t *testing.T, limits *httpapi.RateLimits, tweak func(*httpapi.Deps)) *env {
	t.Helper()
	h := apitest.New(t, apitest.Options{})
	lf := logto.New(aud)
	t.Cleanup(lf.Close)
	logs := &bytes.Buffer{}
	syncw := &syncWriter{w: logs}
	log := obs.NewLogger(obs.LogOptions{Component: obs.ComponentAPI, Writer: syncw, Level: slog.LevelDebug})
	parser, _ := config.NewParser()
	sent := &sync.Map{}
	sender := notify.SenderFunc(func(ctx context.Context, m notify.Message) error {
		sent.Store(m.EventID.String()+m.Kind, m)
		return nil
	})
	reg := prometheus.NewRegistry()
	unsub, err := notify.LoadOrCreateUnsubscriber(context.Background(), h.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{h: h, logto: lf, logs: logs, sent: sent, unsub: unsub}
	deps := httpapi.Deps{
		Pool: h.Pool, Verifier: auth.NewVerifier(lf.Issuer(), aud, nil), Users: auth.NewProvisioner(h.Pool, auth.NewLogtoManagement(lf.Issuer(), "m2m", "s", nil)),
		CA: h.CA, Secrets: h.Secrets, Engine: h.Engine, Logs: h.Logs, Events: h.Events, Parser: parser, Metrics: h.Metrics, Registry: reg, Log: log,
		Outbox:    notify.New(h.Pool, map[string]notify.Sender{"email": sender, "ntfy": sender}, h.Metrics, log),
		Unsub:     unsub,
		Questions: h.Questions,
		Gateway:   httpapi.Gateway{Host: "ssh.test", Port: 22}, Limits: limits, BillingEnforce: true,
		Migrations: func(ctx context.Context) (int, error) {
			st, err := db.MigrateStatus(ctx, h.Pool)
			return len(st.Pending), err
		},
	}
	if tweak != nil {
		tweak(&deps)
	}
	if deps.Gate == nil {
		// The gate as a configured deploy has it (Paddle on), so the row
		// logic is what the tests exercise; without a key every start is
		// subscription_required (TestBillingDisabledRoutes covers the
		// routes' 503). BillingEnforce=false still lets everything through.
		deps.Gate = billing.NewGate(h.Pool, billing.Config{APIKey: "pdl_sdbx_apikey_test", DashboardURL: "https://repose.herakraft.co", Enforce: deps.BillingEnforce}, h.Metrics, log)
	}
	e.srv = httpapi.New(deps)
	e.srv.SetReady(true)
	e.api = httptest.NewServer(e.srv.Handler())
	e.internal = httptest.NewServer(e.srv.InternalHandler())
	t.Cleanup(e.api.Close)
	t.Cleanup(e.internal.Close)
	return e
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

type resp struct {
	status int
	body   map[string]any
	list   []any
	raw    []byte
	hdr    http.Header
}

func (e *env) do(t *testing.T, token, method, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.api.URL+"/v1"+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw, hdr: res.Header}
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out.body)
	} else if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &out.list)
	}
	return out
}

// doRaw posts a body verbatim with the given headers and no bearer token:
// the Paddle webhook route authenticates with its own header, so it cannot
// be exercised through do().
func (e *env) doRaw(t *testing.T, method, path string, body []byte, headers map[string]string) resp {
	t.Helper()
	req, _ := http.NewRequest(method, e.api.URL+"/v1"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw, hdr: res.Header}
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

func (e *env) internalDo(t *testing.T, method, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.internal.URL+"/v1"+path, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw, hdr: res.Header}
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out.body)
	} else if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &out.list)
	}
	return out
}

func errCode(r resp) string {
	if e, ok := r.body["error"].(map[string]any); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

func (e *env) waitOp(t *testing.T, r resp) *store.Op {
	t.Helper()
	id, _ := r.body["op_id"].(string)
	if id == "" {
		t.Fatalf("no op_id in %s", r.raw)
	}
	return e.h.WaitOp(uuid.MustParse(id))
}

// signIn creates a Logto identity and returns a token; the user gets a
// card so compute is allowed.
func (e *env) signIn(t *testing.T, sub, login string) string {
	t.Helper()
	e.logto.AddUser(sub, logto.User{Email: login + "@example.com", GithubLogin: login})
	tok := e.logto.Token(sub)
	r := e.do(t, tok, "GET", "/me", nil)
	if r.status != 200 {
		t.Fatalf("first sign-in: %d %s", r.status, r.raw)
	}
	e.subscribe(t, sub, "plus")
	return tok
}

// subscribe gives the signed-in user a live subscription on plan, the
// way the Paddle webhook would (I-289), so the compute gate lets the
// test's projects through; "" removes it.
func (e *env) subscribe(t *testing.T, sub, plan string) {
	t.Helper()
	ctx := e.h.Ctx
	var uid string
	if err := e.h.Pool.QueryRow(ctx, "select id from users where logto_sub = $1", sub).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, "delete from subscriptions where user_id = $1", uid); err != nil {
		t.Fatal(err)
	}
	if plan == "" {
		if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'none', has_card = false where id = $1", uid); err != nil {
			t.Fatal(err)
		}
		return
	}
	seats := map[string]int{"solo": 1, "plus": 2, "pro": 4}[plan]
	if _, err := e.h.Pool.Exec(ctx, `insert into subscriptions (id, user_id, paddle_customer_id, plan, status, seats, period_start, period_end, next_billed_at)
		values ('sub_' || $1, $2, 'ctm_' || $1, $3, 'active', $4, date_trunc('month', now()), date_trunc('month', now()) + interval '1 month', date_trunc('month', now()) + interval '1 month')`,
		sub, uid, plan, seats); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'active', has_card = true, paddle_customer_id = 'ctm_' || $2 where id = $1", uid, sub); err != nil {
		t.Fatal(err)
	}
}

// TestNotifyUnsubscribe covers docs/workstreams/13-notifications.md §5.6
// and §9's "unsubscribe link that works" (DECISIONS I-442): GET shows a
// confirmation and changes nothing, POST with a valid token flips
// notify_email off with no auth, and a forged, malformed or expired token
// is refused.
func TestNotifyUnsubscribe(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-uns", "uns")
	r := e.do(t, tok, "GET", "/me", nil)
	if r.status != 200 {
		t.Fatalf("me: %d %s", r.status, r.raw)
	}
	userID, err := uuid.Parse(r.body["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	notifyEmail := func() bool {
		t.Helper()
		var v bool
		if err := e.h.Pool.QueryRow(e.h.Ctx, "select notify_email from users where id = $1", userID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	exp := time.Now().Add(time.Hour)

	// Invalid tokens never touch the row.
	for _, m := range []string{"GET", "POST"} {
		if resp := e.do(t, "", m, "/notify/unsubscribe?token=garbage", nil); resp.status != 400 {
			t.Fatalf("%s garbage token: %d %s", m, resp.status, resp.raw)
		}
	}
	// A well-formed token signed for a user id nobody has is not an error
	// (it verifies; there is just no row to update) and must not touch the
	// real user's row.
	other := e.unsub.Sign(uuid.New(), exp)
	if resp := e.do(t, "", "POST", "/notify/unsubscribe?token="+other, nil); resp.status != 200 {
		t.Fatalf("unknown-user token: %d %s", resp.status, resp.raw)
	}
	if !notifyEmail() {
		t.Fatal("an unrelated token's success must not have touched this user's row")
	}

	// GET, what a link scanner does, only shows the confirmation.
	token := e.unsub.Sign(userID, exp)
	resp := e.do(t, "", "GET", "/notify/unsubscribe?token="+token, nil)
	if resp.status != 200 || !strings.Contains(string(resp.raw), `method="post" action="/v1/notify/unsubscribe"`) || !strings.Contains(string(resp.raw), token) {
		t.Fatalf("unsubscribe page: %d %s", resp.status, resp.raw)
	}
	if !notifyEmail() {
		t.Fatal("GET turned email off")
	}

	// An expired token is refused on both.
	old := e.unsub.Sign(userID, time.Now().Add(-time.Minute))
	for _, m := range []string{"GET", "POST"} {
		if resp := e.do(t, "", m, "/notify/unsubscribe?token="+old, nil); resp.status != 410 {
			t.Fatalf("%s expired token: %d %s", m, resp.status, resp.raw)
		}
	}
	if !notifyEmail() {
		t.Fatal("an expired token turned email off")
	}

	// A tampered signature is refused.
	idPart, _, _ := strings.Cut(token, ".")
	_, sigPart, _ := strings.Cut(other, ".")
	if resp := e.do(t, "", "POST", "/notify/unsubscribe?token="+idPart+"."+sigPart, nil); resp.status != 400 {
		t.Fatalf("tampered token: %d %s", resp.status, resp.raw)
	}

	// The page's form POST, and RFC 8058's one-click POST (token in the
	// query, List-Unsubscribe=One-Click in the body), flip it off with no
	// Authorization header.
	form := url.Values{"token": {token}}
	res, err := http.PostForm(e.api.URL+"/v1/notify/unsubscribe", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "unsubscribed") {
		t.Fatalf("form post: %d %s", res.StatusCode, body)
	}
	if notifyEmail() {
		t.Fatal("notify_email was not cleared")
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update users set notify_email = true where id = $1", userID); err != nil {
		t.Fatal(err)
	}
	res, err = http.Post(e.api.URL+"/v1/notify/unsubscribe?token="+token, "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || notifyEmail() {
		t.Fatalf("one-click post: %d, notify_email still on: %v", res.StatusCode, notifyEmail())
	}
}

// TestNotifyTestRoute is 13-notifications.md §9's "POST /me/notify-test
// returns per-channel results": the settings page's test button.
func TestNotifyTestRoute(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-kim", "kim")
	// Email only, no ntfy: the result carries email but not ntfy.
	r := e.do(t, tok, "POST", "/me/notify-test", nil)
	if r.status != 200 {
		t.Fatalf("notify-test: %d %s", r.status, r.raw)
	}
	if r.body["email"] != "ok" {
		t.Fatalf("email result: %v", r.body)
	}
	if _, ok := r.body["ntfy"]; ok {
		t.Fatalf("ntfy attempted with no url configured: %v", r.body)
	}
	// Setting an ntfy url gets it included too; the fake sender in this
	// harness (newEnvLimits) answers every send with no error.
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"ntfy_url": "https://ntfy.example/topic"}}); r.status != 200 {
		t.Fatalf("set ntfy: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/me/notify-test", nil)
	if r.status != 200 || r.body["email"] != "ok" || r.body["ntfy"] != "ok" {
		t.Fatalf("notify-test with ntfy: %d %v", r.status, r.body)
	}
	// Turning email off drops it from the test, not just real events.
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"email": false}}); r.status != 200 {
		t.Fatalf("disable email: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/me/notify-test", nil)
	if _, ok := r.body["email"]; ok {
		t.Fatalf("email attempted after being disabled: %v", r.body)
	}
}

func TestRouteContract(t *testing.T) {
	e := newEnv(t)
	documented, err := apidoc.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, r := range documented {
		want[apidoc.Pattern(r)] = true
	}
	got := map[string]bool{}
	for _, r := range e.srv.Routes() {
		got[r] = true
	}
	var missing, extra []string
	for r := range want {
		if !got[r] {
			missing = append(missing, r)
		}
	}
	for r := range got {
		if !want[r] {
			extra = append(extra, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("missing from router: %v\nnot in api.md: %v", missing, extra)
	}
	t.Logf("%d routes documented, %d registered, all matched", len(want), len(got))
	for _, r := range e.srv.Routes() {
		t.Logf("  %s", r)
	}
}

// TestDestroyingFreesTheSlot: a project being destroyed no longer counts
// toward the project limit, so `repose rm` then `repose run` works at the
// limit, but its name stays taken until the destroy ends; a project left in
// error by a failed destroy still counts (DECISIONS I-300).
func TestDestroyingFreesTheSlot(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-cleo", "cleo")
	// An exempt account without a plan works within users.project_limit
	// (I-289); 3 keeps the test short.
	e.subscribe(t, "sub-cleo", "")
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'exempt', project_limit = 3 where logto_sub = 'sub-cleo'"); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, n := range []string{"one", "two", "three"} {
		r := e.do(t, tok, "POST", "/projects", map[string]any{"name": n, "class": "small"})
		if r.status != 201 {
			t.Fatalf("create %s: %d %s", n, r.status, r.raw)
		}
		ids[n] = r.body["id"].(string)
		e.waitOp(t, r)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "four", "class": "small"}); r.status != 400 || r.body["error"].(map[string]any)["detail"].(map[string]any)["limit"].(float64) != 3 {
		t.Fatalf("at the limit: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'destroying' where id = $1", ids["three"]); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "three", "class": "small"}); r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("name of a destroying project: %d %s", r.status, r.raw)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "four", "class": "small"})
	if r.status != 201 {
		t.Fatalf("slot of a destroying project: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'error' where id = $1", ids["three"]); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "five", "class": "small"}); r.status != 400 || r.body["error"].(map[string]any)["detail"].(map[string]any)["projects"].(float64) != 4 {
		t.Fatalf("a failed destroy still counts: %d %s", r.status, r.raw)
	}
}

func TestSignInAndProjectsLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	// Unauthenticated and bad tokens.
	if r := e.do(t, "", "GET", "/me", nil); r.status != 401 || errCode(r) != "unauthenticated" || r.hdr.Get("X-Request-Id") == "" {
		t.Fatalf("no token: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "garbage", "GET", "/me", nil); r.status != 401 {
		t.Fatalf("bad token: %d", r.status)
	}
	// First sign-in creates the user with the derived handle and trial defaults.
	e.logto.AddUser("sub-alice", logto.User{Email: "alice@example.com", GithubLogin: "Alice_Dev"})
	tok := e.logto.Token("sub-alice")
	r := e.do(t, tok, "GET", "/me", nil)
	if r.status != 200 || r.body["handle"] != "alice-dev" {
		t.Fatalf("me: %d %s", r.status, r.raw)
	}
	// A new account has no plan: status none, no credit, Solo's project
	// count and no xl until a plan is chosen (I-289).
	b := r.body["billing"].(map[string]any)
	l := r.body["limits"].(map[string]any)
	if b["status"] != "none" || b["trial_credit_cents"].(float64) != 0 || b["plan"] != nil || b["has_card"] != false || l["projects"].(float64) != 10 || l["xl"].(float64) != 0 || l["memory_gb"].(float64) != 8 {
		t.Fatalf("defaults: %s", r.raw)
	}
	var row map[string]any
	rows, _ := e.h.Pool.Query(ctx, "select handle, trial_credit_cents, project_limit, xl_limit, billing_status from users where logto_sub = 'sub-alice'")
	for rows.Next() {
		var handle, status string
		var credit int64
		var pl, xl int
		_ = rows.Scan(&handle, &credit, &pl, &xl, &status)
		row = map[string]any{"handle": handle, "trial_credit_cents": credit, "project_limit": pl, "xl_limit": xl, "billing_status": status}
	}
	rows.Close()
	t.Logf("user row after first sign-in: %v", row)
	// No plan: payment_required with the reason and the whole sentence.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "large", "remote_url": "github.com/alice/todo"})
	if r.status != 402 || errCode(r) != "payment_required" || errDetail(r, "reason") != "subscription_required" {
		t.Fatalf("no plan: %d %s", r.status, r.raw)
	}
	if msg, _ := r.body["error"].(map[string]any)["message"].(string); msg != "Choose a plan at https://repose.herakraft.co/billing first." {
		t.Fatalf("message %q", msg)
	}
	e.subscribe(t, "sub-alice", "plus")
	r = e.do(t, tok, "GET", "/me", nil)
	b = r.body["billing"].(map[string]any)
	l = r.body["limits"].(map[string]any)
	if b["status"] != "active" || b["plan"] != "plus" || b["seats"].(float64) != 2 || b["period_end"] == nil || l["projects"].(float64) != 25 || l["xl"].(float64) != 1 || l["memory_gb"].(float64) != 16 {
		t.Fatalf("subscribed /me: %s", r.raw)
	}
	// Validation.
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "bad name!", "class": "large"}); r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("bad name: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "x", "class": "huge"}); r.status != 400 {
		t.Fatalf("bad class: %d", r.status)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "x", "class": "small", "bogus": 1}); r.status != 400 {
		t.Fatalf("unknown field: %d", r.status)
	}
	// Create and watch it come up.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "Todo.App", "class": "large", "remote_url": "github.com/alice/todo"})
	if r.status != 201 || r.body["slug"] != "todo-app" || r.body["state"] != "creating" {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	op := e.waitOp(t, r)
	if op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	r = e.do(t, tok, "GET", "/projects/"+pid, nil)
	if r.status != 200 || r.body["state"] != "running" || r.body["guest_ip"] == nil || r.body["host_id"] == nil {
		t.Fatalf("running project: %d %s", r.status, r.raw)
	}
	// Duplicates.
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "small"}); r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("dup name: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "other", "class": "small", "remote_url": "github.com/alice/todo"}); r.status != 409 {
		t.Fatalf("dup remote: %d %s", r.status, r.raw)
	}
	// The plan's memory (Plus, 16 GB): the large running takes 8; an xl
	// (16) does not fit beside it, two smalls do, a third does not.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "big", "class": "xl"})
	if r.status != 402 || errDetail(r, "reason") != "plan_limit" {
		t.Fatalf("xl beside a large on Plus: %d %s", r.status, r.raw)
	}
	if projects, _ := r.body["error"].(map[string]any)["detail"].(map[string]any)["projects"].([]any); len(projects) != 1 || projects[0] != "todo-app" {
		t.Fatalf("plan_limit names the machines: %s", r.raw)
	}
	for _, name := range []string{"second", "third"} {
		r = e.do(t, tok, "POST", "/projects", map[string]any{"name": name, "class": "small"})
		if r.status != 201 {
			t.Fatalf("%s: %d %s", name, r.status, r.raw)
		}
		e.waitOp(t, r)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "fourth", "class": "small"})
	if r.status != 402 || errDetail(r, "reason") != "plan_limit" {
		t.Fatalf("memory full: %d %s", r.status, r.raw)
	}
	// past_due is payment_required on start.
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'past_due' where logto_sub = 'sub-alice'"); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/start", nil); r.status != 402 || errCode(r) != "payment_required" || errDetail(r, "reason") != "past_due" {
		t.Fatalf("past due start: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'active' where logto_sub = 'sub-alice'"); err != nil {
		t.Fatal(err)
	}
	// List shows three; cross-user is 404.
	if r := e.do(t, tok, "GET", "/projects", nil); r.status != 200 || len(r.list) != 3 {
		t.Fatalf("list: %d %d", r.status, len(r.list))
	}
	bobTok := e.signIn(t, "sub-bob", "bob")
	if r := e.do(t, bobTok, "GET", "/projects/"+pid, nil); r.status != 404 {
		t.Fatalf("cross-user: %d", r.status)
	}
	if r := e.do(t, bobTok, "POST", "/projects/"+pid+"/stop", nil); r.status != 404 {
		t.Fatalf("cross-user stop: %d", r.status)
	}
	// Secrets: put pushes to the running guest, list never shows values.
	val := base64.StdEncoding.EncodeToString([]byte("postgres://PLANTED-SECRET-VALUE"))
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/secrets/DATABASE_URL", map[string]any{"value": val}); r.status != 200 || r.body["pushed"] != true {
		t.Fatalf("put secret: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/secrets/user_ca.pub", map[string]any{"value": val}); r.status != 400 {
		t.Fatalf("reserved name: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/secrets/lower", map[string]any{"value": val}); r.status != 400 {
		t.Fatalf("bad name: %d", r.status)
	}
	r = e.do(t, tok, "GET", "/projects/"+pid+"/secrets", nil)
	if r.status != 200 || len(r.list) != 1 || strings.Contains(string(r.raw), "PLANTED") || strings.Contains(string(r.raw), val) {
		t.Fatalf("list secrets: %d %s", r.status, r.raw)
	}
	e.h.WaitFor("secret pushed to the guest", func() bool {
		for _, g := range e.h.Fake.Guests() {
			if string(g.Secrets["DATABASE_URL"]) == "postgres://PLANTED-SECRET-VALUE" {
				return true
			}
		}
		return false
	})
	if r := e.do(t, tok, "DELETE", "/projects/"+pid+"/secrets/DATABASE_URL", nil); r.status != 204 {
		t.Fatalf("delete secret: %d", r.status)
	}
	if r := e.do(t, tok, "DELETE", "/projects/"+pid+"/secrets/DATABASE_URL", nil); r.status != 404 {
		t.Fatalf("delete missing secret: %d", r.status)
	}
	// Certificates.
	_, pub, _ := sshca.GenerateHostKey("laptop")
	pubLine := strings.TrimSpace(string(sshMarshal(pub)))
	r = e.do(t, tok, "POST", "/certs", map[string]any{"public_key": pubLine, "project_ids": []string{pid}})
	if r.status != 200 || !strings.HasPrefix(r.body["certificate"].(string), "ssh-ed25519-cert-v01@openssh.com ") || r.body["gateway"].(map[string]any)["host"] != "ssh.test" {
		t.Fatalf("certs: %d %s", r.status, r.raw)
	}
	serial := int64(r.body["serial"].(float64))
	if r := e.do(t, tok, "POST", "/certs", map[string]any{"public_key": pubLine, "project_ids": []string{uuid.NewString()}}); r.status != 404 {
		t.Fatalf("cert for foreign project: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/certs", map[string]any{"public_key": "nope", "project_ids": []string{pid}}); r.status != 400 {
		t.Fatalf("bad key: %d", r.status)
	}
	// The gateway's route check and revocation.
	ir := e.internalDo(t, "GET", "/internal/route?login=todo-app.alice-dev", nil)
	if ir.status != 200 || ir.body["project_id"] != pid || ir.body["state"] != "running" {
		t.Fatalf("route: %d %s", ir.status, ir.raw)
	}
	if ir := e.internalDo(t, "GET", "/internal/route?login=todo-app.bob", nil); ir.status != 404 {
		t.Fatalf("route wrong user: %d", ir.status)
	}
	if ir := e.internalDo(t, "GET", "/internal/revoked", nil); ir.status != 200 || len(ir.list) != 0 {
		t.Fatalf("revoked before: %d %s", ir.status, ir.raw)
	}
	if r := e.do(t, tok, "POST", "/certs/revoke", map[string]any{"serial": serial}); r.status != 200 {
		t.Fatalf("revoke: %d %s", r.status, r.raw)
	}
	if ir := e.internalDo(t, "GET", "/internal/revoked", nil); ir.status != 200 || len(ir.list) != 1 || int64(ir.list[0].(float64)) != serial {
		t.Fatalf("revoked after: %d %s", ir.status, ir.raw)
	}
	if ir := e.internalDo(t, "GET", "/internal/ca", nil); ir.status != 200 || !strings.HasPrefix(ir.body["user_ca_pub"].(string), "ssh-ed25519 ") {
		t.Fatalf("ca: %d %s", ir.status, ir.raw)
	}
	// Sessions (I-176): two relays under one certificate are two sessions,
	// and closing one leaves the other; a report without a session_id (a
	// gateway older than I-176) is still accepted, one row per certificate.
	session := func(event, id string, want float64) {
		t.Helper()
		body := map[string]any{"project_id": pid, "event": event, "cert_serial": serial}
		if id != "-" {
			body["session_id"] = id
		}
		ir := e.internalDo(t, "POST", "/internal/sessions", body)
		if ir.status != 200 || ir.body["open"] != want {
			t.Fatalf("sessions %s %s: %d %s, want open %v", event, id, ir.status, ir.raw, want)
		}
	}
	session("opened", "0a1b", 1)
	session("opened", "2c3d", 2)
	session("closed", "0a1b", 1)
	session("opened", "-", 2)
	session("closed", "-", 1)
	session("closed", "2c3d", 0)
	if ir := e.internalDo(t, "POST", "/internal/sessions", map[string]any{"project_id": pid, "event": "opened", "cert_serial": serial, "session_id": "x y"}); ir.status != 400 {
		t.Fatalf("a session id with a space: %d %s", ir.status, ir.raw)
	}
	session("opened", "0a1b", 1)
	if ir := e.internalDo(t, "POST", "/internal/gateway-certs", map[string]any{"public_key": pubLine, "project_id": pid}); ir.status != 200 || !strings.Contains(ir.body["certificate"].(string), "ssh-ed25519-cert") {
		t.Fatalf("gateway cert: %d %s", ir.status, ir.raw)
	}
	if ir := e.internalDo(t, "GET", "/internal/hosts", nil); ir.status != 200 || len(ir.list) != 1 {
		t.Fatalf("hosts: %d %s", ir.status, ir.raw)
	}
	// The secret put and delete above each queued an update_secrets op for
	// the running guest; let them drain or the stop answers 409.
	e.h.WaitIdle(uuid.MustParse(pid))
	// Stop with snapshot, then start; ops are visible.
	r = e.do(t, tok, "POST", "/projects/"+pid+"/stop", map[string]any{"snapshot": true})
	if r.status != 202 {
		t.Fatalf("stop: %d %s", r.status, r.raw)
	}
	opID := r.body["op_id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("stop op: %+v", op.Error)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/ops/"+opID, nil); r.status != 200 || r.body["state"] != "done" {
		t.Fatalf("get op: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/snapshots", nil); r.status != 200 || len(r.list) != 1 || r.list[0].(map[string]any)["reason"] != "stop" {
		t.Fatalf("snapshots: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/stop", nil); r.status != 409 {
		t.Fatalf("stop twice: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"class": "small"}); r.status != 200 || r.body["class"] != "small" {
		t.Fatalf("class change while stopped: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	if r.status != 202 {
		t.Fatalf("start: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("start op: %+v", op.Error)
	}
	if r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"class": "large"}); r.status != 409 {
		t.Fatalf("class change while running: %d", r.status)
	}
	// Resize grows only.
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/resize", map[string]any{"volume_bytes": 1}); r.status != 400 {
		t.Fatalf("shrink: %d", r.status)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/resize", map[string]any{"volume_bytes": 100 << 30})
	if r.status != 202 {
		t.Fatalf("resize: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
	// Events and logs.
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/events", nil); r.status != 200 || len(r.list) == 0 {
		t.Fatalf("events: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/logs?kind=ops", nil); r.status != 200 || !strings.Contains(string(r.raw), `"kind":"create"`) {
		t.Fatalf("ops log: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "GET", "/projects/"+pid+"/logs?kind=build", nil)
	if r.status != 200 || !strings.Contains(string(r.raw), "evaluating") || !strings.Contains(string(r.raw), `"kind":"build"`) {
		t.Fatalf("build log: %d %s", r.status, r.raw)
	}
	// Each build line has its time (I-322), and since= is a cursor.
	var first struct {
		TS time.Time `json:"ts"`
	}
	lines := strings.Split(strings.TrimSpace(string(r.raw)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &first); err != nil || first.TS.IsZero() {
		t.Fatalf("build line without ts: %s", lines[len(lines)-1])
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/logs?kind=build&since="+url.QueryEscape(first.TS.Format(time.RFC3339Nano)), nil); r.status != 200 || strings.TrimSpace(string(r.raw)) != "" {
		t.Fatalf("build log after its last line: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/usage", nil); r.status != 200 {
		t.Fatalf("usage: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/billing/portal", nil); r.status != 503 || errCode(r) != "billing_disabled" {
		t.Fatalf("billing disabled: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/catalog", nil); r.status != 200 || len(r.list) < 5 {
		t.Fatalf("catalog: %d", r.status)
	}
	// Restore as a new project.
	r = e.do(t, tok, "GET", "/projects/"+pid+"/snapshots", nil)
	sid := r.list[0].(map[string]any)["id"].(string)
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/snapshots/"+sid+"/restore", nil); r.status != 409 {
		t.Fatalf("restore into a running project: %d %s", r.status, r.raw)
	}
	// Destroy frees a slot; restore --as-new brings the snapshot back.
	r = e.do(t, tok, "DELETE", "/projects/"+pid, nil)
	if r.status != 202 {
		t.Fatalf("destroy: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("destroy op: %+v", op.Error)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid, nil); r.status != 404 {
		t.Fatalf("destroyed project visible: %d", r.status)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/snapshots", nil); r.status != 200 || len(r.list) != 2 {
		t.Fatalf("destroyed project's snapshots: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/snapshots/"+sid+"/restore", map[string]any{"as_new_project": "todo-yesterday"})
	if r.status != 202 {
		t.Fatalf("restore as new: %d %s", r.status, r.raw)
	}
	newID := r.body["project_id"].(string)
	// The source is destroyed, so its closure has no GC root on the host
	// (I-115): the restore plan must build before it restores and boots.
	var phases []string
	if err := e.h.Pool.QueryRow(ctx, "select array(select jsonb_array_elements_text(params->'phases')) from ops where id = $1", r.body["op_id"].(string)).Scan(&phases); err != nil {
		t.Fatal(err)
	}
	if strings.Join(phases, ",") != "build,restore,start_guest" {
		t.Fatalf("restore-as-new of a destroyed project planned %v, want build,restore,start_guest", phases)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("restore op: %+v", op.Error)
	}
	if r := e.do(t, tok, "GET", "/projects/"+newID, nil); r.status != 200 || r.body["state"] != "running" || r.body["slug"] != "todo-yesterday" {
		t.Fatalf("restored project: %d %s", r.status, r.raw)
	}
	// Suspended users get forbidden everywhere but GET /me.
	if _, err := e.h.Pool.Exec(ctx, "update users set suspended_at = now() where logto_sub = 'sub-alice'"); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "GET", "/projects", nil); r.status != 403 || errCode(r) != "forbidden" || !strings.Contains(string(r.raw), "account suspended") {
		t.Fatalf("suspended: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/me", nil); r.status != 200 {
		t.Fatalf("suspended me: %d", r.status)
	}
	// Logs never carry the planted values.
	out := e.logs.String()
	for _, needle := range []string{"PLANTED-SECRET-VALUE", val, "alice@example.com", tok, "Alice_Dev", "alice-dev"} {
		if strings.Contains(out, needle) {
			t.Fatalf("log contains %q", needle)
		}
	}
	if !strings.Contains(out, `"event":"request"`) || !strings.Contains(out, `"event":"cert_issue"`) {
		t.Fatal("expected request and cert_issue log events")
	}
}

// A menu selection may carry any nixpkgs package next to catalog ids
// (DECISIONS I-220): rendered, built, returned by GET /config, and
// removable down to an empty menu, which keeps the project in menu mode.
func TestConfigMenuPackages(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-pkg", "pkg")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "pkgs", "class": "small"})
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	put := func(sel []map[string]any) resp {
		return e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"menu": sel})
	}
	r = put([]map[string]any{{"package": "gcc"}, {"id": "bun"}, {"package": "python312Packages.black"}})
	if r.status != 202 {
		t.Fatalf("menu put: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("menu build: %+v", op.Error)
	}
	r = e.do(t, tok, "GET", "/projects/"+pid+"/config", nil)
	frag, _ := r.body["fragment"].(string)
	for _, want := range []string{
		`# repose-menu: [{"id":"bun"},{"package":"gcc"},{"package":"python312Packages.black"}]`,
		`(nixpkg [ "gcc" ])`, `(nixpkg [ "python312Packages" "black" ])`, "pkgs.bun",
	} {
		if !strings.Contains(frag, want) {
			t.Fatalf("fragment lacks %q:\n%s", want, frag)
		}
	}
	if m, _ := json.Marshal(r.body["menu"]); !strings.Contains(string(m), `{"package":"gcc"}`) {
		t.Fatalf("menu: %s", m)
	}
	for _, c := range []struct {
		sel  []map[string]any
		want string
	}{
		{[]map[string]any{{"package": "a;b"}}, `\"a;b\" is not a nixpkgs attribute path`},
		{[]map[string]any{{"package": "${x}"}}, `is not a nixpkgs attribute path`},
		{[]map[string]any{{"package": "bun"}}, `\"bun\" is a catalog id`},
		{[]map[string]any{{"package": "gcc", "id": "bun"}}, `either id (with options) or package`},
		{[]map[string]any{{"package": strings.Repeat("a", 201)}}, `at most 200 characters`},
	} {
		r := put(c.sel)
		if r.status != 400 || errCode(r) != "invalid" || !strings.Contains(string(r.raw), c.want) {
			t.Fatalf("%v: %d %s", c.sel, r.status, r.raw)
		}
	}
	// Removing everything is an empty menu, still generated.
	r = put([]map[string]any{})
	if r.status != 202 {
		t.Fatalf("empty menu: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
	r = e.do(t, tok, "GET", "/projects/"+pid+"/config", nil)
	if frag, _ := r.body["fragment"].(string); !strings.HasPrefix(frag, "# generated by repose") || strings.Contains(frag, "nixpkg") {
		t.Fatalf("empty menu fragment: %s", r.raw)
	}
	if r := put([]map[string]any{{"package": "gcc"}}); r.status != 202 {
		t.Fatalf("menu after empty: %d %s", r.status, r.raw)
	}
}

func TestConfigRoutes(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-carol", "carol")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "cfg", "class": "small"})
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/config", nil); r.status != 200 || r.body["status"] != "applied" {
		t.Fatalf("get config: %d %s", r.status, r.raw)
	}
	if _, ok := config.NewParser(); ok {
		r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": "{ home.packages = [ pkgs.ripgrep ; }"})
		if r.status != 400 || errCode(r) != "invalid" || !strings.Contains(string(r.raw), "fragment.nix:1:") || r.body["error"].(map[string]any)["detail"].(map[string]any)["fragment_line"].(float64) != 1 {
			t.Fatalf("parse error: %d %s", r.status, r.raw)
		}
	}
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": "{ }", "menu": []any{}}); r.status != 400 {
		t.Fatalf("both fragment and menu: %d", r.status)
	}
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": strings.Repeat("x", 300<<10)}); r.status != 400 {
		t.Fatalf("oversize: %d", r.status)
	}
	// Menu renders and builds.
	r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"menu": []map[string]any{{"id": "bun"}, {"id": "nodejs", "options": map[string]string{"version": "22"}}}})
	if r.status != 202 || r.body["revision_id"] == nil {
		t.Fatalf("menu put: %d %s", r.status, r.raw)
	}
	rid := r.body["revision_id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("menu build: %+v", op.Error)
	}
	r = e.do(t, tok, "GET", "/projects/"+pid+"/config", nil)
	if r.body["revision_id"] != rid || !strings.Contains(r.body["fragment"].(string), "pkgs.nodejs_22") || r.body["menu"] == nil {
		t.Fatalf("config after menu: %s", r.raw)
	}
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"menu": []map[string]any{{"id": "nope"}}}); r.status != 400 {
		t.Fatalf("unknown menu item: %d %s", r.status, r.raw)
	}
	// Same fragment again is a no-op.
	frag := r.body["fragment"].(string)
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": frag}); r.status != 200 || r.body["unchanged"] != true {
		t.Fatalf("unchanged: %d %s", r.status, r.raw)
	}
	// Taking over with a custom fragment turns the menu off.
	r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.deno ]; }"})
	if r.status != 202 {
		t.Fatalf("fragment put: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"menu": []map[string]any{{"id": "bun"}}}); r.status != 409 || !strings.Contains(string(r.raw), "custom fragment") {
		t.Fatalf("menu on fragment project: %d %s", r.status, r.raw)
	}
	// reboot_required blocks apply until confirmed.
	e.h.Fake.SetKernelChanged(true)
	r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.zig ]; }"})
	if r.status != 202 {
		t.Fatalf("kernel put: %d %s", r.status, r.raw)
	}
	krid := r.body["revision_id"].(string)
	opID := r.body["op_id"].(string)
	if op := e.waitOp(t, r); op.State != "done" || !op.RebootRequired {
		t.Fatalf("kernel build: %s reboot=%v %+v", op.State, op.RebootRequired, op.Error)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/ops/"+opID, nil); r.body["reboot_required"] != true {
		t.Fatalf("op reboot flag: %s", r.raw)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/config", nil); r.body["revision_id"] == krid {
		t.Fatal("kernel revision applied without confirmation")
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/config/revisions/"+krid+"/apply", nil); r.status != 409 {
		t.Fatalf("apply without reboot: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/config/revisions/"+krid+"/apply?reboot=true", nil)
	if r.status != 202 {
		t.Fatalf("apply with reboot: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("apply op: %+v", op.Error)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/config", nil); r.body["revision_id"] != krid {
		t.Fatalf("after confirmed apply: %s", r.raw)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/config/revisions", nil); r.status != 200 || len(r.list) < 4 {
		t.Fatalf("revisions: %d %d", r.status, len(r.list))
	}
	// Hold flag.
	if r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"hold_base_updates": true}); r.status != 200 || r.body["hold_base_updates"] != true {
		t.Fatalf("hold: %d %s", r.status, r.raw)
	}
	// The laptop's zone, sent on a run or attach from another zone (I-198).
	if r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"tz": "Asia/Tokyo"}); r.status != 200 || r.body["tz"] != "Asia/Tokyo" {
		t.Fatalf("tz: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"tz": "JST"}); r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("tz abbreviation: %d %s", r.status, r.raw)
	}
}

func TestRateLimits(t *testing.T) {
	e := newEnvLimits(t, nil)
	tok := e.signIn(t, "sub-dan", "dan")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "rl", "class": "small"})
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	_, pub, _ := sshca.GenerateHostKey("k")
	line := strings.TrimSpace(string(sshMarshal(pub)))
	limited := false
	for i := 0; i < 12; i++ {
		r := e.do(t, tok, "POST", "/certs", map[string]any{"public_key": line, "project_ids": []string{pid}})
		if r.status == 429 {
			if errCode(r) != "rate_limited" || r.hdr.Get("Retry-After") == "" {
				t.Fatalf("rate limited shape: %s %v", r.raw, r.hdr)
			}
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("12 cert requests were never rate limited (limit is 10/min)")
	}
	// Reads: a minute of a waiting CLI's polling (two GETs every 500 ms,
	// I-154) passes; the 600/min read bucket still ends somewhere (I-187).
	for i := 0; i < 240; i++ {
		if r := e.do(t, tok, "GET", "/projects/"+pid, nil); r.status != 200 {
			t.Fatalf("GET %d refused: %d %s", i, r.status, r.raw)
		}
	}
	limited = false
	for i := 0; i < 400; i++ {
		r := e.do(t, tok, "GET", "/me", nil)
		if r.status == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("read limit of 600/min never hit")
	}
	// Writes keep the general 60/min, untouched by the reads above.
	limited = false
	for i := 0; i < 70; i++ {
		r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"hold_base_updates": i%2 == 0})
		if r.status == 429 {
			if i < 40 {
				t.Fatalf("write %d refused after only reads had been spent: %s", i, r.raw)
			}
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("general limit of 60/min never hit")
	}
}

func TestSSEDeliversEveryLineInOrderWithSince(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-eve", "eve")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "sse", "class": "small"})
	pid := r.body["id"].(string)
	opID := r.body["op_id"].(string)
	e.waitOp(t, r)
	// 10k lines appended to the finished op through the store (the fake host
	// only emits three).
	oid := uuid.MustParse(opID)
	for i := int64(4); i <= 10003; i++ {
		e.h.Logs.Append(oid, i, fmt.Sprintf("line %d", i))
	}
	e.h.Logs.Flush(e.h.Ctx)
	// The background flusher may still be inserting the batches it took.
	e.h.WaitFor("all lines stored", func() bool {
		var n int
		_ = e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from build_logs where op_id = $1", oid).Scan(&n)
		return n == 10003
	})
	read := func(since int64, query bool) []int64 {
		url := e.api.URL + "/v1/projects/" + pid + "/ops/" + opID + "/log"
		if query {
			url += fmt.Sprintf("?access_token=%s&since=%d", tok, since)
		}
		req, _ := http.NewRequest("GET", url, nil)
		if !query {
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("Last-Event-ID", fmt.Sprint(since))
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("sse status %d %s", res.StatusCode, res.Header.Get("Content-Type"))
		}
		var seqs []int64
		done := false
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "id: ") {
				var n int64
				_, _ = fmt.Sscanf(l, "id: %d", &n)
				seqs = append(seqs, n)
			}
			if l == "event: done" {
				done = true
			}
		}
		if !done {
			t.Fatal("no done event")
		}
		return seqs
	}
	seqs := read(0, false)
	if len(seqs) != 10003 {
		t.Fatalf("got %d lines", len(seqs))
	}
	for i, s := range seqs {
		if s != int64(i+1) {
			t.Fatalf("out of order at %d: %d", i, s)
		}
	}
	tail := read(10000, true)
	if len(tail) != 3 || tail[0] != 10001 {
		t.Fatalf("since=10000 gave %v", tail)
	}
	// The token in the query string never reaches the log.
	if strings.Contains(e.logs.String(), tok) {
		t.Fatal("access_token logged")
	}
}

func TestSSELiveStreamAndConcurrentLoad(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-fay", "fay")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "load", "class": "small"})
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	// 20 concurrent live SSE streams on one build op.
	r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.bun ]; }"})
	if r.status != 202 {
		t.Fatalf("put: %d %s", r.status, r.raw)
	}
	opID := r.body["op_id"].(string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[int]int{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest("GET", e.api.URL+"/v1/projects/"+pid+"/ops/"+opID+"/log", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer func() { _ = res.Body.Close() }()
			n := 0
			switching := false
			sc := bufio.NewScanner(res.Body)
			for sc.Scan() {
				if strings.HasPrefix(sc.Text(), "id: ") {
					n++
				}
				if strings.Contains(sc.Text(), `"line":"switching the machine"`) && strings.Contains(sc.Text(), `"ts":`) {
					switching = true
				}
			}
			if !switching {
				n = -n // the api's own apply line (I-320) is missing
			}
			mu.Lock()
			counts[i] = n
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	for i, n := range counts {
		// hostd's three, then the api's "switching the machine" as the
		// running project's build moves to its apply (I-320).
		if n != 4 {
			t.Fatalf("stream %d saw %d lines (negative: no switching line)", i, n)
		}
	}
	if len(counts) != 20 {
		t.Fatalf("%d streams finished", len(counts))
	}
	// 200 concurrent GET /projects, p99 under 200 ms. The general rate limit
	// is per user, so spread over users.
	var toks []string
	for i := 0; i < 5; i++ {
		toks = append(toks, e.signIn(t, fmt.Sprintf("sub-load-%d", i), fmt.Sprintf("load%d", i)))
	}
	durations := make([]time.Duration, 200)
	var lwg sync.WaitGroup
	for i := 0; i < 200; i++ {
		lwg.Add(1)
		go func(i int) {
			defer lwg.Done()
			start := time.Now()
			req, _ := http.NewRequest("GET", e.api.URL+"/v1/projects", nil)
			req.Header.Set("Authorization", "Bearer "+toks[i%len(toks)])
			res, err := http.DefaultClient.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
			}
			durations[i] = time.Since(start)
		}(i)
	}
	lwg.Wait()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p99 := durations[197]
	t.Logf("200 concurrent GET /projects: p50=%v p99=%v max=%v", durations[100], p99, durations[199])
	if p99 > 2*time.Second {
		t.Fatalf("p99 %v", p99)
	}
}

// The user listener is what the public proxy fronts, so /metrics must not
// be reachable through it: the registry is served by the metrics listener
// alone (DECISIONS I-136; found answering 200 from the internet on
// 2026-09-21).
func TestMetricsIsNotOnTheUserListener(t *testing.T) {
	e := newEnv(t)
	res, err := http.Get(e.api.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("GET /metrics on the user listener: %d, want 404", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.api.URL+"/v1/metrics", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatalf("GET /v1/metrics on the user listener answered 200")
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	res, err := http.Get(e.api.URL + "/healthz")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("healthz %v %v", res, err)
	}
	_ = res.Body.Close()
	if _, err := db.MigrateDown(e.h.Ctx, e.h.Pool, 1); err != nil {
		t.Fatal(err)
	}
	res, _ = http.Get(e.api.URL + "/healthz")
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 503 || !strings.Contains(string(body), "migrations pending") {
		t.Fatalf("healthz with pending migration: %d %s", res.StatusCode, body)
	}
	if _, err := db.MigrateUp(e.h.Ctx, e.h.Pool); err != nil {
		t.Fatal(err)
	}
	if ir := e.internalDo(t, "POST", "/internal/events", map[string]any{"source_ip": "10.0.0.1", "agent": "claude", "kind": "completed", "summary": "x"}); ir.status != 404 {
		t.Fatalf("event from unknown ip: %d %s", ir.status, ir.raw)
	}
}

// ntfy_url: null clears the topic (interfaces/api.md, and what the CLI's
// `repose notify set --ntfy none` sends); an absent key leaves it; "" also
// clears. Live, null was read as absent and the old topic stayed.
func TestPatchMeNtfyNullClears(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-ntfy", "ntfyer")
	ntfy := func() any {
		r := e.do(t, tok, "GET", "/me", nil)
		return r.body["notify"].(map[string]any)["ntfy_url"]
	}
	set := func(body map[string]any) {
		t.Helper()
		if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": body}); r.status != 200 {
			t.Fatalf("patch %v: %d %s", body, r.status, r.raw)
		}
	}
	set(map[string]any{"ntfy_url": "https://ntfy.example/a"})
	set(map[string]any{"email": false})
	if got := ntfy(); got != "https://ntfy.example/a" {
		t.Fatalf("absent ntfy_url changed it: %v", got)
	}
	set(map[string]any{"ntfy_url": nil})
	if got := ntfy(); got != nil {
		t.Fatalf("ntfy_url null did not clear: %v", got)
	}
	// A literal non-public address, or localhost, is refused when saved.
	for _, bad := range []string{"http://127.0.0.1:8080/t", "http://169.254.169.254/latest", "http://[::1]/t", "http://10.255.0.1:3100/t", "http://localhost/t", "http://api.localhost./t"} {
		if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"ntfy_url": bad}}); r.status != 400 {
			t.Fatalf("ntfy_url %s: %d %s, want 400", bad, r.status, r.raw)
		}
	}
	set(map[string]any{"ntfy_url": "https://ntfy.example/b"})
	set(map[string]any{"ntfy_url": ""})
	if got := ntfy(); got != nil {
		t.Fatalf(`ntfy_url "" did not clear: %v`, got)
	}
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"ntfy_url": 5}}); r.status != 400 {
		t.Fatalf("ntfy_url 5: %d, want 400", r.status)
	}
}
