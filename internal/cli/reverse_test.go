package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// `[forward] reverse` takes numbers and "MACHINE:LAPTOP" strings, a
// project table adds to the global one, and every refusal names the
// file, the key and the port (DECISIONS I-1104).
func TestForwardReverseConfig(t *testing.T) {
	load := func(t *testing.T, body string) (Config, error) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(configPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return loadConfig(dir)
	}
	cfg, err := load(t, `[forward]
reverse = [5037, 11434, "15432:5432"]

[projects.todo-app.forward]
reverse = ["16379:6379", 11434]
`)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	want := map[string]string{
		"todo-app": "5037:5037 11434:11434 15432:5432 16379:6379",
		"other":    "5037:5037 11434:11434 15432:5432",
	}
	for slug, w := range want {
		var got []string
		for _, r := range cfg.forwardReverse(slug) {
			got = append(got, fmt.Sprintf("%d:%d", r.Machine, r.Laptop))
		}
		if strings.Join(got, " ") != w {
			t.Errorf("forwardReverse(%q) = %q, want %q", slug, strings.Join(got, " "), w)
		}
		t.Logf("forwardReverse(%q) = %q", slug, strings.Join(got, " "))
	}

	// Each refusal, with its text. A refused file fails every command,
	// so the message says the file, the key and what is wrong with it.
	refusals := []struct{ toml, want string }{
		{"[forward]\nreverse = [80]\n", "forward.reverse: 80 is below 1024. Ports below 1024 can't be lent to the machine."},
		{"[forward]\nreverse = [6080]\n", "forward.reverse: 6080 is a port the machine keeps for itself."},
		{"[forward]\nreverse = [9226]\n", "forward.reverse: 9226 is a port the machine keeps for itself."},
		{"[forward]\nreverse = [9222]\n", "forward.reverse: 9222 is Chrome's DevTools port. To lend the agents your Chrome, use repose browser bridge."},
		{"[forward]\nreverse = [9229]\n", "forward.reverse: 9229 is Node's inspector port, which runs code on your laptop."},
		{"[forward]\nreverse = [2375]\n", "forward.reverse: 2375 is Docker's API port, which runs anything on your laptop."},
		{"[forward]\nreverse = [2376]\n", "forward.reverse: 2376 is Docker's API port, which runs anything on your laptop."},
		{"[forward]\nreverse = [70000]\n", "forward.reverse: 70000 is above 65535, which is not a port."},
		{"[forward]\nreverse = [true]\n", `forward.reverse: true is not a port or "MACHINE:LAPTOP".`},
		{"[forward]\nreverse = [\"5432:\"]\n", `forward.reverse: "5432:" is not a port or "MACHINE:LAPTOP".`},
		{"[forward]\nreverse = [1024,1025,1026,1027,1028,1029,1030,1031,1032,1033,1034,1035,1036,1037,1038,1039,1040]\n", "forward.reverse: 17 ports; at most 16."},
		{"[projects.todo-app.forward]\nreverse = [9222]\n", "projects.todo-app.forward.reverse: 9222 is Chrome's DevTools port."},
		// The limit counts the project's table on top of the global one.
		{"[forward]\nreverse = [1024,1025,1026,1027,1028,1029,1030,1031,1032,1033,1034,1035,1036,1037,1038,1039]\n[projects.todo-app.forward]\nreverse = [1040]\n", "projects.todo-app.forward.reverse: 17 ports; at most 16."},
	}
	// The count is asserted so a table that lost its rows cannot pass as
	// a table whose rows all held (CHECKLIST, I-1026): one row per rule
	// in reverse.go, plus the two project ones.
	if len(refusals) != 13 {
		t.Fatalf("%d refusals in the table, want 13", len(refusals))
	}
	for _, c := range refusals {
		_, err := load(t, c.toml)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("loadConfig(%q) = %v, want an error saying %q", c.toml, err, c.want)
			continue
		}
		if !strings.Contains(err.Error(), "config.toml") {
			t.Errorf("error %q does not name the file", err)
		}
		// Wrong usage: a bad config file fails every command with 2,
		// which is what cli.md has promised since I-922.
		if code := exitCodeFor(err, io.Discard); code != ExitUsage {
			t.Errorf("exit code %d, want %d", code, ExitUsage)
		}
		t.Logf("%s", err)
	}
}

