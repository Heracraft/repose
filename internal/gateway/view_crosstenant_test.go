package gateway

import (
	"testing"
)

// THE CROSS-TENANT TEST for the edge half of the browser view.
//
// Guest addresses are recycled: `guest_ip` is nulled on destroy and
// handed out again on create (internal/api/ops/phases.go). A guest's host
// certificate carries that address as a principal and lasts ten years,
// and a tenant has root in their own guest, so they can copy the host key
// and its certificate out. If the relay dialled `guest_ip:22`, the
// principal the Host CA check verifies would be the address, and a copy
// of an old guest's host material would answer for whoever holds that
// address next.
//
// What stops it is not a comment: the address passed to
// ssh.NewClientConn on this path is the *login* the api sent with the
// request -- `<slug>.<handle>`, derived from the ticket's row and never
// from anything a client chose -- so the checked principal is the login
// (internal/gateway/webhookdial.go). The view reuses that dial rather
// than writing its own, and this is the test that holds it there.
//
// Narrower than the whole of hole 6: the guest certificate's IP principal
// and its ten-year life are untouched here and belong to the webhook
// path's own entry. On this path they buy an attacker nothing.
func TestViewRelayRefusesAGuestThatNamesOnlyItsAddress(t *testing.T) {
	v := newViewHarness(t, &fakeXvnc{password: "pw123456", frame: []byte("F")})

	// The login the api sent: accepted, because the guest's certificate
	// names it.
	c, reply := v.request(t, v.ok())
	if !reply.OK {
		t.Fatalf("the project's own guest: %+v", reply)
	}
	clientHandshake(t, c)
	_ = c.Close()

	// The same address, answered by a guest whose host certificate names
	// the address and nothing else -- a tenant who kept their old guest's
	// host material and waited for the /22 to hand that address on.
	impostor := newFakeGuest(t, v.ca, v.project.ID, v.project.GuestIP)
	v.route(v.project.GuestIP, impostor.addr())
	for i := 0; i < 10; i++ {
		_, reply = v.request(t, v.ok())
		if reply.Code == ViewDialFailed {
			return
		}
		if reply.Code != ViewBusy {
			t.Fatalf("a guest that names only its address: %+v, want %s", reply, ViewDialFailed)
		}
		// The first view's slot is still unwinding; try again.
	}
	t.Fatalf("a guest that names only its address: %+v, want %s", reply, ViewDialFailed)
}
