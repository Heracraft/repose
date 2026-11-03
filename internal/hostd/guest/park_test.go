package guest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/fakeguestd"
	"github.com/heracraft/repose/internal/hostd/metrics"
	"github.com/heracraft/repose/internal/hostd/park"
	"github.com/heracraft/repose/internal/hostd/systemd"
)

// fakeImage is the image volume: it remembers each guest's key check and
// refuses an Open with another key, as a wrong dm-crypt key would.
type fakeImage struct {
	mu      sync.Mutex
	vols    map[string]string // guest id -> key check
	open    map[string]bool
	ops     []string
	failOn  map[string]error
	created map[string]uint64
	// fs is the filesystem each volume was made with, and openedAs what
	// the restore asked for: an image mounted as the wrong type fails on
	// a host, so the fake fails too.
	fs       map[string]string
	openedAs map[string]string
	// madeAs is what FS() reports; the real one is btrfs (I-685).
	madeAs string
}

func newFakeImage() *fakeImage {
	return &fakeImage{vols: map[string]string{}, open: map[string]bool{}, failOn: map[string]error{},
		created: map[string]uint64{}, fs: map[string]string{}, openedAs: map[string]string{},
		// What the real image volume makes, so a test of the record's
		// filesystem is a test of the host's, not of the fake's.
		madeAs: (&park.Image{}).FS()}
}

func (f *fakeImage) op(s string) error {
	f.ops = append(f.ops, s)
	return f.failOn[strings.Fields(s)[0]]
}

func (f *fakeImage) Create(_ context.Context, id string, size uint64, key []byte, _ string, _, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.op("create " + id); err != nil {
		return err
	}
	f.vols[id], f.open[id], f.created[id] = park.KeyCheck(key), true, size
	f.fs[id] = f.madeAs
	return nil
}

func (f *fakeImage) FS() string { return f.madeAs }

func (f *fakeImage) Open(_ context.Context, id string, key []byte, _, fs string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.op("open " + id); err != nil {
		return err
	}
	kc, ok := f.vols[id]
	if !ok {
		return errors.New("no image volume")
	}
	if kc != park.KeyCheck(key) {
		return errors.New("mount: wrong fs type")
	}
	if fs == "" {
		fs = park.FSExt4
	}
	f.openedAs[id] = fs
	if made, ok := f.fs[id]; ok && made != "" && made != fs {
		return fmt.Errorf("mount: image is %s, asked for %s", made, fs)
	}
	f.open[id] = true
	return nil
}

func (f *fakeImage) Close(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.op("close " + id); err != nil {
		return err
	}
	f.open[id] = false
	return nil
}

func (f *fakeImage) Remove(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.op("remove " + id); err != nil {
		return err
	}
	delete(f.vols, id)
	delete(f.open, id)
	return nil
}

func (f *fakeImage) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.vols[id]
	return ok
}

// fakeSealer wraps by reversing the key; Unwrap can be made to fail.
type fakeSealer struct {
	notReady  error
	unwrapErr error
	unwraps   int
	// unwrapDeadlines is each Unwrap's ctx deadline. UnparkAll gives every
	// image one shared context, so these are identical when the
	// UnwrapTimeout is spent once for the whole unpark and differ when it
	// is spent per image (I-972).
	unwrapDeadlines []time.Time
	mu              sync.Mutex
}

func (s *fakeSealer) Ready() error                  { return s.notReady }
func (s *fakeSealer) Refresh(context.Context) error { return nil }
func (s *fakeSealer) Wrap(key []byte) ([]byte, string, error) {
	w := slices.Clone(key)
	slices.Reverse(w)
	return w, "v1", nil
}
func (s *fakeSealer) Unwrap(ctx context.Context, w []byte, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unwraps++
	if dl, ok := ctx.Deadline(); ok {
		s.unwrapDeadlines = append(s.unwrapDeadlines, dl)
	}
	if s.unwrapErr != nil {
		return nil, s.unwrapErr
	}
	if id != "v1" {
		return nil, errors.New("unknown key version")
	}
	k := slices.Clone(w)
	slices.Reverse(k)
	return k, nil
}

// parkCH is the fake hypervisor client with what a park needs: a
// shutdown ends the unit (as the real vmm.shutdown does), and a version.
type parkCH struct {
	*ch.Fake
	h *harness
}

func (c parkCH) Shutdown(ctx context.Context, sock string) error {
	err := c.Fake.Shutdown(ctx, sock)
	// The guest id is the directory the socket is in, so this holds for
	// however many guests a test makes.
	if id := filepath.Base(filepath.Dir(sock)); id != "" {
		c.h.sd.Exit(GuestUnit(id), 0)
	}
	return err
}

// heldCH holds every snapshot until the test lets them all go, so a
// park's snapshots really are in flight at once.
type heldCH struct {
	parkCH
	in      chan string
	release chan struct{}
}

func (c heldCH) Snapshot(ctx context.Context, sock, dir string) error {
	c.in <- sock
	<-c.release
	return c.Fake.Snapshot(ctx, sock, dir)
}

// hangingCH is a hypervisor that answers a snapshot only when the call's
// own context ends, as one mid-snapshot does.
type hangingCH struct{ parkCH }

