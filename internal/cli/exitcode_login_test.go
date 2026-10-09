package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A refresh Logto refuses is an expired login; the login server out of
// reach is a network problem, exit 1, never "Not logged in" and exit 3
// (DECISIONS I-623).
func TestExitCodeForLoginFailures(t *testing.T) {
	cases := []struct {
		err  error
		code int
		want string
	}{
		{&notLoggedInError{cause: errors.New("not logged in")}, ExitNotLoggedIn, "Not logged in. Run `repose login`."},
		{&notLoggedInError{cause: fmt.Errorf("refreshing session: %w", errors.New("invalid_grant: grant request is invalid"))}, ExitNotLoggedIn, "Your login has expired."},
		{&notLoggedInError{cause: fmt.Errorf("refreshing session: %w", errors.New("invalid_client: no such client"))}, ExitNotLoggedIn, "The login server refused to renew your login (invalid_client: no such client). Run `repose login`."},
		{&loginUnreachableError{host: "auth.example", cause: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, ExitGeneric, "Could not reach the login server (auth.example): connection refused."},
		{&loginUnreachableError{host: "auth.example", cause: &net.DNSError{Name: "auth.example", IsNotFound: true}}, ExitGeneric, "the name auth.example does not resolve"},
		{&loginUnreachableError{host: "auth.example", cause: &statusError{status: 502}}, ExitGeneric, "(auth.example): it answered 502."},
		{&unreachableError{host: "api.example", cause: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, ExitGeneric, "Could not reach api.example: connection refused."},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if code := exitCodeFor(c.err, &buf); code != c.code {
			t.Errorf("%v: exit %d, want %d", c.err, code, c.code)
		}
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("%v: printed %q, want it to contain %q", c.err, buf.String(), c.want)
		}
	}
}

// Offline, the token refresh's discovery read fails; the client returns
// that as the login server out of reach, not as a missing login.
func TestOfflineRefreshIsANetworkError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	issuer := "http://" + ln.Addr().String()
	_ = ln.Close() // nothing listens there now
	dir := t.TempDir()
	creds := Credentials{RefreshToken: "r", AccessToken: "old", ExpiresAt: time.Now().Add(-time.Hour), LogtoIssuer: issuer}
	c := newClient("http://127.0.0.1:1/v1", newOIDCTokenSource(dir, http.DefaultClient, creds))
	_, err = c.GetMe(context.Background())
	var lu *loginUnreachableError
	if !errors.As(err, &lu) {
		t.Fatalf("err = %T %v, want *loginUnreachableError", err, err)
	}
	var buf bytes.Buffer
	if code := exitCodeFor(err, &buf); code != ExitGeneric || !strings.Contains(buf.String(), "connection refused") {
		t.Fatalf("exit %d, %q", code, buf.String())
	}
}

