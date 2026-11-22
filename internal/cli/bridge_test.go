package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/testguest"
)

func TestChromeUserDataDir(t *testing.T) {
	env := map[string]string{"LOCALAPPDATA": `C:\Users\dev\AppData\Local`}
	getenv := func(k string) string { return env[k] }
	cases := map[string]string{
		"darwin":  "/home/dev/Library/Application Support/Google/Chrome",
		"linux":   "/home/dev/.config/google-chrome",
		"windows": filepath.Join(`C:\Users\dev\AppData\Local`, "Google", "Chrome", "User Data"),
		"plan9":   "",
	}
	for goos, want := range cases {
		if got := chromeUserDataDir(goos, "/home/dev", getenv); got != want {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
	env["XDG_CONFIG_HOME"] = "/xdg"
	if got := chromeUserDataDir("linux", "/home/dev", getenv); got != "/xdg/google-chrome" {
		t.Errorf("linux with XDG_CONFIG_HOME: %q", got)
	}
}

func TestReadDevToolsActivePort(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := readDevToolsActivePort(dir); err == nil {
		t.Error("no file: want an error")
	}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("53211\n/devtools/browser/0b1e2d3c-4f5a-6b7c-8d9e-0f1a2b3c4d5e\n")
	port, path, err := readDevToolsActivePort(dir)
	if err != nil || port != 53211 || path != "/devtools/browser/0b1e2d3c-4f5a-6b7c-8d9e-0f1a2b3c4d5e" {
		t.Errorf("got %d %q %v", port, path, err)
	}
	for _, bad := range []string{"", "53211\n", "abc\n/devtools/browser/x\n", "0\n/x\n", "53211\ndevtools/browser/x\n"} {
		write(bad)
		if _, _, err := readDevToolsActivePort(dir); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// A browser started with --remote-debugging-port answers /json/version;
// Chrome's own switch answers 404 to every HTTP request; a port nothing
// listens on is not a DevTools server at all.
func TestDevToolsVersionAndChromeAtSwitch(t *testing.T) {
	flag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"Browser":"Chrome/144.0.7559.1","webSocketDebuggerUrl":"ws://%s/devtools/browser/flag-id"}`, r.Host)
	}))
	defer flag.Close()
	switchOnly := httptest.NewServer(http.NotFoundHandler())
	defer switchOnly.Close()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()

	ctx := context.Background()
	if found, browser, path := devToolsVersion(ctx, strings.TrimPrefix(flag.URL, "http://")); !found || browser != "Chrome/144.0.7559.1" || path != "/devtools/browser/flag-id" {
		t.Errorf("flag server: %v %q %q", found, browser, path)
	}
	if found, browser, path := devToolsVersion(ctx, strings.TrimPrefix(switchOnly.URL, "http://")); !found || browser != "" || path != "" {
		t.Errorf("switch server: %v %q %q", found, browser, path)
	}
	if found, _, _ := devToolsVersion(ctx, closedAddr); found {
		t.Error("closed port: found")
	}

	dir := t.TempDir()
	writePort := func(addr string) {
		_, port, _ := net.SplitHostPort(addr)
		if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/switch-id\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := chromeAtSwitch(ctx, dir); ok {
		t.Error("no file: found")
	}
	writePort(closedAddr)
	if _, ok := chromeAtSwitch(ctx, dir); ok {
		t.Error("stale file, nothing listening: found")
	}
	writePort(strings.TrimPrefix(switchOnly.URL, "http://"))
	c, ok := chromeAtSwitch(ctx, dir)
	if !ok || !c.Switch || c.Path != "/devtools/browser/switch-id" || c.Name() != "Chrome" {
		t.Errorf("switch: %v %+v name %q", ok, c, c.Name())
	}
	writePort(strings.TrimPrefix(flag.URL, "http://"))
	c, ok = chromeAtSwitch(ctx, dir)
	if !ok || c.Switch || c.Path != "/devtools/browser/flag-id" || c.Name() != "Chrome 144" {
		t.Errorf("flag: %v %+v name %q", ok, c, c.Name())
	}

	if _, err := bridgeFromCDP(ctx, "9222"); err == nil {
		t.Error("--cdp 9222: want a usage error")
	}
	if _, err := bridgeFromCDP(ctx, "http://"+closedAddr); err == nil {
		t.Error("--cdp on a closed port: want an error")
	}
	if _, err := bridgeFromCDP(ctx, switchOnly.URL); err == nil {
		t.Error("--cdp on a server without /json/version: want an error")
	}
	c, err = bridgeFromCDP(ctx, flag.URL)
	if err != nil || c.Path != "/devtools/browser/flag-id" || c.Switch {
		t.Errorf("--cdp: %+v %v", c, err)
	}
}

func TestBridgeSSHArgs(t *testing.T) {
	args := bridgeSSHArgs(sshTarget{Args: []string{"todo-app.repose"}}, 51234)
	s := strings.Join(args, " ")
	for _, want := range []string{"-o ExitOnForwardFailure=yes", "-R 127.0.0.1:9226:127.0.0.1:51234", " todo-app.repose repose-guest-profile browser bridge hold"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q lacks %q", s, want)
		}
	}
	if strings.Index(s, "-R ") > strings.Index(s, "todo-app.repose") {
		t.Errorf("options after the host: %q", s)
	}
}

// bridgeGuest is a fake guest with a fake repose-guest-profile on its
// PATH that logs every call and, for `hold`, says on and waits for its
// stdin to close, as the real one does.
func bridgeGuest(t *testing.T) (target sshTarget, log string) {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh")
	}
	home := t.TempDir()
	privPath, pub := cachedClientKey(t)
	guest, err := testguest.New(home, pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(guest.Close)
	bin := t.TempDir()
	log = filepath.Join(bin, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$*\" in\n  'browser bridge hold') echo on; cat >/dev/null; echo \"$* ended\" >> " + log + " ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "repose-guest-profile"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	host, port, _ := strings.Cut(guest.Addr, ":")
	target = sshTarget{Args: []string{
		"-p", port,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-i", privPath,
		"guest@" + host,
	}}
	return target, log
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

// The whole bridge against a fake guest: the tunnel is up once the hold
// says on, a client on the guest's side reaches the laptop's DevTools
// server through it with the websocket URL leading back through the
// guest's endpoint, and ending the context ends the hold (its stdin
// closes) and the tunnel.
func TestBridgeEndToEnd(t *testing.T) {
	if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", bridgeGuestPort)); err != nil {
		t.Skipf("port %d is taken on this machine", bridgeGuestPort)
	} else {
		_ = l.Close()
	}
	target, log := bridgeGuest(t)
	addr, got := fakeDevTools(t)
	front, err := startCDPFront(laptopChrome{Addr: addr, Path: "/devtools/browser/e2e", Browser: "Chrome/144.0.1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridgeRelease(ctx, target)
	ready := make(chan struct{})
	held := make(chan error, 1)
	go func() { held <- holdBridge(ctx, target, front.Port(), func() { close(ready) }) }()
	select {
	case <-ready:
	case err := <-held:
		t.Fatalf("the hold ended before it was ready: %v (log: %s)", err, readLog(t, log))
	case <-time.After(20 * time.Second):
		t.Fatalf("the hold never said on (log: %s)", readLog(t, log))
	}

	guestSide := fmt.Sprintf("127.0.0.1:%d", bridgeGuestPort)
	c, err := net.Dial("tcp", guestSide)
	if err != nil {
		t.Fatalf("the guest's side of the tunnel: %v", err)
	}
	_, _ = fmt.Fprintf(c, "GET /json/version HTTP/1.1\r\nHost: %s\r\n\r\n", bridgeEndpoint)
	res, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = c.Close()
	var v map[string]string
	if err := json.Unmarshal(body, &v); err != nil || v["webSocketDebuggerUrl"] != "ws://"+bridgeEndpoint+"/devtools/browser/e2e" || v["Browser"] != "Chrome/144.0.1" {
		t.Errorf("through the tunnel: %s %v", body, err)
	}
	c, err = net.Dial("tcp", guestSide)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "GET /devtools/browser/e2e HTTP/1.1\r\nHost: 127.0.0.1:9224\r\nUpgrade: websocket\r\n\r\n")
	select {
	case sent := <-got:
		if !strings.HasPrefix(sent, "GET /devtools/browser/e2e HTTP/1.1\r\n") {
			t.Errorf("DevTools got %q", sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the upgrade never reached the laptop's DevTools server")
	}
	if line, _ := bufio.NewReader(c).ReadString('\n'); line != "HTTP/1.1 101 Switching Protocols\r\n" {
		t.Errorf("got %q back through the tunnel", line)
	}
	_ = c.Close()

	cancel()
	select {
	case err := <-held:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("hold ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hold did not end with the context")
	}
	bridgeStop(target, "todo-app", "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		l := readLog(t, log)
		if strings.Contains(l, "browser bridge hold ended") && strings.Contains(l, "browser bridge stop") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log: %q", l)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if l := readLog(t, log); !strings.HasPrefix(l, "browser bridge release\nbrowser bridge hold\n") {
		t.Errorf("calls: %q", l)
	}
	if _, err := net.Dial("tcp", guestSide); err == nil {
		t.Error("the guest's side of the tunnel is still listening after the hold ended")
	}
}

// A port an earlier bridge still holds fails the tunnel with a message
// that says so, not with ssh's warning buried in a stack of others.
func TestHoldBridgeReportsAHeldPort(t *testing.T) {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", bridgeGuestPort))
	if err != nil {
		t.Skipf("port %d is taken on this machine", bridgeGuestPort)
	}
	defer func() { _ = l.Close() }()
	target, _ := bridgeGuest(t)
	err = holdBridge(context.Background(), target, 1, func() { t.Error("ready with the port held") })
	if !errors.Is(err, errBridgeHeld) {
		t.Errorf("err = %v, want errBridgeHeld", err)
	}
}
