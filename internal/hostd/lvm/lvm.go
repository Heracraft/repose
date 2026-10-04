// Package lvm carves thin volumes and snapshots on vg-guests/thin. Every
// operation is idempotent: create checks existence first, mkfs checks for
// a filesystem, remove tolerates absence.
package lvm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// LVM is what the guest state machine needs from the volume layer.
type LVM interface {
	VolumeExists(ctx context.Context, name string) (bool, error)
	CreateVolume(ctx context.Context, name string, bytes uint64) error
	HasFilesystem(ctx context.Context, name string) (bool, error)
	Mkfs(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
	ExtendVolume(ctx context.Context, name string, bytes uint64) error
	Snapshot(ctx context.Context, name, snap string) error
	Activate(ctx context.Context, name string) error
	// VolumeStats returns the allocated size and the bytes in use.
	VolumeStats(ctx context.Context, name string) (size, used uint64, err error)
	// PoolStats returns the thin pool's size and free bytes.
	PoolStats(ctx context.Context) (size, free uint64, err error)
	// Allocated returns the sum of the virtual sizes of the pool's thin
	// volumes, except snapshots (snap-*, which share their origin's blocks
	// and live only while one uploads) and the volume named except.
	Allocated(ctx context.Context, except string) (uint64, error)
	// Fsck runs e2fsck -fp and returns its exit code (0 or 1 is clean).
	Fsck(ctx context.Context, name string) (int, error)
	// ListVolumes returns every volume name in the group except the pool.
	ListVolumes(ctx context.Context) ([]string, error)
	DevPath(name string) string
}

// Real drives the lvm2 tools through a shell.Runner.
type Real struct {
	VG   string // vg-guests
	Pool string // thin
	R    shell.Runner
}

// DevPath is the device node for a volume.
func (l *Real) DevPath(name string) string { return "/dev/" + l.VG + "/" + name }

func (l *Real) lvs(ctx context.Context, target string, cols string) ([]string, error) {
	res, err := l.R.Run(ctx, "lvs", "--noheadings", "--units", "b", "--nosuffix", "--separator", "|", "-o", cols, target)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			rows = append(rows, t)
		}
	}
	return rows, nil
}

// VolumeExists implements LVM.
func (l *Real) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := l.R.Run(ctx, "lvs", l.VG+"/"+name)
	var ee *shell.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return err == nil, err
}

// CreateVolume implements LVM: lvcreate -V <bytes>b -T vg/thin -n name.
func (l *Real) CreateVolume(ctx context.Context, name string, bytes uint64) error {
	ok, err := l.VolumeExists(ctx, name)
	if err != nil || ok {
		return err
	}
	_, err = l.R.Run(ctx, "lvcreate", "-V", fmt.Sprintf("%db", bytes), "-T", l.VG+"/"+l.Pool, "-n", name)
	return err
}

// HasFilesystem implements LVM via blkid.
func (l *Real) HasFilesystem(ctx context.Context, name string) (bool, error) {
	res, err := l.R.Run(ctx, "blkid", "-s", "TYPE", "-o", "value", l.DevPath(name))
	var ee *shell.ExitError
	if errors.As(err, &ee) && ee.Result.ExitCode == 2 {
		return false, nil // blkid: no filesystem found
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(res.Stdout)) != "", nil
}

// Mkfs implements LVM. The inode tables are left to the guest kernel's
// lazy init: a thin volume has no write-zeroes, so lazy_itable_init=0 wrote
// about 670 MB of zeros for a 40 GB volume inside the create, about a
// second of every create on host-01 (DECISIONS I-162).
func (l *Real) Mkfs(ctx context.Context, name string) error {
	has, err := l.HasFilesystem(ctx, name)
	if err != nil || has {
		return err
	}
	_, err = l.R.Run(ctx, "mkfs.ext4", "-q", "-L", "guest", "-E", "lazy_itable_init=1", l.DevPath(name))
	return err
}

// RemoveVolume implements LVM.
func (l *Real) RemoveVolume(ctx context.Context, name string) error {
	ok, err := l.VolumeExists(ctx, name)
	if err != nil || !ok {
		return err
	}
	_, err = l.R.Run(ctx, "lvremove", "-f", l.VG+"/"+name)
	return err
}

