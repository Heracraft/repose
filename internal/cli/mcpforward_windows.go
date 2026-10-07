package cli

import "os/exec"

func setProcGroup(cmd *exec.Cmd) {}

// signalGroup ends the server; Windows has no TERM, so both are a kill.
func signalGroup(cmd *exec.Cmd, kill bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
