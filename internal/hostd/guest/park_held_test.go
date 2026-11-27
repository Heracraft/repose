package guest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
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

// A save on a locked Mac fails and can end the VM (lab Mac, 2026-11-27:
// "The virtual machine stopped unexpectedly", then "cannot resume in state
// Shutdown"). The park then leaves its record `writing`, and the resume
// that follows a refused `repose host restart` ran UnparkAll while the
// session was locked: it held that record, which had no park time, and
// expired it at once as "parked more than 6 hours with no login", stopping
// the machine. A park that never finished is not held: it is settled as
// anywhere, its image discarded.
func TestUnfinishedParkIsNotHeld(t *testing.T) {
	// A short wall: the failed resume is retried until it.
	fh := newParkHarness(t, func(c *ParkConfig) { c.Deadline, c.StopGrace, c.WriteRate = 2*time.Second, time.Second, 1<<40 })
	fh.create(gid1)
	fh.chc.Fail = map[string]error{
		"snapshot": errors.New("save: Internal Virtualization error. The virtual machine stopped unexpectedly."),
		"resume":   errors.New("cannot resume in state Shutdown"),
	}
	if r := fh.parkOne(gid1); r.OK {
		t.Fatalf("park: %+v", r)
	}
	fh.sd.Exit(GuestUnit(gid1), 0)
	if rec := fh.record(gid1); rec == nil {
		t.Fatal("the failed park left no record; this test no longer covers the case")
	}
	fh.chc.Fail = nil
	fh.m.d.Park.CanRestore = func(context.Context) bool { return false }
	fh.m.UnparkAll(context.Background())
	if n := fh.m.HeldGuests(); n != 0 {
		t.Fatalf("%d held: an unfinished park waits for the session", n)
	}
	fh.m.expireHeld(context.Background())
	if fh.record(gid1) != nil {
		t.Fatal("the unfinished park's record is still there")
	}
	if g := fh.guest(gid1); g.Reason == ReasonHeldTooLong {
		t.Fatalf("stopped as %q", g.Reason)
	}
}

// ENG-263: after a restore a Mac VM's footprint is its whole configured
// memory (9414 MB for an 8 GiB machine on the lab Mac, 2026-11-27) while
// its next save is the size of the last (1248, 1250, 1252 MB), so a park
// room counted from footprints refused `repose host restart` 2 of 3 times
// right after a restore. With GuestTouched the park counts what each guest
// says it has touched (MemTotal less MemFree), and the footprint only for
// a guest that does not answer.
func TestParkRoomCountsWhatTheGuestTouched(t *testing.T) {
	meminfo := func(argv []string) *guestdv1.ExecResult {
		if len(argv) == 3 && strings.Contains(argv[2], "/proc/meminfo") {
			// 8 GiB guest, 1.25 GiB touched.
			return &guestdv1.ExecResult{Stdout: []byte("MemTotal:        8388608 kB\nMemFree:         7077888 kB\n")}
		}
		return nil
	}
	run := func(touched bool) (parked int) {
		fh := newParkHarness(t, nil)
		fh.gopts.OnExec = meminfo
		fh.create(gid1)
		fh.create(gid2)
		fh.create(gidC3)
		// Room under the threshold for three 1.4 GiB images, not for three
		// whole 8 GiB footprints.
		fh.lvm.PoolSize, fh.lvm.PoolFree = 100<<30, 100<<30-(85<<30)+(5<<30)
		fh.m.d.Park.GuestTouched = touched
		fh.m.d.Park.ImageEstimate = func(context.Context, string) (uint64, bool) { return 9414 << 20, true }
		for _, r := range fh.m.ParkAll(context.Background(), ParkOperator) {
			if r.Outcome == "parked" {
				parked++
			} else {
				t.Logf("touched %v: %s %s %q", touched, r.GuestID, r.Outcome, r.Reason)
			}
		}
		return parked
	}
	if n := run(false); n != 0 {
		t.Fatalf("counted from footprints %d parked; this test no longer shows the refusal", n)
	}
	if n := run(true); n != 3 {
		t.Fatalf("counted from what the guests touched %d of 3 parked", n)
	}
}

func TestTouchedFromMeminfo(t *testing.T) {
	for in, want := range map[string]uint64{
		"MemTotal:        8388608 kB\nMemFree:         7077888 kB\n": (8388608 - 7077888) << 10,
		"MemTotal:        8388608 kB\n":                              0,
		"MemFree:         7077888 kB\n":                              0,
		"MemTotal: 1 kB\nMemFree: 2 kB\n":                            0,
		"":                                                           0,
	} {
		if got := touchedFromMeminfo([]byte(in)); got != want {
			t.Errorf("%q: %d, want %d", in, got, want)
		}
	}
}
