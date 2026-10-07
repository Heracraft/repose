package sample

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// DECISIONS I-213: the nix packages run the agents through makeWrapper,
// so claude's process is `.claude-wrapped` (comm and exe), and a longer
// name is cut to 15 bytes in comm. Each is still its window's agent, and
// protected; an unrelated dotted process is not.
func TestOOMPriorityFindsNixWrappedAgents(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "tmux: server", uid: 1000},
		{pid: 200, ppid: 100, comm: "bash", uid: 1000},
		{pid: 201, ppid: 200, comm: ".claude-wrapped", uid: 1000, exe: ".claude-wrapped"},
		{pid: 300, ppid: 100, comm: "bash", uid: 1000},
		{pid: 301, ppid: 300, comm: ".opencode-wrapp", uid: 1000},
		{pid: 400, ppid: 100, comm: "bash", uid: 1000},
		{pid: 401, ppid: 400, comm: ".codex-wrapped", uid: 1000},
		{pid: 500, ppid: 100, comm: "bash", uid: 1000},
		{pid: 501, ppid: 500, comm: ".claudeish", uid: 1000},
	}
	r, _ := newProcFixture(t, procs)
	children, ok := r.childIndex()
	if !ok {
		t.Fatal("no child index")
	}
	agents := r.agentPIDs(children, map[int]string{200: "claude", 300: "opencode", 400: "codex", 500: "claude"})
	if len(agents) != 3 || !agents[201] || !agents[301] || !agents[401] {
		t.Fatalf("agents = %v, want 201, 301 and 401", agents)
	}
	if !isAgentCommand("claude", ".claude-wrapped") || isAgentCommand("claude", ".claude") || isAgentCommand("pi", ".claude-wrapped") {
		t.Error("isAgentCommand does not follow nix's wrapped names")
	}
}

// I-200 on a fixture /proc: the tmux server and each window's agent get
// OOMProtected; everything of dev's that inherited a negative value (the
// pane's shell, an MCP server under claude, the vite Gemini started, which
// is `node` like Gemini itself) goes back to 0; root's processes and a
// value dev raised on purpose are left alone; a second pass writes nothing.
func TestOOMPriority(t *testing.T) {
	procs := []fakeProc{
		{pid: 100, ppid: 1, comm: "tmux: server", uid: 1000},
		{pid: 200, ppid: 100, comm: "bash", uid: 1000},
		{pid: 201, ppid: 200, comm: "claude", uid: 1000},
		{pid: 202, ppid: 201, comm: "node", uid: 1000, exe: "node"},
		{pid: 300, ppid: 100, comm: "bash", uid: 1000},
		{pid: 301, ppid: 300, comm: "MainThread", uid: 1000, exe: "node"},
		{pid: 302, ppid: 301, comm: "node", uid: 1000, exe: "node"},
		{pid: 400, ppid: 1, comm: "dockerd", uid: 0},
		{pid: 500, ppid: 100, comm: "bash", uid: 1000},
		{pid: 501, ppid: 500, comm: "python3", uid: 1000},
	}
	r, p := newProcFixture(t, procs)
	adj := map[int]int{100: 0, 200: -800, 201: -800, 202: -800, 300: -800, 301: -800, 302: -800, 400: -500, 500: -800, 501: 300}
	for pid, v := range adj {
		if err := os.WriteFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"), []byte(fmt.Sprintf("%d\n", v)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	children, ok := r.childIndex()
	if !ok {
		t.Fatal("no child index")
	}
	agents := r.agentPIDs(children, map[int]string{200: "claude", 300: "gemini"})
	if len(agents) != 2 || !agents[201] || !agents[301] {
		t.Fatalf("agents = %v, want 201 (claude) and 301 (gemini's node, not the vite under it)", agents)
	}
	changes := r.applyOOM(1000, agents, nil)
	got := map[int]int{}
	for pid := range adj {
		b, _ := os.ReadFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"))
		var v int
		_, _ = fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &v)
		got[pid] = v
	}
	want := map[int]int{100: -800, 200: 0, 201: -800, 202: 0, 300: 0, 301: -800, 302: 0, 400: -500, 500: 0, 501: 300}
	for pid, w := range want {
		if got[pid] != w {
			t.Errorf("pid %d oom_score_adj = %d, want %d", pid, got[pid], w)
		}
	}
	t.Logf("changes: %+v", changes)
	if again := r.applyOOM(1000, agents, nil); len(again) != 0 {
		t.Errorf("second pass wrote %+v", again)
	}
}

