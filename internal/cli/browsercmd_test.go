package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// TestBrowserURLCarriesThePasswordInTheFragment is I-292: the password is
// after the #, escaped, and nowhere a request would carry it.
func TestBrowserURLCarriesThePasswordInTheFragment(t *testing.T) {
	got := browserURL(6080, "a/b=c 8")
	want := "http://localhost:6080/#p=a%2Fb%3Dc+8"
	if got != want {
		t.Fatalf("browserURL = %q, want %q", got, want)
	}
	if strings.Contains(got, "?") {
		t.Fatalf("a query would reach the server: %q", got)
	}
	if got := browserURL(6081, "s3cr3tpw"); !strings.HasSuffix(got, "/#p=s3cr3tpw") || !strings.HasPrefix(got, "http://localhost:6081/") {
		t.Fatalf("browserURL = %q", got)
	}
}

// TestBrowserForwardArgs pins the ssh invocation of the background forward.
func TestBrowserForwardArgs(t *testing.T) {
	t.Setenv("REPOSE_TEST_GOOS", "linux")
	got := strings.Join(browserForwardArgs(sshTarget{Args: []string{"todo-app.repose"}}, 6080, 6080), " ")
	want := "-o ControlPath=none -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -L 127.0.0.1:6080:127.0.0.1:6080 todo-app.repose"
	if got != want {
		t.Fatalf("args = %q\nwant  %q", got, want)
	}
}

