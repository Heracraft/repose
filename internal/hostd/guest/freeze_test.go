package guest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/freeze"
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
}

func newFakeImage() *fakeImage {
	return &fakeImage{vols: map[string]string{}, open: map[string]bool{}, failOn: map[string]error{}, created: map[string]uint64{}}
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
	return nil
}

func (f *fakeImage) Open(_ context.Context, id string, key []byte, _ string) error {
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
	if fh.img.created[gid1] != (8192+256)<<20 {
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
	if !bytes.Contains([]byte(logs), []byte(`"event":"freeze"`)) || !bytes.Contains([]byte(logs), []byte(`"event":"thaw"`)) {
		t.Fatal("freeze and thaw not logged")
	}
}
