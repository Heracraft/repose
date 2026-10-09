package clirelease

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchReadsTheTagFromTheRedirect(t *testing.T) {
	loc := "https://github.com/heracraft/repose/releases/tag/v0.1.32"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", loc)
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	l := New(srv.URL, nil)
	if l.Version() != "" {
		t.Fatalf("before a read: %q", l.Version())
	}
	if err := l.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l.Version() != "v0.1.32" {
		t.Fatalf("Version = %q", l.Version())
	}
	// A bad answer keeps the last good release.
	loc = "https://github.com/heracraft/repose/releases/tag/nightly"
	if err := l.Fetch(context.Background()); err == nil {
		t.Fatal("a tag that is not vX.Y.Z was taken")
	}
	if l.Version() != "v0.1.32" {
		t.Fatalf("after a bad read: %q", l.Version())
	}
	var none *Latest
	if none.Version() != "" {
		t.Fatal("nil Latest names a release")
	}
}
