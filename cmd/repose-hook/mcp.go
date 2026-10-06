package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/heracraft/repose/internal/mcpreg"
	"github.com/heracraft/repose/internal/mcpshim"
)

// Run as repose-mcp (or `repose-hook mcp`), this binary is the guest's MCP
// registry command (DECISIONS I-555, docs/interfaces/guest-conventions.md
// "MCP registry").
const mcpUsage = `usage: repose-mcp sync [AGENT...]
       repose-mcp run NAME
       repose-mcp status --json
       repose-mcp hold [--remove] NAME...
       repose-mcp NAME

sync renders ~/.repose/mcp into each AGENT's config (all five by default)
and always exits 0. run starts the carried stdio server NAME with its
${NAME} references filled from the machine's secrets. status prints what
each agent has as JSON. hold and NAME are the two ends of repose mcp
forward: hold serves the laptop's servers on /run/repose/mcp/NAME.sock for
as long as its stdin lasts, and NAME is the stdio server agents start.
hold --remove takes NAME off every agent.
`

// isMCP reports whether this binary runs as repose-mcp, from its name or
// its first argument, and returns the arguments that follow.
func isMCP(argv []string) ([]string, bool) {
	if filepath.Base(argv[0]) == "repose-mcp" {
		return argv[1:], true
	}
	if len(argv) > 1 && argv[1] == "mcp" {
		return argv[2:], true
	}
	return nil, false
}

func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, mcpUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, mcpUsage)
		return 0
	case "sync":
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "repose-mcp: no home directory; MCP servers not updated")
			return 0
		}
		agents := args[1:]
		if len(agents) == 0 {
			agents = mcpreg.Agents
		}
		mcpreg.Sync(mcpreg.DefaultPaths(home), agents, stderr)
		return 0
	case "run":
		if len(args) != 2 {
			fmt.Fprint(stderr, mcpUsage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "repose-mcp: no home directory")
			return 1
		}
		path, argv, env, err := mcpreg.Prepare(mcpreg.DefaultPaths(home), args[1])
		var le *mcpreg.LaunchError
		if errors.As(err, &le) {
			fmt.Fprintln(stderr, le.Msg)
			return le.Code
		}
		if err != nil {
			fmt.Fprintln(stderr, "repose-mcp:", err)
			return 1
		}
		err = execve(path, argv, env)
		fmt.Fprintf(stderr, "repose-mcp: %s did not start: %v\n", args[1], err)
		return 126
	case "status":
		if len(args) != 2 || args[1] != "--json" {
			fmt.Fprint(stderr, mcpUsage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "repose-mcp: no home directory")
			return 1
		}
		st, err := mcpreg.ReadStatus(mcpreg.DefaultPaths(home))
		if err != nil {
			fmt.Fprintln(stderr, "repose-mcp:", err)
			return 1
		}
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(st); err != nil {
			return 1
		}
		return 0
	case "hold":
		return mcpshim.Hold(args[1:], stdin, stdout, stderr)
	}
	if mcpreg.ValidName(args[0]) && len(args) == 1 {
		return mcpshim.Serve(args[0], stdin, stdout, stderr)
	}
	fmt.Fprint(stderr, mcpUsage)
	return 2
}
