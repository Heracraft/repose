package guest

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/lvm"
)

// stoppedGuest is a guest created and stopped, with the pool fine.
func stoppedGuest(t *testing.T, h *harness) {
	t.Helper()
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
}

func guestRuns(h *harness) int {
	n := 0
	for _, op := range h.sd.Ops {
		if op == "run guest@"+gid1 {
			n++
		}
	}
	return n
}

// DECISIONS I-644: a start on a host whose pool is down is refused with
// volume_unavailable before anything boots, and the guest stays stopped.
func TestStartRefusesUnavailablePool(t *testing.T) {
	h := newHarness(t, nil)
	stoppedGuest(t, h)
	runs := guestRuns(h)
	h.lvm.PoolDown = true
	h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeVolumeUnavailable)
	if g := h.guest(gid1); g.State != StateStopped {
		t.Fatalf("state %s after a refused start, want stopped", g.State)
	}
	if guestRuns(h) != runs {
		t.Fatal("a guest unit was started on an unavailable pool")
	}
	// The pool comes back: the same start works.
	h.lvm.PoolDown = false
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
}

// A volume the pool unit left inactive is activated once by the start.
func TestStartActivatesInactiveVolume(t *testing.T) {
	h := newHarness(t, nil)
	stoppedGuest(t, h)
	h.lvm.Inactive = map[string]bool{VolumeName(gid1): true}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if h.lvm.Inactive[VolumeName(gid1)] {
		t.Fatal("volume still inactive after the start")
	}
	if !strings.Contains(h.logs.String(), `"event":"volume_activate"`) {
		t.Fatal("no volume_activate line")
	}
	// One that will not activate refuses the start, the guest stopped.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.lvm.Inactive = map[string]bool{VolumeName(gid1): true}
	h.lvm.FailOn["activate"] = errors.New("lvchange: device busy")
	h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeVolumeUnavailable)
	if g := h.guest(gid1); g.State != StateStopped {
		t.Fatalf("state %s", g.State)
	}
}

// The user's login-share volume gets the same single activation.
func TestStartAuthVolumeInactive(t *testing.T) {
	h := newHarness(t, nil)
	stoppedGuest(t, h)
	auth := AuthVolumeName("user-1")
	if _, ok := h.lvm.Volumes[auth]; !ok {
		t.Fatalf("no login-share volume %s after create", auth)
	}
	h.lvm.Inactive = map[string]bool{auth: true}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if h.lvm.Inactive[auth] {
		t.Fatal("login-share volume still inactive")
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.lvm.Inactive = map[string]bool{auth: true}
	h.lvm.FailOn["activate"] = errors.New("lvchange: failed")
	h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeVolumeUnavailable)
}

// The incident path to a boot fallback: a dead pool fails the boot, and
// before I-644 bootOrFallBack then adopted the last good closure for a
// disk problem. The start is refused first now; the closure stays.
func TestStartNeverFallsBackOnUnavailablePool(t *testing.T) {
	h := fallbackHarness(t)
	stoppedGuest(t, h)
	other := fakeClosure(t, "nixos-system-v2")
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: other}))
	h.lvm.PoolDown = true
	h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeVolumeUnavailable)
	g := h.guest(gid1)
	if g.BootFallback != nil || g.SystemClosure != other || g.State != StateStopped {
		t.Fatalf("after a refused start %+v", g)
	}
}

// A missing VG is an lvs error, never "volume not found".
func TestStartMissingVGIsVolumeUnavailable(t *testing.T) {
	h := newHarness(t, nil)
	stoppedGuest(t, h)
	h.lvm.VGMissing = true
	h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeVolumeUnavailable)
	if g := h.guest(gid1); g.State != StateStopped {
		t.Fatalf("state %s", g.State)
	}
}

