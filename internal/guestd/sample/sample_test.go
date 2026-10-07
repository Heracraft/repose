package sample

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

func TestSampleCombinesCachedSignalsAndAFreshProcWalk(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "bash"},
		{pid: 101, ppid: 100, comm: "claude", ticks: 50},
		{pid: 102, ppid: 1, comm: "sshd", uid: 1000},
		{pid: 103, ppid: 1, comm: "node", ticks: 900},
	}
	w, run, _, clk, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput([4]string{"claude", "101", "claude", "0"})
	run.Match["list-clients"] = sysdep.RunResult{Stdout: []byte("/dev/pts/0\n/dev/pts/1\n")}
	ctx := context.Background()
	w.Refresh(ctx)

	h := NewHandler(w.paths, w, quietLog(), clk.now)
	res, err := h.Sample(ctx)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	sig := res.GetSignals()
	if sig.GetSshSessions() != 1 {
		t.Errorf("ssh_sessions = %d, want 1", sig.GetSshSessions())
	}
	if sig.GetTmuxClients() != 2 {
		t.Errorf("tmux_clients = %d, want 2", sig.GetTmuxClients())
	}
	if sig.GetDockerContainers() != 2 {
		t.Errorf("docker_containers = %d, want 2", sig.GetDockerContainers())
	}
	if !sig.GetGuestdOk() {
		t.Error("guestd_ok is false in guestd's own sample")
	}
	if len(sig.GetAgents()) != 1 || sig.GetAgents()[0].GetAgent() != "claude" {
		t.Errorf("agents = %+v", sig.GetAgents())
	}
	if len(res.GetProcs()) == 0 {
		t.Error("no process samples")
	}
	if res.GetPartial() {
		t.Error("partial was set on a healthy sample")
	}
}

func TestSampleNeverCarriesArgumentsOrPaths(t *testing.T) {
	w, run, _, clk, _ := newWatcherFixture(t, []fakeProc{
		{pid: 100, ppid: 1, comm: "node", ticks: 5},
	})
	run.Match["list-windows"] = tmuxOutput()
	w.Refresh(context.Background())

	h := NewHandler(w.paths, w, quietLog(), clk.now)
	res, err := h.Sample(context.Background())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	// The ProcSample shape has one string field and it is the process name.
	// This is the wire-level half of the privacy promise; the strace test is
	// the other half.
	for _, p := range res.GetProcs() {
		if p.GetComm() == "" {
			t.Error("a sample has no name")
		}
		if len(p.GetComm()) > 64 {
			t.Errorf("comm %q is longer than a kernel comm can be, which suggests a command line", p.GetComm())
		}
	}
}

func TestSampleReportsPartialOnAStaleCache(t *testing.T) {
	w, run, _, clk, _ := newWatcherFixture(t, nil)
	run.Match["list-windows"] = tmuxOutput()
	w.Refresh(context.Background())
	clk.advance(CacheStale + time.Second)

	h := NewHandler(w.paths, w, quietLog(), clk.now)
	res, err := h.Sample(context.Background())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if !res.GetPartial() {
		t.Fatal("a stale cache must come back as partial, not as current-looking zeroes")
	}
}

