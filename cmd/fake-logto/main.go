// Command fake-logto runs test/fake-logto as a standalone process, for the
// dashboard's Playwright suite to sign in against instead of the real
// self-hosted Logto (docs/workstreams/08-dashboard.md §7).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	fakelogto "github.com/heracraft/repose/test/fake-logto"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "the address to listen on, which is also the issuer the browser is sent to")
	issuer := flag.String("issuer", "", "the URL the browser reaches this fake at, when a proxy serves it (default: the listen address)")
	flag.Parse()
	f, err := fakelogto.NewOn(*listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake-logto:", err)
		os.Exit(1)
	}
	if *issuer != "" {
		f.SetIssuer(*issuer)
	}
	defer func() { _ = f.Close() }()

	// The one line of stdout this process ever prints.
	fmt.Printf("FAKELOGTO_URL=%s\n", f.Issuer())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
