package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	httpapi "github.com/heracraft/repose/internal/api/http"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// The browser view's mint route (DECISIONS I-1216): an ordinary
// authenticated route whose answer carries no credential.

// countingWaker stands in for internal/api/resting so a test can see
// whether the mint woke anything.
type countingWaker struct{ wakes atomic.Int32 }

func (w *countingWaker) Wake(_ context.Context, _ uuid.UUID, _ string) (*hostdv1.WakeResult, error) {
	w.wakes.Add(1)
	return &hostdv1.WakeResult{WasResting: true, WakeMs: 12}, nil
}

// newViewEnv is an env with the view configured: a fake relay in place of
// the edge, and the dashboard as the one allowed origin.
func newViewEnv(t *testing.T, relay *fakeRelay, waker httpapi.Waker) *env {
	t.Helper()
	return newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		d.ViewRelay = relay
		d.ViewOrigins = []string{"https://repose.run"}
		if waker != nil {
			d.Resting = waker
		}
	})
}

// newViewProject signs a user in and creates a running project.
func newViewProject(t *testing.T, e *env, sub, handle string) (token, pid string) {
	t.Helper()
	token = e.signIn(t, sub, handle)
	r := e.do(t, token, "POST", "/projects", map[string]any{"name": "watched", "class": "small"})
	if r.status != 201 && r.status != 202 && r.status != 200 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid = r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create: %+v", op.Error)
	}
	return token, pid
}

// The mint answers a cookie and an expiry, and nothing else: no ticket in
// the body, no password of any kind, and the cookie is unreadable by
// script, sent over TLS only, same-site only, and scoped to the one route
// that takes it.
func TestMintAnswersACookieAndNoCredentialInTheBody(t *testing.T) {
	e := newViewEnv(t, &fakeRelay{}, nil)
	tok, pid := newViewProject(t, e, "sub-view-mint", "vmint")

	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if r.status != 200 {
		t.Fatalf("mint: %d %s", r.status, r.raw)
	}
	if r.body["stream_path"] != "/v1/browser/stream" {
		t.Fatalf("stream_path %v", r.body["stream_path"])
	}
	if r.body["expires_at"] == nil {
		t.Fatal("no expires_at")
	}
	for _, forbidden := range []string{"ticket", "password", "vnc"} {
		if strings.Contains(strings.ToLower(string(r.raw)), forbidden) {
			t.Fatalf("the answer carries %q: %s", forbidden, r.raw)
		}
	}
	var cookie *http.Cookie
	for _, c := range (&http.Response{Header: r.hdr}).Cookies() {
		if c.Name == "repose_view" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("no repose_view cookie in %v", r.hdr)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie attributes: %+v", cookie)
	}
	if cookie.Path != "/v1/browser/stream" {
		t.Fatalf("cookie path %q", cookie.Path)
	}
	if len(cookie.Value) != 43 { // 256 bits, base64url, unpadded
		t.Fatalf("ticket %q is %d characters", cookie.Value, len(cookie.Value))
	}
	// The row holds the hash and not the ticket.
	var n int
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from browser_view_tickets where hash = sha256($1::bytea)", []byte(cookie.Value)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d rows for the ticket's hash", n)
	}
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from browser_view_tickets where hash = $1::bytea", []byte(cookie.Value)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the ticket itself is in the table")
	}
}

// Bearer header only, as every project route is: the `?access_token=` the
// SSE log route is allowed (I-7) does not reach this one.
func TestMintNeedsABearerHeader(t *testing.T) {
	e := newViewEnv(t, &fakeRelay{}, nil)
	tok, pid := newViewProject(t, e, "sub-view-auth", "vauth")
	if r := e.do(t, "", "POST", "/projects/"+pid+"/browser/view", nil); errCode(r) != "unauthenticated" {
		t.Fatalf("no token: %d %s", r.status, r.raw)
	}
	if r := e.do(t, "", "POST", "/projects/"+pid+"/browser/view?access_token="+tok, nil); errCode(r) != "unauthenticated" {
		t.Fatalf("query token: %d %s", r.status, r.raw)
	}
}

