// Package cli implements cmd/repose: the API client, project resolution,
// certificate management, and every command in docs/workstreams/07-cli.md.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// APIError is the decoded {error: {code, message, detail}} envelope from
// docs/interfaces/api.md.
type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
	Status  int            `json:"-"`
	// RetryAfter is a 429's Retry-After header, zero when absent.
	RetryAfter time.Duration `json:"-"`
	// RequestID is the answer's X-Request-Id, for a support thread.
	RequestID string `json:"-"`
	// Raw is set when the body was not the api's error envelope: a proxy
	// in front of it answered (a deploy's 502), and Message is that body.
	Raw bool `json:"-"`
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Is lets errors.Is(err, apiCode(...)) match on the API error code.
func (e *APIError) Is(target error) bool {
	t, ok := target.(codeError)
	return ok && e.Code == string(t)
}

type codeError string

func (c codeError) Error() string { return string(c) }

// TokenSource supplies and refreshes the bearer token. The CLI's real
// implementation is the OIDC login state; tests use a static token.
type TokenSource interface {
	// AccessToken returns a token to try. forceRefresh asks it to refresh
	// first (used after a 401).
	AccessToken(ctx context.Context, forceRefresh bool) (string, error)
}

// staticToken is a TokenSource that never refreshes, for tests and
// internal routes.
type staticToken string

func (s staticToken) AccessToken(context.Context, bool) (string, error) { return string(s), nil }

// Client is the HTTP API client (docs/interfaces/api.md).
type Client struct {
	BaseURL string
	Tokens  TokenSource
	HTTP    *http.Client
	// Warn prints one line to the user while a request waits (the api's
	// rate limit); nil prints nothing.
	Warn func(string)
}

func newClient(baseURL string, tokens TokenSource) *Client {
	return &Client{BaseURL: strings.TrimSuffix(baseURL, "/"), Tokens: tokens, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// rateLimitBudget is how long one request waits out the api's per-user
// rate limit before the refusal reaches the caller (DECISIONS I-187). The
// api refuses before any handler runs, so a refused request of any method
// did nothing and is safe to send again.
var rateLimitBudget = 60 * time.Second

// rateLimitWait is the pause after a 429: its Retry-After, else 2 s, and
// never more than 15 s at a time.
func rateLimitWait(e *APIError) time.Duration {
	d := e.RetryAfter
	if d <= 0 {
		d = 2 * time.Second
	}
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}

// gatewayRetryPause is the pause before a GET answered 502, 503 or 504
// by the proxy in front of the api (a deploy) is sent again.
var gatewayRetryPause = 2 * time.Second

// userAgent names the CLI and its version; the api logs only that it was
// the CLI (clientKind), and the version tells it which release asked.
func userAgent() string {
	v := cliVersion
	if v == "" {
		v = "dev"
	}
	return "repose-cli/" + v
}

// do sends one request, retrying once on a 401 unauthenticated after a
// forced token refresh (07-cli.md §5.2: "on 401 with unauthenticated,
// refresh once, retry once"), waiting out a rate_limited refusal for
// up to rateLimitBudget, and sending a GET again once when the proxy in
// front of the api answered 502, 503 or 504 (DECISIONS I-623).
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doHeader(ctx, method, path, body, out, nil)
}

// doHeader is do, also storing the successful response's headers in hdr
// when it is not nil.
func (c *Client) doHeader(ctx context.Context, method, path string, body any, out any, hdr *http.Header) error {
	var refreshed, gatewayRetried bool
	var limitedSince time.Time
	for {
		var reader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			reader = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("User-Agent", userAgent())
		if c.Tokens != nil {
			tok, err := c.Tokens.AccessToken(ctx, refreshed)
			if err != nil {
				return tokenError(err)
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return &unreachableError{host: req.URL.Host, cause: err}
		}
		noteLatestCLI(resp.Header)
		apiErr, decodeErr := readResponse(resp, out)
		if apiErr != nil && apiErr.Raw && method == http.MethodGet && !gatewayRetried && isGatewayStatus(apiErr.Status) {
			gatewayRetried = true
			if err := sleepOrDone(ctx, gatewayRetryPause); err != nil {
				return err
			}
			continue
		}
		if apiErr != nil && apiErr.Code == "unauthenticated" && !refreshed && c.Tokens != nil {
			refreshed = true
			continue
		}
		if apiErr != nil && apiErr.Code == "rate_limited" {
			if limitedSince.IsZero() {
				limitedSince = time.Now()
				if c.Warn != nil {
					c.Warn("Waiting for the api's rate limit...")
				}
			}
			if time.Since(limitedSince) < rateLimitBudget {
				if err := sleepOrDone(ctx, rateLimitWait(apiErr)); err != nil {
					return err
				}
				continue
			}
		}
		if decodeErr != nil {
			return decodeErr
		}
		if apiErr != nil {
			return apiErr
		}
		if hdr != nil {
			*hdr = resp.Header
		}
		return nil
	}
}

// tokenError is a token source's failure as the client returns it: the
// login server out of reach is a network problem (exit 1), anything else
// a login the user has to make again (exit 3, DECISIONS I-623).
func tokenError(err error) error {
	var lu *loginUnreachableError
	if errors.As(err, &lu) {
		return lu
	}
	return &notLoggedInError{cause: err}
}

func isGatewayStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func readResponse(resp *http.Response, out any) (*APIError, error) {
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	rid := resp.Header.Get("X-Request-Id")
	if resp.StatusCode >= 400 {
		var env struct {
			Error APIError `json:"error"`
		}
		if err := json.Unmarshal(b, &env); err != nil || env.Error.Code == "" {
			return &APIError{Code: "internal", Message: string(b), Status: resp.StatusCode, RequestID: rid, Raw: true}, nil
		}
		env.Error.Status = resp.StatusCode
		env.Error.RequestID = rid
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			env.Error.RetryAfter = time.Duration(s) * time.Second
		}
		return &env.Error, nil
	}
	if out == nil || len(b) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
			return nil, &notAPIError{url: apiRoot(resp.Request)}
		}
		return nil, err
	}
	return nil, nil
}