// The messages for the ports a laptop lends: one port names the
// laptop's process when the laptop's own tools said, several name the
// ports, and a port the machine holds says so and how to move it.
func TestReverseMessages(t *testing.T) {
	for _, c := range []struct {
		held []reverseHeld
		want string
	}{
		{[]reverseHeld{{reverseForward{5037, 5037}, "adb"}}, "⇄ localhost:5037 on the machine → adb on your laptop"},
		{[]reverseHeld{{reverseForward{11434, 11434}, ""}}, "⇄ localhost:11434 on the machine → your laptop (nothing listens on 11434 there yet)"},
		{[]reverseHeld{{reverseForward{15432, 5432}, "postgres"}}, "⇄ localhost:15432 on the machine → postgres on your laptop's 5432"},
		{[]reverseHeld{{reverseForward{15432, 5432}, ""}}, "⇄ localhost:15432 on the machine → your laptop's 5432 (nothing listens on 5432 there yet)"},
		{[]reverseHeld{{reverseForward{5037, 5037}, "adb"}, {reverseForward{11434, 11434}, "ollama"}}, "⇄ localhost:5037, 11434 on the machine → your laptop"},
	} {
		got := reverseLines(c.held)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("reverseLines(%v) = %q, want %q", c.held, got, c.want)
			continue
		}
		t.Log(got[0])
	}
	if got := reverseLines(nil); got != nil {
		t.Errorf("reverseLines(nil) = %q", got)
	}
	for _, c := range []struct {
		rf   reverseForward
		who  string
		lent bool
		want string
	}{
		{reverseForward{5432, 5432}, "postgres", false, `Could not lend your laptop's 5432: postgres on the machine listens there. Use "15432:5432" in [forward] reverse.`},
		{reverseForward{5432, 5432}, "", false, `Could not lend your laptop's 5432: something on the machine listens there. Use "15432:5432" in [forward] reverse.`},
		{reverseForward{11434, 11434}, "sshd-session", true, "Could not lend your laptop's 11434: another laptop lends 11434 on the machine."},
	} {
		if got := reverseBusyLine(c.rf, c.who, c.lent); got != c.want {
			t.Errorf("reverseBusyLine = %q, want %q", got, c.want)
			continue
		}
		t.Log(reverseBusyLine(c.rf, c.who, c.lent))
	}
}

// The ssh that holds the lent ports: its own connection, exit if a
// listener cannot be made, both loopbacks per port, and a remote
// command that ends when the helper's pipe closes.
func TestReverseSSHArgs(t *testing.T) {
	got := strings.Join(reverseSSHArgs(sshTarget{Args: []string{"todo-app.repose"}}, []reverseForward{{5037, 5037}, {15432, 5432}}), " ")
	want := "-o ControlPath=none -o ExitOnForwardFailure=yes " +
		"-R 127.0.0.1:5037:localhost:5037 -R [::1]:5037:localhost:5037 " +
		"-R 127.0.0.1:15432:localhost:5432 -R [::1]:15432:localhost:5432 " +
		"todo-app.repose exec cat >/dev/null"
	if got != want {
		t.Errorf("reverseSSHArgs =\n%s\nwant\n%s", got, want)
	}
	t.Log(got)
	if ps := reverseFailedPorts("Warning: remote port forwarding failed for listen port 5037\n"); len(ps) != 1 || ps[0] != 5037 {
		t.Errorf("reverseFailedPorts = %v", ps)
	}
}

func TestParseLaptopProcess(t *testing.T) {
	for _, c := range []struct{ os, out, want string }{
		{"darwin", "p731\ncollama\n", "ollama"},
		{"linux", `LISTEN 0 4096 127.0.0.1:11434 0.0.0.0:* users:(("ollama",pid=731,fd=3))`, "ollama"},
		{"linux", "", ""},
		{"windows", "whatever", ""},
	} {
		if got := parseLaptopProcess(c.os, c.out); got != c.want {
			t.Errorf("parseLaptopProcess(%q, %q) = %q, want %q", c.os, c.out, got, c.want)
		}
	}
}

// A reverse listener is never forwarded back to the laptop: the
// forwarder skips the ports it lends and the ports another attached
// laptop's registry entry says it lends. Without it, the machine's
// 5037 (the laptop's adb, through sshd) is forwarded to the laptop's
// 5038, a loop home (I-1104).
func TestForwarderDoesNotForwardALentPortBack(t *testing.T) {
	ctx := context.Background()
	f, ctl, _, ss := fakeForwarder(nil)
	f.setReverse([]reverseHeld{{reverseForward{5037, 5037}, "adb"}})
	*ss = "LISTEN 0 128 127.0.0.1:5037 0.0.0.0:* users:((\"sshd-session\",pid=900,fd=9))\n" +
		"LISTEN 0 128 127.0.0.1:11434 0.0.0.0:* users:((\"sshd-session\",pid=901,fd=10))\n" +
		"LISTEN 0 511 127.0.0.1:5173 0.0.0.0:*\n" +
		forwardRegistryMark + "\n" +
		"5173\nout:5173:5173\nin:11434:11434:ollama\n"
	if _, err := f.sync(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*ctl, "|") != "forward 5173:127.0.0.1:5173" {
		t.Errorf("ssh -O = %v, want only 5173 (5037 is this laptop's, 11434 another's)", *ctl)
	}
	t.Logf("ss had 5037, 11434 and 5173; ssh -O = %v", *ctl)
}

