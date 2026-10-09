package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/multiplexer"
)

// The session helper is what keeps working beside an attached tmux or
// herdr client once
// the CLI has become ssh, or is busy proxying its terminal (the input
// proxy, I-280). `run` and `attach` start it just before the attach,
// detached, with the same ssh target. It carries the
// laptop's config on `attach` (I-195, I-196, I-198), where doing it first
// would delay the first keystroke, and reports through tmux or herdr,
// never over the pane. It ends when the ssh it was started beside ends: the CLI's pid
// is ssh's after an exec, and the proxy exits when its ssh does, so the
// helper's parent changing is the signal either way.
//
// Nothing it does is allowed to delay or break the attach: it starts in a
// few milliseconds, never reads the terminal, and every failure is at
// most a tmux message.

// sessionEnv carries the helper's options, base64 JSON, so none of them
// (paths, the target) shows in a process listing.
const sessionEnv = "REPOSE_SESSION"

// sessionHelperCmd is the hidden command the helper runs as.
const sessionHelperCmd = "__session"

// sessionOptions is everything the helper needs; it talks to the guest
// only, never to the api.
type sessionOptions struct {
	Slug   string   `json:"slug"`
	Target []string `json:"target"`
	// Carry sends the carry (carry.go) with the options below.
	Carry   bool   `json:"carry"`
	TZ      string `json:"tz,omitempty"`
	RepoDir string `json:"repo_dir,omitempty"`
	HomeDir string `json:"home_dir,omitempty"`
	// Forward keeps the guest's listeners forwarded to the laptop for as
	// long as the attach lasts (I-199); off with REPOSE_NO_FORWARD=1.
	Forward bool `json:"forward"`
	// Bridge keeps the laptop's Chrome bridged to the guest for as long as
	// the attach lasts (`--bridge`, I-296).
	Bridge bool `json:"bridge,omitempty"`
	// BridgeAllow is `--bridge-allow`: the bridge's allowlist (I-311).
	BridgeAllow []string `json:"bridge_allow,omitempty"`
	// Checkout is the extra checkout's name (I-480), "" for the
	// machine's own: where the MCP carry files local-scope servers.
	Checkout string `json:"checkout,omitempty"`
	// MCPOff is logins.skip naming mcp: the carry sends an empty list
	// (I-556).
	MCPOff bool `json:"mcp_off,omitempty"`
	// MCP is [mcp] forward: the laptop's MCP servers forwarded for as
	// long as the attach lasts (I-557).
	MCP []string `json:"mcp,omitempty"`
	// Multiplexer is what the machine runs (I-509): messages go to tmux's
	// status line or herdr's notifications. "" is tmux.
	Multiplexer string `json:"multiplexer,omitempty"`
	// Messages are shown on the multiplexer first: a line the run printed
	// just before the attach covered it (DECISIONS I-618).
	Messages []string `json:"messages,omitempty"`
}

// windowsMCPForwardLine is [mcp] forward on Windows, where attach runs no
// helper to forward with.
const windowsMCPForwardLine = "[mcp] forward in config.toml does nothing on Windows; run repose mcp forward NAME in a terminal of its own."

// startSessionHelper starts the helper for the attach that follows, and
// never fails the attach: a helper that cannot start is simply absent.
// Windows has no multiplexing and no exec, and tests (TargetFor set) drive
// runSession themselves.
func startSessionHelper(e *Env, opts sessionOptions) {
	if goos() == "windows" && len(opts.MCP) > 0 {
		// Set and silently ignored would read as a broken forward.
		_, _ = fmt.Fprintln(e.ErrOut, windowsMCPForwardLine)
	}
	if e.TargetFor != nil || goos() == "windows" || (!opts.Carry && !opts.Forward && !opts.Bridge && len(opts.MCP) == 0 && len(opts.Messages) == 0) {
		return
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = spawnDetached(exe, []string{sessionHelperCmd}, sessionEnv+"="+base64.StdEncoding.EncodeToString(b))
}

func newSessionHelperCmd() *cobra.Command {
	return &cobra.Command{
		Use:    sessionHelperCmd,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := base64.StdEncoding.DecodeString(os.Getenv(sessionEnv))
			if err != nil {
				return err
			}
			var opts sessionOptions
			if err := json.Unmarshal(raw, &opts); err != nil {
				return err
			}
			ppid := os.Getppid()
			alive := func() bool { return os.Getppid() == ppid }
			return runSession(cmd.Context(), opts, alive)
		},
	}
}

// runSession is the helper's whole life: the carry, then (while alive
// says the attach is still there) whatever keeps running beside it.
func runSession(ctx context.Context, opts sessionOptions, alive func() bool) error {
	return runSessionWith(ctx, opts, alive, nil)
}

