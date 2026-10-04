package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A laptop server on any of the four addresses holds the port: macOS's
// vite binds ::1 only, and forwarding onto 127.0.0.1 beside it would send
// the browser's localhost to the laptop's server.
func TestLaptopPortTakenOnAnyAddress(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "0.0.0.0", "::"} {
		l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			t.Logf("%s: %v (no such address here; skipped)", host, err)
			continue
		}
		port := l.Addr().(*net.TCPAddr).Port
		if laptopPortFree(port) {
			t.Errorf("port %d held on %s reads as free", port, host)
		}
		_ = l.Close()
		// A just-closed port can read as taken for a moment under load
		// (seen under -race on the dev box); give it a second to settle.
		free := false
		for i := 0; i < 20 && !free; i++ {
			if free = laptopPortFree(port); !free {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if !free {
			t.Errorf("port %d reads as taken after %s let it go", port, host)
		}
	}
}

func TestParseListeners(t *testing.T) {
	out := `LISTEN 0 511 0.0.0.0:5173 0.0.0.0:*
LISTEN 0 4096 127.0.0.1:3000 0.0.0.0:*
LISTEN 0 4096 [::]:8080 [::]:*
LISTEN 0 4096 [::1]:4000 [::]:*
LISTEN 0 4096 *:9229 *:*
LISTEN 0 128 0.0.0.0:22 0.0.0.0:*
LISTEN 0 4096 127.0.0.53%lo:53 0.0.0.0:*
LISTEN 0 4096 10.64.0.5:7000 0.0.0.0:*
LISTEN 0 5 127.0.0.1:6080 0.0.0.0:*
LISTEN 0 5 127.0.0.1:5900 0.0.0.0:*
LISTEN 0 4096 127.0.0.1:9224 0.0.0.0:*
LISTEN 0 10 127.0.0.1:9225 0.0.0.0:*
LISTEN 0 10 127.0.0.1:9222 0.0.0.0:*
LISTEN 0 511 [::1]:5173 [::]:*
LISTEN 0 4096 0.0.0.0:5355 0.0.0.0:*
LISTEN 0 4096 [::]:5355 [::]:*
LISTEN 0 4096 0.0.0.0:5353 0.0.0.0:*
`
	got := parseListeners(out)
	want := map[int]string{5173: "127.0.0.1", 3000: "127.0.0.1", 8080: "127.0.0.1", 4000: "::1", 9229: "127.0.0.1", 9222: "127.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p, h := range want {
		if got[p].Host != h {
			t.Errorf("port %d host %q, want %q", p, got[p].Host, h)
		}
	}
}

// fakeForwarder drives the forwarder's logic with a scripted guest and a
// recorded ssh -O.
func fakeForwarder(taken map[int]bool) (*forwarder, *[]string, *[]string, *string) {
	var ctl, said []string
	ss := ""
	f := &forwarder{slug: "izma", id: "t", fwd: map[int]forwardEntry{}, failed: map[int]time.Time{}}
	f.localFree = func(p int) bool { return !taken[p] }
	f.say = func(m string) { said = append(said, m) }
	f.listeners = func(context.Context) (string, error) { return ss, nil }
	f.ctl = func(_ context.Context, op, spec string) error { ctl = append(ctl, op+" "+spec); return nil }
	return f, &ctl, &said, &ss
}

func TestForwarderFollowsTheGuest(t *testing.T) {
	ctx := context.Background()
	f, ctl, said, ss := fakeForwarder(map[int]bool{3000: true, 1355: true})
	*ss = "LISTEN 0 511 0.0.0.0:5173 0.0.0.0:*\nLISTEN 0 511 127.0.0.1:3000 0.0.0.0:*\nLISTEN 0 511 127.0.0.1:1355 0.0.0.0:*\n"
	if changed, err := f.sync(ctx); err != nil || !changed {
		t.Fatalf("sync: %v %v", changed, err)
	}
	wantCtl := []string{"forward 1356:127.0.0.1:1355", "forward 3001:127.0.0.1:3000", "forward 5173:127.0.0.1:5173"}
	if strings.Join(*ctl, "|") != strings.Join(wantCtl, "|") {
		t.Errorf("ssh -O = %v, want %v", *ctl, wantCtl)
	}
	// A remap is said at once; a same-number forward once a poll finds
	// nothing new after it (I-324).
	f.announce(time.Now())
	f.announce(time.Now().Add(forwardQuiet))
	wantSaid := []string{
		"1355 is taken on your laptop (portless?); izma's portless is on localhost:1356",
		"⇄ localhost:3001 → :3000 (3000 is taken on your laptop)",
		"⇄ localhost:5173 → :5173",
	}
	if strings.Join(*said, "|") != strings.Join(wantSaid, "|") {
		t.Errorf("messages = %q", *said)
	}
	// Nothing changed: no ssh, no message.
	*ctl, *said = nil, nil
	if changed, _ := f.sync(ctx); changed || len(*ctl) != 0 || len(*said) != 0 {
		t.Errorf("idle sync: %v %v %v", changed, *ctl, *said)
	}
	// vite stops: its forward is cancelled on the next poll.
	*ss = "LISTEN 0 511 127.0.0.1:3000 0.0.0.0:*\nLISTEN 0 511 127.0.0.1:1355 0.0.0.0:*\n"
	if changed, _ := f.sync(ctx); !changed || strings.Join(*ctl, "|") != "cancel 5173:127.0.0.1:5173" {
		t.Errorf("after vite stopped: %v", *ctl)
	}
	if got := f.guestPorts(); fmt.Sprint(got) != "[1355 3000]" {
		t.Errorf("ports = %v", got)
	}
	*ctl = nil
	f.closeAll(ctx)
	if len(*ctl) != 2 || len(f.fwd) != 0 {
		t.Errorf("closeAll: %v", *ctl)
	}
}

