package guest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/vzfake"
)

// The tests in this file run hostd's guest manager against vzfake, a
// stand-in for the Mac's launcher (cmd/repose-vm) that refuses what
// Virtualization.framework refuses, through hostd's real client
// (ch.HTTP), with the substrate switches a Mac sets (adaptMac). A test
// against ch.Fake, which says yes to all of it, cannot see a path that
// works on Linux and fails on a Mac (design 2, proposal item 10).

type vzHarness struct {
	*parkHarness
	vz *vzfake.Host

	mu       sync.Mutex
	vzLocked bool
	// hostdSees is what hostd reads of the session (vzpark.ReadSession):
	// nil reads the fake's own lock; set, it is what hostd read before the
	// lock changed under it.
	hostdSees *bool
}

func newVZHarness(t *testing.T) *vzHarness {
	t.Helper()
	// The launcher's API socket is <guests>/<id>/ch.sock, and a unix
	// socket path is at most 104 bytes on a Mac and 108 here: under
	// t.TempDir() a long test name does not fit.
	short, err := os.MkdirTemp("", "vz")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	vz := vzfake.New()
	t.Cleanup(vz.Close) // registered first, so it runs after the manager's own cleanup
	fh := newParkHarnessWith(t, nil, func(c *Config) { c.GuestsDir = filepath.Join(short, "g") })
	vh := &vzHarness{parkHarness: fh, vz: vz}
	// The launcher's process ends: launchd sees the job exit. A restore
	// that fails exits before the job's bootstrap is seen to finish, so
	// wait for the unit to be there first.
	vh.vz.OnExit = func(sock string, code int) {
		unit := GuestUnit(filepath.Base(filepath.Dir(sock)))
		for i := 0; i < 200; i++ {
			if a, _ := fh.sd.IsActive(context.Background(), unit); a {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		fh.sd.Exit(unit, code)
	}
	prev := fh.sd.OnRun
	fh.sd.OnRun = func(unit string, argv []string) error {
		if strings.HasPrefix(unit, "guest@") {
			if err := vh.vz.Start(argv); err != nil {
				return err
			}
		}
		if prev != nil {
			return prev(unit, argv)
		}
		return nil
	}
	d := &fh.m.d
	d.CH = &ch.HTTP{}
	// What adaptMac sets that these paths read.
	d.FileVolumes = true
	d.RestartOnExit = true
	d.Park.CanRestore = func(context.Context) bool { return vh.unlocked() }
	return vh
}

func (vh *vzHarness) unlocked() bool {
	vh.mu.Lock()
	defer vh.mu.Unlock()
	if vh.hostdSees != nil {
		v := *vh.hostdSees
		vh.hostdSees = nil // one stale read; the next read is the session's
		return v
	}
	return !vh.vzLocked
}

// lock locks the Mac's session.
func (vh *vzHarness) lock(v bool) {
	vh.mu.Lock()
	vh.vzLocked = v
	vh.mu.Unlock()
	vh.vz.SetLocked(v)
}

func (vh *vzHarness) sock(id string) string { return ch.APISocket(vh.m.guestDir(id)) }

// A park and a host restart, with the session unlocked, go through the
// launcher's own verbs with none refused: pause, save of the paused VM,
// shutdown; then a restore that comes up paused, and a resume.
func TestVZParkAndRestoreThroughTheLauncher(t *testing.T) {
	vh := newVZHarness(t)
	vh.create(gid1)
	if s := vh.vz.State(vh.sock(gid1)); s != vzfake.Running {
		t.Fatalf("launcher state %q after create", s)
	}
	if r := vh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v\ncalls:\n%s", r, strings.Join(vh.vz.Calls(), "\n"))
	}
	if vh.unitActive(gid1) {
		t.Fatal("the launcher still runs after the park")
	}
	vh.nextStart("boot-b")
	if !vh.unitActive(gid1) || vh.vz.State(vh.sock(gid1)) != vzfake.Running {
		t.Fatalf("not restored: active %v state %q\ncalls:\n%s", vh.unitActive(gid1), vh.vz.State(vh.sock(gid1)), strings.Join(vh.vz.Calls(), "\n"))
	}
	if vh.record(gid1) != nil {
		t.Fatal("the record outlived the restore")
	}
	assertNoRefusal(t, vh)
}

// A restore that VZ refuses because the session locked after hostd read
// it unlocked keeps the save and holds the machine until the unlock; the
// machine's memory is the whole point of the park, so a refusal hostd
// can retry is never a cold boot.
func TestVZRestoreRefusedOnALockedSessionKeepsTheImage(t *testing.T) {
	vh := newVZHarness(t)
	vh.create(gid1)
	if r := vh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	vh.lock(true)
	stale := true
	vh.hostdSees = &stale // hostd read the session a moment before it locked
	vh.nextStart("boot-b")
	if vh.vz.Count("restore ") != 1 {
		t.Fatalf("restores tried: %d\n%s", vh.vz.Count("restore "), strings.Join(vh.vz.Calls(), "\n"))
	}
	if vh.record(gid1) == nil || !vh.img.has(gid1) {
		t.Fatal("a restore refused for the lock threw away the parked image")
	}
	if g := vh.guest(gid1); g.State != StateRunning || vh.m.HeldGuests() != 1 {
		t.Fatalf("not held for the unlock: state %s held %d", g.State, vh.m.HeldGuests())
	}
	if vh.vz.Count("start ") != 1 {
		t.Fatalf("booted cold while its save waits: %d starts", vh.vz.Count("start "))
	}
	vh.lock(false)
	if ran, err := vh.m.RestoreHeld(context.Background()); !ran || err != nil {
		t.Fatalf("RestoreHeld at the unlock: %v %v", ran, err)
	}
	if !vh.unitActive(gid1) || vh.vz.State(vh.sock(gid1)) != vzfake.Running || vh.record(gid1) != nil {
		t.Fatalf("not restored at the unlock\n%s", strings.Join(vh.vz.Calls(), "\n"))
	}
}

// A machine resting as a save cannot be woken while the session is
// locked: the wake says so and the save stays, and a wake after the
// unlock restores it.
func TestVZRestWakeOnALockedSessionKeepsTheSave(t *testing.T) {
	vh := newVZHarness(t)
	vh.m.d.RestBySave = func(context.Context, string) bool { return true }
	vh.create(gid1)
	vh.pauseOK(gid1)
	if vh.unitActive(gid1) {
		t.Fatalf("the launcher runs after a rest by save\n%s", strings.Join(vh.vz.Calls(), "\n"))
	}
	vh.lock(true)
	res := vh.run(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "ssh"}))
	if res.Error == nil || !strings.Contains(res.Error.Message, "locked") {
		t.Fatalf("wake on a locked session: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(vh.m.restDir(gid1), "vz.state")); err != nil {
		t.Fatalf("the rest save went with the refused wake: %v", err)
	}
	if g := vh.guest(gid1); g.State != StateRunning || g.Resting == nil {
		t.Fatalf("after the refused wake: state %s resting %v", g.State, g.Resting)
	}
	vh.lock(false)
	w := vh.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "ssh"})).GetWake()
	if w == nil || !w.WasResting || vh.vz.State(vh.sock(gid1)) != vzfake.Running {
		t.Fatalf("wake after the unlock: %+v state %q", w, vh.vz.State(vh.sock(gid1)))
	}
}

