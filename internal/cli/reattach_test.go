//go:build !windows

package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// fakeProbe answers the reattacher's probes from a script of errors, then
// succeeds.
type fakeProbe struct {
	mu    sync.Mutex
	errs  []error
	calls int
}

func (p *fakeProbe) probe(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(p.errs) == 0 {
		return nil
	}
	err := p.errs[0]
	p.errs = p.errs[1:]
	return err
}

func testReattacher(p *fakeProbe, renew func(context.Context) error) *reattacher {
	return &reattacher{slug: "todo-app", probe: p.probe, renew: renew, wait: 2 * time.Second, interval: 10 * time.Millisecond}
}

func dropped(stderr string) error {
	return &sshError{ExitCode: 255, Stderr: stderr}
}

// I-469: a dropped attach says so, waits through failed probes, and
// attaches again once the machine answers.
func TestReattachWaitsForTheMachine(t *testing.T) {
	p := &fakeProbe{errs: []error{
		dropped("ssh: connect to host ssh.repose.herakraft.co port 22: Connection refused"),
		dropped("repose gateway: gateway busy"),
	}}
	out := &syncBuffer{}
	if !testReattacher(p, nil).again(context.Background(), out, nil, time.Hour, false) {
		t.Fatalf("did not reattach; said %q", out.b.String())
	}
	if p.calls != 3 {
		t.Errorf("probes = %d, want 3", p.calls)
	}
	if got := out.b.String(); got != "\r\nrepose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.\r\n" {
		t.Errorf("said %q", got)
	}
}

// A stopped machine will not answer however long the wait: the gateway's
// line is shown and the attach ends.
func TestReattachStopsOnAStoppedMachine(t *testing.T) {
	p := &fakeProbe{errs: []error{dropped("todo-app is stopped; run `repose start todo-app`")}}
	out := &syncBuffer{}
	if testReattacher(p, nil).again(context.Background(), out, nil, time.Hour, false) {
		t.Fatal("reattached to a stopped machine")
	}
	if !strings.Contains(out.b.String(), "todo-app is stopped; run `repose start todo-app`") {
		t.Errorf("said %q", out.b.String())
	}
}

// A relay ended by its certificate's expiry (I-436) gets one renewal.
func TestReattachRenewsAnExpiredCertificateOnce(t *testing.T) {
	p := &fakeProbe{errs: []error{
		dropped("permission denied (certificate expired)"),
		dropped("permission denied (certificate expired)"),
	}}
	renewals := 0
	r := testReattacher(p, func(context.Context) error { renewals++; return nil })
	out := &syncBuffer{}
	if r.again(context.Background(), out, nil, time.Hour, false) {
		t.Fatal("reattached on a certificate refused after a renewal")
	}
	if renewals != 1 || !strings.Contains(out.String(), "certificate expired") {
		t.Errorf("renewals = %d, said %q", renewals, out.String())
	}

	// One renewal fixes it.
	p = &fakeProbe{errs: []error{dropped("permission denied (certificate expired)")}}
	renewals = 0
	if !testReattacher(p, func(context.Context) error { renewals++; return nil }).again(context.Background(), &syncBuffer{}, nil, time.Hour, false) || renewals != 1 {
		t.Fatalf("did not reattach after one renewal (renewals %d)", renewals)
	}

	// A renewal that fails (api away) is tried again on the next refusal.
	p = &fakeProbe{errs: []error{dropped("permission denied (certificate expired)"), dropped("permission denied (certificate expired)")}}
	calls := 0
	renew := func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("api unreachable")
		}
		return nil
	}
	if !testReattacher(p, renew).again(context.Background(), &syncBuffer{}, nil, time.Hour, false) || calls != 2 {
		t.Fatalf("did not retry a failed renewal (calls %d)", calls)
	}
}

// Ctrl-C (a byte, the terminal is raw) stops the wait.
func TestReattachCtrlCStops(t *testing.T) {
	p := &fakeProbe{}
	for i := 0; i < 1000; i++ {
		p.errs = append(p.errs, dropped("Connection refused"))
	}
	in := make(chan []byte, 1)
	in <- []byte("x\x03")
	out := &syncBuffer{}
	if testReattacher(p, nil).again(context.Background(), out, in, time.Hour, false) {
		t.Fatal("reattached after Ctrl-C")
	}
	if !strings.Contains(out.b.String(), "stopped reconnecting. `repose attach todo-app` attaches again.") {
		t.Errorf("said %q", out.b.String())
	}
}

// Past the wait, the attach ends with a line saying how to come back.
func TestReattachGivesUp(t *testing.T) {
	p := &fakeProbe{}
	for i := 0; i < 1000; i++ {
		p.errs = append(p.errs, dropped("Connection timed out"))
	}
	r := testReattacher(p, nil)
	r.wait = 100 * time.Millisecond
	out := &syncBuffer{}
	if r.again(context.Background(), out, nil, time.Hour, false) {
		t.Fatal("reattached")
	}
	if !strings.Contains(out.b.String(), "could not reach todo-app for") {
		t.Errorf("said %q", out.b.String())
	}
}

// An attach that drops again right after a reattach is not retried for
// ever.
func TestReattachFlapStops(t *testing.T) {
	p := &fakeProbe{}
	out := &syncBuffer{}
	if testReattacher(p, nil).again(context.Background(), out, nil, time.Second, true) {
		t.Fatal("reattached a flapping connection")
	}
	if p.calls != 0 {
		t.Errorf("probed %d times", p.calls)
	}
	if testReattacher(p, nil).again(context.Background(), &syncBuffer{}, nil, time.Hour, true) != true {
		t.Fatal("a reattach that held for an hour may drop and come back again")
	}
}

