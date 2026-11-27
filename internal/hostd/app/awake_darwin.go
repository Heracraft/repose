package app

import (
	"log/slog"
	"os"
	"os/exec"
	"strconv"
)

// keepAwake holds the Mac awake while any machine on it runs, resting
// ones included, and lets it sleep when none does: a Mac asleep answers
// no ssh, and nothing through the edge wakes it. D1's launcher held the
// same assertion for as long as its appliance ran (`caffeinate -s -i`,
// documented "While the machine runs, the Mac does not sleep"); design 2
// had none (d2-surface, 2026-11-27). The assertion is a caffeinate child
// that ends with hostd (-w), so a hostd restart or crash never leaves one
// behind.
type keepAwake struct {
	cmd   *exec.Cmd
	start func() (*exec.Cmd, error)
}

func newKeepAwake() *keepAwake {
	return &keepAwake{start: func() (*exec.Cmd, error) {
		c := exec.Command("/usr/bin/caffeinate", "-s", "-i", "-w", strconv.Itoa(os.Getpid()))
		return c, c.Start()
	}}
}

// set takes the assertion when any guest runs and drops it when none
// does; it changes nothing when the state is what it was.
func (k *keepAwake) set(running bool, log *slog.Logger) {
	switch {
	case running && k.cmd == nil:
		c, err := k.start()
		if err != nil {
			log.Warn("the Mac is not held awake while machines run", "event", "keep_awake", "err", err.Error())
			return
		}
		k.cmd = c
		if c.Process != nil {
			go func() { _ = c.Wait() }()
		}
		log.Info("machines running: the Mac is held awake", "event", "keep_awake")
	case !running && k.cmd != nil:
		if k.cmd.Process != nil {
			_ = k.cmd.Process.Kill()
		}
		k.cmd = nil
		log.Info("no machine running: the Mac may sleep", "event", "keep_awake")
	}
}
