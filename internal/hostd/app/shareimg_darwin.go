package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// The login share's bound on a Mac (DECISIONS I-1666): Linux gives each
// user's share a 16 MiB volume of its own (I-464) so a guest that writes
// there freely cannot fill the host; here it is a sparse APFS disk image of
// the same size beside the share, attached at the share's path. APFS has no
// inode count to bound, so the size alone bounds it.

// shareImage is the share's disk image in the user's directory.
func shareImage(userDir string) string { return filepath.Join(userDir, "claude-auth.sparseimage") }

// mountedAt reports whether a filesystem is mounted exactly at path.
func mountedAt(path string) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return unix.ByteSliceToString(st.Mntonname[:]) == filepath.Clean(path), nil
}

// boundShare makes the image if it is missing and attaches it at share
// unless something is mounted there already. Ownership is honoured on the
// image (-owners on), so the share's root can be the user's alone.
func boundShare(r shell.Runner) func(ctx context.Context, userDir, share string, bytes uint64) error {
	return func(ctx context.Context, userDir, share string, bytes uint64) error {
		if ok, err := mountedAt(share); err != nil || ok {
			return err
		}
		img := shareImage(userDir)
		if _, err := os.Stat(img); errors.Is(err, os.ErrNotExist) {
			if _, err := r.Run(ctx, "/usr/bin/hdiutil", "create", "-quiet", "-type", "SPARSE", "-fs", "APFS",
				"-volname", "claude-auth", "-size", sizeArg(bytes), img); err != nil {
				return err
			}
		}
		if _, err := r.Run(ctx, "/usr/bin/hdiutil", "attach", "-quiet", "-nobrowse", "-noautoopen", "-owners", "on",
			"-mountpoint", share, img); err != nil {
			return err
		}
		// No Spotlight on a user's login directory: mds opened it as soon
		// as it was mounted (lab Mac, 19:30Z). Best effort; the share works
		// without it.
		_, _ = r.Run(ctx, "/usr/bin/mdutil", "-i", "off", share)
		return nil
	}
}

func sizeArg(bytes uint64) string {
	return strconv.FormatUint(bytes/(1<<20), 10) + "m"
}

// macMount is the Mounter of a Mac host: it can tell a mount and detach
// the login share's image when the share is swept; it mounts nothing else.
type macMount struct{ r shell.Runner }

func (macMount) IsMounted(path string) (bool, error) { return mountedAt(path) }

func (macMount) Mount(context.Context, string, string) error {
	return errors.New("mount: not on macOS")
}

func (macMount) MountType(context.Context, string, string, string, string) error {
	return errors.New("mount: not on macOS")
}

func (m macMount) Unmount(ctx context.Context, path string) error {
	ok, err := mountedAt(path)
	if err != nil || !ok {
		return err
	}
	_, err = m.r.Run(ctx, "/usr/bin/hdiutil", "detach", "-quiet", path)
	return err
}
