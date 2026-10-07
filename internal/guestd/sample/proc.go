package sample

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// TopProcs is how many process names are reported by CPU, before the watch
// list is added (docs/workstreams/04-guestd.md).
const TopProcs = 50

// clockTicks is the kernel's USER_HZ. It is 100 on every Linux architecture
// the guest runs on; /proc/<pid>/stat reports CPU in these units.
const clockTicks = 100

// pageSize converts the RSS field of /proc/<pid>/stat, which is in pages.
var pageSize = uint64(os.Getpagesize())

// procReader walks /proc and aggregates per process name. It never opens
// cmdline or environ: comm and stat are the whole contract
// (DECISIONS R5-3), and a test asserts it under strace.
type procReader struct {
	paths sysdep.Paths

	mu   sync.Mutex
	prev map[string]uint64 // comm -> cumulative CPU nanoseconds
	// setNice sets one thread's nice value (tests replace it).
	setNice func(tid, nice int) error
}

func newProcReader(p sysdep.Paths) *procReader {
	return &procReader{paths: p, prev: map[string]uint64{}, setNice: setPriority}
}

type procTotals struct {
	cpuNS uint64
	rss   uint64
}

// read returns one ProcSample per process name and the guest user's ssh
// session count, from a single walk of /proc. They were two walks once; in a
// guest that cost more than the whole 20 ms sampling budget.
func (r *procReader) read(devUID int) ([]*hostdv1.ProcSample, uint32, error) {
	totals, sessions, err := r.scan(devUID)
	if err != nil {
		return nil, 0, err
	}

	r.mu.Lock()
	out := make([]*hostdv1.ProcSample, 0, len(totals))
	for comm, t := range totals {
		var delta uint64
		if before, ok := r.prev[comm]; ok && t.cpuNS >= before {
			delta = t.cpuNS - before
		}
		out = append(out, &hostdv1.ProcSample{Comm: comm, CpuNsDelta: delta, RssBytes: t.rss})
	}
	next := make(map[string]uint64, len(totals))
	for comm, t := range totals {
		next[comm] = t.cpuNS
	}
	r.prev = next
	r.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].GetCpuNsDelta() != out[j].GetCpuNsDelta() {
			return out[i].GetCpuNsDelta() > out[j].GetCpuNsDelta()
		}
		return out[i].GetComm() < out[j].GetComm()
	})
	return trim(out), sessions, nil
}

// trim keeps the top slice by CPU plus every watched name below it.
func trim(in []*hostdv1.ProcSample) []*hostdv1.ProcSample {
	if len(in) <= TopProcs {
		return in
	}
	out := in[:TopProcs:TopProcs]
	for _, p := range in[TopProcs:] {
		if Watched(p.GetComm()) {
			out = append(out, p)
		}
	}
	return out
}

// scan reads every /proc/<pid>/stat once, and /proc/<pid>/status only for the
// processes that could be an ssh session. It opens no other file of a process:
// never cmdline, never environ (DECISIONS R5-3).
func (r *procReader) scan(devUID int) (map[string]procTotals, uint32, error) {
	entries, err := os.ReadDir(r.paths.Proc())
	if err != nil {
		return nil, 0, sysdep.Errf(sysdep.CodeInternal, "read /proc: %w", err)
	}
	totals := make(map[string]procTotals, 128)
	var sessions uint32
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		comm, cpu, rss, ok := r.readStat(e.Name())
		if !ok {
			continue // the process exited between readdir and open; normal
		}
		t := totals[comm]
		t.cpuNS += cpu
		t.rss += rss
		totals[comm] = t

		// OpenSSH names the per-session process "sshd: dev@pts/0", but that
		// title lives in cmdline, which guestd never opens. The same
		// processes are identified by comm plus the dev uid from status.
		if comm == "sshd" || comm == "sshd-session" {
			if procUID(filepath.Join(r.paths.ProcPID(e.Name()), "status")) == devUID {
				sessions++
			}
		}
	}
	return totals, sessions, nil
}

func isPID(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return true
}

