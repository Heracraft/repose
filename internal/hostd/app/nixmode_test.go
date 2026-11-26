package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// --nix-mode and the nix binary decide whether a hostd runs Nix guests
// (DECISIONS I-1484): on by default, off by the flag or by a host
// without nix on PATH.
func TestNixEnabled(t *testing.T) {
	old := nixLookPath
	t.Cleanup(func() { nixLookPath = old })
	have := func() error { return nil }
	none := func() error { return errors.New("not found") }
	for _, c := range []struct {
		mode string
		look func() error
		on   bool
		why  string
	}{
		{"", have, true, ""}, {"on", have, true, ""}, {"off", have, false, "nix_mode_off"},
		{"on", none, false, "no_nix_binary"}, {"maybe", have, false, "nix_mode_unknown"},
	} {
		nixLookPath = c.look
		on, why := NixEnabled(Options{NixMode: c.mode})
		if on != c.on || why != c.why {
			t.Errorf("mode %q: %v %q, want %v %q", c.mode, on, why, c.on, c.why)
		}
	}
}

// The owned vCPU rule (DECISIONS I-1640): two per guest when hostd runs
// under a hypervisor (the Mac appliance), every vCPU on bare metal;
// --owned-vcpus overrides both, never above the host's.
func TestOwnedVCPUs(t *testing.T) {
	for _, c := range []struct {
		flag   uint
		host   uint32
		nested bool
		want   uint32
	}{
		{0, 8, true, 2}, {0, 8, false, 8}, {0, 1, true, 1},
		{2, 8, false, 2}, {6, 8, true, 6}, {12, 8, true, 8}, {12, 8, false, 8},
	} {
		if got := ownedVCPUs(c.flag, c.host, c.nested); got != c.want {
			t.Fatalf("ownedVCPUs(%d, %d, nested %v) = %d, want %d", c.flag, c.host, c.nested, got, c.want)
		}
	}
}

type recRunner struct{ argv [][]string }

func (r *recRunner) Run(_ context.Context, argv ...string) (shell.Result, error) {
	r.argv = append(r.argv, argv)
	return shell.Result{}, nil
}

func (r *recRunner) Start(context.Context, io.Reader, ...string) (shell.Proc, error) {
	return nil, errors.New("not used")
}

// guests.slice on an owned host keeps one vCPU and a larger CPU share for
// the appliance's own services (DECISIONS I-1640), beside its memory.
func TestOwnedSliceKeepsACPUForTheHost(t *testing.T) {
	r := &recRunner{}
	ownedSlice(context.Background(), r, 16<<30, 8, slog.New(slog.DiscardHandler))
	if len(r.argv) != 1 {
		t.Fatalf("runs %v", r.argv)
	}
	got := strings.Join(r.argv[0], " ")
	for _, want := range []string{"set-property --runtime guests.slice", "MemoryMax=16642998272", "CPUQuota=700%", "CPUWeight=50"} {
		if !strings.Contains(got, want) {
			t.Fatalf("argv %q lacks %q", got, want)
		}
	}
	r = &recRunner{}
	ownedSlice(context.Background(), r, 16<<30, 1, slog.New(slog.DiscardHandler))
	if got := strings.Join(r.argv[0], " "); strings.Contains(got, "CPUQuota") {
		t.Fatalf("a one-vCPU host has no vCPU to keep back: %q", got)
	}
}
