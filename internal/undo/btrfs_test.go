package undo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var epoch = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// clock returns a Now that moves on by step at every point, so points
// have an order a test can assert.
func clock(step time.Duration) func() time.Time {
	n := -1
	return func() time.Time {
		n++
		return epoch.Add(time.Duration(n) * step)
	}
}

// newStore returns a backend over a temp directory whose layout is in
// place, as Provision leaves it.
func newStore(t *testing.T, r *FakeRunner) *Btrfs {
	t.Helper()
	if r.Usage.Total == 0 {
		r.Usage = Space{Total: 40 << 30, Used: 1 << 30, Free: 39 << 30}
	}
	dir := t.TempDir()
	for _, p := range []string{filepath.Join(dir, "projects"), filepath.Join(dir, "points")} {
		if err := r.Mark(p); err != nil {
			t.Fatal(err)
		}
	}
	b := New(dir, r)
	b.Subvol = r.Subvol
	b.Now = clock(time.Minute)
	return b
}

// liveFile writes a file into the project's live area.
func liveFile(t *testing.T, b *Btrfs, project, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(b.livePath(project), name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func liveRead(t *testing.T, b *Btrfs, project, name string) string {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join(b.livePath(project), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func calls(r *FakeRunner) []string {
	var out []string
	for _, c := range r.Calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// index returns where the first call containing each of want is, or -1.
func index(r *FakeRunner, want ...string) int {
	for i, c := range calls(r) {
		found := true
		for _, w := range want {
			if !strings.Contains(c, w) {
				found = false
				break
			}
		}
		if found {
			return i
		}
	}
	return -1
}

// A fresh store is made once: the image, the filesystem, the mount with
// the installer's options, and the two subvolumes of the layout.
func TestProvisionMakesTheStoreOnce(t *testing.T) {
	r := &FakeRunner{}
	dir := filepath.Join(t.TempDir(), "undo")
	b := New(dir, r)
	b.Subvol = r.Subvol
	ctx := context.Background()
	if err := b.Provision(ctx, 1<<30); err != nil {
		t.Fatal(err)
	}
	got := calls(r)
	want := []string{
		"blkid -s TYPE -o value " + dir + ".img",
		"mkfs.btrfs -q -L repose-undo " + dir + ".img",
		"mount -o loop," + MountOptions + " " + dir + ".img " + dir,
		"btrfs subvolume create " + dir + "/projects",
		"btrfs subvolume create " + dir + "/points",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("provision ran:\n%s", strings.Join(got, "\n"))
	}
	if fi, err := os.Stat(dir + ".img"); err != nil || fi.Size() != 1<<30 {
		t.Fatalf("store image: %v %v", fi, err)
	}
	// A second run changes nothing: no mkfs, no subvolume create.
	r.Calls = nil
	if err := b.Provision(ctx, 1<<30); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls(r) {
		if strings.HasPrefix(c, "mkfs.btrfs") || strings.Contains(c, "subvolume create") {
			t.Fatalf("a second provision changed the store: %s", c)
		}
	}
}

// A new store needs a size; an existing one does not.
func TestProvisionRefusesAStoreWithNoSize(t *testing.T) {
	r := &FakeRunner{}
	b := New(filepath.Join(t.TempDir(), "undo"), r)
	b.Subvol = r.Subvol
	if err := b.Provision(context.Background(), 0); err == nil {
		t.Fatal("a store with no size and no device was provisioned")
	}
}

// Take is a read-only snapshot, and a point of the same name that exists
// already is success without a second snapshot: the CLI resends over SSH
// what it did not see the answer to.
func TestTakeIsIdempotent(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "one")
	p, err := b.Take(ctx, "todo-app", "r-0199a1c2", ReasonRun)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "r-0199a1c2" || p.Project != "todo-app" || p.Reason != ReasonRun {
		t.Fatalf("point %+v", p)
	}
	if !p.TakenAt.Equal(epoch) {
		t.Fatalf("taken at %s", p.TakenAt)
	}
	if i := index(r, "subvolume snapshot -r", b.livePath("todo-app"), b.pointPath("todo-app", "r-0199a1c2")); i < 0 {
		t.Fatalf("no read-only snapshot:\n%s", strings.Join(calls(r), "\n"))
	}
	liveFile(t, b, "todo-app", "f", "two")
	again, err := b.Take(ctx, "todo-app", "r-0199a1c2", ReasonRun)
	if err != nil {
		t.Fatal(err)
	}
	if !again.TakenAt.Equal(p.TakenAt) || again.Reason != p.Reason {
		t.Fatalf("a repeated take changed the point: %+v", again)
	}
	if n := strings.Count(strings.Join(calls(r), "\n"), "subvolume snapshot"); n != 1 {
		t.Fatalf("%d snapshots for two takes of one name", n)
	}
	// The point holds what the project held when it was taken.
	buf, err := os.ReadFile(filepath.Join(b.pointPath("todo-app", "r-0199a1c2"), "f"))
	if err != nil || string(buf) != "one" {
		t.Fatalf("point content %q: %v", buf, err)
	}
}

// A project with no live area has nothing to fix as a point.
func TestTakeWithoutALiveArea(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	if _, err := b.Take(context.Background(), "todo-app", "r-1", ReasonManual); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v, want ErrNotFound", err)
	}
}

// Names that would leave the store's layout, and reasons outside the
// enum, are refused before anything runs.
func TestNamesAndReasonsAreChecked(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	for _, tc := range []struct{ project, name, reason string }{
		{"../etc", "r-1", ReasonManual},
		{"todo-app/x", "r-1", ReasonManual},
		{".meta", "r-1", ReasonManual},
		{"", "r-1", ReasonManual},
		{"Todo-App", "r-1", ReasonManual},
		{"todo-app", "../../x", ReasonManual},
		{"todo-app", "a/b", ReasonManual},
		{"todo-app", "", ReasonManual},
		{"todo-app", "r-1", "whatever"},
	} {
		if _, err := b.Take(ctx, tc.project, tc.name, tc.reason); !errors.Is(err, ErrInvalidName) {
			t.Errorf("take %q %q %q: err %v, want ErrInvalidName", tc.project, tc.name, tc.reason, err)
		}
	}
	if len(r.Calls) != 0 {
		t.Fatalf("a refused name still ran:\n%s", strings.Join(calls(r), "\n"))
	}
}

// List is newest first, with the reason the point was taken for.
func TestListIsNewestFirst(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "one")
	for _, n := range []string{"r-1", "r-2", "r-3"} {
		if _, err := b.Take(ctx, "todo-app", n, ReasonRun); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := b.List(ctx, "todo-app")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "r-3,r-2,r-1" {
		t.Fatalf("points %v", names)
	}
	if ps[0].Reason != ReasonRun {
		t.Fatalf("reason %q", ps[0].Reason)
	}
	// An empty project has no points and no error.
	if ps, err := b.List(ctx, "other"); err != nil || len(ps) != 0 {
		t.Fatalf("points of a project with none: %v %v", ps, err)
	}
}

// A rewind takes a point of the current state before it swaps, and the
// point it rewinds to becomes the live area.
func TestRewindTakesASafetyPointBeforeTheSwap(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "before")
	if _, err := b.Take(ctx, "todo-app", "r-good", ReasonRun); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "wrecked")
	safety, err := b.Rewind(ctx, "todo-app", "r-good")
	if err != nil {
		t.Fatal(err)
	}
	if safety == "" {
		t.Fatal("a rewind with no safety point")
	}
	if got := liveRead(t, b, "todo-app", "f"); got != "before" {
		t.Fatalf("live file %q after the rewind", got)
	}
	// The pre-rewind state is a point of its own, so the rewind is undoable.
	buf, err := os.ReadFile(filepath.Join(b.pointPath("todo-app", safety), "f"))
	if err != nil || string(buf) != "wrecked" {
		t.Fatalf("safety point content %q: %v", buf, err)
	}
	// Order: the safety point before the writable snapshot of the point.
	took := index(r, "subvolume snapshot -r", b.pointPath("todo-app", safety))
	swap := index(r, "subvolume snapshot "+b.pointPath("todo-app", "r-good"))
	if took < 0 || swap < 0 || took > swap {
		t.Fatalf("safety point at %d, swap at %d:\n%s", took, swap, strings.Join(calls(r), "\n"))
	}
	// Nothing of the swap is left in projects/.
	ents, err := os.ReadDir(b.projectsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), stagingPrefix) || strings.HasPrefix(e.Name(), asidePrefix) {
			t.Fatalf("a rewind left %s behind", e.Name())
		}
	}
}

