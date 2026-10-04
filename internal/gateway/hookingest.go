package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// Hook ingest limits (docs/workstreams/06-gateway-edge.md §5.7).
const (
	hookBodyCap    = 4 << 10 // 4 KB
	hookRatePerSec = 10      // per source guest address
)

// hookKinds and hookAgents are what a guest may report on this path, the
// same sets the api accepts from a guest (internal/api/events GuestKinds):
// the platform's own kinds (billing, abuse, notifications_paused, ...) are
// written by the api and never come from a guest.
var (
	hookKinds  = map[string]bool{"completed": true, "needs_input": true, "error": true, "agent_message": true}
	hookAgents = map[string]bool{"claude": true, "opencode": true, "codex": true, "gemini": true, "pi": true, "shell": true}
)

// HookIngest is the HTTPS forwarder guests reach over WireGuard when guestd
// is unavailable: it accepts POST /hooks from guest addresses, adds the
// source ip, and forwards to the api's POST /internal/events (DECISIONS
// I-4). It is the secondary path; the primary is guestd over vsock.
type HookIngest struct {
	api   *Client
	log   *slog.Logger
	m     *obsmetrics.GatewayMetrics
	clock func() time.Time

	mu   sync.Mutex
	hits map[string][]time.Time // source ip -> recent request times
}

// NewHookIngest builds the forwarder.
func NewHookIngest(api *Client, log *slog.Logger, m *obsmetrics.GatewayMetrics) *HookIngest {
	if log == nil {
		log = obs.Nop(obs.ComponentGateway)
	}
	return &HookIngest{api: api, log: log, m: m, clock: time.Now, hits: map[string][]time.Time{}}
}

// Handler serves POST /hooks and GET /healthz.
func (h *HookIngest) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("POST /hooks", h.postHook)
	return mux
}

// hookBody is what a guest POSTs; source_ip is added by the gateway, never
// trusted from the body.
type hookBody struct {
	Agent   string `json:"agent"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Window  string `json:"window"`
}

func (h *HookIngest) result(result string) {
	if h.m != nil {
		h.m.HookEventsTotal.WithLabelValues(result).Inc()
	}
}

func (h *HookIngest) postHook(w http.ResponseWriter, r *http.Request) {
	src := sourceIP(r.RemoteAddr)
	// nftables limits the source to 10.64.0.0/12; this is defence in depth.
	if !isGuestAddr(src) {
		h.result(HookRejected)
		http.Error(w, "hooks accept guest sources only", http.StatusForbidden)
		return
	}
	if !h.allow(src) {
		h.result(HookRateLimited)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	var body hookBody
	dec := json.NewDecoder(io.LimitReader(r.Body, hookBodyCap))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		h.result(HookRejected)
		http.Error(w, "invalid hook body", http.StatusBadRequest)
		return
	}
	if !hookKinds[body.Kind] {
		h.result(HookRejected)
		http.Error(w, "unknown kind", http.StatusBadRequest)
		return
	}
	if body.Agent != "" && !hookAgents[body.Agent] {
		h.result(HookRejected)
		http.Error(w, "unknown agent", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	err := h.api.Event(ctx, EdgeEvent{SourceIP: src, Agent: body.Agent, Kind: body.Kind, Summary: body.Summary, Window: body.Window})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.result(HookNotFound)
			http.Error(w, "no project at that address", http.StatusNotFound)
			return
		}
		h.result(HookAPIError)
		// The source address is a guest ip; log the kind and the result, never the summary.
		h.log.Warn("hook forward failed", "event", "route_fail", "reason", "events", "kind", body.Kind, "err", err.Error())
		http.Error(w, "forwarding failed", http.StatusBadGateway)
		return
	}
	h.result(HookForwarded)
	w.WriteHeader(http.StatusAccepted)
}

// allow enforces hookRatePerSec per source ip over a one-second window.
func (h *HookIngest) allow(src string) bool {
	now := h.clock()
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.hits[src][:0]
	for _, t := range h.hits[src] {
		if now.Sub(t) < time.Second {
			kept = append(kept, t)
		}
	}
	if len(kept) >= hookRatePerSec {
		h.hits[src] = kept
		return false
	}
	h.hits[src] = append(kept, now)
	if len(h.hits) > 100000 { // bound under a scan
		for k, ts := range h.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= time.Second {
				delete(h.hits, k)
			}
		}
	}
	return true
}

func sourceIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// guestNet is 10.64.0.0/12, the guest address space (host-conventions.md).
var guestNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("10.64.0.0/12")
	return n
}()

func isGuestAddr(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	return parsed != nil && guestNet.Contains(parsed)
}
