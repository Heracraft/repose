package app

import "testing"

func TestFootprintParsers(t *testing.T) {
	if b, ok := physFootprint([]byte("com.apple.Virtualization.VirtualMachine [2532]: 64-bit\tFootprint: 4447 MB\n\tphys_footprint: 4447 MB\n")); !ok || b != 4447<<20 {
		t.Fatalf("%d %v", b, ok)
	}
	if b, ok := physFootprint([]byte("phys_footprint: 1.5 GB")); !ok || b != 3<<29 {
		t.Fatalf("%d %v", b, ok)
	}
	if p := responsiblePID([]byte("program path = /x\n\tresponsible pid = 2493\n")); p != "2493" {
		t.Fatalf("%q", p)
	}
}
