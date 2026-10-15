package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// saveProjectTZ's wait gives the PATCH two seconds, then cancels it, so
// no request of the command is still in flight after it returns; a
// second call returns at once (runRun calls it before the attach and
// defers it for every other way out, a refused sync included).
func TestSaveProjectTZCancelsAPatchItStopsWaitingFor(t *testing.T) {
	gone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read to the end, as the api does: only then does the server
		// notice the client hanging up.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		close(gone)
	}))
	defer srv.Close()
	e := &Env{Client: newClient(srv.URL+"/v1", staticToken("tok"))}
	wait := saveProjectTZ(context.Background(), e, &Project{ID: "p1"}, "Europe/Berlin")
	start := time.Now()
	wait()
	if d := time.Since(start); d < 2*time.Second || d > 4*time.Second {
		t.Fatalf("the wait took %s, want about 2s", d)
	}
	select {
	case <-gone:
	case <-time.After(time.Second):
		t.Fatal("the PATCH was still in flight after the wait returned")
	}
	start = time.Now()
	wait()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("a second wait took %s", d)
	}
}
