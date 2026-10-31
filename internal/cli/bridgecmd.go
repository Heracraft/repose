package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// bridgeUpLine is the line that says the bridge is on.
func bridgeUpLine(c laptopChrome, slug string, allow bridgeAllow) string {
	s := fmt.Sprintf("%s → %s: the agents there browse in your Chrome (Ctrl-C ends it).", c.Name(), slug)
	if c.Switch {
		s += "\nChrome asks you to allow each new connection."
	}
	if allow != nil {
		s += "\nOnly " + strings.Join(allow, ", ") + ": other sites fail in the agents' tabs."
	}
	return s + "\nPages the agents open are listed below (host and path only)."
}

// bridgeTmuxLine is shown in the project's tmux session, for whoever is
// attached.
func bridgeTmuxLine(on bool) string {
	if on {
		return "Your laptop's Chrome is bridged in: the browser tools on this machine drive it now."
	}
	return "The bridge to your laptop's Chrome closed; the browser tools are back on this machine's browser."
}

// bridgeWardenHint is said before the warden connects through Chrome's
// switch, where Chrome asks first.
const bridgeWardenHint = "Chrome asks you to allow the bridge's own connection first."

// errWardenLost is the warden's connection ending mid-bridge.
var errWardenLost = errors.New("the bridge's own connection to Chrome closed")

// errBridgeEndedByGuest is the hold ending with the tunnel still wanted.
var errBridgeEndedByGuest = errors.New("the bridge closed from the machine's side")

// runBridge is the bridge's life, shared by `repose browser bridge` and
// `--bridge`: the warden when there is an allowlist, the front, the
// tunnel. up is called once the machine's endpoint points at the laptop.
// It returns when ctx ends (nil), the warden's connection ends
// (errWardenLost), or the tunnel does.
func runBridge(ctx context.Context, t sshTarget, chrome laptopChrome, policy *bridgePolicy, say func(string), onAttach, up func()) error {
	var warden *cdpWarden
	if policy.allowlisted() {
		if chrome.Switch {
			say(bridgeWardenHint)
		}
		wctx, cancel := context.WithTimeout(ctx, bridgeToggleWait)
		w, err := startWarden(wctx, chrome, policy)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return exitf(ExitGeneric, "Could not connect to Chrome to hold the agents to --allow (%s). Allow the connection in Chrome and run it again.", oneLine(err.Error()))
		}
		warden = w
		defer warden.Close()
	}
	front, err := startCDPFront(chrome, policy, onAttach)
	if err != nil {
		return stepFailed("listen on this laptop for the machine", err, "")
	}
	defer front.Close()
	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lost := make(chan struct{})
	if warden != nil {
		go func() {
			select {
			case <-warden.Done():
				close(lost)
				cancel()
			case <-hctx.Done():
			}
		}()
	}
	bridgeRelease(hctx, t)
	err = holdBridge(hctx, t, front.Port(), up)
	select {
	case <-lost:
		return errWardenLost
	default:
	}
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		// The guest ended the hold on its own: the tunnel listener vanished.
		return errBridgeEndedByGuest
	}
	return err
}

