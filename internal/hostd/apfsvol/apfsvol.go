// Package apfsvol is the lvm.LVM of a Mac host (design 2, DECISIONS
// I-1664): each guest volume is a sparse file on the APFS volume that holds
// Dir, and a snapshot or a clone is an APFS clone of that file
// (clonefile(2)), which takes no time and no space until either side is
// written (measured: 26 ms for a running machine's 8 GiB volume with 2.2 GB
// written, docs/proposals/2026-11-26-design2.md section 2.6).
//
// There is no thin pool: the pool is the APFS volume, its size and free
// space statfs's. There is no device to activate. The ext4 a guest boots is
// made and checked by e2fsprogs' mke2fs and e2fsck (Mke2fs, E2fsck), which
// macOS does not ship.
package apfsvol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// Suffix is a volume file's extension.
const Suffix = ".img"

// Volumes implements lvm.LVM on files in Dir.
type Volumes struct {
	Dir    string
	R      shell.Runner
	Mke2fs string // path to mke2fs
	E2fsck string // path to e2fsck
	// Chown gives a new volume file to the user its VM runs as, when hostd
	// is root (DECISIONS I-1664); nil leaves it hostd's.
	Chown func(path string) error
	// Statfs returns the size and free bytes of the filesystem holding a
	// path; nil uses the platform's statfs.
	Statfs func(path string) (size, free uint64, err error)
}

var _ lvm.LVM = (*Volumes)(nil)

// DevPath implements lvm.LVM: the volume's file.
func (v *Volumes) DevPath(name string) string { return filepath.Join(v.Dir, name+Suffix) }

func validName(name string) error {
	if name == "" || strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
		return fmt.Errorf("apfsvol: bad volume name %q", name)
	}
	return nil
}

// VolumeExists implements lvm.LVM.
func (v *Volumes) VolumeExists(_ context.Context, name string) (bool, error) {
	if err := validName(name); err != nil {
		return false, err
	}
	_, err := os.Lstat(v.DevPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// CreateVolume implements lvm.LVM: a sparse file of bytes; one that exists
// is left as it is.
func (v *Volumes) CreateVolume(ctx context.Context, name string, bytes uint64) error {
	if ok, err := v.VolumeExists(ctx, name); err != nil || ok {
		return err
	}
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return err
	}
	tmp := v.DevPath(name) + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(bytes)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := v.chown(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, v.DevPath(name))
}

// HasFilesystem implements lvm.LVM: the ext4 superblock's magic (0xEF53 at
// byte 1080).
func (v *Volumes) HasFilesystem(_ context.Context, name string) (bool, error) {
	f, err := os.Open(v.DevPath(name))
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, 2)
	if _, err := f.ReadAt(b, 1080); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return b[0] == 0x53 && b[1] == 0xEF, nil
}

func (v *Volumes) mkfs(ctx context.Context, name string, args ...string) error {
	has, err := v.HasFilesystem(ctx, name)
	if err != nil || has {
		return err
	}
	if v.Mke2fs == "" {
		return errors.New("apfsvol: no mke2fs on this host")
	}
	argv := append([]string{v.Mke2fs, "-t", "ext4", "-F"}, args...)
	_, err = v.R.Run(ctx, append(argv, v.DevPath(name))...)
	return err
}

// Mkfs implements lvm.LVM.
func (v *Volumes) Mkfs(ctx context.Context, name string) error {
	return v.mkfs(ctx, name, "-q", "-E", "lazy_itable_init=1")
}

// MkfsRW implements lvm.LVM: the erofs guest's vdb, labelled as on Linux.
func (v *Volumes) MkfsRW(ctx context.Context, name string) error {
	return v.mkfs(ctx, name, "-q", "-L", lvm.RWLabel, "-E", "lazy_itable_init=1")
}

// MkfsShare implements lvm.LVM.
func (v *Volumes) MkfsShare(ctx context.Context, name string) error {
	return v.mkfs(ctx, name, "-q", "-m", "0", "-N", "256", "-L", "claude-auth")
}