func (hangingCH) Snapshot(ctx context.Context, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

type parkHarness struct {
	*harness
	img    *fakeImage
	sealer *fakeSealer
	fp     map[string]string
	fpMu   sync.Mutex
}

func newParkHarness(t *testing.T, mut func(*ParkConfig)) *parkHarness {
	t.Helper()
	recDir := t.TempDir()
	h := newHarness(t, func(c *Config) {
		c.Park = ParkConfig{Enabled: true, RecordDir: recDir, Settle: -1, UnwrapTimeout: 200 * time.Millisecond, RestoreTimeout: 3 * time.Second}
		if mut != nil {
			mut(&c.Park)
		}
	})
	fh := &parkHarness{harness: h, img: newFakeImage(), sealer: &fakeSealer{}, fp: map[string]string{}}
	h.chc.Ver = "cloud-hypervisor v53.0.0"
	h.m.d.CH = parkCH{Fake: h.chc, h: h}
	h.m.d.Park = ParkDeps{
		Sealer: fh.sealer, Image: fh.img,
		Fingerprint: func(dev string) (string, error) {
			fh.fpMu.Lock()
			defer fh.fpMu.Unlock()
			if v, ok := fh.fp[dev]; ok {
				return v, nil
			}
			return "sb-1", nil
		},
		BootID:     func() string { return "boot-a" },
		ImageBytes: func(string) uint64 { return 900 << 20 },
	}
	return fh
}

// nextStart models the host coming back after a restart: a new hostd, so
// a new Manager with a registry whose counters begin at zero, reading
// what the last one left beside the records. The counter starting at
// zero is the whole point -- a test that reuses the registry asserts a
// rise production never produces (I-796).
func (fh *parkHarness) nextStart(boot string) {
	fh.t.Helper()
	fh.m.Close()
	d := fh.m.d
	d.Metrics = metrics.New()
	d.Park.BootID = func() string { return boot }
	m2, err := New(fh.cfg, d)
	if err != nil {
		fh.t.Fatal(err)
	}
	m2.Run()
	fh.t.Cleanup(m2.Close)
	fh.m = m2
	m2.UnparkAll(context.Background())
}

func (fh *parkHarness) setFingerprint(id, v string) {
	fh.fpMu.Lock()
	defer fh.fpMu.Unlock()
	fh.fp[fh.lvm.DevPath(VolumeName(id))] = v
}

func (fh *parkHarness) record(id string) *park.Record {
	fh.t.Helper()
	r, err := fh.m.records().Get(id)
	if err != nil {
		fh.t.Fatal(err)
	}
	return r
}

func (fh *parkHarness) unitActive(id string) bool {
	a, _ := fh.sd.IsActive(context.Background(), GuestUnit(id))
	return a
}

func (fh *parkHarness) unitArgv(id string) []string {
	fh.sd.Units[GuestUnit(id)].Argv = slices.Clone(fh.sd.Units[GuestUnit(id)].Argv)
	return fh.sd.Units[GuestUnit(id)].Argv
}

func (fh *parkHarness) parkOne(id string) ParkResult {
	fh.t.Helper()
	res := fh.m.ParkAll(context.Background(), ParkOperator)
	for _, r := range res {
		if r.GuestID == id {
			return r
		}
	}
	fh.t.Fatalf("no park result for %s: %+v", id, res)
	return ParkResult{}
}

func (fh *parkHarness) chCalls() string {
	return strings.Join(fh.chc.Calls, "\n")
}

// A parked guest is paused, snapshotted into its image and its hypervisor
// stopped, its state still running; the next reconcile restores it from
// the image (not a boot), sets its clock, and the image is gone.
func TestParkThenUnparkRestoresTheGuest(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	res := fh.parkOne(gid1)
	if !res.OK || res.Outcome != "parked" {
		t.Fatalf("park: %+v", res)
	}
	if fh.unitActive(gid1) {
		t.Fatal("hypervisor still running after the park")
	}
	if g := fh.guest(gid1); g.State != StateRunning {
		t.Fatalf("state %s after the park, want running", g.State)
	}
	rec := fh.record(gid1)
	if rec == nil || rec.State != park.StateComplete || rec.Fingerprint != "sb-1" || rec.CHVersion == "" || len(rec.WrappedKey) != park.KeyBytes {
		t.Fatalf("record %+v", rec)
	}
	calls := fh.chCalls()
	for _, want := range []string{"pause ", "snapshot ", "shutdown "} {
		if !strings.Contains(calls, want) {
			t.Fatalf("no %q among the hypervisor calls:\n%s", want, calls)
		}
	}
	if !strings.Contains(strings.Join(fh.img.ops, " "), "close "+gid1) {
		t.Fatalf("image not closed after the park: %v", fh.img.ops)
	}
	if fh.img.created[gid1] != (8192+1536)<<20 {
		t.Fatalf("image volume of %d bytes for a large guest", fh.img.created[gid1])
	}

	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest not running after the unpark")
	}
	argv := strings.Join(fh.unitArgv(gid1), " ")
	if !strings.Contains(argv, "--restore source_url=file://") || strings.Contains(argv, "--kernel") {
		t.Fatalf("hypervisor started with %q, want a restore", argv)
	}
	if !strings.Contains(fh.chCalls(), "resume ") {
		t.Fatal("restored VM never resumed")
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image or record left after a successful restore")
	}
	fh.waitState(gid1, StateRunning)
	if !slices.Contains(fh.guestd(gid1).Kinds(), "Exec") {
		t.Fatal("guest clock never set after the restore")
	}
}

// Whatever changed the guest since its park, its image is not restored:
// the image goes and reconcile handles the guest as after any restart.
func TestUnparkDiscardsAStaleImage(t *testing.T) {
	cases := map[string]func(fh *parkHarness){
		"closure": func(fh *parkHarness) {
			g := fh.guest(gid1)
			g.SystemClosure = fakeClosure(fh.t, "nixos-system-v2")
			_ = fh.st.PutGuest(g)
		},
		"written": func(fh *parkHarness) { fh.setFingerprint(gid1, "sb-2") },
		"resized": func(fh *parkHarness) {
			g := fh.guest(gid1)
			g.VolumeBytes += 1 << 30
			_ = fh.st.PutGuest(g)
		},
		"chver": func(fh *parkHarness) { fh.chc.Ver = "cloud-hypervisor v54.0.0" },
		"partial": func(fh *parkHarness) {
			r := fh.record(gid1)
			r.State = park.StateWriting
			_ = fh.m.records().Put(r)
		},
		"old": func(fh *parkHarness) {
			r := fh.record(gid1)
			r.DoneAt = r.DoneAt.Add(-7 * time.Hour)
			_ = fh.m.records().Put(r)
		},
		"nokey": func(fh *parkHarness) { fh.sealer.unwrapErr = errors.New("vault unreachable") },
		"badkey": func(fh *parkHarness) {
			r := fh.record(gid1)
			r.WrappedKey[0] ^= 0xff
			_ = fh.m.records().Put(r)
		},
		"stopped": func(fh *parkHarness) {
			g := fh.guest(gid1)
			g.State = StateStopped
			_ = fh.st.PutGuest(g)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			fh := newParkHarness(t, nil)
			fh.create(gid1)
			if r := fh.parkOne(gid1); !r.OK {
				t.Fatalf("park: %+v", r)
			}
			change(fh)
			if err := fh.m.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if fh.unitActive(gid1) {
				t.Fatalf("guest restored after %s", name)
			}
			if fh.record(gid1) != nil || fh.img.has(gid1) {
				t.Fatalf("image kept after %s", name)
			}
			if g := fh.guest(gid1); g.State != StateStopped && g.State != StateError {
				t.Fatalf("state %s after a discarded image", g.State)
			}
		})
	}
}

// A snapshot that fails resumes the guest: it keeps running, untouched.
func TestParkFailureResumesTheGuest(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.chc.Fail = map[string]error{"snapshot": errors.New("No space left on device")}
	res := fh.parkOne(gid1)
	if res.OK || res.Reason != "snapshot failed" {
		t.Fatalf("park: %+v", res)
	}
	if !strings.Contains(fh.chCalls(), "resume ") {
		t.Fatal("paused guest never resumed")
	}
	if !fh.unitActive(gid1) || fh.guest(gid1).State != StateRunning {
		t.Fatal("guest not running after a failed park")
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image or record left by a failed park")
	}
	// It is still a live guest: commands reach it.
	if _, err := fh.m.session(gid1); err != nil {
		t.Fatalf("no guestd session after a failed park: %v", err)
	}
}

// A restore whose hypervisor does not start falls back: no image left, the
// guest is settled by reconcile as after any restart (L6 starts it cold).
func TestUnparkFallsBackWhenTheRestoreFails(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	prev := fh.sd.OnRun
	fh.sd.OnRun = func(unit string, argv []string) error {
		if slices.Contains(argv, "--restore") {
			return errors.New("snapshot state version mismatch")
		}
		return prev(unit, argv)
	}
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image kept after a failed restore")
	}
	if g := fh.guest(gid1); g.State != StateStopped {
		t.Fatalf("state %s after a failed restore, want stopped", g.State)
	}
	// A normal start boots it.
	fh.sd.OnRun = prev
	fh.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if argv := strings.Join(fh.unitArgv(gid1), " "); !strings.Contains(argv, "--kernel") {
		t.Fatalf("start after a failed restore did not boot: %s", argv)
	}
}