// notAPIError is a success answer that is not JSON: --api-url or
// $REPOSE_API_URL points at something else (a web page, a captive
// portal).
type notAPIError struct{ url string }

func (e *notAPIError) Error() string {
	return fmt.Sprintf("%s is not a repose api. Check --api-url or $REPOSE_API_URL.", e.url)
}

// apiRoot is the scheme and host a request went to.
func apiRoot(r *http.Request) string {
	if r == nil || r.URL == nil {
		return "the api url"
	}
	return r.URL.Scheme + "://" + r.URL.Host
}

// unreachableError wraps a transport failure (07-cli.md §6: "API
// unreachable"). host is where the request went.
type unreachableError struct {
	host  string
	cause error
}

func (e *unreachableError) Error() string { return e.cause.Error() }
func (e *unreachableError) Unwrap() error { return e.cause }

// message is the sentence the user reads (DECISIONS I-623).
func (e *unreachableError) message() string {
	host := e.host
	if host == "" {
		host = "the api"
	}
	return fmt.Sprintf("Could not reach %s: %s.", host, netReason(e.cause))
}

// loginUnreachableError is the login server (Logto) out of reach or not
// answering as one, while the CLI reads its discovery document or
// refreshes a token: the network's problem, not the login's.
type loginUnreachableError struct {
	host  string
	cause error
}

func (e *loginUnreachableError) Error() string {
	return fmt.Sprintf("Could not reach the login server (%s): %s.", e.host, netReason(e.cause))
}
func (e *loginUnreachableError) Unwrap() error { return e.cause }

// netReason is a transport failure as a short clause: "connection
// refused", "timed out", "the name does not resolve", or else the last
// part of Go's message.
func netReason(err error) string {
	var dnsErr *net.DNSError
	switch {
	case err == nil:
		return "no answer"
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return "the name " + dnsErr.Name + " does not resolve"
		}
		return "looking up " + dnsErr.Name + " failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "no route to it"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.Error()
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		msg = msg[i+2:]
	}
	return strings.TrimSuffix(msg, ".")
}

// statusError is an HTTP answer that is not what the request expects (a
// proxy's 502 page in place of the login server's JSON).
type statusError struct {
	status int
}

func (e *statusError) Error() string { return fmt.Sprintf("it answered %d", e.status) }

// notLoggedInError wraps a token-source failure (refresh token invalid or
// absent).
type notLoggedInError struct{ cause error }

func (e *notLoggedInError) Error() string { return e.cause.Error() }
func (e *notLoggedInError) Unwrap() error { return e.cause }

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}
func (c *Client) put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPut, path, body, out)
}
func (c *Client) patch(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPatch, path, body, out)
}
func (c *Client) delete(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, out)
}

// getNDJSON is GET /projects/:id/logs's shape (api.md: "JSON lines", the
// fake serves it as application/x-ndjson): a stream of concatenated JSON
// values rather than one JSON array, decoded one at a time into a slice
// built from a zero-value template via a factory so callers keep type
// safety without generics duplicating this per type.
func (c *Client) getNDJSON(ctx context.Context, path string, decodeLine func(dec *json.Decoder) error) error {
	var refreshed bool
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent())
		if c.Tokens != nil {
			tok, err := c.Tokens.AccessToken(ctx, refreshed)
			if err != nil {
				return tokenError(err)
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return &unreachableError{host: req.URL.Host, cause: err}
		}
		noteLatestCLI(resp.Header)
		if resp.StatusCode >= 400 {
			apiErr, decodeErr := readResponse(resp, nil)
			if decodeErr != nil {
				return decodeErr
			}
			if apiErr.Code == "unauthenticated" && !refreshed && c.Tokens != nil {
				refreshed = true
				continue
			}
			return apiErr
		}
		defer func() { _ = resp.Body.Close() }()
		dec := json.NewDecoder(resp.Body)
		for dec.More() {
			if err := decodeLine(dec); err != nil {
				return err
			}
		}
		return nil
	}
}
