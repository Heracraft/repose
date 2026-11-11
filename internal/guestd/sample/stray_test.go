package sample

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// strayProc is one process of a fixture /proc, with what the stray rule
// reads beyond the fields writeProc covers: its cgroup, its start time,
// the ports it listens on and the ports a client is connected to.
type strayProc struct {
	pid, ppid int
	comm      string
	uid       int
	start     uint64
	ticks     uint64
	cgroup    string
	listen    []int
	peers     []int
}

// paneScope is a tmux pane's cgroup, the ordinary home of a browser a
// script in a pane started.
func paneScope(uid int) string {
	return fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/app.slice/tmux-spawn-4e1.scope", uid, uid)
}

func writeStrayProcs(t *testing.T, p sysdep.Paths, procs []strayProc) {
	t.Helper()
	// The table every process in this namespace sees.
	var rows []string
	for _, pr := range procs {
		for i, port := range pr.listen {
			rows = append(rows, netRow(port, tcpListen, pr.pid*100+i))
		}
		for _, port := range pr.peers {
			rows = append(rows, netRow(port, tcpEstablished, 0))
		}
	}
	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		strings.Join(rows, "\n") + "\n"

	for _, pr := range procs {
		writeProc(t, p, []fakeProc{{pid: pr.pid, ppid: pr.ppid, comm: pr.comm, uid: pr.uid, ticks: pr.ticks}})
		dir := p.ProcPID(fmt.Sprint(pr.pid))
		// writeProc leaves every other stat field at 0; field 22 is the
		// start time, which is half of a process's identity here.
		b, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(b))
		fields[21] = fmt.Sprint(pr.start) // field 22, 1-indexed
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(strings.Join(fields, " ")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if pr.cgroup != "" {
			if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::"+pr.cgroup+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		fdDir := filepath.Join(dir, "fd")
		if err := os.RemoveAll(fdDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(fdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// A descriptor that is not a socket: the rule must drop it
		// without keeping the path (DECISIONS R5-3).
		if err := os.Symlink("/home/dev/todo-app/.env", filepath.Join(fdDir, "0")); err != nil {
			t.Fatal(err)
		}
		for i := range pr.listen {
			link := fmt.Sprintf("socket:[%d]", pr.pid*100+i)
			if err := os.Symlink(link, filepath.Join(fdDir, fmt.Sprint(i+3))); err != nil {
				t.Fatal(err)
			}
		}
		netDir := filepath.Join(dir, "net")
		if err := os.MkdirAll(netDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(table), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func netRow(port int, state string, inode int) string {
	return fmt.Sprintf("   0: 0100007F:%04X 00000000:0000 %s 00000000:00000000 00:00000000 00000000  1000        0 %d 1 0000000000000000 100 0 0 10 0",
		port, state, inode)
}

// strayBase is a quiet machine with one orphaned headless browser: a
// Playwright shell whose `node` launcher exited, in the pane's scope,
// listening on a DevTools port nobody is connected to.
func strayBase(uid int) []strayProc {
	return []strayProc{
		{pid: 1, ppid: 0, comm: "systemd", uid: 0, start: 10},
		{pid: 500, ppid: 1, comm: "systemd", uid: uid, start: 50, cgroup: fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/init.scope", uid, uid)},
		{pid: 900, ppid: 500, comm: "tmux: server", uid: uid, start: 90, cgroup: fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/app.slice/tmux-spawn-000.scope", uid, uid)},
		{pid: 4182, ppid: 500, comm: "chrome-headless", uid: uid, start: 4182, ticks: 221400, cgroup: paneScope(uid), listen: []int{9333}},
	}
}

// newStrayFixture writes a /proc and returns a watcher over it whose stop
// is recorded rather than sent.
func newStrayFixture(t *testing.T, procs []strayProc) (*strayWatcher, *clock, *[]int) {
	t.Helper()
	p := sysdep.Paths{Root: t.TempDir()}
	if err := os.MkdirAll(p.Proc(), 0o755); err != nil {
		t.Fatal(err)
	}
	// /proc/uptime, for the "running N seconds" of the log line.
	if err := os.WriteFile(p.Uptime(), []byte("20000.00 40000.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStrayProcs(t, p, procs)
	clk := &clock{t: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	s := newStrayWatcher(p, newProcReader(p), clk.now)
	var stopped []int
	s.stop = func(pid int, _ uint64) error {
		stopped = append(stopped, pid)
		return nil
	}
	return s, clk, &stopped
}

// drive runs n passes a minute apart.
func drive(s *strayWatcher, clk *clock, uid, manager, n int, quiet bool) {
	for i := 0; i < n; i++ {
		s.run(uid, manager, quiet)
		clk.advance(StrayInterval)
	}
}

// DECISIONS I-1124: ten quiet minutes with the launcher gone and nobody
// connected, and the browser is stopped -- not before.
func TestStrayBrowserStopsAnOrphanOnAQuietMachine(t *testing.T) {
	uid, _ := sysdep.DevIdentity()
	s, clk, stopped := newStrayFixture(t, strayBase(uid))

	drive(s, clk, uid, 500, StrayChecks-1, true)
	if len(*stopped) != 0 {
		t.Fatalf("stopped after %d checks: %v, want nothing before %d", StrayChecks-1, *stopped, StrayChecks)
	}
	drive(s, clk, uid, 500, 1, true)
	if len(*stopped) != 1 || (*stopped)[0] != 4182 {
		t.Fatalf("stopped = %v, want [4182] at the tenth check", *stopped)
	}
}

// A pass inside StrayInterval of the last one does not count: ten checks
// are ten minutes, whatever the watcher's own refresh interval is.
func TestStrayBrowserCountsOnePassAMinute(t *testing.T) {
	uid, _ := sysdep.DevIdentity()
	s, clk, stopped := newStrayFixture(t, strayBase(uid))
	for i := 0; i < StrayChecks*12; i++ {
		s.run(uid, 500, true)
		clk.advance(5 * time.Second) // the watcher's Interval
	}
	if len(*stopped) != 1 {
		t.Fatalf("stopped = %v over %v, want one stop", *stopped, time.Duration(StrayChecks*12)*5*time.Second)
	}
}

// Each of the five conditions, failing on its own, keeps the browser.
func TestStrayBrowserKeepsEverythingElse(t *testing.T) {
	uid, _ := sysdep.DevIdentity()
	manager := 500
	cases := []struct {
		name  string
		quiet bool
		procs func(uid int) []strayProc
	}{
		{
			// 1. Not a browser at all.
			name: "a node process of the same shape", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].comm = "node"
				return p
			},
		},
		{
			// 1. Not the top of a browser tree: a renderer goes with
			// the browser that forked it.
			name: "a renderer of a live browser", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].ppid = 4100
				return append(p, strayProc{pid: 4100, ppid: 900, comm: "chromium", uid: uid, start: 4100, cgroup: paneScope(uid)})
			},
		},
		{
			// 2. A unit of its own: how a person runs a browser on
			// purpose (systemd-run --user --unit NAME).
			name: "a browser in a unit of its own", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].cgroup = fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/app.slice/keep.service", uid, uid)
				return p
			},
		},
		{
			// 2. The agents' own browser, a system unit.
			name: "the agents' shared browser", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].comm = "chromium"
				p[3].cgroup = "/system.slice/repose-browser.slice/repose-browser.service"
				return p
			},
		},
		{
			// 2. A container runs outside dev's slice.
			name: "a browser in a container", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].cgroup = "/system.slice/docker-6c1d.scope"
				return p
			},
		},
		{
			// 3. The launcher is still running: a playwright test run,
			// a Puppeteer script waiting on a slow page.
			name: "a browser whose launcher still runs", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].ppid = 4000
				return append(p, strayProc{pid: 4000, ppid: 900, comm: "node", uid: uid, start: 4000, cgroup: paneScope(uid)})
			},
		},
		{
			// 4. Somebody is connected to its DevTools port.
			name: "a browser with a client", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].peers = []int{9333}
				return p
			},
		},
		{
			// 5. The machine is not quiet.
			name: "a quiet browser on a machine someone is on", quiet: false,
			procs: strayBase,
		},
		{
			// The agents' own browser. Its unit keeps it out of dev's
			// slice only while Chromium cannot reach dev's user bus;
			// the port it serves DevTools on cannot move.
			name: "the agents' browser after Chromium moved it to its own scope", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].comm = "chromium"
				p[3].ppid = 1
				p[3].listen = []int{browserBackendPort}
				p[3].cgroup = fmt.Sprintf(
					"/user.slice/user-%d.slice/user@%d.service/app.slice/app-org.chromium.Chromium-4182.scope", uid, uid)
				return p
			},
		},
		{
			name: "a browser of another user", quiet: true,
			procs: func(uid int) []strayProc {
				p := strayBase(uid)
				p[3].uid = 0
				return p
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, clk, stopped := newStrayFixture(t, c.procs(uid))
			drive(s, clk, uid, manager, StrayChecks*2, c.quiet)
			if len(*stopped) != 0 {
				t.Fatalf("stopped %v after %d checks; this browser must be left alone", *stopped, StrayChecks*2)
			}
		})
	}
}

