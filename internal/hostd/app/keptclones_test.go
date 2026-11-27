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
