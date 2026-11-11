package sample

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// The two browser rules of DECISIONS I-1122 and I-1124, kept in one file
// because both hang off the watcher's existing /proc pass.
//
//   - last-agent-working: the stamp the desktop's idle check reads to tell
//     the agents' browser's two thresholds apart (I-1122). guestd is the
//     only thing on the machine that knows an agent's state.
//   - stray browsers: a headless browser whose launching process has
//     exited, that nobody is connected to, on a machine that is otherwise
//     quiet, is stopped (I-1124, which amends I-200 for this one case).
//
// Only process names, stat fields, the uid, the cgroup path and socket
// inodes are read here, never a command line and never the path an open
// file descriptor names (DECISIONS R5-3).

// workingStamp is the file under the desktop's run directory whose mtime
// is the last refresh that saw an agent working.
const workingStamp = "last-agent-working"

const (
	// StrayInterval is how often the stray pass runs. The watcher
	// refreshes every Interval; the pass is a /proc walk of its own and
	// the rule is written in minutes, so it runs at most this often.
	StrayInterval = time.Minute
	// StrayChecks is how many passes in a row a process must be a stray,
	// on a quiet machine, before it is stopped: ten minutes.
	StrayChecks = 10
	// StrayTermGrace is how long a stray has to exit on SIGTERM before it
	// is killed.
	StrayTermGrace = 10 * time.Second
)

// strayComms are the names a browser process gives itself. The kernel cuts
// comm at 15 characters, which is what turns Playwright's
// `chrome-headless-shell` into `chrome-headless`.
var strayComms = map[string]bool{
	"chrome-headless": true,
	"chromium":        true,
	"chrome":          true,
	"headless_shell":  true,
}

// strayCandidate is one process that met every condition at the last pass.
type strayCandidate struct {
	// startTime is field 22 of the process's stat, which together with
	// the pid identifies the process: a pid the kernel hands out again
	// has another start time, so a reused pid is never signalled.
	startTime uint64
	comm      string
	// checks is how many passes in a row it has been a stray.
	checks int
	seen   bool
}

// strayWatcher holds the per-pass state of the stray rule. Refresh is
// driven both by the watcher's ticker and by a caller of its own (the
// server's first refresh, and tests), so the pass takes a lock of its
// own rather than assuming one goroutine.
type strayWatcher struct {
	paths sysdep.Paths
	procs *procReader
	now   func() time.Time

	mu      sync.Mutex
	lastRun time.Time
	seen    map[int]*strayCandidate
	// stop ends a stray. Tests replace it; the real one is stopStray.
	stop func(pid int, startTime uint64) error
}

func newStrayWatcher(p sysdep.Paths, procs *procReader, now func() time.Time) *strayWatcher {
	s := &strayWatcher{paths: p, procs: procs, now: now, seen: map[int]*strayCandidate{}}
	s.stop = s.stopStray
	return s
}

// strayStopped is one browser the pass ended, for the log line.
type strayStopped struct {
	PID   int
	Comm  string
	RanS  int64
	CPUS  int64
	Error error
}

// quiet reports whether the machine's guest-side terms of the rest rule
// hold: no SSH session, no tmux client and every agent idle or waiting on
// the user (internal/api/idle.UsedSQL, negated, less the CPU term, which
// is what a stray is the cause of). A herdr client arrives over SSH and is
// in the SSH count, so "no multiplexer client" needs no third term.
func (w *Watcher) strayQuiet(sshSessions uint32) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if sshSessions > 0 || w.clients > 0 {
		return false
	}
	for _, ws := range w.windows {
		if ws.state != StateIdle && ws.state != StateNeedsInput {
			return false
		}
	}
	return true
}

// agentWorking reports whether any agent window is in a state the freeze
// counts as work: anything but idle and needs_input.
func (w *Watcher) agentWorking() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ws := range w.windows {
		if ws.state != StateIdle && ws.state != StateNeedsInput {
			return true
		}
	}
	return false
}

