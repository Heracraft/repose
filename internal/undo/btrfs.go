package undo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// MountOptions are the store's mount options. user_subvol_rm_allowed is
// what lets the login user delete its own points without sudo,
// compress=zstd:1 is the level the measurements were taken at
// (docs/research/wrapper-undo.md, SPINE.md contract 5), and
// discard=async is what gives the space back: without it btrfs frees a
// deleted point's blocks inside the filesystem and the sparse image
// stays as large as it ever grew, so a store that filled once never
// gives the box its disk back.
//
// btrfs has discard=async since Linux 5.6 and an older kernel refuses
// the whole mount, so mountOptionsBase is what a mount falls back to.
// ops/install/machine-install.sh makes the same choice for its mount
// unit; the two must move together.
const (
	mountOptionsBase = "noatime,compress=zstd:1,user_subvol_rm_allowed"
	MountOptions     = mountOptionsBase + ",discard=async"
)

// The store's layout: one mountpoint holding two subvolumes.
//
//	<store>/projects/<slug>            the live area, writable
//	<store>/points/<slug>/<name>       a point, read only
//	<store>/points/<slug>/.meta/<name>.json   why and when it was taken
//
// btrfs records when a subvolume was made but nothing about why, so the
// reason lives beside the point in a sidecar. A point whose sidecar is
// gone still rewinds.
const (
	metaDir       = ".meta"
	stagingPrefix = ".rewind-" // the writable snapshot a rewind has not swapped in yet
	asidePrefix   = ".old-"    // the pre-rewind live area a rewind has not deleted yet
)

// Btrfs is the undo backend on btrfs subvolumes.
type Btrfs struct {
	// Store is the store's mountpoint on the box.
	Store string
	// Image is the file the store lives in on a box with no spare device.
	// Empty means <Store>.img.
	Image string
	// Device, when set, is made the store instead of an image file
	// (`machine-install.sh --device`).
	Device string
	// R runs btrfs, mkfs.btrfs, blkid and mount.
	R Runner
	// Subvol reports whether a path is a subvolume; nil means the stat
	// check, which is the real one. A test with a FakeRunner sets it to
	// that runner's model (FakeRunner.Subvol).
	Subvol func(path string) (bool, error)
	// Now is the clock a point's TakenAt comes from; nil means time.Now.
	Now func() time.Time
	// Floors are what GC will not collect however full the store is;
	// nil means the box's own, MinPointAge and MinPointsPerProject.
	Floors *GCFloors
	// BackingFree reports the bytes free on the filesystem holding the
	// store's image, which is what bounds an image-backed store's
	// space; nil means the statfs call, which is the real one.
	BackingFree func(path string) (uint64, error)
	// Scan, when set, is a second holder scan with more privilege than
	// this process has. The agent points it at itself under sudo, so a
	// root-owned writer in the live area is refused rather than missed:
	// /proc/<pid>/cwd of another user's process denies a plain reader,
	// and a holder nothing can see is a holder a rewind loses writes
	// under. nil means the in-process scan alone.
	Scan func(ctx context.Context, root string) ([]Holder, error)
}

// floors are the floors GC works to.
func (b *Btrfs) floors() GCFloors {
	if b.Floors != nil {
		return *b.Floors
	}
	return GCFloors{MinAge: MinPointAge, MinKeep: MinPointsPerProject}
}

// New returns the backend for a store mountpoint.
func New(store string, r Runner) *Btrfs { return &Btrfs{Store: store, R: r} }

func (b *Btrfs) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Btrfs) projectsDir() string            { return filepath.Join(b.Store, "projects") }
func (b *Btrfs) pointsRoot() string             { return filepath.Join(b.Store, "points") }
func (b *Btrfs) livePath(project string) string { return filepath.Join(b.projectsDir(), project) }
func (b *Btrfs) pointsDir(project string) string {
	return filepath.Join(b.pointsRoot(), project)
}
func (b *Btrfs) pointPath(project, name string) string {
	return filepath.Join(b.pointsDir(project), name)
}
func (b *Btrfs) metaPath(project, name string) string {
	return filepath.Join(b.pointsDir(project), metaDir, name+".json")
}

