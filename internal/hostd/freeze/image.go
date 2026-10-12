package freeze

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/mount"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// LVName is the thin volume a guest's frozen image lives on (I-653).
func LVName(guestID string) string { return "frz-" + guestID }

// MapperName is the dm-crypt mapping of that volume.
func MapperName(guestID string) string { return "repose-frz-" + guestID }

// MapperPath is the mapping's device node.
func MapperPath(guestID string) string { return "/dev/mapper/" + MapperName(guestID) }

// Image filesystems. The image is btrfs with zstd compression forced on
// (I-685): the guest's memory is what a freeze writes and a restore reads,
// and compressing it is the one change that makes both faster, because
// both are bounded by the host's data disk (600 MB/s on host-01). Measured
// on woker-1, one large guest: 3.99 GB of image bytes on disk became 0.33
// GB under a synthetic load, 5.03 GB became 2.89 GB with 3 GB of
// /dev/urandom resident (the worst case), and the snapshot was never
// slower. Real agent memory on that box compresses 5.3x at zstd -1.
//
// FSExt4 is what images made before I-685 are; a record without a
// filesystem names it, so an image written by the previous release still
// restores (one release, as docs/interfaces promises).
const (
	FSBtrfs = "btrfs"
	FSExt4  = "ext4"
	// CompressOpt forces compression of every extent. Without `-force`
	// btrfs tries the first block of a file and gives up on the whole
	// file when that block does not shrink, which is exactly what the
	// memory file's first pages look like. Level 1 is the fastest; it
	// decompresses at about 960 MB/s per stream on one woker-1 core,
	// above the data disk's 600 MB/s, so the restore stays disk-bound
	// rather than becoming CPU-bound.
	CompressOpt = "compress-force=zstd:1"
)

// Image makes, opens, closes and removes a guest's image volume: a thin
// volume in the guest pool, dm-crypt in plain mode (aes-xts-plain64, a
// 512-bit key that exists only in hostd's memory and wrapped in the
// record), btrfs with zstd compression, mounted where the guest's
// hypervisor can write. Plain mode keeps no header on the disk, so the
// volume holds nothing but ciphertext; a wrong key is caught by the
// record's key check before any mount. Compression happens above
// dm-crypt, so what reaches the disk is still ciphertext only. Removing
// the volume discards its blocks back to the pool.
type Image struct {
	R     shell.Runner
	LVM   lvm.LVM
	Mount mount.Mounter
}

// FS is the filesystem Create makes, recorded with the image so a restore
// mounts what is there.
func (i *Image) FS() string { return FSBtrfs }

func (i *Image) cryptOpen(ctx context.Context, guestID string, key []byte) error {
	// A mapping left by a hostd that died mid-freeze is closed first; its
	// key may not be this one.
	_ = i.cryptClose(ctx, guestID) // absent is the usual case
	p, err := i.R.Start(ctx, bytes.NewReader(key), "cryptsetup", "open", "--type", "plain",
		"--cipher", "aes-xts-plain64", "--key-size", "512", "--key-file", "-", "--keyfile-size", fmt.Sprint(KeyBytes),
		i.LVM.DevPath(LVName(guestID)), MapperName(guestID))
	if err != nil {
		return err
	}
	if err := p.Wait(); err != nil {
		return fmt.Errorf("cryptsetup open: %w", err)
	}
	return nil
}

