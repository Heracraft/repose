package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestSystemdReloadAndRestart runs the gateway under the machine's own
// systemd with the unit settings of nix/edge (DECISIONS I-470, I-471):
// a socket unit holding the port, Type=notify, DynamicUser, and an
// ExecReload that hands over. It does what a NixOS switch does to a
// reloadIfChanged unit (rewrite the unit to name the new build,
// daemon-reload, reload) with a session open, then restarts the unit with
// a client waiting. It needs root and changes the running system's
// units under /run, so it runs only when asked:
//
//	go test -c -o /tmp/gw.test ./cmd/gateway
//	sudo GATEWAY_E2E_SYSTEMD=1 /tmp/gw.test -test.run TestSystemd -test.v
func TestSystemdReloadAndRestart(t *testing.T) {
	if os.Getenv("GATEWAY_E2E_SYSTEMD") != "1" || os.Getuid() != 0 {
		t.Skip("needs root and GATEWAY_E2E_SYSTEMD=1")
	}
	const (
		unit = "gw-e2e"
		base = "/run/gw-e2e-test" // not the unit's RuntimeDirectory, which a stop removes
	)
	e := newE2EIn(t, filepath.Join(base, "files"))
	// The unit's dynamic user reads the keys; a test key only.
	_ = filepath.Walk(e.dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
	_ = os.Chmod(base, 0o755)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	self := mustExe(t)
	exeA, exeB := filepath.Join(bin, "gateway-a"), filepath.Join(bin, "gateway-b")
	for _, p := range []string{exeA, exeB} {
		if out, err := exec.Command("cp", self, p).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v %s", err, out)
		}
	}
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	var env []string
	for _, kv := range e.env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "NOTIFY_SOCKET" || k == "GATEWAY_CONTROL" || k == "PATH" {
			continue
		}
		env = append(env, fmt.Sprintf("%q", kv))
	}
	env = append(env, `"GATEWAY_CONTROL=/run/`+unit+`/control.sock"`)
	writeUnit := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join("/run/systemd/system", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	socketUnit := fmt.Sprintf(`[Socket]
ListenStream=%s
FileDescriptorName=ssh
Service=%s.service
Backlog=4096
`, addr, unit)
	serviceUnit := func(exe string) string {
		return fmt.Sprintf(`[Unit]
Requires=%[1]s-ssh.socket
After=%[1]s-ssh.socket
[Service]
Environment=%[3]s
ExecStart=%[2]s serve
ExecReload=%[2]s handover
Type=notify
NotifyAccess=all
RuntimeDirectory=%[1]s
RuntimeDirectoryMode=0700
DynamicUser=true
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
Restart=always
RestartSec=1
`, unit, exe, strings.Join(env, " "))
	}
	systemctl := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("systemctl", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("systemctl %s: %v\n%s\n%s", strings.Join(args, " "), err, out, journal(unit))
		}
		return strings.TrimSpace(string(out))
	}
	mainPID := func() int {
		t.Helper()
		n, _ := strconv.Atoi(strings.TrimPrefix(systemctl("show", "-p", "MainPID", unit+".service"), "MainPID="))
		return n
	}
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", unit+".service", unit+"-ssh.socket").Run()
		_ = os.Remove("/run/systemd/system/" + unit + ".service")
		_ = os.Remove("/run/systemd/system/" + unit + "-ssh.socket")
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = os.RemoveAll(base)
	})

	writeUnit(unit+"-ssh.socket", socketUnit)
	writeUnit(unit+".service", serviceUnit(exeA))
	systemctl("daemon-reload")
	systemctl("start", unit+"-ssh.socket", unit+".service")
	pidA := mainPID()
	if exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pidA)); exe != exeA {
		t.Fatalf("main process runs %q", exe)
	}

	c1 := e.dial(addr)
	s1 := openEcho(t, c1)
	s1.roundTrip(t, "before the switch")

	// The switch: the unit now names build B; reloadIfChanged reloads.
	writeUnit(unit+".service", serviceUnit(exeB))
	systemctl("daemon-reload")
	systemctl("reload", unit+".service")
	pidB := mainPID()
	if pidB == pidA {
		t.Fatalf("MainPID still %d after the reload\n%s", pidA, journal(unit))
	}
	if exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pidB)); exe != exeB {
		t.Fatalf("new main process runs %q, want %q", exe, exeB)
	}
	if st := systemctl("is-active", unit+".service"); st != "active" {
		t.Fatalf("unit %s after the reload", st)
	}
	s1.roundTrip(t, "after the switch")
	c2 := e.dial(addr)
	if got := runCmd(t, c2, "new"); got != "new" {
		t.Fatalf("new connection ran %q", got)
	}
	// The old process ends with its last relay; the unit stays up on B.
	_ = c1.Close()
	waitGone(t, pidA, 10*time.Second)
	if st := systemctl("is-active", unit+".service"); st != "active" || mainPID() != pidB {
		t.Fatalf("after the old process ended: %s, MainPID %d\n%s", st, mainPID(), journal(unit))
	}
	_ = c2.Close()
	if j := journal(unit); strings.Contains(j, "notify barrier failed") {
		t.Errorf("the barrier failed:\n%s", j)
	}

	// The main process after a handover is not systemd's child. systemd
	// must still see it die (pidfd), and restart the unit.
	if err := syscall.Kill(pidB, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		pid := mainPID()
		st, _ := exec.Command("systemctl", "is-active", unit+".service").Output()
		if pid != 0 && pid != pidB && strings.TrimSpace(string(st)) == "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("systemd never restarted the unit after its handed-over main process died (MainPID %d)\n%s", pid, journal(unit))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got := runCmd(t, e.dial(addr), "restarted"); got != "restarted" {
		t.Fatalf("after the restart ran %q", got)
	}

	// A restart: a client connecting while no gateway runs is served by
	// the next one.
	got := make(chan error, 1)
	go func() {
		// Stop, then dial while nothing runs, then start.
		c, err := e.tryDial(addr, 30*time.Second)
		if err == nil {
			s, serr := c.NewSession()
			if serr == nil {
				out, rerr := s.Output("queued")
				if rerr == nil && strings.TrimSpace(string(out)) != "queued" {
					rerr = fmt.Errorf("ran %q", out)
				}
				err = rerr
			} else {
				err = serr
			}
		}
		got <- err
	}()
	systemctl("stop", unit+".service")
	time.Sleep(time.Second)
	systemctl("start", unit+".service")
	if err := <-got; err != nil {
		t.Fatalf("a connection made across the restart: %v\n%s", err, journal(unit))
	}
	// A client started while the service is stopped is queued too.
	systemctl("stop", unit+".service")
	queued := make(chan *ssh.Client, 1)
	go func() {
		c, err := e.tryDial(addr, 30*time.Second)
		if err != nil {
			t.Errorf("dial while stopped: %v", err)
		}
		queued <- c
	}()
	time.Sleep(2 * time.Second)
	systemctl("start", unit+".service")
	if c := <-queued; c != nil {
		if out := runCmd(t, c, "late"); out != "late" {
			t.Fatalf("ran %q", out)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still running after %s", pid, within)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func journal(unit string) string {
	out, _ := exec.Command("journalctl", "-u", unit, "-n", "40", "--no-pager", "-o", "cat").CombinedOutput()
	return string(out)
}
