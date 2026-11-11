package webhook

import (
	"sync"
	"time"
)

// The rates of docs/features/webhooks.md. They are per route and per
// project, not per source: providers send from a few shared addresses,
// so a per-source cap would let one tenant's Stripe burst throttle every
// other tenant's. The per-source limits apply only to unknown tokens.
const (
	RouteRate       = 10
	RouteBurst      = 50
	ProjectRate     = 30
	ProjectBurst    = 60
	ProjectInflight = 8
	// UnknownRate caps 404s from one source address: a scan.
	UnknownRate  = 20
	UnknownBurst = 40
)

// bucket is a token bucket that refills at rate a second up to burst.
type bucket struct {
	tokens float64
	last   time.Time
}

// limiter holds one bucket per key and drops the ones nothing has used
// for a while, so a scan cannot grow it without bound.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
	clock   func() time.Time
}

func newLimiter(rate, burst int, clock func() time.Time) *limiter {
	if clock == nil {
		clock = time.Now
	}
	return &limiter{buckets: map[string]*bucket{}, rate: float64(rate), burst: float64(burst), clock: clock}
}

// Allow takes one token for key and reports whether there was one.
func (l *limiter) Allow(key string) bool {
	now := l.clock()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) > 50000 {
			l.sweep(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if d := now.Sub(b.last); d > 0 {
		b.tokens += d.Seconds() * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops every bucket that is full again, which is every key that
// has been quiet for burst/rate seconds. Called under the lock.
func (l *limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// inflight caps how many deliveries one project may have in the air.
type inflight struct {
	mu    sync.Mutex
	n     map[string]int
	limit int
}

func newInflight(limit int) *inflight { return &inflight{n: map[string]int{}, limit: limit} }

// Acquire takes a slot for projectID, or reports false.
func (f *inflight) Acquire(projectID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n[projectID] >= f.limit {
		return false
	}
	f.n[projectID]++
	return true
}

// Release gives the slot back.
func (f *inflight) Release(projectID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n[projectID] <= 1 {
		delete(f.n, projectID)
		return
	}
	f.n[projectID]--
}

// Total is how many deliveries are in the air across every project.
func (f *inflight) Total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, v := range f.n {
		n += v
	}
	return n
}