// refreshBrowsers is the watcher's hook into both rules. It runs on every
// refresh; the stray pass gates itself to StrayInterval.
func (w *Watcher) refreshBrowsers(devUID int) {
	if w.agentWorking() {
		if err := stampWorking(w.paths, w.now()); err != nil {
			w.log.Debug("could not stamp the agent-working time",
				"event", "desktop_idle", "reason", "write")
		}
	}
	w.mu.Lock()
	if w.strays == nil {
		w.strays = newStrayWatcher(w.paths, w.procs, w.now)
	}
	strays := w.strays
	w.mu.Unlock()
	sessions, manager := w.procs.sshSessionsAndManager(devUID)
	for _, s := range strays.run(devUID, manager, w.strayQuiet(sessions)) {
		if s.Error != nil {
			w.log.Debug("could not stop a stray browser",
				"event", "stray_browser", "comm", s.Comm, "pid", s.PID)
			continue
		}
		// Names and numbers only: no URL, no window title, no argument
		// (docs/ops/OBSERVABILITY.md, DECISIONS R5-3).
		w.log.Info("stopped a stray browser, whose launcher had exited",
			"event", "stray_browser", "comm", s.Comm, "pid", s.PID,
			"ran_s", s.RanS, "cpu_s", s.CPUS)
	}
}

// stampWorking writes the mtime the desktop's idle check reads. guestd is
// root and the desktop's units run as dev, so the stamp is left dev's,
// which is the invariant I-727 protects; the shell writers go through
// repose-desktop-stamp for the same reason.
func stampWorking(p sysdep.Paths, now time.Time) error {
	dir := filepath.Join(p.RunDir(), "desktop")
	path := filepath.Join(dir, workingStamp)
	uid, gid := sysdep.DevIdentity()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(path, now, now); err != nil {
		return err
	}
	// Best effort: a guest whose dev user this process cannot chown to
	// still gets the stamp, and the idle check only reads its mtime.
	_ = os.Chown(path, uid, gid)
	return nil
}

// run does one pass. quiet is the machine's state this minute; manager is
// dev's user manager pid, 0 when there is none. It returns what it
// stopped.
func (s *strayWatcher) run(devUID, manager int, quiet bool) []strayStopped {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !s.lastRun.IsZero() && now.Sub(s.lastRun) < StrayInterval {
		return nil
	}
	s.lastRun = now
	if !quiet {
		// Somebody is here. The count starts again from zero, so ten
		// quiet minutes means ten in a row (DECISIONS I-1124).
		s.seen = map[int]*strayCandidate{}
		return nil
	}
	for _, c := range s.seen {
		c.seen = false
	}
	var out []strayStopped
	for _, pid := range s.candidates(devUID, manager) {
		st, ok := s.startTime(pid)
		if !ok {
			continue
		}
		comm, cpuNS, _, ok := s.procs.readStat(strconv.Itoa(pid))
		if !ok {
			continue
		}
		c := s.seen[pid]
		if c == nil || c.startTime != st {
			c = &strayCandidate{startTime: st, comm: comm}
			s.seen[pid] = c
		}
		c.seen = true
		c.checks++
		if c.checks < StrayChecks {
			continue
		}
		delete(s.seen, pid)
		res := strayStopped{
			PID: pid, Comm: comm,
			RanS: s.ranFor(st),
			CPUS: int64(cpuNS / 1_000_000_000),
		}
		res.Error = s.stop(pid, st)
		out = append(out, res)
	}
	for pid, c := range s.seen {
		if !c.seen {
			delete(s.seen, pid)
		}
	}
	return out
}