// image is the store's backing file when no device was given.
func (b *Btrfs) image() string {
	if b.Image != "" {
		return b.Image
	}
	return b.Store + ".img"
}

func (b *Btrfs) btrfs(ctx context.Context, args ...string) (Result, error) {
	return b.R.Run(ctx, append([]string{"btrfs"}, args...)...)
}

// btrfsSubvolInode is the inode a btrfs subvolume's root directory
// always has, the toplevel one included.
const btrfsSubvolInode = 256

// isSubvolume reports whether path is a btrfs subvolume.
func (b *Btrfs) isSubvolume(path string) (bool, error) {
	if b.Subvol != nil {
		return b.Subvol(path)
	}
	return statSubvolume(path)
}

// statSubvolume is the subvolume check by stat: a subvolume's root is a
// directory whose inode is 256. `btrfs subvolume show` would say the
// same, but it searches the b-tree, which needs root — and the agent
// runs as the box's own login user, which is the point of the store's
// user_subvol_rm_allowed mount option.
func statSubvolume(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("undo: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok {
		return false, nil
	}
	return st.Ino == btrfsSubvolInode, nil
}

// ensureSubvolume creates path as a subvolume unless it is one already.
func (b *Btrfs) ensureSubvolume(ctx context.Context, path string) error {
	ok, err := b.isSubvolume(path)
	if err != nil || ok {
		return err
	}
	if _, err := b.btrfs(ctx, "subvolume", "create", path); err != nil {
		return fmt.Errorf("btrfs subvolume create: %w", err)
	}
	return nil
}

// snapshot takes a snapshot of src at dest, read only for a point and
// writable for a live area. An allocation failure is ErrNoSpace: btrfs
// reports a full store as a write error and nothing above would know
// what it was.
func (b *Btrfs) snapshot(ctx context.Context, src, dest string, readOnly bool) error {
	argv := []string{"subvolume", "snapshot"}
	if readOnly {
		argv = append(argv, "-r")
	}
	if _, err := b.btrfs(ctx, append(argv, src, dest)...); err != nil {
		if stderrHas(err, "No space left on device", "no space left") {
			return fmt.Errorf("%w: btrfs subvolume snapshot", ErrNoSpace)
		}
		return fmt.Errorf("btrfs subvolume snapshot: %w", err)
	}
	return nil
}

// deleteSubvolume removes a subvolume. Absence is success. commit waits
// for the transaction, which is what makes the next Space reading
// honest: a plain delete removes the directory at once and the blocks
// later.
func (b *Btrfs) deleteSubvolume(ctx context.Context, path string, commit bool) error {
	ok, err := b.isSubvolume(path)
	if err != nil || !ok {
		return err
	}
	// A point is a read-only subvolume, and the login user cannot delete
	// one: btrfs answers "Could not destroy subvolume/snapshot: Read-only
	// file system" however user_subvol_rm_allowed is set (measured on this
	// box, 2026-10-19). Clearing the flag first works as that user, so
	// retention and GC run without root. A failure here is not fatal: a
	// writable subvolume has nothing to clear, and the delete below says
	// what went wrong either way.
	_, _ = b.btrfs(ctx, "property", "set", "-ts", path, "ro", "false")
	argv := []string{"subvolume", "delete"}
	if commit {
		argv = append(argv, "--commit-after")
	}
	if _, err := b.btrfs(ctx, append(argv, path)...); err != nil {
		return fmt.Errorf("btrfs subvolume delete: %w", err)
	}
	return nil
}

