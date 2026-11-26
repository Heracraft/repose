package guest

import (
	"testing"
	"time"
)

func countState(h *harness, id, want string) int {
	n := 0
	for _, s := range h.rec.states(id) {
		if s == want {
			n++
		}
	}
	return n
}

// On a Mac a killed VM process left the machine in error until a manual
// start (d2-lab, 2026-11-26 19:21Z; D1's launcher relaunched its VM and
// the machines answered in 19-25 s). With RestartOnExit the machine boots
// again by itself, its memory lost and the reason in its state; a machine
// that keeps dying stays in error after restartBudget restarts.
func TestRestartOnExitBootsTheMachineAgain(t *testing.T) {
	h := newHarness(t, nil)
	h.m.d.RestartOnExit = true
	h.create(gid1)
	for i := 0; i < restartBudget; i++ {
		before := countState(h, gid1, StateRunning)
		h.sd.Exit(GuestUnit(gid1), 137)
		deadline := time.Now().Add(10 * time.Second)
		for countState(h, gid1, StateRunning) == before {
			if time.Now().After(deadline) {
				t.Fatalf("restart %d: not booted again (state %s %q)", i+1, h.guest(gid1).State, h.guest(gid1).Reason)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if countState(h, gid1, StateError) != 0 {
		t.Fatal("went through error while restarting")
	}
	h.sd.Exit(GuestUnit(gid1), 137)
	deadline := time.Now().Add(10 * time.Second)
	for h.guest(gid1).State != StateError {
		if time.Now().After(deadline) {
			t.Fatalf("past the budget: state %s", h.guest(gid1).State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
