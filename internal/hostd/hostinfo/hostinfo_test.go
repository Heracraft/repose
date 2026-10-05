package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemInfo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "meminfo")
	_ = os.WriteFile(p, []byte("MemTotal:       65536000 kB\nMemFree:         1000 kB\nMemAvailable:   40000000 kB\n"), 0o644)
	total, avail, err := memInfoFrom(p)
	if err != nil || total != 65536000<<10 || avail != 40000000<<10 {
		t.Fatalf("%d %d %v", total, avail, err)
	}
}

func TestCgroupCPUPressure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "system.slice", "guest@g-1.service")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.pressure"), []byte("some avg10=0.00 avg60=0.00 avg300=0.00 total=9000\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := cgroupCPUPressureFrom(root, "/system.slice/guest@g-1.service"); err != nil || got != 9000 {
		t.Fatalf("pressure = %d, %v; want 9000", got, err)
	}
	for _, bad := range []string{"system.slice/x", "/system.slice/../../etc", ""} {
		if _, err := cgroupCPUPressureFrom(root, bad); err == nil {
			t.Errorf("cgroup %q accepted", bad)
		}
	}
}