// After a host reboot that left the pool down, a guest recorded running
// whose unit is gone is stopped, not error: before I-644 the missing VG
// read as "volume missing" and every project went to error through Hello.
func TestReconcileMissingVGKeepsStopped(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*lvm.Fake)
	}{
		{"vg", func(f *lvm.Fake) { f.VGMissing = true }},
		{"lv", func(f *lvm.Fake) { f.PoolDown = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.create(gid1)
			h.sd.Exit(GuestUnit(gid1), 0)
			// the reboot: no monitor sees the exit; reconcile does.
			if mon := h.m.removeMonitor(gid1); mon != nil {
				mon.stop()
			}
			tc.set(h.lvm)
			if err := h.m.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			g := h.guest(gid1)
			if g.State != StateStopped || !strings.Contains(g.Reason, "pool unavailable") {
				t.Fatalf("after reconcile %s %q, want stopped for the pool", g.State, g.Reason)
			}
			if v := testutil.ToFloat64(h.metrics.PoolAvailable); v != 0 {
				t.Fatalf("repose_host_pool_available %v", v)
			}
		})
	}
	// The pool up and the volume really gone is still error.
	h := newHarness(t, nil)
	h.create(gid1)
	h.sd.Exit(GuestUnit(gid1), 0)
	if mon := h.m.removeMonitor(gid1); mon != nil {
		mon.stop()
	}
	delete(h.lvm.Volumes, VolumeName(gid1))
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := h.guest(gid1); g.State != StateError {
		t.Fatalf("a missing volume on an available pool is %s, want error", g.State)
	}
}

func TestSnapshotStoppedGuestUnavailablePool(t *testing.T) {
	h := newHarness(t, nil)
	stoppedGuest(t, h)
	h.lvm.PoolDown = true
	h.mustFail(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"}), CodeVolumeUnavailable)
	for name := range h.lvm.Volumes {
		if strings.HasPrefix(name, "snap-") {
			t.Fatalf("snapshot volume %s made on an unavailable pool", name)
		}
	}
}

// The login share's volume work goes through the same guard: no lvcreate
// behind the pool unit's back.
func TestAuthShareUnavailablePool(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolDown = true
	err := h.m.mountAuthVolume(context.Background(), "user-2", t.TempDir())
	if !errors.Is(err, lvm.ErrPoolUnavailable) {
		t.Fatalf("mount auth volume: %v", err)
	}
	if _, ok := h.lvm.Volumes[AuthVolumeName("user-2")]; ok {
		t.Fatal("a login-share volume was created on an unavailable pool")
	}
}

func TestCreateRestoreResizeUnavailablePool(t *testing.T) {
	h := newHarness(t, nil)
	stoppedGuest(t, h)
	h.lvm.PoolDown = true
	req := createReq(gid2)
	req.SystemClosure = h.closure
	h.mustFail(cmd(req), CodeVolumeUnavailable)
	h.mustFail(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 80 << 30}), CodeVolumeUnavailable)
	h.mustFail(cmd(&hostdv1.Restore{GuestId: gid2, ProjectId: "proj-0192f0a2", Class: "large", VolumeBytes: 40 << 30, BlobPath: "x/y.zst"}), CodeVolumeUnavailable)
	h.mustFail(cmd(&hostdv1.DestroyGuest{GuestId: gid1}), CodeVolumeUnavailable)
	if _, ok := h.lvm.Volumes[VolumeName(gid1)]; !ok {
		t.Fatal("destroy removed the volume")
	}
}

func TestReconcileSkipsSnapshotSweep(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.Volumes["snap-g1-1"] = &lvm.FakeVolume{}
	h.lvm.PoolDown = true
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, op := range h.lvm.Ops {
		if op == "remove" {
			t.Fatal("the sweep tried a remove on an unavailable pool")
		}
	}
	h.lvm.PoolDown = false
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.lvm.Volumes["snap-g1-1"]; ok {
		t.Fatal("the next reconcile with the pool up did not sweep")
	}
}

func TestHeartbeatPoolUnavailable(t *testing.T) {
	h := newHarness(t, nil)
	if hb := h.m.Heartbeat(); hb.PoolUnavailable || hb.PoolBytes == 0 {
		t.Fatalf("healthy pool heartbeat %+v", hb)
	}
	h.lvm.PoolDown = true
	hb := h.m.Heartbeat()
	if !hb.PoolUnavailable || hb.PoolBytes != 0 || hb.PoolFreeBytes != 0 {
		t.Fatalf("heartbeat %+v, want the flag and zero pool bytes", hb)
	}
	h.lvm.PoolDown, h.lvm.PoolHealth = true, "metadata_read_only"
	if hb := h.m.Heartbeat(); !hb.PoolUnavailable {
		t.Fatal("read-only metadata is unavailable")
	}
}

