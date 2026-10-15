package guestd

import (
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
)

// RegisterPaths forks nix-store several times against the guest's database
// — --check-validity over the shared store's lower, --load-db, the view
// sync — which the 30 s default did not cover on a loaded host, so creates
// failed at step 10 (DECISIONS I-740). It has its own deadline, above the
// default and under the two 10-minute bounds outside it (hostd's
// vsockclient cap on the call and the api's create_guest phase), so the
// error a tenant gets still names the command that was slow.
func TestRegisterPathsGetsItsOwnDeadline(t *testing.T) {
	s := &Server{}
	req := &guestdv1.Request{Req: &guestdv1.Request_RegisterPaths{RegisterPaths: &guestdv1.RegisterPaths{}}}
	if d := s.deadlineFor(req); d != RegisterPathsDeadline {
		t.Fatalf("RegisterPaths deadline %s, want %s", d, RegisterPathsDeadline)
	}
	if RegisterPathsDeadline <= DefaultDeadline {
		t.Fatalf("RegisterPathsDeadline %s is not above the %s default", RegisterPathsDeadline, DefaultDeadline)
	}
	if RegisterPathsDeadline >= 10*time.Minute {
		t.Fatalf("RegisterPathsDeadline %s leaves no room under the 10-minute bounds outside guestd", RegisterPathsDeadline)
	}
	// A request family with no case keeps the default.
	ping := &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}}
	if d := s.deadlineFor(ping); d != DefaultDeadline {
		t.Fatalf("Ping deadline %s, want the %s default", d, DefaultDeadline)
	}
}