// The registry's new lines are invisible to the reader every CLI since
// I-199 has: the union of the status bar's ports is the same with them
// and without (I-1106).
func TestOldRegistryReadersSeeTheSameUnion(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	f, _, _, ss := fakeForwarder(nil)
	*ss = "LISTEN 0 1 127.0.0.1:3000 0.0.0.0:*\nLISTEN 0 1 127.0.0.1:5173 0.0.0.0:*\n"
	if _, err := f.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.setReverse([]reverseHeld{{reverseForward{5037, 5037}, "adb"}})
	home := t.TempDir()
	run := func(script string) string {
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "HOME="+home, "TMUX_TMPDIR="+tmuxTmpdir(t), "TMUX=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", script, err, out)
		}
		return string(out)
	}
	run(f.publishScript())
	entry, err := os.ReadFile(filepath.Join(home, ".repose", "forwards", f.id))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("entry:\n%s", entry)
	union := func() string {
		return strings.TrimSpace(run(`cd "$HOME/.repose/forwards" && cat * | tr ' ' '\n' | grep -E '^[0-9]+$' | sort -un | tr '\n' ' '`))
	}
	withLines := union()
	// The same entry as a CLI from before I-1106 would have written it.
	first, _, _ := strings.Cut(string(entry), "\n")
	if err := os.WriteFile(filepath.Join(home, ".repose", "forwards", f.id), []byte(first+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	line1Only := union()
	if withLines != line1Only || withLines != "3000 5173" {
		t.Errorf("union with the new lines = %q, from line 1 alone = %q", withLines, line1Only)
	}
	t.Logf("union with the new lines = %q, from line 1 alone = %q", withLines, line1Only)
	// The reverse port is not in the bar: the bar answers "what can I
	// open on my laptop".
	if strings.Contains(withLines, "5037") {
		t.Errorf("the status bar names a lent port: %q", withLines)
	}
}

func TestParseForwardRows(t *testing.T) {
	rows := parseForwardRows("3000 5173\nout:5173:5173\nout:3000:3001\nin:5037:5037:adb\nin:11434:11434\nin:bad:1\nout:1\n")
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%v %d %d %q", r.In, r.Machine, r.Laptop, r.Process))
	}
	// Sorted by the machine's port, whichever way they go.
	want := `false 3000 3001 ""|true 5037 5037 "adb"|false 5173 5173 ""|true 11434 11434 ""`
	if strings.Join(got, "|") != want {
		t.Errorf("parseForwardRows = %v, want %s", got, want)
	}
}

