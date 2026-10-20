//go:build !windows

package machine

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on path (created if missing)
// and returns the function that releases it. internal/cli/filelock_unix.go
// is the same lock on the same kind of file.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
