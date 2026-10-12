package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBootIDAndTime(t *testing.T) {
	dir := t.TempDir()
	id := filepath.Join(dir, "boot_id")
	_ = os.WriteFile(id, []byte("8b0c5e7e-1f2a-4c3d-9e8f-0a1b2c3d4e5f\n"), 0o644)
	if got, err := bootIDFrom(id); err != nil || got != "8b0c5e7e-1f2a-4c3d-9e8f-0a1b2c3d4e5f" {
		t.Fatalf("boot id %q, %v", got, err)
	}
	_ = os.WriteFile(id, []byte("\n"), 0o644)
	if _, err := bootIDFrom(id); err == nil {
		t.Fatal("an empty boot id was accepted")
	}
	st := filepath.Join(dir, "stat")
	_ = os.WriteFile(st, []byte("cpu  1 2 3\nbtime 1760075172\nprocesses 9\n"), 0o644)
	if got, err := bootTimeFrom(st); err != nil || !got.Equal(time.Unix(1760075172, 0)) {
		t.Fatalf("boot time %v, %v", got, err)
	}
	if _, err := BootID(); err != nil {
		t.Fatalf("this machine's boot id: %v", err)
	}
}

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