// The lent ports really reach the laptop: a listener on the laptop, the
// held connection, a dial of the machine's 127.0.0.1 and ::1, bytes
// back. The connection ends with the attach, and its listeners with it.
func TestReverseForwardReachesTheLaptopAndEndsWithTheAttach(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ssh")
	}
	f := newSyncFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The laptop's service, the Ollama of the example.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(c, "hello from your laptop")
			_ = c.Close()
		}
	}()
	laptop := ln.Addr().(*net.TCPAddr).Port
	machine, err := freePort()
	if err != nil {
		t.Fatal(err)
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
	done := make(chan string, 1)
	go func() {
		done <- holdReverse(ctx, f.target, []reverseForward{{Machine: machine, Laptop: laptop}}, alive)
	}()
	read := func(host string) string {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(machine)), time.Second)
		if err != nil {
			return ""
		}
		defer func() { _ = c.Close() }()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		b, _ := io.ReadAll(c)
		return string(b)
	}
	waitFor(t, func() bool { return read("127.0.0.1") == "hello from your laptop" })
	t.Logf("the machine's 127.0.0.1:%d read %q from the laptop's %d", machine, read("127.0.0.1"), laptop)
	if got := read("::1"); got != "hello from your laptop" {
		// The test guest answers tcpip-forward for each address asked
		// for, as sshd with GatewayPorts no binds both loopbacks.
		t.Errorf("the machine's [::1]:%d read %q", machine, got)
	} else {
		t.Logf("the machine's [::1]:%d read %q", machine, got)
	}

	// The attach ends: the hold returns and the listener goes with it.
	close(stop)
	select {
	case stderr := <-done:
		t.Logf("the hold ended with the attach; ssh said %q", strings.TrimSpace(stderr))
	case <-time.After(15 * time.Second):
		t.Fatal("the hold outlived the attach")
	}
	gone := false
	for i := 0; i < 50 && !gone; i++ {
		gone = read("127.0.0.1") == ""
		if !gone {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !gone {
		t.Errorf("the machine's %d still answers after the attach ended", machine)
	}
	t.Logf("the machine's %d stopped answering after the attach ended", machine)
}

// The session helper's two loops together, against the test guest: the
// reverse connection lends a laptop port, the forwarder writes it to
// the machine's registry as an `in:` line and never forwards it back,
// and both end with the attach (DECISIONS I-1104, I-1106).
func TestSessionHelperLendsAPortAndTakesItBackOnDetach(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ssh")
	}
	f := newSyncFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(c, "ollama")
			_ = c.Close()
		}
	}()
	laptop := ln.Addr().(*net.TCPAddr).Port
	machine, err := freePort()
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var said, ctl []string
	fw := newForwarder(f.target, testSlug, func(m string) { mu.Lock(); said = append(said, m); mu.Unlock() })
	fw.master = nil
	fw.ctl = func(_ context.Context, op, spec string) error {
		mu.Lock()
		defer mu.Unlock()
		ctl = append(ctl, op+" "+spec)
		return nil
	}
	// What the machine's `ss` shows: sshd holding the lent port once
	// the reverse connection has really made it, and nothing else worth
	// forwarding. The probe keeps the fake honest — `ss` cannot see a
	// listener before it exists, which is what makes the order safe.
	fw.listeners = func(context.Context) (string, error) {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(machine)), 200*time.Millisecond)
		if err != nil {
			return forwardRegistryMark + "\n", nil
		}
		_ = c.Close()
		return fmt.Sprintf("LISTEN 0 128 127.0.0.1:%d 0.0.0.0:* users:((\"sshd-session\",pid=900,fd=9))\n%s\n", machine, forwardRegistryMark), nil
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
	var done sync.WaitGroup
	done.Add(2)
	go func() { defer done.Done(); runForwards(ctx, fw, alive) }()
	go func() {
		defer done.Done()
		runSessionReverse(ctx, f.target, []reverseForward{{Machine: machine, Laptop: laptop}}, fw, func(m string) { mu.Lock(); said = append(said, m); mu.Unlock() }, alive)
	}()

	entry := filepath.Join(f.guestHome, ".repose", "forwards")
	registry := func() string {
		names, _ := filepath.Glob(filepath.Join(entry, "*"))
		var b strings.Builder
		for _, n := range names {
			got, _ := os.ReadFile(n)
			b.Write(got)
		}
		return b.String()
	}
	want := fmt.Sprintf("in:%d:%d", machine, laptop)
	waitFor(t, func() bool { return strings.Contains(registry(), want) })
	t.Logf("the machine's registry:\n%s", registry())

	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(machine)), 2*time.Second)
	if err != nil {
		t.Fatalf("the lent port on the machine: %v", err)
	}
	b, _ := io.ReadAll(c)
	_ = c.Close()
	if string(b) != "ollama" {
		t.Errorf("the lent port read %q", b)
	}
	t.Logf("the machine's localhost:%d read %q from the laptop's %d", machine, b, laptop)

	mu.Lock()
	if len(ctl) != 0 {
		t.Errorf("the lent port was forwarded back to the laptop: %v", ctl)
	}
	lent := false
	for _, m := range said {
		lent = lent || strings.HasPrefix(m, fmt.Sprintf("⇄ localhost:%d on the machine →", machine))
	}
	if !lent {
		t.Errorf("no message named the lent port: %q", said)
	}
	t.Logf("messages: %q; ssh -O calls: %v", said, ctl)
	mu.Unlock()

	// Detach.
	close(stop)
	waited := make(chan struct{})
	go func() { done.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(30 * time.Second):
		t.Fatal("the helper's loops outlived the attach")
	}
	if got := registry(); strings.Contains(got, "in:") {
		t.Errorf("the registry still lends a port after the detach: %q", got)
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(machine)), time.Second); err == nil {
		t.Errorf("the machine's %d still answers after the detach", machine)
	}
	t.Logf("after the detach the registry is %q and the machine's %d refuses", registry(), machine)
}

// [forward] reverse on Windows, where attach runs no helper, says so
// once per attach instead of doing nothing quietly, as [mcp] forward
// does (I-1104); elsewhere it says nothing.
func TestWindowsReverseForwardLine(t *testing.T) {
	for goosName, want := range map[string]string{"windows": windowsReverseLine + "\n", "linux": ""} {
		t.Setenv("REPOSE_TEST_GOOS", goosName)
		var errb bytes.Buffer
		e := &Env{ErrOut: &errb, TargetFor: func(string) sshTarget { return sshTarget{} }}
		startSessionHelper(e, sessionOptions{Forward: true, Reverse: []reverseForward{{5037, 5037}}})
		if errb.String() != want {
			t.Errorf("%s: %q, want %q", goosName, errb.String(), want)
		}
		errb.Reset()
		startSessionHelper(e, sessionOptions{Forward: true})
		if errb.Len() != 0 {
			t.Errorf("%s with no [forward] reverse: %q", goosName, errb.String())
		}
	}
}
