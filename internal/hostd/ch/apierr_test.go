package ch

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failing CH API call carries the body, because the body is the
// diagnosis. get() did not, while put() and putJSON() always had: a
// guest whose API connection cap was full logged `ch api vm.info:
// status 503` for three hours while the body said `Too many open
// connections`, and reading it took a hand probe of the socket (I-912).
func TestAPIErrorCarriesTheBody(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "ch.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix socket: %v", err)
	}
	defer func() { _ = ln.Close() }()
	// What Cloud Hypervisor answers once its API connection cap is full.
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{ "error": "Too many open connections" }`))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	h := &HTTP{}
	ctx := context.Background()

	// get is the one that dropped it; vm.info is the call the wake makes.
	_, gerr := h.get(ctx, sock, "vm.info")
	if gerr == nil {
		t.Fatal("a 503 was not an error")
	}
	for _, want := range []string{"vm.info", "503", "Too many open connections"} {
		if !strings.Contains(gerr.Error(), want) {
			t.Errorf("get error %q lacks %q", gerr, want)
		}
	}

	// Its siblings keep doing so.
	if _, perr := h.put(ctx, sock, "vm.resume"); perr == nil || !strings.Contains(perr.Error(), "Too many open connections") {
		t.Errorf("put error %v", perr)
	}
	if jerr := h.putJSON(ctx, sock, "vm.add-device", map[string]string{"id": "x"}); jerr == nil || !strings.Contains(jerr.Error(), "Too many open connections") {
		t.Errorf("putJSON error %v", jerr)
	}
	_ = os.Remove(sock)
}
