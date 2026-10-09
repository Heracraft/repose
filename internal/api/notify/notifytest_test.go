package notify_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/store"
)

// TestOutboxTestSaysWhyNtfyFailed: POST /me/notify-test names why the
// user's ntfy URL failed, leaves out a channel that is off, and never
// echoes the URL (DECISIONS I-622).
func TestOutboxTestSaysWhyNtfyFailed(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   map[string]string
	}{
		{200, map[string]string{"ntfy": "ok"}},
		{403, map[string]string{"ntfy": "error", "ntfy_error": "the server answered 403"}},
		{502, map[string]string{"ntfy": "error", "ntfy_error": "the server answered 502"}},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
		o := notify.New(nil, map[string]notify.Sender{"ntfy": &notify.Ntfy{HTTP: srv.Client()}}, metrics.NewNop(), slog.New(slog.DiscardHandler))
		url := srv.URL + "/secret-topic"
		got := o.Test(context.Background(), &store.User{NtfyURL: &url})
		srv.Close()
		if len(got) != len(tc.want) {
			t.Errorf("status %d: got %v, want %v", tc.status, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("status %d: %s = %q, want %q (all: %v)", tc.status, k, got[k], v, got)
			}
		}
	}
	// An address that is not public is refused before any request.
	url := "http://10.0.0.1/topic"
	o := notify.New(nil, map[string]notify.Sender{"ntfy": &notify.Ntfy{}}, metrics.NewNop(), slog.New(slog.DiscardHandler))
	got := o.Test(context.Background(), &store.User{NtfyURL: &url})
	if got["ntfy_error"] != "the address is not on the public internet" {
		t.Errorf("private address: %v", got)
	}
	if _, ok := got["email"]; ok {
		t.Errorf("email is off and still reported: %v", got)
	}
}
