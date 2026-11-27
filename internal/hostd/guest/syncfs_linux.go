package guest

import (
	"os"

	"golang.org/x/sys/unix"
)

// syncfs flushes the filesystem holding f.
func syncfs(f *os.File) error { return unix.Syncfs(int(f.Fd())) }