// subvolCreated is when btrfs made the subvolume, for a point whose
// sidecar is gone.
func (b *Btrfs) subvolCreated(ctx context.Context, path string) (time.Time, error) {
	res, err := b.btrfs(ctx, "subvolume", "show", path)
	if err != nil {
		return time.Time{}, fmt.Errorf("btrfs subvolume show: %w", err)
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key != "Creation time" {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05 -0700", strings.TrimSpace(val))
		if err != nil {
			return time.Time{}, fmt.Errorf("btrfs subvolume show: creation time: %w", err)
		}
		return t.UTC(), nil
	}
	return time.Time{}, errors.New("btrfs subvolume show: no creation time")
}

// Provision implements Backend. It makes the store out of a device or a
// sparse file, puts btrfs on it, mounts it and creates the layout —
// each step skipped when it is already done, so a second run changes
// nothing. The mount is this process's; the installer's mount unit is
// what brings the store back after a reboot (SPINE.md contract 5).
func (b *Btrfs) Provision(ctx context.Context, sizeBytes uint64) error {
	if b.Store == "" {
		return errors.New("undo: no store path")
	}
	if err := os.MkdirAll(b.Store, 0o755); err != nil {
		return fmt.Errorf("undo: store directory: %w", err)
	}
	mounted, err := isMountpoint(b.Store)
	if err != nil {
		return fmt.Errorf("undo: store directory: %w", err)
	}
	if !mounted {
		dev := b.Device
		if dev == "" {
			if sizeBytes == 0 {
				return errors.New("undo: a new store needs a size")
			}
			dev = b.image()
			if err := ensureImage(dev, sizeBytes); err != nil {
				return err
			}
		}
		if err := b.ensureFilesystem(ctx, dev); err != nil {
			return err
		}
		if err := b.mount(ctx, dev); err != nil {
			return err
		}
	}
	for _, d := range []string{b.projectsDir(), b.pointsRoot()} {
		if err := b.ensureSubvolume(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

// mount mounts dev on the store, falling back to a kernel older than
// 5.6 by dropping discard=async: btrfs refuses the whole mount on an
// option it does not know, and a store that cannot mount is a box with
// no undo at all.
func (b *Btrfs) mount(ctx context.Context, dev string) error {
	prefix := ""
	if b.Device == "" {
		prefix = "loop,"
	}
	_, err := b.R.Run(ctx, "mount", "-o", prefix+MountOptions, dev, b.Store)
	if err == nil {
		return nil
	}
	if !stderrHas(err, "discard") && !stderrHas(err, "bad option", "unrecognized mount option", "Invalid argument") {
		return fmt.Errorf("mount the store: %w", err)
	}
	if _, err2 := b.R.Run(ctx, "mount", "-o", prefix+mountOptionsBase, dev, b.Store); err2 != nil {
		return fmt.Errorf("mount the store: %w", err2)
	}
	return nil
}

// Ready reports whether the store is a mounted btrfs with its layout in
// place, which is what Provision leaves behind.
func (b *Btrfs) Ready(ctx context.Context) (bool, error) {
	mounted, err := isMountpoint(b.Store)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !mounted {
		return false, err
	}
	for _, d := range []string{b.projectsDir(), b.pointsRoot()} {
		ok, err := b.isSubvolume(d)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// ensureImage makes the store's backing file sparse at the asked size.
// A file that is there already keeps the size it has: growing a store
// is a resize of the filesystem in it, which is not this pass.
func ensureImage(path string, sizeBytes uint64) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("undo: store image: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("undo: store image: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(int64(sizeBytes)); err != nil {
		return fmt.Errorf("undo: store image: %w", err)
	}
	return nil
}

// ensureFilesystem puts btrfs on dev unless it holds a filesystem
// already. blkid exits 2 when it finds none, as it does for the hosted
// path's volumes (internal/hostd/lvm).
func (b *Btrfs) ensureFilesystem(ctx context.Context, dev string) error {
	res, err := b.R.Run(ctx, "blkid", "-s", "TYPE", "-o", "value", dev)
	var ee *ExitError
	switch {
	case err == nil && strings.TrimSpace(string(res.Stdout)) != "":
		return nil
	case err == nil, errors.As(err, &ee) && ee.Result.ExitCode == 2:
	default:
		return fmt.Errorf("blkid: %w", err)
	}
	if _, err := b.R.Run(ctx, "mkfs.btrfs", "-q", "-L", "repose-undo", dev); err != nil {
		return fmt.Errorf("mkfs.btrfs: %w", err)
	}
	return nil
}

// isMountpoint reports whether path is where a filesystem is mounted: it
// and its parent sit on different devices. btrfs is in a file here, so
// there is no device name to look for.
func isMountpoint(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	pfi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	pst, pok := pfi.Sys().(*syscall.Stat_t)
	if !ok || !pok {
		return false, nil
	}
	return st.Dev != pst.Dev, nil
}

// Init implements Backend.
func (b *Btrfs) Init(ctx context.Context, project string) (string, error) {
	if err := checkProject(project); err != nil {
		return "", err
	}
	live := b.livePath(project)
	if err := b.ensureSubvolume(ctx, live); err != nil {
		return "", err
	}
	if err := os.MkdirAll(b.pointsDir(project), 0o755); err != nil {
		return "", fmt.Errorf("undo: points directory: %w", err)
	}
	return live, nil
}

// Take implements Backend. A point of the same name that exists already
// is the answer, the way an LVM snapshot volume that exists already is
// (internal/hostd/lvm.Snapshot): the CLI reaches the box over SSH and
// resends what it did not see the answer to.
func (b *Btrfs) Take(ctx context.Context, project, name, reason string) (Point, error) {
	if err := checkProject(project); err != nil {
		return Point{}, err
	}
	if err := checkPoint(name); err != nil {
		return Point{}, err
	}
	if reason == "" {
		reason = ReasonManual
	}
	if err := checkReason(reason); err != nil {
		return Point{}, err
	}
	dest := b.pointPath(project, name)
	if ok, err := b.isSubvolume(dest); err != nil {
		return Point{}, err
	} else if ok {
		return b.point(ctx, project, name)
	}
	live := b.livePath(project)
	if ok, err := b.isSubvolume(live); err != nil {
		return Point{}, err
	} else if !ok {
		return Point{}, fmt.Errorf("%w: %s has no live area", ErrNotFound, project)
	}
	if err := os.MkdirAll(b.pointsDir(project), 0o755); err != nil {
		return Point{}, fmt.Errorf("undo: points directory: %w", err)
	}
	if err := b.snapshot(ctx, live, dest, true); err != nil {
		return Point{}, err
	}
	// Full precision, not a truncated second: List sorts on this, and two
	// points inside one second (a rewind's safety point and the point it
	// rewinds to) would then tie and come back in name order, which is not
	// newest first. A caller that shows a time rounds it itself.
	p := Point{Name: name, Project: project, Reason: reason, TakenAt: b.now().UTC()}
	if err := writeMeta(b.metaPath(project, name), p); err != nil {
		return Point{}, err
	}
	return p, nil
}

// point reads one point's record.
func (b *Btrfs) point(ctx context.Context, project, name string) (Point, error) {
	p := Point{Name: name, Project: project}
	m, err := readMeta(b.metaPath(project, name))
	switch {
	case err == nil:
		p.Reason, p.TakenAt = m.Reason, m.TakenAt.UTC()
	case errors.Is(err, os.ErrNotExist):
		// No sidecar: btrfs knows when the subvolume was made and
		// nothing knows why it was taken. Reading even that wants root,
		// so on a box where the agent is the login user a point whose
		// record is gone carries no time rather than a made-up one.
		if t, terr := b.subvolCreated(ctx, b.pointPath(project, name)); terr == nil {
			p.TakenAt = t
		}
	default:
		return p, err
	}
	return p, nil
}

// List implements Backend.
func (b *Btrfs) List(ctx context.Context, project string) ([]Point, error) {
	if err := checkProject(project); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(b.pointsDir(project))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("undo: points of %s: %w", project, err)
	}
	var out []Point
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := b.point(ctx, project, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TakenAt.Equal(out[j].TakenAt) {
			return out[i].Name > out[j].Name
		}
		return out[i].TakenAt.After(out[j].TakenAt)
	})
	return out, nil
}

// Rewind implements Backend. It verifies quiescence, takes a point of
// the current state, swaps by rename and then deletes what it moved
// aside, so a failure anywhere leaves the pre-rewind state live. The
// name it returns is the point of the pre-rewind state.
func (b *Btrfs) Rewind(ctx context.Context, project, name string) (string, error) {
	if err := checkProject(project); err != nil {
		return "", err
	}
	if err := checkPoint(name); err != nil {
		return "", err
	}
	point := b.pointPath(project, name)
	if ok, err := b.isSubvolume(point); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("%w: %s of %s", ErrNotFound, name, project)
	}
	// Quiescence first. A writer with its cwd in the live subvolume
	// survives the swap and goes on writing where no path reaches it, so
	// its last writes are lost without a word
	// (docs/research/wrapper-undo.md). Stopping it is the caller's job.
	hs, err := b.Holders(ctx, project)
	if err != nil {
		return "", err
	}
	if len(hs) > 0 {
		return "", fmt.Errorf("%w: still open by %s", ErrBusy, processes(len(hs)))
	}
	b.sweep(ctx, project)
	// ReasonRewind, not ReasonManual: this point holds the state the user
	// is escaping, so nothing may pick it as the newest point to go back
	// to (internal/cli/rewind.go).
	safety := NewPointName()
	if _, err := b.Take(ctx, project, safety, ReasonRewind); err != nil {
		return "", err
	}
	staging := filepath.Join(b.projectsDir(), stagingPrefix+project+"-"+safety)
	if err := b.snapshot(ctx, point, staging, false); err != nil {
		return "", err
	}
	live := b.livePath(project)
	aside := filepath.Join(b.projectsDir(), asidePrefix+project+"-"+safety)
	if err := os.Rename(live, aside); err != nil {
		// The rename is what the caller is told about; the staging
		// subvolume it leaves is the next sweep's, not a second error.
		_ = b.deleteSubvolume(ctx, staging, false)
		return "", fmt.Errorf("undo: rewind %s: move the live area aside: %w", project, err)
	}
	if err := os.Rename(staging, live); err != nil {
		if rerr := os.Rename(aside, live); rerr != nil {
			// Both renames failed: say where the pre-rewind state is,
			// relative to the store, because nothing else can find it.
			return "", fmt.Errorf("undo: rewind %s: the live area is at projects/%s: %w", project, filepath.Base(aside), err)
		}
		// As above: the swap's error is the one to report.
		_ = b.deleteSubvolume(ctx, staging, false)
		return "", fmt.Errorf("undo: rewind %s: swap the point in: %w", project, err)
	}
	// The rewind has happened. A delete that fails now only holds space,
	// which the next rewind's sweep takes, so it does not fail a rewind
	// that worked.
	_ = b.deleteSubvolume(ctx, aside, false)
	return safety, nil
}

// Holders returns the processes whose working directory, open files or
// memory mappings are in the project's live area. It is what Rewind
// refuses on, and what the agent's quiesce reports after it has stopped
// them.
//
// It scans twice where it can: once in this process, which sees the
// login user's own processes, and once through Scan, which sees a
// process of another user that /proc hides from a plain reader. A Scan
// that cannot run is not a failure — the in-process scan still refuses
// the holders it found, and a quiesce that cannot run at all is worse
// than one that sees less.
func (b *Btrfs) Holders(ctx context.Context, project string) ([]Holder, error) {
	if err := checkProject(project); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(b.livePath(project))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("undo: the live area of %s: %w", project, err)
	}
	hs, err := holders(root)
	if err != nil {
		return nil, fmt.Errorf("undo: processes of %s: %w", project, err)
	}
	if b.Scan != nil {
		more, err := b.Scan(ctx, root)
		if err == nil {
			hs = mergeHolders(hs, more)
		}
	}
	return hs, nil
}

// mergeHolders is the union of two scans by pid, lowest pid first, so
// the same process seen by both is reported once.
func mergeHolders(a, b []Holder) []Holder {
	seen := make(map[int]bool, len(a)+len(b))
	out := make([]Holder, 0, len(a)+len(b))
	for _, hs := range [][]Holder{a, b} {
		for _, h := range hs {
			if seen[h.PID] {
				continue
			}
			seen[h.PID] = true
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// processes is "1 process" or "3 processes": the count is what ErrBusy
// carries, never the paths they hold (docs/ops/OBSERVABILITY.md).
func processes(n int) string {
	if n == 1 {
		return "1 process"
	}
	return fmt.Sprintf("%d processes", n)
}

// sweep removes what a rewind that died halfway left in projects/: the
// staging subvolume it had not swapped in, and the pre-rewind subvolume
// it had not deleted. Both carry a dotted prefix no slug can have.
func (b *Btrfs) sweep(ctx context.Context, project string) {
	ents, err := os.ReadDir(b.projectsDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, stagingPrefix+project+"-") && !strings.HasPrefix(n, asidePrefix+project+"-") {
			continue
		}
		_ = b.deleteSubvolume(ctx, filepath.Join(b.projectsDir(), n), false)
	}
}

// Fork implements Backend: a writable snapshot of a point, which copies
// no bytes and takes no time (docs/research/wrapper-undo.md).
func (b *Btrfs) Fork(ctx context.Context, srcProject, name, newProject string) error {
	if err := checkProject(srcProject); err != nil {
		return err
	}
	if err := checkPoint(name); err != nil {
		return err
	}
	if err := checkProject(newProject); err != nil {
		return err
	}
	point := b.pointPath(srcProject, name)
	if ok, err := b.isSubvolume(point); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: %s of %s", ErrNotFound, name, srcProject)
	}
	dest := b.livePath(newProject)
	if ok, err := b.isSubvolume(dest); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("%w: %s", ErrExists, newProject)
	}
	if err := b.snapshot(ctx, point, dest, false); err != nil {
		return err
	}
	if err := os.MkdirAll(b.pointsDir(newProject), 0o755); err != nil {
		return fmt.Errorf("undo: points directory: %w", err)
	}
	return nil
}

// Delete implements Backend.
func (b *Btrfs) Delete(ctx context.Context, project, name string) error {
	return b.deletePoint(ctx, project, name, false)
}

func (b *Btrfs) deletePoint(ctx context.Context, project, name string, commit bool) error {
	if err := checkProject(project); err != nil {
		return err
	}
	if err := checkPoint(name); err != nil {
		return err
	}
	if err := b.deleteSubvolume(ctx, b.pointPath(project, name), commit); err != nil {
		return err
	}
	if err := os.Remove(b.metaPath(project, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("undo: point record: %w", err)
	}
	return nil
}

// Space implements Backend from `btrfs filesystem usage --raw`. df has no
// honest figure for btrfs: it reports what is allocated, which left a
// 100 MB rewrite looking free (docs/research/wrapper-undo.md).
func (b *Btrfs) Space(ctx context.Context) (Space, error) {
	res, err := b.btrfs(ctx, "filesystem", "usage", "--raw", b.Store)
	if err != nil {
		return Space{}, fmt.Errorf("btrfs filesystem usage: %w", err)
	}
	var s Space
	var haveTotal, haveUsed, haveFree bool
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		// "Free (estimated): 3741319168 (min: 1874853888)" — the first
		// number is the one, the parenthetical is the worst case.
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "Device size":
			s.Total, haveTotal = n, true
		case "Used":
			s.Used, haveUsed = n, true
		case "Free (estimated)":
			s.Free, haveFree = n, true
		}
	}
	if !haveTotal || !haveUsed || !haveFree {
		return s, errors.New("btrfs filesystem usage: no overall figures")
	}
	return b.bound(s), nil
}

// bound is Space as a user can rely on it. An image-backed store is a
// sparse file: btrfs reports the image's whole size as the device, so a
// 40 GB store on a filesystem with 6 GB left answers "34 GB free" and
// then a point fails with ENOSPC out of nowhere. The honest free figure
// is the smaller of the two, and the honest total is what is used plus
// that — which is also what makes GC's high-water mark fire before the
// box's own disk fills.
func (b *Btrfs) bound(s Space) Space {
	if b.Device != "" {
		return s
	}
	free, err := b.backingFree(filepath.Dir(b.image()))
	if err != nil || free >= s.Free {
		return s
	}
	s.Free = free
	s.Total = s.Used + free
	return s
}

func (b *Btrfs) backingFree(dir string) (uint64, error) {
	if b.BackingFree != nil {
		return b.BackingFree(dir)
	}
	return statfsFree(dir)
}

// statfsFree is the bytes an unprivileged writer can still put on the
// filesystem at dir: Bavail, not Bfree, which counts the blocks only
// root may have.
func statfsFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("undo: free space of %s: %w", dir, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// GC implements Backend: the oldest points go until the store is under
// the high-water mark, within three floors that keep a runaway from
// eating the history that would undo it.
//
//   - A project's newest MinKeep points stay, whatever their age.
//   - A point inside MinAge stays: a point from ten minutes ago is the
//     one the user is about to reach for.
//   - A delete that frees nothing stops the loop. Once the space is
//     held by the live areas rather than by the points, deleting more
//     points empties the history and frees nothing, which is the one
//     way a GC can turn a full store into a full store with no undo.
func (b *Btrfs) GC(ctx context.Context, highWaterPercent int) (GCResult, error) {
	var res GCResult
	if highWaterPercent < 1 || highWaterPercent > 100 {
		return res, fmt.Errorf("%w: high-water %d is not 1 to 100", ErrInvalidName, highWaterPercent)
	}
	sp, err := b.Space(ctx)
	if err != nil {
		return res, err
	}
	cand, young, kept, err := b.collectable(ctx)
	if err != nil {
		return res, err
	}
	res.Young, res.Kept = young, kept
	if !over(sp, highWaterPercent) {
		return res, nil
	}
	for _, p := range cand {
		before := sp.Used
		if err := b.deletePoint(ctx, p.Project, p.Name, true); err != nil {
			return res, err
		}
		res.Deleted = append(res.Deleted, p)
		if sp, err = b.Space(ctx); err != nil {
			return res, err
		}
		if !over(sp, highWaterPercent) {
			break
		}
		if sp.Used >= before {
			res.Stalled = true
			break
		}
	}
	return res, nil
}

// over reports whether the store is at or above the high-water mark.
func over(s Space, highWaterPercent int) bool {
	if s.Total == 0 {
		return false
	}
	return s.Used*100 >= s.Total*uint64(highWaterPercent)
}

// collectable is every point GC may take, oldest first, and how many it
// passed over: young for the ones inside MinAge, kept for the ones a
// project holds as its floor.
//
// A point whose sidecar is gone and whose creation time cannot be read
// has no TakenAt, and counts as old: the alternative is a store of
// sidecar-less points that nothing can ever collect.
func (b *Btrfs) collectable(ctx context.Context) (cand []Point, young, kept int, err error) {
	ents, err := os.ReadDir(b.pointsRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("undo: points: %w", err)
	}
	fl := b.floors()
	cutoff := b.now().Add(-fl.MinAge)
	for _, e := range ents {
		// A directory that is not a slug is not a project of ours, and
		// nothing in points/ is worth failing a GC over.
		if !e.IsDir() || checkProject(e.Name()) != nil {
			continue
		}
		ps, err := b.List(ctx, e.Name())
		if err != nil {
			return nil, 0, 0, err
		}
		// List is newest first, so the floor is the head of the slice.
		floor := fl.MinKeep
		if floor < 1 {
			floor = 1 // a project always keeps its newest point
		}
		if len(ps) <= floor {
			kept += len(ps)
			continue
		}
		kept += floor
		for _, p := range ps[floor:] {
			if !p.TakenAt.IsZero() && p.TakenAt.After(cutoff) {
				young++
				continue
			}
			cand = append(cand, p)
		}
	}
	sort.Slice(cand, func(i, j int) bool {
		if cand[i].TakenAt.Equal(cand[j].TakenAt) {
			return cand[i].Name < cand[j].Name
		}
		return cand[i].TakenAt.Before(cand[j].TakenAt)
	})
	return cand, young, kept, nil
}

// meta is a point's sidecar: what btrfs does not record.
type meta struct {
	Reason  string    `json:"reason"`
	TakenAt time.Time `json:"taken_at"`
}

func writeMeta(path string, p Point) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("undo: point record: %w", err)
	}
	buf, err := json.Marshal(meta{Reason: p.Reason, TakenAt: p.TakenAt})
	if err != nil {
		return fmt.Errorf("undo: point record: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
		return fmt.Errorf("undo: point record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("undo: point record: %w", err)
	}
	return nil
}

func readMeta(path string) (meta, error) {
	var m meta
	buf, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(buf, &m); err != nil {
		return m, fmt.Errorf("undo: point record: %w", err)
	}
	return m, nil
}

var _ Backend = (*Btrfs)(nil)
