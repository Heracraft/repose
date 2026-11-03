package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBuildInfoCarriesTheBinaryVersion holds the wiring, not the
// mechanism. internal/obs/metrics' own TestBuildInfo asserts that
// NewVersion labels the version it is handed, and it passed on every run
// while hostd handed it nothing: `repose_build_info{component="hostd"}`
// read version="dev" on host-02 at the same moment `hostd status` on that
// binary printed 4e98597 (DECISIONS I-1030). A test one layer up from the
// bug is not a test of the bug.
func TestBuildInfoCarriesTheBinaryVersion(t *testing.T) {
	m := newMetrics(Options{Version: "4e98597"})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	const want = `repose_build_info{component="hostd",version="4e98597"} 1`
	if !strings.Contains(body, want) {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "repose_build_info") {
				t.Fatalf("build_info reads %q, want %q", line, want)
			}
		}
		t.Fatalf("no repose_build_info at all in %d bytes", len(body))
	}
}

// An empty version is what a `go build` with no ldflags leaves, and the
// obs layer turns it into "dev". That is the honest answer for a local
// build and must not become the answer for a release.
func TestBuildInfoFallsBackToDev(t *testing.T) {
	m := newMetrics(Options{})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `repose_build_info{component="hostd",version="dev"} 1`) {
		t.Error("an unset version should read dev")
	}
}