// With the park off nothing is parked, and images a host left behind
// are removed, never restored.
func TestParkOffRestoresNothingAndCleansUp(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	fh.m.cfg.Park.Enabled = false
	if res := fh.m.ParkAll(context.Background(), ParkShutdown); res != nil {
		t.Fatalf("park while off: %+v", res)
	}
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fh.unitActive(gid1) || fh.img.has(gid1) || fh.record(gid1) != nil {
		t.Fatal("an image was restored or kept with the park off")
	}
}

// No key, no park: the guests are left running for the shutdown.
func TestParkWithoutKeyParksNothing(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.sealer.notReady = errors.New("key vault public key not fetched yet")
	if res := fh.m.ParkAll(context.Background(), ParkShutdown); len(res) != 0 {
		t.Fatalf("results without a key: %+v", res)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest stopped by a park that could not happen")
	}
}

// When the pool cannot take every image, the smallest guests go first and
// the rest are skipped, running.
func TestParkStopsAtThePoolLimit(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	small := createReq(gid2)
	small.SystemClosure, small.Class = fh.closure, "small"
	fh.mustOK(cmd(small))
	// 100 GiB pool, refuse at 85: 6 GiB of room takes the small guest's 4
	// GiB image and not the large one's 8.
	fh.lvm.PoolSize, fh.lvm.PoolFree = 100<<30, 21<<30
	res := fh.m.ParkAll(context.Background(), ParkOperator)
	got := map[string]string{}
	for _, r := range res {
		got[r.GuestID] = r.Outcome
	}
	if got[gid2] != "parked" || got[gid1] != "skipped" {
		t.Fatalf("outcomes %v, want the small guest parked and the large skipped", got)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("skipped guest stopped")
	}
}

// An image volume with no record (hostd died between lvcreate and the
// record) is removed at start.
func TestUnparkSweepsImageVolumesWithoutRecord(t *testing.T) {
	fh := newParkHarness(t, nil)
	if err := fh.lvm.CreateVolume(context.Background(), park.LVName(gid2), 1<<30); err != nil {
		t.Fatal(err)
	}
	fh.m.UnparkAll(context.Background())
	if !slices.Contains(fh.img.ops, "remove "+gid2) {
		t.Fatalf("orphan image volume not removed: %v", fh.img.ops)
	}
}

// The shutdown's park writes nothing a tenant typed into the log.
func TestParkLogsCarryNoTenantData(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.parkOne(gid1)
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs := fh.logs.String()
	for _, bad := range []string{"s3cret", "todo-app", "git@github.com"} {
		if strings.Contains(logs, bad) {
			t.Fatalf("log carries %q", bad)
		}
	}
	if !bytes.Contains([]byte(logs), []byte(`"event":"guest_park"`)) || !bytes.Contains([]byte(logs), []byte(`"event":"guest_unpark"`)) {
		t.Fatal("park and unpark not logged")
	}
}

// hostd restarted mid-park (the record still `writing`, the hypervisor
// running with the VM paused): the next start resumes the guest and only
// then removes the image.
func TestRestartMidParkResumesTheGuest(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if err := fh.m.records().Put(&park.Record{GuestID: gid1, State: park.StateWriting, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fh.img.vols[gid1] = "x"
	_ = fh.chc.Pause(context.Background(), "paused-by-the-park")
	before := len(fh.chc.Calls)
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(fh.chc.Calls[before:], "\n")
	if !strings.Contains(calls, "resume ") {
		t.Fatalf("guest left paused after a restart mid-park; hypervisor calls %q", calls)
	}
	if !fh.unitActive(gid1) || fh.guest(gid1).State != StateRunning {
		t.Fatal("guest not running after the restart")
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image or record kept once the guest ran again")
	}
}

// If the hypervisor is still busy writing (it answers nothing), the image
// is not removed under it: record and image stay for the next start.
func TestMidParkBusyHypervisorKeepsImage(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) { c.ResumeWait = 300 * time.Millisecond })
	fh.create(gid1)
	if err := fh.m.records().Put(&park.Record{GuestID: gid1, State: park.StateWriting, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fh.img.vols[gid1] = "x"
	fh.chc.Fail = map[string]error{"info": errors.New("timeout"), "resume": errors.New("timeout")}
	res := fh.m.UnparkAll(context.Background())
	if len(res) != 1 || res[0].Outcome != "skipped" {
		t.Fatalf("unpark with a busy hypervisor: %+v", res)
	}
	if slices.Contains(fh.img.ops, "remove "+gid1) || fh.record(gid1) == nil {
		t.Fatalf("image removed under a busy hypervisor: ops %v", fh.img.ops)
	}
	// Once it answers, the next UnparkAll resumes it and removes the image.
	fh.chc.Fail = nil
	fh.m.UnparkAll(context.Background())
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image kept once the guest ran again")
	}
}

// A second park of a parked guest (two ParkAlls one after the other:
// an operator's and the shutdown's) leaves the finished image alone.
func TestSecondParkKeepsTheImage(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	if res := fh.m.ParkAll(context.Background(), ParkShutdown); len(res) != 0 {
		t.Fatalf("second ParkAll: %+v", res)
	}
	// What a job already queued for the guest does, on its worker.
	res := fh.m.parkGuest(context.Background(), gid1, ParkShutdown, time.Time{})
	if res.OK || res.Outcome != "skipped" {
		t.Fatalf("second park of a parked guest: %+v", res)
	}
	if strings.Count(strings.Join(fh.img.ops, " "), "create "+gid1) != 1 {
		t.Fatalf("finished image re-created: %v", fh.img.ops)
	}
	if rec := fh.record(gid1); rec == nil || rec.State != park.StateComplete || !fh.img.has(gid1) {
		t.Fatal("finished image lost to a second park")
	}
}

// gatedCH holds a snapshot until the test releases it.
type gatedCH struct {
	parkCH
	started, release chan struct{}
}

func (c gatedCH) Snapshot(ctx context.Context, sock, dir string) error {
	close(c.started)
	<-c.release
	return c.Fake.Snapshot(ctx, sock, dir)
}

// An unpark (hostd unpark, hostd reconcile, a called-off Scheduled Event) that
// comes while a park is writing waits for it, and then restores the
// finished image instead of removing the one being written.
func TestUnparkDuringAParkWaitsForIt(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	gc := gatedCH{parkCH: fh.m.d.CH.(parkCH), started: make(chan struct{}), release: make(chan struct{})}
	fh.m.d.CH = gc
	parked := make(chan []ParkResult)
	go func() { parked <- fh.m.ParkAll(context.Background(), ParkOperator) }()
	<-gc.started
	unparked := make(chan []ParkResult)
	go func() { unparked <- fh.m.UnparkAll(context.Background()) }()
	select {
	case r := <-unparked:
		t.Fatalf("unpark ran during the park: %+v", r)
	case <-time.After(200 * time.Millisecond):
	}
	if slices.Contains(fh.img.ops, "remove "+gid1) {
		t.Fatalf("image removed while it was written: %v", fh.img.ops)
	}
	close(gc.release)
	if r := <-parked; len(r) != 1 || !r[0].OK {
		t.Fatalf("park: %+v", r)
	}
	if r := <-unparked; len(r) != 1 || r[0].Outcome != "unparked" {
		t.Fatalf("unpark after the park: %+v", r)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest not running after the unpark")
	}
}

// With Key Vault down, the start waits one UnwrapTimeout whatever the
// number of images, not one per image.
//
// Asserted on the deadline the unwraps are given, not on how long the
// reconcile took. UnparkAll builds one `uctx` and hands it to every
// image's goroutine, so one timeout for the whole unpark means every
// Unwrap sees the SAME deadline, and one timeout per image would mean
// different ones. That is the claim in the name, and it holds however
// slow the machine is.
//
// It used to assert `elapsed < 550ms` against a 300 ms timeout, which
// is the same claim only if nothing else in the unpark is slow. The test
// deliberately spends 300 ms of that budget in `unwrapWithRetry`, which
// left 250 ms for sweepImageVolumes, the record list, the window
// arithmetic and two volume removals; on a loaded 2-core CI runner
// under -race that was not enough and it failed at 611 ms (CI 41379622622
// on 466badf2). The failure said "2 images, 300 ms timeout" and looked
// exactly like two timeouts being spent, which is the one thing it was
// not: a second timeout is impossible by construction here (I-972).
func TestUnparkWaitsOneUnwrapTimeout(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) { c.Concurrency = 1; c.UnwrapTimeout = 300 * time.Millisecond })
	fh.create(gid1)
	fh.create(gid2)
	if res := fh.m.ParkAll(context.Background(), ParkOperator); len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("park: %+v", res)
	}
	fh.sealer.unwrapErr = context.DeadlineExceeded
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	fh.sealer.mu.Lock()
	deadlines := slices.Clone(fh.sealer.unwrapDeadlines)
	fh.sealer.mu.Unlock()
	if len(deadlines) != 2 {
		t.Fatalf("%d unwraps with a deadline, want one per image: %v", len(deadlines), deadlines)
	}
	if !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("the two images were unwrapped against different deadlines (%s and %s), so the timeout is being spent per image",
			deadlines[0].Format(time.RFC3339Nano), deadlines[1].Format(time.RFC3339Nano))
	}
	if fh.img.has(gid1) || fh.img.has(gid2) {
		t.Fatal("images kept without their key")
	}
}