// viewerStandIn serves what the guest's web root serves at /healthz, on a
// free laptop port, and records the paths it was asked for.
func viewerStandIn(t *testing.T, healthy bool) (port int, paths *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		if r.URL.Path == "/healthz" && healthy {
			_, _ = io.WriteString(w, "repose desktop viewer ok\n")
			return
		}
		http.NotFound(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port, &seen
}

// TestViewerHealthyKnowsOurViewer: only repose's healthz counts; a port
// with another server, or nothing, is not a live forward.
func TestViewerHealthyKnowsOurViewer(t *testing.T) {
	ours, _ := viewerStandIn(t, true)
	other, _ := viewerStandIn(t, false)
	closed, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	if !viewerHealthy(ours) {
		t.Error("our viewer not seen as healthy")
	}
	if viewerHealthy(other) {
		t.Error("another server on the port counted as our viewer")
	}
	if viewerHealthy(closed) {
		t.Error("a closed port counted as our viewer")
	}
}

// fakeGuestProfile puts a `repose-guest-profile` on PATH that prints the
// boot's password for `desktop start` and records every call, standing in
// for the guest's script when the fake guest runs commands.
func fakeGuestProfile(t *testing.T, password string) (calls func() string) {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\ncase \"$2\" in start) echo 'Job started'; echo %s;; esac\n", rec, password)
	if err := os.WriteFile(filepath.Join(dir, "repose-guest-profile"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string { b, _ := os.ReadFile(rec); return string(b) }
}

// TestBrowserCmdWatchesReusesAndStops runs `repose browser` end to end
// against the fake api and the fake guest (a real sshd): the guest's
// helper is asked to start the desktop, a background ssh forward reaches a
// stand-in for the viewer on the guest's port, the one line printed
// carries the URL with the password in its fragment and nothing else
// carries the password, a second run reuses the live forward, and --stop
// ends the forward and asks the guest to stop the viewer.
func TestBrowserCmdWatchesReusesAndStops(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh client")
	}
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	if _, err := fake.CreateProject(testSlug, "small"); err != nil {
		t.Fatal(err)
	}
	calls := fakeGuestProfile(t, "s3cr3tpw")
	guestPort, seen := viewerStandIn(t, true)
	old := desktopGuestPort
	desktopGuestPort = guestPort
	t.Cleanup(func() { desktopGuestPort = old })

	ctx := context.Background()
	out, errOut := &strings.Builder{}, &strings.Builder{}
	f.env.Out, f.env.ErrOut = out, errOut
	if err := BrowserCmd(ctx, f.env, testSlug, BrowserOptions{NoOpen: true}); err != nil {
		t.Fatalf("repose browser: %v\nstderr: %s", err, errOut.String())
	}
	line := strings.TrimSpace(out.String())
	prefix := "Watching " + testSlug + "'s browser at http://localhost:"
	if !strings.HasPrefix(line, prefix) || !strings.Contains(line, "/#p=s3cr3tpw (the view sleeps after 30 idle minutes).") {
		t.Fatalf("printed %q", line)
	}
	if strings.Contains(errOut.String(), "s3cr3tpw") {
		t.Fatalf("the password reached stderr: %s", errOut.String())
	}
	if !strings.Contains(calls(), "desktop start") {
		t.Fatalf("the guest was not asked to start the desktop: %q", calls())
	}
	var port int
	if _, err := fmt.Sscanf(line[len(prefix):], "%d/", &port); err != nil {
		t.Fatalf("no port in %q: %v", line, err)
	}
	st, ok := readBrowserForward(f.env, testSlug)
	if !ok || st.Port != port || st.PID <= 0 {
		t.Fatalf("forward state = %+v ok=%v, want port %d and a pid", st, ok, port)
	}
	if !viewerHealthy(port) {
		t.Fatal("the forwarded port does not reach the viewer")
	}
	if len(*seen) == 0 || !strings.HasPrefix((*seen)[0], "/healthz") {
		t.Fatalf("the stand-in saw %v", *seen)
	}

	// A second run finds the live forward and does not start another.
	out.Reset()
	if err := BrowserCmd(ctx, f.env, testSlug, BrowserOptions{NoOpen: true}); err != nil {
		t.Fatalf("second repose browser: %v", err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("http://localhost:%d/#p=s3cr3tpw", port)) {
		t.Fatalf("second run printed %q, want the same port %d", out.String(), port)
	}
	if st2, _ := readBrowserForward(f.env, testSlug); st2 != st {
		t.Fatalf("second run replaced the forward: %+v then %+v", st, st2)
	}
	if n := strings.Count(calls(), "desktop start"); n != 2 {
		t.Fatalf("desktop start asked %d times, want 2:\n%s", n, calls())
	}

	// --stop: the guest's viewer is stopped, the forward is gone.
	out.Reset()
	if err := BrowserCmd(ctx, f.env, testSlug, BrowserOptions{Stop: true}); err != nil {
		t.Fatalf("repose browser --stop: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "Stopped watching "+testSlug+"'s browser." {
		t.Fatalf("printed %q", got)
	}
	if !strings.Contains(calls(), "desktop stop") {
		t.Fatalf("the guest was not asked to stop the viewer: %q", calls())
	}
	if _, ok := readBrowserForward(f.env, testSlug); ok {
		t.Fatal("forward state kept after --stop")
	}
	deadline := time.Now().Add(5 * time.Second)
	for viewerHealthy(port) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if viewerHealthy(port) {
		t.Fatal("the forward still answers after --stop")
	}
	if p, err := os.FindProcess(st.PID); err == nil {
		// On unix FindProcess always succeeds; a signal tells whether
		// the ssh is gone.
		if err := p.Signal(os.Signal(nil)); err == nil {
			t.Logf("pid %d still exists (may be a zombie until reaped)", st.PID)
		}
	}
}

// TestBrowserCmdReportsAForwardThatCannotStart: ssh exiting at once (a
// target nothing answers on) is one "Could not start the forward" error,
// not a 20-second wait.
func TestBrowserCmdReportsAForwardThatCannotStart(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh client")
	}
	closed, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	target := sshTarget{Args: []string{"-p", fmt.Sprint(closed), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "guest@127.0.0.1"}}
	local, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = startBrowserForward(context.Background(), target, local, 6080)
	if err == nil {
		t.Fatal("a forward to a closed port started")
	}
	if !strings.Contains(err.Error(), "Could not start the forward to the desktop") {
		t.Fatalf("error = %v", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatalf("took %s to notice ssh exiting", time.Since(started))
	}
}

// TestOpenDesktopIsTheOldNameOfBrowser: `repose open --desktop` and its
// --stop stay, hidden from help, and `--stop` alone is still a usage error.
func TestOpenDesktopIsTheOldNameOfBrowser(t *testing.T) {
	root := newRootCmd("test")
	open, _, err := root.Find([]string{"open"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"desktop", "stop"} {
		fl := open.Flags().Lookup(name)
		if fl == nil || !fl.Hidden {
			t.Errorf("open --%s: %+v, want a hidden flag", name, fl)
		}
	}
	browser, _, err := root.Find([]string{"browser"})
	if err != nil || browser.Name() != "browser" {
		t.Fatalf("repose browser: %v %v", browser, err)
	}
	for _, name := range []string{"stop", "no-open"} {
		if browser.Flags().Lookup(name) == nil {
			t.Errorf("repose browser has no --%s", name)
		}
	}
	if bridge, _, err := root.Find([]string{"browser", "bridge"}); err != nil || bridge.Name() != "bridge" {
		t.Fatalf("repose browser bridge went missing: %v %v", bridge, err)
	}
}
