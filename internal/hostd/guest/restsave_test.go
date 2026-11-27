package guest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// saveCH is a hypervisor whose vm.snapshot writes what repose-vm writes.
type saveCH struct{ parkCH }

func (c saveCH) Snapshot(ctx context.Context, sock, dir string) error {
	if err := c.Fake.Snapshot(ctx, sock, dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "repose-vm.json"), []byte("{}"), 0o600)
}

// On a host that rests by save (a Mac, DECISIONS I-1666), an idle pause
// saves the paused VM and ends its hypervisor; reconcile and the monitor
// read it as resting, and the wake restores it from the save, resumes
// it, and removes the save.
func TestRestBySaveEndsTheHypervisorAndTheWakeRestores(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.m.d.CH = saveCH{parkCH{Fake: fh.chc, h: fh.harness}}
	fh.m.d.RestBySave = func(context.Context, string) bool { return true }
	fh.create(gid1)
	fh.pauseOK(gid1)
	if fh.unitActive(gid1) {
		t.Fatal("hypervisor still running after a rest by save")
	}
	if g := fh.guest(gid1); g.Resting == nil || g.State != StateRunning {
		t.Fatalf("state %s resting %v", g.State, g.Resting)
	}
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := fh.guest(gid1); g.State != StateRunning || g.Resting == nil {
		t.Fatalf("reconcile settled a guest resting as a save: %s %v", g.State, g.Resting)
	}
	res := fh.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "ssh"}))
	if w := res.GetWake(); w == nil || !w.WasResting {
		t.Fatalf("wake %+v", res)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("not running after the wake")
	}
	if argv := strings.Join(fh.unitArgv(gid1), " "); !strings.Contains(argv, "--restore source_url=file://"+fh.m.restDir(gid1)) {
		t.Fatalf("woken with %q, want a restore of the rest save", argv)
	}
	if _, err := os.Stat(fh.m.restDir(gid1)); !os.IsNotExist(err) {
		t.Fatal("rest save left after the wake")
	}
	if g := fh.guest(gid1); g.Resting != nil {
		t.Fatal("still marked resting")
	}
}

// Without RestBySave (every host but a Mac) a rest is a pause: the
// hypervisor keeps running.
func TestRestWithoutSaveOnlyPauses(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.pauseOK(gid1)
	if !fh.unitActive(gid1) || strings.Contains(fh.chCalls(), "snapshot ") {
		t.Fatal("a plain rest saved or stopped the VM")
	}
}

// DECISIONS I-1682: at the lock a Mac restores every guest resting as a
// save, its vCPUs still stopped, so it rests by pause through the lock
// (VZ cannot read a save once the screen has been locked a few seconds);
// the first wake after that is a resume, and the login file is bound
// again then. A minute into the unlock SaveRests gives the memory back
// again.
func TestLockRestoresRestSavesAndUnlockSavesThemAgain(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.m.d.CH = saveCH{parkCH{Fake: fh.chc, h: fh.harness}}
	fh.m.d.RestBySave = func(context.Context, string) bool { return true }
	fh.create(gid1)
	fh.pauseOK(gid1)
	if fh.unitActive(gid1) {
		t.Fatal("not saved")
	}
	before := len(fh.chCalls())
	if n := fh.m.UnsaveRests(context.Background()); n != 1 {
		t.Fatalf("UnsaveRests %d, want 1", n)
	}
	g := fh.guest(gid1)
	if !fh.unitActive(gid1) || g.Resting == nil || fh.m.restSaved(g) {
		t.Fatalf("after the lock: active %v resting %v saved %v", fh.unitActive(gid1), g.Resting, fh.m.restSaved(g))
	}
	if strings.Contains(fh.chCalls()[before:], "resume ") {
		t.Fatal("resumed at the lock; it must stay paused")
	}
	if n := fh.m.SaveRests(context.Background()); n != 1 || fh.unitActive(gid1) || !fh.m.restSaved(fh.guest(gid1)) {
		t.Fatalf("SaveRests %d active %v", n, fh.unitActive(gid1))
	}
	fh.m.UnsaveRests(context.Background())
	res := fh.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "ssh"}))
	if w := res.GetWake(); w == nil || !w.WasResting {
		t.Fatalf("wake %+v", res)
	}
	if _, due := fh.m.rebindDue.Load(gid1); due {
		t.Fatal("the login file was not bound again at the wake after a restore at the lock")
	}
}

