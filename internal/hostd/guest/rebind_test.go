package guest

import (
	"context"
	"strings"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// rebinds counts the Execs that bind the login file again.
func rebinds(fh *parkHarness, id string) int {
	n := 0
	for _, c := range fh.guestd(id).Calls() {
		if c.Kind == "Exec" && strings.Contains(strings.Join(c.Req.GetExec().GetArgv(), " "), "repose-claude-auth") {
			n++
		}
	}
	return n
}

// On a Mac the guest's bind of the login file went stale
// after a VZ restore while the share's directory still worked (lab Mac,
// 2026-11-26 16:17Z); after every restore of a guest with the login share,
// hostd binds the file again.
func TestRestoreRebindsTheLoginFile(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.m.d.DirectAuthShare = true
	fh.create(gid1)
	if !fh.m.argsHaveAuthShare(gid1) {
		t.Fatal("the guest has no login share")
	}
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	fh.waitState(gid1, StateRunning)
	if n := rebinds(fh, gid1); n != 1 {
		t.Fatalf("rebinds after the unpark: %d", n)
	}

	fh.m.d.CH = saveCH{parkCH{Fake: fh.chc, h: fh.harness}}
	fh.m.d.RestBySave = func(context.Context, string) bool { return true }
	fh.pauseOK(gid1)
	before := fh.guestd(gid1)
	fh.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "ssh"}))
	// The restored unit has a guestd of its own in the harness.
	if fh.guestd(gid1) == before {
		t.Fatal("the wake did not restore a new unit")
	}
	if n := rebinds(fh, gid1); n != 1 {
		t.Fatalf("rebinds after the wake from a rest save: %d", n)
	}
}