// An image whose restore cannot start within UnwrapTimeout +
// RestoreTimeout of the unpark is discarded; that guest starts cold.
func TestUnparkRestoreWindowDiscards(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) {
		c.Concurrency = 1
		c.RestoreConcurrency = 1
		c.UnwrapTimeout = 100 * time.Millisecond
		c.RestoreTimeout = 300 * time.Millisecond
		// The window also holds however long the images take to read at
		// the host's rate (I-796); a rate this high leaves the 400 ms
		// this test is about.
		c.WriteRate = 100 << 30
	})
	fh.create(gid1)
	fh.create(gid2)
	if res := fh.m.ParkAll(context.Background(), ParkOperator); len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("park: %+v", res)
	}
	// Whichever restore goes first takes 500 ms to start its hypervisor,
	// past the 400 ms window: it still finishes, the other is discarded.
	prev := fh.sd.OnRun
	var once sync.Once
	fh.sd.OnRun = func(unit string, argv []string) error {
		if slices.Contains(argv, "--restore") {
			once.Do(func() { time.Sleep(500 * time.Millisecond) })
		}
		return prev(unit, argv)
	}
	res := fh.m.UnparkAll(context.Background())
	got := map[string]int{}
	for _, r := range res {
		got[r.Reason]++
	}
	if got["restore window passed"] != 1 || got[""] != 1 {
		t.Fatalf("results %+v", res)
	}
	if fh.img.has(gid1) || fh.img.has(gid2) {
		t.Fatal("image kept")
	}
}

// A guest whose snapshot would run past the deadline is not paused at
// all: it is left running for the shutdown's clean stop.
func TestParkSkipsASnapshotPastDeadline(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30 // 2 GiB at 10 MiB/s: 205 s
	res := fh.parkOne(gid1)
	if res.OK || res.Reason != "snapshot would outlast the deadline" {
		t.Fatalf("park: %+v", res)
	}
	if strings.Contains(fh.chCalls(), "pause ") || !fh.unitActive(gid1) {
		t.Fatal("guest paused or stopped for a snapshot that was not started")
	}
	// The unit's memory, not the class, is what is estimated: 200 MiB fits.
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 200 << 20
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park of a guest using 200 MiB: %+v", r)
	}
}

// The page cache is dropped only in guests that will be parked and have
// the balloon; every guest still syncs.
func TestParkDropsCachesOnlyWithTheBalloon(t *testing.T) {
	execs := func(srv *fakeguestd.Server) string {
		var out []string
		for _, c := range srv.Calls() {
			if c.Req == nil {
				continue
			}
			if e := c.Req.GetExec(); e != nil {
				out = append(out, strings.Join(e.Argv, " "))
			}
		}
		return strings.Join(out, "\n")
	}
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	srv := fh.guestd(gid1) // the hypervisor's guestd goes with it
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	if e := execs(srv); !strings.Contains(e, "sync") || strings.Contains(e, "drop_caches") {
		t.Fatalf("guest without the balloon ran %q, want a sync and no cache drop", e)
	}

	fh = newParkHarness(t, nil)
	fh.create(gid1)
	args := filepath.Join(fh.m.guestDir(gid1), "ch.args")
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(args, append(b, []byte("--balloon\n"+ch.Balloon+"\n")...), 0o640); err != nil {
		t.Fatal(err)
	}
	srv = fh.guestd(gid1)
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	if e := execs(srv); !strings.Contains(e, "drop_caches") {
		t.Fatalf("guest with the balloon ran %q, want a cache drop", e)
	}
}

// Guests are snapshotted smallest first, in that order, so a deadline that
// cannot hold them all parks as many as it can. On host-stg-01
// (2026-10-12, 4 guests, 125 MB/s disk) the xl took a slot ahead of a
// small one, and the small and the large were left out at the deadline.
func TestParkStartsSmallestFirst(t *testing.T) {
	for i := range 10 {
		fh := newParkHarness(t, func(c *ParkConfig) { c.Concurrency = 1 })
		for _, g := range []struct{ id, class string }{{gid1, "xl"}, {gid2, "small"}} {
			req := createReq(g.id)
			req.Class = g.class
			req.SystemClosure = fh.closure
			fh.mustOK(cmd(req))
			fh.sd.Units[GuestUnit(g.id)].MemBytes = 200 << 20
		}
		fh.chc.Calls = nil
		for _, r := range fh.m.ParkAll(context.Background(), ParkOperator) {
			if !r.OK {
				t.Fatalf("run %d: park: %+v", i, r)
			}
		}
		var order []string
		for _, c := range fh.chc.Calls {
			if strings.HasPrefix(c, "pause ") {
				if strings.Contains(c, gid1) {
					order = append(order, "xl")
				} else if strings.Contains(c, gid2) {
					order = append(order, "small")
				}
			}
		}
		if !slices.Equal(order, []string{"small", "xl"}) {
			t.Fatalf("run %d: paused in the order %v, want small before xl", i, order)
		}
	}
}

