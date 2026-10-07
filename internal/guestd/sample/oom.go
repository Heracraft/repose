package sample

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Agents outlive dev servers under memory pressure (DECISIONS I-200).
// oom_score_adj is inherited on fork, so a value set once on an agent
// would also protect every dev server the agent starts; and the `dev`
// user can raise its own value but never lower it. So guestd (root) owns
// it and re-applies it on every refresh: OOMProtected for the tmux server,
// the keystroke path (I-576: dev's sshd-session, the tmux and herdr
// clients) and each agent window's agent process, OOMUserManager for dev's
// user manager, and 0 for any other process of dev's that inherited a
// negative value, so a vite an agent started, or a command run over SSH,
// is back to the kernel's ordinary choice within one refresh. Nothing is
// ever killed or stopped by guestd; the kernel's OOM killer decides, and
// the existing `oom` warning names what it killed.
//
// Only process names, stat fields, the uid and the exe link's basename are
// read, never a command line (DECISIONS R5-3).

// OOMProtected is the value agents and the tmux server run with: strongly
// preferred to survive, but not -1000 (never killable), so a runaway agent
// can still be stopped by the kernel rather than hang the guest.
const OOMProtected = -800

// OOMUserManager is dev's user manager's value, the one its unit sets
// (nix/guest/base/keystroke-path.nix): when the kernel kills it, systemd
// stops user@1000.service and with it the tmux server and every pane
// (kanali, 2026-10-05, at upstream's 100). guestd writes it too, so a
// manager started before the base that sets it is protected without a
// restart.
const OOMUserManager = -900

// tmuxServerComm is the name tmux's server process gives itself.
const tmuxServerComm = "tmux: server"

// keystrokeComms are the processes between the laptop's keyboard and the
// tmux or herdr server (I-576): the session half of sshd that runs as dev
// (sshd hands its unit's -800 to the session on fork, and its children
// inherit it), the tmux client a `tmux attach` runs, and herdr's client
// and server (one binary).
var keystrokeComms = map[string]bool{
	"sshd-session":  true,
	"tmux: client":  true,
	herdrServerComm: true,
}

// agentPIDs returns, for each agent window's pane, the agent's own
// process: the shallowest process on each branch of the pane's tree whose
// name or executable is one of the agent's binaries. The agent's children
// are not in it, even when they share its name (a `node` dev server under
// Gemini CLI, which is itself `node`).
func (r *procReader) agentPIDs(children map[int][]int, panes map[int]string) map[int]bool {
	out := map[int]bool{}
	for root, agent := range panes {
		wants := binaries[agent]
		stack := []int{root}
		seen := map[int]bool{}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[pid] {
				continue
			}
			seen[pid] = true
			comm, _, _, ok := r.readStat(strconv.Itoa(pid))
			if ok && (matchesBinary(wants, comm) || matchesBinary(wants, r.readExeBase(pid))) {
				out[pid] = true
				continue // its descendants are its work, not the agent
			}
			stack = append(stack, children[pid]...)
		}
	}
	return out
}

// oomChange is one write applyOOM made, for tests and the debug log.
type oomChange struct {
	PID  int
	Comm string
	From int
	To   int
}

// applyOOM sets oom_score_adj on dev's processes: OOMUserManager for
// dev's user manager (`systemd` whose parent is pid 1), OOMProtected for
// the tmux server, the herdr servers, the keystroke path and the agents,
// 0 for any other that has a negative value.
// A positive value the user chose (choom) is left alone. It writes only
// what differs.
func (r *procReader) applyOOM(devUID int, agents, herdrServers map[int]bool) []oomChange {
	entries, err := os.ReadDir(r.paths.Proc())
	if err != nil {
		return nil
	}
	var changes []oomChange
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		dir := r.paths.ProcPID(e.Name())
		if procUID(filepath.Join(dir, "status")) != devUID {
			continue
		}
		comm, ppid, ok := r.commAndParent(e.Name())
		if !ok {
			continue
		}
		pid, _ := strconv.Atoi(e.Name())
		adjPath := filepath.Join(dir, "oom_score_adj")
		b, err := os.ReadFile(adjPath)
		if err != nil {
			continue
		}
		cur, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue
		}
		want := cur
		switch {
		case comm == "systemd" && ppid == 1:
			want = OOMUserManager
		case agents[pid] || herdrServers[pid] || comm == tmuxServerComm || keystrokeComms[comm]:
			want = OOMProtected
		case cur < 0:
			want = 0
		}
		if want == cur {
			continue
		}
		if err := os.WriteFile(adjPath, []byte(strconv.Itoa(want)), 0o644); err != nil {
			continue // exited, or not ours to change; the next refresh tries again
		}
		changes = append(changes, oomChange{PID: pid, Comm: comm, From: cur, To: want})
	}
	return changes
}

// herdrServerComm is the herdr server's process name.
const herdrServerComm = "herdr"

// HerdrServerNice is the nice value of every herdr server thread
// (DECISIONS I-505): keystrokes and screen updates get the CPU before a
// build in a pane, which shares the server's cgroup.
const HerdrServerNice = -5