func TestHelloPoolUnavailable(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.VGMissing = true
	hello := h.m.Hello()
	if !hello.PoolUnavailable || hello.PoolFreeBytes != 0 || hello.PoolBytes != 0 {
		t.Fatalf("hello %+v", hello)
	}
	if v := testutil.ToFloat64(h.metrics.PoolAvailable); v != 0 {
		t.Fatalf("repose_host_pool_available %v", v)
	}
	h.lvm.VGMissing = false
	if hello := h.m.Hello(); hello.PoolUnavailable {
		t.Fatal("flag stuck")
	}
	if v := testutil.ToFloat64(h.metrics.PoolAvailable); v != 1 {
		t.Fatalf("repose_host_pool_available %v after the pool came back", v)
	}
}

func TestPoolWarningSilentWhenUnavailable(t *testing.T) {
	h := newHarness(t, nil)
	h.lvm.PoolDown = true
	h.m.poolWarning()
	h.m.poolWarning()
	for _, w := range h.rec.warnings() {
		if w == "pool_high" {
			t.Fatal("pool_high from a pool that is down")
		}
	}
	if n := strings.Count(h.logs.String(), `"event":"pool_unavailable"`); n != 1 {
		t.Fatalf("%d pool_unavailable lines from two ticks, want 1 (every 10 min)", n)
	}
}

// DECISIONS I-650: a host shutdown stops the guest@ units, maybe before
// hostd hears of it. The monitor's exit check must not record the guest
// as failed: it stays running, closure and all, for the next boot.
func TestUnexpectedExitDuringShutdownKeepsRunning(t *testing.T) {
	// No subtests: the fake guestd's socket path holds the test's name,
	// and a unix socket path has 108 bytes.
	for _, how := range []string{"systemd stopping", "hostd SIGTERM"} {
		func() {
			h := newHarness(t, nil)
			var stopping atomic.Bool
			h.m.d.SystemStopping = func(context.Context) bool { return stopping.Load() }
			h.create(gid1)
			closure := h.guest(gid1).SystemClosure
			if how == "systemd stopping" {
				stopping.Store(true)
			} else {
				h.m.SetShuttingDown()
			}
			h.sd.Exit(GuestUnit(gid1), 143)
			// several unit polls
			time.Sleep(20 * h.cfg.UnitPoll)
			g := h.guest(gid1)
			if g.State != StateRunning || g.SystemClosure != closure {
				t.Fatalf("%s: after the shutdown exit %s %s", how, g.State, g.SystemClosure)
			}
			for _, s := range h.rec.states(gid1) {
				if s == StateError {
					t.Fatal("an error state was sent for a guest the shutdown stopped")
				}
			}
			if !strings.Contains(h.logs.String(), `"event":"guest_interrupted"`) {
				t.Fatalf("%s: no guest_interrupted line", how)
			}
		}()
	}
	// Without a shutdown the same exit is a crash, as before.
	h := newHarness(t, nil)
	h.m.d.SystemStopping = func(context.Context) bool { return false }
	h.create(gid1)
	h.sd.Exit(GuestUnit(gid1), 1)
	h.waitState(gid1, StateError)
}

// A boot cut off by a shutdown is neither a failed closure (no fallback,
// no BootFallback) nor a stored result: the record keeps its closure and
// the command runs again when the api resends it after the restart.
func TestBootDuringShutdownNeverFallsBack(t *testing.T) {
	h := fallbackHarness(t)
	stoppedGuest(t, h)
	bad := fakeClosure(t, "nixos-system-v2")
	h.badClosure[bad] = stage2Missing
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: bad}))
	var stopping atomic.Bool
	h.m.d.SystemStopping = func(context.Context) bool { return stopping.Load() }
	start := cmd(&hostdv1.StartGuest{GuestId: gid1})
	done := make(chan *hostdv1.Result, 1)
	go func() { done <- h.m.Execute(context.Background(), start) }()
	// The shutdown begins while the boot waits for Ready.
	time.Sleep(100 * time.Millisecond)
	stopping.Store(true)
	if res := <-done; res != nil {
		t.Fatalf("an interrupted start answered %v; it must not be stored or sent", res)
	}
	g := h.guest(gid1)
	if g.BootFallback != nil || g.SystemClosure != bad || g.LastGoodClosure != h.closure {
		t.Fatalf("after the interrupted boot %+v", g)
	}
	if g.State == StateError {
		t.Fatal("the interrupted boot wrote error")
	}
	c, err := h.st.GetCommand(start.CommandId)
	if err != nil || c.Status == "done" {
		t.Fatalf("command record %+v %v: an interrupted command must not be done", c, err)
	}
}

