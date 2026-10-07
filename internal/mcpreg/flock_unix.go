//go:build unix

package mcpreg

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// flockFile takes an exclusive flock on path, waiting up to wait. The lock
// goes with the process, so a crashed sync never leaves it held.
func flockFile(path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // closing releases it anyway
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
