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
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/fakeguestd"
	"github.com/heracraft/repose/internal/hostd/freeze"
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
		madeAs: (&freeze.Image{}).FS()}
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
	f.vols[id], f.open[id], f.created[id] = freeze.KeyCheck(key), true, size
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
	if kc != freeze.KeyCheck(key) {
		return errors.New("mount: wrong fs type")
	}
	if fs == "" {
		fs = freeze.FSExt4
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
	mu        sync.Mutex
}

func (s *fakeSealer) Ready() error                  { return s.notReady }
func (s *fakeSealer) Refresh(context.Context) error { return nil }
func (s *fakeSealer) Wrap(key []byte) ([]byte, string, error) {
	w := slices.Clone(key)
	slices.Reverse(w)
	return w, "v1", nil
}
func (s *fakeSealer) Unwrap(_ context.Context, w []byte, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unwraps++
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

// freezeCH is the fake hypervisor client with what a freeze needs: a
// shutdown ends the unit (as the real vmm.shutdown does), and a version.
type freezeCH struct {
	*ch.Fake
	h *harness
}

func (c freezeCH) Shutdown(ctx context.Context, sock string) error {
	err := c.Fake.Shutdown(ctx, sock)
	for _, id := range []string{gid1, gid2} {
		if strings.Contains(sock, id) {
			c.h.sd.Exit(GuestUnit(id), 0)
		}
	}
	return err
}

type freezeHarness struct {
	*harness
	img    *fakeImage
	sealer *fakeSealer
	fp     map[string]string
	fpMu   sync.Mutex
}

func newFreezeHarness(t *testing.T, mut func(*FreezeConfig)) *freezeHarness {
	t.Helper()
	recDir := t.TempDir()
	h := newHarness(t, func(c *Config) {
		c.Freeze = FreezeConfig{Enabled: true, RecordDir: recDir, Settle: -1, UnwrapTimeout: 200 * time.Millisecond, RestoreTimeout: 3 * time.Second}
		if mut != nil {
			mut(&c.Freeze)
		}
	})
	fh := &freezeHarness{harness: h, img: newFakeImage(), sealer: &fakeSealer{}, fp: map[string]string{}}
	h.chc.Ver = "cloud-hypervisor v53.0.0"
	h.m.d.CH = freezeCH{Fake: h.chc, h: h}
	h.m.d.Freeze = FreezeDeps{
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

func (fh *freezeHarness) setFingerprint(id, v string) {
	fh.fpMu.Lock()
	defer fh.fpMu.Unlock()
	fh.fp[fh.lvm.DevPath(VolumeName(id))] = v
}

func (fh *freezeHarness) record(id string) *freeze.Record {
	fh.t.Helper()
	r, err := fh.m.records().Get(id)
	if err != nil {
		fh.t.Fatal(err)
	}
	return r
}

func (fh *freezeHarness) unitActive(id string) bool {
	a, _ := fh.sd.IsActive(context.Background(), GuestUnit(id))
	return a
}

func (fh *freezeHarness) unitArgv(id string) []string {
	fh.sd.Units[GuestUnit(id)].Argv = slices.Clone(fh.sd.Units[GuestUnit(id)].Argv)
	return fh.sd.Units[GuestUnit(id)].Argv
}

func (fh *freezeHarness) freezeOne(id string) FreezeResult {
	fh.t.Helper()
	res := fh.m.FreezeAll(context.Background(), FreezeOperator)
	for _, r := range res {
		if r.GuestID == id {
			return r
		}
	}
	fh.t.Fatalf("no freeze result for %s: %+v", id, res)
	return FreezeResult{}
}

func (fh *freezeHarness) chCalls() string {
	return strings.Join(fh.chc.Calls, "\n")
}

// A frozen guest is paused, snapshotted into its image and its hypervisor
// stopped, its state still running; the next reconcile restores it from
// the image (not a boot), sets its clock, and the image is gone.
func TestFreezeThenThawRestoresTheGuest(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	res := fh.freezeOne(gid1)
	if !res.OK || res.Outcome != "frozen" {
		t.Fatalf("freeze: %+v", res)
	}
	if fh.unitActive(gid1) {
		t.Fatal("hypervisor still running after the freeze")
	}
	if g := fh.guest(gid1); g.State != StateRunning {
		t.Fatalf("state %s after the freeze, want running", g.State)
	}
	rec := fh.record(gid1)
	if rec == nil || rec.State != freeze.StateComplete || rec.Fingerprint != "sb-1" || rec.CHVersion == "" || len(rec.WrappedKey) != freeze.KeyBytes {
		t.Fatalf("record %+v", rec)
	}
	calls := fh.chCalls()
	for _, want := range []string{"pause ", "snapshot ", "shutdown "} {
		if !strings.Contains(calls, want) {
			t.Fatalf("no %q among the hypervisor calls:\n%s", want, calls)
		}
	}
	if !strings.Contains(strings.Join(fh.img.ops, " "), "close "+gid1) {
		t.Fatalf("image not closed after the freeze: %v", fh.img.ops)
	}
	if fh.img.created[gid1] != (8192+1536)<<20 {
		t.Fatalf("image volume of %d bytes for a large guest", fh.img.created[gid1])
	}

	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest not running after the thaw")
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

// Whatever changed the guest since its freeze, its image is not restored:
// the image goes and reconcile handles the guest as after any restart.
func TestThawDiscardsAStaleImage(t *testing.T) {
	cases := map[string]func(fh *freezeHarness){
		"closure": func(fh *freezeHarness) {
			g := fh.guest(gid1)
			g.SystemClosure = fakeClosure(fh.t, "nixos-system-v2")
			_ = fh.st.PutGuest(g)
		},
		"written": func(fh *freezeHarness) { fh.setFingerprint(gid1, "sb-2") },
		"resized": func(fh *freezeHarness) {
			g := fh.guest(gid1)
			g.VolumeBytes += 1 << 30
			_ = fh.st.PutGuest(g)
		},
		"chver": func(fh *freezeHarness) { fh.chc.Ver = "cloud-hypervisor v54.0.0" },
		"partial": func(fh *freezeHarness) {
			r := fh.record(gid1)
			r.State = freeze.StateWriting
			_ = fh.m.records().Put(r)
		},
		"old": func(fh *freezeHarness) {
			r := fh.record(gid1)
			r.DoneAt = r.DoneAt.Add(-7 * time.Hour)
			_ = fh.m.records().Put(r)
		},
		"nokey": func(fh *freezeHarness) { fh.sealer.unwrapErr = errors.New("vault unreachable") },
		"badkey": func(fh *freezeHarness) {
			r := fh.record(gid1)
			r.WrappedKey[0] ^= 0xff
			_ = fh.m.records().Put(r)
		},
		"stopped": func(fh *freezeHarness) {
			g := fh.guest(gid1)
			g.State = StateStopped
			_ = fh.st.PutGuest(g)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			fh := newFreezeHarness(t, nil)
			fh.create(gid1)
			if r := fh.freezeOne(gid1); !r.OK {
				t.Fatalf("freeze: %+v", r)
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
func TestFreezeFailureResumesTheGuest(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.chc.Fail = map[string]error{"snapshot": errors.New("No space left on device")}
	res := fh.freezeOne(gid1)
	if res.OK || res.Reason != "snapshot failed" {
		t.Fatalf("freeze: %+v", res)
	}
	if !strings.Contains(fh.chCalls(), "resume ") {
		t.Fatal("paused guest never resumed")
	}
	if !fh.unitActive(gid1) || fh.guest(gid1).State != StateRunning {
		t.Fatal("guest not running after a failed freeze")
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image or record left by a failed freeze")
	}
	// It is still a live guest: commands reach it.
	if _, err := fh.m.session(gid1); err != nil {
		t.Fatalf("no guestd session after a failed freeze: %v", err)
	}
}

// A restore whose hypervisor does not start falls back: no image left, the
// guest is settled by reconcile as after any restart (L6 starts it cold).
func TestThawFallsBackWhenTheRestoreFails(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	if r := fh.freezeOne(gid1); !r.OK {
		t.Fatalf("freeze: %+v", r)
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

// With the freeze off nothing is frozen, and images a host left behind
// are removed, never restored.
func TestFreezeOffRestoresNothingAndCleansUp(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	if r := fh.freezeOne(gid1); !r.OK {
		t.Fatalf("freeze: %+v", r)
	}
	fh.m.cfg.Freeze.Enabled = false
	if res := fh.m.FreezeAll(context.Background(), FreezeShutdown); res != nil {
		t.Fatalf("freeze while off: %+v", res)
	}
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fh.unitActive(gid1) || fh.img.has(gid1) || fh.record(gid1) != nil {
		t.Fatal("an image was restored or kept with the freeze off")
	}
}

// No key, no freeze: the guests are left running for the shutdown.
func TestFreezeWithoutKeyFreezesNothing(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.sealer.notReady = errors.New("key vault public key not fetched yet")
	if res := fh.m.FreezeAll(context.Background(), FreezeShutdown); len(res) != 0 {
		t.Fatalf("results without a key: %+v", res)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest stopped by a freeze that could not happen")
	}
}

// When the pool cannot take every image, the smallest guests go first and
// the rest are skipped, running.
func TestFreezeStopsAtThePoolLimit(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	small := createReq(gid2)
	small.SystemClosure, small.Class = fh.closure, "small"
	fh.mustOK(cmd(small))
	// 100 GiB pool, refuse at 85: 6 GiB of room takes the small guest's 4
	// GiB image and not the large one's 8.
	fh.lvm.PoolSize, fh.lvm.PoolFree = 100<<30, 21<<30
	res := fh.m.FreezeAll(context.Background(), FreezeOperator)
	got := map[string]string{}
	for _, r := range res {
		got[r.GuestID] = r.Outcome
	}
	if got[gid2] != "frozen" || got[gid1] != "skipped" {
		t.Fatalf("outcomes %v, want the small guest frozen and the large skipped", got)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("skipped guest stopped")
	}
}

// An image volume with no record (hostd died between lvcreate and the
// record) is removed at start.
func TestThawSweepsImageVolumesWithoutRecord(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	if err := fh.lvm.CreateVolume(context.Background(), freeze.LVName(gid2), 1<<30); err != nil {
		t.Fatal(err)
	}
	fh.m.ThawAll(context.Background())
	if !slices.Contains(fh.img.ops, "remove "+gid2) {
		t.Fatalf("orphan image volume not removed: %v", fh.img.ops)
	}
}

// The shutdown's freeze writes nothing a tenant typed into the log.
func TestFreezeLogsCarryNoTenantData(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.freezeOne(gid1)
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs := fh.logs.String()
	for _, bad := range []string{"s3cret", "todo-app", "git@github.com"} {
		if strings.Contains(logs, bad) {
			t.Fatalf("log carries %q", bad)
		}
	}
	if !bytes.Contains([]byte(logs), []byte(`"event":"guest_freeze"`)) || !bytes.Contains([]byte(logs), []byte(`"event":"guest_thaw"`)) {
		t.Fatal("freeze and thaw not logged")
	}
}

// hostd restarted mid-freeze (the record still `writing`, the hypervisor
// running with the VM paused): the next start resumes the guest and only
// then removes the image.
func TestRestartMidFreezeResumesTheGuest(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	if err := fh.m.records().Put(&freeze.Record{GuestID: gid1, State: freeze.StateWriting, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fh.img.vols[gid1] = "x"
	_ = fh.chc.Pause(context.Background(), "paused-by-the-freeze")
	before := len(fh.chc.Calls)
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(fh.chc.Calls[before:], "\n")
	if !strings.Contains(calls, "resume ") {
		t.Fatalf("guest left paused after a restart mid-freeze; hypervisor calls %q", calls)
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
func TestMidFreezeBusyHypervisorKeepsImage(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) { c.ResumeWait = 300 * time.Millisecond })
	fh.create(gid1)
	if err := fh.m.records().Put(&freeze.Record{GuestID: gid1, State: freeze.StateWriting, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fh.img.vols[gid1] = "x"
	fh.chc.Fail = map[string]error{"info": errors.New("timeout"), "resume": errors.New("timeout")}
	res := fh.m.ThawAll(context.Background())
	if len(res) != 1 || res[0].Outcome != "skipped" {
		t.Fatalf("thaw with a busy hypervisor: %+v", res)
	}
	if slices.Contains(fh.img.ops, "remove "+gid1) || fh.record(gid1) == nil {
		t.Fatalf("image removed under a busy hypervisor: ops %v", fh.img.ops)
	}
	// Once it answers, the next ThawAll resumes it and removes the image.
	fh.chc.Fail = nil
	fh.m.ThawAll(context.Background())
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image kept once the guest ran again")
	}
}

// A second freeze of a frozen guest (two FreezeAlls one after the other:
// an operator's and the shutdown's) leaves the finished image alone.
func TestSecondFreezeKeepsTheImage(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	if r := fh.freezeOne(gid1); !r.OK {
		t.Fatalf("freeze: %+v", r)
	}
	if res := fh.m.FreezeAll(context.Background(), FreezeShutdown); len(res) != 0 {
		t.Fatalf("second FreezeAll: %+v", res)
	}
	// What a job already queued for the guest does, on its worker.
	res := fh.m.freezeGuest(context.Background(), gid1, FreezeShutdown)
	if res.OK || res.Outcome != "skipped" {
		t.Fatalf("second freeze of a frozen guest: %+v", res)
	}
	if strings.Count(strings.Join(fh.img.ops, " "), "create "+gid1) != 1 {
		t.Fatalf("finished image re-created: %v", fh.img.ops)
	}
	if rec := fh.record(gid1); rec == nil || rec.State != freeze.StateComplete || !fh.img.has(gid1) {
		t.Fatal("finished image lost to a second freeze")
	}
}

// gatedCH holds a snapshot until the test releases it.
type gatedCH struct {
	freezeCH
	started, release chan struct{}
}

func (c gatedCH) Snapshot(ctx context.Context, sock, dir string) error {
	close(c.started)
	<-c.release
	return c.Fake.Snapshot(ctx, sock, dir)
}

// A thaw (hostd thaw, hostd reconcile, a called-off Scheduled Event) that
// comes while a freeze is writing waits for it, and then restores the
// finished image instead of removing the one being written.
func TestThawDuringAFreezeWaitsForIt(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	gc := gatedCH{freezeCH: fh.m.d.CH.(freezeCH), started: make(chan struct{}), release: make(chan struct{})}
	fh.m.d.CH = gc
	frozen := make(chan []FreezeResult)
	go func() { frozen <- fh.m.FreezeAll(context.Background(), FreezeOperator) }()
	<-gc.started
	thawed := make(chan []FreezeResult)
	go func() { thawed <- fh.m.ThawAll(context.Background()) }()
	select {
	case r := <-thawed:
		t.Fatalf("thaw ran during the freeze: %+v", r)
	case <-time.After(200 * time.Millisecond):
	}
	if slices.Contains(fh.img.ops, "remove "+gid1) {
		t.Fatalf("image removed while it was written: %v", fh.img.ops)
	}
	close(gc.release)
	if r := <-frozen; len(r) != 1 || !r[0].OK {
		t.Fatalf("freeze: %+v", r)
	}
	if r := <-thawed; len(r) != 1 || r[0].Outcome != "thawed" {
		t.Fatalf("thaw after the freeze: %+v", r)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest not running after the thaw")
	}
}

// With Key Vault down, the start waits one UnwrapTimeout whatever the
// number of images, not one per image.
func TestThawWaitsOneUnwrapTimeout(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) { c.Concurrency = 1; c.UnwrapTimeout = 300 * time.Millisecond })
	fh.create(gid1)
	fh.create(gid2)
	if res := fh.m.FreezeAll(context.Background(), FreezeOperator); len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("freeze: %+v", res)
	}
	fh.sealer.unwrapErr = context.DeadlineExceeded
	start := time.Now()
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d >= 550*time.Millisecond {
		t.Fatalf("reconcile took %v for 2 images with a 300 ms unwrap timeout", d)
	}
	if fh.img.has(gid1) || fh.img.has(gid2) {
		t.Fatal("images kept without their key")
	}
}

// An image whose restore cannot start within UnwrapTimeout +
// RestoreTimeout of the thaw is discarded; that guest starts cold.
func TestThawRestoreWindowDiscards(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) {
		c.Concurrency = 1
		c.RestoreConcurrency = 1
		c.UnwrapTimeout = 100 * time.Millisecond
		c.RestoreTimeout = 300 * time.Millisecond
	})
	fh.create(gid1)
	fh.create(gid2)
	if res := fh.m.FreezeAll(context.Background(), FreezeOperator); len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("freeze: %+v", res)
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
	res := fh.m.ThawAll(context.Background())
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
func TestFreezeSkipsASnapshotPastDeadline(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30 // 2 GiB at 10 MiB/s: 205 s
	res := fh.freezeOne(gid1)
	if res.OK || res.Reason != "snapshot would outlast the deadline" {
		t.Fatalf("freeze: %+v", res)
	}
	if strings.Contains(fh.chCalls(), "pause ") || !fh.unitActive(gid1) {
		t.Fatal("guest paused or stopped for a snapshot that was not started")
	}
	// The unit's memory, not the class, is what is estimated: 200 MiB fits.
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 200 << 20
	if r := fh.freezeOne(gid1); !r.OK {
		t.Fatalf("freeze of a guest using 200 MiB: %+v", r)
	}
}

// The page cache is dropped only in guests that will be frozen and have
// the balloon; every guest still syncs.
func TestFreezeDropsCachesOnlyWithTheBalloon(t *testing.T) {
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
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	srv := fh.guestd(gid1) // the hypervisor's guestd goes with it
	if r := fh.freezeOne(gid1); !r.OK {
		t.Fatalf("freeze: %+v", r)
	}
	if e := execs(srv); !strings.Contains(e, "sync") || strings.Contains(e, "drop_caches") {
		t.Fatalf("guest without the balloon ran %q, want a sync and no cache drop", e)
	}

	fh = newFreezeHarness(t, nil)
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
	if r := fh.freezeOne(gid1); !r.OK {
		t.Fatalf("freeze: %+v", r)
	}
	if e := execs(srv); !strings.Contains(e, "drop_caches") {
		t.Fatalf("guest with the balloon ran %q, want a cache drop", e)
	}
}

// Guests are snapshotted smallest first, in that order, so a deadline that
// cannot hold them all freezes as many as it can. On host-stg-01
// (2026-10-12, 4 guests, 125 MB/s disk) the xl took a slot ahead of a
// small one, and the small and the large were left out at the deadline.
func TestFreezeStartsSmallestFirst(t *testing.T) {
	for i := range 10 {
		fh := newFreezeHarness(t, func(c *FreezeConfig) { c.Concurrency = 1 })
		for _, g := range []struct{ id, class string }{{gid1, "xl"}, {gid2, "small"}} {
			req := createReq(g.id)
			req.Class = g.class
			req.SystemClosure = fh.closure
			fh.mustOK(cmd(req))
			fh.sd.Units[GuestUnit(g.id)].MemBytes = 200 << 20
		}
		fh.chc.Calls = nil
		for _, r := range fh.m.FreezeAll(context.Background(), FreezeOperator) {
			if !r.OK {
				t.Fatalf("run %d: freeze: %+v", i, r)
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
// 38 s into a freeze of 4 guests): the image is whole, so the next start
// restores the guest from it rather than leaving it stopped with its
// memory gone.
func TestKilledAfterTheSnapshotRestoresTheGuest(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.m.cutFreeze = func(_, at string) bool { return at == "stopped" }
	fh.m.FreezeAll(context.Background(), FreezeOperator)
	fh.m.cutFreeze = nil
	if fh.unitActive(gid1) {
		t.Fatal("hypervisor still running at the cut")
	}
	res := fh.m.ThawAll(context.Background())
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("thaw after a kill past the snapshot: %+v", res)
	}
	if !fh.unitActive(gid1) || fh.guest(gid1).State != StateRunning {
		t.Fatal("guest not running after the thaw")
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
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	if err := fh.m.records().Put(&freeze.Record{GuestID: gid1, State: freeze.StateWriting, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fh.img.vols[gid1] = "x"
	fh.sd.Exit(GuestUnit(gid1), 0)
	res := fh.m.ThawAll(context.Background())
	if len(res) != 1 || res[0].OK || res[0].Reason != "image incomplete" {
		t.Fatalf("thaw of an image cut mid-snapshot: %+v", res)
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("broken image kept")
	}
}

// hostd killed while a guest's VM is paused for its snapshot: the guest is
// not resumed (a VM resumed after a snapshot loses its virtio-fs; host-stg-01,
// 2026-10-12) but frozen again and restored from the image.
func TestKilledWhilePausedRestoresInsteadOfResuming(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	fh.m.cutFreeze = func(_, at string) bool { return at == "snapshot" }
	fh.m.FreezeAll(context.Background(), FreezeOperator)
	fh.m.cutFreeze = nil
	if !fh.unitActive(gid1) {
		t.Fatal("hypervisor gone at the cut")
	}
	before := len(fh.chc.Calls)
	res := fh.m.ThawAll(context.Background())
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("thaw after a kill while paused: %+v", res)
	}
	calls := strings.Join(fh.chc.Calls[before:], "\n")
	if !strings.Contains(calls, "snapshot ") || !strings.Contains(calls, "shutdown ") {
		t.Fatalf("paused VM not frozen again; hypervisor calls %q", calls)
	}
	if argv := strings.Join(fh.unitArgv(gid1), " "); !strings.Contains(argv, "--restore source_url=file://") {
		t.Fatalf("hypervisor started with %q, want a restore", argv)
	}
	if !fh.unitActive(gid1) || fh.guest(gid1).State != StateRunning {
		t.Fatal("guest not running after the thaw")
	}
	if fh.record(gid1) != nil || fh.img.has(gid1) {
		t.Fatal("image or record left after the restore")
	}
}

// The record names the image's filesystem, and the restore mounts that:
// a new image is the compressed btrfs of I-685, and an image written by
// the release before (ext4, no filesystem in its record) is still
// restored, as docs/interfaces promises for one release.
func TestFreezeRecordsTheImageFilesystem(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	if res := fh.freezeOne(gid1); !res.OK {
		t.Fatalf("freeze: %+v", res)
	}
	if rec := fh.record(gid1); rec.FS != freeze.FSBtrfs {
		t.Fatalf("record says the image is %q", rec.FS)
	}
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fh.img.openedAs[gid1]; got != freeze.FSBtrfs {
		t.Fatalf("image opened as %q", got)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("guest not restored")
	}
}

func TestThawRestoresAnImageFromTheReleaseBefore(t *testing.T) {
	fh := newFreezeHarness(t, nil)
	fh.create(gid1)
	if res := fh.freezeOne(gid1); !res.OK {
		t.Fatalf("freeze: %+v", res)
	}
	// What the previous release left: an ext4 image and a record with no
	// filesystem in it.
	rec := fh.record(gid1)
	rec.FS = ""
	if err := fh.m.records().Put(rec); err != nil {
		t.Fatal(err)
	}
	fh.img.fs[gid1] = freeze.FSExt4
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fh.img.openedAs[gid1]; got != freeze.FSExt4 {
		t.Fatalf("an image from the release before was opened as %q", got)
	}
	if !fh.unitActive(gid1) || fh.img.has(gid1) {
		t.Fatalf("guest restored=%v image kept=%v", fh.unitActive(gid1), fh.img.has(gid1))
	}
}

// Restores run RestoreConcurrency at a time, which is more than the
// freeze's Concurrency: a compressed image is read back by one core
// decompressing it, so restoring more guests at once finishes them all
// sooner (I-685). Before, both shared the one Concurrency and a host that
// froze two at a time restored two at a time.
func TestRestoresRunMoreAtOnceThanFreezes(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) {
		c.Concurrency = 1
		c.RestoreConcurrency = 2
		c.RestoreTimeout = 10 * time.Second
	})
	fh.create(gid1)
	fh.create(gid2)
	if res := fh.m.FreezeAll(context.Background(), FreezeOperator); len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("freeze: %+v", res)
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
	res := fh.m.ThawAll(context.Background())
	if len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("thaw: %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if most != 2 {
		t.Fatalf("at most %d restore(s) ran at once; RestoreConcurrency is 2", most)
	}
}

// Four freezes and four restores at once by default (I-685), and never
// fewer restores than freezes.
func TestFreezeConfigDefaultsConcurrency(t *testing.T) {
	c := FreezeConfig{}.withDefaults()
	if c.Concurrency != 4 || c.RestoreConcurrency != 4 {
		t.Fatalf("defaults: freeze %d, restore %d", c.Concurrency, c.RestoreConcurrency)
	}
	if got := (FreezeConfig{Concurrency: 6}).withDefaults(); got.RestoreConcurrency != 6 {
		t.Fatalf("with 6 freezes at once, %d restores", got.RestoreConcurrency)
	}
	if got := (FreezeConfig{RestoreConcurrency: 1}).withDefaults(); got.RestoreConcurrency != got.Concurrency {
		t.Fatalf("fewer restores (%d) than freezes (%d) at once", got.RestoreConcurrency, got.Concurrency)
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
// against the 120 MB/s host-01 is configured with (it measured 123 to
// 126).
func TestFreezeTakesEveryClassAtItsResidentSize(t *testing.T) {
	const hostRate = 120_000_000 // host-01's configured rate (I-796)
	for _, c := range []struct {
		name     string
		class    string
		resident uint64
		deadline time.Duration // 0 is the shipped default
		want     string
	}{
		{"small", "small", 4 << 30, 0, "frozen"},
		{"large", "large", 8 << 30, 0, "frozen"},
		{"xl", "xl", 16 << 30, 0, "frozen"},
		// What the owner lost: the deadline that shipped before I-796
		// cannot hold an xl at any load.
		{"xl at the old 120 s deadline", "xl", 16 << 30, 120 * time.Second, "skipped"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fh := newFreezeHarness(t, func(f *FreezeConfig) {
				f.WriteRate = hostRate
				f.Deadline = c.deadline
			})
			req := createReq(gid1)
			req.SystemClosure, req.Class = fh.closure, c.class
			fh.mustOK(cmd(req))
			fh.sd.Units[GuestUnit(gid1)].MemBytes = c.resident
			res := fh.freezeOne(gid1)
			if res.Outcome != c.want {
				t.Fatalf("a %s guest with %d GiB resident: %+v, want %s", c.class, c.resident>>30, res, c.want)
			}
			if c.want == "frozen" && !res.OK {
				t.Fatalf("%+v", res)
			}
		})
	}
}

// unfrozenReason is what turns a result into the counter GuestLeftUnfrozen
// reads, so every outcome a freeze can produce is named here: the three
// that leave a running guest without its memory, and the ones that cost
// nothing.
func TestUnfrozenReasonNamesEveryResult(t *testing.T) {
	for _, c := range []struct {
		res  FreezeResult
		want string
	}{
		{FreezeResult{OK: true, Outcome: "frozen"}, ""},
		{FreezeResult{Outcome: "skipped", Reason: "pool has no room for the image"}, "no_room"},
		{FreezeResult{Outcome: "skipped", Reason: "deadline passed"}, "deadline"},
		{FreezeResult{Outcome: "skipped", Reason: "snapshot would outlast the deadline"}, "estimate"},
		{FreezeResult{Outcome: "failed", Reason: "snapshot failed"}, "failed"},
		{FreezeResult{Outcome: "failed", Reason: "snapshot failed; guest still paused"}, "failed"},
		// Nothing was lost: the guest had no memory to keep, or its
		// image was already written.
		{FreezeResult{Outcome: "skipped", Reason: "guest is not running"}, ""},
		{FreezeResult{Outcome: "skipped", Reason: "guest already frozen"}, ""},
	} {
		if got := unfrozenReason(c.res); got != c.want {
			t.Errorf("%+v: reason %q, want %q", c.res, got, c.want)
		}
	}
}

// Every path that leaves a running guest unfrozen counts it: the skip was
// a Warn line and a row in a table nobody read, so nothing alerted when
// host-01 lost its xl guest (I-796).
func TestFreezeCountsEveryGuestItLeavesUnfrozen(t *testing.T) {
	unfrozen := func(fh *freezeHarness, why string) float64 {
		return testutil.ToFloat64(fh.metrics.GuestFreezeUnfrozen.WithLabelValues(why))
	}

	t.Run("estimate", func(t *testing.T) {
		fh := newFreezeHarness(t, func(c *FreezeConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
		fh.create(gid1)
		fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30 // 205 s at 10 MiB/s
		if r := fh.freezeOne(gid1); r.Reason != "snapshot would outlast the deadline" {
			t.Fatalf("freeze: %+v", r)
		}
		if n := unfrozen(fh, "estimate"); n != 1 {
			t.Fatalf("estimate counter %v, want 1", n)
		}
	})

	t.Run("deadline passed", func(t *testing.T) {
		// The deadline passes while the guests write back their caches,
		// so no snapshot is started at all.
		fh := newFreezeHarness(t, func(c *FreezeConfig) { c.Deadline = 150 * time.Millisecond })
		fh.gopts.Delay = map[string]time.Duration{"Exec": 400 * time.Millisecond}
		fh.create(gid1)
		if r := fh.freezeOne(gid1); r.Reason != "deadline passed" {
			t.Fatalf("freeze: %+v", r)
		}
		if n := unfrozen(fh, "deadline"); n != 1 {
			t.Fatalf("deadline counter %v, want 1", n)
		}
	})

	t.Run("no room in the pool", func(t *testing.T) {
		fh := newFreezeHarness(t, nil)
		fh.create(gid1)
		fh.lvm.PoolSize, fh.lvm.PoolFree = 100<<30, 21<<30 // 6 GiB of room, 8 needed
		if r := fh.freezeOne(gid1); r.Reason != "pool has no room for the image" {
			t.Fatalf("freeze: %+v", r)
		}
		if n := unfrozen(fh, "no_room"); n != 1 {
			t.Fatalf("no_room counter %v, want 1", n)
		}
	})

	t.Run("failed after it started", func(t *testing.T) {
		fh := newFreezeHarness(t, nil)
		fh.create(gid1)
		fh.chc.Fail = map[string]error{"snapshot": errors.New("No space left on device")}
		if r := fh.freezeOne(gid1); r.Outcome != "failed" {
			t.Fatalf("freeze: %+v", r)
		}
		if n := unfrozen(fh, "failed"); n != 1 {
			t.Fatalf("failed counter %v, want 1", n)
		}
	})

	t.Run("no image key", func(t *testing.T) {
		fh := newFreezeHarness(t, nil)
		fh.create(gid1)
		fh.sealer.notReady = errors.New("key vault public key not fetched yet")
		if res := fh.m.FreezeAll(context.Background(), FreezeShutdown); len(res) != 0 {
			t.Fatalf("results without a key: %+v", res)
		}
		if n := unfrozen(fh, "no_key"); n != 1 {
			t.Fatalf("no_key counter %v, want 1", n)
		}
	})
}

// A shutdown freeze's counter is never scraped: the host goes down behind
// it. Its tally is kept beside the records and counted again at the next
// start, which is where Prometheus can see it (I-796).
func TestTheUnfrozenTallyIsCountedAtTheNextStart(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30
	if r := fh.m.FreezeAll(context.Background(), FreezeShutdown); len(r) != 1 || r[0].OK {
		t.Fatalf("freeze: %+v", r)
	}
	count := func() float64 {
		return testutil.ToFloat64(fh.metrics.GuestFreezeUnfrozen.WithLabelValues("estimate"))
	}
	if n := count(); n != 1 {
		t.Fatalf("counter after the freeze %v, want 1", n)
	}
	// The host restarted: a new boot id, and the process's counter would
	// have started at zero.
	fh.m.d.Freeze.BootID = func() string { return "boot-b" }
	fh.m.ThawAll(context.Background())
	if n := count(); n != 2 {
		t.Fatalf("counter after the next start %v, want the tally counted again", n)
	}
	// Reported once: the tally is gone.
	fh.m.ThawAll(context.Background())
	if n := count(); n != 2 {
		t.Fatalf("counter after a second start %v, want the tally reported once", n)
	}
}

// A freeze for an event that was then called off leaves its tally behind,
// but the host never restarted: counting it at the next start would report
// a loss that did not happen.
func TestATallyFromThisBootIsNotCountedAgain(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) { c.WriteRate = 10 << 20; c.Deadline = 30 * time.Second })
	fh.create(gid1)
	fh.sd.Units[GuestUnit(gid1)].MemBytes = 2 << 30
	fh.m.FreezeAll(context.Background(), FreezeScheduledEvent)
	fh.m.ThawAll(context.Background())
	if n := testutil.ToFloat64(fh.metrics.GuestFreezeUnfrozen.WithLabelValues("estimate")); n != 1 {
		t.Fatalf("counter %v, want the one count the freeze made", n)
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

// A freeze that never gets the lock (a thaw or an operator's freeze still
// running) leaves every running guest unfrozen, and lists them itself to
// say how many. Its own deadline has passed by then, so the listing needs
// a context of its own.
func TestAFreezeThatCannotStartCountsTheGuestsItLeaves(t *testing.T) {
	fh := newFreezeHarness(t, func(c *FreezeConfig) { c.Deadline = 50 * time.Millisecond })
	fh.create(gid1)
	fh.m.d.Systemd = ctxSystemd{fh.sd}
	fh.m.freezeMu <- struct{}{} // held by something else
	defer func() { <-fh.m.freezeMu }()
	if res := fh.m.FreezeAll(context.Background(), FreezeShutdown); len(res) != 0 {
		t.Fatalf("results: %+v", res)
	}
	if n := testutil.ToFloat64(fh.metrics.GuestFreezeUnfrozen.WithLabelValues("busy")); n != 1 {
		t.Fatalf("busy counter %v, want 1", n)
	}
}
