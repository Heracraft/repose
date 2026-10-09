//go:build unix

package cli

import "syscall"

// closeInheritedFDs marks every descriptor past stderr close-on-exec, so
// the processes tests start (a fake guest's tmux server above all, which
// outlives a timed-out test binary) do not hold what the test run
// inherited: `flock LOCK go test` passes the lock's descriptor down, and
// a leaked tmux kept every later flock waiting (I-633).
func closeInheritedFDs() {
	for fd := 3; fd < 1024; fd++ {
		syscall.CloseOnExec(fd)
	}
}
