package park

import (
	"os"

	"golang.org/x/sys/unix"
)

// openDirect opens a volume for reads past the host page cache.
func openDirect(dev string) (*os.File, error) {
	return os.OpenFile(dev, os.O_RDONLY|unix.O_DIRECT, 0)
}