// The api's answers become one sentence each, with the exit code the
// cli.md table gives (DECISIONS I-623).
func TestAPIErrorSentences(t *testing.T) {
	cases := []struct {
		name string
		err  *APIError
		code int
		want string
		not  string
	}{
		{"proxy 502", &APIError{Code: "internal", Message: "<html>Bad Gateway</html>", Status: 502, Raw: true, RequestID: "r1"}, ExitGeneric, "The repose api answered 502, request r1. Try again in a minute.", "html"},
		{"api 500", &APIError{Code: "internal", Message: "internal error", Status: 500, RequestID: "r2"}, ExitGeneric, "The repose api failed (internal, request r2). Try again in a minute.", ""},
		{"capacity", &APIError{Code: "capacity", Status: 503}, ExitCapacity, "repose has no room for this machine right now. Try again in a few minutes.", "alerted"},
		{"rate limit", &APIError{Code: "rate_limited", Status: 429}, ExitGeneric, "Too many requests from this account in the last minute. Try again in a minute.", "repose status"},
		{"project cap", &APIError{Code: "invalid", Status: 400, Detail: map[string]any{"reason": "project_limit", "limit": float64(100), "projects": float64(100)}}, ExitPaymentRequired, "You have 100 of the 100 projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`.", ""},
		{"refusal", &APIError{Code: "invalid", Message: "kind must be console, build or ops", Status: 400}, ExitGeneric, "Kind must be console, build or ops (invalid).", "invalid:"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if code := exitCodeFor(c.err, &buf); code != c.code {
			t.Errorf("%s: exit %d, want %d", c.name, code, c.code)
		}
		if got := strings.TrimSpace(buf.String()); got != c.want {
			t.Errorf("%s: printed %q, want %q", c.name, got, c.want)
		}
		if c.not != "" && strings.Contains(buf.String(), c.not) {
			t.Errorf("%s: printed %q, which has %q", c.name, buf.String(), c.not)
		}
	}
}

// A GET the proxy answered 502 (a deploy) is sent once more; a POST is
// not, since the api may have taken it.
func TestGatewayErrorRetriesAGetOnce(t *testing.T) {
	defer func(d time.Duration) { gatewayRetryPause = d }(gatewayRetryPause)
	gatewayRetryPause = time.Millisecond
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>Bad Gateway</html>"))
	}))
	defer srv.Close()
	c := newClient(srv.URL, staticToken("t"))
	err := c.get(context.Background(), "/me", nil)
	var ae *APIError
	if !errors.As(err, &ae) || !ae.Raw || ae.Status != 502 {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 2 {
		t.Fatalf("GET sent %d times, want 2", n.Load())
	}
	n.Store(0)
	_ = c.post(context.Background(), "/projects", map[string]string{}, nil)
	if n.Load() != 1 {
		t.Fatalf("POST sent %d times, want 1", n.Load())
	}
	// A GET answered 502 and then 200 succeeds.
	n.Store(0)
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"handle":"h"}`))
	}))
	defer ok.Close()
	if _, err := newClient(ok.URL, staticToken("t")).GetMe(context.Background()); err != nil {
		t.Fatalf("after one 503: %v", err)
	}
}

// --api-url pointed at a web page is said as that.
func TestSuccessThatIsNotJSONIsNotAnAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>hello</html>"))
	}))
	defer srv.Close()
	_, err := newClient(srv.URL+"/v1", staticToken("t")).GetMe(context.Background())
	var buf bytes.Buffer
	if code := exitCodeFor(err, &buf); code != ExitGeneric || !strings.Contains(buf.String(), srv.URL+" is not a repose api. Check --api-url or $REPOSE_API_URL.") {
		t.Fatalf("exit %d, %q", code, buf.String())
	}
}

// A capacity failure of an op exits 8 like the api's own refusal, and a
// reason that already says to try again gets no second sentence.
func TestOpFailedExitCodes(t *testing.T) {
	cases := []struct {
		oe   OpError
		next string
		code int
		want string
	}{
		{OpError{Code: "insufficient_capacity", Message: "the host has no room for this project right now; try again in a few minutes"}, nextAfterFailedStart("todo-app", "insufficient_capacity"), ExitCapacity,
			"Could not start todo-app: the host has no room for this project right now; try again in a few minutes (insufficient_capacity)."},
		{OpError{Code: "insufficient_capacity"}, nextAfterFailedStart("todo-app", "insufficient_capacity"), ExitCapacity,
			"Could not start todo-app: the host had no room for it right now (insufficient_capacity). Try again in a few minutes."},
		{OpError{Code: "payment_required", Message: "the account needs a plan"}, "", ExitPaymentRequired, "Could not start todo-app: the account needs a plan (payment_required)."},
		{OpError{Code: "boot_failed"}, "", ExitGeneric, "Could not start todo-app"},
	}
	for _, c := range cases {
		err := opFailed("start", "todo-app", c.oe, c.next)
		var buf bytes.Buffer
		if code := exitCodeFor(err, &buf); code != c.code {
			t.Errorf("%s: exit %d, want %d", c.oe.Code, code, c.code)
		}
		got := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(got, c.want) || strings.Count(strings.ToLower(got), "try again") > 1 || strings.Contains(got, "alerted") {
			t.Errorf("%s: %q, want %q", c.oe.Code, got, c.want)
		}
	}
}

// The api's rate limit wait says it is waiting, once.
func TestRateLimitWaitSaysSo(t *testing.T) {
	defer func(b time.Duration) { rateLimitBudget = b }(rateLimitBudget)
	rateLimitBudget = 50 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`))
	}))
	defer srv.Close()
	var said []string
	c := newClient(srv.URL, staticToken("t"))
	c.Warn = func(s string) { said = append(said, s) }
	if err := c.get(context.Background(), "/me", nil); err == nil {
		t.Fatal("no error")
	}
	if len(said) != 1 || said[0] != "Waiting for the api's rate limit..." {
		t.Fatalf("said %q", said)
	}
}
