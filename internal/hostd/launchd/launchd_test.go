package launchd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// Shapes copied from `launchctl print` on the lab Mac (macOS 15.8.3).
const printRunning = `gui/501/run.repose.guest@g1 = {
	active count = 1
	path = /Users/u/x.plist
	type = LaunchAgent
	state = running

	program = /Users/u/repose-vm
	arguments = {
		/Users/u/repose-vm
		--api-socket
	}

	runs = 1
	pid = 8432
	last exit code = (never exited)
}
`

const printExited = `gui/501/run.repose.guest@g1 = {
	state = not running
	runs = 1
	last exit code = 7
	environment = {
		pid = 99
	}
}
`

const printDomain = `com.apple.xpc.launchd.domain.user.501 = {
	type = gui
	services = {
		     403      - 	com.apple.syncdefaultsd
		    8531      - 	run.repose.guest@g1
		       0      7 	run.repose.guest@g2
		    8540      - 	run.repose.virtiofsd@g1
	}
	disabled services = {
		"run.repose.guest@g9" => disabled
	}
}
`

func TestParsePrint(t *testing.T) {
	j := parsePrint([]byte(printRunning))
	if !j.loaded || !j.running || j.pid != 8432 || j.exited {
		t.Fatalf("running: %+v", j)
	}
	j = parsePrint([]byte(printExited))
	if j.running || j.pid != 0 || !j.exited || j.lastExit != 7 {
		t.Fatalf("exited (nested pid must not count): %+v", j)
	}
}

func TestParseServicesListsRunningMatches(t *testing.T) {
	got := parseServices([]byte(printDomain), "guest@*")
	if strings.Join(got, ",") != "guest@g1" {
		t.Fatalf("got %v", got)
	}
}

func TestRunWritesJobAndBootstraps(t *testing.T) {
	dir := t.TempDir()
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"launchctl", "print"}, Err: &shell.ExitError{Argv: []string{"launchctl"}, Result: shell.Result{ExitCode: 113}}},
	}}
	l := &Launchd{R: r, UID: 501, Dir: dir, Launcher: "/opt/repose-vm",
		ExtraArgs: func(string, []string) []string { return []string{"--net-dir", "/n"} }}
	if err := l.Run(context.Background(), "guest@g1", []string{"MemoryMax=1M"}, []string{"cloud-hypervisor", "--api-socket", "/g/ch.sock"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "run.repose.guest@g1.plist"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<string>/opt/repose-vm</string>", "<string>--net-dir</string>", "<key>KeepAlive</key><false/>", "<string>Interactive</string>"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plist lacks %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "cloud-hypervisor") || strings.Contains(string(b), "MemoryMax") {
		t.Errorf("plist carries the Linux argv[0] or a systemd property:\n%s", b)
	}
	if c := r.CallsWithPrefix("launchctl", "bootstrap"); len(c) != 1 || c[0][2] != "gui/501" {
		t.Fatalf("bootstrap calls %v", c)
	}
}

func TestRunLeavesRunningJobAndReplacesExitedOne(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"launchctl", "print"}, Result: shell.Result{Stdout: []byte(printRunning)}}}}
	l := &Launchd{R: r, UID: 501, Dir: t.TempDir(), Launcher: "/x"}
	if err := l.Run(context.Background(), "guest@g1", nil, []string{"cloud-hypervisor"}); err != nil {
		t.Fatal(err)
	}
	if len(r.CallsWithPrefix("launchctl", "bootstrap")) != 0 {
		t.Fatal("a running job was bootstrapped again")
	}
	r = &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"launchctl", "print"}, Result: shell.Result{Stdout: []byte(printExited)}}}}
	l.R = r
	if err := l.Run(context.Background(), "guest@g1", nil, []string{"cloud-hypervisor"}); err != nil {
		t.Fatal(err)
	}
	if len(r.CallsWithPrefix("launchctl", "bootout")) != 1 || len(r.CallsWithPrefix("launchctl", "bootstrap")) != 1 {
		t.Fatalf("an exited job must be booted out, then bootstrapped: %v", r.Calls)
	}
}

func TestStopAndKillOfAbsentJob(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"launchctl"}, Err: &shell.ExitError{Argv: []string{"launchctl"}, Result: shell.Result{ExitCode: 3}}}}}
	l := &Launchd{R: r, UID: 501}
	if err := l.Stop(context.Background(), "guest@x"); err != nil {
		t.Fatal(err)
	}
	if err := l.Kill(context.Background(), "guest@x"); err != nil {
		t.Fatal(err)
	}
}

func TestShowReadsPrintAndPs(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"launchctl", "print"}, Result: shell.Result{Stdout: []byte(printRunning)}},
		{Prefix: []string{"ps"}, Result: shell.Result{Stdout: []byte("  2048 01:02.50\n")}},
	}}
	l := &Launchd{R: r, UID: 501}
	p, err := l.Show(context.Background(), "guest@g1", "MainPID", "MemoryCurrent", "CPUUsageNSec", "ControlGroup", "Result")
	if err != nil {
		t.Fatal(err)
	}
	if p["MainPID"] != "8432" || p["MemoryCurrent"] != "2097152" || p["CPUUsageNSec"] != "62500000000" || p["ControlGroup"] != "" || p["Result"] != "success" {
		t.Fatalf("%v", p)
	}
	r.Scripts[0].Result.Stdout = []byte(printExited)
	p, _ = l.Show(context.Background(), "guest@g1", "ExecMainStatus", "Result", "MainPID")
	if p["ExecMainStatus"] != "7" || p["Result"] != "exit-code" || p["MainPID"] != "0" {
		t.Fatalf("%v", p)
	}
}

func TestPsTime(t *testing.T) {
	for in, want := range map[string]uint64{"0:01.00": 1e9, "1:00:00.00": 3600e9, "2-00:00:01.00": 2*86400e9 + 1e9} {
		if got, ok := psTime(in); !ok || got != want {
			t.Errorf("%s: %d %v", in, got, ok)
		}
	}
}
