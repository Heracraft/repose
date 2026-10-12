package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// hangingSealer is a vault that never answers.
type hangingSealer struct{ refreshed chan struct{} }

func (h *hangingSealer) Ready() error { return nil }
func (h *hangingSealer) Refresh(ctx context.Context) error {
	<-ctx.Done()
	close(h.refreshed)
	return ctx.Err()
}
func (h *hangingSealer) Wrap([]byte) ([]byte, string, error) { return nil, "", nil }
func (h *hangingSealer) Unwrap(context.Context, []byte, string) ([]byte, error) {
	return nil, nil
}

// The start's key fetch does not hold the reconcile, and so Hello, back:
// with the vault unreachable the restore's own 90 s unwrap bound is the
// whole wait (host-stg-01, 2026-10-12: the fetch added 30 s).
func TestKeyFetchAtStartDoesNotBlock(t *testing.T) {
	s := &hangingSealer{refreshed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	refreshKeyAtStart(ctx, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if d := time.Since(start); d > time.Second {
		t.Fatalf("refreshKeyAtStart held the start for %s", d)
	}
	cancel()
	select {
	case <-s.refreshed:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch never ended after hostd's context did")
	}
}