// readStat parses the fields of /proc/<pid>/stat that a sample needs: comm
// (2), utime (14), stime (15), rss (24). Nothing else in the file is read and
// no other file of the process is opened.
func (r *procReader) readStat(pid string) (comm string, cpuNS, rss uint64, ok bool) {
	b, err := os.ReadFile(filepath.Join(r.paths.ProcPID(pid), "stat"))
	if err != nil {
		return "", 0, 0, false
	}
	nameStart := bytes.IndexByte(b, '(')
	nameEnd := bytes.LastIndexByte(b, ')')
	if nameStart < 0 || nameEnd < 0 || nameEnd < nameStart {
		return "", 0, 0, false
	}
	comm = string(b[nameStart+1 : nameEnd])

	// Field 3 (state) starts after "') '"; utime is field 14, so it is the
	// 11th whitespace-separated token of the remainder.
	rest := bytes.Fields(b[nameEnd+1:])
	const (
		utimeIdx = 11
		stimeIdx = 12
		rssIdx   = 21
	)
	if len(rest) <= rssIdx {
		return "", 0, 0, false
	}
	utime, err1 := strconv.ParseUint(string(rest[utimeIdx]), 10, 64)
	stime, err2 := strconv.ParseUint(string(rest[stimeIdx]), 10, 64)
	rssPages, err3 := strconv.ParseUint(string(rest[rssIdx]), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return "", 0, 0, false
	}
	cpuNS = (utime + stime) * (1_000_000_000 / clockTicks)
	return comm, cpuNS, rssPages * pageSize, true
}

// treeCPU sums the CPU of a process and its descendants, in nanoseconds. It is
// how an agent window is judged to be working: the agent's own process is
// often idle while a child compiler is not.
func (r *procReader) treeCPU(children map[int][]int, rootPID int) (uint64, bool) {
	if children == nil {
		return 0, false
	}
	var total uint64
	var seen = map[int]bool{}
	stack := []int{rootPID}
	found := false
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if _, cpu, _, ok := r.readStat(strconv.Itoa(pid)); ok {
			total += cpu
			found = true
		}
		stack = append(stack, children[pid]...)
	}
	return total, found
}

// childIndex builds pid -> children from the ppid field of every stat file.
// The watcher builds it once per refresh and hands it to every tree walk;
// building it per window was a full /proc walk per agent.
func (r *procReader) childIndex() (map[int][]int, bool) {
	entries, err := os.ReadDir(r.paths.Proc())
	if err != nil {
		return nil, false
	}
	index := make(map[int][]int, 128)
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.paths.ProcPID(e.Name()), "stat"))
		if err != nil {
			continue
		}
		nameEnd := bytes.LastIndexByte(b, ')')
		if nameEnd < 0 {
			continue
		}
		rest := bytes.Fields(b[nameEnd+1:])
		if len(rest) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(string(rest[1]))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		index[ppid] = append(index[ppid], pid)
	}
	return index, true
}

// treeHasComm reports whether the process tree rooted at rootPID contains a
// process whose name is comm. It is what makes a window named "claude" an
// agent window only when claude is actually running in it.
// treeHasAnyComm is treeHasComm over the names an agent may run as.
func (r *procReader) treeHasAnyComm(children map[int][]int, rootPID int, wants []string) bool {
	for _, w := range wants {
		if r.treeHasComm(children, rootPID, w) {
			return true
		}
	}
	return false
}

func (r *procReader) treeHasComm(children map[int][]int, rootPID int, want string) bool {
	if children == nil {
		return false
	}
	seen := map[int]bool{}
	stack := []int{rootPID}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if comm, _, _, ok := r.readStat(strconv.Itoa(pid)); ok && matchesBinary([]string{want}, comm) {
			return true
		}
		// comm is the thread name, which a runtime may rename: node's main
		// thread is "MainThread", so Gemini CLI's process never read as
		// `node` and its window was never an agent's (I-125). The
		// executable's name is read from the exe link, never from cmdline
		// or environ (docs/SECURITY.md).
		if matchesBinary([]string{want}, r.readExeBase(pid)) {
			return true
		}
		stack = append(stack, children[pid]...)
	}
	return false
}

// readExeBase is the basename of /proc/<pid>/exe, or "".
func (r *procReader) readExeBase(pid int) string {
	target, err := os.Readlink(filepath.Join(r.paths.ProcPID(strconv.Itoa(pid)), "exe"))
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSuffix(target, " (deleted)"))
}

// procUID reads the real uid from /proc/<pid>/status, or -1.
func procUID(statusPath string) int {
	b, err := os.ReadFile(statusPath)
	if err != nil {
		return -1
	}
	for _, line := range bytes.Split(b, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("Uid:")) {
			continue
		}
		fields := bytes.Fields(line[4:])
		if len(fields) == 0 {
			return -1
		}
		uid, err := strconv.Atoi(string(fields[0]))
		if err != nil {
			return -1
		}
		return uid
	}
	return -1
}
