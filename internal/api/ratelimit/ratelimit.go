// Package ratelimit is an in-memory token bucket per key (user id), one
// replica (05-control-plane-api.md §5.12). Limits: 60/min general, 10/min
// POST /certs, 5/min PUT /config.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Limiter is a set of buckets sharing one rate.
type Limiter struct {
	perMinute float64
	burst     float64
	mu        sync.Mutex
	buckets   map[string]*bucket
	now       func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New makes a limiter allowing perMinute requests per key, with a burst of
// the same size.
func New(perMinute int) *Limiter {
	return &Limiter{perMinute: float64(perMinute), burst: float64(perMinute), buckets: map[string]*bucket{}, now: time.Now}
}

// NewPerHour makes a limiter allowing perHour requests per key, with a
// burst of the same size. The browser view's mint is 10 an hour
// (DECISIONS I-1216), which is not a whole number of requests a minute.
func NewPerHour(perHour int) *Limiter {
	return &Limiter{perMinute: float64(perHour) / 60, burst: float64(perHour), buckets: map[string]*bucket{}, now: time.Now}
}

// Allow takes a token for key; when refused it returns how long until the
// next token.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Minutes()
	b.tokens = math.Min(l.burst, b.tokens+elapsed*l.perMinute)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	need := (1 - b.tokens) / l.perMinute
	return false, time.Duration(math.Ceil(need*60)) * time.Second
}

// Sweep drops buckets idle for longer than age.
func (l *Limiter) Sweep(age time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := l.now().Add(-age)
	for k, b := range l.buckets {
		if b.last.Before(cut) {
			delete(l.buckets, k)
		}
	}
}