// candidates returns the pids that meet conditions 1 to 4 of I-1124.
func (s *strayWatcher) candidates(devUID, manager int) []int {
	entries, err := os.ReadDir(s.paths.Proc())
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		comm, ppid, ok := s.procs.commAndParent(e.Name())
		if !ok || !strayComms[comm] {
			continue
		}
		if procUID(filepath.Join(s.paths.ProcPID(e.Name()), "status")) != devUID {
			continue
		}
		// 1. The top of a browser tree: a renderer goes with its parent.
		if pcomm, _, ok := s.procs.commAndParent(strconv.Itoa(ppid)); ok && strayComms[pcomm] {
			continue
		}
		// 3. The launcher has exited, so the kernel moved the process to
		// pid 1 or to dev's user manager.
		if ppid != 1 && (manager == 0 || ppid != manager) {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		// 2. A pane's or a session's scope, or herdr's unit, whose panes
		// share it. Any other .service is a browser someone ran on
		// purpose, and a container is outside dev's slice altogether.
		if !strayCgroup(s.readCgroup(pid), devUID) {
			continue
		}
		// 4. Nobody is connected to it.
		listening, connected := s.sockets(pid)
		if connected {
			continue
		}
		// The agents' own browser, whose rule is I-1122's and not this
		// one. It is the system unit repose-browser.service, so condition
		// 2 ought to answer for it -- and it does only while Chromium
		// cannot reach dev's user bus, because a Chromium that can moves
		// itself into app-org.chromium.Chromium-<pid>.scope under dev's
		// slice, measured in the guest-stray-browser VM test. The port it
		// serves DevTools on is a convention of ours
		// (docs/interfaces/guest-conventions.md) and it cannot move.
		if listening[browserBackendPort] {
			continue
		}
		out = append(out, pid)
	}
	return out
}

// strayCgroup reports whether a cgroup path is one of dev's panes or
// sessions. The path is the cgroup v2 line of /proc/<pid>/cgroup, with its
// "0::" prefix removed.
func strayCgroup(path string, devUID int) bool {
	if path == "" {
		return false
	}
	if !strings.Contains(path+"/", "/user-"+strconv.Itoa(devUID)+".slice/") {
		return false
	}
	if strings.HasSuffix(path, ".scope") {
		return true
	}
	// herdr's panes have no scope of their own: they run in the server's
	// unit, so a browser started in one is as unmanaged as one in a tmux
	// pane (DECISIONS I-1124).
	return strings.HasSuffix(path, "/repose-herdr-server.service")
}