// BrowserBridgeCmd implements `repose browser bridge [PROJECT]`.
func BrowserBridgeCmd(ctx context.Context, e *Env, projectArg string, opts BridgeOptions) error {
	allow, err := parseBridgeAllow(opts.Allow)
	if err != nil {
		return exitf(ExitUsage, "--allow %s.", err.Error())
	}
	// A running machine only, like code, exec and ssh (connectRunning):
	// the bridge is for an agent that is working, and starting a machine
	// is `repose start`'s job (I-312).
	project, target, err := connectRunning(ctx, e, projectArg)
	if err != nil {
		return err
	}
	say := func(m string) { _, _ = fmt.Fprintln(e.ErrOut, m) }
	chrome, err := findLaptopChrome(ctx, e, opts, say)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	var outMu sync.Mutex
	out := func(s string) {
		outMu.Lock()
		defer outMu.Unlock()
		_, _ = fmt.Fprintln(e.Out, s)
	}
	policy := newBridgePolicy(allow, func(n bridgeNav) { out(n.String()) })
	var wasUp atomic.Bool
	err = runBridge(ctx, target, chrome, policy, say,
		func() { out(fmt.Sprintf("An agent on %s is in your Chrome.", project.Slug)) },
		func() {
			wasUp.Store(true)
			out(bridgeUpLine(chrome, project.Slug, allow))
			go func() {
				mctx, cancel := context.WithTimeout(ctx, bridgeStopWait)
				defer cancel()
				_ = runSSHOK(mctx, target, tmuxIfAttached(project.Slug, bridgeTmuxLine(true)))
			}()
		})
	if ctx.Err() != nil {
		// Ctrl-C is how a bridge ends: not an interruption.
		if wasUp.Load() {
			out(fmt.Sprintf("\nBridge closed. The agents on %s are back on the machine's browser.", project.Slug))
		}
		bridgeStop(target, project.Slug, bridgeTmuxLine(false))
		return nil
	}
	var xe *exitError
	if errors.As(err, &xe) {
		return err
	}
	bridgeStop(target, project.Slug, bridgeTmuxLine(false))
	switch {
	case errors.Is(err, errWardenLost):
		return exitf(ExitGeneric, "Chrome's connection to the bridge closed (Chrome quit, or remote debugging was turned off). The agents on %s are back on the machine's browser.", project.Slug)
	case errors.Is(err, errBridgeHeld):
		return exitf(ExitGeneric, "Another bridge to %s is still open (a laptop that went to sleep keeps its bridge for up to two minutes). Try again in a moment.", project.Slug)
	case errors.Is(err, errBridgeEndedByGuest):
		return exitf(ExitGeneric, "The bridge to %s closed from the machine's side. The agents there are back on the machine's browser; run repose browser bridge again to reopen it.", project.Slug)
	}
	return stepFailed("keep the bridge to "+project.Slug+" open", err, "")
}

// runSessionBridge is --bridge on `run` and `attach`: the same bridge,
// beside the attach, for as long as it lasts, reporting through tmux.
// There is no terminal of its own, so the navigation log is only the
// blocked lines, as tmux messages.
func runSessionBridge(ctx context.Context, t sshTarget, slug string, allowVals []string, say func(string), alive func() bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for alive() && ctx.Err() == nil {
			if sleepOrDone(ctx, time.Second) != nil {
				return
			}
		}
		cancel()
	}()
	allow, err := parseBridgeAllow(allowVals)
	if err != nil {
		say("repose could not bridge your Chrome: " + err.Error())
		return
	}
	home, _ := os.UserHomeDir()
	chrome, err := findLaptopChrome(ctx, &Env{HomeDir: home}, BridgeOptions{}, say)
	if err != nil {
		if ctx.Err() == nil {
			say("repose could not bridge your Chrome: " + oneLine(err.Error()))
		}
		return
	}
	policy := newBridgePolicy(allow, func(n bridgeNav) {
		if n.Blocked {
			say("The bridge blocked " + n.Place + ": it isn't on --bridge-allow.")
		}
	})
	err = runBridge(ctx, t, chrome, policy, say, nil, func() { say(bridgeTmuxLine(true)) })
	bridgeStop(t, slug, "")
	switch {
	case ctx.Err() != nil:
	case errors.Is(err, errWardenLost):
		say("The bridge's own connection to your Chrome closed, so the bridge closed: --bridge-allow can't hold without it.")
	case errors.Is(err, errBridgeHeld):
		say("repose could not bridge your Chrome: another bridge to " + slug + " is still open.")
	case errors.Is(err, errBridgeEndedByGuest):
		say(bridgeTmuxLine(false))
	case err != nil:
		say("repose could not keep the bridge to your Chrome open: " + oneLine(err.Error()))
	}
}
