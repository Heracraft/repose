//go:build unix

package testdb

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A throwaway cluster is owned by the process that started it, and the
// kernel is what reports the owner's death. `testdb.Stop` covers the normal
// exit; this file covers every other one -- a panic, a SIGKILL, the OOM
// killer, a `go test` cancelled from the terminal -- where no deferred code
// runs at all. On 2026-11-03 twenty-five clusters were alive on the dev box
// with data dirs going back six days, 2.3 GB, on a machine that had just
// killed two background tasks for want of memory (DECISIONS I-998, closed by I-1022).
//
// Ownership, not age: each cluster holds an exclusive flock on a lock file
// in its own directory for the life of the process that started it, and a
// lock released is a process gone, whichever way it went. Age cannot tell a
// six-hour suite from a six-hour corpse.
//
// The hazard here is the other way round: removing a *live* cluster reds
// somebody else's suite, and this walks directories it does not own. So
// every branch below defaults to leaving a directory alone, and the only
// directories considered at all are ones matching testdb's own
// `repose-pg-*` prefix.

const lockName = "owner.lock"

// ownerLock is held for the life of the process. It is never closed: closing
// the file releases the flock, which is exactly what we are promising not to
// do while the cluster lives.
var ownerLock *os.File

// claim takes the ownership lock on a cluster directory this process just
// created. A failure here is not fatal -- the cluster is usable, it just
// will not be reaped automatically -- so it reports nothing and the caller
// carries on.
func claim(base string) {
	f, err := os.OpenFile(filepath.Join(base, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return
	}
	ownerLock = f
}

// reapDirs is where a leaked cluster can be. os.MkdirTemp puts ours under
// os.TempDir(), but inside `nix develop` TMPDIR is itself a `nix-shell.*`
// directory that the shell removes on exit, so a cluster started in one dev
// shell is invisible from the next. Twenty-three of the twenty-five sat in
// /tmp and two were nested in a nix-shell directory under it, which is why
// this ascends out of every `nix-shell.*` component rather than looking in
// one place.
func reapDirs() []string {
	dirs := []string{os.TempDir()}
	d := os.TempDir()
	for {
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		if !strings.HasPrefix(filepath.Base(d), "nix-shell.") {
			break
		}
		dirs = append(dirs, parent)
		d = parent
	}
	return dirs
}

// reap removes the clusters in those directories whose owner is gone. It is
// called once, before a new cluster is allocated: the process that is about
// to pay for a cluster is the one with an interest in the disk, and no
// background goroutine then races a suite that is shutting down.
func reap() {
	seen := map[string]bool{}
	for _, dir := range reapDirs() {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "repose-pg-") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if seen[p] {
				continue
			}
			seen[p] = true
			if abandoned(p) {
				stopPostgres(p)
				// Twice, with a pause: RemoveAll walks the tree, and
				// a postgres still writing into it as the walk passes
				// leaves the final rmdir with a non-empty directory.
				// Measured: one pass left the 23 MB cluster's files
				// gone and the directory behind.
				if err := os.RemoveAll(p); err != nil {
					time.Sleep(200 * time.Millisecond)
					_ = os.RemoveAll(p)
				}
			}
		}
	}
}

// abandoned answers whether nothing living owns the cluster at base. False
// on every doubt, including an error reading it: the cost of a wrong "true"
// is another agent's red suite, and the cost of a wrong "false" is a
// directory that survives until the next run.
func abandoned(base string) bool {
	lock := filepath.Join(base, lockName)
	if _, err := os.Stat(lock); err == nil {
		// Written by a process that had this file: the flock answers.
		f, err := os.OpenFile(lock, os.O_RDWR, 0o600)
		if err != nil {
			return false
		}
		defer func() { _ = f.Close() }() // releases the lock we may have taken
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return false // held: the owner is alive
		}
		return true
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}

	// No lock file, so the cluster was started by a binary older than this
	// code. Ask the postmaster instead: its pid is the first line of
	// postmaster.pid, and `kill -0` on a dead pid is the whole test. The
	// age check is only for the seconds between MkdirTemp and pg_ctl in
	// that older code, where there is no pid file yet and nothing else
	// distinguishes a cluster being born from one that never was. It is a
	// floor under a race, not a policy; clusters this code starts are
	// judged by the lock alone. Remove it once no unlocked cluster
	// predates this.
	fi, err := os.Stat(base)
	if err != nil || time.Since(fi.ModTime()) < 30*time.Minute {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(base, "data", "postmaster.pid"))
	if errors.Is(err, os.ErrNotExist) {
		return true // half-built and half an hour old
	}
	if err != nil {
		return false
	}
	pid, ok := postmasterPID(raw)
	if !ok {
		return false
	}
	// ESRCH is the only answer that means gone. EPERM means it is running
	// as somebody else, which is a live cluster and not ours to remove.
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// postmasterPID reads the pid out of a postmaster.pid. The first line is
// NEGATIVE for a standalone backend -- `postgres --single`, which is what
// initdb's bootstrap runs, and which is therefore what a cluster killed
// during its first four seconds leaves behind. The magnitude is a real pid
// either way: measured at -4111094 with pid 4111094 alive and its cmdline
// `postgres --single -F -O`. Reading the sign as part of the number made
// every such cluster read as live for ever.
func postmasterPID(raw []byte) (int, bool) {
	line := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	pid, err := strconv.Atoi(strings.TrimPrefix(line, "-"))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// stopPostgres ends whatever still serves the cluster at base. pg_ctl is
// first because it is the orderly way and it knows about a postmaster; it
// cannot stop a standalone backend, which is the shape a cluster killed
// during initdb is in, so the pid file is the fallback.
//
// The identity check is the whole safety of this: a pid in a file left over
// from days ago may belong to something else entirely by now, and sending
// SIGKILL to a stranger is far worse than leaving a directory. So the pid
// is killed only when the kernel agrees it is this cluster -- its cmdline
// names this data directory. /proc is Linux; elsewhere the read fails and
// nothing is killed, which is the safe direction.
func stopPostgres(base string) {
	data := filepath.Join(base, "data")
	_ = exec.Command("pg_ctl", "-D", data, "-m", "immediate", "stop").Run()
	raw, err := os.ReadFile(filepath.Join(data, "postmaster.pid"))
	if err != nil {
		return
	}
	pid, ok := postmasterPID(raw)
	if !ok {
		return
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || !strings.Contains(string(cmdline), data) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
