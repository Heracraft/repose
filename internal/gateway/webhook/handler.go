package webhook

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// Config configures a Handler.
type Config struct {
	API    API
	Dial   Dialer
	Table  *Table
	Log    *slog.Logger
	Metric *obsmetrics.GatewayMetrics
	Clock  func() time.Time
	// Host is the public hostname, for the X-Forwarded-Host and
	// Repose-Webhook-URL headers Twilio's check needs. The edge gets it
	// from WEBHOOK_HOST, which is the one place the name is configured
	// for this component; empty falls back to the Host the sender used,
	// rather than a second copy of the name in this package.
	Host string
}

// Handler serves the webhook hostname on the edge's :443.
type Handler struct {
	cfg      Config
	clock    func() time.Time
	routeLim *limiter
	projLim  *limiter
	unkLim   *limiter
	busy     *inflight
	states   *stateCache
	batch    *batcher
}

// New builds a handler. It does not start the refresh loop; Run does.
func New(cfg Config) *Handler {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Table == nil {
		cfg.Table = NewTable(cfg.Clock)
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Handler{
		cfg: cfg, clock: cfg.Clock,
		routeLim: newLimiter(RouteRate, RouteBurst, cfg.Clock),
		projLim:  newLimiter(ProjectRate, ProjectBurst, cfg.Clock),
		unkLim:   newLimiter(UnknownRate, UnknownBurst, cfg.Clock),
		busy:     newInflight(ProjectInflight),
		states:   newStateCache(cfg.Clock),
		batch:    newBatcher(cfg.Clock),
	}
}

// Prime fetches the table once, before the listener accepts.
func (h *Handler) Prime(ctx context.Context) error { return h.cfg.Table.Refresh(ctx, h.cfg.API) }

// Run keeps the table fresh and sends the delivery batches until ctx
// ends. The last batch goes on the way out.
func (h *Handler) Run(ctx context.Context) {
	routes := time.NewTicker(RoutesRefresh)
	defer routes.Stop()
	flush := time.NewTicker(DeliveryFlush)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			h.flush(sctx)
			cancel()
			return
		case <-routes.C:
			if err := h.cfg.Table.Refresh(ctx, h.cfg.API); err != nil {
				h.cfg.Log.Warn("webhook route table not refreshed", "event", "webhook_table_fail", "err", err.Error())
				continue
			}
			if h.cfg.Metric != nil {
				h.cfg.Metric.WebhookRoutes.Set(float64(h.cfg.Table.Len()))
			}
		case <-flush.C:
			h.flush(ctx)
		}
	}
}

func (h *Handler) flush(ctx context.Context) {
	batch := h.batch.take()
	if len(batch) == 0 {
		return
	}
	if err := h.cfg.API.ReportWebhookDeliveries(ctx, batch); err != nil {
		h.cfg.Log.Warn("webhook deliveries not reported", "event", "webhook_report_fail", "count", len(batch), "err", err.Error())
		h.batch.putBack(batch)
	}
}

// delivery is one request's bookkeeping. Nothing in it is a path, a
// query, a header value or a body byte.
type delivery struct {
	routeID  string
	project  string
	started  time.Time
	bytesIn  int64
	bytesOut int64
	status   int
	outcome  string
	prefix   string
}

