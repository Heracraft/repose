package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The api's answer when no host runs the kind of machine the account
// makes (internal/api/ops/place.go, I-1464): 503 capacity, detail.reason
// no_runtime_host. The fake does not produce it, so the test serves the
// fake with that one answer in front of POST /v1/projects.
const noRuntimeHostBody = `{"error":{"code":"capacity","message":"no host with capacity","detail":{"reason":"no_runtime_host","hosted_allowed":true}}}`

// The x86 check (I-1538): "repose has no room for this machine" was false
// there, since the user's own host was online and empty.
func TestRunNoRuntimeHostSaysWhy(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/projects" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(noRuntimeHostBody))
			return
		}
		fake.Server.Config.Handler.ServeHTTP(w, r)
	}))
	defer front.Close()
	f := newRunFixture(t, fake)
	f.env.Client = newClient(front.URL+"/v1", staticToken("tok"))
	err := runRun(context.Background(), f.env, RunOptions{NoAttach: true}, false)
	var errOut bytes.Buffer
	code := exitCodeFor(err, &errOut)
	got := strings.TrimSpace(errOut.String())
	if code != ExitCapacity || strings.Contains(got, "no room") || !strings.Contains(got, "No host can run the kind of machine your account makes") || !strings.Contains(got, "`repose run --host repose`") {
		t.Fatalf("exit %d, said %q", code, got)
	}

	// Anywhere else the same answer arrives (a restore, a start), the
	// generic refusal says the same, without the run's next step.
	errOut.Reset()
	if code := apiErrorExit(&APIError{Code: "capacity", Message: "no host with capacity", Status: 503, Detail: map[string]any{"reason": "no_runtime_host"}}, &errOut); code != ExitCapacity || strings.Contains(errOut.String(), "no room") {
		t.Fatalf("generic: exit %d, said %q", code, errOut.String())
	}
}

// TestCapacityRefusalOwnHost: a `capacity` refusal from the user's own
// host prints the api's sentence, which names the host and the figure
// (I-1586), on every path that prints a capacity refusal; the fleet's
// keeps I-724's sentence.
func TestCapacityRefusalOwnHost(t *testing.T) {
	own := &APIError{Code: "capacity", Status: 503, Message: "your host e2e-x86-a has 5 GB of memory free for machines, and a large machine needs 8 GB",
		Detail: map[string]any{"reason": "no_own_host", "host": "e2e-x86-a", "host_state": "ready"}}
	if got, want := capacityRefusal(own), "your host e2e-x86-a has 5 GB of memory free for machines, and a large machine needs 8 GB."; got != want {
		t.Fatalf("own host: %q, want %q", got, want)
	}
	var buf bytes.Buffer
	if code := apiErrorExit(own, &buf); code != ExitCapacity || !strings.Contains(buf.String(), "e2e-x86-a has 5 GB") {
		t.Fatalf("apiErrorExit: %d %q", code, buf.String())
	}
	fleet := &APIError{Code: "capacity", Status: 503, Message: "no host with capacity", Detail: map[string]any{"reason": "no_capacity"}}
	if got := capacityRefusal(fleet); !strings.HasPrefix(got, "repose has no room for this machine.") {
		t.Fatalf("fleet: %q", got)
	}
}
