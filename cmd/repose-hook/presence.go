package main

// repose-presence (DECISIONS I-1098): the guest command that says whether
// the owner is at the machine, so an agent handing over a URL for a port
// here knows whether `http://localhost:PORT` reaches them. It is
// repose-hook under one more name, like repose-notify and repose-ask, so
// the guest base gains no binary.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/heracraft/repose/internal/guestd/hooks"
)

const presenceUsage = `usage: repose-presence

Prints "attached" while the owner is at the machine and "away" while they
are not. At the machine means a tmux client or an SSH connection; an
editor over SSH and a laptop herdr bridge are SSH connections.

Exits 0 either way, and 1 when the machine's agent service does not answer.
`

// runPresence is repose-presence.
func runPresence(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("repose-presence", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	socket := fs.String("socket", socketDefault(), "guestd hook socket")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(stdout, presenceUsage)
			return ExitOK
		}
		_, _ = fmt.Fprintf(stderr, "repose-presence: %v\n\n%s", err, presenceUsage)
		return ExitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "repose-presence: takes no arguments\n\n%s", presenceUsage)
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	code, body, err := call(ctx, socketClient(*socket, Timeout), http.MethodGet, "/presence", nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-presence: guestd is not answering on %s: %v\n", *socket, err)
		return ExitError
	}
	if code != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "repose-presence: guestd answered %d: %s\n", code, strings.TrimSpace(string(body)))
		return ExitError
	}
	var st hooks.PresenceState
	if err := json.Unmarshal(body, &st); err != nil || (st.State != hooks.StateAttached && st.State != hooks.StateAway) {
		_, _ = fmt.Fprintf(stderr, "repose-presence: guestd answered something other than a presence\n")
		return ExitError
	}
	_, _ = fmt.Fprintln(stdout, st.State)
	return ExitOK
}
