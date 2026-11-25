package app

import (
	"errors"
	"testing"
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

func TestOwnedVCPUs(t *testing.T) {
	for _, c := range []struct {
		flag uint
		host uint32
		want uint32
	}{{0, 8, 8}, {2, 8, 2}, {6, 8, 6}, {12, 8, 8}} {
		if got := ownedVCPUs(c.flag, c.host); got != c.want {
			t.Fatalf("ownedVCPUs(%d, %d) = %d, want %d", c.flag, c.host, got, c.want)
		}
	}
}
