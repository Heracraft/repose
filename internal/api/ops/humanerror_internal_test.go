package ops

import (
	"strings"
	"testing"
)

// A boot that answered but ran out of time setting up says so, instead of
// "did not answer within a minute" (DECISIONS I-705). It names no bound:
// an old hostd cuts SetupProject at 30 s, a new one at 3 minutes.
func TestUnresponsiveBootSaysWhichWait(t *testing.T) {
	noReady, _ := humanError(KindCreate, PhaseCreateGuest, codeGuestUnresponsive, "create: step 10 (ready) failed: guest did not become ready: no Ready from guestd within 1m0s")
	if !strings.HasPrefix(noReady, "the environment did not answer within a minute of starting;") {
		t.Fatalf("no Ready: %q", noReady)
	}
	for _, msg := range []string{
		"create: step 10 (ready) failed: SetupProject: context deadline exceeded",
		"create: step 10 (ready) failed: RegisterPaths: guestd: internal: register paths: run /run/current-system/sw/bin/nix-store: context deadline exceeded",
	} {
		got, _ := humanError(KindStart, PhaseStartGuest, codeGuestUnresponsive, msg)
		if !strings.HasPrefix(got, "the environment started but did not finish setting up in time;") || !strings.Contains(got, "`repose start` tries again") {
			t.Fatalf("%s: %q", msg, got)
		}
	}
	// A setup that failed for another reason is not a timeout.
	other, _ := humanError(KindCreate, PhaseCreateGuest, codeGuestUnresponsive, "create: step 10 (ready) failed: SetupProject: guestd: internal: start the tmux session unit: systemctl exited 1")
	if strings.Contains(other, "in time") {
		t.Fatalf("a failed setup reads as a timeout: %q", other)
	}
}
