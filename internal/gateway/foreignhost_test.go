package gateway

import (
	"fmt"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// TestAProjectOnAnotherAccountsHostIsRefused (DECISIONS I-1368): the
// gateway routes a connection to a host the operator does not run only
// when the host's owner is the account the certificate was issued to.
// The api's placement never puts a project on another account's host,
// and this is the gateway's own check of that, against the certificate
// in hand rather than the api's word. Probe: with the check removed this
// test fails with "connected to a project on another account's host".
func TestAProjectOnAnotherAccountsHostIsRefused(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)

	// A host repose runs (no owner): routed as always.
	c, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatalf("a hosted machine was refused: %v", err)
	}
	_ = c.Close()

	// The project's host is owned by another account.
	h.api.SetHostOwner(h.project.ID, "acct-someone-else")
	h.offset.Add(6) // past the route cache
	if c, banner, err := h.dial(h.login, cert); err == nil {
		_ = c.Close()
		t.Fatal("connected to a project on another account's host")
	} else if want := fmt.Sprintf(MsgForeignHostFmt, h.project.Slug); banner != want {
		t.Fatalf("banner %q, want %q", banner, want)
	}
	if n := h.authFails(ResultForeignHost); n != 1 {
		t.Fatalf("foreign_host auth failures %d, want 1", n)
	}

	// The project's host is the account's own: routed.
	h.api.SetHostOwner(h.project.ID, fakeapi.CannedUser.ID)
	h.offset.Add(6)
	c, _, err = h.dial(h.login, cert)
	if err != nil {
		t.Fatalf("the owner's own host was refused: %v", err)
	}
	_ = c.Close()
}
