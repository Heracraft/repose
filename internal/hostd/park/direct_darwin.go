package park

import (
	"os"

	"golang.org/x/sys/unix"
)

// openDirect opens a volume for reads past the host page cache: macOS has
// no O_DIRECT; F_NOCACHE turns the cache off for the descriptor.
func openDirect(dev string) (*os.File, error) {
	f, err := os.OpenFile(dev, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	if _, err := unix.FcntlInt(f.Fd(), unix.F_NOCACHE, 1); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
