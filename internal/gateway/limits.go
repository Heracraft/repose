package gateway

import (
	"net"
	"sync"
	"time"
)

// Connection limits of 06-gateway-edge.md §5.2 and the fail2ban-style
// per-source ban of ops/RUNBOOK.md "GatewayAuthSpike". Everything here is
// keyed on the source address in memory only; the address is never logged
// (docs/ops/OBSERVABILITY.md).
//
// A connection passes through two budgets (I-435). Before and during the
// handshake it holds a per-source slot (DefaultMaxAuthPerIP) and one of
// DefaultMaxPreAuth global slots, both checked before any SSH byte is
// sent, and it has DefaultAuthTimeout to authenticate. Once authenticated
// it holds a relay slot (DefaultMaxConns) and one of its user's
// DefaultMaxConnsPerUser. An unauthenticated client can therefore never
// hold a relay slot, and one source cannot hold more than its few
// pre-auth slots however many connections it opens.
const (
	DefaultMaxConns        = 200
	DefaultMaxConnsPerUser = 32
	DefaultMaxPreAuth      = 64
	DefaultMaxAuthPerIP    = 4
	DefaultAuthTimeout     = 10 * time.Second
	DefaultMaxAuthTries    = 3
	// A source with banFailures failures inside banWindow is refused for
	// banDuration.
	banFailures = 20
	banWindow   = 10 * time.Minute
	banDuration = 10 * time.Minute
)

// limiter tracks per-source authentication attempts and failures.
type limiter struct {
	mu       sync.Mutex
	maxAuth  int
	inAuth   map[string]int
	failures map[string][]time.Time
	banned   map[string]time.Time
	clock    func() time.Time
}

func newLimiter(maxAuthPerIP int, clock func() time.Time) *limiter {
	return &limiter{
		maxAuth:  maxAuthPerIP,
		inAuth:   map[string]int{},
		failures: map[string][]time.Time{},
		banned:   map[string]time.Time{},
		clock:    clock,
	}
}

// sourceKey is the address without the port; an IPv6 source is keyed on
// its /64, which one client typically holds whole.
func sourceKey(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return (&net.IPNet{IP: ip.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
	}
	return host
}

// beginAuth reserves an authentication slot for the source. It returns
// false when the source is banned or already has maxAuth connections in
// the authentication phase.
func (l *limiter) beginAuth(src string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	if until, ok := l.banned[src]; ok {
		if now.Before(until) {
			return false
		}
		delete(l.banned, src)
	}
	if l.inAuth[src] >= l.maxAuth {
		return false
	}
	l.inAuth[src]++
	return true
}

// endAuth releases the slot; failed records a failure towards the ban.
func (l *limiter) endAuth(src string, failed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inAuth[src] <= 1 {
		delete(l.inAuth, src)
	} else {
		l.inAuth[src]--
	}
	if !failed {
		delete(l.failures, src)
		return
	}
	now := l.clock()
	kept := l.failures[src][:0]
	for _, t := range l.failures[src] {
		if now.Sub(t) < banWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	if len(kept) >= banFailures {
		l.banned[src] = now.Add(banDuration)
		delete(l.failures, src)
		return
	}
	l.failures[src] = kept
	// Bound the maps under a distributed scan.
	if len(l.failures) > 100000 {
		for k, ts := range l.failures {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= banWindow {
				delete(l.failures, k)
			}
		}
	}
}

// keyedCounter caps the connections held per key (a user id).
type keyedCounter struct {
	mu  sync.Mutex
	n   map[string]int
	max int
}

func newKeyedCounter(max int) *keyedCounter {
	return &keyedCounter{n: map[string]int{}, max: max}
}

func (k *keyedCounter) acquire(key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.max > 0 && k.n[key] >= k.max {
		return false
	}
	k.n[key]++
	return true
}

func (k *keyedCounter) release(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.n[key] <= 1 {
		delete(k.n, key)
		return
	}
	k.n[key]--
}

// connCounter caps a number of connections.
type connCounter struct {
	mu   sync.Mutex
	n    int
	max  int
	peak int
}

func (c *connCounter) acquire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.max > 0 && c.n >= c.max {
		return false
	}
	c.n++
	if c.n > c.peak {
		c.peak = c.n
	}
	return true
}

func (c *connCounter) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n > 0 {
		c.n--
	}
}

func (c *connCounter) open() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}