// A rest by pause and its wake are the launcher's pause and resume.
func TestVZRestByPauseWakes(t *testing.T) {
	vh := newVZHarness(t)
	vh.create(gid1)
	vh.pauseOK(gid1)
	if vh.vz.State(vh.sock(gid1)) != vzfake.Paused {
		t.Fatalf("state %q after the rest", vh.vz.State(vh.sock(gid1)))
	}
	vh.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "ssh"}))
	if vh.vz.State(vh.sock(gid1)) != vzfake.Running {
		t.Fatalf("state %q after the wake", vh.vz.State(vh.sock(gid1)))
	}
	assertNoRefusal(t, vh)
}

// A live grow is refused by hostd before it reaches the launcher, which
// would refuse it after the file had grown (I-1666).
func TestVZLiveGrowNeverReachesTheLauncher(t *testing.T) {
	vh := newVZHarness(t)
	vh.create(gid1)
	res := vh.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 80 << 30}), CodeInvalidArgument)
	if !strings.Contains(res.Error.Message, "while it is stopped") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if n := vh.vz.Count("PUT vm.resize-disk"); n != 0 {
		t.Fatalf("resize-disk reached the launcher %d times", n)
	}
	// What the launcher answers when it is reached, so the guard above is
	// not guarding against nothing.
	err := (&ch.HTTP{}).ResizeDisk(context.Background(), vh.sock(gid1), "vol", 80<<30)
	if err == nil || !strings.Contains(err.Error(), vzfake.ErrResizeDisk) {
		t.Fatalf("launcher resize-disk: %v", err)
	}
}

