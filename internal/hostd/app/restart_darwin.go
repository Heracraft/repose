package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// restartArgv is how hostd asks macOS to restart (DECISIONS I-1690), after
// every machine is parked. With the user logged in it is the Apple menu's
// restart, through System Events in the user's own session, so the user's
// apps are asked to quit and keep their documents as at any restart (and
// one with unsaved work can cancel it, which RestartWatch covers). A root
// hostd reaches that session with `launchctl asuser`. With nobody logged
// in there is no app to ask, and a root hostd restarts with shutdown.
func restartArgv(root, loggedIn bool, uid int, name string) ([][]string, error) {
	polite := []string{"/usr/bin/osascript", "-e", `tell application "System Events" to restart`}
	hard := []string{"/sbin/shutdown", "-r", "now"}
	switch {
	case loggedIn && root:
		return [][]string{append([]string{"/bin/launchctl", "asuser", strconv.Itoa(uid), "/usr/bin/sudo", "-u", name}, polite...), hard}, nil
	case loggedIn:
		return [][]string{polite}, nil
	case root:
		return [][]string{hard}, nil
	}
	return nil, errors.New("nobody is logged in to this Mac and hostd is not root, so it cannot restart it")
}

// politeTimeout bounds the System Events restart. Measured on the lab Mac
// (2026-11-27): asked through `launchctl asuser` from root, osascript
// waits on an Automation consent prompt and never answers, which would
// leave every machine parked behind a restart that is not coming. A var
// for tests.
var politeTimeout = 15 * time.Second

// restartMac runs restartArgv's commands in turn until one succeeds, each
// but the last within politeTimeout. The shutdown after a System Events
// restart that failed or did not answer is what the user asked for: every
// machine is parked already, so only the restart itself is left.
func restartMac(ctx context.Context, r shell.Runner, root, loggedIn bool, uid int, name string) error {
	tries, err := restartArgv(root, loggedIn, uid, name)
	if err != nil {
		return err
	}
	var last error
	for i, argv := range tries {
		rctx, cancel := ctx, context.CancelFunc(func() {})
		if i < len(tries)-1 {
			rctx, cancel = context.WithTimeout(ctx, politeTimeout)
		}
		_, err := r.Run(rctx, argv...)
		cancel()
		if err != nil {
			last = fmt.Errorf("%s: %w", argv[0], err)
			continue
		}
		return nil
	}
	return last
}