// herdrServers finds dev's herdr servers: a process named herdr whose
// parent is dev's `systemd --user` (repose-herdr-server.service execs it
// there; a client runs under sshd or a shell instead).
func (r *procReader) herdrServers(devUID int) map[int]bool {
	out := map[int]bool{}
	entries, err := os.ReadDir(r.paths.Proc())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		comm, ppid, ok := r.commAndParent(e.Name())
		if !ok || comm != herdrServerComm {
			continue
		}
		if procUID(filepath.Join(r.paths.ProcPID(e.Name()), "status")) != devUID {
			continue
		}
		pcomm, _, ok := r.commAndParent(strconv.Itoa(ppid))
		if !ok || pcomm != "systemd" || procUID(filepath.Join(r.paths.ProcPID(strconv.Itoa(ppid)), "status")) != devUID {
			continue
		}
		pid, _ := strconv.Atoi(e.Name())
		out[pid] = true
	}
	return out
}

// herdrAgentWants is every agent's binary but node: under herdr guestd has
// no pane-to-agent map (it never asks herdr for pane pids, which come with
// argv and cwd), and a `node` dev server in a shell pane looks the same as
// Gemini CLI, so Gemini under herdr is protected only through a binary
// named gemini.
func herdrAgentWants() []string {
	var out []string
	for _, a := range Agents {
		for _, b := range binaries[a] {
			if b != "node" {
				out = append(out, b)
			}
		}
	}
	return out
}

// herdrAgentPIDs walks each herdr server's tree and returns, per branch,
// the shallowest process whose name or executable is an agent's binary.
func (r *procReader) herdrAgentPIDs(children map[int][]int, servers map[int]bool) map[int]bool {
	out := map[int]bool{}
	wants := herdrAgentWants()
	for server := range servers {
		stack := append([]int(nil), children[server]...)
		seen := map[int]bool{server: true}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[pid] {
				continue
			}
			seen[pid] = true
			comm, _, _, ok := r.readStat(strconv.Itoa(pid))
			if ok && (matchesBinary(wants, comm) || matchesBinary(wants, r.readExeBase(pid))) {
				out[pid] = true
				continue
			}
			stack = append(stack, children[pid]...)
		}
	}
	return out
}

// applyNice sets HerdrServerNice on every thread of each herdr server that
// is above it, and 0 on any thread below 0 of a process in the server's
// tree: a pane forked from a reniced thread inherits -5, and a build in it
// would get the priority meant for the server. It returns how many
// threads it changed.
func (r *procReader) applyNice(children map[int][]int, servers map[int]bool) int {
	changed := 0
	for server := range servers {
		for _, tid := range r.threads(server) {
			if nice, ok := r.threadNice(server, tid); ok && nice > HerdrServerNice {
				if r.setNice(tid, HerdrServerNice) == nil {
					changed++
				}
			}
		}
		stack := append([]int(nil), children[server]...)
		seen := map[int]bool{server: true}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[pid] {
				continue
			}
			seen[pid] = true
			for _, tid := range r.threads(pid) {
				if nice, ok := r.threadNice(pid, tid); ok && nice < 0 {
					if r.setNice(tid, 0) == nil {
						changed++
					}
				}
			}
			stack = append(stack, children[pid]...)
		}
	}
	return changed
}

// threads lists a process's thread ids; the process itself when its task
// directory cannot be read.
func (r *procReader) threads(pid int) []int {
	entries, err := os.ReadDir(filepath.Join(r.paths.ProcPID(strconv.Itoa(pid)), "task"))
	if err != nil {
		return []int{pid}
	}
	out := make([]int, 0, len(entries))
	for _, e := range entries {
		if tid, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, tid)
		}
	}
	return out
}

// threadNice reads field 19 (nice) of a thread's stat.
func (r *procReader) threadNice(pid, tid int) (int, bool) {
	dir := r.paths.ProcPID(strconv.Itoa(pid))
	b, err := os.ReadFile(filepath.Join(dir, "task", strconv.Itoa(tid), "stat"))
	if err != nil {
		if tid != pid {
			return 0, false
		}
		if b, err = os.ReadFile(filepath.Join(dir, "stat")); err != nil {
			return 0, false
		}
	}
	end := bytes.LastIndexByte(b, ')')
	if end < 0 {
		return 0, false
	}
	rest := bytes.Fields(b[end+1:])
	const niceIdx = 16 // field 19; rest[0] is field 3
	if len(rest) <= niceIdx {
		return 0, false
	}
	n, err := strconv.Atoi(string(rest[niceIdx]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// commAndParent reads a process's name and parent pid from its stat.
func (r *procReader) commAndParent(pid string) (string, int, bool) {
	b, err := os.ReadFile(filepath.Join(r.paths.ProcPID(pid), "stat"))
	if err != nil {
		return "", 0, false
	}
	start, end := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
	if start < 0 || end < start {
		return "", 0, false
	}
	rest := bytes.Fields(b[end+1:])
	if len(rest) < 2 {
		return "", 0, false
	}
	ppid, err := strconv.Atoi(string(rest[1]))
	if err != nil {
		return "", 0, false
	}
	return string(b[start+1 : end]), ppid, true
}

// setPriority is the real setter: nice is per thread on Linux, and
// PRIO_PROCESS with a thread id sets that thread's.
func setPriority(tid, nice int) error {
	return unix.Setpriority(unix.PRIO_PROCESS, tid, nice)
}
