//go:build darwin

package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestNewestPerProject(t *testing.T) {
	in := []string{"u/p1/2026-11-26T10-00-00Z.img.zst", "u/p1/2026-11-26T12-00-00Z.img.zst", "u/p1/2026-11-26T11-00-00Z.img.zst", "u/p2/2026-11-26T09-00-00Z.img.zst"}
	got := newestPerProject(in, 2)
	sort.Strings(got)
	if strings.Join(got, ",") != "u/p1/2026-11-26T11-00-00Z.img.zst,u/p1/2026-11-26T12-00-00Z.img.zst,u/p2/2026-11-26T09-00-00Z.img.zst" {
		t.Fatalf("%v", got)
	}
}

// A destroyed machine keeps no APFS clone of its disk (DECISIONS I-1712):
// with the disk gone the clone owns every block, and 22 of them held 36.8
// GiB of a Mac for 30 days. Its final snapshot stays as a stream.
func TestKeptClonesOnlyForLiveMachines(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"u/live/2026-11-26T10-00-00Z.img.zst", "u/gone/2026-11-26T11-00-00Z.img.zst"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := keptClonesWanted(dir, map[string]bool{"live": true})
	if len(got) != 1 || got[0] != "u/live/2026-11-26T10-00-00Z.img.zst" {
		t.Fatalf("kept %v", got)
	}
}