// hostd killed after a guest's snapshot finished and its hypervisor
// exited, before the image was closed (host-stg-01, 2026-10-12: kill -9
// 38 s into a park of 4 guests): the image is whole, so the next start
// restores the guest from it rather than leaving it stopped with its
// memory gone.
func TestKilledAfterTheSnapshotRestoresTheGuest(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.m.cutPark = func(_, at string) bool { return at == "stopped" }
	fh.m.ParkAll(context.Background(), ParkOperator)
	fh.m.cutPark = nil
	if fh.unitActive(gid1) {
		t.Fatal("hypervisor still running at the cut")
	}
	res := fh.m.UnparkAll(context.Background())
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("unpark after a kill past the snapshot: %+v", res)
	}
	if !fh.unitActive(gid1) || fh.guest(gid1).State != StateRunning {
		t.Fatal("guest not running after the unpark")
	}
	if argv := strings.Join(fh.unitArgv(gid1), " "); !strings.Contains(argv, "--restore source_url=file://") {
		t.Fatalf("hypervisor started with %q, want a restore", argv)
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image or record left after the restore")
	}
}

// Killed before the snapshot finished, the image is never trusted: the
// guest's hypervisor is gone with it only if hostd stopped it, which it
// does after the snapshot; before that the guest is resumed (above).
func TestKilledMidSnapshotNeverRestores(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if err := fh.m.records().Put(&park.Record{GuestID: gid1, State: park.StateWriting, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fh.img.vols[gid1] = "x"
	fh.sd.Exit(GuestUnit(gid1), 0)
	res := fh.m.UnparkAll(context.Background())
	if len(res) != 1 || res[0].OK || res[0].Reason != "image incomplete" {
		t.Fatalf("unpark of an image cut mid-snapshot: %+v", res)
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("broken image kept")
	}
}

// hostd killed while a guest's VM is paused for its snapshot: the guest is
// not resumed (a VM resumed after a snapshot loses its virtio-fs; host-stg-01,
// 2026-10-12) but parked again and restored from the image.
func TestKilledWhilePausedRestoresInsteadOfResuming(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.m.cutPark = func(_, at string) bool { return at == "snapshot" }
	fh.m.ParkAll(context.Background(), ParkOperator)
	fh.m.cutPark = nil
	if !fh.unitActive(gid1) {
		t.Fatal("hypervisor gone at the cut")
	}
	before := len(fh.chc.Calls)
	res := fh.m.UnparkAll(context.Background())
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("unpark after a kill while paused: %+v", res)
	}
	calls := strings.Join(fh.chc.Calls[before:], "\n")
	if !strings.Contains(calls, "snapshot ") || !strings.Contains(calls, "shutdown ") {
		t.Fatalf("paused VM not parked again; hypervisor calls %q", calls)
	}
	if argv := strings.Join(fh.unitArgv(gid1), " "); !strings.Contains(argv, "--restore source_url=file://") {
		t.Fatalf("hypervisor started with %q, want a restore", argv)
	}
	if !fh.unitActive(gid1) || fh.guest(gid1).State != StateRunning {
		t.Fatal("guest not running after the unpark")
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image or record left after the restore")
	}
}

// The record names the image's filesystem, and the restore mounts that:
// a new image is the compressed btrfs of I-685, and an image written by
// the release before (ext4, no filesystem in its record) is still
// restored, as docs/interfaces promises for one release.
func TestParkRecordsTheImageFilesystem(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if res := fh.parkOne(gid1); !res.OK {
		t.Fatalf("park: %+v", res)
	}
	if rec := fh.record(gid1); rec.FS != park.FSBtrfs {
		t.Fatalf("record says the image is %q", rec.FS)
	}
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fh.img.openedAs[gid1]; got != park.FSBtrfs {
		t.Fatalf("image opened as %q", got)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest not restored")
	}
}

func TestUnparkRestoresAnImageFromTheReleaseBefore(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if res := fh.parkOne(gid1); !res.OK {
		t.Fatalf("park: %+v", res)
	}
	// What the previous release left: an ext4 image and a record with no
	// filesystem in it.
	rec := fh.record(gid1)
	rec.FS = ""
	if err := fh.m.records().Put(rec); err != nil {
		t.Fatal(err)
	}
	fh.img.fs[gid1] = park.FSExt4
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fh.img.openedAs[gid1]; got != park.FSExt4 {
		t.Fatalf("an image from the release before was opened as %q", got)
	}
	if !fh.unitActive(gid1) || fh.img.has(gid1) {
		t.Fatalf("guest restored=%v image kept=%v", fh.unitActive(gid1), fh.img.has(gid1))
	}
}

// Restores run RestoreConcurrency at a time, which is more than the
// park's Concurrency: a compressed image is read back by one core
// decompressing it, so restoring more guests at once finishes them all
// sooner (I-685). Before, both shared the one Concurrency and a host that
// parked two at a time restored two at a time.
func TestRestoresRunMoreAtOnceThanParks(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) {
		c.Concurrency = 1
		c.RestoreConcurrency = 2
		c.RestoreTimeout = 10 * time.Second
	})
	fh.create(gid1)
	fh.create(gid2)
	if res := fh.m.ParkAll(context.Background(), ParkOperator); len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("park: %+v", res)
	}
	var mu sync.Mutex
	now, most := 0, 0
	prev := fh.sd.OnRun
	fh.sd.OnRun = func(unit string, argv []string) error {
		if !slices.Contains(argv, "--restore") {
			return prev(unit, argv)
		}
		mu.Lock()
		now++
		if now > most {
			most = now
		}
		mu.Unlock()
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		now--
		mu.Unlock()
		return prev(unit, argv)
	}
	res := fh.m.UnparkAll(context.Background())
	if len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("unpark: %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if most != 2 {
		t.Fatalf("at most %d restore(s) ran at once; RestoreConcurrency is 2", most)
	}
}

// Four parks and four restores at once by default (I-685), and never
// fewer restores than parks.
func TestParkConfigDefaultsConcurrency(t *testing.T) {
	c := ParkConfig{}.withDefaults()
	if c.Concurrency != 4 || c.RestoreConcurrency != 4 {
		t.Fatalf("defaults: park %d, restore %d", c.Concurrency, c.RestoreConcurrency)
	}
	if got := (ParkConfig{Concurrency: 6}).withDefaults(); got.RestoreConcurrency != 6 {
		t.Fatalf("with 6 parks at once, %d restores", got.RestoreConcurrency)
	}
	if got := (ParkConfig{RestoreConcurrency: 1}).withDefaults(); got.RestoreConcurrency != got.Concurrency {
		t.Fatalf("fewer restores (%d) than parks (%d) at once", got.RestoreConcurrency, got.Concurrency)
	}
}

// An image volume has room for the worst image a guest can write: every
// page of its memory, none of it compressible. btrfs allocates data in
// chunks of up to 1 GiB and needs a metadata and a system chunk besides,
// so the volume is the class's memory rounded up to a chunk plus room for
// those (I-685). The volume is thin: what it does not write costs nothing.
func TestImageVolumeHasRoomForAnIncompressibleImage(t *testing.T) {
	const gib = uint64(1) << 30
	for _, class := range []string{"small", "large", "xl"} {
		mem := uint64(Classes[class].MemMiB) << 20
		got := imageVolumeBytes(class)
		need := (mem+gib-1)/gib*gib + 288<<20
		if got < need {
			t.Fatalf("%s: image volume %d bytes, an incompressible image needs %d", class, got, need)
		}
	}
}

