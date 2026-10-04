package guest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ensureDir makes path a real directory owned by uid:gid (-1 keeps that
// id) with mode, without following a symlink there. The guest directory
// is writable by the hypervisor's user, so whatever sits at a name hostd
// creates in it may have been planted: an entry that is not a directory
// is removed (os.Remove takes the link, never its target), a directory
// owned by anyone but hostd or the intended owner is removed with its
// contents, and the chown and chmod go through a descriptor opened
// O_NOFOLLOW, so a link swapped in after the check fails the open instead
// of redirecting a root chown.
func ensureDir(path string, mode os.FileMode, uid, gid int) error {
	for attempt := 0; ; attempt++ {
		fi, err := os.Lstat(path)
		switch {
		case err == nil && !fi.IsDir():
			if err := os.Remove(path); err != nil {
				return err
			}
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return err
		}
		if err := os.Mkdir(path, mode.Perm()); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open %s: %w", filepath.Base(path), err)
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			_ = unix.Close(fd)
			return err
		}
		if owner := int(st.Uid); owner != os.Geteuid() && owner != uid {
			_ = unix.Close(fd)
			if attempt > 0 {
				return fmt.Errorf("%s is owned by uid %d", filepath.Base(path), owner)
			}
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			continue
		}
		err = fixDir(fd, mode, uid, gid)
		if cerr := unix.Close(fd); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		return nil
	}
}

func fixDir(fd int, mode os.FileMode, uid, gid int) error {
	if uid >= 0 || gid >= 0 {
		if err := unix.Fchown(fd, uid, gid); err != nil {
			return err
		}
	}
	m := uint32(mode.Perm())
	if mode&os.ModeSticky != 0 {
		m |= unix.S_ISVTX
	}
	return unix.Fchmod(fd, m)
}
