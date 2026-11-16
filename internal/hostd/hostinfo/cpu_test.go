package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleCPUStat = `usage_usec 1234567
user_usec 900000
system_usec 334567
nr_periods 10
nr_throttled 2
throttled_usec 5000
`

func TestCgroupCPUUsageReadsUsageUsec(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "guests.slice", "guest@g-abc.service")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte(sampleCPUStat), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := cgroupCPUUsageFrom(root, "/guests.slice/guest@g-abc.service")
	if err != nil || got != 1234567 {
		t.Fatalf("usage = %d, %v; want 1234567", got, err)
	}
	if _, err := cgroupCPUUsageFrom(root, "guests.slice"); err == nil {
		t.Error("a relative cgroup path was accepted")
	}
	if _, err := cgroupCPUUsageFrom(root, "/../etc"); err == nil {
		t.Error("a path with .. was accepted")
	}
	if _, err := cgroupCPUUsageFrom(root, "/nosuch"); err == nil {
		t.Error("a missing cgroup read succeeded")
	}
	if _, err := cpuStatUsage([]byte("nr_periods 3\n")); err == nil {
		t.Error("a cpu.stat without usage_usec parsed")
	}
}

func TestSetCgroupCPUMaxWritesQuotaAndPeriod(t *testing.T) {
	root := t.TempDir()
	cg := "/guests.slice/guest@g-abc.service"
	if err := os.MkdirAll(filepath.Join(root, cg), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		milli uint64
		want  string
	}{
		{1000, "100000 100000"}, // one core: the small's base
		{2000, "200000 100000"}, // the large's base
		{6000, "600000 100000"}, // the large's ceiling
	} {
		if err := setCgroupCPUMaxIn(root, cg, tc.milli); err != nil {
			t.Fatalf("%d millicores: %v", tc.milli, err)
		}
		b, err := os.ReadFile(filepath.Join(root, cg, "cpu.max"))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("%d millicores wrote %q; want %q", tc.milli, b, tc.want)
		}
	}
	if err := setCgroupCPUMaxIn(root, "relative", 1000); err == nil {
		t.Error("a relative cgroup path was accepted")
	}
}
