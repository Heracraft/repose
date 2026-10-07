//go:build linux

package guestd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/vsockrpc"
)

// TestStraceNeverOpensCmdlineOrEnviron is the enforcement of DECISIONS R5-3
// and of the sentence in the privacy policy. guestd is run under strace while
// a Sample and a hook are served, and the syscall log must contain no open of
// any /proc/<pid>/cmdline and no open of any /proc/<pid>/environ other than
// the one documented read of a hook caller's TMUX_PANE.
//
// The reason this is a test and not a code review: the boundary is a promise
// made in the privacy policy, and a library added later that reads a process
// command line would keep every unit test green.
func TestStraceNeverOpensCmdlineOrEnviron(t *testing.T) {
	// A herdr project with herdr answering (I-504): the agent list, the
	// herdr server's tree for the OOM and nice pass, and a hook naming a
	// herdr pane are all in the trace.
	var herdrSock string
	var herdrAnswered *atomic.Int32
	_, devSock, hookSock, tracePath, stop := tracedGuestd(t, func(root string) {
		p := sysdep.Paths{Root: root}
		mustMkdir(t, filepath.Dir(p.ProjectJSON()))
		mustWrite(t, p.ProjectJSON(), `{"slug":"strace-app","multiplexer":"herdr"}`)
		herdrSock = p.HerdrSock()
		if len(herdrSock) >= 108 {
			return // sun_path: the temp root is too deep for the socket
		}
		if uid, _ := sysdep.DevIdentity(); os.Getuid() != uid {
			// guestd answers a herdr socket only when its peer is dev
			// (uid from the host's dev user, else 1000); a CI runner is
			// someone else, so the herdr half runs only where the test
			// user is dev's uid, as on a dev box or guest.
			t.Logf("herdr half skipped: test uid %d, dev uid %d", os.Getuid(), uid)
			return
		}
		mustMkdir(t, filepath.Dir(herdrSock))
		herdrAnswered = serveHerdrAt(t, herdrSock, `{"type":"agent_list","agents":[`+
			`{"pane_id":"w1:p1","workspace_id":"w1","name":"claude","agent":"claude","agent_status":"working","state_change_seq":1,"cwd":"/home/dev/x"}]}`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := dialWithRetry(t, devSock)
	defer client.Close() //nolint:errcheck // test cleanup

	// A Sample walks every process in /proc. This is the request that would
	// read a command line if anything did.
	resp, err := client.Do(ctx, &guestdv1.Request{Req: &guestdv1.Request_Sample{Sample: &guestdv1.Sample{}}})
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if !resp.GetOk() {
		t.Fatalf("sample: %+v", resp.GetError())
	}
	if len(resp.GetSample().GetProcs()) == 0 {
		t.Fatal("the sample walked no processes, so the trace proves nothing")
	}

	// A hook that names its own window: the TMUX_PANE lookup must not happen.
	postHook(t, hookSock, `{"agent":"claude","kind":"completed","summary":"x","window":"claude"}`)
	// One from a herdr pane: resolved through herdr's socket, no environ.
	postHook(t, hookSock, `{"agent":"claude","kind":"completed","summary":"x","window":"herdr:w1:p1"}`)
	// A slow runner under -race reaches herdr later than 300 ms after
	// the hook; wait for the ask, then let the trace settle.
	for end := time.Now().Add(10 * time.Second); herdrAnswered != nil && herdrAnswered.Load() == 0 && time.Now().Before(end); {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	stop()
	if herdrAnswered != nil && herdrAnswered.Load() == 0 {
		t.Error("guestd never asked the fake herdr, so the trace proves nothing about the herdr path")
	}

	cmdlines, environs := procOpens(t, tracePath)
	if len(cmdlines) > 0 {
		t.Fatalf("guestd opened a process command line, which the privacy policy says it never does:\n%s",
			strings.Join(cmdlines, "\n"))
	}
	if len(environs) > 0 {
		t.Fatalf("guestd opened a process environment while serving a Sample and a hook that named its window:\n%s",
			strings.Join(environs, "\n"))
	}
}

// TestStraceReadsEnvironOnlyForTheHookPaneLookup is the other half: when a
// hook does not name its window, exactly one environment is read, and it is
// the caller's own.
func TestStraceReadsEnvironOnlyForTheHookPaneLookup(t *testing.T) {
	_, _, hookSock, tracePath, stop := tracedGuestd(t)

	waitForSocket(t, hookSock)
	postHook(t, hookSock, `{"agent":"claude","kind":"completed","summary":"x"}`)
	time.Sleep(300 * time.Millisecond)
	stop()

	cmdlines, environs := procOpens(t, tracePath)
	if len(cmdlines) > 0 {
		t.Fatalf("guestd opened a process command line:\n%s", strings.Join(cmdlines, "\n"))
	}
	if len(environs) != 1 {
		t.Fatalf("environ opens = %d, want exactly the one documented TMUX_PANE read:\n%s",
			len(environs), strings.Join(environs, "\n"))
	}
}

// tracedGuestd starts guestd under strace over a guest root whose /proc is the
// real one, and returns the trace path and a stop function. The whole process
// group is killed on stop: strace -f leaves its child running otherwise, and a
// leaked guestd would outlive the test.
func tracedGuestd(t *testing.T, prep ...func(root string)) (root, devSock, hookSock, tracePath string, stop func()) {
	t.Helper()
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("strace is not installed; this test must run on the Linux CI runner")
	}

	root = t.TempDir()
	// The sampler must walk a real process table for the trace to prove
	// anything, so the fake root's /proc is the real /proc.
	if err := os.Symlink("/proc", filepath.Join(root, "proc")); err != nil {
		t.Fatalf("link /proc into the test root: %v", err)
	}
	for _, f := range prep {
		f(root)
	}
	devSock = filepath.Join(root, "run", "repose", "guestd.sock")
	hookSock = filepath.Join(root, "run", "repose", "hooks.sock")
	tracePath = filepath.Join(t.TempDir(), "trace.log")

	cmd := exec.Command(strace,
		"-f", "-e", "trace=openat,open", "-o", tracePath,
		buildGuestd(t),
		"--root", root,
		"--dev-socket", devSock,
		"--hook-socket", hookSock,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start guestd under strace: %v", err)
	}

	var once bool
	stop = func() {
		if once {
			return
		}
		once = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}
	t.Cleanup(stop)
	return root, devSock, hookSock, tracePath, stop
}

// procOpens splits a trace into the command-line and environment opens of
// process directories, which are the two things guestd must not do.
func procOpens(t *testing.T, tracePath string) (cmdlines, environs []string) {
	t.Helper()
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read the trace: %v", err)
	}
	if !strings.Contains(string(trace), "/proc/") {
		t.Fatal("the trace contains no /proc access at all, so the filter did not capture what it should and the test proves nothing")
	}
	for _, line := range strings.Split(string(trace), "\n") {
		if !strings.Contains(line, "/proc/") {
			continue
		}
		if strings.Contains(line, "/cmdline") {
			cmdlines = append(cmdlines, line)
		}
		if strings.Contains(line, "/environ") {
			environs = append(environs, line)
		}
	}
	return cmdlines, environs
}

func buildGuestd(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "guestd")
	cmd := exec.Command("go", "build", "-o", out, "github.com/heracraft/repose/cmd/guestd")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build guestd: %v\n%s", err, b)
	}
	return out
}

func dialWithRetry(t *testing.T, path string) *vsockrpc.Client {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := vsockrpc.DialUnix(path)
		if err == nil {
			return vsockrpc.NewClient(conn, nil)
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", filepath.Base(path))
}
