package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client speaks the internal routes of docs/interfaces/api.md
// ("Internal (gateway)") over the shared mTLS client certificate.
type Client struct {
	base string
	http *http.Client
}

// APIError is an error envelope the api returned.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api: %d %s: %s", e.Status, e.Code, e.Message)
}

// ErrNotFound is the api's not_found for a route or project.
var ErrNotFound = errors.New("api: not found")

// NewClient makes a client for baseURL (scheme and host, without /v1). A
// nil tlsConfig is plain HTTP or the system roots.
func NewClient(baseURL string, tlsConfig *tls.Config, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("api url %q: scheme and host are required", baseURL)
	}
	tr := &http.Transport{
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        16,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	return &Client{
		base: strings.TrimRight(baseURL, "/") + "/v1/internal",
		http: &http.Client{Transport: tr, Timeout: timeout},
	}, nil
}

// Route is GET /internal/route.
type Route struct {
	ProjectID       string   `json:"project_id"`
	GuestIP         string   `json:"guest_ip"`
	State           string   `json:"state"`
	Principals      []string `json:"principals"`
	HostUnreachable bool     `json:"host_unreachable"`
	// Resting: the machine's vCPUs are stopped and its memory is
	// resident, so the dial wakes it first (DECISIONS I-818). Its state
	// is still `running` and nothing refuses on this flag: the route
	// cache holds an answer for RouteTTL, which makes it stale in both
	// directions, so the wake is idempotent and the dial asks for one
	// whenever the flag is set. False from an api older than I-818.
	Resting bool `json:"resting"`
}

// WakeResult is POST /internal/wake. WasResting says whether that call
// is what resumed the machine; WakeMs is what hostd measured from the
// resume to guestd's answer.
type WakeResult struct {
	WasResting bool   `json:"was_resting"`
	WakeMs     uint32 `json:"wake_ms"`
}

// Host is one row of GET /internal/hosts.
type Host struct {
	HostID    string `json:"host_id"`
	Name      string `json:"name"`
	WGPubkey  string `json:"wg_pubkey"`
	WGIP      string `json:"wg_ip"`
	GuestCIDR string `json:"guest_cidr"`
	State     string `json:"state"`
}

// CAKeys is GET /internal/ca.
type CAKeys struct {
	UserCAPub string `json:"user_ca_pub"`
	HostCAPub string `json:"host_ca_pub"`
}

// EdgeEvent is POST /internal/events: a hook event that reached the edge
// over HTTP because guestd was unavailable.
type EdgeEvent struct {
	SourceIP string `json:"source_ip"`
	Agent    string `json:"agent"`
	Kind     string `json:"kind"`
	Summary  string `json:"summary"`
	Window   string `json:"window,omitempty"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("api: encode %s: %w", path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return fmt.Errorf("api: %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("api: %s %s: read: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env) // a non-JSON error body still yields a status-coded error below
		if resp.StatusCode == http.StatusNotFound || env.Error.Code == "not_found" {
			return ErrNotFound
		}
		return &APIError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("api: %s %s: decode: %w", method, path, err)
	}
	return nil
}

// Route resolves a login name.
func (c *Client) Route(ctx context.Context, login string) (*Route, error) {
	var r Route
	if err := c.do(ctx, http.MethodGet, "/route?login="+url.QueryEscape(login), nil, &r); err != nil {
		return nil, err
	}
	if r.ProjectID == "" {
		return nil, errors.New("api: route without project_id")
	}
	return &r, nil
}

// Wake resumes a machine that rests and answers once it is awake
// (DECISIONS I-818). Idempotent: a machine already awake is answered
// was_resting false, which is what makes the route cache's stale flag
// harmless. reason is a lower_snake word for the host's log line.
func (c *Client) Wake(ctx context.Context, projectID, reason string) (*WakeResult, error) {
	var out WakeResult
	body := map[string]string{"project_id": projectID, "reason": reason}
	if err := c.do(ctx, http.MethodPost, "/wake", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Revoked lists serials revoked since a time (zero means all).
func (c *Client) Revoked(ctx context.Context, since time.Time) ([]uint64, error) {
	path := "/revoked"
	if !since.IsZero() {
		path += "?since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	}
	var out []uint64
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CA fetches both CA public keys.
func (c *Client) CA(ctx context.Context) (*CAKeys, error) {
	var out CAKeys
	if err := c.do(ctx, http.MethodGet, "/ca", nil, &out); err != nil {
		return nil, err
	}
	if out.UserCAPub == "" || out.HostCAPub == "" {
		return nil, errors.New("api: /internal/ca returned an empty key")
	}
	return &out, nil
}

// ReportSession posts a session open or close. sessionID names the relay,
// so two connections under one certificate are two sessions (I-176).
func (c *Client) ReportSession(ctx context.Context, projectID string, opened bool, serial uint64, sessionID string) error {
	ev := "closed"
	if opened {
		ev = "opened"
	}
	body := map[string]any{"project_id": projectID, "event": ev, "cert_serial": serial, "session_id": sessionID}
	return c.do(ctx, http.MethodPost, "/sessions", body, nil)
}

// Hosts lists registered hosts for wgsync.
func (c *Client) Hosts(ctx context.Context) ([]Host, error) {
	var out []Host
	if err := c.do(ctx, http.MethodGet, "/hosts", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GatewayCert asks for a five-minute certificate for the gateway's key
// with the project id as principal; the result is one authorized_keys
// line.
func (c *Client) GatewayCert(ctx context.Context, publicKeyLine, projectID string) (string, error) {
	var out struct {
		Certificate string `json:"certificate"`
	}
	body := map[string]string{"public_key": publicKeyLine, "project_id": projectID}
	if err := c.do(ctx, http.MethodPost, "/gateway-certs", body, &out); err != nil {
		return "", err
	}
	if out.Certificate == "" {
		return "", errors.New("api: /internal/gateway-certs returned no certificate")
	}
	return out.Certificate, nil
}

// Event forwards a hook event; the api answers 2xx, not_found (no project
// at that address) or invalid.
func (c *Client) Event(ctx context.Context, ev EdgeEvent) error {
	return c.do(ctx, http.MethodPost, "/events", ev, nil)
}
