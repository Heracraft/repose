// Package webhook is the edge's public webhook path
// (docs/features/webhooks.md, DECISIONS I-1110): one hostname, routed by
// path, with an in-memory route table fed by the api.
//
// A wrong token is a 404 from the table: no api call, no wake, no dial.
// A hit reaches the app through the gateway's own SSH connection to the
// guest's sshd and a direct-tcpip channel to 127.0.0.1:<port>, so the
// guest gets no new inbound port and the host's nftables do not change.
// The edge stores nothing: body bytes stream through, and what is kept
// is a delivery's route id, status, outcome, duration and byte counts.
package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The limits of docs/features/webhooks.md.
const (
	// MaxRequestBytes is the body the edge forwards; a Content-Length
	// over it is 413 before anything is woken or dialled.
	MaxRequestBytes = 1 << 20
	// MaxReplyBytes is the reply body passed back.
	MaxReplyBytes = 64 << 10
	// ReplyTimeout is the last request byte to the first reply byte.
	// Svix's figure, above every other provider's documented one.
	ReplyTimeout = 30 * time.Second
	// RoutesRefresh is how often the edge asks for the table.
	RoutesRefresh = 5 * time.Second
	// StateTTL is how long a project's state is cached, the SSH
	// gateway's RouteTTL.
	StateTTL = 5 * time.Second
	// StaleAfter is how long the table keeps serving after the last
	// successful refresh, as the revocation cache does.
	StaleAfter = time.Hour
	// DeliveryFlush is how often the delivery batch goes to the api.
	DeliveryFlush = 10 * time.Second
	// DeliveryMaxAge is how long an unsent batch is kept before it is
	// dropped: a count is worth less than unbounded memory.
	DeliveryMaxAge = 10 * time.Minute
	// IdleReuse is how long a project's SSH connection is kept for the
	// next delivery.
	IdleReuse = 30 * time.Second
	// WakeBudget is how long a delivery waits for a frozen machine.
	WakeBudget = 8 * time.Second
)

// Route is one row of the edge's table. It carries the token's hash,
// never the token.
type Route struct {
	RouteID    string `json:"route_id"`
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	Port       int    `json:"port"`
	Path       string `json:"path"`
	AllowStart bool   `json:"allow_start"`
	TokenHash  string `json:"token_sha256"`
}

// RouteTable is GET /internal/webhook-routes.
type RouteTable struct {
	Version    string   `json:"version"`
	Routes     []Route  `json:"routes"`
	Tombstones []string `json:"tombstones"`
	Unchanged  bool     `json:"unchanged"`
}

// ProjectState is the part of GET /internal/route a delivery reads.
type ProjectState struct {
	ProjectID string `json:"project_id"`
	// Login is <slug>.<handle>, the principal the guest's host
	// certificate carries (I-42). A delivery has no login of its own
	// and still checks the host key against it.
	Login           string `json:"login"`
	GuestIP         string `json:"guest_ip"`
	State           string `json:"state"`
	HostUnreachable bool   `json:"host_unreachable"`
	Resting         bool   `json:"resting"`
}