// End to end on a real ControlMaster to the local sshd harness: a server
// the "guest" starts is reachable on the laptop within two seconds of the
// poll with no command, the tmux status bar lists it, and when the
// server goes the forward and the status go too.
func TestForwardOverTheControlMaster(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh client")
	}
	f := newSyncFixture(t)
	ctx := context.Background()
	cmDir, err := os.MkdirTemp("", "cm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cmDir) })
	mux := sshTarget{Args: append([]string{"-o", "ControlMaster=auto", "-o", "ControlPath=" + filepath.Join(cmDir, "cm-%C"), "-o", "ControlPersist=60"}, f.target.Args...)}
	t.Cleanup(func() { _ = exec.Command("ssh", append([]string{"-O", "exit"}, mux.Args...)...).Run() })
	if err := waitForSSH(ctx, mux, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runSSH(ctx, mux, "tmux new-session -d -s "+testSlug, nil); err != nil {
		t.Fatal(err)
	}

	// The guest's dev server. The guest is this machine, so its port is
	// also taken on the "laptop" and the forward lands on the next one.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "hello from vite") })}
	go func() { _ = srv.Serve(ln) }()
	port := ln.Addr().(*net.TCPAddr).Port

	var mu sync.Mutex
	var said []string
	fw := newForwarder(mux, testSlug, func(m string) { mu.Lock(); said = append(said, m); mu.Unlock() })
	var listening sync.Mutex
	up := true
	fw.listeners = func(context.Context) (string, error) {
		listening.Lock()
		defer listening.Unlock()
		if !up {
			return "", nil
		}
		return fmt.Sprintf("LISTEN 0 511 127.0.0.1:%d 0.0.0.0:*\n", port), nil
	}
	stop := make(chan struct{})
	alive := func() bool {
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}
	done := make(chan struct{})
	go func() { runForwards(ctx, fw, alive); close(done) }()

	var local int
	start := time.Now()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range said {
			if _, err := fmt.Sscanf(m, "⇄ localhost:%d", &local); err == nil {
				return true
			}
		}
		return false
	})
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", local))
	if err != nil {
		t.Fatalf("GET through the forward: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	t.Logf("forward up in %s: %q; messages %q", time.Since(start).Round(time.Millisecond), body, said)
	if string(body) != "hello from vite" {
		t.Fatalf("body = %q", body)
	}
	if local == port || !strings.Contains(said[0], "is taken on your laptop") {
		t.Errorf("local %d for guest %d, message %q", local, port, said[0])
	}
	var bar string
	waitFor(t, func() bool {
		out, _ := runSSH(ctx, mux, "tmux show-options -v -t "+testSlug+" status-right", nil)
		bar = strings.TrimSpace(string(out))
		return strings.HasPrefix(bar, fmt.Sprintf("⇄ %d │", port))
	})
	t.Logf("status-right: %q", bar)

	// The connection drops and the attach connects again (I-469): the new
	// master has none of the old one's forwards until the helper sees the
	// new pid and adds them back, on the same laptop port, unannounced.
	if out, err := exec.Command("ssh", append([]string{"-O", "exit"}, mux.Args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ssh -O exit: %v %s", err, out)
	}
	if _, err := runSSH(ctx, mux, "true", nil); err != nil { // a new master
		t.Fatal(err)
	}
	mu.Lock()
	announced := len(said)
	mu.Unlock()
	waitFor(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", local))
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	})
	mu.Lock()
	if len(said) != announced {
		t.Errorf("re-adding announced again: %q", said[announced:])
	}
	mu.Unlock()

	// The server stops: within two seconds the forward is gone.
	listening.Lock()
	up = false
	listening.Unlock()
	_ = srv.Close()
	gone := time.Now()
	waitFor(t, func() bool {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return false
		}
		return true
	})
	if d := time.Since(gone); d > 2500*time.Millisecond {
		t.Errorf("forward cancelled after %s, want under 2 s", d)
	}
	waitFor(t, func() bool {
		out, _ := runSSH(ctx, mux, "tmux show-options -t "+testSlug, nil)
		return !strings.Contains(string(out), "status-right ")
	})
	close(stop)
	<-done
}