// A plain hostd restart (every switch) is not a host shutdown: a boot cut
// off by it is torn down and answered as before I-650, so a closure that
// never reached Ready is never reconciled as running and the resend reads
// the failure back.
func TestHostdRestartMidBootIsNotAnInterruption(t *testing.T) {
	h := fallbackHarness(t)
	stoppedGuest(t, h)
	bad := fakeClosure(t, "nixos-system-v2")
	h.badClosure[bad] = stage2Missing
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: bad}))
	h.m.d.SystemStopping = func(context.Context) bool { return false }
	start := cmd(&hostdv1.StartGuest{GuestId: gid1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *hostdv1.Result, 1)
	go func() { done <- h.m.Execute(ctx, start) }()
	time.Sleep(100 * time.Millisecond)
	// app.Run's order: the root context ends, SetShuttingDown, Close.
	h.m.SetShuttingDown()
	cancel()
	res := <-done
	if res == nil || res.Ok {
		t.Fatalf("a start cut off by a hostd restart answered %v; want a stored failure", res)
	}
	if a, _ := h.sd.IsActive(context.Background(), GuestUnit(gid1)); a {
		t.Fatal("the half-booted guest@ unit was left running")
	}
	h.m.Close()
	m2, err := New(h.cfg, h.m.d)
	if err != nil {
		t.Fatal(err)
	}
	m2.Run()
	defer m2.Close()
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := h.guest(gid1); g.State == StateRunning {
		t.Fatalf("a closure that never reached Ready reconciled as running: %+v", g)
	}
	if r2 := m2.Execute(context.Background(), clone(start)); r2.Ok {
		t.Fatal("the resent start answered ok for a boot that never reached Ready")
	}
}

// A create cut off by a host shutdown is not stored; after the reboot the
// api's resend of the same CreateGuest finishes it (I-650).
func TestCreateInterruptedByHostShutdownResends(t *testing.T) {
	h := newHarness(t, nil)
	h.noBoot[gid1] = true
	var stopping atomic.Bool
	h.m.d.SystemStopping = func(context.Context) bool { return stopping.Load() }
	req := createReq(gid1)
	req.SystemClosure = h.closure
	c := cmd(req)
	done := make(chan *hostdv1.Result, 1)
	go func() { done <- h.m.Execute(context.Background(), c) }()
	for i := 0; i < 200; i++ {
		if a, _ := h.sd.IsActive(context.Background(), GuestUnit(gid1)); a {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopping.Store(true)
	if res := <-done; res != nil {
		t.Fatalf("an interrupted create answered %v", res)
	}
	h.m.Close()
	h.sd.Exit(GuestUnit(gid1), 143) // the host went down
	delete(h.noBoot, gid1)
	m2, err := New(h.cfg, h.m.d)
	if err != nil {
		t.Fatal(err)
	}
	m2.d.SystemStopping = func(context.Context) bool { return false }
	m2.Run()
	defer m2.Close()
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	r2 := m2.Execute(context.Background(), clone(c))
	if !r2.Ok {
		t.Fatalf("the resent create after the reboot: %v", r2.Error)
	}
	if g := h.guest(gid1); g.State != StateRunning {
		t.Fatalf("after the resent create: %s", g.State)
	}
	// A different project's create for a stopped guest id is still refused.
	other := createReq(gid1)
	other.ProjectId = "p_other"
	other.SystemClosure = h.closure
	if r := m2.Execute(context.Background(), cmd(&hostdv1.StopGuest{GuestId: gid1})); !r.Ok {
		t.Fatalf("stop: %v", r.Error)
	}
	if r := m2.Execute(context.Background(), cmd(other)); r.Ok || r.Error.Code != CodeAlreadyExists {
		t.Fatalf("a create over a stopped guest that has booted: %v", r)
	}
}