// Delivery is one record for POST /internal/webhook-deliveries.
type Delivery struct {
	RouteID    string    `json:"route_id"`
	At         time.Time `json:"at"`
	Status     int       `json:"status"`
	Outcome    string    `json:"outcome"`
	DurationMs int       `json:"duration_ms"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

// API is the part of the api the webhook path uses. The gateway's client
// satisfies it.
type API interface {
	WebhookRoutes(ctx context.Context, since string) (*RouteTable, error)
	ProjectState(ctx context.Context, projectID string) (*ProjectState, error)
	WebhookWake(ctx context.Context, projectID string) error
	WebhookStart(ctx context.Context, projectID, routeID string) (started bool, reason string, err error)
	ReportWebhookDeliveries(ctx context.Context, batch []Delivery) error
}

// Dialer opens a TCP stream to a port inside a guest, through the
// gateway's own SSH connection to that guest's sshd.
type Dialer interface {
	DialGuestPort(ctx context.Context, projectID, login, guestIP string, port int) (net.Conn, error)
}

// HashToken is the lookup form of a token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Lookup is what the table says about a token hash.
type Lookup int

const (
	// Unknown: no route and no tombstone. 404.
	Unknown Lookup = iota
	// Gone: a token that existed and was rotated, removed or destroyed
	// with its project, inside the 30-day window. 410.
	Gone
	// Found: a live route.
	Found
)

// Table is the edge's in-memory route table, refreshed from the api.
type Table struct {
	mu         sync.RWMutex
	byHash     map[string]Route
	tombstones map[string]bool
	version    string
	lastOK     time.Time
	primed     bool
	clock      func() time.Time
}

// NewTable builds an empty table.
func NewTable(clock func() time.Time) *Table {
	if clock == nil {
		clock = time.Now
	}
	return &Table{byHash: map[string]Route{}, tombstones: map[string]bool{}, clock: clock}
}

// Refresh asks the api for the table and installs it. An unchanged
// answer costs one round trip and no allocation.
func (t *Table) Refresh(ctx context.Context, api API) error {
	t.mu.RLock()
	since := t.version
	t.mu.RUnlock()
	tbl, err := api.WebhookRoutes(ctx, since)
	if err != nil {
		return err
	}
	now := t.clock()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastOK, t.primed = now, true
	// An unchanged answer carries no rows, so installing it would empty
	// the table. The api sends it only for the version the edge already
	// holds; it is refused here whatever version it names, because
	// routing nothing is the worse of the two mistakes.
	if tbl.Unchanged {
		return nil
	}
	byHash := make(map[string]Route, len(tbl.Routes))
	for _, r := range tbl.Routes {
		byHash[strings.ToLower(r.TokenHash)] = r
	}
	tombs := make(map[string]bool, len(tbl.Tombstones))
	for _, h := range tbl.Tombstones {
		tombs[strings.ToLower(h)] = true
	}
	t.byHash, t.tombstones, t.version = byHash, tombs, tbl.Version
	return nil
}

// Lookup answers for a token hash. A live route wins over a tombstone:
// a hash cannot be both, and if the api ever sent both the live row is
// the one that routes.
func (t *Table) Lookup(hash string) (Route, Lookup) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if r, ok := t.byHash[hash]; ok {
		return r, Found
	}
	if t.tombstones[hash] {
		return Route{}, Gone
	}
	return Route{}, Unknown
}

// Len is the number of live routes.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.byHash)
}

// Stale reports whether the table has gone longer than StaleAfter
// without a successful refresh, or was never filled.
func (t *Table) Stale() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return !t.primed || t.clock().Sub(t.lastOK) > StaleAfter
}

// parsePath splits `/<project_id>/<route>/<token>[/rest]`. rest keeps its
// leading slash, or is empty. ok is false for anything else, which is a
// 404 before any lookup.
func parsePath(p string) (projectID, name, token, rest string, ok bool) {
	if !strings.HasPrefix(p, "/") {
		return "", "", "", "", false
	}
	parts := strings.SplitN(p[1:], "/", 4)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", "", false
	}
	if len(parts) == 4 {
		rest = "/" + parts[3]
	}
	return parts[0], parts[1], parts[2], rest, true
}

// hopByHop are the fields RFC 9110 §7.6.1 requires a proxy to drop.
var hopByHop = map[string]bool{
	"connection": true, "keep-alive": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true, "proxy-authenticate": true,
	"proxy-authorization": true, "proxy-connection": true,
}

// replyTypes are the reply bodies the edge passes back: enough for
// Slack's challenge, Discord's answers and TwiML, and too little for a
// page a browser would render.
var replyTypes = map[string]bool{
	"application/json":                  true,
	"text/plain":                        true,
	"application/xml":                   true,
	"text/xml":                          true,
	"application/x-www-form-urlencoded": true,
}

// allowedReplyType reports whether a Content-Type may carry its body
// back. The parameters (`; charset=utf-8`) are ignored.
func allowedReplyType(ct string) bool {
	base, _, _ := strings.Cut(ct, ";")
	return replyTypes[strings.ToLower(strings.TrimSpace(base))]
}

// sourcePrefix is the /24 or /48 of an address, which is what the SSH
// gateway logs and all a webhook line may carry of a sender.
func sourcePrefix(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return net.IP(append(append([]byte{}, v4[:3]...), 0)).String() + "/24"
	}
	return net.IP(append(append([]byte{}, ip[:6]...), make([]byte, 10)...)).String() + "/48"
}

// writePlain is every answer the edge writes of its own: a short text
// line with no HTML, no cookie and nothing a browser renders.
func writePlain(w http.ResponseWriter, status int, body string, retryAfter int) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if retryAfter > 0 {
		h.Set("Retry-After", itoa(retryAfter))
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
