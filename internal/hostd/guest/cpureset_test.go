package guest

import (
	"sync/atomic"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Each time a guest's hypervisor is gone (stop, destroy), hostd asks for
// the host's CPUs to be reset: on the Mac appliance a powered-off guest
// leaves the CPUs its vCPUs ran on burning a core each in macOS until
// they are (DECISIONS I-1578). The call comes after the guest@ unit has
// stopped, never while the hypervisor still runs.
func TestTeardownAsksForACPUReset(t *testing.T) {
	var calls atomic.Int32
	var unitUp atomic.Bool
	var h *harness
	h = newHarnessWith(t, nil, func(d *Deps) {
		d.AfterHypervisorExit = func() {
			if a, _ := h.sd.IsActive(t.Context(), GuestUnit(gid1)); a {
				unitUp.Store(true)
			}
			calls.Add(1)
		}
	})
	h.create(gid1)
	if n := calls.Load(); n != 0 {
		t.Fatalf("reset asked %d times by a create", n)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1, TimeoutS: 2}))
	if n := calls.Load(); n != 1 {
		t.Fatalf("after stop: reset asked %d times, want 1", n)
	}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	if n := calls.Load(); n < 2 {
		t.Fatalf("after destroy: reset asked %d times, want 2 or more", n)
	}
	if unitUp.Load() {
		t.Fatal("reset asked while guest@ was still active")
	}
}
