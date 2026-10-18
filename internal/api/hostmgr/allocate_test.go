package hostmgr

import (
	"net/netip"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestAllocateStepsOverReserved pins the addresses the edge already holds
// by hand out of the host plan (DECISIONS I-782). host-01 keeps 10.255.0.2:
// renumbering a registered host would strand it.
func TestAllocateStepsOverReserved(t *testing.T) {
	for _, c := range []struct {
		seq      int64
		wantCIDR string
		wantWG   string
	}{
		{1, "10.64.0.0/22", "10.255.0.2"},
		{2, "10.64.4.0/22", "10.255.0.4"},
		{3, "10.64.8.0/22", "10.255.0.5"},
	} {
		cidr, wg, err := allocate(c.seq)
		if err != nil {
			t.Fatalf("allocate(%d): %v", c.seq, err)
		}
		if cidr.String() != c.wantCIDR {
			t.Errorf("allocate(%d) cidr = %s, want %s", c.seq, cidr, c.wantCIDR)
		}
		if wg.String() != c.wantWG {
			t.Errorf("allocate(%d) wg = %s, want %s", c.seq, wg, c.wantWG)
		}
	}
}

// TestAllocateHandsOutEachAddressOnce is what a bare skip would break: an
// offset that steps over a reserved address must not land on the one the
// next sequence number was already going to get.
func TestAllocateHandsOutEachAddressOnce(t *testing.T) {
	seen := map[netip.Addr]int64{}
	for n := int64(1); n <= 1000; n++ {
		_, wg, err := allocate(n)
		if err != nil {
			t.Fatalf("allocate(%d): %v", n, err)
		}
		if prev, dup := seen[wg]; dup {
			t.Fatalf("allocate(%d) and allocate(%d) both give %s", prev, n, wg)
		}
		seen[wg] = n
		for _, r := range wgReservedOffsets {
			if res := addrAt(r); wg == res {
				t.Fatalf("allocate(%d) gives the reserved %s", n, res)
			}
		}
	}
}

// TestEdgeStaticPeersAreReserved is the drift guard: every 10.255.0.x the
// edge configuration hands to a peer of its own must be in
// wgReservedOffsets, or the api will one day give it to a host.
func TestEdgeStaticPeersAreReserved(t *testing.T) {
	b, err := os.ReadFile("../../../nix/edge/edge-01.nix")
	if err != nil {
		t.Fatalf("read edge-01.nix: %v", err)
	}
	// A peer address is written as a /32 or as the host of a URL; the
	// /16 and the prose in comments are neither.
	peer := regexp.MustCompile(`10\.255\.0\.(\d+)(?:/32|:\d+)`)
	found := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := peer.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		a, err := netip.ParseAddr("10.255.0." + m[1])
		if err != nil {
			t.Fatalf("parse %s: %v", m[0], err)
		}
		if a == netip.MustParseAddr("10.255.0.1") {
			continue // the edge itself, not a peer
		}
		found++
		reserved := false
		for _, r := range wgReservedOffsets {
			if addrAt(r) == a {
				reserved = true
			}
		}
		if !reserved {
			t.Errorf("edge-01.nix uses %s but wgReservedOffsets does not hold it: a host will be given it", a)
		}
	}
	if found == 0 {
		t.Error("no 10.255.0.x peer found in edge-01.nix; the guard is reading the wrong file")
	}
}

func addrAt(off uint32) netip.Addr {
	i := uint32(10)<<24 | uint32(255)<<16 | off
	return netip.AddrFrom4([4]byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)})
}