func (i *Image) cryptClose(ctx context.Context, guestID string) error {
	if _, err := os.Stat(MapperPath(guestID)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_, err := i.R.Run(ctx, "cryptsetup", "close", MapperName(guestID))
	return err
}

// Create makes a fresh image volume of size bytes for guestID under key and
// mounts it on dir, owned by uid:gid (the hypervisor's user) with mode 0700.
func (i *Image) Create(ctx context.Context, guestID string, size uint64, key []byte, dir string, uid, gid int) error {
	if err := i.Remove(ctx, guestID, dir); err != nil {
		return fmt.Errorf("stale image: %w", err)
	}
	if err := i.LVM.CreateVolume(ctx, LVName(guestID), size); err != nil {
		return err
	}
	if err := i.cryptOpen(ctx, guestID, key); err != nil {
		return err
	}
	// nodiscard: the thin volume is new and reads as zeros already.
	// `-m single -d single`: one copy of the metadata, not btrfs's default
	// DUP, so a nearly incompressible image still fits the volume.
	if _, err := i.R.Run(ctx, "mkfs.btrfs", "-q", "-f", "--nodiscard", "-m", "single", "-d", "single",
		"-L", "repose-frz", MapperPath(guestID)); err != nil {
		return fmt.Errorf("mkfs: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := i.Mount.MountType(ctx, MapperPath(guestID), dir, FSBtrfs, "noatime,"+CompressOpt); err != nil {
		return err
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// Open maps and mounts an existing image volume for a restore. fs is the
// filesystem the record says it holds; "" is ext4, what images written
// before I-685 are.
func (i *Image) Open(ctx context.Context, guestID string, key []byte, dir, fs string) error {
	if ok, err := i.LVM.VolumeExists(ctx, LVName(guestID)); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("image volume %s is missing", LVName(guestID))
	}
	if err := i.LVM.Activate(ctx, LVName(guestID)); err != nil {
		return err
	}
	if err := i.cryptOpen(ctx, guestID, key); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if fs == "" {
		fs = FSExt4
	}
	// Read-only would be enough for a restore, but the mount is also where
	// the hypervisor is snapshotted again when a freeze was cut short
	// (freeze.go, refreezePaused), so it is writable and compressed.
	opts := ""
	if fs == FSBtrfs {
		opts = "noatime," + CompressOpt
	}
	return i.Mount.MountType(ctx, MapperPath(guestID), dir, fs, opts)
}

// Close unmounts and unmaps the image, leaving the volume. After Close the
// key is gone from the kernel.
func (i *Image) Close(ctx context.Context, guestID, dir string) error {
	if err := i.Mount.Unmount(ctx, dir); err != nil {
		return err
	}
	return i.cryptClose(ctx, guestID)
}

// Remove closes the image and removes its volume. Absent pieces are fine.
func (i *Image) Remove(ctx context.Context, guestID, dir string) error {
	if err := i.Close(ctx, guestID, dir); err != nil {
		return err
	}
	return i.LVM.RemoveVolume(ctx, LVName(guestID))
}

// KeyCheck is what the record keeps to tell a wrong key from the right
// one without mounting what a wrong key decrypts to: a hash of the key
// under a fixed label. The key is 512 random bits, so the hash says
// nothing about it.
func KeyCheck(key []byte) string {
	h := sha256.New()
	h.Write([]byte("repose-frz-key-check\x00"))
	h.Write(key)
	return hex.EncodeToString(h.Sum(nil))
}

// Fingerprint hashes the first 4 KiB of a guest volume, read past the
// host page cache: ext4's superblock, whose mount count, mount and write
// times and state change with any mount or fsck. Recorded when a guest
// freezes and compared at the restore, it catches the one thing that makes
// a frozen guest's memory a lie: its disk written by anything since.
func Fingerprint(dev string) (string, error) {
	f, err := os.OpenFile(dev, os.O_RDONLY|unix.O_DIRECT, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only
	const n = 4096
	buf := alignedBlock(n)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return "", err
	}
	s := sha256.Sum256(buf)
	return hex.EncodeToString(s[:]), nil
}

// alignedBlock returns n bytes aligned to 4096, as O_DIRECT reads need.
func alignedBlock(n int) []byte {
	raw := make([]byte, n+4096)
	off := 4096 - int(uintptr(unsafe.Pointer(&raw[0]))&4095)
	if off == 4096 {
		off = 0
	}
	return raw[off : off+n]
}
