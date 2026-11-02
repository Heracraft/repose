package ch

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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
	call := func(n int) {
		for i := 0; i < n; i++ {
			if _, err := h.get(context.Background(), sock, "vm.info"); err != nil {
				t.Fatalf("call: %v", err)
			}
		}
	}
	// settle waits for the server to finish accounting for the closes, then
	// reports what is still open. `open` is decremented by the ConnState
	// callback, which runs after the client call has already returned to
	// this goroutine, so a reading taken the instant the calls end counts
	// connections that are closing rather than leaking. The distinction is
	// not a tolerance to be tuned: a leaked connection never drains, so a
	// bounded wait separates the two exactly, and a wait that ends early
	// costs nothing because a leak is still there when it ends.
	settle := func() int64 {
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			n := atomic.LoadInt64(&open)
			if n == 0 || time.Now().After(deadline) {
				return n
			}
		}
	}
	// Then the question is whether what is left grows with N, which is what
	// the comment above has always meant. An absolute threshold could not
	// ask it: it read red on a loaded runner with no leak (CI 41379622622,
	// "2 STILL OPEN" of 25) and would read green on a leak that drained
	// slower than the test looked (DECISIONS I-968).
	const few, many = 5, 25
	call(few)
	o1 := settle()
	call(many - few)
	o2 := settle()
	t.Logf("%d calls: %d still open; %d calls: %d still open (%d opened in all)",
		few, o1, many, o2, atomic.LoadInt64(&total))
	if o1 != 0 || o2 != 0 {
		t.Errorf("connections still open after they drained: %d at %d calls, %d at %d calls; "+
			"CH accepts 10 at once and reaps none, so anything left makes a guest unreachable",
			o1, few, o2, many)
	}
}
