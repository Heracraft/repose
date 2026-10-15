package guest

import (
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// TestStartReportsTheGuestAddress: every start carries the guest's
// address, the same one the create reported (DECISIONS I-720). Until
// this, StartResult had no address field at all, so a start after a
// create that failed past the guest's own boot gave the api nothing to
// route with and the project stayed unreachable (teksafari-org,
// 2026-10-13).
func TestStartReportsTheGuestAddress(t *testing.T) {
	h := newHarness(t, nil)
	cr := h.create(gid1).GetCreate()
	if cr.GuestIp == "" || cr.VsockCid == 0 {
		t.Fatalf("create reported no address: %v", cr)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	st := h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1})).GetStart()
	if st.GetGuestIp() != cr.GuestIp {
		t.Fatalf("start reported guest_ip %q, want the create's %q", st.GetGuestIp(), cr.GuestIp)
	}
	if st.GetVsockCid() != cr.VsockCid {
		t.Fatalf("start reported vsock_cid %d, want the create's %d", st.GetVsockCid(), cr.VsockCid)
	}
	// A start of a guest that is already running reports it too: the api
	// may be the one that lost the address, not the host.
	again := h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1})).GetStart()
	if again.GetGuestIp() != cr.GuestIp || again.GetVsockCid() != cr.VsockCid {
		t.Fatalf("a start of a running guest reported %q/%d", again.GetGuestIp(), again.GetVsockCid())
	}
}