// The point a rewind takes of the state it replaces carries its own
// reason, so nothing can pick it as the newest point to go back to.
func TestRewindSafetyPointCarriesItsOwnReason(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "before")
	if _, err := b.Take(ctx, "todo-app", "r-good", ReasonRun); err != nil {
		t.Fatal(err)
	}
	safety, err := b.Rewind(ctx, "todo-app", "r-good")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := b.List(ctx, "todo-app")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Name != safety {
		t.Fatalf("points %+v, want %s newest", ps, safety)
	}
	if ps[0].Reason != ReasonRewind {
		t.Fatalf("the safety point's reason is %q, want %q", ps[0].Reason, ReasonRewind)
	}
	if ps[1].Reason != ReasonRun {
		t.Fatalf("the point rewound to changed reason to %q", ps[1].Reason)
	}
}

// A second scan with more privilege is merged in by pid, and one that
// fails leaves the first scan's answer standing: a quiesce that cannot
// run is worse than one that sees less.
func TestHoldersMergesThePrivilegedScan(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "live")
	fd, err := os.Open(filepath.Join(b.livePath("todo-app"), "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fd.Close() }()
	mine := os.Getpid()

	b.Scan = func(context.Context, string) ([]Holder, error) {
		return []Holder{{PID: 1, Comm: "systemd"}, {PID: mine, Comm: "undo.test"}}, nil
	}
	hs, err := b.Holders(ctx, "todo-app")
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 2 || hs[0].PID != 1 || hs[1].PID != mine {
		t.Fatalf("holders %+v, want pid 1 and %d once each", hs, mine)
	}

	b.Scan = func(context.Context, string) ([]Holder, error) {
		return nil, errors.New("sudo: a password is required")
	}
	hs, err = b.Holders(ctx, "todo-app")
	if err != nil {
		t.Fatalf("a scan that could not run failed the check: %v", err)
	}
	if len(hs) != 1 || hs[0].PID != mine {
		t.Fatalf("holders %+v, want only %d", hs, mine)
	}
}

// A mapped file holds the live area as firmly as an open one: a process
// that mmaps a database and closes the descriptor has nothing in fd/ and
// still writes through the mapping after a swap.
func TestHoldersSeeAMappedFile(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.livePath("todo-app"), "app.db")
	if err := os.WriteFile(path, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := syscall.Mmap(int(f.Fd()), 0, 4096, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		t.Skipf("this box will not mmap a file in a temp directory: %v", err)
	}
	// The descriptor goes; the mapping stays, which is the whole point.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	hs, err := b.Holders(ctx, "todo-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Munmap(m); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hs {
		if h.PID == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Fatalf("a mapped file left the live area looking quiesced: %+v", hs)
	}
}

