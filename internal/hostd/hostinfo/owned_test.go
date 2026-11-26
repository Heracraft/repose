package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupMemory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, GuestsSlice), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, GuestsSlice, "memory.current"), []byte("123456789\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := cgroupMemoryFrom(root, GuestsSlice)
	if err != nil || got != 123456789 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := cgroupMemoryFrom(root, "missing.slice"); err == nil {
		t.Fatal("a missing cgroup read as a figure")
	}
}

func TestSetOOMScoreAdj(t *testing.T) {
	old := procRoot
	procRoot = t.TempDir()
	t.Cleanup(func() { procRoot = old })
	if err := os.MkdirAll(filepath.Join(procRoot, "42"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetOOMScoreAdj(42, 750); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(procRoot, "42", "oom_score_adj"))
	if string(b) != "750\n" {
		t.Fatalf("wrote %q", b)
	}
	for _, bad := range [][2]int{{0, 10}, {42, 1001}, {42, -1001}} {
		if err := SetOOMScoreAdj(bad[0], bad[1]); err == nil {
			t.Fatalf("pid %d adj %d accepted", bad[0], bad[1])
		}
	}
}

// hostd runs under a hypervisor when the DMI product is Apple's
// Virtualization framework (the Mac appliance) or an x86 kernel's CPU
// flags name "hypervisor"; bare metal has neither (DECISIONS I-1640).
func TestUnderHypervisor(t *testing.T) {
	write := func(root, rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, product, flags string
		want                 bool
	}{
		{"mac appliance", "Apple Virtualization Generic Platform\n", "", true},
		{"x86 vm", "Standard PC (Q35 + ICH9, 2009)\n", "flags\t\t: fpu vme de pse hypervisor lahf_lm\n", true},
		{"x86 bare metal", "PowerEdge R650\n", "flags\t\t: fpu vme de pse lahf_lm\n", false},
		{"arm64 bare metal, no dmi", "", "Features\t: fp asimd evtstrm\n", false},
	} {
		sys, proc := t.TempDir(), t.TempDir()
		if c.product != "" {
			write(sys, "class/dmi/id/product_name", c.product)
		}
		write(proc, "cpuinfo", c.flags)
		if got := underHypervisorFrom(sys, proc); got != c.want {
			t.Fatalf("%s: underHypervisor = %v, want %v", c.name, got, c.want)
		}
	}
}