// The deadline must hold the classes repose sells. On 2026-10-18 host-01
// was rebooted with a 120 s deadline and its xl guest was skipped:
// "snapshot would outlast the deadline". It cold-booted and every
// agent session in it was lost (DECISIONS I-796). The estimate is the
// guest's resident memory at the host's write rate, so this is the table
// the deadline has to pass: each class at the memory its unit can hold,
// against the 400 MB/s host-01's guest pool measured.
func TestParkTakesEveryClassAtItsResidentSize(t *testing.T) {
	const hostRate = 400_000_000 // host-01's measured pool rate (I-796)
	const oldRate = 100_000_000  // what the code assumed before it
	for _, c := range []struct {
		name     string
		class    string
		resident uint64
		rate     uint64
		deadline time.Duration // 0 is the shipped default
		want     string
	}{
		{"small", "small", 4 << 30, hostRate, 0, "parked"},
		{"large", "large", 8 << 30, hostRate, 0, "parked"},
		{"xl", "xl", 16 << 30, hostRate, 0, "parked"},
		// An xl fits the shipped deadline even at the old assumed rate,
		// which the deadline alone used not to do.
		{"xl at the assumed 100 MB/s", "xl", 16 << 30, oldRate, 0, "parked"},
		// What the owner lost: the deadline and the rate that shipped
		// before I-796 cannot hold an xl at any load.
		{"xl as it shipped before: 120 s at 100 MB/s", "xl", 16 << 30, oldRate, 120 * time.Second, "skipped"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fh := newParkHarness(t, func(f *ParkConfig) {
				f.WriteRate = c.rate
				f.Deadline = c.deadline
			})
			req := createReq(gid1)
			req.SystemClosure, req.Class = fh.closure, c.class
			fh.mustOK(cmd(req))
			fh.sd.Units[GuestUnit(gid1)].MemBytes = c.resident
			res := fh.parkOne(gid1)
			if res.Outcome != c.want {
				t.Fatalf("a %s guest with %d GiB resident: %+v, want %s", c.class, c.resident>>30, res, c.want)
			}
			if c.want == "parked" && !res.OK {
				t.Fatalf("%+v", res)
			}
		})
	}
}

// skippedReason is what turns a result into the counter GuestNotParked
// reads, so every outcome a park can produce is named here: the three
// that leave a running guest without its memory, and the ones that cost
// nothing.
func TestSkippedReasonNamesEveryResult(t *testing.T) {
	for _, c := range []struct {
		res  ParkResult
		want string
	}{
		{ParkResult{OK: true, Outcome: "parked"}, ""},
		{ParkResult{Outcome: "skipped", Reason: "pool has no room for the image"}, "no_room"},
		{ParkResult{Outcome: "skipped", Reason: "deadline passed"}, "deadline"},
		{ParkResult{Outcome: "skipped", Reason: "snapshot would outlast the deadline"}, "estimate"},
		{ParkResult{Outcome: "failed", Reason: "snapshot failed"}, "failed"},
		{ParkResult{Outcome: "failed", Reason: "snapshot failed; guest still paused"}, "failed"},
		// Nothing was lost: the guest had no memory to keep, or its
		// image was already written.
		{ParkResult{Outcome: "skipped", Reason: "guest is not running"}, ""},
		{ParkResult{Outcome: "skipped", Reason: "guest already parked"}, ""},
	} {
		if got := skippedReason(c.res); got != c.want {
			t.Errorf("%+v: reason %q, want %q", c.res, got, c.want)
		}
	}
}