// Two CLIs attached to one project (two laptops): each forwards on its own
// laptop and the status bar shows the union; the bar goes back to the
// global one only when the last of them is gone.
func TestForwardStatusIsTheUnionOfAttachedCLIs(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-session -d -s "+testSlug, nil); err != nil {
		t.Fatal(err)
	}
	a, _, _, ssA := fakeForwarder(nil)
	b, _, _, ssB := fakeForwarder(nil)
	for _, fw := range []*forwarder{a, b} {
		fw.t, fw.slug = f.target, testSlug
	}
	a.id, b.id = "laptop-a", "laptop-b"
	*ssA = "LISTEN 0 1 127.0.0.1:3000 0.0.0.0:*\n"
	*ssB = "LISTEN 0 1 127.0.0.1:5173 0.0.0.0:*\n"
	bar := func() string {
		out, _ := runSSH(ctx, f.target, "tmux show-options -t "+testSlug+": status-right 2>/dev/null", nil)
		return strings.TrimSpace(string(out))
	}
	for _, fw := range []*forwarder{a, b} {
		if _, err := fw.sync(ctx); err != nil {
			t.Fatal(err)
		}
		if err := fw.publish(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := bar(); !strings.HasPrefix(got, `status-right "⇄ 3000 5173 │`) {
		t.Errorf("bar with both = %q", got)
	}
	a.closeAll(ctx)
	_ = a.publish(ctx)
	if got := bar(); !strings.HasPrefix(got, `status-right "⇄ 5173 │`) {
		t.Errorf("bar after a left = %q", got)
	}
	b.closeAll(ctx)
	_ = b.publish(ctx)
	if got := bar(); got != "" {
		t.Errorf("bar after both left = %q, want the session option unset", got)
	}
}

// The helper as it really runs: `repose __session` started beside a parent
// that then exits (the ssh the CLI became). It must exit on its own within
// a couple of polls, cancelling what it held.
func TestSessionHelperEndsWithItsParent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	f := newSyncFixture(t)
	bin := filepath.Join(t.TempDir(), "repose")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/repose")
	build.Env = append(filterTestEnv(os.Environ(), "HOME", "XDG_CONFIG_HOME"), "HOME="+realHome, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	opts, _ := json.Marshal(sessionOptions{Slug: testSlug, Target: f.target.Args, Forward: true})
	pidFile := filepath.Join(t.TempDir(), "pid")
	// The parent starts the helper the way spawnDetached does (own process
	// group, stdio closed), records its pid, and exits after a second.
	parent := exec.Command("sh", "-c", `setsid "$0" __session </dev/null >/dev/null 2>&1 & echo $! > "$1"; sleep 1`, bin, pidFile)
	parent.Env = append(os.Environ(), sessionEnv+"="+base64.StdEncoding.EncodeToString(opts))
	if err := parent.Run(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(string(b))
	start := time.Now()
	waitFor(t, func() bool { return exec.Command("kill", "-0", pid).Run() != nil })
	took := time.Since(start)
	t.Logf("helper %s exited %s after its parent", pid, took.Round(100*time.Millisecond))
	if took > 4*time.Second {
		t.Errorf("helper took %s to notice its parent was gone", took)
	}
}

// Many ports at once (a test suite's servers) are one message, not a
// four-second message each; one that went before the message is not
// named (I-324).
func TestForwarderCoalescesMessages(t *testing.T) {
	ctx := context.Background()
	f, _, said, ss := fakeForwarder(nil)
	listen := func(ports ...int) {
		var b strings.Builder
		for _, p := range ports {
			fmt.Fprintf(&b, "LISTEN 0 511 127.0.0.1:%d 0.0.0.0:*\n", p)
		}
		*ss = b.String()
	}
	listen(3000, 3001, 3002)
	_, _ = f.sync(ctx)
	f.announce(time.Now())
	listen(3000, 3001, 3002, 3003, 3004, 3005, 3006, 3007, 3008, 3009, 3010)
	_, _ = f.sync(ctx)
	f.announce(time.Now()) // still coming: nothing yet
	if len(*said) != 0 {
		t.Fatalf("said before the burst ended: %q", *said)
	}
	listen(3000, 3001, 3002, 3003, 3004, 3005, 3006, 3007, 3008, 3010) // 3009 went
	_, _ = f.sync(ctx)
	f.announce(time.Now().Add(forwardQuiet))
	want := "⇄ 10 ports on localhost: 3000, 3001, 3002, 3003, 3004, 3005, 3006, 3007, …"
	if len(*said) != 1 || (*said)[0] != want {
		t.Fatalf("messages %q, want %q", *said, want)
	}
	// A burst that never pauses is still said after forwardCoalesceMax.
	*said = nil
	listen(4000)
	_, _ = f.sync(ctx)
	f.pendingFirst = time.Now().Add(-forwardCoalesceMax)
	f.announce(time.Now())
	if len(*said) != 1 || (*said)[0] != "⇄ localhost:4000 → :4000" {
		t.Fatalf("capped wait: %q", *said)
	}
}