// I-576 on a fixture /proc: dev's user manager gets OOMUserManager from
// upstream's 100; dev's sshd-session and the tmux client it runs get
// OOMProtected (sshd handed them -800, or they started at 0 on an older
// base); the login shell and a `repose exec` build that inherited -800
// from the session go back to 0; root's sshd-session and root's systemd
// are left alone; a `systemd` of dev's that is not the manager is not
// treated as one; a second pass writes nothing.
func TestOOMKeystrokePath(t *testing.T) {
	procs := []fakeProc{
		{pid: 10, ppid: 1, comm: "systemd", uid: 1000},
		{pid: 11, ppid: 10, comm: "systemd", uid: 1000},
		{pid: 20, ppid: 1, comm: "sshd", uid: 0},
		{pid: 21, ppid: 20, comm: "sshd-session", uid: 0},
		{pid: 22, ppid: 21, comm: "sshd-session", uid: 1000},
		{pid: 23, ppid: 22, comm: "bash", uid: 1000},
		{pid: 24, ppid: 23, comm: "tmux: client", uid: 1000},
		{pid: 30, ppid: 21, comm: "sshd-session", uid: 1000},
		{pid: 31, ppid: 30, comm: "bash", uid: 1000},
		{pid: 32, ppid: 31, comm: "make", uid: 1000},
		{pid: 40, ppid: 10, comm: "tmux: server", uid: 1000},
		{pid: 41, ppid: 40, comm: "bash", uid: 1000},
	}
	r, p := newProcFixture(t, procs)
	adj := map[int]int{10: 100, 11: 0, 20: -1000, 21: -800, 22: -800, 23: -800, 24: -800, 30: 0, 31: -800, 32: -800, 40: 200, 41: 200}
	for pid, v := range adj {
		if err := os.WriteFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"), []byte(fmt.Sprintf("%d\n", v)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.applyOOM(1000, map[int]bool{}, nil)
	want := map[int]int{10: OOMUserManager, 11: 0, 20: -1000, 21: -800, 22: OOMProtected, 23: 0, 24: OOMProtected, 30: OOMProtected, 31: 0, 32: 0, 40: OOMProtected, 41: 200}
	for pid, w := range want {
		b, _ := os.ReadFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"))
		if got := strings.TrimSpace(string(b)); got != fmt.Sprint(w) {
			t.Errorf("pid %d oom_score_adj = %s, want %d", pid, got, w)
		}
	}
	if again := r.applyOOM(1000, map[int]bool{}, nil); len(again) != 0 {
		t.Errorf("second pass wrote %+v", again)
	}
}

// writeTasks gives a fixture process threads with nice values: tid -> nice.
func writeTasks(t *testing.T, p sysdep.Paths, pid int, comm string, tasks map[int]int) {
	t.Helper()
	for tid, nice := range tasks {
		dir := filepath.Join(p.ProcPID(fmt.Sprint(pid)), "task", fmt.Sprint(tid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTaskNice(t, dir, tid, comm, nice)
	}
}

func writeTaskNice(t *testing.T, dir string, tid int, comm string, nice int) {
	t.Helper()
	fields := make([]string, 52)
	for i := range fields {
		fields[i] = "0"
	}
	fields[15] = fmt.Sprint(nice) // field 19
	line := fmt.Sprintf("%d (%s) S %s\n", tid, comm, joinFields(fields))
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// I-505 on a fixture /proc: the herdr server (herdr under dev's systemd
// --user) and the agents in its tree get OOMProtected; its threads go to
// nice -5; a pane's shell and a build that inherited -5 go back to 0; a
// node dev server is neither; a herdr client under sshd is not a server
// but is on the keystroke path (I-576), so it gets OOMProtected and no
// nice; a second pass changes nothing.
func TestHerdrServerOOMAndNice(t *testing.T) {
	procs := []fakeProc{
		{pid: 10, ppid: 1, comm: "systemd", uid: 1000},
		{pid: 20, ppid: 10, comm: "herdr", uid: 1000},
		{pid: 30, ppid: 20, comm: "bash", uid: 1000},
		{pid: 31, ppid: 30, comm: ".claude-wrapped", uid: 1000, exe: ".claude-wrapped"},
		{pid: 32, ppid: 31, comm: "node", uid: 1000, exe: "node"},
		{pid: 40, ppid: 20, comm: "bash", uid: 1000},
		{pid: 41, ppid: 40, comm: "node", uid: 1000, exe: "node"},
		{pid: 42, ppid: 40, comm: "make", uid: 1000},
		{pid: 50, ppid: 20, comm: "bash", uid: 1000},
		{pid: 51, ppid: 50, comm: "gemini", uid: 1000},
		{pid: 60, ppid: 1, comm: "sshd", uid: 0},
		{pid: 61, ppid: 60, comm: "herdr", uid: 1000},
		{pid: 70, ppid: 1, comm: "systemd", uid: 0},
		{pid: 71, ppid: 70, comm: "herdr", uid: 1000},
	}
	r, p := newProcFixture(t, procs)
	writeTasks(t, p, 20, "herdr", map[int]int{20: 0, 21: 0, 22: -5, 23: -10})
	writeTasks(t, p, 30, "bash", map[int]int{30: -5})
	writeTasks(t, p, 42, "make", map[int]int{42: -5, 43: -5})
	writeTasks(t, p, 41, "node", map[int]int{41: 0})
	writeTasks(t, p, 61, "herdr", map[int]int{61: 0})
	var calls []string
	r.setNice = func(tid, nice int) error {
		calls = append(calls, fmt.Sprintf("%d=%d", tid, nice))
		// Write it back, as the kernel would.
		for _, pid := range []int{20, 30, 42, 41, 61} {
			dir := filepath.Join(p.ProcPID(fmt.Sprint(pid)), "task", fmt.Sprint(tid))
			if _, err := os.Stat(dir); err == nil {
				writeTaskNice(t, dir, tid, "x", nice)
			}
		}
		return nil
	}
	for pid := range map[int]bool{20: true, 30: true, 31: true, 32: true, 41: true, 51: true, 61: true} {
		if err := os.WriteFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"), []byte("200\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p.ProcPID("30"), "oom_score_adj"), []byte("-800\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := r.herdrServers(1000)
	if len(servers) != 1 || !servers[20] {
		t.Fatalf("servers = %v, want 20 only (61 is a client under sshd, 71's systemd is root's)", servers)
	}
	children, _ := r.childIndex()
	agents := r.herdrAgentPIDs(children, servers)
	if len(agents) != 2 || !agents[31] || !agents[51] {
		t.Fatalf("agents = %v, want 31 (claude) and 51 (gemini), not the node processes", agents)
	}
	r.applyOOM(1000, agents, servers)
	for pid, want := range map[int]int{20: -800, 31: -800, 51: -800, 30: 0, 32: 200, 41: 200, 61: -800} {
		b, _ := os.ReadFile(filepath.Join(p.ProcPID(fmt.Sprint(pid)), "oom_score_adj"))
		if got := strings.TrimSpace(string(b)); got != fmt.Sprint(want) {
			t.Errorf("pid %d oom_score_adj = %s, want %d", pid, got, want)
		}
	}

	if n := r.applyNice(children, servers); n != 5 {
		t.Errorf("changed %d threads (%v), want 5", n, calls)
	}
	want := "20=-5 21=-5 30=0 42=0 43=0"
	sortStrings(calls)
	if got := strings.Join(calls, " "); got != want {
		t.Fatalf("setpriority calls = %s, want %s", got, want)
	}
	calls = nil
	if n := r.applyNice(children, servers); n != 0 || len(calls) != 0 {
		t.Fatalf("second pass changed %d: %v", n, calls)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
