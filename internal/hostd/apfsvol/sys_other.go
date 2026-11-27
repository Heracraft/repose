//go:build !darwin

package apfsvol

import (
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// cloneFile copies src to dst. Only darwin has clonefile; this is for the
// tests that run on Linux, and keeps a sparse source sparse.
func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	zero := make([]byte, len(buf))
	var off int64
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if string(buf[:n]) != string(zero[:n]) {
				if _, err := out.WriteAt(buf[:n], off); err != nil {
					_ = out.Close()
					return err
				}
			}
			off += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = out.Close()
			return rerr
		}
	}
	if err := out.Truncate(fi.Size()); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
