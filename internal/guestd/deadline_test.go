package guestd

import (
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
)

// RegisterPaths and SetupProject wait on the boot itself: the load of the
// closure's registration, and home-manager's first activation before the
// session unit can start. On a busy host both took over 30 s, the default
// deadline, and the boot failed instead of finishing (2026-10-13: three
// creates on host-01 failed with "SetupProject: context deadline
// exceeded", DECISIONS I-705).
func TestBootRequestsOutliveTheDefaultDeadline(t *testing.T) {
	s := &Server{}
	for name, req := range map[string]*guestdv1.Request{
		"RegisterPaths": {Req: &guestdv1.Request_RegisterPaths{RegisterPaths: &guestdv1.RegisterPaths{}}},
		"SetupProject":  {Req: &guestdv1.Request_SetupProject{SetupProject: &guestdv1.SetupProject{}}},
	} {
		if got := s.deadlineFor(req); got != BootDeadline {
			t.Errorf("%s deadline %s, want %s", name, got, BootDeadline)
		}
	}
	if BootDeadline < 3*time.Minute {
		t.Errorf("BootDeadline %s is under 3 minutes", BootDeadline)
	}
	ping := &guestdv1.Request{Req: &guestdv1.Request_Ping{Ping: &guestdv1.Ping{}}}
	if got := s.deadlineFor(ping); got != DefaultDeadline {
		t.Errorf("Ping deadline %s, want the default %s", got, DefaultDeadline)
	}
}
