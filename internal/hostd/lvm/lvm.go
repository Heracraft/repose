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
	// MkfsShare makes the small ext4 of a login-share volume (I-464).
	MkfsShare(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
	ExtendVolume(ctx context.Context, name string, bytes uint64) error
	Snapshot(ctx context.Context, name, snap string) error
	Activate(ctx context.Context, name string) error
	// VolumeStats returns the allocated size and the bytes in use.
	VolumeStats(ctx context.Context, name string) (size, used uint64, err error)
	// PoolStats returns the thin pool's size and free bytes.
	PoolStats(ctx context.Context) (size, free uint64, err error)
	// PoolMetadataPercent returns how full the thin pool's metadata is:
	// a pool out of metadata fails every volume's writes as one out of
	// data does (DECISIONS I-586).
	PoolMetadataPercent(ctx context.Context) (float64, error)
	// PoolStatus reads the thin pool's state in one lvs call (DECISIONS
	// I-644). Its error wraps ErrPoolUnavailable when the pool cannot hold
	// a guest's volume: inactive, unreadable, or failed.
	PoolStatus(ctx context.Context) (PoolState, error)
	// VolumeActive reports whether a volume's device is active.
	VolumeActive(ctx context.Context, name string) (bool, error)
	// Fsck runs e2fsck -fp and returns its exit code (0 or 1 is clean).
	Fsck(ctx context.Context, name string) (int, error)
	// ListVolumes returns every volume name in the group except the pool.
	ListVolumes(ctx context.Context) ([]string, error)
	DevPath(name string) string
}

// ErrPoolUnavailable is a thin pool that cannot serve a guest's volume:
// not active (after a host reboot that left it down, DECISIONS I-643),
// missing with its VG, or failed (health failed or metadata_read_only).
// Every mutator refuses it before calling lvm, which would otherwise
// activate the pool itself, with its own thin_check, behind the pool
// unit's back (I-644). out_of_data is not unavailable: PoolFull pages for
// it, and the volumes still read.
var ErrPoolUnavailable = errors.New("thin pool unavailable")

// PoolState is the thin pool as one lvs call saw it.
type PoolState struct {
	Active bool
	// Health is lv_health_status: "" when healthy, or failed,
	// metadata_read_only, out_of_data and the like.
	Health      string
	Size        uint64
	DataPct     float64
	MetadataPct float64
}

// Available is the I-644 rule: active, percentages readable, and not
// failed or read-only.
func (p PoolState) Available() bool {
	return p.Active && p.Health != "failed" && p.Health != "metadata_read_only"
}

// Reason names why a pool is unavailable, for logs and hostd status.
func (p PoolState) Reason() string {
	switch {
	case !p.Active:
		return "inactive"
	case p.Health != "":
		return p.Health
	}
	return ""
}

// Real drives the lvm2 tools through a shell.Runner.
type Real struct {
	VG   string // vg-guests
	Pool string // thin
	R    shell.Runner
	// Sandbox runs e2fsck and blkid, which parse metadata a guest may
	// have written, in shell.Sandboxed rather than as hostd (DECISIONS
	// I-465).
	Sandbox bool
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

// notFound is lvs's own sentence for a volume that is not in the VG. A
// missing VG says "Volume group ... not found" instead, which is an error:
// read as "no volume", it sent running guests to error and let destroy
// leak a volume (DECISIONS I-644).
func notFound(err error) bool {
	var ee *shell.ExitError
	return errors.As(err, &ee) && strings.Contains(string(ee.Result.Stderr), "Failed to find logical volume")
}

// VolumeExists implements LVM: false only when lvs says the volume is not
// found; any other lvs failure is an error.
func (l *Real) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := l.R.Run(ctx, "lvs", l.VG+"/"+name)
	if err == nil {
		return true, nil
	}
	if notFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("lvs %s: %w", name, err)
}

// VolumeActive implements LVM.
func (l *Real) VolumeActive(ctx context.Context, name string) (bool, error) {
	rows, err := l.lvs(ctx, l.VG+"/"+name, "lv_active")
	if err != nil {
		return false, err
	}
	return len(rows) > 0 && rows[0] == "active", nil
}

