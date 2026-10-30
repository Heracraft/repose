package ch

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// An API call must not leave its connection open. Cloud Hypervisor's
// API accepts ten at once (micro_http MAX_CONNECTIONS) and reaps none,
// and hostd built a fresh http.Transport per call and dropped it: the
// idle connection stayed, because the discarded Transport is never
// collected while its persistConn's readLoop holds a reference. So
// about ten calls -- a handful of rest/wake cycles, or one failed
// wake's poll loop -- made a guest permanently unreachable, and the
// recorded recovery was to kill the hypervisor and lose the tenant's
// session (I-920).
func TestAPICallsDoNotLeakConnections(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "ch.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip(err)
	}
	defer func() { _ = ln.Close() }()
	var open, total int64
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"state":"Running"}`))
		}),
		ConnState: func(c net.Conn, s http.ConnState) {
			switch s {
			case http.StateNew:
				atomic.AddInt64(&open, 1)
				atomic.AddInt64(&total, 1)
			case http.StateClosed, http.StateHijacked:
				atomic.AddInt64(&open, -1)
			}
		},
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	h := &HTTP{}
	const N = 25
	for i := 0; i < N; i++ {
		if _, err := h.get(context.Background(), sock, "vm.info"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	t.Logf("after %d calls: %d connections opened, %d STILL OPEN", N, atomic.LoadInt64(&total), atomic.LoadInt64(&open))
	// CH's cap is 10, so anything that grows with N exhausts it.
	if n := atomic.LoadInt64(&open); n > 1 {
		t.Errorf("%d connections still open after %d calls; CH accepts 10 at once, so this makes a guest unreachable", n, N)
	}
}
