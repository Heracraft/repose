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
	"github.com/heracraft/repose/internal/hostd/guest"
)

// hostdControlSock is where hostd on a Mac without the appliance listens
// (design 2, DECISIONS I-1666); a var so a test can point it elsewhere.
var hostdControlSock = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".repose", "hostd.sock")
}

// macRestart restarts the Mac as the Apple menu does; a var for tests.
var macRestart = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", `tell application "System Events" to restart`).Run()
}

// HostRestartCmd is `repose host restart` (proposal item 7b): park every
// machine through hostd, then restart the Mac. hostd restores the parked
// machines at the next login. A machine that could not be parked stops
// the restart (the parked ones are unparked) unless force.
func HostRestartCmd(ctx context.Context, e *Env, force bool) error {
	if runtime.GOOS != "darwin" {
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
	res, err := c.Park(pctx, guest.ParkOperator)
	if err != nil {
		return exitf(ExitGeneric, "The machines were not parked: %v. Nothing restarted.", err)
	}
	parked, lost := 0, []string{}
	for _, r := range res {
		switch {
		case r.Outcome == "parked":
			parked++
		case r.Outcome == "skipped" && r.Reason == "guest is not running":
		default:
			lost = append(lost, r.GuestID)
		}
	}
	if len(lost) > 0 && !force {
		if _, err := c.Unpark(pctx); err != nil {
			return exitf(ExitGeneric, "%d machines could not be parked (%s), and the parked ones did not run again: %v. Nothing restarted.", len(lost), strings.Join(lost, ", "), err)
		}
		return exitf(ExitGeneric, "%d machines could not be parked and would start fresh (%s). Nothing restarted; --force restarts anyway.", len(lost), strings.Join(lost, ", "))
	}
	_, _ = fmt.Fprintf(e.ErrOut, "Parked %d machines in %.1fs; restarting this Mac.\n", parked, time.Since(t0).Seconds())
	if err := macRestart(ctx); err != nil {
		return exitf(ExitGeneric, "The machines are parked, but the Mac did not restart: %v. Restart it from the Apple menu; they come back at your login.", err)
	}
	return nil
}
