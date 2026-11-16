package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// The browser view's stream route is the api's first route that is not
// behind `authed`: a cookie ticket is what authenticates it, so none of
// `authed`'s per-user buckets run, and until I-1216 the api had no
// per-source limit of any kind. This is the shape the SSH gateway
// already uses (internal/gateway/limits.go, I-435), narrowed to what this
// route needs:
//
//   - a slot per source prefix, held only until the ticket is checked, so
//     one address cannot hold many connections in that phase however many
//     it opens. The ticket is checked *before* the upgrade here, so that
//     phase is short and a shared office address does not run out of
//     slots while people watch their own machines;
//   - a global slot, held for the whole stream, which is the one bound on
//     the total this api will carry;
//   - a failure count per source prefix: 20 refused tickets inside ten
//     minutes and that prefix is refused for ten minutes, the
//     fail2ban-style ban of ops/RUNBOOK.md "GatewayAuthSpike".
//
// Everything here is keyed on the source *prefix* in memory only -- the
// /24 of an IPv4 address, the /64 of an IPv6 one -- and the prefix is the
// only form that ever reaches a log line. A full address is never logged
// (docs/ops/OBSERVABILITY.md).
const (
	viewMaxPerSource = 4
	viewMaxStreams   = 64
	viewBanFailures  = 20
	viewBanWindow    = 10 * time.Minute
	viewBanDuration  = 10 * time.Minute
)

// viewLimiter holds the counters. One per Server.
type viewLimiter struct {
	mu       sync.Mutex
	perMax   int
	max      int
	open     int
	checking map[string]int
	failures map[string][]time.Time
	banned   map[string]time.Time
	clock    func() time.Time
}

func newViewLimiter(perSource, max int, clock func() time.Time) *viewLimiter {
	if clock == nil {
		clock = time.Now
	}
	return &viewLimiter{
		perMax:   perSource,
		max:      max,
		checking: map[string]int{},
		failures: map[string][]time.Time{},
		banned:   map[string]time.Time{},
		clock:    clock,
	}
}

// begin takes a slot for the pre-ticket phase and one of the stream
// slots. The refusal code says which ran out, or that the prefix is
// banned, so an operator reading the counter knows which bound fired.
func (l *viewLimiter) begin(prefix string) (code string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	if until, ok := l.banned[prefix]; ok {
		if now.Before(until) {
			return "forbidden"
		}
		delete(l.banned, prefix)
	}
	if l.perMax > 0 && l.checking[prefix] >= l.perMax {
		return "rate_limited"
	}
	if l.max > 0 && l.open >= l.max {
		return "view_capacity"
	}
	l.checking[prefix]++
	l.open++
	return ""
}

// checked releases the pre-ticket slot; the stream slot stays until done.
func (l *viewLimiter) checked(prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.checking[prefix] <= 1 {
		delete(l.checking, prefix)
		return
	}
	l.checking[prefix]--
}

// done releases the stream slot.
func (l *viewLimiter) done() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open > 0 {
		l.open--
	}
}

// fail counts a refused ticket and bans the prefix at viewBanFailures
// inside viewBanWindow.
func (l *viewLimiter) fail(prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	kept := l.failures[prefix][:0]
	for _, t := range l.failures[prefix] {
		if now.Sub(t) < viewBanWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	l.failures[prefix] = kept
	if len(kept) >= viewBanFailures {
		l.banned[prefix] = now.Add(viewBanDuration)
		delete(l.failures, prefix)
	}
}

// sweep drops the state of prefixes nothing has used lately. SweepLimiters
// calls it with the rate-limit buckets.
func (l *viewLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	for p, until := range l.banned {
		if now.After(until) {
			delete(l.banned, p)
		}
	}
	for p, ts := range l.failures {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= viewBanWindow {
			delete(l.failures, p)
		}
	}
}

// sourcePrefix is the client's address narrowed to the /24 (or /64 for
// IPv6), which is the only form of it that may be logged or kept. The
// address itself comes from the rightmost X-Forwarded-For entry, which
// the edge proxy appended, else the peer -- the same reading
// customerIP does for Polar.
func sourcePrefix(r *http.Request) string {
	cand := ""
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		cand = strings.TrimSpace(parts[len(parts)-1])
	} else if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		cand = host
	}
	ip, err := netip.ParseAddr(cand)
	if err != nil {
		return "unknown"
	}
	bits := 24
	if ip.Is6() && !ip.Is4In6() {
		bits = 64
	}
	p, err := ip.Prefix(bits)
	if err != nil {
		return "unknown"
	}
	return p.String()
}
