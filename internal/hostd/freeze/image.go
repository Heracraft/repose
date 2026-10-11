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

// Image makes, opens, closes and removes a guest's image volume: a thin
// volume in the guest pool, dm-crypt in plain mode (aes-xts-plain64, a
// 512-bit key that exists only in hostd's memory and wrapped in the
// record), ext4, mounted where the guest's hypervisor can write. Plain
// mode keeps no header on the disk, so the volume holds nothing but
// ciphertext; a wrong key is caught by the record's key check before any
// mount. Removing the volume discards its blocks back to the pool.
type Image struct {
	R     shell.Runner
	LVM   lvm.LVM
	Mount mount.Mounter
}

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
	// nodiscard: the thin volume is new and reads as zeros already; lazy
	// init keeps mkfs to a few MiB of writes.
	if _, err := i.R.Run(ctx, "mkfs.ext4", "-q", "-F", "-m", "0", "-L", "repose-frz",
		"-E", "nodiscard,lazy_itable_init=1,lazy_journal_init=1", MapperPath(guestID)); err != nil {
		return fmt.Errorf("mkfs: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := i.Mount.Mount(ctx, MapperPath(guestID), dir); err != nil {
		return err
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// Open maps and mounts an existing image volume for a restore.
func (i *Image) Open(ctx context.Context, guestID string, key []byte, dir string) error {
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
	return i.Mount.Mount(ctx, MapperPath(guestID), dir)
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
