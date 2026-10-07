package sample

import "golang.org/x/sys/unix"

// rootFS is the root filesystem's used and size in bytes from statfs:
// blocks less those available to dev, so root's reserve counts as used, as
// the disk_high warning counts it (DECISIONS I-567). This is what the
// guest's writes run out of; the host's figure for the volume counts a
// deleted file's blocks until the weekly fstrim. Both 0 when statfs fails.
func rootFS(path string) (used, size uint64) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil || st.Blocks == 0 || st.Bavail > st.Blocks || st.Bsize <= 0 {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	return (st.Blocks - st.Bavail) * bs, st.Blocks * bs
}