// ExtendVolume implements LVM.
func (l *Real) ExtendVolume(ctx context.Context, name string, bytes uint64) error {
	_, err := l.R.Run(ctx, "lvextend", "-L", fmt.Sprintf("%db", bytes), l.VG+"/"+name)
	return err
}

// Snapshot implements LVM.
func (l *Real) Snapshot(ctx context.Context, name, snap string) error {
	ok, err := l.VolumeExists(ctx, snap)
	if err != nil || ok {
		return err
	}
	_, err = l.R.Run(ctx, "lvcreate", "-s", "-n", snap, l.VG+"/"+name)
	return err
}

// Activate implements LVM: thin snapshots are created inactive and skipped.
func (l *Real) Activate(ctx context.Context, name string) error {
	_, err := l.R.Run(ctx, "lvchange", "-ay", "-K", l.VG+"/"+name)
	return err
}

func parseSizePercent(row string) (size uint64, pct float64, err error) {
	f := strings.Split(row, "|")
	if len(f) < 2 {
		return 0, 0, fmt.Errorf("lvs: unexpected row %q", row)
	}
	size, err = strconv.ParseUint(strings.TrimSpace(f[0]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("lvs: size %q: %w", f[0], err)
	}
	p := strings.TrimSpace(f[1])
	if p == "" {
		return size, 0, nil
	}
	pct, err = strconv.ParseFloat(p, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("lvs: data_percent %q: %w", f[1], err)
	}
	return size, pct, nil
}

// VolumeStats implements LVM.
func (l *Real) VolumeStats(ctx context.Context, name string) (uint64, uint64, error) {
	rows, err := l.lvs(ctx, l.VG+"/"+name, "lv_size,data_percent")
	if err != nil {
		return 0, 0, err
	}
	if len(rows) == 0 {
		return 0, 0, fmt.Errorf("lvs: no row for %s", name)
	}
	size, pct, err := parseSizePercent(rows[0])
	if err != nil {
		return 0, 0, err
	}
	return size, uint64(float64(size) * pct / 100), nil
}

// PoolStats implements LVM.
func (l *Real) PoolStats(ctx context.Context) (uint64, uint64, error) {
	rows, err := l.lvs(ctx, l.VG+"/"+l.Pool, "lv_size,data_percent")
	if err != nil {
		return 0, 0, err
	}
	if len(rows) == 0 {
		return 0, 0, fmt.Errorf("lvs: no row for pool %s", l.Pool)
	}
	size, pct, err := parseSizePercent(rows[0])
	if err != nil {
		return 0, 0, err
	}
	used := uint64(float64(size) * pct / 100)
	return size, size - used, nil
}

// Allocated implements LVM with one lvs of the volume group.
func (l *Real) Allocated(ctx context.Context, except string) (uint64, error) {
	rows, err := l.lvs(ctx, l.VG, "lv_name,lv_size,pool_lv")
	if err != nil {
		return 0, err
	}
	var sum uint64
	for _, r := range rows {
		f := strings.Split(r, "|")
		if len(f) < 3 {
			return 0, fmt.Errorf("lvs: unexpected row %q", r)
		}
		name, pool := strings.TrimSpace(f[0]), strings.TrimSpace(f[2])
		if pool != l.Pool || name == except || strings.HasPrefix(name, "snap-") {
			continue
		}
		size, err := strconv.ParseUint(strings.TrimSpace(f[1]), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("lvs: size %q: %w", f[1], err)
		}
		sum += size
	}
	return sum, nil
}

// Fsck implements LVM.
func (l *Real) Fsck(ctx context.Context, name string) (int, error) {
	_, err := l.R.Run(ctx, "e2fsck", "-fp", l.DevPath(name))
	var ee *shell.ExitError
	if errors.As(err, &ee) {
		return ee.Result.ExitCode, nil
	}
	return 0, err
}

// ListVolumes implements LVM.
func (l *Real) ListVolumes(ctx context.Context) ([]string, error) {
	rows, err := l.lvs(ctx, l.VG, "lv_name")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if r != l.Pool {
			out = append(out, r)
		}
	}
	return out, nil
}

// FakeVolume is the in-memory model of a thin volume.
type FakeVolume struct {
	Size  uint64
	Used  uint64
	HasFS bool
	Data  []byte
}

