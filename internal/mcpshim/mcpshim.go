// Package mcpshim is the guest side of `repose mcp forward` (DECISIONS
// I-557): `repose-mcp NAME`, the stdio server agents start for a server
// that runs on the laptop, and `repose-mcp hold NAME...`, the endpoint the
// laptop's ssh holds open. repose-mcp's dispatch (cmd/repose-hook/mcp.go)
// routes both here.
//
// This base carries the entry points only; the forward unit replaces Serve
// and Hold. Until then both say so and exit 1, and an agent lists a
// forwarded server as failed to start.
package mcpshim

import (
	"fmt"
	"io"
)

// NotBuilt is what both entry points print in a base without forwarding.
const NotBuilt = "forwarding is not built in this base"

// Serve runs the shim for the forwarded server name on stdin and stdout.
// It returns the process exit code.
func Serve(name string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "repose-mcp: %s: %s\n", name, NotBuilt)
	return 1
}

// Hold is `repose-mcp hold NAME...`. It returns the process exit code.
func Hold(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "repose-mcp: "+NotBuilt)
	return 1
}