// Another user's project id is not found, not forbidden: the mint goes
// through the single-owner lookup every project route uses.
func TestMintOnAnotherUsersProjectIsNotFound(t *testing.T) {
	e := newViewEnv(t, &fakeRelay{}, nil)
	_, pidA := newViewProject(t, e, "sub-view-a", "viewa")
	tokB := e.signIn(t, "sub-view-b", "viewb")
	if r := e.do(t, tokB, "POST", "/projects/"+pidA+"/browser/view", nil); errCode(r) != "not_found" {
		t.Fatalf("%d %s", r.status, r.raw)
	}
}

// Ten mints an hour per user, then rate_limited: the mint is where the
// wake is, so this is also the ceiling on wakes a script can buy.
func TestMintIsTenAnHour(t *testing.T) {
	e := newViewEnv(t, &fakeRelay{}, nil)
	tok, pid := newViewProject(t, e, "sub-view-rate", "vrate")
	for i := 0; i < httpapi.ViewMintRate; i++ {
		if r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil); r.status != 200 {
			t.Fatalf("mint %d: %d %s", i, r.status, r.raw)
		}
	}
	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if errCode(r) != "rate_limited" {
		t.Fatalf("mint %d: %d %s", httpapi.ViewMintRate+1, r.status, r.raw)
	}
	if r.hdr.Get("Retry-After") == "" {
		t.Fatal("no Retry-After on the refusal")
	}
}

// A machine that is not running is a refusal and never an implicit start:
// `repose stop` is a deliberate act that frees plan memory.
func TestMintRefusesAStoppedMachineAndStartsNothing(t *testing.T) {
	e := newViewEnv(t, &fakeRelay{}, nil)
	tok, pid := newViewProject(t, e, "sub-view-stop", "vstop")
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update projects set state = 'stopped' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if errCode(r) != "conflict" {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	var state string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select state from projects where id = $1", pid).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "stopped" {
		t.Fatalf("the mint moved the machine to %q", state)
	}
	var tickets int
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from browser_view_tickets where project_id = $1", pid).Scan(&tickets); err != nil {
		t.Fatal(err)
	}
	if tickets != 0 {
		t.Fatalf("%d tickets for a stopped machine", tickets)
	}
}

// A resting machine is woken once, and the mints inside the next ten
// minutes answer `resting: true` without waking again: the rule is a row,
// so it survives an api restart, and a page that re-mints in a loop buys
// at most six wakes an hour.
func TestMintWakesARestingMachineOncePerTenMinutes(t *testing.T) {
	w := &countingWaker{}
	e := newViewEnv(t, &fakeRelay{}, w)
	tok, pid := newViewProject(t, e, "sub-view-rest", "vrest")
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update projects set resting_since = now() where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if r.status != 200 {
		t.Fatalf("first mint: %d %s", r.status, r.raw)
	}
	if w.wakes.Load() != 1 {
		t.Fatalf("%d wakes on the first mint", w.wakes.Load())
	}
	if r.body["resting"] != false {
		t.Fatalf("resting %v after a wake", r.body["resting"])
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if r.status != 200 {
		t.Fatalf("second mint: %d %s", r.status, r.raw)
	}
	if w.wakes.Load() != 1 {
		t.Fatalf("%d wakes inside the window", w.wakes.Load())
	}
	if r.body["resting"] != true {
		t.Fatalf("resting %v inside the window: the page must show the desktop-off state instead of billing a resume", r.body["resting"])
	}
	// Eleven minutes later the rule allows one more.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update browser_view_wakes set at = now() - interval '11 minutes' where project_id = $1", pid); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil); r.status != 200 {
		t.Fatalf("third mint: %d %s", r.status, r.raw)
	}
	if w.wakes.Load() != 2 {
		t.Fatalf("%d wakes after the window", w.wakes.Load())
	}
}

// With no relay configured the feature is off and says so, rather than
// the api failing to start or the page waiting on a stream nothing will
// serve.
func TestMintWithNoRelayIsUnavailable(t *testing.T) {
	e := newEnv(t)
	tok, pid := newViewProject(t, e, "sub-view-off", "voff")
	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if errCode(r) != "unavailable" || r.status != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", r.status, r.raw)
	}
}
