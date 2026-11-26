package guest

import (
	"context"
	"testing"
)

// A Mac holds a parked guest while its save file cannot be restored (the
// user's session is locked or absent, DECISIONS I-1666): reconcile leaves
// the guest, its record and its image alone, and RestoreHeld restores it
// once the session allows.
func TestParkedGuestHeldUntilItCanBeRestored(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	can := false
	fh.m.d.Park.CanRestore = func(context.Context) bool { return can }
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fh.unitActive(gid1) {
		t.Fatal("restored while it could not be")
	}
	if g := fh.guest(gid1); g.State != StateRunning || g.Interrupted != nil {
		t.Fatalf("held guest settled by reconcile: state %s interrupted %v", g.State, g.Interrupted)
	}
	if fh.record(gid1) == nil || !fh.img.has(gid1) {
		t.Fatal("held guest lost its record or image")
	}
	if ran, _ := fh.m.RestoreHeld(context.Background()); ran {
		t.Fatal("RestoreHeld ran while CanRestore is false")
	}
	can = true
	if ran, err := fh.m.RestoreHeld(context.Background()); !ran || err != nil {
		t.Fatalf("RestoreHeld %v %v", ran, err)
	}
	if !fh.unitActive(gid1) || fh.record(gid1) != nil {
		t.Fatal("not restored once it could be")
	}
	if fh.m.HeldGuests() != 0 {
		t.Fatal("still held")
	}
}

// A guest the Mac cannot park is skipped with its reason, untouched.
func TestCanParkSkips(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.m.d.Park.CanPark = func(context.Context, string) (bool, string) { return false, "started with nobody logged in" }
	r := fh.parkOne(gid1)
	if r.OK || r.Outcome != "skipped" || r.Reason != "started with nobody logged in" || !fh.unitActive(gid1) {
		t.Fatalf("%+v active=%v", r, fh.unitActive(gid1))
	}
}