// A cancelled command (Ctrl-C under `repose open`) ends the wait quietly.
func TestReattachParentCancelled(t *testing.T) {
	p := &fakeProbe{}
	for i := 0; i < 1000; i++ {
		p.errs = append(p.errs, errors.New("refused"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	out := &syncBuffer{}
	if testReattacher(p, nil).again(ctx, out, nil, time.Hour, false) {
		t.Fatal("reattached after cancel")
	}
	if strings.Contains(out.b.String(), "could not reach") {
		t.Errorf("said %q", out.b.String())
	}
}

func TestWaitWords(t *testing.T) {
	for d, want := range map[time.Duration]string{2 * time.Minute: "2 minutes", time.Minute: "a minute", 5 * time.Second: "5 seconds"} {
		if got := waitWords(d); got != want {
			t.Errorf("waitWords(%s) = %q, want %q", d, got, want)
		}
	}
}

// cutProxy stands between ssh and the fake guest the way the edge does:
// cut ends every connection through it, and while it is down a new one
// is closed at once.
type cutProxy struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	conns  []net.Conn
	down   bool
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close(); p.cut(false) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			down := p.down
			p.mu.Unlock()
			if down {
				_ = c.Close()
				continue
			}
			g, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, g)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(g, c); _ = g.Close() }()
			go func() { _, _ = io.Copy(c, g); _ = c.Close() }()
		}
	}()
	return p
}

// cut ends every open connection; down says whether new ones are refused
// until the next cut(false).
func (p *cutProxy) cut(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
	p.down = down
}

func (p *cutProxy) port() string {
	_, port, _ := net.SplitHostPort(p.ln.Addr().String())
	return port
}

// I-469, end to end: real ssh on a pty under the attach loop, to the fake
// guest's tmux through a proxy. The proxy cuts the connection and refuses
// new ones for two seconds, as an edge restart did; the loop says so,
// waits, attaches again to the same tmux session, and what is typed
// after reaches the same program. A detach ends the loop with status 0.
func TestAttachLoopReattachesAfterADrop(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("needs script(1) to give the fake guest's tmux client a terminal")
	}
	f, _ := pasteFixture(t)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n agent "+shQuote(`stty -icanon -echo; exec cat -v`), nil); err != nil {
		t.Fatal(err)
	}
	// The same target, through the proxy.
	host, port, _ := strings.Cut(f.guest.Addr, ":")
	proxy := newCutProxy(t, host+":"+port)
	via := sshTarget{Args: append([]string{}, f.target.Args...)}
	for i, a := range via.Args {
		if a == "-p" {
			via.Args[i+1] = proxy.port()
		}
	}
	attach := func(window string) []string {
		target := testSlug
		if window != "" {
			target += ":" + window
		}
		remote := "TERM=xterm-256color script -qfc " + shQuote("stty cols 200 rows 50; tmux attach -t "+target) + " /dev/null"
		return append(append([]string{"-t"}, via.Args...), remote)
	}
	re := newReattacher(via, testSlug, nil)
	re.interval = 200 * time.Millisecond
	re.args = attach("")

	stdinR, stdinW := io.Pipe()
	input := readInput(stdinR)
	screen := &syncBuffer{}
	h := newDropHandler(f.target, testSlug, f.local)
	done := make(chan error, 1)
	go func() {
		_, err := attachLoop(attach("agent"), h, re, func() <-chan []byte { return input }, screen, func() (*pty.Winsize, error) {
			return &pty.Winsize{Rows: 50, Cols: 200}, nil
		})
		done <- err
	}()
	t.Cleanup(func() { _ = stdinW.Close() })
	clientAttached := func() {
		t.Helper()
		waitFor(t, func() bool {
			out, _ := runSSH(ctx, f.target, "tmux list-clients -t "+testSlug, nil)
			return strings.TrimSpace(string(out)) != ""
		})
	}
	typeIn := func(s string) {
		t.Helper()
		if _, err := stdinW.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	clientAttached()
	typeIn("before")
	waitPane(t, f, "agent", "before")

	// The edge goes away for two seconds.
	proxy.cut(true)
	waitFor(t, func() bool { return strings.Contains(screen.String(), "lost the connection to "+testSlug) })
	time.Sleep(2 * time.Second)
	proxy.cut(false)

	// Keys typed while it reconnects are dropped, so wait for the new
	// attach to draw tmux's status line.
	waitFor(t, func() bool {
		sc := screen.String()
		i := strings.Index(sc, "Reconnecting; Ctrl-C stops.")
		return i >= 0 && strings.Contains(sc[i:], "agent*")
	})
	typeIn("after")
	// The same program got both. (The ^D between them is script(1), the
	// fixture's stand-in for sshd's pty, passing its lost client's EOF on.)
	pane := waitPane(t, f, "agent", "after")
	if !strings.Contains(pane, "before") {
		t.Fatalf("pane lost what came before the drop:\n%s", pane)
	}
	if !strings.Contains(screen.String(), "lost the connection to "+testSlug+". Reconnecting; Ctrl-C stops.") {
		t.Errorf("screen: %q", tail(screen.String(), 300))
	}

	typeIn("\x02d")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop after detach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("loop did not end after the detach")
	}
}
