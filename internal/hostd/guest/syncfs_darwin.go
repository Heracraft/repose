package guest

import "os"

// syncfs flushes f (a directory) with fsync: macOS has no syncfs.
func syncfs(f *os.File) error { return f.Sync() }