// PoolStatus implements LVM.
func (l *Real) PoolStatus(ctx context.Context) (PoolState, error) {
	rows, err := l.lvs(ctx, l.VG+"/"+l.Pool, "lv_active,lv_health_status,lv_size,data_percent,metadata_percent")
	if err != nil {
		return PoolState{}, fmt.Errorf("%w: lvs: %v", ErrPoolUnavailable, err)
	}
	if len(rows) == 0 {
		return PoolState{}, fmt.Errorf("%w: lvs: no row for pool %s", ErrPoolUnavailable, l.Pool)
	}
	f := strings.Split(rows[0], "|")
	if len(f) < 5 {
		return PoolState{}, fmt.Errorf("%w: lvs: unexpected row %q", ErrPoolUnavailable, rows[0])
	}
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	p := PoolState{Active: f[0] == "active", Health: f[1]}
	if p.Size, err = strconv.ParseUint(f[2], 10, 64); err != nil {
		return p, fmt.Errorf("lvs: size %q: %w", f[2], err)
	}
	if !p.Available() {
		return p, fmt.Errorf("%w: %s", ErrPoolUnavailable, p.Reason())
	}
	// An active pool always reports both percentages; empty ones are an
	// lvs that could not read the pool's status.
	if f[3] == "" || f[4] == "" {
		return p, fmt.Errorf("%w: no usage reported", ErrPoolUnavailable)
	}
	if p.DataPct, err = strconv.ParseFloat(f[3], 64); err != nil {
		return p, fmt.Errorf("lvs: data_percent %q: %w", f[3], err)
	}
	if p.MetadataPct, err = strconv.ParseFloat(f[4], 64); err != nil {
		return p, fmt.Errorf("lvs: metadata_percent %q: %w", f[4], err)
	}
	return p, nil
}

// guard refuses a mutation while the pool is unavailable.
func (l *Real) guard(ctx context.Context) error {
	_, err := l.PoolStatus(ctx)
	return err
}

// CreateVolume implements LVM: lvcreate -V <bytes>b -T vg/thin -n name.
func (l *Real) CreateVolume(ctx context.Context, name string, bytes uint64) error {
	if err := l.guard(ctx); err != nil {
		return err
	}
	ok, err := l.VolumeExists(ctx, name)
	if err != nil || ok {
		return err
	}
	_, err = l.R.Run(ctx, "lvcreate", "-V", fmt.Sprintf("%db", bytes), "-T", l.VG+"/"+l.Pool, "-n", name)
	return err
}

