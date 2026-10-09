package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestOpenForwardReachesWhereTheServerListens is I-261: `repose open PORT`
// forwards to ::1 when the guest's server listens only there (Vite's
// default on some setups), and to 127.0.0.1 for 127.0.0.1, 0.0.0.0, :: or
// nothing yet.
func TestOpenForwardReachesWhereTheServerListens(t *testing.T) {
	cases := []struct {
		name, ss  string
		wantSpec  string
		wantFound bool
	}{
		{"ipv4 loopback", "LISTEN 0 511 127.0.0.1:5173 0.0.0.0:*\n", "127.0.0.1:5173:127.0.0.1:5173", true},
		{"ipv6 loopback only", "LISTEN 0 511 [::1]:5173 [::]:*\n", "127.0.0.1:5173:[::1]:5173", true},
		{"ipv4 wildcard", "LISTEN 0 4096 0.0.0.0:5173 0.0.0.0:*\n", "127.0.0.1:5173:127.0.0.1:5173", true},
		{"ipv6 wildcard", "LISTEN 0 4096 [::]:5173 [::]:*\n", "127.0.0.1:5173:127.0.0.1:5173", true},
		{"star", "LISTEN 0 4096 *:5173 *:*\n", "127.0.0.1:5173:127.0.0.1:5173", true},
		{"both, ipv4 wins", "LISTEN 0 511 [::1]:5173 [::]:*\nLISTEN 0 511 127.0.0.1:5173 0.0.0.0:*\n", "127.0.0.1:5173:127.0.0.1:5173", true},
		{"other port only", "LISTEN 0 511 [::1]:3000 [::]:*\n", "127.0.0.1:5173:127.0.0.1:5173", false},
		{"nothing", "", "127.0.0.1:5173:127.0.0.1:5173", false},
		{"not loopback", "LISTEN 0 511 10.64.4.2:5173 0.0.0.0:*\n", "127.0.0.1:5173:127.0.0.1:5173", false},
	}
	for _, c := range cases {
		l, found := listenerFor(c.ss, 5173)
		if got := openForwardSpec(5173, l); got != c.wantSpec || found != c.wantFound {
			t.Errorf("%s: spec %q found %v, want %q %v", c.name, got, found, c.wantSpec, c.wantFound)
		}
	}
	// `repose open 6080` names the port, so it is not filtered the way
	// auto-forward filters the platform's ports.
	if l, found := listenerFor("LISTEN 0 5 127.0.0.1:6080 0.0.0.0:*\n", 6080); !found || l.Host != "127.0.0.1" {
		t.Errorf("6080: %+v %v", l, found)
	}
}

// TestOpenForwardsToAnIPv6OnlyServer runs the forward `repose open` builds
// through a real sshd, against a server that listens on ::1 alone: ss over
// SSH finds it, and the forward reaches it, where the forward before I-261
// (to 127.0.0.1) is refused.
func TestOpenForwardsToAnIPv6OnlyServer(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh client")
	}
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "hello from vite on ::1") })}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	f := newSyncFixture(t)
	ctx := context.Background()
	if err := waitForSSH(ctx, f.target, sshWaitTimeout, nil, nil); err != nil {
		t.Fatal(err)
	}
	out, err := runSSH(ctx, f.target, "ss -Hltn", nil)
	if err != nil {
		t.Fatal(err)
	}
	l, found := listenerFor(string(out), port)
	if !found || l.Host != "::1" {
		t.Fatalf("listener for %d: %+v found=%v", port, l, found)
	}

	get := func(spec string, local int, wait time.Duration) (string, error) {
		cmd := exec.Command("ssh", append([]string{"-N", "-o", "ExitOnForwardFailure=yes", "-L", spec}, f.target.Args...)...)
		if err := cmd.Start(); err != nil {
			return "", err
		}
		defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
		url := fmt.Sprintf("http://127.0.0.1:%d/", local)
		var last error
		for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			resp, err := http.Get(url)
			if err != nil {
				last = err
				continue
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return string(b), nil
		}
		return "", last
	}
	local, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	body, err := get(openForwardSpec(local, l), local, 10*time.Second)
	if err != nil || body != "hello from vite on ::1" {
		t.Fatalf("forward to ::1: %q %v", body, err)
	}
	old, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	if body, err := get(fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", old, port), old, 2*time.Second); err == nil && body == "hello from vite on ::1" {
		t.Fatal("the old 127.0.0.1 forward reached a ::1-only server; this test proves nothing")
	}
}

// TestPickLocalPortRemapsABusyPort is I-261 for `repose browser` (and
// `open PORT`): a taken laptop port is swapped for a free one, and said.
func TestPickLocalPortRemapsABusyPort(t *testing.T) {
	var errOut strings.Builder
	e := &Env{ErrOut: &errOut}
	got, err := pickLocalPort(e, desktopPort, func(int) bool { return true }, func() (int, error) {
		t.Fatal("picked another port with 6080 free")
		return 0, nil
	})
	if err != nil || got != 6080 || errOut.Len() != 0 {
		t.Fatalf("free: %d %v %q", got, err, errOut.String())
	}
	got, err = pickLocalPort(e, desktopPort, func(int) bool { return false }, func() (int, error) { return 51234, nil })
	if err != nil || got != 51234 || errOut.String() != "port 6080 is taken; forwarding to 51234 instead\n" {
		t.Fatalf("busy: %d %v %q", got, err, errOut.String())
	}
	// And against a real listener on this machine.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	busy := l.Addr().(*net.TCPAddr).Port
	errOut.Reset()
	got, err = pickLocalPort(e, busy, laptopPortFree, freePort)
	if err != nil || got == busy || got == 0 || !strings.Contains(errOut.String(), "is taken") {
		t.Fatalf("real busy port %d: got %d %v %q", busy, got, err, errOut.String())
	}
}
