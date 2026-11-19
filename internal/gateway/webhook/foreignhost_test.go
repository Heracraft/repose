package webhook

import (
	"net/http"
	"testing"
	"time"
)

// TestADeliveryToAProjectOnAnotherAccountsHostIsRefused (DECISIONS
// I-1368): a webhook delivery is the gateway's own connection into a
// machine, with no certificate behind it, so the owner check reads the
// route's two ids: the host's owner must be the project's owner, or the
// host is repose's. Probe: with the check removed this test fails with
// "a delivery was dialled into a project on another account's host".
func TestADeliveryToAProjectOnAnotherAccountsHostIsRefused(t *testing.T) {
	hp := newHarness(t)
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: "running",
		UserID: "acct-owner", HostOwnerID: "acct-someone-else"})
	w := hp.do(hp.post([]byte("{}"), nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d, want 503", w.Code)
	}
	if n := hp.dial.dials.Load(); n != 0 {
		t.Fatalf("a delivery was dialled into a project on another account's host: %d dials", n)
	}
	if d := hp.flushed(); len(d) != 1 || d[0].Outcome != "unavailable" {
		t.Fatalf("recorded: %+v", d)
	}
	// The owner's own host takes the delivery.
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: "running",
		UserID: "acct-owner", HostOwnerID: "acct-owner"})
	hp.now = hp.now.Add(StateTTL + time.Second)
	if w := hp.do(hp.post([]byte("{}"), nil)); w.Code != 200 {
		t.Fatalf("%d, want 200 on the owner's own host", w.Code)
	}
}
