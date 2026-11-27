package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A root hostd's VMs run as the user and open their files under hostd's
// directories; a 0700 directory on the way stops them (lab Mac,
// 2026-11-27: "Permission denied" on the volume under the state
// directory). unreachable names the directory that blocks.
func TestUnreachableNamesTheBlockingDirectory(t *testing.T) {
	root := t.TempDir()
	// t.TempDir and the directory Go makes above it are 0700, which would
	// block on their own.
	for _, d := range []string{filepath.Dir(root), root} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(root, "state")
	vols := filepath.Join(state, "volumes")
	if err := os.MkdirAll(vols, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o700); err != nil {
		t.Fatal(err)
	}
	other := os.Getuid() + 4242 // not the owner, not in the group
	if got := unreachable(vols, other, os.Getgid()+4242); got != state {
		t.Fatalf("unreachable = %q, want %q", got, state)
	}
	if err := os.Chmod(state, 0o711); err != nil {
		t.Fatal(err)
	}
	if got := unreachable(vols, other, os.Getgid()+4242); got != "" {
		t.Fatalf("unreachable after 0711 = %q", got)
	}
	if got := unreachable(filepath.Join(vols, "not-made-yet"), other, os.Getgid()+4242); got != "" {
		t.Fatalf("a directory not made yet: %q", got)
	}
}