// RemoveVolume implements lvm.LVM; an absent volume is removed already.
func (v *Volumes) RemoveVolume(_ context.Context, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	err := os.Remove(v.DevPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ExtendVolume implements lvm.LVM: the file grows to bytes, never shrinks.
// A running VM sees the new size at its next start (VZ cannot resize an
// attached disk).
func (v *Volumes) ExtendVolume(_ context.Context, name string, bytes uint64) error {
	p := v.DevPath(name)
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if uint64(fi.Size()) >= bytes {
		return nil
	}
	return os.Truncate(p, int64(bytes))
}

// Snapshot implements lvm.LVM: an APFS clone named snap.
func (v *Volumes) Snapshot(ctx context.Context, name, snap string) error {
	return v.Clone(ctx, name, snap)
}

// Clone implements lvm.LVM; a name that exists is left as it is.
func (v *Volumes) Clone(ctx context.Context, origin, name string) error {
	if err := validName(origin); err != nil {
		return err
	}
	if ok, err := v.VolumeExists(ctx, name); err != nil || ok {
		return err
	}
	tmp := v.DevPath(name) + ".new"
	_ = os.Remove(tmp)
	if err := cloneFile(v.DevPath(origin), tmp); err != nil {
		return fmt.Errorf("clone %s: %w", origin, err)
	}
	if err := v.chown(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, v.DevPath(name))
}

// Activate implements lvm.LVM: a file needs no activation.
func (v *Volumes) Activate(ctx context.Context, name string) error {
	ok, err := v.VolumeExists(ctx, name)
	if err == nil && !ok {
		return fmt.Errorf("apfsvol: volume %s not found", name)
	}
	return err
}

// VolumeActive implements lvm.LVM: a volume that exists is usable.
func (v *Volumes) VolumeActive(ctx context.Context, name string) (bool, error) {
	return v.VolumeExists(ctx, name)
}

// VolumeStats implements lvm.LVM: the file's size and its allocated bytes
// (a clone's shared blocks count in each, as a thin snapshot's do).
func (v *Volumes) VolumeStats(_ context.Context, name string) (size, used uint64, err error) {
	fi, err := os.Stat(v.DevPath(name))
	if err != nil {
		return 0, 0, err
	}
	return uint64(fi.Size()), allocated(fi), nil
}

func (v *Volumes) statfs() (size, free uint64, err error) {
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return 0, 0, err
	}
	if v.Statfs != nil {
		return v.Statfs(v.Dir)
	}
	return statfs(v.Dir)
}

// PoolStats implements lvm.LVM: the APFS volume's size and free bytes.
func (v *Volumes) PoolStats(context.Context) (size, free uint64, err error) { return v.statfs() }

// PoolMetadataPercent implements lvm.LVM: APFS has no separate metadata
// space to run out of.
func (v *Volumes) PoolMetadataPercent(context.Context) (float64, error) { return 0, nil }

// PoolStatus implements lvm.LVM.
func (v *Volumes) PoolStatus(context.Context) (lvm.PoolState, error) {
	size, free, err := v.statfs()
	if err != nil {
		return lvm.PoolState{}, fmt.Errorf("%w: %v", lvm.ErrPoolUnavailable, err)
	}
	st := lvm.PoolState{Active: true, Size: size}
	if size > 0 {
		st.DataPct = 100 * float64(size-free) / float64(size)
	}
	if free == 0 {
		st.Health = "out_of_data"
	}
	return st, nil
}

// Fsck implements lvm.LVM: e2fsck -fp's exit code.
func (v *Volumes) Fsck(ctx context.Context, name string) (int, error) {
	if v.E2fsck == "" {
		return 0, errors.New("apfsvol: no e2fsck on this host")
	}
	_, err := v.R.Run(ctx, v.E2fsck, "-fp", v.DevPath(name))
	var ee *shell.ExitError
	if errors.As(err, &ee) {
		return ee.Result.ExitCode, nil
	}
	return 0, err
}

// ListVolumes implements lvm.LVM.
func (v *Volumes) ListVolumes(context.Context) ([]string, error) {
	ents, err := os.ReadDir(v.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), Suffix); ok && e.Type().IsRegular() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (v *Volumes) chown(p string) error {
	if v.Chown == nil {
		return nil
	}
	return v.Chown(p)
}