// HasFilesystem implements LVM via blkid, which probes the volume's first
// blocks: a guest's once it has run, so it is sandboxed like e2fsck.
func (l *Real) HasFilesystem(ctx context.Context, name string) (bool, error) {
	argv := []string{"blkid", "-s", "TYPE", "-o", "value", l.DevPath(name)}
	if l.Sandbox {
		argv = shell.Sandboxed(l.DevPath(name), false, argv...)
	}
	res, err := l.R.Run(ctx, argv...)
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

// MkfsShare implements LVM: no reserved blocks and 256 inodes, which bounds
// the file count as the volume size bounds the bytes (DECISIONS I-464).
func (l *Real) MkfsShare(ctx context.Context, name string) error {
	has, err := l.HasFilesystem(ctx, name)
	if err != nil || has {
		return err
	}
	_, err = l.R.Run(ctx, "mkfs.ext4", "-q", "-m", "0", "-N", "256", "-L", "claude-auth", l.DevPath(name))
	return err
}

// RemoveVolume implements LVM.
func (l *Real) RemoveVolume(ctx context.Context, name string) error {
	if err := l.guard(ctx); err != nil {
		return err
	}
	ok, err := l.VolumeExists(ctx, name)
	if err != nil || !ok {
		return err
	}
	_, err = l.R.Run(ctx, "lvremove", "-f", l.VG+"/"+name)
	return err
}

// ExtendVolume implements LVM.
func (l *Real) ExtendVolume(ctx context.Context, name string, bytes uint64) error {
	if err := l.guard(ctx); err != nil {
		return err
	}
	_, err := l.R.Run(ctx, "lvextend", "-L", fmt.Sprintf("%db", bytes), l.VG+"/"+name)
	return err
}

// Snapshot implements LVM.
func (l *Real) Snapshot(ctx context.Context, name, snap string) error {
	if err := l.guard(ctx); err != nil {
		return err
	}
	ok, err := l.VolumeExists(ctx, snap)
	if err != nil || ok {
		return err
	}
	_, err = l.R.Run(ctx, "lvcreate", "-s", "-n", snap, l.VG+"/"+name)
	return err
}

// Activate implements LVM: thin snapshots are created inactive and skipped.
func (l *Real) Activate(ctx context.Context, name string) error {
	if err := l.guard(ctx); err != nil {
		return err
	}
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

// PoolStats implements LVM. An inactive pool parsed as 0 percent used
// before I-644, which read as an empty pool; it is ErrPoolUnavailable now.
func (l *Real) PoolStats(ctx context.Context) (uint64, uint64, error) {
	p, err := l.PoolStatus(ctx)
	if err != nil {
		return 0, 0, err
	}
	used := uint64(float64(p.Size) * p.DataPct / 100)
	return p.Size, p.Size - used, nil
}

// PoolMetadataPercent implements LVM.
func (l *Real) PoolMetadataPercent(ctx context.Context) (float64, error) {
	p, err := l.PoolStatus(ctx)
	if err != nil {
		return 0, err
	}
	return p.MetadataPct, nil
}

// Fsck implements LVM.
func (l *Real) Fsck(ctx context.Context, name string) (int, error) {
	argv := []string{"e2fsck", "-fp", l.DevPath(name)}
	if l.Sandbox {
		argv = shell.Sandboxed(l.DevPath(name), true, argv...)
	}
	_, err := l.R.Run(ctx, argv...)
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
	// PoolMetaPct is the pool's metadata percent used.
	PoolMetaPct float64
	FailOn      map[string]error
	FsckExit    int
	Ops         []string
	// PoolDown makes the pool inactive (PoolHealth "" ) or unhealthy
	// (PoolHealth set): PoolStatus, PoolStats and every mutator return
	// ErrPoolUnavailable, as Real does (I-644).
	PoolDown   bool
	PoolHealth string
	// VGMissing makes every lvs call fail as a missing VG does: an error
	// that is not "not found".
	VGMissing bool
	// Inactive lists volumes whose device is not active; Activate clears
	// the entry.
	Inactive map[string]bool
}

// NewFake returns a Fake with a 1 TB pool.
func NewFake() *Fake {
	return &Fake{Volumes: map[string]*FakeVolume{}, PoolSize: 1 << 40, PoolFree: 1 << 40, FailOn: map[string]error{}}
}

func (f *Fake) fail(op string) error {
	f.Ops = append(f.Ops, op)
	return f.FailOn[op]
}

// guarded is fail for the five mutators Real guards (I-644).
func (f *Fake) guarded(op string) error {
	f.Ops = append(f.Ops, op)
	if err := f.poolErr(); err != nil {
		return err
	}
	return f.FailOn[op]
}

// poolErr is the guard's answer under f.mu.
func (f *Fake) poolErr() error {
	switch {
	case f.VGMissing:
		return fmt.Errorf("%w: lvs: Volume group \"vg-guests\" not found", ErrPoolUnavailable)
	case f.PoolDown && f.PoolHealth == "":
		return fmt.Errorf("%w: inactive", ErrPoolUnavailable)
	case f.PoolDown:
		return fmt.Errorf("%w: %s", ErrPoolUnavailable, f.PoolHealth)
	}
	return nil
}

// PoolStatus implements LVM.
func (f *Fake) PoolStatus(context.Context) (PoolState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := PoolState{Active: !f.PoolDown || f.PoolHealth != "", Health: f.PoolHealth, Size: f.PoolSize, MetadataPct: f.PoolMetaPct}
	if f.PoolSize > 0 {
		p.DataPct = float64(f.PoolSize-f.PoolFree) / float64(f.PoolSize) * 100
	}
	if err := f.poolErr(); err != nil {
		return p, err
	}
	return p, nil
}

// VolumeActive implements LVM.
func (f *Fake) VolumeActive(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.VGMissing {
		return false, f.poolErr()
	}
	_, ok := f.Volumes[name]
	return ok && !f.Inactive[name] && !f.PoolDown, nil
}

// DevPath implements LVM.
func (f *Fake) DevPath(name string) string { return "/dev/vg-guests/" + name }

// VolumeExists implements LVM.
func (f *Fake) VolumeExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.VGMissing {
		return false, f.poolErr()
	}
	_, ok := f.Volumes[name]
	return ok, nil
}

// CreateVolume implements LVM.
func (f *Fake) CreateVolume(_ context.Context, name string, bytes uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.guarded("create"); err != nil {
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

// MkfsShare implements LVM.
func (f *Fake) MkfsShare(ctx context.Context, name string) error { return f.Mkfs(ctx, name) }

// RemoveVolume implements LVM.
func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.guarded("remove"); err != nil {
		return err
	}
	delete(f.Volumes, name)
	return nil
}

// ExtendVolume implements LVM.
func (f *Fake) ExtendVolume(_ context.Context, name string, bytes uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.guarded("extend"); err != nil {
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
	if err := f.guarded("snapshot"); err != nil {
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
func (f *Fake) Activate(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.guarded("activate"); err != nil {
		return err
	}
	delete(f.Inactive, name)
	return nil
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
	if err := f.poolErr(); err != nil {
		return 0, 0, err
	}
	return f.PoolSize, f.PoolFree, nil
}

// PoolMetadataPercent implements LVM.
func (f *Fake) PoolMetadataPercent(context.Context) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.poolErr(); err != nil {
		return 0, err
	}
	return f.PoolMetaPct, nil
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
	if f.VGMissing {
		return nil, f.poolErr()
	}
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
