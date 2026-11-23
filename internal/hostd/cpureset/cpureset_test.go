package cpureset

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func sysTree(t *testing.T, online string, withFile ...int) string {
	t.Helper()
	root := t.TempDir()
	cpu := filepath.Join(root, "devices", "system", "cpu")
	if err := os.MkdirAll(cpu, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cpu, "online"), []byte(online+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range withFile {
		d := filepath.Join(cpu, "cpu"+itoa(c))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "online"), []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func itoa(i int) string { return strconv.Itoa(i) }

type recorder struct {
	mu     sync.Mutex
	writes []string
	fail   map[string]int // "cpuN=V" -> failures left
}

func (rc *recorder) write(path, value string) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	key := filepath.Base(filepath.Dir(path)) + "=" + value
	rc.writes = append(rc.writes, key)
	if rc.fail[key] > 0 {
		rc.fail[key]--
		return errors.New("EBUSY")
	}
	return os.WriteFile(path, []byte(value), 0)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Every online CPU goes offline and straight back, one at a time, cpu0
// included: a guest's vCPUs leave whichever CPUs they last ran on busy.
func TestCycleTakesEachCPUOffAndOnInTurn(t *testing.T) {
	root := sysTree(t, "0-3", 0, 1, 2, 3)
	rc := &recorder{}
	r := New(root, quiet())
	r.Write = rc.write
	n, err := r.Cycle()
	if err != nil || n != 4 {
		t.Fatalf("Cycle = %d, %v; want 4, nil", n, err)
	}
	want := []string{"cpu0=0", "cpu0=1", "cpu1=0", "cpu1=1", "cpu2=0", "cpu2=1", "cpu3=0", "cpu3=1"}
	if !reflect.DeepEqual(rc.writes, want) {
		t.Fatalf("writes %v, want %v", rc.writes, want)
	}
	for c := 0; c < 4; c++ {
		b, _ := os.ReadFile(filepath.Join(root, "devices", "system", "cpu", "cpu"+itoa(c), "online"))
		if string(b) != "1" {
			t.Fatalf("cpu%d online = %q, want 1", c, b)
		}
	}
}

// A CPU with no online file (one the kernel cannot take offline) is
// skipped; a host with one CPU online is left alone.
func TestCycleSkipsWhatCannotGoAndASingleCPU(t *testing.T) {
	rc := &recorder{}
	r := New(sysTree(t, "0-2", 1, 2), quiet())
	r.Write = rc.write
	if n, err := r.Cycle(); err != nil || n != 2 {
		t.Fatalf("Cycle = %d, %v; want 2, nil", n, err)
	}
	if rc.writes[0] != "cpu1=0" {
		t.Fatalf("first write %q, want cpu1=0", rc.writes[0])
	}
	rc = &recorder{}
	r = New(sysTree(t, "0", 0), quiet())
	r.Write = rc.write
	if n, err := r.Cycle(); err != nil || n != 0 || len(rc.writes) != 0 {
		t.Fatalf("one CPU: Cycle = %d, %v, writes %v; want nothing", n, err, rc.writes)
	}
}

// A CPU that refuses to come back is retried; one that never does stops
// the cycle, so no second CPU is taken from the host.
func TestCycleNeverLeavesTwoCPUsOffline(t *testing.T) {
	root := sysTree(t, "0-2", 0, 1, 2)
	rc := &recorder{fail: map[string]int{"cpu0=1": 2}}
	r := New(root, quiet())
	r.Write = rc.write
	if n, err := r.Cycle(); err != nil || n != 3 {
		t.Fatalf("retried online: Cycle = %d, %v; want 3, nil", n, err)
	}
	rc = &recorder{fail: map[string]int{"cpu1=1": 99}}
	r.Write = rc.write
	n, err := r.Cycle()
	if err == nil || !strings.Contains(err.Error(), "cpu1 stays offline") || n != 1 {
		t.Fatalf("Cycle = %d, %v; want 1 and cpu1 stays offline", n, err)
	}
	for _, w := range rc.writes {
		if strings.HasPrefix(w, "cpu2=") {
			t.Fatalf("cpu2 touched after cpu1 stayed offline: %v", rc.writes)
		}
	}
}

// Kicks while a cycle is pending coalesce; Run cycles after the settle.
func TestRunCoalescesKicks(t *testing.T) {
	root := sysTree(t, "0-1", 0, 1)
	rc := &recorder{}
	r := New(root, quiet())
	r.Write = rc.write
	r.Settle = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	for i := 0; i < 5; i++ {
		r.Kick()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rc.mu.Lock()
		n := len(rc.writes)
		rc.mu.Unlock()
		if n >= 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.writes) != 4 {
		t.Fatalf("writes %v: want one cycle of 2 CPUs for 5 kicks", rc.writes)
	}
	var nilR *Resetter
	nilR.Kick() // a host without the problem: no panic
}

func TestEnabled(t *testing.T) {
	apple := t.TempDir()
	dmi := filepath.Join(apple, "class", "dmi", "id")
	if err := os.MkdirAll(dmi, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dmi, "product_name"), []byte("Apple Virtualization Generic Platform\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	for _, tc := range []struct {
		mode, root string
		want       bool
		err        bool
	}{
		{"auto", apple, true, false},
		{"", apple, true, false},
		{"auto", other, false, false},
		{"off", apple, false, false},
		{"on", other, true, false},
		{"yes", apple, false, true},
	} {
		got, err := Enabled(tc.mode, tc.root)
		if got != tc.want || (err != nil) != tc.err {
			t.Errorf("Enabled(%q, %s) = %v, %v", tc.mode, filepath.Base(tc.root), got, err)
		}
	}
}

func TestParseList(t *testing.T) {
	got, err := ParseList("0-3,6,8-9")
	if err != nil || !reflect.DeepEqual(got, []int{0, 1, 2, 3, 6, 8, 9}) {
		t.Fatalf("ParseList = %v, %v", got, err)
	}
	for _, bad := range []string{"a", "3-1", "1-x"} {
		if _, err := ParseList(bad); err == nil {
			t.Errorf("ParseList(%q) accepted", bad)
		}
	}
}
