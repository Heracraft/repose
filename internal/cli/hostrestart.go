package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/hostd/control"
)

// hostdControlSock is where hostd on a Mac without the appliance listens
// (design 2, DECISIONS I-1666); a var so a test can point it elsewhere.
var hostdControlSock = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".repose", "hostd.sock")
}

// onMac is whether this binary runs on macOS; a var for tests.
var onMac = func() bool { return runtime.GOOS == "darwin" }

// macRestart restarts the Mac as the Apple menu does; a var for tests.
var macRestart = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", `tell application "System Events" to restart`).Run()
}

// HostRestartCmd is `repose host restart` (proposal item 7b): park every
// machine through hostd, then restart the Mac. hostd restores the parked
// machines at the next login. A machine that could not be parked stops
// the restart (the parked ones are unparked) unless force.
func HostRestartCmd(ctx context.Context, e *Env, force bool) error {
	if !onMac() {
		return exitf(ExitUsage, "repose host restart runs on the Mac it restarts.")
	}
	sock := hostdControlSock()
	if _, err := os.Stat(sock); err != nil {
		return exitf(ExitGeneric, "No repose host that can park its machines runs on this Mac (%s is missing).", sock)
	}
	c := control.NewClient(sock)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	t0 := time.Now()
	res, err := c.Park(pctx, control.ParkOperatorReason)
	if err != nil {
		return exitf(ExitGeneric, "The machines were not parked: %v. Nothing restarted.", err)
	}
	parked, lost := 0, []string{}
	for _, r := range res {
		switch {
		case r.Outcome == "parked", r.Outcome == "skipped" && r.Reason == control.ParkSkippedParked:
			parked++
		case r.Outcome == "skipped" && r.Reason == control.ParkSkippedStopped:
		default:
			lost = append(lost, r.GuestID)
		}
	}
	if len(lost) > 0 && !force {
		back, err := c.Unpark(pctx)
		if err != nil {
			return exitf(ExitGeneric, "%d machines could not be parked (%s), and the parked ones did not run again: %v. Nothing restarted.", len(lost), strings.Join(lost, ", "), err)
		}
		restored := 0
		for _, r := range back {
			if r.OK {
				restored++
			}
		}
		if restored < parked {
			// A Mac restores a parked machine only in an unlocked session.
			return exitf(ExitGeneric, "%d machines could not be parked and would start fresh (%s). Nothing restarted; the parked machines run again once this Mac's screen is unlocked. --force restarts anyway.", len(lost), strings.Join(lost, ", "))
		}
		return exitf(ExitGeneric, "%d machines could not be parked and would start fresh (%s). Nothing restarted; --force restarts anyway.", len(lost), strings.Join(lost, ", "))
	}
	_, _ = fmt.Fprintf(e.ErrOut, "Parked %d machines in %.1fs; restarting this Mac.\n", parked, time.Since(t0).Seconds())
	if err := macRestart(ctx); err != nil {
		return exitf(ExitGeneric, "The machines are parked, but the Mac did not restart: %v. Restart it from the Apple menu; they come back at your login.", err)
	}
	return nil
}