// A rewind refuses a project something is still writing in: the writer
// would survive the swap and go on writing where nothing reaches it.
func TestRewindRefusesABusyProject(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "live")
	if _, err := b.Take(ctx, "todo-app", "r-good", ReasonRun); err != nil {
		t.Fatal(err)
	}
	fd, err := os.Open(filepath.Join(b.livePath("todo-app"), "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fd.Close() }()
	_, err = b.Rewind(ctx, "todo-app", "r-good")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err %v, want ErrBusy", err)
	}
	if !strings.Contains(err.Error(), "1 process") {
		t.Fatalf("ErrBusy does not count the processes: %v", err)
	}
	if strings.Contains(err.Error(), b.Store) {
		t.Fatalf("ErrBusy names a path: %v", err)
	}
	// Nothing happened: no safety point, and the live area is as it was.
	ps, err := b.List(ctx, "todo-app")
	if err != nil || len(ps) != 1 {
		t.Fatalf("points %+v: %v", ps, err)
	}
	if got := liveRead(t, b, "todo-app", "f"); got != "live" {
		t.Fatalf("live file %q", got)
	}
	// With the file closed the same rewind goes through.
	_ = fd.Close()
	if _, err := b.Rewind(ctx, "todo-app", "r-good"); err != nil {
		t.Fatal(err)
	}
}

