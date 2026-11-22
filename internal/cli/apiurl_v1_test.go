package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// Council finding 17: an api URL without /v1 says so, instead of the
// bare "Route not found (not_found)".
func TestAPIURLMissingV1(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	c := newClient(fake.URL(), staticToken("tok"))
	_, err := c.ListProjects(context.Background())
	var errOut discardWriter
	code := exitCodeFor(err, &errOut)
	want := fake.URL() + " has no /v1 at its end; the api answers at " + fake.URL() + "/v1. Check --api-url or $REPOSE_API_URL."
	if code != ExitGeneric || strings.TrimSpace(errOut.buf.String()) != want {
		t.Fatalf("exit %d, said %q", code, errOut.buf.String())
	}
}