// A machine the Mac cannot park (started with nobody logged in) is
// skipped before its VM is paused, and keeps running.
func TestVZParkSkipsWhatCannotBeSaved(t *testing.T) {
	vh := newVZHarness(t)
	vh.create(gid1)
	vh.m.d.Park.CanPark = func(context.Context, string) (bool, string) { return false, "started with nobody logged in" }
	r := vh.parkOne(gid1)
	if r.OK || r.Outcome != "skipped" {
		t.Fatalf("%+v", r)
	}
	if vh.vz.Count("PUT vm.pause") != 0 || vh.vz.Count("PUT vm.snapshot") != 0 {
		t.Fatalf("the VM was touched:\n%s", strings.Join(vh.vz.Calls(), "\n"))
	}
	if vh.vz.State(vh.sock(gid1)) != vzfake.Running || !vh.unitActive(gid1) {
		t.Fatal("the skipped machine is not running")
	}
}

// A VM whose launcher ends under a running machine is booted again
// (I-1664), through the launcher.
func TestVZKilledLauncherBootsAgain(t *testing.T) {
	vh := newVZHarness(t)
	vh.create(gid1)
	vh.vz.Kill(vh.sock(gid1))
	deadline := time.Now().Add(10 * time.Second)
	for vh.vz.Count("start ") < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if vh.vz.Count("start ") < 2 {
		t.Fatalf("not booted again:\n%s", strings.Join(vh.vz.Calls(), "\n"))
	}
}

// assertNoRefusal fails when hostd sent the launcher a verb its VM's state
// refuses (a resume of a running VM, a pause of a paused one, a save of a
// running one), which ch.Fake accepts and VZ does not.
func assertNoRefusal(t *testing.T, vh *vzHarness) {
	t.Helper()
	if n := vh.vz.Refused(); len(n) > 0 {
		t.Fatalf("the launcher refused: %s", strings.Join(n, "; "))
	}
}

// A machine held for the unlock that the user stops (or removes) stays
// stopped at the unlock: the unlock restores only what still waits, and
// the stopped machine's image does not outlive it.
func TestVZHeldMachineStoppedBeforeTheUnlockStaysStopped(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    *hostdv1.Command
	}{
		{"stop", cmd(&hostdv1.StopGuest{GuestId: gid1})},
		{"destroy", cmd(&hostdv1.DestroyGuest{GuestId: gid1})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vh := newVZHarness(t)
			vh.create(gid1)
			if r := vh.parkOne(gid1); !r.OK {
				t.Fatalf("park: %+v", r)
			}
			vh.lock(true)
			vh.nextStart("boot-b")
			if vh.m.HeldGuests() != 1 {
				t.Fatalf("held %d", vh.m.HeldGuests())
			}
			vh.mustOK(tc.c)
			vh.lock(false)
			if _, err := vh.m.RestoreHeld(context.Background()); err != nil {
				t.Fatal(err)
			}
			if vh.vz.Count("restore ") != 0 || vh.unitActive(gid1) {
				t.Fatalf("restored after the %s:\n%s", tc.name, strings.Join(vh.vz.Calls(), "\n"))
			}
			if vh.record(gid1) != nil || vh.img.has(gid1) {
				t.Fatalf("the image outlived the %s", tc.name)
			}
			if tc.name == "stop" {
				if g := vh.guest(gid1); g.State != StateStopped {
					t.Fatalf("state %s after the stop and the unlock", g.State)
				}
			}
			if vh.m.HeldGuests() != 0 {
				t.Fatalf("still held: %d", vh.m.HeldGuests())
			}
		})
	}
}