// A rewind that fails leaves the pre-rewind state live.
func TestRewindFailureLeavesTheLiveAreaAlone(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "before")
	if _, err := b.Take(ctx, "todo-app", "r-good", ReasonRun); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "wrecked")
	// The writable snapshot of the point fails; the read-only snapshot of
	// the live area (the safety point) is a different call.
	r.FailOn = map[string]string{"snapshot " + b.pointPath("todo-app", "r-good"): "ERROR: No space left on device"}
	if _, err := b.Rewind(ctx, "todo-app", "r-good"); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err %v, want ErrNoSpace", err)
	}
	if got := liveRead(t, b, "todo-app", "f"); got != "wrecked" {
		t.Fatalf("live file %q after a failed rewind, want the pre-rewind state", got)
	}
	if ok, err := b.isSubvolume(b.livePath("todo-app")); err != nil || !ok {
		t.Fatalf("the live area is not a subvolume after a failed rewind: %v", err)
	}
}

// Rewinding to a point that is not there is not found.
func TestRewindWithNoSuchPoint(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rewind(ctx, "todo-app", "r-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v, want ErrNotFound", err)
	}
}

// Fork is a writable snapshot of a point in another project's live area,
// and it refuses a project that is already there.
func TestForkIntoANewProject(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	liveFile(t, b, "todo-app", "f", "one")
	if _, err := b.Take(ctx, "todo-app", "r-1", ReasonManual); err != nil {
		t.Fatal(err)
	}
	if err := b.Fork(ctx, "todo-app", "r-1", "todo-app-2"); err != nil {
		t.Fatal(err)
	}
	if got := liveRead(t, b, "todo-app-2", "f"); got != "one" {
		t.Fatalf("forked file %q", got)
	}
	if i := index(r, "subvolume snapshot", b.pointPath("todo-app", "r-1"), b.livePath("todo-app-2")); i < 0 {
		t.Fatalf("no writable snapshot:\n%s", strings.Join(calls(r), "\n"))
	}
	if err := b.Fork(ctx, "todo-app", "r-1", "todo-app-2"); !errors.Is(err, ErrExists) {
		t.Fatalf("err %v, want ErrExists", err)
	}
	if err := b.Fork(ctx, "todo-app", "r-nope", "todo-app-3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v, want ErrNotFound", err)
	}
}

// Deleting a point that is not there is success, and a deleted point
// takes its record with it.
func TestDeleteAbsenceIsSuccess(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, "todo-app", "r-nope"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Take(ctx, "todo-app", "r-1", ReasonManual); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, "todo-app", "r-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.metaPath("todo-app", "r-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the point record outlived the point: %v", err)
	}
	if ps, err := b.List(ctx, "todo-app"); err != nil || len(ps) != 0 {
		t.Fatalf("points %+v: %v", ps, err)
	}
}

// sample is `btrfs filesystem usage --raw` on a 4 GB loopback store.
const sample = `Overall:
    Device size:		        4294967296
    Device allocated:		         562036736
    Device unallocated:		        3732930560
    Device missing:		                 0
    Device slack:		                 0
    Used:			            393216
    Free (estimated):		        3741319168	(min: 1874853888)
    Free (statfs, df):		        3740270592
    Data ratio:			              1.00
    Metadata ratio:		              2.00
    Global reserve:		           5767168	(used: 0)
    Multiple profiles:		                no

Data,single: Size:8388608, Used:0 (0.00%)
   /dev/loop1	   8388608

Metadata,DUP: Size:268435456, Used:180224 (0.07%)
   /dev/loop1	 536870912

Unallocated:
   /dev/loop1	3732930560
`

type stubRunner struct{ stdout string }

func (s stubRunner) Run(context.Context, ...string) (Result, error) {
	return Result{Stdout: []byte(s.stdout)}, nil
}

