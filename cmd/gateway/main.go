// Command gateway is the repose SSH gateway and WireGuard peer sync on the
// edge (docs/workstreams/06-gateway-edge.md, docs/interfaces/ssh-gateway.md).
//
//	gateway serve    the SSH gateway on :22, the hook-ingest forwarder on the
//	                 WireGuard address :8443, the preview-proxy stub on :443,
//	                 the metrics server, and the CA/revocation refresh loop.
//	gateway handover the unit's ExecReload: start this binary in place of the
//	                 running gateway, which keeps its open sessions until
//	                 they end (DECISIONS I-471).
//	gateway wgsync   the WireGuard peer reconciler; a separate process so its
//	                 CAP_NET_ADMIN is not held by the relay (nix/edge).
//	gateway version  print the version.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev" // set by -ldflags at release

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version":
		fmt.Println("gateway", version)
	case "serve", "":
		if err := serve(signalContext()); err != nil {
			fmt.Fprintln(os.Stderr, "gateway serve:", err)
			os.Exit(1)
		}
	case "handover":
		if err := handoverMain(); err != nil {
			fmt.Fprintln(os.Stderr, "gateway handover:", err)
			os.Exit(1)
		}
	case "wgsync":
		if err := wgsyncMain(signalContext()); err != nil {
			fmt.Fprintln(os.Stderr, "gateway wgsync:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "gateway: unknown command %q; want serve, handover, wgsync or version\n", cmd)
		os.Exit(2)
	}
}

// signalContext is cancelled on SIGINT or SIGTERM so systemd's stop drains
// the relays (docs/workstreams/06-gateway-edge.md §8).
func signalContext() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	_ = stop // the process exits after the context ends; the handler lives for the process
	return ctx
}
