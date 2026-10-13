package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/db/testdb"
)

// TestRetriedEmailIsByteIdentical (DECISIONS I-691): Resend refuses a
// reused Idempotency-Key whose body differs (409), so a retry of the same
// row sends the same bytes: the unsubscribe link's expiry comes from the
// event, not the clock.
func TestRetriedEmailIsByteIdentical(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	m := metrics.NewNop()
	_, pid := seed(t, pool, "")
	var mu sync.Mutex
	var bodies, keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies, keys = append(bodies, string(b)), append(keys, r.Header.Get("Idempotency-Key"))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadGateway) // as if the first send timed out after Resend took it
		}
	}))
	defer srv.Close()
	u, err := notify.LoadOrCreateUnsubscriber(ctx, newFakePlatformSecrets())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ob := notify.New(pool, map[string]notify.Sender{"email": &notify.Email{APIKey: "k", URL: srv.URL}}, m, log)
	ob.Unsub = u
	ob.Now = func() time.Time { return now }
	if _, _, err := events.New(pool, m, log).Insert(ctx, events.Incoming{ProjectID: mustUUID(pid), TS: now, Kind: "agent_done", Agent: "claude", Summary: "done"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := ob.Once(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := ob.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("%d sends, want 2", len(bodies))
	}
	if bodies[0] != bodies[1] || keys[0] != keys[1] || keys[0] == "" {
		t.Fatalf("the retry differs from the first send:\n%s\n%s", bodies[0], bodies[1])
	}
}

// TestEmail409WithKeyIsRetried: a 409 on a keyed request is Resend's
// "this key is still in flight" or "already used", not a refusal of the
// message, so it is retried rather than marked failed.
func TestEmail409WithKeyIsRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusConflict) }))
	defer srv.Close()
	err := (&notify.Email{APIKey: "k", URL: srv.URL}).Send(context.Background(), notify.Message{EventID: mustUUID("0192f0aa-0000-7000-8000-000000000001"), Kind: "agent_done", Agent: "claude", Project: "todo", Email: "u@example.com"})
	var perm notify.Permanent
	if err == nil || errors.As(err, &perm) {
		t.Fatalf("409 with a key: %v, want a retryable error", err)
	}
}