// A minute with somebody on the machine starts the ten over.
func TestStrayBrowserCountStartsOverWhenSomebodyIsThere(t *testing.T) {
	uid, _ := sysdep.DevIdentity()
	s, clk, stopped := newStrayFixture(t, strayBase(uid))
	drive(s, clk, uid, 500, StrayChecks-1, true)
	drive(s, clk, uid, 500, 1, false) // an ssh session, a tmux client, an agent at work
	drive(s, clk, uid, 500, StrayChecks-1, true)
	if len(*stopped) != 0 {
		t.Fatalf("stopped = %v; the count must start over after a minute of use", *stopped)
	}
	drive(s, clk, uid, 500, 1, true)
	if len(*stopped) != 1 {
		t.Fatalf("stopped = %v, want one stop ten quiet checks after the interruption", *stopped)
	}
}

// A pid the kernel handed out again is a different process: the count
// starts over rather than the new process being signalled.
func TestStrayBrowserDoesNotSignalAReusedPID(t *testing.T) {
	uid, _ := sysdep.DevIdentity()
	procs := strayBase(uid)
	s, clk, stopped := newStrayFixture(t, procs)
	drive(s, clk, uid, 500, StrayChecks-1, true)

	// The same pid, a later start time: another process entirely.
	reused := procs
	reused[3].start = 99999
	writeStrayProcs(t, s.paths, reused)
	drive(s, clk, uid, 500, 1, true)
	if len(*stopped) != 0 {
		t.Fatalf("stopped = %v; a reused pid must start the count over", *stopped)
	}
	drive(s, clk, uid, 500, StrayChecks-1, true)
	if len(*stopped) != 1 {
		t.Fatalf("stopped = %v, want the new process stopped on its own ten checks", *stopped)
	}
}

