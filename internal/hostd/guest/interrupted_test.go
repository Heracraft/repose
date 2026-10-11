package guest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/state"
)

// bootClock is the host's boot id as the tests change it.
type bootClock struct{ id string }

func (b *bootClock) bootID() (string, error) { return b.id, nil }

var bootStart = time.Date(2026, 10, 10, 5, 46, 12, 0, time.UTC)

func bootTime() (time.Time, error) { return bootStart, nil }

// withBoot gives the harness's manager a boot id, and records it the way
// hostd's start in that boot does.
func withBoot(t *testing.T, h *harness, id string) *bootClock {
	t.Helper()
	b := &bootClock{id: id}
	h.m.d.BootID, h.m.d.BootTime = b.bootID, bootTime
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

// hostRestart models the host going down under hostd and coming back in
// boot id: every guest unit is gone and a new hostd starts and reconciles.
func hostRestart(t *testing.T, h *harness, b *bootClock, id string) *Manager {
	t.Helper()
	h.m.Close()
	for _, gid := range []string{gid1, gid2, gid3} {
		h.sd.Exit("guest@"+gid, 0)
	}
	b.id = id
	m2, err := New(h.cfg, h.m.d)
	if err != nil {
		t.Fatal(err)
	}
	m2.Run()
	t.Cleanup(m2.Close)
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.m = m2
	return m2
}

const gid3 = "0192f0a3-3333-7000-8000-000000000003"

func TestReconcileAfterHostRebootMarksRunningGuests(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid2}))
	b := withBoot(t, h, "boot-1")
	m2 := hostRestart(t, h, b, "boot-2")

	g := h.guest(gid1)
	if g.State != StateStopped {
		t.Fatalf("interrupted guest state %s, want stopped", g.State)
	}
	if g.Interrupted == nil || g.Interrupted.BootID != "boot-2" || g.Interrupted.PrevState != StateRunning || g.Interrupted.At.IsZero() {
		t.Fatalf("running guest not marked by the restart: %+v", g.Interrupted)
	}
	if g2 := h.guest(gid2); g2.Interrupted != nil {
		t.Fatalf("a guest stopped before the restart was marked: %+v", g2.Interrupted)
	}
	if id, _ := h.st.HostBootID(); id != "boot-2" {
		t.Fatalf("recorded boot id %q", id)
	}
	if n := gaugeValue(t, h.metrics.GuestsInterrupted); n != 1 {
		t.Fatalf("repose_host_guests_interrupted = %v, want 1", n)
	}
	logs := h.logs.String()
	for _, want := range []string{`"event":"guest_interrupted"`, `"event":"host_boot"`, `"prev_boot_id":"boot-1"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log has no %s", want)
		}
	}
	// guest.json carries the mark, so a rebuild from disk keeps it.
	if d := m2.guestFromDisk(gid1); d == nil || d.Interrupted == nil || d.Interrupted.BootID != "boot-2" {
		t.Fatalf("guest.json mark: %+v", d)
	}
}

func TestReconcileAfterHostRebootInSameBootMarksNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	b := withBoot(t, h, "boot-1")
	// hostd restarted (or was killed) and the guest's unit died with it,
	// all within one boot: not a host restart.
	hostRestart(t, h, b, "boot-1")
	g := h.guest(gid1)
	if g.State != StateStopped || g.Interrupted != nil {
		t.Fatalf("same boot: state %s mark %+v", g.State, g.Interrupted)
	}
	if strings.Contains(h.logs.String(), `"event":"guest_interrupted"`) {
		t.Fatal("guest_interrupted logged without a host restart")
	}
}

func TestReconcileAfterHostRebootFirstRecordedBootMarksNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	// The hostd that adds I-645 starts for the first time: no boot id has
	// been recorded, so nothing can be told apart from a hostd restart.
	b := &bootClock{id: "boot-1"}
	h.m.d.BootID, h.m.d.BootTime = b.bootID, bootTime
	hostRestart(t, h, b, "boot-2")
	if g := h.guest(gid1); g.Interrupted != nil {
		t.Fatalf("first recorded boot marked %+v", g.Interrupted)
	}
	if id, _ := h.st.HostBootID(); id != "boot-2" {
		t.Fatalf("first start did not record the boot: %q", id)
	}
	if !strings.Contains(h.logs.String(), `"prev_boot_id":""`) {
		t.Fatal("host_boot with an empty previous id not logged")
	}
}

func TestReconcileAfterHostRebootBootIDRecordedAfterMarks(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	b := withBoot(t, h, "boot-1")
	h.m.Close()
	for _, gid := range []string{gid1, gid2} {
		h.sd.Exit("guest@"+gid, 0)
	}
	// A first hostd in boot-2 marked gid1 and died before gid2 and before
	// recording the boot: what bbolt holds is gid1 stopped and marked,
	// gid2 still running, and boot-1 as the recorded boot.
	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	g1 := h.guest(gid1)
	g1.State, g1.Interrupted = StateStopped, &state.Interrupted{BootID: "boot-2", At: at, PrevState: StateRunning}
	if err := h.st.PutGuest(g1); err != nil {
		t.Fatal(err)
	}
	b.id = "boot-2"
	m2, err := New(h.cfg, h.m.d)
	if err != nil {
		t.Fatal(err)
	}
	m2.Run()
	defer m2.Close()
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := h.guest(gid2); g.Interrupted == nil || g.Interrupted.BootID != "boot-2" {
		t.Fatalf("the rerun did not mark the guest the first run missed: %+v", g.Interrupted)
	}
	if g := h.guest(gid1); g.Interrupted == nil || !g.Interrupted.At.Equal(at) {
		t.Fatalf("the first run's mark changed: %+v", g.Interrupted)
	}
	if id, _ := h.st.HostBootID(); id != "boot-2" {
		t.Fatalf("boot id %q after the rerun", id)
	}
	// A third start in the same boot changes nothing.
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := h.guest(gid2); g.Interrupted == nil || g.State != StateStopped {
		t.Fatalf("a reconcile in the same boot cleared or moved the mark: %+v", g)
	}
}

func TestReconcileAfterHostRebootMarksStartingGuests(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	h.create(gid3)
	b := withBoot(t, h, "boot-1")
	// gid1 was booting when the host went down; gid2 was being stopped
	// by its owner; gid3 was being restored.
	_, _ = h.st.SetGuestState(gid1, StateStarting, "")
	_, _ = h.st.SetGuestState(gid2, StateStopping, "")
	_, _ = h.st.SetGuestState(gid3, StateRestoring, "")
	hostRestart(t, h, b, "boot-2")
	if g := h.guest(gid1); g.Interrupted == nil || g.Interrupted.PrevState != StateStarting {
		t.Fatalf("starting guest not marked: %+v", g.Interrupted)
	}
	for _, gid := range []string{gid2, gid3} {
		if g := h.guest(gid); g.Interrupted != nil {
			t.Fatalf("%s guest marked: %+v", gid, g.Interrupted)
		}
	}
}

func TestSetStateRunningClearsMark(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	b := withBoot(t, h, "boot-1")
	m2 := hostRestart(t, h, b, "boot-2")
	if h.guest(gid1).Interrupted == nil {
		t.Fatal("not marked")
	}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if g := h.guest(gid1); g.State != StateRunning || g.Interrupted != nil {
		t.Fatalf("after the start: state %s mark %+v", g.State, g.Interrupted)
	}
	for _, gs := range m2.Hello().Guests {
		if gs.InterruptedBootId != "" || gs.InterruptedAt != 0 {
			t.Fatalf("Hello still carries the mark: %v", gs)
		}
	}
	if n := gaugeValue(t, h.metrics.GuestsInterrupted); n != 0 {
		t.Fatalf("gauge %v after the start", n)
	}
	if d := m2.guestFromDisk(gid1); d == nil || d.Interrupted != nil {
		t.Fatalf("guest.json still carries the mark: %+v", d)
	}
	// The found-running path of reconcile is a setState(running) too: a
	// marked guest whose unit is up loses the mark.
	g := h.guest(gid1)
	g.State, g.Interrupted = StateStarting, &state.Interrupted{BootID: "boot-2", At: time.Now(), PrevState: StateRunning}
	_ = h.st.PutGuest(g)
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := h.guest(gid1); g.State != StateRunning || g.Interrupted != nil {
		t.Fatalf("reconciled running: state %s mark %+v", g.State, g.Interrupted)
	}
}

func TestStopDestroyRestoreClearMark(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	h.create(gid3)
	sr := h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid3, Reason: "manual"})).GetSnapshot()
	b := withBoot(t, h, "boot-1")
	hostRestart(t, h, b, "boot-2")
	for _, gid := range []string{gid1, gid2, gid3} {
		if h.guest(gid).Interrupted == nil {
			t.Fatalf("%s not marked", gid)
		}
	}
	// The owner stops a machine that is already down: it stays down.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	if g := h.guest(gid1); g.Interrupted != nil {
		t.Fatalf("stop left the mark: %+v", g.Interrupted)
	}
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid2}))
	if g, err := h.st.GetGuest(gid2); err == nil && g.Interrupted != nil {
		t.Fatalf("destroy left the mark: %+v", g.Interrupted)
	}
	// A restore into the guest (after a failure left it in error).
	_, _ = h.st.SetGuestState(gid3, StateError, "x")
	h.mustOK(cmd(&hostdv1.Restore{ProjectId: "proj-0192f0a3", GuestId: gid3, BlobPath: sr.BlobPath, Class: "large", VolumeBytes: 40 << 30, SystemClosure: h.closure}))
	if g := h.guest(gid3); g.Interrupted != nil {
		t.Fatalf("restore left the mark: %+v", g.Interrupted)
	}
	if n := gaugeValue(t, h.metrics.GuestsInterrupted); n != 0 {
		t.Fatalf("gauge %v", n)
	}
}

func TestHelloCarriesInterrupted(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid2}))
	b := withBoot(t, h, "boot-1")
	m2 := hostRestart(t, h, b, "boot-2")
	hello := m2.Hello()
	if hello.HostBootId != "boot-2" || hello.HostBootAt != bootStart.Unix() {
		t.Fatalf("hello boot %q at %d", hello.HostBootId, hello.HostBootAt)
	}
	seen := 0
	for _, gs := range hello.Guests {
		switch gs.GuestId {
		case gid1:
			seen++
			if gs.State != StateStopped || gs.InterruptedBootId != "boot-2" || gs.InterruptedAt == 0 {
				t.Fatalf("interrupted guest in Hello: %v", gs)
			}
		case gid2:
			seen++
			if gs.InterruptedBootId != "" || gs.InterruptedAt != 0 {
				t.Fatalf("stopped guest carries a mark: %v", gs)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("hello guests %v", hello.Guests)
	}
	// The status the operator's `hostd guests --json` prints.
	sts, err := m2.Guests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range sts {
		if st.GuestID == gid1 && (st.InterruptedBootID != "boot-2" || st.InterruptedAt == nil) {
			t.Fatalf("guests status %+v", st)
		}
	}
	// A hostd with no boot source sends neither.
	m2.d.BootID = nil
	if hl := m2.Hello(); hl.HostBootId != "" || hl.HostBootAt != 0 {
		t.Fatalf("no boot source: %q %d", hl.HostBootId, hl.HostBootAt)
	}
}

func TestOldMarkPruned(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid2}))
	old := time.Now().Add(-InterruptedKeep - time.Hour)
	recent := time.Now().Add(-InterruptedRecent - time.Hour)
	for gid, at := range map[string]time.Time{gid1: old, gid2: recent} {
		g := h.guest(gid)
		g.Interrupted = &state.Interrupted{BootID: "boot-0", At: at, PrevState: StateRunning}
		if err := h.st.PutGuest(g); err != nil {
			t.Fatal(err)
		}
	}
	withBoot(t, h, "boot-1")
	if g := h.guest(gid1); g.Interrupted != nil {
		t.Fatalf("a mark older than %s was kept: %+v", InterruptedKeep, g.Interrupted)
	}
	if g := h.guest(gid2); g.Interrupted == nil {
		t.Fatal("a mark inside the keep window was pruned")
	}
	// Past the api's window it no longer counts as down.
	if n := gaugeValue(t, h.metrics.GuestsInterrupted); n != 0 {
		t.Fatalf("gauge %v counts a mark older than %s", n, InterruptedRecent)
	}
}

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	return testutil.ToFloat64(g)
}
