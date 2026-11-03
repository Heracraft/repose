//go:build unix

package testdb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// deadPID returns a pid that cannot be running. pid_max is exclusive, so the
// kernel never allocates it; a pid from a process we waited for could be
// reused between the wait and the check.
func deadPID(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		t.Skipf("no /proc/sys/kernel/pid_max: %v", err)
	}
	max, err := strconv.Atoi(string(bytesTrim(b)))
	if err != nil {
		t.Fatalf("pid_max %q: %v", b, err)
	}
	return max
}

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

// cluster fabricates a cluster directory. No Postgres runs: everything the
// reaper decides on is in the file system, which is the point -- a reaper
// that needed a live cluster to test would only ever be tested by hand.
func cluster(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(p, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestAbandonedLeavesALockedClusterAlone(t *testing.T) {
	p := cluster(t, t.TempDir(), "repose-pg-live", 0)
	f, err := os.OpenFile(filepath.Join(p, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// flock conflicts between open file descriptions, so one taken here
	// stands in for one held by another `go test`.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("take the lock: %v", err)
	}
	if abandoned(p) {
		t.Error("a cluster whose lock is held reads as abandoned; a live suite would lose its database")
	}
}

func TestAbandonedTakesAClusterWhoseLockIsFree(t *testing.T) {
	p := cluster(t, t.TempDir(), "repose-pg-dead", 0)
	// Written and released: what a SIGKILL leaves behind.
	f, err := os.OpenFile(filepath.Join(p, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if !abandoned(p) {
		t.Error("a cluster with a free lock reads as live, so nothing is ever reaped")
	}
}

// The unlocked cases are clusters started by a binary older than the lock
// file. They are judged by the postmaster, with a 30-minute floor under the
// seconds that code spends between MkdirTemp and pg_ctl.
func TestAbandonedOnClustersWithNoLockFile(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		age  time.Duration
		pid  string
		want bool
		why  string
	}{
		{"repose-pg-young", 0, "", false, "a directory a minute old may be a cluster still being built"},
		{"repose-pg-old-nopid", 2 * time.Hour, "", true, "half-built and two hours old"},
		{"repose-pg-old-livepid", 2 * time.Hour, strconv.Itoa(os.Getpid()), false, "its postmaster pid is running"},
		{"repose-pg-old-deadpid", 2 * time.Hour, "", true, "its postmaster pid is gone"},
		{"repose-pg-old-junkpid", 2 * time.Hour, "not a number", false, "an unreadable pid file is not evidence of death"},
	} {
		t.Run(c.name, func(t *testing.T) {
			pid := c.pid
			if c.name == "repose-pg-old-deadpid" {
				pid = strconv.Itoa(deadPID(t))
			}
			p := cluster(t, dir, c.name, 0)
			if pid != "" {
				// The real file has the port and the socket dir on
				// later lines; only the first is read.
				if err := os.WriteFile(filepath.Join(p, "data", "postmaster.pid"), []byte(pid+"\n5432\n/tmp\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if c.age > 0 {
				old := time.Now().Add(-c.age)
				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
			}
			if got := abandoned(p); got != c.want {
				t.Errorf("abandoned = %v, want %v: %s", got, c.want, c.why)
			}
		})
	}
}

func TestReapRemovesOnlyWhatItOwns(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir) // os.TempDir reads it on every call

	dead := cluster(t, dir, "repose-pg-dead", 2*time.Hour)
	live := cluster(t, dir, "repose-pg-live", 2*time.Hour)
	other := cluster(t, dir, "postgresql-somebody-elses", 2*time.Hour)
	file := filepath.Join(dir, "repose-pg-not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(filepath.Join(live, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	reap()

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("the abandoned cluster survived reap: %v", err)
	}
	for _, p := range []string{live, other, file} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("reap removed %s, which is not its to remove: %v", filepath.Base(p), err)
		}
	}
}

func TestClaimHoldsTheLockForTheProcess(t *testing.T) {
	saved := ownerLock
	t.Cleanup(func() {
		if ownerLock != nil && ownerLock != saved {
			_ = ownerLock.Close()
		}
		ownerLock = saved
	})
	ownerLock = nil

	p := cluster(t, t.TempDir(), "repose-pg-mine", 0)
	claim(p)
	if ownerLock == nil {
		t.Fatal("claim took no lock, so this cluster would be reaped by the next run while it is in use")
	}
	if abandoned(p) {
		t.Error("a cluster this process has claimed reads as abandoned")
	}
}

// reapDirs is where the two nested clusters of 2026-11-03 were found: inside
// `nix develop`, TMPDIR is a nix-shell directory that goes away with the
// shell, so a leak from one dev shell is invisible from the next.
func TestReapDirsAscendsOutOfNixShell(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp/nix-shell.AbCdEf")
	got := reapDirs()
	want := []string{"/tmp/nix-shell.AbCdEf", "/tmp"}
	if len(got) != len(want) {
		t.Fatalf("reapDirs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reapDirs() = %v, want %v", got, want)
		}
	}

	t.Setenv("TMPDIR", "/tmp")
	if got := reapDirs(); len(got) != 1 || got[0] != "/tmp" {
		t.Errorf("reapDirs() = %v, want [/tmp]: an ordinary TMPDIR has nothing to ascend out of", got)
	}
}

func TestPostmasterPIDReadsTheMagnitude(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int
		ok   bool
	}{
		// A standalone backend, which is what initdb's bootstrap runs
		// and so what a cluster killed in its first seconds leaves.
		{"-4111094\n/tmp/x/data\n1793723206\n5432\n", 4111094, true},
		{"8321\n/tmp/x/data\n", 8321, true},
		{"\n", 0, false},
		{"not a number\n", 0, false},
		{"0\n", 0, false},
	} {
		got, ok := postmasterPID([]byte(c.raw))
		if got != c.want || ok != c.ok {
			t.Errorf("postmasterPID(%q) = %d, %v; want %d, %v", c.raw, got, ok, c.want, c.ok)
		}
	}
}

// The pid in a file days old may belong to anything by now, so stopPostgres
// kills it only when the kernel says it is this cluster. Both directions are
// tested with a real child process, because the cost of getting this wrong
// is a SIGKILL to a stranger.
func TestStopPostgresKillsOnlyWhatNamesThisCluster(t *testing.T) {
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no /proc")
	}
	for _, c := range []struct {
		name      string
		namesData bool
	}{
		{"a stranger with the recorded pid", false},
		{"a postgres whose cmdline names this data directory", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			base := cluster(t, t.TempDir(), "repose-pg-stop", 0)
			data := filepath.Join(base, "data")
			// A loop, not `sh -c "sleep 60"`: a shell execs a single
			// simple command and replaces its own cmdline with
			// sleep's, which then names nothing and made the first
			// draft of this test fail for the wrong reason.
			args := []string{"-c", "while :; do sleep 1; done"}
			if c.namesData {
				args = append(args, "sh", data)
			}
			cmd := exec.Command("/bin/sh", args...)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			})
			if err := os.WriteFile(filepath.Join(data, "postmaster.pid"),
				[]byte(strconv.Itoa(cmd.Process.Pid)+"\n"+data+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			stopPostgres(base)

			// SIGKILL is not instant, and the process stays as a
			// zombie until Wait, which still answers kill(pid, 0).
			// So wait for the child itself.
			done := make(chan struct{})
			go func() { _, _ = cmd.Process.Wait(); close(done) }()
			alive := true
			select {
			case <-done:
				alive = false
			case <-time.After(2 * time.Second):
			}
			if c.namesData && alive {
				t.Error("the cluster's own postgres survived stopPostgres, so a reaped directory leaves a process writing into it")
			}
			if !c.namesData && !alive {
				t.Error("stopPostgres killed a process that only shared the recorded pid")
			}
		})
	}
}
