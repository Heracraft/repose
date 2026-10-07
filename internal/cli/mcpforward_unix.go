//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// setProcGroup starts a forwarded server in a process group of its own, so
// stopping it reaches what it started (npx starts node).
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup sends TERM, or KILL, to the server's process group.
func signalGroup(cmd *exec.Cmd, kill bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
