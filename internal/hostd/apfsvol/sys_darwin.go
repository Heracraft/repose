//go:build darwin

package apfsvol

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// cloneFile is clonefile(2): an APFS clone sharing every block of src.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
}

func statfs(path string) (size, free uint64, err error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return 0, 0, err
	}
	return s.Blocks * uint64(s.Bsize), s.Bavail * uint64(s.Bsize), nil
}

func allocated(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Blocks) * 512
	}
	return uint64(fi.Size())
}
