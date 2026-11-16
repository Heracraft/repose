package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The stream route's pre-ticket limits, which the api had no peer for
// before the browser view (DECISIONS I-1216): a slot per source prefix
// held until the ticket is checked, a global slot held for the stream,
// and a ban on refused tickets.

func TestViewLimiterSlots(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newViewLimiter(2, 3, func() time.Time { return now })

	// Two at once from one prefix, then that prefix waits; another
	// prefix is unaffected.
	if code := l.begin("203.0.113.0/24"); code != "" {
		t.Fatalf("the first: %q", code)
	}
	if code := l.begin("203.0.113.0/24"); code != "" {
		t.Fatalf("the second: %q", code)
	}
	if code := l.begin("203.0.113.0/24"); code != "rate_limited" {
		t.Fatalf("the third from one prefix: %q", code)
	}
	if code := l.begin("198.51.100.0/24"); code != "" {
		t.Fatalf("another prefix: %q", code)
	}
	// The global cap is the next bound: three are open, and the fourth
	// is refused whatever its prefix.
	if code := l.begin("192.0.2.0/24"); code != "view_capacity" {
		t.Fatalf("past the global cap: %q", code)
	}
	// Releasing the pre-ticket slot frees the prefix but not the stream
	// slot, so the global cap still holds.
	l.checked("203.0.113.0/24")
	if code := l.begin("192.0.2.0/24"); code != "view_capacity" {
		t.Fatalf("the stream slot was freed by checked(): %q", code)
	}
	l.done()
	if code := l.begin("192.0.2.0/24"); code != "" {
		t.Fatalf("after a stream ended: %q", code)
	}
}

func TestViewLimiterBansARefusedPrefix(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newViewLimiter(4, 64, func() time.Time { return now })
	const prefix = "203.0.113.0/24"
	for i := 0; i < viewBanFailures-1; i++ {
		l.fail(prefix)
	}
	if code := l.begin(prefix); code != "" {
		t.Fatalf("under the ban threshold: %q", code)
	}
	l.checked(prefix)
	l.done()
	l.fail(prefix)
	if code := l.begin(prefix); code != "forbidden" {
		t.Fatalf("at the ban threshold: %q", code)
	}
	// Ten minutes later it is allowed again, and the ban state is swept.
	now = now.Add(viewBanDuration + time.Second)
	if code := l.begin(prefix); code != "" {
		t.Fatalf("after the ban: %q", code)
	}
	l.checked(prefix)
	l.done()
	l.sweep()
	if len(l.banned) != 0 || len(l.failures) != 0 {
		t.Fatalf("sweep left %d bans and %d failure lists", len(l.banned), len(l.failures))
	}
	// Failures older than the window do not add up to a ban.
	for i := 0; i < viewBanFailures*2; i++ {
		l.fail(prefix)
		now = now.Add(viewBanWindow / 2)
	}
	if code := l.begin(prefix); code != "" {
		t.Fatalf("failures spread over hours: %q", code)
	}
}

// Only the prefix is ever kept or logged, never the address: the /24 of
// an IPv4 client and the /64 of an IPv6 one, read from the rightmost
// X-Forwarded-For entry the edge proxy appended, else the peer.
func TestSourcePrefixIsOnlyThePrefix(t *testing.T) {
	for _, tc := range []struct{ xff, remote, want string }{
		{"", "203.0.113.45:51000", "203.0.113.0/24"},
		{"198.51.100.9, 203.0.113.45", "10.0.0.1:1", "203.0.113.0/24"},
		{"2001:db8:1:2:3:4:5:6", "10.0.0.1:1", "2001:db8:1:2::/64"},
		{"not-an-address", "10.0.0.1:1", "unknown"},
		{"", "garbage", "unknown"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/browser/stream", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := sourcePrefix(r); got != tc.want {
			t.Errorf("xff %q remote %q: %q, want %q", tc.xff, tc.remote, got, tc.want)
		}
	}
}
