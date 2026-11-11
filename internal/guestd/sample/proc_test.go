package sample

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// fixtureProc builds a /proc tree under a temp root. Each process is
// (pid, ppid, comm, cpuTicks, rssPages, uid).
type fakeProc struct {
	pid, ppid int
	comm      string
	ticks     uint64
	rssPages  uint64
	uid       int
	exe       string // basename of the exe link, when the process has one
}

func writeProc(t *testing.T, p sysdep.Paths, procs []fakeProc) {
	t.Helper()
	for _, pr := range procs {
		dir := p.ProcPID(fmt.Sprint(pr.pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// The layout of /proc/<pid>/stat: pid (comm) state ppid ... utime
		// (14) stime (15) ... rss (24).
		fields := make([]string, 52)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0] = fmt.Sprint(pr.ppid) // ppid, field 4
		fields[10] = fmt.Sprint(pr.ticks)
		fields[11] = "0"
		fields[20] = fmt.Sprint(pr.rssPages)
		line := fmt.Sprintf("%d (%s) S %s\n", pr.pid, pr.comm, joinFields(fields))
		if pr.exe != "" {
			if err := os.Symlink("/nix/store/0000000000000000000000000000000-fake/bin/"+pr.exe, filepath.Join(dir, "exe")); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		status := fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\n", pr.comm, pr.uid, pr.uid, pr.uid, pr.uid)
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func joinFields(f []string) string {
	out := ""
	for i, s := range f {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

func newProcFixture(t *testing.T, procs []fakeProc) (*procReader, sysdep.Paths) {
	t.Helper()
	p := sysdep.Paths{Root: t.TempDir()}
	writeProc(t, p, procs)
	return newProcReader(p), p
}

func TestProcReaderAggregatesByName(t *testing.T) {
	r, _ := newProcFixture(t, []fakeProc{
		{pid: 1, ppid: 0, comm: "systemd", ticks: 10, rssPages: 100},
		{pid: 2, ppid: 1, comm: "node", ticks: 100, rssPages: 1000},
		{pid: 3, ppid: 1, comm: "node", ticks: 50, rssPages: 500},
		{pid: 4, ppid: 1, comm: "claude", ticks: 5, rssPages: 250},
	})

	first, _, err := r.read(1000)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	byComm := map[string]uint64{}
	rss := map[string]uint64{}
	for _, s := range first {
		byComm[s.GetComm()] = s.GetCpuNsDelta()
		rss[s.GetComm()] = s.GetRssBytes()
	}
	// The first sample has no previous total, so the delta is zero; the RSS
	// of the two node processes is summed.
	if byComm["node"] != 0 {
		t.Errorf("first delta = %d, want 0", byComm["node"])
	}
	if want := uint64(1500) * uint64(os.Getpagesize()); rss["node"] != want {
		t.Errorf("node rss = %d, want %d", rss["node"], want)
	}
}

func TestProcReaderReportsDeltas(t *testing.T) {
	r, p := newProcFixture(t, []fakeProc{{pid: 2, ppid: 1, comm: "node", ticks: 100, rssPages: 10}})
	if _, _, err := r.read(1000); err != nil {
		t.Fatalf("first read: %v", err)
	}
	writeProc(t, p, []fakeProc{{pid: 2, ppid: 1, comm: "node", ticks: 160, rssPages: 10}})

	second, _, err := r.read(1000)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	var got uint64
	for _, s := range second {
		if s.GetComm() == "node" {
			got = s.GetCpuNsDelta()
		}
	}
	if want := uint64(60) * (1_000_000_000 / clockTicks); got != want {
		t.Fatalf("delta = %d ns, want %d", got, want)
	}
}

func TestProcReaderKeepsWatchedNamesBelowTheTop(t *testing.T) {
	procs := []fakeProc{}
	for i := 0; i < TopProcs+20; i++ {
		procs = append(procs, fakeProc{pid: 100 + i, ppid: 1, comm: fmt.Sprintf("busy%02d", i), ticks: uint64(1000 - i)})
	}
	// A miner that throttles itself to the bottom of the list.
	procs = append(procs, fakeProc{pid: 9999, ppid: 1, comm: "xmrig", ticks: 0})

	p := sysdep.Paths{Root: t.TempDir()}
	writeProc(t, p, procs)
	r := newProcReader(p)
	if _, _, err := r.read(1000); err != nil {
		t.Fatalf("first read: %v", err)
	}
	out, _, err := r.read(1000)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	found := false
	for _, s := range out {
		if s.GetComm() == "xmrig" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a watched name was trimmed away; %d samples returned", len(out))
	}
	if len(out) > TopProcs+5 {
		t.Fatalf("returned %d samples, want about %d", len(out), TopProcs)
	}
}

func TestTreeCPUAndTreeHasComm(t *testing.T) {
	r, _ := newProcFixture(t, []fakeProc{
		{pid: 10, ppid: 1, comm: "bash", ticks: 5},
		{pid: 11, ppid: 10, comm: "claude", ticks: 30},
		{pid: 12, ppid: 11, comm: "cargo", ticks: 200},
	})

	children, ok := r.childIndex()
	if !ok {
		t.Fatal("childIndex failed")
	}
	cpu, ok := r.treeCPU(children, 10)
	if !ok {
		t.Fatal("treeCPU did not find the tree")
	}
	if want := uint64(235) * (1_000_000_000 / clockTicks); cpu != want {
		t.Fatalf("tree cpu = %d, want %d: the children's work is the agent's work", cpu, want)
	}
	if !r.treeHasComm(children, 10, "claude") {
		t.Fatal("claude was not found in its own pane's tree")
	}
	if r.treeHasComm(children, 10, "codex") {
		t.Fatal("codex was found in a tree that does not contain it")
	}
}

func TestSSHSessionsCountsDevOwnedSSHD(t *testing.T) {
	r, _ := newProcFixture(t, []fakeProc{
		{pid: 20, ppid: 1, comm: "sshd", uid: 0},             // the listener
		{pid: 21, ppid: 20, comm: "sshd", uid: 0},            // the privsep parent
		{pid: 22, ppid: 21, comm: "sshd", uid: 1000},         // one session
		{pid: 23, ppid: 21, comm: "sshd-session", uid: 1000}, // another, newer OpenSSH
		{pid: 24, ppid: 1, comm: "node", uid: 1000},
	})
	_, n, err := r.read(1000)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != 2 {
		t.Fatalf("ssh_sessions = %d, want 2", n)
	}
}

func TestProcNamesWithSpacesAndParens(t *testing.T) {
	r, _ := newProcFixture(t, []fakeProc{{pid: 30, ppid: 1, comm: "Web Content (tab)", ticks: 7}})
	out, _, err := r.read(1000)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	found := false
	for _, s := range out {
		if s.GetComm() == "Web Content (tab)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a comm containing a space and parentheses was mis-parsed: %v", out)
	}
}

func TestWatchListIsNotEmpty(t *testing.T) {
	if len(WatchList()) == 0 {
		t.Fatal("the watch list is empty")
	}
	if !Watched("xmrig") || Watched("node") {
		t.Fatal("Watched does not agree with the list")
	}
	// A miner the api stops a guest for is watched in any spelling (I-239).
	for _, comm := range []string{"lolMiner", "xmrig-notls", "SRBMiner-MULTI", "nanominer"} {
		if !Watched(comm) {
			t.Errorf("Watched(%q) = false", comm)
		}
	}
}

// TestAProcessArgumentNeverReachesTheSample: the idle analysis
// (DECISIONS I-1152) reads `proc_samples` to say what was burning a
// machine's CPU while it would not freeze, so the whole of its usefulness
// rests on the sample carrying the process NAME and nothing else. A
// process here is given a secret in its command line and another in its
// environment; the reader must produce a row that holds neither, and
// must not have read either file.
//
// The probe that proves this would fail without the boundary: delete the
// `exe`/`stat` reads' comm and read `cmdline` instead, and the secret
// turns up in the ProcSample. The system-call half -- that the files are
// never even opened -- is internal/guestd/strace_test.go; this is the
// half that can run anywhere.
func TestAProcessArgumentNeverReachesTheSample(t *testing.T) {
	const argSecret = "sk-live-7f3a9c21deadbeef"
	const envSecret = "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI"
	r, p := newProcFixture(t, []fakeProc{
		{pid: 1, ppid: 0, comm: "systemd", ticks: 10, rssPages: 100},
		{pid: 7, ppid: 1, comm: "node", ticks: 900, rssPages: 4000},
	})
	dir := p.ProcPID("7")
	for name, body := range map[string]string{
		"cmdline": "node\x00--api-key=" + argSecret + "\x00/home/dev/todo-app/src/secret.ts\x00",
		"environ": envSecret + "\x00",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Two reads, so the row carries a real CPU delta and not only a name.
	if _, _, err := r.read(1000); err != nil {
		t.Fatal(err)
	}
	writeProc(t, p, []fakeProc{{pid: 7, ppid: 1, comm: "node", ticks: 1800, rssPages: 4000}})
	procs, _, err := r.read(1000)
	if err != nil {
		t.Fatal(err)
	}
	var node *hostdv1.ProcSample
	for _, s := range procs {
		if s.GetComm() == "node" {
			node = s
		}
		text := fmt.Sprintf("%+v", s)
		for _, bad := range []string{argSecret, envSecret, "--api-key", "/home/dev", "secret.ts"} {
			if strings.Contains(text, bad) {
				t.Fatalf("the sample carries %q from a process's command line or environment: %s", bad, text)
			}
		}
	}
	if node == nil {
		t.Fatal("the process is not in the sample at all; the name is what the sample is for")
	}
	if node.GetCpuNsDelta() == 0 {
		t.Fatal("no CPU delta, so the row says nothing about what spent the machine's CPU")
	}
	// The row's whole content: a name, a CPU delta and an RSS. Anything
	// else would be a fourth field to argue about.
	if node.GetComm() != "node" {
		t.Fatalf("comm = %q, want the name alone", node.GetComm())
	}
}
