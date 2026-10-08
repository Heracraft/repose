package gateway

import (
	"testing"
	"time"
)

func TestLimiterConcurrentAuthPerSource(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newLimiter(4, func() time.Time { return now })
	for i := 0; i < 4; i++ {
		if !l.beginAuth("198.51.100.7") {
			t.Fatalf("attempt %d refused", i)
		}
	}
	if l.beginAuth("198.51.100.7") {
		t.Fatal("fifth concurrent attempt allowed")
	}
	if !l.beginAuth("198.51.100.8") {
		t.Fatal("another source refused")
	}
	l.endAuth("198.51.100.7", authOK)
	if !l.beginAuth("198.51.100.7") {
		t.Fatal("slot not released")
	}
}

func TestLimiterBansAfterTwentyFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newLimiter(4, func() time.Time { return now })
	for i := 0; i < 19; i++ {
		if !l.beginAuth("src") {
			t.Fatalf("refused at failure %d", i)
		}
		l.endAuth("src", authFailed)
	}
	if !l.beginAuth("src") {
		t.Fatal("banned before the 20th failure")
	}
	l.endAuth("src", authFailed)
	if l.beginAuth("src") {
		t.Fatal("not banned after 20 failures")
	}
	now = now.Add(banDuration + time.Second)
	if !l.beginAuth("src") {
		t.Fatal("ban did not expire")
	}
	l.endAuth("src", authOK)
	// A success clears the failure history.
	for i := 0; i < 19; i++ {
		l.beginAuth("src")
		l.endAuth("src", authFailed)
	}
	l.beginAuth("src")
	l.endAuth("src", authOK)
	l.beginAuth("src")
	l.endAuth("src", authFailed)
	if !l.beginAuth("src") {
		t.Fatal("failures before a success counted towards the ban")
	}
}

func TestConnCounter(t *testing.T) {
	c := &connCounter{max: 2}
	a, b := c.acquire(), c.acquire()
	if !a || !b {
		t.Fatal("first two refused")
	}
	if c.acquire() {
		t.Fatal("third accepted over the cap")
	}
	c.release()
	if !c.acquire() {
		t.Fatal("release did not free a slot")
	}
	if c.open() != 2 {
		t.Fatalf("open %d", c.open())
	}
}

// I-599: a refusal of the user's own certificate (a stopped project, one
// whose id changed in a restore) neither counts towards the ban nor
// clears the failures that do.
func TestLimiterRefusalsNeitherCountNorClear(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newLimiter(4, func() time.Time { return now })
	for i := 0; i < 100; i++ {
		l.beginAuth("src")
		if n, banned := l.endAuth("src", authRefused); n != 0 || banned {
			t.Fatalf("refusal %d counted: %d %v", i, n, banned)
		}
	}
	if !l.beginAuth("src") {
		t.Fatal("refusals banned the source")
	}
	l.endAuth("src", authFailed)
	for i := 1; i < banFailures-1; i++ {
		l.beginAuth("src")
		l.endAuth("src", authFailed)
		l.beginAuth("src")
		l.endAuth("src", authRefused)
	}
	l.beginAuth("src")
	if n, banned := l.endAuth("src", authFailed); n != banFailures || !banned {
		t.Fatalf("the 20th counted failure: %d %v", n, banned)
	}
	if l.beginAuth("src") {
		t.Fatal("not banned")
	}
}

func TestLogBucketBoundsAndReportsDrops(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := newLogBucket(1, 3, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if ok, _ := b.take(); !ok {
			t.Fatalf("line %d of the burst dropped", i)
		}
	}
	for i := 0; i < 50; i++ {
		if ok, _ := b.take(); ok {
			t.Fatal("line past the burst written")
		}
	}
	now = now.Add(time.Second)
	if ok, dropped := b.take(); !ok || dropped != 50 {
		t.Fatalf("after a second: %v, dropped %d", ok, dropped)
	}
}
