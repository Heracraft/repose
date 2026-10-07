package sample

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/multiplexer"
)

// TestHerdrLiveBinary runs the watcher against a real herdr server when
// REPOSE_TEST_HERDR names a herdr binary (0.9.3, the release the base
// pins): a pane reported as gemini working, then idle, reads as working,
// then idle with one completion. It is skipped without the variable.
func TestHerdrLiveBinary(t *testing.T) {
	bin := os.Getenv("REPOSE_TEST_HERDR")
	if bin == "" {
		t.Skip("REPOSE_TEST_HERDR is not set")
	}
	dir, err := os.MkdirTemp("", "hl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "herdr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "herdr", "config.toml"), []byte("[update]\nversion_check = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "h.sock")
	env := append(os.Environ(), "HOME="+home, "HERDR_SOCKET_PATH="+sock, "XDG_CONFIG_HOME=", "HERDR_ENV=", "HERDR_PANE_ID=")
	herdr := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env, cmd.Dir = env, home
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("herdr %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	srv := exec.Command(bin, "server")
	srv.Env, srv.Dir = env, home
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	herdr("workspace", "create", "--cwd", home, "--label", "todo-app", "--no-focus")

	w, _, rec, clk, _ := newHerdrWatcher(t, nil, multiplexer.Herdr, nil)
	w.SetHerdrSocket(sock, os.Getuid())
	ctx := context.Background()

	herdr("pane", "report-agent", "--source", "repose-test", "--agent", "gemini", "--state", "working", "w1:p1")
	w.Refresh(ctx)
	clk.advance(Interval)
	w.Refresh(ctx)
	if got := agentStates(w); got["gemini w1:p1"] != "gemini:working" {
		t.Fatalf("agents = %v, want gemini w1:p1 working", got)
	}
	herdr("pane", "report-agent", "--source", "repose-test", "--agent", "gemini", "--state", "idle", "w1:p1")
	for i := 0; i < 2; i++ {
		clk.advance(Interval)
		w.Refresh(ctx)
	}
	states, events, warns := rec.snapshot()
	t.Logf("states %v events %v warns %v", states, events, warns)
	if got := agentStates(w); got["gemini w1:p1"] != "gemini:idle" {
		t.Fatalf("agents = %v, want gemini w1:p1 idle", got)
	}
	if len(events) != 1 || events[0] != "gemini/gemini w1:p1=completed:gemini went idle" {
		t.Fatalf("events = %v, want one completion", events)
	}
	herdr("agent", "rename", "w1:p1", "gemini")
	clk.advance(Interval)
	w.Refresh(ctx)
	if k, ok := w.ResolveHerdr(ctx, "w1:p1"); !ok || k != "gemini" {
		t.Fatalf("ResolveHerdr after rename = %q, %v", k, ok)
	}
}

// TestHerdrLiveRenice runs applyNice against a real herdr server's threads
// (I-505) when REPOSE_TEST_HERDR names a herdr binary and the test runs as
// root (lowering a nice value needs CAP_SYS_NICE): every server thread ends
// at -5, a pane opened after the renice inherits -5 and is put back to 0,
// and a second pass changes nothing.
func TestHerdrLiveRenice(t *testing.T) {
	bin := os.Getenv("REPOSE_TEST_HERDR")
	if bin == "" || os.Geteuid() != 0 {
		t.Skip("needs REPOSE_TEST_HERDR and root")
	}
	dir, err := os.MkdirTemp("", "hn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "herdr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "herdr", "config.toml"), []byte("[update]\nversion_check = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "h.sock")
	env := append(os.Environ(), "HOME="+home, "HERDR_SOCKET_PATH="+sock, "XDG_CONFIG_HOME=")
	srv := exec.Command(bin, "server")
	srv.Env, srv.Dir = env, home
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	r := newProcReader(sysdepRoot())
	pid := srv.Process.Pid
	servers := map[int]bool{pid: true}
	children, _ := r.childIndex()
	n := r.applyNice(children, servers)
	t.Logf("first pass changed %d threads", n)

	ws := exec.Command(bin, "workspace", "create", "--cwd", home, "--label", "t", "--no-focus")
	ws.Env, ws.Dir = env, home
	if out, err := ws.CombinedOutput(); err != nil {
		t.Fatalf("workspace create: %v\n%s", err, out)
	}
	time.Sleep(300 * time.Millisecond)
	children, _ = r.childIndex()
	if len(children[pid]) == 0 {
		t.Fatal("the herdr server has no pane process")
	}
	shell := children[pid][0]
	before, _ := r.threadNice(shell, shell)
	n = r.applyNice(children, servers)
	after, _ := r.threadNice(shell, shell)
	t.Logf("pane shell pid %d nice %d before the second pass, %d after; second pass changed %d", shell, before, after, n)
	if before != HerdrServerNice || after != 0 {
		t.Fatalf("pane shell nice %d then %d, want -5 inherited then 0", before, after)
	}
	for _, tid := range r.threads(pid) {
		if nice, ok := r.threadNice(pid, tid); !ok || nice != HerdrServerNice {
			t.Fatalf("server thread %d nice = %d, want -5", tid, nice)
		}
	}
	t.Logf("server threads: %d, all at nice -5", len(r.threads(pid)))
	children, _ = r.childIndex()
	if n := r.applyNice(children, servers); n != 0 {
		t.Fatalf("third pass changed %d threads", n)
	}
}

func sysdepRoot() sysdep.Paths { return sysdep.Paths{} }