// ServeHTTP is the whole public path. Every exit goes through finish, so
// no delivery leaves without a log line and a record.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d := &delivery{started: h.clock(), prefix: sourcePrefix(r.RemoteAddr)}
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	pid, name, token, rest, ok := parsePath(r.URL.EscapedPath())
	if !ok {
		h.refuse(w, d, http.StatusNotFound, "not_found", "repose: no such webhook route", 0)
		return
	}
	route, found := h.cfg.Table.Lookup(HashToken(token))
	switch {
	case found == Gone:
		d.project = route.ProjectID
		h.refuse(w, d, http.StatusGone, "gone", "repose: this webhook URL was removed or rotated", 0)
		return
	case found == Unknown || route.ProjectID != pid || route.Name != name:
		// Nothing has called the api or touched a host. The per-source
		// rate limit is on this path alone, which is the scan case.
		if !h.unkLim.Allow(d.prefix) {
			h.refuse(w, d, http.StatusTooManyRequests, "rate_limited", "repose: too many unknown webhook URLs from your address", 60)
			return
		}
		h.refuse(w, d, http.StatusNotFound, "not_found", "repose: no such webhook route", 0)
		return
	}
	d.routeID, d.project = route.RouteID, route.ProjectID
	if !h.routeLim.Allow(route.RouteID) || !h.projLim.Allow(route.ProjectID) {
		h.refuse(w, d, http.StatusTooManyRequests, "rate_limited", "repose: too many deliveries; retry", 1)
		return
	}
	if !h.busy.Acquire(route.ProjectID) {
		h.refuse(w, d, http.StatusTooManyRequests, "rate_limited", "repose: too many deliveries at once; retry", 1)
		return
	}
	defer func() {
		h.busy.Release(route.ProjectID)
		if h.cfg.Metric != nil {
			h.cfg.Metric.WebhookInflight.Set(float64(h.busy.Total()))
		}
	}()
	if h.cfg.Metric != nil {
		h.cfg.Metric.WebhookInflight.Set(float64(h.busy.Total()))
	}
	// Before any wake or dial: a body the edge will not forward costs
	// the tenant's machine nothing.
	if r.ContentLength > MaxRequestBytes {
		h.refuse(w, d, http.StatusRequestEntityTooLarge, "too_large", "repose: a webhook body is at most 1 MiB", 0)
		return
	}
	state, err := h.states.lookup(r.Context(), h.cfg.API, route.ProjectID)
	if err != nil {
		h.refuse(w, d, http.StatusServiceUnavailable, "unavailable", "repose: the machine cannot be reached right now", 30)
		return
	}
	outcome := "delivered"
	switch {
	case state.HostUnreachable:
		h.refuse(w, d, http.StatusServiceUnavailable, "unavailable", "repose: the machine's server cannot be reached right now", 300)
		return
	case state.State == "stopped":
		h.stopped(w, r, d, route)
		return
	case state.State == "error":
		h.refuse(w, d, http.StatusServiceUnavailable, "unavailable", "repose: the machine is in error", 300)
		return
	case state.State != "running":
		// creating, building, starting, stopping, restoring, destroying
		h.refuse(w, d, http.StatusServiceUnavailable, "unavailable", "repose: the machine is "+state.State, 30)
		return
	case state.Resting:
		woke := h.clock()
		if err := h.cfg.API.WebhookWake(r.Context(), route.ProjectID); err != nil {
			h.cfg.Log.Warn("machine did not wake for a delivery", "event", "guest_wake_fail",
				"project_id", route.ProjectID, "route_id", route.RouteID, "err", err.Error())
			h.refuse(w, d, http.StatusServiceUnavailable, "wake_failed", "repose: the machine is frozen and did not wake", 30)
			return
		}
		h.states.invalidate(route.ProjectID)
		if h.cfg.Metric != nil {
			h.cfg.Metric.WebhookWakeSeconds.Observe(h.clock().Sub(woke).Seconds())
		}
		outcome = "woke"
		if s2, err := h.states.lookup(r.Context(), h.cfg.API, route.ProjectID); err == nil {
			state = s2
		}
	}
	if state.GuestIP == "" {
		h.refuse(w, d, http.StatusServiceUnavailable, "unavailable", "repose: repose has no address for the machine", 300)
		return
	}
	h.forward(w, r, d, route, state, rest, outcome)
}

// stopped answers a delivery to a machine that is not running, and asks
// the api to start it when the route may (DECISIONS I-1114). The 503
// goes out either way: a start takes longer than every provider's
// timeout, so a held request would fail at the provider and tie up a
// connection here.
func (h *Handler) stopped(w http.ResponseWriter, r *http.Request, d *delivery, route Route) {
	if !route.AllowStart {
		h.refuse(w, d, http.StatusServiceUnavailable, "stopped", "repose: the machine is stopped", 3600)
		return
	}
	started, _, err := h.cfg.API.WebhookStart(r.Context(), route.ProjectID, route.RouteID)
	switch {
	case err != nil:
		h.refuse(w, d, http.StatusServiceUnavailable, "unavailable", "repose: the machine is stopped and could not be started", 60)
	case started:
		h.states.invalidate(route.ProjectID)
		h.refuse(w, d, http.StatusServiceUnavailable, "started", "repose: the machine is stopped and is starting", 60)
	default:
		h.refuse(w, d, http.StatusServiceUnavailable, "start_refused", "repose: the machine is stopped", 60)
	}
}