// A sample past its budget still counts the ssh sessions. The /proc walk
// was skipped once Signals had waited out the budget on the watcher's lock,
// and the sample went out with ssh_sessions 0 beside the cached
// tmux_clients: `sessions 0   tmux clients 1` on an attached machine.
func TestSamplePastItsBudgetStillCountsSSHSessions(t *testing.T) {
	w, run, _, clk, _ := newWatcherFixture(t, []fakeProc{
		{pid: 102, ppid: 1, comm: "sshd-session", uid: 1000},
		{pid: 103, ppid: 102, comm: "tmux: client", uid: 1000},
	})
	run.Match["list-windows"] = tmuxOutput()
	run.Match["list-clients"] = sysdep.RunResult{Stdout: []byte("/dev/pts/0\n")}
	w.Refresh(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the budget is already spent
	h := NewHandler(w.paths, w, quietLog(), clk.now)
	res, err := h.Sample(ctx)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	sig := res.GetSignals()
	if sig.GetSshSessions() != 1 || sig.GetTmuxClients() != 1 {
		t.Errorf("ssh_sessions %d, tmux_clients %d past the budget, want 1 and 1", sig.GetSshSessions(), sig.GetTmuxClients())
	}
	if len(res.GetProcs()) == 0 {
		t.Error("no process samples past the budget")
	}
	if !res.GetPartial() {
		t.Error("a sample past its budget is partial")
	}
}

func TestSampleIsUnder20Milliseconds(t *testing.T) {
	// A guest of a few hundred processes, which is a busy one.
	var procs []fakeProc
	for i := 0; i < 300; i++ {
		procs = append(procs, fakeProc{pid: 1000 + i, ppid: 1, comm: fmt.Sprintf("proc%03d", i), ticks: uint64(i)})
	}
	w, run, _, _, _ := newWatcherFixture(t, procs)
	run.Match["list-windows"] = tmuxOutput()
	ctx := context.Background()
	w.Refresh(ctx)

	h := NewHandler(w.paths, w, quietLog(), time.Now)
	if _, err := h.Sample(ctx); err != nil { // prime the CPU deltas
		t.Fatalf("sample: %v", err)
	}

	const runs = 20
	start := time.Now()
	for i := 0; i < runs; i++ {
		if _, err := h.Sample(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	avg := time.Since(start) / runs
	t.Logf("sample of %d processes took %v on average", len(procs), avg)
	// The budget is 20 ms. CI runs every test under -race, which slows this
	// code 5 to 10 times: it failed there four times in two days at about
	// 35 ms. Under the race detector the check is 5x looser, still a guard
	// against a sample that got many times slower.
	budget := 20 * time.Millisecond
	if raceEnabled {
		budget *= 5
	}
	if avg > budget {
		t.Fatalf("a sample took %v, over the %v budget (20 ms in docs/workstreams/04-guestd.md, 5x under -race)", avg, budget)
	}
}

func TestSampleCarriesCPUPressure(t *testing.T) {
	w, run, _, clk, _ := newWatcherFixture(t, []fakeProc{{pid: 100, ppid: 1, comm: "node", ticks: 5}})
	run.Match["list-windows"] = tmuxOutput()
	w.Refresh(context.Background())
	h := NewHandler(w.paths, w, quietLog(), clk.now)

	// No PSI in the kernel: the counter is 0 and the sample is not partial.
	res, err := h.Sample(context.Background())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if res.GetCpuPressureUsTotal() != 0 || res.GetPartial() {
		t.Fatalf("without PSI: pressure %d partial %v", res.GetCpuPressureUsTotal(), res.GetPartial())
	}

	p := w.paths.CPUPressure()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("some avg10=1.00 avg60=0.50 avg300=0.10 total=1234567\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = h.Sample(context.Background())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if got := res.GetCpuPressureUsTotal(); got != 1234567 {
		t.Fatalf("cpu_pressure_us_total = %d, want 1234567", got)
	}
}

func TestSampleCarriesGuestMemoryUsed(t *testing.T) {
	w, run, _, clk, _ := newWatcherFixture(t, []fakeProc{{pid: 100, ppid: 1, comm: "node", ticks: 5}})
	run.Match["list-windows"] = tmuxOutput()
	w.Refresh(context.Background())
	h := NewHandler(w.paths, w, quietLog(), clk.now)
	if err := os.WriteFile(w.paths.MemInfo(), []byte("MemTotal:        8000000 kB\nMemFree:          100000 kB\nMemAvailable:    6000000 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := h.Sample(context.Background())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if got, want := res.GetMemUsedBytes(), uint64(2000000)<<10; got != want {
		t.Fatalf("mem_used_bytes = %d, want %d", got, want)
	}
}

func TestMemUsed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "meminfo")
	for in, want := range map[string]uint64{
		"MemTotal: 100 kB\nMemAvailable: 40 kB\n": 60 << 10,
		"MemTotal: 100 kB\n":                      0,
		"MemTotal: 10 kB\nMemAvailable: 40 kB\n":  0,
		"":                                        0,
	} {
		if err := os.WriteFile(p, []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := memUsed(p); got != want {
			t.Errorf("memUsed(%q) = %d, want %d", in, got, want)
		}
	}
	if got := memUsed(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Errorf("missing file gave %d", got)
	}
}
