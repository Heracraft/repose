package guest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