// readCgroup reads the cgroup v2 path of a process, "" when it has none.
func (s *strayWatcher) readCgroup(pid int) string {
	b, err := os.ReadFile(filepath.Join(s.paths.ProcPID(strconv.Itoa(pid)), "cgroup"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// browserBackendPort is the DevTools port the agents' shared browser
// serves (nix/guest/base/browser.nix, guest-conventions.md "Desktop").
const browserBackendPort = 9225

// sockets returns the TCP ports a process listens on and whether a
// connection is established to one of them. A browser launched with a
// pipe listens on nothing and reads as unconnected, which is what makes
// an orphan with no DevTools port a stray.
//
// The process's own outgoing connections do not count: a page fetching
// the internet is the stray at work, not somebody using it.
func (s *strayWatcher) sockets(pid int) (map[int]bool, bool) {
	inodes := s.socketInodes(pid)
	if len(inodes) == 0 {
		return nil, false
	}
	listening := map[int]bool{}
	var established []int
	for _, name := range []string{"tcp", "tcp6"} {
		for _, r := range s.readNetTCP(pid, name) {
			port, err := strconv.ParseUint(r.localPort, 16, 32)
			if err != nil {
				continue
			}
			switch r.state {
			case tcpListen:
				if inodes[r.inode] {
					listening[int(port)] = true
				}
			case tcpEstablished:
				established = append(established, int(port))
			}
		}
	}
	for _, port := range established {
		if listening[port] {
			return listening, true
		}
	}
	return listening, false
}

// socketInodes is the set of socket inodes a process holds. The file
// descriptor links are read and everything that is not a socket is
// dropped without being looked at, so no path inside the guest is kept
// (DECISIONS R5-3).
func (s *strayWatcher) socketInodes(pid int) map[string]bool {
	dir := filepath.Join(s.paths.ProcPID(strconv.Itoa(pid)), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		ino, ok := strings.CutPrefix(target, "socket:[")
		if !ok {
			continue
		}
		out[strings.TrimSuffix(ino, "]")] = true
	}
	return out
}

// The two /proc/net/tcp states this rule reads, as that file writes them.
const (
	tcpEstablished = "01"
	tcpListen      = "0A"
)

type netTCPRow struct {
	localPort string
	state     string
	inode     string
}

// readNetTCP parses /proc/<pid>/net/<name>: the process's own network
// namespace, so a browser in a namespace of its own is read there and not
// against the guest's.
func (s *strayWatcher) readNetTCP(pid int, name string) []netTCPRow {
	f, err := os.Open(filepath.Join(s.paths.ProcPID(strconv.Itoa(pid)), "net", name))
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // read only
	var out []netTCPRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// sl local_address rem_address st tx:rx tr:when retrnsmt uid
		// timeout inode
		if len(fields) < 10 || !strings.HasSuffix(fields[0], ":") {
			continue // the header
		}
		_, port, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		out = append(out, netTCPRow{localPort: port, state: fields[3], inode: fields[9]})
	}
	return out
}

// startTime is field 22 of a process's stat, in clock ticks since boot.
func (s *strayWatcher) startTime(pid int) (uint64, bool) {
	b, err := os.ReadFile(filepath.Join(s.paths.ProcPID(strconv.Itoa(pid)), "stat"))
	if err != nil {
		return 0, false
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return 0, false
	}
	rest := strings.Fields(string(b)[end+1:])
	const startIdx = 19 // field 22; rest[0] is field 3
	if len(rest) <= startIdx {
		return 0, false
	}
	n, err := strconv.ParseUint(rest[startIdx], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ranFor is how long a process with this start time has run, in seconds.
func (s *strayWatcher) ranFor(startTime uint64) int64 {
	b, err := os.ReadFile(s.paths.Uptime())
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	up, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	ran := int64(up) - int64(startTime)/clockTicks
	if ran < 0 {
		return 0
	}
	return ran
}

// stopStray sends SIGTERM through a pidfd and SIGKILL StrayTermGrace
// later. The pidfd is opened once and refers to that process for as long
// as it is held, so neither signal can reach a pid the kernel handed out
// again; the start time is checked as well, for the window between the
// walk and the open.
func (s *strayWatcher) stopStray(pid int, startTime uint64) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return err
	}
	if st, ok := s.startTime(pid); !ok || st != startTime {
		return unix.Close(fd)
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0); err != nil {
		_ = unix.Close(fd)
		return err
	}
	go func() {
		defer unix.Close(fd) //nolint:errcheck // nothing to do with it
		time.Sleep(StrayTermGrace)
		// ESRCH when it is gone, which is the ordinary outcome.
		_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
	}()
	return nil
}

// sshSessionsAndManager counts dev's SSH sessions and finds dev's user
// manager, in one walk. The count is the same one Sample reports
// (proc.go's scan), read here because the stray rule runs on the
// watcher's refresh and not on a Sample.
func (r *procReader) sshSessionsAndManager(devUID int) (sessions uint32, manager int) {
	entries, err := os.ReadDir(r.paths.Proc())
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		comm, ppid, ok := r.commAndParent(e.Name())
		if !ok {
			continue
		}
		if comm != "sshd" && comm != "sshd-session" && (comm != "systemd" || ppid != 1) {
			continue
		}
		if procUID(filepath.Join(r.paths.ProcPID(e.Name()), "status")) != devUID {
			continue
		}
		if comm == "systemd" {
			manager, _ = strconv.Atoi(e.Name())
			continue
		}
		sessions++
	}
	return sessions, manager
}
