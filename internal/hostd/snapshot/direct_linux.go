package snapshot

import (
	"os"
	"syscall"
)

// openDirect opens dev for writing around the page cache, or returns nil
// where the filesystem refuses O_DIRECT (tmpfs, in tests).
func openDirect(dev string) *os.File {
	f, err := os.OpenFile(dev, os.O_WRONLY|syscall.O_DIRECT, 0)
	if err != nil {
		return nil
	}
	return f
}

// openDirectRead opens dev for reading around the page cache, or returns
// nil where O_DIRECT is refused. A snapshot read through it does not fill
// the host's page cache with a guest's disk (DECISIONS I-571).
func openDirectRead(dev string) *os.File {
	f, err := os.OpenFile(dev, os.O_RDONLY|syscall.O_DIRECT, 0)
	if err != nil {
		return nil
	}
	return f
}