// Every path that leaves a running guest skipped counts it: the skip was
// a Warn line and a row in a table nobody read, so nothing alerted when
// host-01 lost its xl guest (I-796).
func TestParkCountsEveryGuestItLeavesSkipped(t *testing.T) {
	skipped := func(fh *parkHarness, why string) float64 {
		return testutil.ToFloat64(fh.metrics.GuestParkSkipped.WithLabelValues(why))
	}

	t.Run("estimate", func(t *testing.T) {
		fh := newParkHarness(t, func(c *ParkConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
		fh.create(gid1)
		fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30 // 205 s at 10 MiB/s
		if r := fh.parkOne(gid1); r.Reason != "snapshot would outlast the deadline" {
			t.Fatalf("park: %+v", r)
		}
		if n := skipped(fh, "estimate"); n != 1 {
			t.Fatalf("estimate counter %v, want 1", n)
		}
	})

	t.Run("deadline passed", func(t *testing.T) {
		// The deadline passes while the guests write back their caches,
		// so no snapshot is started at all.
		fh := newParkHarness(t, func(c *ParkConfig) { c.Deadline = 150 * time.Millisecond })
		fh.gopts.Delay = map[string]time.Duration{"Exec": 400 * time.Millisecond}
		fh.create(gid1)
		if r := fh.parkOne(gid1); r.Reason != "deadline passed" {
			t.Fatalf("park: %+v", r)
		}
		if n := skipped(fh, "deadline"); n != 1 {
			t.Fatalf("deadline counter %v, want 1", n)
		}
	})

	t.Run("no room in the pool", func(t *testing.T) {
		fh := newParkHarness(t, nil)
		fh.create(gid1)
		fh.lvm.PoolSize, fh.lvm.PoolFree = 100<<30, 21<<30 // 6 GiB of room, 8 needed
		if r := fh.parkOne(gid1); r.Reason != "pool has no room for the image" {
			t.Fatalf("park: %+v", r)
		}
		if n := skipped(fh, "no_room"); n != 1 {
			t.Fatalf("no_room counter %v, want 1", n)
		}
	})

	t.Run("failed after it started", func(t *testing.T) {
		fh := newParkHarness(t, nil)
		fh.create(gid1)
		fh.chc.Fail = map[string]error{"snapshot": errors.New("No space left on device")}
		if r := fh.parkOne(gid1); r.Outcome != "failed" {
			t.Fatalf("park: %+v", r)
		}
		if n := skipped(fh, "failed"); n != 1 {
			t.Fatalf("failed counter %v, want 1", n)
		}
	})

	t.Run("no image key", func(t *testing.T) {
		fh := newParkHarness(t, nil)
		fh.create(gid1)
		fh.sealer.notReady = errors.New("key vault public key not fetched yet")
		if res := fh.m.ParkAll(context.Background(), ParkShutdown); len(res) != 0 {
			t.Fatalf("results without a key: %+v", res)
		}
		if n := skipped(fh, "no_key"); n != 1 {
			t.Fatalf("no_key counter %v, want 1", n)
		}
	})
}

// A shutdown park's counter is never scraped: the host goes down behind
// it. The running total is kept beside the records and published at the
// next start, which is where Prometheus can see it (I-796).
//
// Each start here gets a fresh registry, because that is what a new hostd
// has: its counters begin at zero. The test this replaces kept the same
// Manager and the same registry across the "restart" and so asserted a
// rise (1 -> 2) that production could never produce -- which is why the
// defect below went unnoticed. host-01's own pattern is the same xl guest
// skipped at every reboot: unless the series rises each time,
// increase() sees 1, 1, 1 and nothing alerts.
func TestTheSkippedTallyRisesAtEveryStart(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30 // 205 s at 10 MiB/s
	count := func() float64 {
		fh.t.Helper()
		return testutil.ToFloat64(fh.m.d.Metrics.GuestParkSkipped.WithLabelValues("estimate"))
	}
	if r := fh.m.ParkAll(context.Background(), ParkShutdown); len(r) != 1 || r[0].OK {
		t.Fatalf("park: %+v", r)
	}
	if n := count(); n != 1 {
		t.Fatalf("counter after the first park %v, want 1", n)
	}
	fh.nextStart("boot-b")
	if n := count(); n != 1 {
		t.Fatalf("counter at the next start %v, want the host's total of 1", n)
	}
	// The same guest skipped by the next restart's park: the total has
	// to rise, or the second loss is invisible.
	if r := fh.m.ParkAll(context.Background(), ParkShutdown); len(r) != 1 || r[0].OK {
		t.Fatalf("second park: %+v", r)
	}
	if n := count(); n != 2 {
		t.Fatalf("counter after the second park %v, want 2", n)
	}
	fh.nextStart("boot-c")
	if n := count(); n != 2 {
		t.Fatalf("counter at the start after it %v, want the host's total of 2", n)
	}
	fh.nextStart("boot-c")
	if n := count(); n != 2 {
		t.Fatalf("counter after a start that parked nothing %v, want 2", n)
	}
	// The line that says those guests started cold belongs to the start
	// that found them: twice would read as two restarts.
	if n := strings.Count(fh.logs.String(), "started cold after the host restarted"); n != 2 {
		t.Fatalf("%d cold-start lines for two restarts that each lost a guest", n)
	}
}

// A park for an event that was then called off leaves its counts
// behind, but the host never restarted: this process already counted
// them, so the unpark that brings the guests back must not count them
// again or say they started cold.
func TestATallyFromThisBootIsNotReportedAsALoss(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30
	// hostd's own start, which is the unpark that publishes the host's
	// totals; every later unpark in this process adds nothing.
	fh.m.UnparkAll(context.Background())
	fh.m.ParkAll(context.Background(), ParkScheduledEvent)
	fh.m.UnparkAll(context.Background())
	if n := testutil.ToFloat64(fh.metrics.GuestParkSkipped.WithLabelValues("estimate")); n != 1 {
		t.Fatalf("counter %v, want the one count the park made", n)
	}
	if strings.Contains(fh.logs.String(), "started cold after the host restarted") {
		t.Errorf("a park this boot never restarted after was reported as a loss:\n%s", fh.logs.String())
	}
}

// A park that saved every guest clears what an earlier one left to
// say. Without this, a called-off scheduled event's counts stay on disk
// and the next reboot -- whose park saved everything -- reports them
// again, with an Error line claiming guests were lost (I-796).
func TestAParkThatSavesEveryGuestClearsTheLastTally(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30
	fh.m.ParkAll(context.Background(), ParkScheduledEvent) // skipped: the estimate
	// The event was called off and the guest resumed; a later restart
	// parked it with room to spare.
	fh.m.UnparkAll(context.Background())
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 1 << 20
	if r := fh.m.ParkAll(context.Background(), ParkShutdown); len(r) != 1 || !r[0].OK {
		t.Fatalf("second park: %+v", r)
	}
	fh.nextStart("boot-b")
	if strings.Contains(fh.logs.String(), "started cold after the host restarted") {
		t.Errorf("a successful park reported the earlier one's loss:\n%s", fh.logs.String())
	}
	if n := testutil.ToFloat64(fh.m.d.Metrics.GuestParkSkipped.WithLabelValues("estimate")); n != 1 {
		t.Fatalf("total %v, want the one guest the first park left", n)
	}
}

// Snapshots share one disk, so what a guest's snapshot has to fit inside
// is every snapshot already running plus its own. Each one was compared
// against the whole remaining deadline on its own, and up to Concurrency
// of them take their slot at the same instant with the whole deadline
// ahead, so the rate a finished snapshot teaches could not correct the
// first batch: on a host with no more running guests than Concurrency --
// every host repose runs -- the deadline was not enforced by this code
// at all, only by systemd's SIGKILL (I-796).
func TestAdmissionCountsTheSnapshotsAlreadyRunning(t *testing.T) {
	w := newWriteRate(100 << 20) // 10.24 s a GiB
	const left = 25 * time.Second
	if _, ok := w.admit(1<<30, left); !ok {
		t.Fatal("the first snapshot was refused")
	}
	if _, ok := w.admit(1<<30, left); !ok {
		t.Fatal("the second snapshot was refused; two fit 25 s")
	}
	if est, ok := w.admit(1<<30, left); ok {
		t.Fatalf("a third snapshot admitted at %s: three of them are 30.7 s of writing in a 25 s deadline", est)
	}
	// A finished one gives its reservation back, so a guest whose slot
	// comes free late is admitted against what is still running.
	w.release(1 << 30)
	if _, ok := w.admit(1<<30, left); !ok {
		t.Fatal("the slot a finished snapshot freed was not reused")
	}
}

// The same, through ParkAll: three guests whose snapshots each fit the
// deadline alone but not together, held so that they are genuinely in
// flight at the same time.
func TestAParkAdmitsOnlyWhatTheDeadlineHolds(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) {
		c.WriteRate = 100 << 20
		c.Deadline = 25 * time.Second
	})
	ids := []string{gid1, gid2, gid3}
	for _, id := range ids {
		fh.create(id)
		fh.sd.Units[GuestUnit(id)].MemBytes = 1 << 30 // 10.24 s each
	}
	hc := heldCH{parkCH: fh.m.d.CH.(parkCH), in: make(chan string, len(ids)), release: make(chan struct{})}
	fh.m.d.CH = hc
	done := make(chan []ParkResult, 1)
	go func() { done <- fh.m.ParkAll(context.Background(), ParkOperator) }()
	<-hc.in
	<-hc.in
	close(hc.release)
	res := <-done

	parked, left := 0, 0
	for _, r := range res {
		switch {
		case r.OK:
			parked++
		case r.Reason == "snapshot would outlast the deadline":
			left++
		default:
			t.Fatalf("unexpected result %+v", r)
		}
	}
	if parked != 2 || left != 1 {
		t.Fatalf("parked %d, left for the deadline %d; want 2 and 1 (%+v)", parked, left, res)
	}
	if n := strings.Count(fh.chCalls(), "snapshot "); n != 2 {
		t.Fatalf("%d snapshots started, want 2: %s", n, fh.chCalls())
	}
	if n := testutil.ToFloat64(fh.metrics.GuestParkSkipped.WithLabelValues("estimate")); n != 1 {
		t.Fatalf("estimate counter %v, want 1", n)
	}
}

// A running guest whose unit cannot be asked is still parked, or still
// counted. Dropping it -- which is what discarding IsActive's error did
// -- left it out of the park, out of the results and out of the tally,
// so the operator saw one fewer guest and no loss (I-796).
func TestAGuestWhoseUnitCannotBeAskedIsNotDropped(t *testing.T) {
	fh := newParkHarness(t, nil)
	// Installed before the guest exists, and switched on after it: the
	// monitor `create` starts reads m.d.Systemd in checkUnits, so
	// assigning the field with that goroutine running is a data race
	// (DECISIONS I-858). The field is written once, before anything reads
	// it, and the behaviour is changed through an atomic instead.
	sd := &switchableIsActive{Systemd: fh.sd}
	fh.m.d.Systemd = sd
	fh.create(gid1)
	sd.failing.Store(true)
	res := fh.m.ParkAll(context.Background(), ParkOperator)
	if len(res) != 1 {
		t.Fatalf("results %+v, want the guest the park could not ask about", res)
	}
	if !res[0].OK {
		t.Fatalf("result %+v, want it parked", res[0])
	}
}

