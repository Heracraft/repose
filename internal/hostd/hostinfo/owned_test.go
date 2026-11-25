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
