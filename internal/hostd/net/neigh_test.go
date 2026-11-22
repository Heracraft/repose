package net

import (
	"context"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// FlushNeigh deletes the bridge's neighbour entry for the address, and an
// address with no entry (ip exits non-zero, "No such file or directory")
// is not an error (DECISIONS I-1530).
func TestFlushNeigh(t *testing.T) {
	r := &shell.Fake{}
	n := NewReal(r)
	if err := n.FlushNeigh(context.Background(), "10.64.0.3"); err != nil {
		t.Fatal(err)
	}
	calls := r.CallsWithPrefix("ip", "neigh")
	if len(calls) != 1 || strings.Join(calls[0], " ") != "ip neigh del 10.64.0.3 dev br-guests" {
		t.Fatalf("calls %v", calls)
	}
	gone := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"ip", "neigh"}, Handle: func(argv []string) (shell.Result, error) {
		res := shell.Result{ExitCode: 2, Stderr: []byte("RTNETLINK answers: No such file or directory\n")}
		return res, &shell.ExitError{Argv: argv, Result: res}
	}}}}
	if err := NewReal(gone).FlushNeigh(context.Background(), "10.64.0.3"); err != nil {
		t.Fatalf("an absent entry failed the flush: %v", err)
	}
}