// Space is read from btrfs, not from df, which reports what is allocated.
func TestSpaceReadsBtrfsUsage(t *testing.T) {
	b := New("/var/lib/repose/undo", stubRunner{sample})
	b.BackingFree = func(string) (uint64, error) { return 1 << 40, nil }
	s, err := b.Space(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Space{Total: 4294967296, Used: 393216, Free: 3741319168}
	if s != want {
		t.Fatalf("space %+v, want %+v", s, want)
	}
	bad := New("/x", stubRunner{"Overall:\n"})
	bad.BackingFree = func(string) (uint64, error) { return 1 << 40, nil }
	if _, err := bad.Space(context.Background()); err == nil {
		t.Fatal("usage with no figures was read as space")
	}
}

// A sparse image promises more than the filesystem under it can hold, so
// the free figure a user sees is the smaller of the two and the total is
// what is used plus that. A store on its own device is left alone.
func TestSpaceIsBoundedByTheBackingFilesystem(t *testing.T) {
	for _, tc := range []struct {
		name   string
		device string
		free   uint64
		err    error
		want   Space
	}{
		{
			name: "the box has less than the image",
			free: 1 << 30,
			want: Space{Total: 393216 + (1 << 30), Used: 393216, Free: 1 << 30},
		},
		{
			name: "the box has more than the image",
			free: 1 << 40,
			want: Space{Total: 4294967296, Used: 393216, Free: 3741319168},
		},
		{
			name:   "a device is its own bound",
			device: "/dev/sdb1",
			free:   1 << 20,
			want:   Space{Total: 4294967296, Used: 393216, Free: 3741319168},
		},
		{
			name: "an unreadable filesystem is not a failure",
			err:  errors.New("statfs: permission denied"),
			want: Space{Total: 4294967296, Used: 393216, Free: 3741319168},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New("/var/lib/repose/undo", stubRunner{sample})
			b.Device = tc.device
			b.BackingFree = func(string) (uint64, error) { return tc.free, tc.err }
			got, err := b.Space(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("space %+v, want %+v", got, tc.want)
			}
		})
	}
}

// GC takes the oldest points until the store is under the mark, and
// never a project's newest point. With no floors — what a test asks for
// and no box does — it works down the whole list.
func TestGCKeepsTheNewestPointOfEachProject(t *testing.T) {
	for _, tc := range []struct {
		name    string
		freed   uint64
		gone    []string
		stalled bool
	}{
		{name: "one delete is enough", freed: 10, gone: []string{"a/r-1"}},
		{name: "a delete that frees nothing stops the loop", freed: 0, gone: []string{"a/r-1"}, stalled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &FakeRunner{Usage: Space{Total: 100, Used: 90, Free: 10}, FreedPerDelete: tc.freed}
			b := newStore(t, r)
			b.Floors = &GCFloors{}
			ctx := context.Background()
			// a's points are the oldest and the newest; b's sit between.
			for _, p := range []struct{ project, name string }{
				{"a", "r-1"}, {"b", "r-1"}, {"a", "r-2"}, {"b", "r-2"}, {"a", "r-3"},
			} {
				if _, err := b.Init(ctx, p.project); err != nil {
					t.Fatal(err)
				}
				if _, err := b.Take(ctx, p.project, p.name, ReasonRun); err != nil {
					t.Fatal(err)
				}
			}
			res, err := b.GC(ctx, 85)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range res.Deleted {
				got = append(got, p.Project+"/"+p.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.gone, ",") {
				t.Fatalf("GC deleted %v, want %v", got, tc.gone)
			}
			if res.Stalled != tc.stalled {
				t.Fatalf("stalled %v, want %v", res.Stalled, tc.stalled)
			}
			for _, project := range []string{"a", "b"} {
				ps, err := b.List(ctx, project)
				if err != nil {
					t.Fatal(err)
				}
				if len(ps) == 0 {
					t.Fatalf("GC left %s with no point", project)
				}
				newest := map[string]string{"a": "r-3", "b": "r-2"}[project]
				if ps[0].Name != newest {
					t.Fatalf("%s's newest point is %s, want %s", project, ps[0].Name, newest)
				}
			}
			// A store under the mark is left alone.
			r.Usage = Space{Total: 100, Used: 10, Free: 90}
			if res, err := b.GC(ctx, 85); err != nil || len(res.Deleted) != 0 {
				t.Fatalf("GC under the mark deleted %v: %v", res.Deleted, err)
			}
		})
	}
}

