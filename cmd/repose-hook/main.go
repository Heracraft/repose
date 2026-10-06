// Command repose-hook is what an agent's hook configuration calls. It reads
// the agent's own hook payload, maps it to {agent, kind, summary}, and POSTs
// it to guestd's hook socket.
//
// It always exits 0. A hook that fails must never block an agent, because a
// blocked agent is a silently wasted night (docs/features/agents.md).
//
// Run as repose-notify or repose-ask (or `repose-hook notify|ask`) it is
// instead the command an agent calls to message the user or ask them
// something (ask.go, DECISIONS I-244); those exit non-zero on failure.
// Run as repose-mcp (or `repose-hook mcp`) it is the guest's MCP registry
// command (mcp.go, DECISIONS I-555).
//
// Workstream: docs/workstreams/04-guestd.md.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/heracraft/repose/internal/guestd/hooks"
)

var version = "dev" // set by -ldflags at release

// DefaultSocket is the hook socket of docs/interfaces/guest-conventions.md.
const DefaultSocket = "/run/repose/hooks.sock"

// Timeout bounds the POST. The agent is waiting on this process.
const Timeout = 3 * time.Second

func main() {
	if args, ok := isMCP(os.Args); ok {
		os.Exit(runMCP(args, os.Stdin, os.Stdout, os.Stderr))
	}
	if name, args := isSub(os.Args); name != "" {
		os.Exit(runSub(name, args))
	}
	// Whatever happens below, the exit code is zero.
	if msg := run(); msg != "" {
		fmt.Fprintln(os.Stderr, "repose-hook:", msg)
	}
}

func run() string {
	fs := flag.NewFlagSet("repose-hook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		agent   = fs.String("agent", agentDefault(), "which agent is reporting")
		socket  = fs.String("socket", socketDefault(), "guestd hook socket")
		window  = fs.String("window", os.Getenv("REPOSE_AGENT_WINDOW"), "tmux window name, if the wrapper knows it")
		showVer = fs.Bool("version", false, "print the version and exit")
	)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err.Error()
	}
	if *showVer {
		fmt.Println("repose-hook", version)
		return ""
	}
	if *agent == "" {
		return "no agent given; pass --agent or set REPOSE_HOOK_AGENT"
	}

	payload, err := readPayload(fs.Args())
	if err != nil {
		return err.Error()
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return "empty payload"
	}

	p, err := hooks.Map(*agent, payload)
	if err != nil {
		if errors.Is(err, hooks.ErrNoEvent) {
			return ""
		}
		return err.Error()
	}
	if *window != "" {
		p.Window = *window
	}

	if err := post(*socket, p); err != nil {
		return err.Error()
	}
	return ""
}

// agentDefault reads the variable the agent wrappers export.
// docs/interfaces/guest-conventions.md names it REPOSE_HOOK_AGENT, and
// nix/overlay/agents/wrap.nix exports that; REPOSE_AGENT is the name this
// binary shipped with and stays accepted for one release (DECISIONS I-58).
func agentDefault() string {
	if a := os.Getenv("REPOSE_HOOK_AGENT"); a != "" {
		return a
	}
	return os.Getenv("REPOSE_AGENT")
}

// socketDefault reads REPOSE_HOOK_SOCKET, or REPOSE_HOOKS_SOCKET, which is
// the name the shell implementation in nix/overlay/agents used; both point at
// the same socket and the default path is the same either way.
func socketDefault() string {
	if s := os.Getenv("REPOSE_HOOK_SOCKET"); s != "" {
		return s
	}
	if s := os.Getenv("REPOSE_HOOKS_SOCKET"); s != "" {
		return s
	}
	return DefaultSocket
}

// readPayload takes the JSON from the first argument when there is one (Codex
// CLI's notify passes it that way) and from stdin otherwise (Claude Code's
// hooks do).
func readPayload(args []string) ([]byte, error) {
	if len(args) > 0 && args[0] != "" && args[0] != "-" {
		return []byte(args[0]), nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read the hook payload: %w", err)
	}
	return b, nil
}

func post(socket string, p hooks.Payload) error {
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode the event: %w", err)
	}
	client := &http.Client{
		Timeout: Timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://guestd/", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post to the hook socket: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response is drained below
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the hook socket answered %d", resp.StatusCode)
	}
	return nil
}
