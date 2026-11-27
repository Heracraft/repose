package guest

import (
	"context"
	"testing"
	"time"
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
	// The api is told it waits (I-1672), so it is not called not answering.
	var parked int64
	for _, gs := range fh.m.CollectSamples(context.Background()).Guests {
		if gs.GuestId == gid1 {
			parked = gs.ParkedWaitingSince
		}
	}
	if want := fh.record(gid1).DoneAt.Unix(); parked != want || parked == 0 {
		t.Fatalf("parked_waiting_since %d, want %d", parked, want)
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
	for _, gs := range fh.m.CollectSamples(context.Background()).Guests {
		if gs.GuestId == gid1 && gs.ParkedWaitingSince != 0 {
			t.Fatal("a restored guest still says it waits")
		}
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

// A held image past the park's age limit (six hours, I-653, I-1386) is
// removed and its guest stopped with the reason, even with no session:
// it never waits for a login forever.
func TestHeldParkExpiresAfterTheAgeLimit(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	fh.m.d.Park.CanRestore = func(context.Context) bool { return false }
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fh.m.HeldGuests() != 1 {
		t.Fatal("not held")
	}
	if ran, _ := fh.m.RestoreHeld(context.Background()); ran {
		t.Fatal("restored with no session")
	}
	if fh.record(gid1) == nil {
		t.Fatal("a fresh held image was discarded")
	}
	parkedAt := fh.record(gid1).DoneAt
	fh.m.d.Now = func() time.Time { return parkedAt.Add(6*time.Hour + time.Minute) }
	_, _ = fh.m.RestoreHeld(context.Background())
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image kept past the age limit")
	}
	if g := fh.guest(gid1); g.State != StateStopped || g.Reason != ReasonHeldTooLong {
		t.Fatalf("state %s reason %q", g.State, g.Reason)
	}
	if fh.m.HeldGuests() != 0 {
		t.Fatal("still held")
	}
}

// The park's room counts what the image will take when the host can tell
// (a Mac: the VM's footprint), not the class's memory: three 12 GiB
// machines were refused on 47 GB free (d2-lab, 2026-11-26).
func TestParkRoomUsesTheImageEstimate(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.create(gid2)
	// Room for one class-sized image (8 GiB large) but not two.
	fh.lvm.PoolSize, fh.lvm.PoolFree = 100<<30, 100<<30-(85<<30)+(12<<30)
	res := fh.m.ParkAll(context.Background(), ParkOperator)
	parked := 0
	for _, r := range res {
		if r.Outcome == "parked" {
			parked++
		}
	}
	if parked != 1 {
		t.Fatalf("without an estimate %d parked, want 1: %+v", parked, res)
	}
	fh2 := newParkHarness(t, nil)
	fh2.create(gid1)
	fh2.create(gid2)
	fh2.lvm.PoolSize, fh2.lvm.PoolFree = 100<<30, 100<<30-(85<<30)+(12<<30)
	fh2.m.d.Park.ImageEstimate = func(context.Context, string) (uint64, bool) { return 1 << 30, true }
	res = fh2.m.ParkAll(context.Background(), ParkOperator)
	for _, r := range res {
		if r.Outcome != "parked" {
			t.Fatalf("with a 1 GiB estimate: %+v", res)
		}
	}
}