// The two floors GC works to: a project's newest MinKeep points, and
// every point younger than MinAge. A runaway that fills the store in
// minutes must not take the morning's history with it.
func TestGCFloors(t *testing.T) {
	// Five points of one project, an hour apart, the newest at epoch.
	const hour = time.Hour
	for _, tc := range []struct {
		name         string
		floors       GCFloors
		gone         []string
		young, kept  int
		otherProject bool
		otherGone    []string
	}{
		{
			name:   "the newest three stay whatever their age",
			floors: GCFloors{MinKeep: 3},
			gone:   []string{"r-1", "r-2"},
			kept:   3,
		},
		{
			name:   "a point inside the age floor stays",
			floors: GCFloors{MinKeep: 1, MinAge: 150 * time.Minute},
			gone:   []string{"r-1", "r-2"},
			young:  2,
			kept:   1,
		},
		{
			name:   "both floors together leave nothing to collect",
			floors: GCFloors{MinKeep: 3, MinAge: 24 * hour},
			young:  2,
			kept:   3,
		},
		{
			name:         "the floor is per project, not per store",
			floors:       GCFloors{MinKeep: 3},
			otherProject: true,
			gone:         []string{"r-1", "r-2"},
			kept:         5, // three of a, and both of b
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &FakeRunner{Usage: Space{Total: 100, Used: 90, Free: 10}, FreedPerDelete: 1}
			b := newStore(t, r)
			b.Floors = &tc.floors
			ctx := context.Background()
			// Take the points oldest first, four hours back to now, then
			// put the clock at the newest so the ages are the test's.
			taken := epoch.Add(-4 * hour)
			b.Now = func() time.Time {
				now := taken
				taken = taken.Add(hour)
				return now
			}
			if _, err := b.Init(ctx, "a"); err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"r-1", "r-2", "r-3", "r-4", "r-5"} {
				if _, err := b.Take(ctx, "a", n, ReasonRun); err != nil {
					t.Fatal(err)
				}
			}
			if tc.otherProject {
				if _, err := b.Init(ctx, "b"); err != nil {
					t.Fatal(err)
				}
				for _, n := range []string{"r-1", "r-2"} {
					if _, err := b.Take(ctx, "b", n, ReasonRun); err != nil {
						t.Fatal(err)
					}
				}
			}
			b.Now = func() time.Time { return epoch }
			res, err := b.GC(ctx, 85)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range res.Deleted {
				got = append(got, p.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.gone, ",") {
				t.Fatalf("GC deleted %v, want %v", got, tc.gone)
			}
			if res.Young != tc.young || res.Kept != tc.kept {
				t.Fatalf("GC reported %d young and %d kept, want %d and %d", res.Young, res.Kept, tc.young, tc.kept)
			}
		})
	}
}

// The box's floors are the ones a backend with no Floors works to, so a
// store provisioned minutes ago collects nothing.
func TestGCFloorsDefaultToTheBoxs(t *testing.T) {
	r := &FakeRunner{Usage: Space{Total: 100, Used: 99, Free: 1}, FreedPerDelete: 50}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"r-1", "r-2", "r-3", "r-4"} {
		if _, err := b.Take(ctx, "a", n, ReasonRun); err != nil {
			t.Fatal(err)
		}
	}
	// newStore's clock steps a minute per point, so every point is inside
	// MinPointAge.
	b.Now = func() time.Time { return epoch.Add(time.Hour) }
	res, err := b.GC(ctx, 85)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 {
		t.Fatalf("GC deleted %+v from a store whose points are minutes old", res.Deleted)
	}
	if res.Kept != MinPointsPerProject || res.Young != 4-MinPointsPerProject {
		t.Fatalf("GC reported %d kept and %d young", res.Kept, res.Young)
	}
}

func TestGCChecksTheHighWaterMark(t *testing.T) {
	r := &FakeRunner{}
	b := newStore(t, r)
	for _, pct := range []int{0, -1, 101} {
		if _, err := b.GC(context.Background(), pct); !errors.Is(err, ErrInvalidName) {
			t.Errorf("GC(%d): err %v, want ErrInvalidName", pct, err)
		}
	}
}

// A point name is a name the store accepts, is never handed out twice,
// and carries the millisecond it was made in, so names sort by when their
// points were taken.
func TestPointNamesAreUniqueAndSortByTime(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for i := 0; i < 500; i++ {
		n := NewPointName()
		if !pointRe.MatchString(n) {
			t.Fatalf("point name %q", n)
		}
		if seen[n] {
			t.Fatalf("point name %q twice", n)
		}
		seen[n] = true
		if ms := n[2:14]; ms < prev {
			t.Fatalf("point name %q is older than %q", n, prev)
		} else {
			prev = ms
		}
	}
}