// runSessionWith is runSession whose messages go to show when it is not
// nil (the helper in the foreground of a laptop herdr pane, I-509), else
// to the machine's multiplexer.
func runSessionWith(ctx context.Context, opts sessionOptions, alive func() bool, show func(string)) error {
	t := sshTarget{Args: opts.Target}
	// Messages go out one at a time from here, so the carry's lines and
	// the forwards' never replace each other on the status line.
	msgs := make(chan string, 64)
	say := func(m string) {
		select {
		case msgs <- m:
		default: // a burst past the buffer is not worth blocking a forward for
		}
	}
	shown := make(chan struct{})
	go func() {
		defer close(shown)
		for m := range msgs {
			switch {
			case show != nil:
				show(m)
			case opts.Multiplexer == multiplexer.Herdr:
				herdrMessage(ctx, t, m)
			default:
				tmuxMessage(ctx, t, opts.Slug, m, alive)
			}
		}
	}()
	for _, m := range opts.Messages {
		say(m)
	}
	carried := make(chan struct{})
	go func() {
		defer close(carried)
		if !opts.Carry {
			return
		}
		o, err := carryOverSession(ctx, t, opts)
		if err != nil {
			say("repose could not carry your config: " + oneLine(err.Error()))
			return
		}
		for _, m := range o.Lines() {
			say(m)
		}
	}()
	bridged := make(chan struct{})
	go func() {
		defer close(bridged)
		if opts.Bridge {
			runSessionBridge(ctx, t, opts.Slug, opts.BridgeAllow, say, alive)
		}
	}()
	mcpDone := make(chan struct{})
	go func() {
		defer close(mcpDone)
		if len(opts.MCP) > 0 {
			runSessionMCP(ctx, t, opts.MCP, opts.HomeDir, opts.RepoDir, say, alive)
		}
	}()
	if opts.Forward {
		runForwards(ctx, newForwarder(t, opts.Slug, say), alive)
	}
	<-mcpDone
	<-bridged
	<-carried
	close(msgs)
	<-shown
	return nil
}

// carryOverSession is the carry on its own: one ssh for the markers, one
// for what changed. Two round trips, both beside the attach.
func carryOverSession(ctx context.Context, t sshTarget, opts sessionOptions) (*carryOutcome, error) {
	out, err := runSSH(ctx, t, markerScript(), nil)
	if err != nil {
		return nil, err
	}
	co := carryOptions{TZ: opts.TZ, Markers: parseMarkers(string(out))}
	var warnings []string
	if opts.RepoDir != "" {
		gc, err := buildGitCarry(opts.RepoDir, opts.HomeDir)
		if err != nil {
			warnings = append(warnings, "Could not read your git config ("+oneLine(err.Error())+"); the guest keeps its own.")
		}
		co.Git = gc
		if gc != nil {
			warnings = append(warnings, gc.Notes...)
		}
	}
	if cc, _ := buildClaudeCarry(opts.HomeDir); cc != nil {
		co.Claude = cc
		warnings = append(warnings, cc.Notes...)
	}
	// The tools carry's bins with the globals in, a superset of what run
	// left out: the hash does not depend on them (I-556).
	bins := toolBinsOf(buildToolsCarry(opts.HomeDir, opts.RepoDir, precedenceFor(false, opts.RepoDir)))
	mc, notes := buildMCPCarry(opts.HomeDir, opts.RepoDir, opts.Slug, opts.Checkout, bins)
	warnings = append(warnings, notes...)
	if mc != nil && opts.MCPOff {
		mc = mc.off()
	}
	co.MCP = mc
	p := newGuestPayload()
	sent, err := addCarry(p, co)
	if err != nil {
		return nil, err
	}
	o := &carryOutcome{Sent: sent, Warnings: warnings}
	if len(sent) == 0 {
		return o, nil
	}
	res, err := p.run(ctx, t)
	if err != nil {
		return nil, err
	}
	o.parse(string(res))
	return o, nil
}

// tmuxMessageWait is how long a message waits for a client to show it on:
// the helper usually finishes before tmux has attached.
const tmuxMessageWait = 8 * time.Second

// tmuxMessage shows msg for four seconds on the session's client, the
// way every helper output reaches the user (never over the pane). It
// waits, briefly, for a client to be attached.
func tmuxMessage(ctx context.Context, t sshTarget, slug, msg string, alive func() bool) {
	cmd := fmt.Sprintf("tmux display-message -d 4000 -t %s %s", shQuote("="+slug+":"), shQuote(strings.ReplaceAll(msg, "#", "##")))
	deadline := time.Now().Add(tmuxMessageWait)
	for {
		clients, err := runSSH(ctx, t, fmt.Sprintf("tmux list-clients -t %s -F x", shQuote("="+slug)), nil)
		if err == nil && strings.TrimSpace(string(clients)) != "" {
			_ = runSSHOK(ctx, t, cmd)
			// Two messages in a row would replace each other at once.
			_ = sleepOrDone(ctx, 1500*time.Millisecond)
			return
		}
		if time.Now().After(deadline) || !alive() {
			return
		}
		if sleepOrDone(ctx, 250*time.Millisecond) != nil {
			return
		}
	}
}

// oneLine keeps the first line of s.
func oneLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