// The real signal path: a pidfd, the start time checked again, SIGTERM.
// A start time that has moved on is a reused pid and nothing is sent.
func TestStopStrayChecksTheStartTimeAgain(t *testing.T) {
	if _, err := unix.PidfdOpen(os.Getpid(), 0); err != nil {
		t.Skipf("pidfd_open: %v", err)
	}
	s := newStrayWatcher(sysdep.Paths{}, newProcReader(sysdep.Paths{}), time.Now)
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	pid := cmd.Process.Pid
	start, ok := s.startTime(pid)
	if !ok {
		t.Fatalf("no start time for pid %d", pid)
	}

	if err := s.stop(pid, start+1); err != nil {
		t.Fatalf("a wrong start time must be refused quietly, got %v", err)
	}
	if err := unix.Kill(pid, 0); err != nil {
		t.Fatalf("the process was signalled although its start time did not match: %v", err)
	}

	if err := s.stop(pid, start); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("the stray exited 0, want a signal")
	}
}

// The guest-side terms of the rest rule, as the stray rule reads them
// (internal/api/idle.UsedSQL, negated).
func TestStrayQuietFollowsTheRestRule(t *testing.T) {
	p := sysdep.Paths{Root: t.TempDir()}
	w := NewWatcher(p, sysdep.NewFakeRunner(), &sysdep.FakeDocker{Up: true}, fixedSlug("todo-app"), &recorder{}, quietLog(), nil)
	if !w.strayQuiet(0) {
		t.Error("a machine with nothing on it reads as busy")
	}
	if w.strayQuiet(1) {
		t.Error("an ssh session must count as use")
	}
	w.clients = 1
	if w.strayQuiet(0) {
		t.Error("a tmux client must count as use")
	}
	w.clients = 0
	for _, st := range []string{StateIdle, StateNeedsInput} {
		w.windows["claude"] = &windowState{agent: "claude", state: st}
		if !w.strayQuiet(0) || w.agentWorking() {
			t.Errorf("an agent in %q must not count as work; it does not for the freeze either", st)
		}
	}
	for _, st := range []string{StateWorking, StateRunning, StateUnknown} {
		w.windows["claude"] = &windowState{agent: "claude", state: st}
		if w.strayQuiet(0) || !w.agentWorking() {
			t.Errorf("an agent in %q must count as work", st)
		}
	}
}

// The stamp the desktop's idle check reads: made under the desktop's run
// directory, with the time of the refresh that saw an agent working.
func TestStampWorkingWritesTheDesktopStamp(t *testing.T) {
	p := sysdep.Paths{Root: t.TempDir()}
	at := time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC)
	if err := stampWorking(p, at); err != nil {
		t.Fatalf("stampWorking: %v", err)
	}
	path := filepath.Join(p.RunDir(), "desktop", workingStamp)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !fi.ModTime().Equal(at) {
		t.Fatalf("mtime = %v, want %v", fi.ModTime(), at)
	}
	later := at.Add(7 * time.Minute)
	if err := stampWorking(p, later); err != nil {
		t.Fatalf("second stampWorking: %v", err)
	}
	fi, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(later) {
		t.Fatalf("mtime = %v, want the second write at %v", fi.ModTime(), later)
	}
}