// What a guest's memory waits for is said on its sample: "unlock" while
// it rests as a save and the session is locked, "login" with nobody
// logged in, nothing while unlocked or on a host without sessions.
func TestSampleSaysWhatARestSaveWaitsFor(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.m.d.CH = saveCH{parkCH{Fake: fh.chc, h: fh.harness}}
	fh.m.d.RestBySave = func(context.Context, string) bool { return true }
	fh.create(gid1)
	fh.pauseOK(gid1)
	waits := func() string {
		for _, gs := range fh.m.CollectSamples(context.Background()).Guests {
			if gs.GuestId == gid1 {
				return gs.WaitsFor
			}
		}
		return "?"
	}
	if w := waits(); w != "" {
		t.Fatalf("no session reader: %q", w)
	}
	state := SessionLocked
	fh.m.d.SessionState = func(context.Context) string { return state }
	fh.m.d.Park.CanRestore = func(context.Context) bool { return state == SessionUnlocked }
	for st, want := range map[string]string{SessionLocked: WaitsForUnlock, SessionNone: WaitsForLogin, SessionUnlocked: ""} {
		state = st
		if w := waits(); w != want {
			t.Fatalf("session %s: waits_for %q, want %q", st, w, want)
		}
	}
}

// A rest save holds a guest's memory on disk as a park image does, so the
// park's age limit holds for it too (I-653, DECISIONS I-1682): past 6
// hours it is restored into a paused machine where the session can read
// it, and otherwise removed with the machine stopped and said why.
func TestRestSavePastTheAgeLimit(t *testing.T) {
	for _, canRestore := range []bool{true, false} {
		fh := newParkHarness(t, nil)
		fh.m.d.CH = saveCH{parkCH{Fake: fh.chc, h: fh.harness}}
		fh.m.d.RestBySave = func(context.Context, string) bool { return true }
		fh.create(gid1)
		fh.pauseOK(gid1)
		fh.m.ExpireRestSaves(context.Background())
		if !fh.m.restSaved(fh.guest(gid1)) {
			t.Fatal("a fresh rest save was expired")
		}
		old := fh.m.d.Now().Add(-7 * time.Hour)
		if err := os.Chtimes(filepath.Join(fh.m.restDir(gid1), "repose-vm.json"), old, old); err != nil {
			t.Fatal(err)
		}
		fh.m.d.Park.CanRestore = func(context.Context) bool { return canRestore }
		fh.m.ExpireRestSaves(context.Background())
		g := fh.guest(gid1)
		if _, err := os.Stat(fh.m.restDir(gid1)); !os.IsNotExist(err) {
			t.Fatalf("canRestore %v: the save is still on disk", canRestore)
		}
		if canRestore && (g.State != StateRunning || g.Resting == nil || !fh.unitActive(gid1)) {
			t.Fatalf("unlocked: state %s resting %v active %v, want restored and paused", g.State, g.Resting, fh.unitActive(gid1))
		}
		if !canRestore && (g.State != StateStopped || g.Reason != ReasonRestTooLong) {
			t.Fatalf("locked: state %s reason %q", g.State, g.Reason)
		}
	}
}

// DECISIONS I-1682, the two-step rest: an idle machine rests by pause
// first (its wake a resume, under a second) and is saved, giving its
// memory back, only after RestSaveAfter, or at once when macOS is short.
func TestRestPausesFirstAndSavesLater(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.m.d.CH = saveCH{parkCH{Fake: fh.chc, h: fh.harness}}
	fh.m.d.RestBySave = func(context.Context, string) bool { return true }
	fh.m.d.RestSaveAfter = 30 * time.Minute
	tight := false
	fh.m.d.MemoryTight = func() bool { return tight }
	fh.create(gid1)
	fh.pauseOK(gid1)
	if !fh.unitActive(gid1) || fh.m.restSaved(fh.guest(gid1)) {
		t.Fatal("saved at the pause; the first step is a pause")
	}
	if n := fh.m.SaveRests(context.Background()); n != 0 {
		t.Fatalf("saved %d before RestSaveAfter", n)
	}
	g := fh.guest(gid1)
	g.Resting.Since = fh.m.d.Now().Add(-31 * time.Minute)
	if err := fh.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	if n := fh.m.SaveRests(context.Background()); n != 1 || fh.unitActive(gid1) || !fh.m.restSaved(fh.guest(gid1)) {
		t.Fatalf("after RestSaveAfter: saved %d, active %v", n, fh.unitActive(gid1))
	}
	if res := fh.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "ssh"})); !res.GetWake().GetWasResting() {
		t.Fatalf("wake %+v", res)
	}
	// Short of memory, the pause saves at once.
	tight = true
	fh.pauseOK(gid1)
	if fh.unitActive(gid1) || !fh.m.restSaved(fh.guest(gid1)) {
		t.Fatal("not saved at the pause with macOS short of memory")
	}
}
