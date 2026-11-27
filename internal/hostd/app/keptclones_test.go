//go:build darwin

package app

import (
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

func TestMemoryTight(t *testing.T) {
	mi := func(total, avail uint64) func() (uint64, uint64, error) {
		return func() (uint64, uint64, error) { return total, avail, nil }
	}
	if memoryTight(mi(24<<30, 18<<30)) {
		t.Fatal("18 of 24 GiB free read as tight")
	}
	if !memoryTight(mi(24<<30, 5<<30)) {
		t.Fatal("5 of 24 GiB free read as not tight")
	}
	if !memoryTight(nil) {
		t.Fatal("no reading read as not tight")
	}
}
