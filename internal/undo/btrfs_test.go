package undo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	r := &FakeRunner{Exclusive: map[string]uint64{"points/todo-app/r-2": 104857600}}
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
	if ps[1].Bytes != 104857600 {
		t.Fatalf("point bytes %d", ps[1].Bytes)
	}
	if ps[0].Bytes != 0 {
		t.Fatalf("a point with no qgroup row reported %d bytes, want 0", ps[0].Bytes)
	}
	if ps[0].Reason != ReasonRun {
		t.Fatalf("reason %q", ps[0].Reason)
	}
	// An empty project has no points and no error.
	if ps, err := b.List(ctx, "other"); err != nil || len(ps) != 0 {
		t.Fatalf("points of a project with none: %v %v", ps, err)
	}
}

// Quotas off is 0 bytes, not a guess and not a failure.
func TestListWithoutQuotas(t *testing.T) {
	r := &FakeRunner{QuotasOff: true}
	b := newStore(t, r)
	ctx := context.Background()
	if _, err := b.Init(ctx, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Take(ctx, "todo-app", "r-1", ReasonManual); err != nil {
		t.Fatal(err)
	}
	ps, err := b.List(ctx, "todo-app")
	if err != nil || len(ps) != 1 || ps[0].Bytes != 0 {
		t.Fatalf("points %+v: %v", ps, err)
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
	s, err := b.Space(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Space{Total: 4294967296, Used: 393216, Free: 3741319168}
	if s != want {
		t.Fatalf("space %+v, want %+v", s, want)
	}
	if _, err := New("/x", stubRunner{"Overall:\n"}).Space(context.Background()); err == nil {
		t.Fatal("usage with no figures was read as space")
	}
}

// GC takes the oldest points until the store is under the mark, and
// never a project's newest point.
func TestGCKeepsTheNewestPointOfEachProject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		freed uint64
		gone  []string
	}{
		{"one delete is enough", 10, []string{"a/r-1"}},
		{"nothing is freed", 0, []string{"a/r-1", "b/r-1", "a/r-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &FakeRunner{Usage: Space{Total: 100, Used: 90, Free: 10}, FreedPerDelete: tc.freed}
			b := newStore(t, r)
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
			gone, err := b.GC(ctx, 85)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range gone {
				got = append(got, p.Project+"/"+p.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.gone, ",") {
				t.Fatalf("GC deleted %v, want %v", got, tc.gone)
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
			if gone, err := b.GC(ctx, 85); err != nil || len(gone) != 0 {
				t.Fatalf("GC under the mark deleted %v: %v", gone, err)
			}
		})
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
