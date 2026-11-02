package sysdep

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serveContainers starts a unix-socket HTTP server that answers
// /containers/json with n containers, each padded to about the size the
// real Engine API returns (ids, names, image, command, ports, labels,
// state, mounts). It returns the socket path.
//
// The size is the point of the test. A small body is already sitting in
// the transport's buffered reader by the time the client reads it, so a
// cancelled request context goes unnoticed; a body larger than that
// buffer has to come off the socket, and then the cancellation lands.
// That is why guestd reported `docker_down` against a daemon that was
// serving containers perfectly well, and only once a project had enough
// of them.
func serveContainers(t *testing.T, n int) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dockersock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("["))
		for i := 0; i < n; i++ {
			if i > 0 {
				_, _ = w.Write([]byte(","))
			}
			// ~1 KB per container, which is the order the real API returns.
			_, _ = fmt.Fprintf(w, `{"Id":"%064d","Names":["/c%d"],"Labels":{"pad":"%s"}}`,
				i, i, strings.Repeat("x", 900))
		}
		_, _ = w.Write([]byte("]"))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// TestRunningContainersReadsTheWholeBody is the regression for guestd
// reporting docker_down against a healthy daemon (DECISIONS I-986).
// `get` took a context with a timeout and cancelled it with `defer
// cancel()`, which runs when `get` returns -- before the caller has read
// a byte of the body. A comment claimed the cancel ran when the caller
// closed the body; it did not.
func TestRunningContainersReadsTheWholeBody(t *testing.T) {
	for _, n := range []int{0, 1, 3, 40} {
		t.Run(fmt.Sprintf("%d_containers", n), func(t *testing.T) {
			d := NewSocketDocker(serveContainers(t, n), 5*time.Second)
			got, err := d.RunningContainers(context.Background())
			if err != nil {
				t.Fatalf("%d containers: %v -- the daemon answered and the count was lost anyway", n, err)
			}
			if got != n {
				t.Fatalf("counted %d containers, want %d", got, n)
			}
		})
	}
}

// TestPingSurvivesTheSameBug holds the other half: Ping only reads the
// status line, so it passed throughout. That asymmetry is what made the
// bug read as "the daemon is up but has no containers" rather than as a
// broken probe.
func TestPingSurvivesTheSameBug(t *testing.T) {
	d := NewSocketDocker(serveContainers(t, 40), 5*time.Second)
	if err := d.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// TestCallerCancellationStillStops proves the fix did not drop the
// caller's cancellation on the floor while removing the per-request one.
func TestCallerCancellationStillStops(t *testing.T) {
	d := NewSocketDocker(serveContainers(t, 40), 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.RunningContainers(ctx); err == nil {
		t.Fatal("a cancelled caller context must stop the request")
	}
}