// forward writes the request into the guest and streams the reply back.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, d *delivery, route Route, state *ProjectState, rest, outcome string) {
	conn, err := h.cfg.Dial.DialGuestPort(r.Context(), route.ProjectID, state.Login, state.GuestIP, route.Port)
	if err != nil {
		h.refuse(w, d, http.StatusBadGateway, "no_listener",
			"repose: nothing is listening on port "+strconv.Itoa(route.Port), 0)
		return
	}
	defer func() { _ = conn.Close() }()
	body, n, err := h.requestBody(r)
	if err != nil {
		h.refuse(w, d, http.StatusRequestEntityTooLarge, "too_large", "repose: a webhook body is at most 1 MiB", 0)
		return
	}
	d.bytesIn = n
	head := h.requestHead(r, route, rest, n)
	deadline := h.clock().Add(ReplyTimeout)
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(head); err != nil {
		h.refuse(w, d, http.StatusBadGateway, "no_listener", "repose: the app closed the connection", 0)
		return
	}
	if n > 0 {
		if _, err := io.Copy(conn, body); err != nil {
			h.refuse(w, d, http.StatusBadGateway, "no_listener", "repose: the app closed the connection", 0)
			return
		}
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			h.refuse(w, d, http.StatusBadGateway, "no_listener", "repose: the app closed the connection", 0)
			return
		}
		h.refuse(w, d, http.StatusGatewayTimeout, "timeout", "repose: the app did not answer in 30 s", 0)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	h.writeReply(w, d, resp, outcome)
}

// requestBody is the body to forward and its length. A body with a known
// length streams; a chunked one is read into memory up to the cap, since
// Transfer-Encoding is hop-by-hop and the guest gets a Content-Length.
func (h *Handler) requestBody(r *http.Request) (io.Reader, int64, error) {
	if r.Body == nil {
		return nil, 0, nil
	}
	if r.ContentLength >= 0 {
		return io.LimitReader(r.Body, r.ContentLength), r.ContentLength, nil
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r.Body, MaxRequestBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if n > MaxRequestBytes {
		return nil, 0, errTooLarge
	}
	return &buf, n, nil
}

var errTooLarge = errors.New("webhook: body over the cap")

// requestHead is the request line and the headers the guest sees.
//
// Header order across different field names is not kept: Go's server
// parses a request into a map, so the order it arrived in is already
// gone by the time this runs. RFC 9110 §5.3 makes the order of
// different field names insignificant, and no provider in
// docs/features/webhooks.md signs it; duplicates of one name keep their
// order and their exact bytes, which is what a signature over a header
// needs.
func (h *Handler) requestHead(r *http.Request, route Route, rest string, length int64) []byte {
	public := h.cfg.Host
	if public == "" {
		public = r.Host
	}
	target := route.Path
	if target == "" {
		target = "/"
	}
	if rest != "" {
		target = strings.TrimSuffix(target, "/") + rest
	}
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", r.Method, target)
	// Host is localhost:<port> on purpose: Vite answers only localhost,
	// .localhost and IP addresses by default, so a dev server would
	// refuse the public name. X-Forwarded-Host carries it instead.
	fmt.Fprintf(&b, "Host: localhost:%d\r\n", route.Port)
	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		lk := strings.ToLower(k)
		if hopByHop[lk] || lk == "host" || lk == "content-length" || strings.HasPrefix(lk, "repose-") {
			continue
		}
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		for _, v := range r.Header[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, sanitiseHeader(v))
		}
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n", length)
	fmt.Fprintf(&b, "X-Forwarded-For: %s\r\n", hostOf(r.RemoteAddr))
	b.WriteString("X-Forwarded-Proto: https\r\n")
	fmt.Fprintf(&b, "X-Forwarded-Host: %s\r\n", public)
	fmt.Fprintf(&b, "Repose-Webhook: %s\r\n", route.Name)
	fmt.Fprintf(&b, "Repose-Delivery: %s\r\n", uuid.Must(uuid.NewV7()).String())
	// The URL Twilio's check takes as an argument. It holds the token,
	// and it reaches only the app, inside the user's own machine.
	fmt.Fprintf(&b, "Repose-Webhook-URL: https://%s%s\r\n", public, requestURLPath(r))
	b.WriteString("Connection: close\r\n\r\n")
	return b.Bytes()
}

func requestURLPath(r *http.Request) string {
	p := r.URL.EscapedPath()
	if q := r.URL.RawQuery; q != "" {
		return p + "?" + q
	}
	return p
}

// sanitiseHeader removes CR and LF from a value a sender chose, so no
// header can inject a line into the request the guest reads.
func sanitiseHeader(v string) string {
	if !strings.ContainsAny(v, "\r\n") {
		return v
	}
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}

