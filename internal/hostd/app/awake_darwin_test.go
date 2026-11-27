package app

import (
	"io"
	"log/slog"
	"os/exec"
	"testing"
)

// The Mac is held awake while a machine runs and let go when none does
// (D1's documented behaviour, d2-surface 2026-11-27): one assertion, not
// one per tick, and none left when the last machine stops.
func TestKeepAwakeFollowsTheMachines(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	starts := 0
	k := &keepAwake{start: func() (*exec.Cmd, error) { starts++; return &exec.Cmd{}, nil }}
	k.set(false, log)
	if starts != 0 || k.cmd != nil {
		t.Fatal("held awake with no machine running")
	}
	k.set(true, log)
	k.set(true, log)
	if starts != 1 || k.cmd == nil {
		t.Fatalf("assertions taken %d, want 1", starts)
	}
	k.set(false, log)
	if k.cmd != nil {
		t.Fatal("still held with no machine running")
	}
	k.set(true, log)
	if starts != 2 {
		t.Fatalf("not taken again when a machine starts: %d", starts)
	}
}
