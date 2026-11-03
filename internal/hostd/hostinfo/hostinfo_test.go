package hostinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

// Cores are counted, not threads: eight CPUs that are two threads on each
// of four cores are four cores (DECISIONS I-735). This is the whole bug
// the field exists for -- runtime.NumCPU() would say eight.
func TestPhysicalCores(t *testing.T) {
	root := t.TempDir()
	for cpu, core := range []string{"0", "0", "1", "1", "2", "2", "3", "3"} {
		dir := filepath.Join(root, "cpu"+strconv.Itoa(cpu), "topology")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "physical_package_id"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "core_id"), []byte(core+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory under the same root that is not a CPU, and a CPU with no
	// topology at all, are both skipped rather than counted.
	if err := os.MkdirAll(filepath.Join(root, "cpufreq"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cpu8"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := physicalCoresFrom(root); got != 4 {
		t.Fatalf("physical cores = %d; want 4 (8 threads on 4 cores)", got)
	}
	// Two sockets of the same core ids are eight cores, not four.
	for cpu, core := range []string{"0", "0", "1", "1", "2", "2", "3", "3"} {
		dir := filepath.Join(root, "cpu1"+strconv.Itoa(cpu), "topology")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "physical_package_id"), []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "core_id"), []byte(core+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := physicalCoresFrom(root); got != 8 {
		t.Fatalf("two sockets = %d cores; want 8", got)
	}
	// An unreadable topology reports the thread count, which is what the
	// field held before it existed: 0 would read as "unknown" and leave
	// the host unbounded.
	if got := physicalCoresFrom(filepath.Join(root, "nothing-here")); got != uint32(runtime.NumCPU()) {
		t.Fatalf("missing topology = %d; want runtime.NumCPU() = %d", got, runtime.NumCPU())
	}
	// On this machine, whatever it is: cores are real and never more than
	// threads. The reverse would have been the original bug.
	if got := PhysicalCores(); got == 0 || got > uint32(runtime.NumCPU()) {
		t.Fatalf("this machine's cores = %d, threads = %d", got, runtime.NumCPU())
	}
}

// TestTainted is the decomposition of the kernel's taint word, with the
// three cases that each fail without the guard (DECISIONS I-1010).
func TestTainted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tainted")

	// 4096 is bit 12, an out-of-tree module. Every host sets it and never
	// clears it, so this is the case that proves `tainted != 0` would have
	// paged for ever: no reason the alert reads may be 1 here.
	_ = os.WriteFile(p, []byte("4096\n"), 0o644)
	got, err := taintedFrom(p)
	if err != nil {
		t.Fatalf("4096: %v", err)
	}
	for _, r := range TaintReasons {
		if got[r] {
			t.Errorf("4096 (out-of-tree module) read %s as tainted", r)
		}
	}

	// 16 is bit 4, a machine check: a taint that is never accompanied by a
	// panic, which is the durable value of reading latched state at all.
	_ = os.WriteFile(p, []byte("16\n"), 0o644)
	got, err = taintedFrom(p)
	if err != nil {
		t.Fatalf("16: %v", err)
	}
	if !got["machine_check"] {
		t.Error("16 did not read machine_check")
	}
	for _, r := range []string{"die", "bad_page", "soft_lockup", "warn"} {
		if got[r] {
			t.Errorf("16 (machine check) also read %s", r)
		}
	}

	// A word with the hard-fault bit and the healthy module bits together:
	// 4096+128 is what a host-02 that had oopsed would read.
	_ = os.WriteFile(p, []byte("4224\n"), 0o644)
	got, _ = taintedFrom(p)
	if !got["die"] || got["machine_check"] {
		t.Errorf("4224 read die=%v machine_check=%v", got["die"], got["machine_check"])
	}

	// The probe. A missing path is an unreadable verdict, not a clean one:
	// every reason reads 1 and HostKernelTainted fires. Revert the error
	// branch of taintedFrom to "leave the gauges alone" and this is the
	// test that fails, with 0s on a host nothing could read.
	got, err = taintedFrom(filepath.Join(dir, "no-such-file"))
	if err == nil {
		t.Fatal("a missing taint word was read without an error")
	}
	for _, r := range TaintReasons {
		if !got[r] {
			t.Errorf("a missing taint word read %s as clean", r)
		}
	}

	// A taint word that is not a number is as unreadable as a missing one.
	_ = os.WriteFile(p, []byte("not-a-word\n"), 0o644)
	got, err = taintedFrom(p)
	if err == nil {
		t.Fatal("a non-numeric taint word was accepted")
	}
	for _, r := range TaintReasons {
		if !got[r] {
			t.Errorf("a non-numeric taint word read %s as clean", r)
		}
	}

	// Every alerting reason is a reason, so the alert's selector cannot
	// name a label value the gauge never carries.
	for _, a := range AlertingTaintReasons {
		if _, ok := taintBits[a]; !ok {
			t.Errorf("alerting reason %q has no bit", a)
		}
	}

	// This machine's own word, read for real: hosts are Intel and this box
	// is Azure AMD, so the value says nothing about a host, but the read
	// path has to work on a live /proc.
	if _, err := Tainted(); err != nil {
		t.Fatalf("this machine's taint word: %v", err)
	}
}

func TestPanicOnOops(t *testing.T) {
	p := filepath.Join(t.TempDir(), "panic_on_oops")
	_ = os.WriteFile(p, []byte("1\n"), 0o644)
	if on, err := panicOnOopsFrom(p); err != nil || !on {
		t.Fatalf("1 read %v, %v", on, err)
	}
	_ = os.WriteFile(p, []byte("0\n"), 0o644)
	if on, err := panicOnOopsFrom(p); err != nil || on {
		t.Fatalf("0 read %v, %v", on, err)
	}
	// Fails closed, like Tainted: an unreadable sysctl warns rather than
	// reporting the host configured.
	if on, err := panicOnOopsFrom(filepath.Join(t.TempDir(), "gone")); err == nil || on {
		t.Fatalf("a missing sysctl read %v, %v", on, err)
	}
}
