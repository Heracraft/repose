package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
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

// guests.slice on an owned host keeps the larger CPU share for the
// appliance's own services and no CPUQuota (DECISIONS I-1640): a quota
// throttled the nested guests' vCPUs in every period, and four busy guests
// hung with 1.3 GiB free (the lab, 2026-11-26 02:51Z). "CPUQuota=" clears
// a quota an earlier hostd set at runtime.
func TestOwnedSliceKeepsAShareForTheHostWithoutAQuota(t *testing.T) {
	r := &recRunner{}
	ownedSlice(context.Background(), r, 16<<30, slog.New(slog.DiscardHandler))
	if len(r.argv) != 1 {
		t.Fatalf("runs %v", r.argv)
	}
	got := r.argv[0]
	joined := strings.Join(got, " ")
	for _, want := range []string{"set-property --runtime guests.slice", "MemoryMax=16642998272", "CPUWeight=50"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q lacks %q", joined, want)
		}
	}
	if !slices.Contains(got, "CPUQuota=") || strings.Contains(joined, "%") {
		t.Fatalf("argv %q: want CPUQuota= (cleared), no quota", joined)
	}
}

// The bound on a nested owned host is its vCPUs less two (DECISIONS
// I-1640): three machines of two on the M4's eight; never under one
// machine; bare metal none.
func TestOwnedMaxVCPUs(t *testing.T) {
	for _, c := range []struct {
		nested bool
		host   uint32
		want   uint32
	}{{true, 8, 6}, {true, 10, 8}, {true, 4, 2}, {true, 3, 2}, {true, 2, 2}, {true, 1, 1}, {false, 8, 0}} {
		if got := ownedMaxVCPUs(c.nested, c.host); got != c.want {
			t.Fatalf("ownedMaxVCPUs(nested %v, %d) = %d, want %d", c.nested, c.host, got, c.want)
		}
	}
}

// --owned-max-vcpus replaces the bound (the lab's runs past it); 0 keeps
// the rule.
func TestOwnedMaxVCPUsOverride(t *testing.T) {
	if got := ownedBound(8, true, 8); got != 8 {
		t.Fatalf("override 8 on a nested 8: %d", got)
	}
	if got := ownedBound(0, true, 8); got != 6 {
		t.Fatalf("no override on a nested 8: %d", got)
	}
	if got := ownedBound(4, false, 8); got != 4 {
		t.Fatalf("override 4 on bare metal: %d", got)
	}
}