func hostOf(remoteAddr string) string {
	if i := strings.LastIndexByte(remoteAddr, ':'); i > 0 {
		return strings.Trim(remoteAddr[:i], "[]")
	}
	return remoteAddr
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// writeReply applies the reply rules of docs/features/webhooks.md: no
// redirect, no cookie, no HTML, 64 KiB.
func (h *Handler) writeReply(w http.ResponseWriter, d *delivery, resp *http.Response, outcome string) {
	out := w.Header()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Every provider counts a 3xx as a failure, and a passed
		// redirect would make this hostname an open redirect.
		h.refuse(w, d, http.StatusBadGateway, "body_dropped", "repose: the app answered with a redirect", 0)
		return
	}
	for k, vs := range resp.Header {
		lk := strings.ToLower(k)
		if hopByHop[lk] || lk == "set-cookie" || lk == "content-length" || lk == "location" ||
			lk == "content-security-policy" || lk == "cache-control" || lk == "x-content-type-options" {
			continue
		}
		for _, v := range vs {
			out.Add(k, sanitiseHeader(v))
		}
	}
	out.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	out.Set("X-Content-Type-Options", "nosniff")
	out.Set("Cache-Control", "no-store")
	keep := allowedReplyType(resp.Header.Get("Content-Type"))
	if !keep {
		out.Set("Repose-Body-Dropped", "type")
		out.Del("Content-Type")
		outcome = "body_dropped"
	}
	w.WriteHeader(resp.StatusCode)
	d.status = resp.StatusCode
	if keep {
		n, _ := io.Copy(w, io.LimitReader(resp.Body, MaxReplyBytes))
		d.bytesOut = n
	}
	d.outcome = outcome
	h.finish(d)
}

// refuse is every answer the edge writes itself.
func (h *Handler) refuse(w http.ResponseWriter, d *delivery, status int, outcome, body string, retryAfter int) {
	writePlain(w, status, body, retryAfter)
	d.status, d.outcome = status, outcome
	h.finish(d)
}

// finish logs the line and queues the record. The line carries ids,
// a status, an outcome, a duration and byte counts, and never the path
// (it holds the token), the query, a header value or a body byte
// (docs/ops/OBSERVABILITY.md).
func (h *Handler) finish(d *delivery) {
	ms := h.clock().Sub(d.started).Milliseconds()
	if h.cfg.Metric != nil {
		h.cfg.Metric.WebhookRequestsTotal.WithLabelValues(d.outcome).Inc()
	}
	attrs := []any{"event", "webhook_delivery", "status", d.status, "outcome", d.outcome,
		"ms", ms, "bytes_in", d.bytesIn, "bytes_out", d.bytesOut, "source_prefix", d.prefix}
	if d.project != "" {
		attrs = append(attrs, "project_id", d.project)
	}
	if d.routeID != "" {
		attrs = append(attrs, "route_id", d.routeID)
	}
	h.cfg.Log.Info("webhook delivery", attrs...)
	if d.routeID == "" {
		return // an unknown token belongs to no route; there is nothing to record
	}
	h.batch.add(Delivery{RouteID: d.routeID, At: h.clock().UTC(), Status: d.status, Outcome: d.outcome,
		DurationMs: int(ms), BytesIn: d.bytesIn, BytesOut: d.bytesOut})
}

// stateCache holds a project's state for StateTTL, as the SSH gateway's
// route cache does, so a burst of deliveries is one lookup.
type stateCache struct {
	mu      sync.Mutex
	entries map[string]stateEntry
	clock   func() time.Time
}

type stateEntry struct {
	state *ProjectState
	at    time.Time
}

func newStateCache(clock func() time.Time) *stateCache {
	return &stateCache{entries: map[string]stateEntry{}, clock: clock}
}

func (c *stateCache) lookup(ctx context.Context, api API, projectID string) (*ProjectState, error) {
	now := c.clock()
	c.mu.Lock()
	if e, ok := c.entries[projectID]; ok && now.Sub(e.at) < StateTTL {
		c.mu.Unlock()
		return e.state, nil
	}
	c.mu.Unlock()
	s, err := api.ProjectState(ctx, projectID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[projectID] = stateEntry{state: s, at: now}
	if len(c.entries) > 10000 {
		for k, e := range c.entries {
			if now.Sub(e.at) >= StateTTL {
				delete(c.entries, k)
			}
		}
	}
	c.mu.Unlock()
	return s, nil
}

func (c *stateCache) invalidate(projectID string) {
	c.mu.Lock()
	delete(c.entries, projectID)
	c.mu.Unlock()
}

// batcher holds the delivery records between flushes.
type batcher struct {
	mu    sync.Mutex
	items []Delivery
	since time.Time
	clock func() time.Time
}

func newBatcher(clock func() time.Time) *batcher { return &batcher{clock: clock} }

func (b *batcher) add(d Delivery) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) == 0 {
		b.since = b.clock()
	}
	// A batch the api has not taken for DeliveryMaxAge is dropped: the
	// counts are worth less than unbounded memory on the edge.
	if b.clock().Sub(b.since) > DeliveryMaxAge {
		b.items, b.since = nil, b.clock()
	}
	b.items = append(b.items, d)
}

func (b *batcher) take() []Delivery {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.items
	b.items = nil
	return out
}

func (b *batcher) putBack(items []Delivery) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clock().Sub(b.since) > DeliveryMaxAge {
		return
	}
	b.items = append(items, b.items...)
}