// failingIsActive is a systemd whose IsActive errors, as dbus does under
// a shutdown's load.
// switchableIsActive fails IsActive once `failing` is set. A test that
// wants IsActive to start failing switches it rather than assigning
// m.d.Systemd, which the monitor goroutine reads (DECISIONS I-858);
// production never reassigns that field once a manager runs, so the race
// belonged to the test and the fix belongs there too.
type switchableIsActive struct {
	systemd.Systemd
	failing atomic.Bool
}

func (f *switchableIsActive) IsActive(ctx context.Context, unit string) (bool, error) {
	if f.failing.Load() {
		return false, errors.New("dbus: connection reset")
	}
	return f.Systemd.IsActive(ctx, unit)
}

// A park stops waiting at its own wall rather than letting systemd cut
// the ExecStop: hostd killed there counts nothing and writes no tally,
// which is the one way to lose a guest and have nothing say so (I-796).
func TestAParkStopsWaitingAtItsWall(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) {
		c.Deadline = 2 * time.Second
		c.StopGrace = time.Second
		c.ResumeWait = time.Minute // what the wall has to override
	})
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 1 << 20 // admitted: 10 ms of writing
	// A hypervisor that never answers the snapshot, as one mid-snapshot
	// does not. Without the wall this waits snapshotTimeout, 10 minutes,
	// and systemd cuts the ExecStop long before that.
	fh.m.d.CH = hangingCH{fh.m.d.CH.(parkCH)}
	start := time.Now()
	res := fh.m.ParkAll(context.Background(), ParkShutdown)
	took := time.Since(start)
	if took > 10*time.Second {
		t.Fatalf("the park took %s; its wall is 3 s and snapshotTimeout is 10 min", took)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatalf("results %+v, want the guest reported skipped", res)
	}
	if n := testutil.ToFloat64(fh.metrics.GuestParkSkipped.WithLabelValues("failed")); n != 1 {
		t.Fatalf("failed counter %v, want the guest counted before the park returned", n)
	}
}

// unparkDiscardedReason is what turns an unpark result into the counter
// GuestImageDiscarded reads. A guest whose image the next start throws
// away loses exactly what one the park never took loses, and that half
// of the loss was a Warn line with no metric at all (I-796).
func TestUnparkDiscardedReasonNamesEveryResult(t *testing.T) {
	for _, c := range []struct {
		res  ParkResult
		want string
	}{
		{ParkResult{OK: true, Outcome: "unparked"}, ""},
		{ParkResult{Outcome: "discarded", Reason: "image incomplete"}, "incomplete"},
		{ParkResult{Outcome: "discarded", Reason: "record unreadable"}, "incomplete"},
		{ParkResult{Outcome: "discarded", Reason: "image older than the restore window"}, "too_old"},
		{ParkResult{Outcome: "discarded", Reason: "closure changed since the park"}, "changed"},
		{ParkResult{Outcome: "discarded", Reason: "class changed since the park"}, "changed"},
		{ParkResult{Outcome: "discarded", Reason: "volume resized since the park"}, "changed"},
		{ParkResult{Outcome: "discarded", Reason: "guest volume changed since the park"}, "changed"},
		{ParkResult{Outcome: "discarded", Reason: "no volume fingerprint"}, "changed"},
		{ParkResult{Outcome: "discarded", Reason: "hypervisor version changed since the park"}, "changed"},
		{ParkResult{Outcome: "discarded", Reason: "image key unavailable"}, "no_key"},
		{ParkResult{Outcome: "discarded", Reason: "image key does not match"}, "no_key"},
		{ParkResult{Outcome: "discarded", Reason: "restore window passed"}, "window"},
		{ParkResult{Outcome: "discarded", Reason: "restore failed"}, "failed"},
		{ParkResult{Outcome: "discarded", Reason: "image volume not opened"}, "failed"},
		{ParkResult{Outcome: "discarded", Reason: "guest volume unavailable"}, "failed"},
		{ParkResult{Outcome: "discarded", Reason: "hypervisor client cannot restore"}, "failed"},
		// Nothing was owed to these: the guest is running, gone from this
		// host, or was stopped by its owner meanwhile.
		{ParkResult{Outcome: "discarded", Reason: "guest already running"}, ""},
		{ParkResult{Outcome: "discarded", Reason: "guest no longer on this host"}, ""},
		{ParkResult{Outcome: "discarded", Reason: "guest state changed since the park"}, ""},
		{ParkResult{Outcome: "discarded", Reason: "park turned off"}, ""},
		{ParkResult{Outcome: "skipped", Reason: "guest still paused; image kept"}, ""},
	} {
		if got := unparkDiscardedReason(c.res); got != c.want {
			t.Errorf("%+v: reason %q, want %q", c.res, got, c.want)
		}
	}
}

// A guest parked and then not restored started cold with its agents
// gone, so it is counted and the total survives the start that counted
// it: the unpark runs before the metrics listener is up, so a per-process
// count would read the same after every reboot (I-796).
func TestAUnparkThatDiscardsAnImageCountsTheGuest(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park: %+v", r)
	}
	// The guest's closure moved under the image while the host was down.
	g := fh.guest(gid1)
	g.SystemClosure = fakeClosure(t, "nixos-system-v2")
	if err := fh.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	fh.nextStart("boot-b")
	if n := testutil.ToFloat64(fh.m.d.Metrics.GuestUnparkDiscarded.WithLabelValues("changed")); n != 1 {
		t.Fatalf("changed counter %v, want 1", n)
	}
	// A new hostd at the start after it publishes the host's total, not
	// its own count of zero.
	fh.nextStart("boot-c")
	if n := testutil.ToFloat64(fh.m.d.Metrics.GuestUnparkDiscarded.WithLabelValues("changed")); n != 1 {
		t.Fatalf("counter at the next start %v, want the host's total of 1", n)
	}
}

// ctxSystemd refuses once its context is done, as the real one does over
// dbus. The fake ignores the context, so a count taken on a deadline that
// has already passed looks right there and finds nothing on a host.
type ctxSystemd struct{ systemd.Systemd }

func (c ctxSystemd) IsActive(ctx context.Context, unit string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return c.Systemd.IsActive(ctx, unit)
}

// A park that never gets the lock (an unpark or an operator's park still
// running) leaves every running guest skipped, and lists them itself to
// say how many. Its own deadline has passed by then, so the listing needs
// a context of its own.
func TestAParkThatCannotStartCountsTheGuestsItLeaves(t *testing.T) {
	fh := newParkHarness(t, func(c *ParkConfig) { c.Deadline = 50 * time.Millisecond })
	// Before the guest, so the monitor `create` starts never sees the
	// field change under it (DECISIONS I-858). This wrapper is inert
	// until a context is cancelled, so installing it early changes
	// nothing about the boot.
	fh.m.d.Systemd = ctxSystemd{fh.sd}
	fh.create(gid1)
	fh.m.parkMu <- struct{}{} // held by something else
	defer func() { <-fh.m.parkMu }()
	if res := fh.m.ParkAll(context.Background(), ParkShutdown); len(res) != 0 {
		t.Fatalf("results: %+v", res)
	}
	if n := testutil.ToFloat64(fh.metrics.GuestParkSkipped.WithLabelValues("busy")); n != 1 {
		t.Fatalf("busy counter %v, want 1", n)
	}
}