// Fake is an in-memory LVM for the state machine tests. FailOn makes a
// named operation fail: keys are "create", "mkfs", "remove", "extend",
// "snapshot", "activate", "fsck".
type Fake struct {
	mu       sync.Mutex
	Volumes  map[string]*FakeVolume
	PoolSize uint64
	PoolFree uint64
	FailOn   map[string]error
	FsckExit int
	Ops      []string
}

// NewFake returns a Fake with a 1 TB pool.
func NewFake() *Fake {
	return &Fake{Volumes: map[string]*FakeVolume{}, PoolSize: 1 << 40, PoolFree: 1 << 40, FailOn: map[string]error{}}
}

func (f *Fake) fail(op string) error {
	f.Ops = append(f.Ops, op)
	return f.FailOn[op]
}

// DevPath implements LVM.
func (f *Fake) DevPath(name string) string { return "/dev/vg-guests/" + name }

// VolumeExists implements LVM.
func (f *Fake) VolumeExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.Volumes[name]
	return ok, nil
}

// CreateVolume implements LVM.
func (f *Fake) CreateVolume(_ context.Context, name string, bytes uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("create"); err != nil {
		return err
	}
	if _, ok := f.Volumes[name]; ok {
		return nil
	}
	f.Volumes[name] = &FakeVolume{Size: bytes}
	return nil
}

// HasFilesystem implements LVM.
func (f *Fake) HasFilesystem(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.Volumes[name]
	return ok && v.HasFS, nil
}

// Mkfs implements LVM. The inode tables are left to the guest kernel's
// lazy init: a thin volume has no write-zeroes, so lazy_itable_init=0 wrote
// about 670 MB of zeros for a 40 GB volume inside the create, about a
// second of every create on host-01 (DECISIONS I-162).
func (f *Fake) Mkfs(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("mkfs"); err != nil {
		return err
	}
	v, ok := f.Volumes[name]
	if !ok {
		return fmt.Errorf("mkfs: %s does not exist", name)
	}
	v.HasFS = true
	return nil
}

// RemoveVolume implements LVM.
func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("remove"); err != nil {
		return err
	}
	delete(f.Volumes, name)
	return nil
}

// ExtendVolume implements LVM.
func (f *Fake) ExtendVolume(_ context.Context, name string, bytes uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("extend"); err != nil {
		return err
	}
	v, ok := f.Volumes[name]
	if !ok {
		return fmt.Errorf("lvextend: %s does not exist", name)
	}
	v.Size = bytes
	return nil
}

// Snapshot implements LVM.
func (f *Fake) Snapshot(_ context.Context, name, snap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("snapshot"); err != nil {
		return err
	}
	v, ok := f.Volumes[name]
	if !ok {
		return fmt.Errorf("lvcreate -s: %s does not exist", name)
	}
	f.Volumes[snap] = &FakeVolume{Size: v.Size, Used: v.Used, HasFS: v.HasFS, Data: append([]byte(nil), v.Data...)}
	return nil
}

// Activate implements LVM.
func (f *Fake) Activate(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fail("activate")
}

// VolumeStats implements LVM.
func (f *Fake) VolumeStats(_ context.Context, name string) (uint64, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.Volumes[name]
	if !ok {
		return 0, 0, fmt.Errorf("lvs: %s does not exist", name)
	}
	return v.Size, v.Used, nil
}

// PoolStats implements LVM.
func (f *Fake) PoolStats(context.Context) (uint64, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.PoolSize, f.PoolFree, nil
}

// Allocated implements LVM.
func (f *Fake) Allocated(_ context.Context, except string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sum uint64
	for name, v := range f.Volumes {
		if name != except && !strings.HasPrefix(name, "snap-") {
			sum += v.Size
		}
	}
	return sum, nil
}

// Fsck implements LVM.
func (f *Fake) Fsck(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("fsck"); err != nil {
		return 0, err
	}
	return f.FsckExit, nil
}

// ListVolumes implements LVM.
func (f *Fake) ListVolumes(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.Volumes {
		out = append(out, k)
	}
	return out, nil
}

// SetData replaces a fake volume's bytes (tests use it before a snapshot).
func (f *Fake) SetData(name string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.Volumes[name]; ok {
		v.Data = append([]byte(nil), data...)
		v.Used = uint64(len(data))
	}
}

// GetData returns a fake volume's bytes.
func (f *Fake) GetData(name string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.Volumes[name]; ok {
		return append([]byte{}, v.Data...)
	}
	return nil
}
