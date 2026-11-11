//go:build !windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// requireAttachTmux refuses to run an attach test on a tmux older than
// 3.5, which crashes on the attach every `repose attach` makes.
//
// Since I-1067 an attach is a session of its own grouped with the
// project's and destroyed when its terminal detaches
// (tmuxAttachSession). tmux 3.4 segfaults in that destruction: the
// client it moves to the next session reads a session it has already
// freed ("Pick newest session (as documented) when looking for next
// session for detach-on-destroy", tmux CHANGES 3.4 to 3.5). The server
// dies and takes the project's session with it, so the next command
// says "no sessions" (TestAttachFallsBackToTheSessionWhenTheWindowIsGone)
// or "server exited unexpectedly"
// (TestTwoAttachesKeepTheirOwnWindowAndSize). The crash needs the
// destruction and a busy machine; on an idle one the tests pass, which
// is why they failed on about half of CI's runs (42985983675, main
// a9056ba7) and never here (DECISIONS I-1166).
//
// A guest runs nixpkgs' tmux, 3.7c, so this is a limit of the machine
// the test runs on and not of what repose ships; ubuntu-24.04, the CI
// runner, is the one that has 3.4. ci.yml therefore builds tmux and
// this says so rather than skipping, which in the one place the test
// has to run would read as a pass (I-1036).
func requireAttachTmux(t *testing.T) {
	t.Helper()
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		t.Fatalf("tmux -V: %v", err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(string(out), "tmux "))
	if old, why := tmuxCrashesOnGroupDestroy(version); old {
		if os.Getenv("CI") != "" {
			t.Fatalf("tmux %s on PATH in CI: %s. ci.yml builds a newer one; that step has gone", version, why)
		}
		t.Skipf("tmux %s: %s. `nix develop ./nix` has one that does not", version, why)
	}
}

// tmuxCrashesOnGroupDestroy reports whether a `tmux -V` version is one
// of the releases that crash above, which is anything before 3.5. A
// version it cannot read (master, next-3.9, a distribution's suffix) is
// taken as new enough: the guard is for the known-bad releases, and a
// test that refuses to run on an unknown tmux would be the skip this
// avoids.
func tmuxCrashesOnGroupDestroy(version string) (bool, string) {
	const why = "a session in a group destroyed on detach crashes the server before 3.5, so the attach tests fail at random (DECISIONS I-1166)"
	digits := func(s string) (int, bool) {
		n := 0
		for n < len(s) && s[n] >= '0' && s[n] <= '9' {
			n++
		}
		v, err := strconv.Atoi(s[:n])
		return v, err == nil
	}
	major, minor, ok := strings.Cut(version, ".")
	if !ok {
		return false, ""
	}
	maj, okMaj := digits(major)
	mnr, okMnr := digits(minor)
	if !okMaj || !okMnr {
		return false, ""
	}
	if maj > 3 || (maj == 3 && mnr >= 5) {
		return false, ""
	}
	return true, why
}

func TestTmuxVersionGuardReadsTheVersionsThatCrash(t *testing.T) {
	for v, crashes := range map[string]bool{
		"3.4": true, "3.4a": true, "3.3a": true, "2.9": true,
		"3.5": false, "3.5a": false, "3.7c": false, "3.10": false, "4.0": false,
		// Unreadable: a build from git, and a packager's own string.
		"master": false, "next-3.9": false, "3.7c-openbsd": false,
	} {
		if got, _ := tmuxCrashesOnGroupDestroy(v); got != crashes {
			t.Errorf("tmux %s: crashes=%v, want %v", v, got, crashes)
		}
	}
}

// `repose attach --bridge` over a live master (the no-api fast path)
// dropped --bridge from the session helper (I-305), and then dropped
// --bridge-allow the same way (I-751). I-305's test took a bare bool, so
// it had no parameter the allowlist could be asserted through; this one
// takes the same RunOptions the full path reads, and asserts both fields
// against the full path's own derivation.
func TestFastAttachKeepsBridgeAndItsAllowlist(t *testing.T) {
	e := &Env{Cwd: t.TempDir(), HomeDir: t.TempDir()}
	guess := &Project{ID: "p1", Slug: "izma"}
	target := sshTarget{Args: []string{"izma.repose"}}
	for _, c := range []struct {
		name   string
		opts   RunOptions
		bridge bool
		allow  []string
	}{
		{name: "no flag", opts: RunOptions{}},
		{name: "--bridge", opts: RunOptions{Bridge: true}, bridge: true},
		// An allowlist implies a bridge, as the full path has it: a nil
		// allowlist is every target, not none (bridgePolicy.allowlisted).
		{name: "--bridge-allow", opts: RunOptions{BridgeAllow: []string{"example.com"}}, bridge: true, allow: []string{"example.com"}},
		{name: "both", opts: RunOptions{Bridge: true, BridgeAllow: []string{"example.com"}}, bridge: true, allow: []string{"example.com"}},
	} {
		h := fastAttachHelper(e, guess, target, "UTC", "izma", c.opts)
		if h.Bridge != c.bridge || !slices.Equal(h.BridgeAllow, c.allow) {
			t.Errorf("%s: Bridge=%v BridgeAllow=%v, want Bridge=%v BridgeAllow=%v", c.name, h.Bridge, h.BridgeAllow, c.bridge, c.allow)
		}
		if !h.Carry || h.Slug != "izma" || h.RepoDir != "" {
			t.Errorf("%s: %+v", c.name, h)
		}
	}
}

// `repose run PROMPT` died with tmux's "can't find window: claude" when
// the agent exited between its prompt and the attach (I-304): the attach
// now goes to the session and says why; a window that is there is
// attached to as before.
func TestAttachFallsBackToTheSessionWhenTheWindowIsGone(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("needs script(1) to give the fake guest's tmux client a terminal")
	}
	requireAttachTmux(t)
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n editor 'exec cat'", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ window, want string }{{"editor", "editor"}, {"claude", ""}} {
		remote := "TERM=xterm-256color script -qfc " + shQuote(attachCommand(testSlug, "", tc.window)) + " /dev/null"
		cmd := exec.Command("ssh", append(append([]string{"-t"}, f.target.Args...), remote)...)
		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 160})
		if err != nil {
			t.Fatal(err)
		}
		screen := &syncBuffer{}
		go func() { _, _ = io.Copy(screen, ptmx) }()
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		var clients string
		for i := 0; ; i++ {
			out, _ := runSSH(ctx, f.target, "tmux list-clients -F '#{window_name}'", nil)
			if clients = strings.TrimSpace(string(out)); clients != "" {
				break
			}
			select {
			case err := <-exited:
				screen.mu.Lock()
				t.Fatalf("window %s: the attach exited (%v):\n%s", tc.window, err, screen.b.String())
			default:
			}
			if i > 100 {
				t.Fatalf("window %s: never attached", tc.window)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if tc.want != "" && clients != tc.want {
			t.Fatalf("window %s: client is on %q", tc.window, clients)
		}
		if tc.want == "" {
			deadline := time.Now().Add(5 * time.Second)
			for {
				screen.mu.Lock()
				s := screen.b.String()
				screen.mu.Unlock()
				if strings.Contains(s, "The claude window closed before the attach") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("no line saying the agent exited:\n%q", s)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		if _, err := runSSH(ctx, f.target, detachAll, nil); err != nil {
			t.Fatal(err)
		}
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("window %s: the attach did not end on detach", tc.window)
		}
		_ = ptmx.Close()
	}
}

// detachAll detaches every terminal from the fake guest's tmux.
const detachAll = `for c in $(tmux list-clients -F '#{client_name}'); do tmux detach-client -t "$c"; done`

// startAttach runs remote, a guest-side attach command, on a terminal of
// cols x rows, and waits until tmux has one more client than before.
func startAttach(t *testing.T, ctx context.Context, f *runFixture, remote string, cols, rows uint16) (*exec.Cmd, chan error) {
	t.Helper()
	before, _ := runSSH(ctx, f.target, "tmux list-clients -F x", nil)
	n := strings.Count(string(before), "x")
	// The fake guest's ssh passes no terminal size on to script(1), so
	// the size is set on the terminal script gives tmux.
	sized := "TERM=xterm-256color script -qfc " + shQuote(fmt.Sprintf("stty cols %d rows %d\n", cols, rows)+remote) + " /dev/null"
	cmd := exec.Command("ssh", append(append([]string{"-t"}, f.target.Args...), sized)...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close() })
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for i := 0; ; i++ {
		out, _ := runSSH(ctx, f.target, "tmux list-clients -F x", nil)
		if strings.Count(string(out), "x") > n {
			return cmd, exited
		}
		select {
		case err := <-exited:
			t.Fatalf("the attach exited (%v)", err)
		default:
		}
		if i > 100 {
			t.Fatal("never attached")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Two laptops attached to one machine (DECISIONS I-1067): each terminal
// gets a session grouped with the project's, so each keeps its own window
// and a window takes the size of the terminal showing it. Both count as
// the session's clients, the window last chosen is where the next attach
// opens, and a detach leaves nothing behind.
func TestTwoAttachesKeepTheirOwnWindowAndSize(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("needs script(1) to give the fake guest's tmux client a terminal")
	}
	requireAttachTmux(t)
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux set -gw aggressive-resize on && tmux new-window -d -t "+testSlug+" -n editor 'exec cat' && tmux new-window -d -t "+testSlug+" -n logs 'exec cat'", nil); err != nil {
		t.Fatal(err)
	}
	_, exitA := startAttach(t, ctx, f, attachNamedCommand(testSlug, "", "editor"), 80, 24)
	_, exitB := startAttach(t, ctx, f, attachNamedCommand(testSlug, "", "logs"), 200, 60)
	clients, err := runSSH(ctx, f.target, tmuxClientsScript(testSlug), nil)
	if err != nil || strings.Count(strings.TrimSpace(string(clients)), "\n") != 1 {
		t.Fatalf("the session's clients: %q (%v)", clients, err)
	}
	on, _ := runSSH(ctx, f.target, "tmux list-clients -F '#{window_name}' | sort | tr '\\n' ' '", nil)
	if string(on) != "editor logs " {
		t.Fatalf("clients are on %q, want editor and logs", on)
	}
	sizes, _ := runSSH(ctx, f.target, "tmux list-windows -t ="+testSlug+" -F '#{window_name} #{window_width}x#{window_height}'", nil)
	if !strings.Contains(string(sizes), "editor 80x23") || !strings.Contains(string(sizes), "logs 200x59") {
		cl, _ := runSSH(ctx, f.target, "tmux list-clients -F '#{client_name} #{client_width}x#{client_height} #{session_name} #{window_name}'; tmux show -gwv aggressive-resize; tmux show -gv window-size", nil)
		t.Fatalf("window sizes:\n%s\nclients:\n%s", sizes, cl)
	}
	cur, _ := runSSH(ctx, f.target, "tmux display-message -p -t ="+testSlug+": '#{window_name}'", nil)
	if strings.TrimSpace(string(cur)) != "logs" {
		t.Fatalf("the project's session is on %q, want logs, the window last chosen", cur)
	}
	if _, err := runSSH(ctx, f.target, detachAll, nil); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []chan error{exitA, exitB} {
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal("an attach did not end on detach")
		}
	}
	left, _ := runSSH(ctx, f.target, "tmux list-sessions -F '#{session_name}'", nil)
	if strings.TrimSpace(string(left)) != testSlug {
		t.Fatalf("sessions after both detached: %q", left)
	}
}

// I-606: `attach -w NAME` opens that window, by exact name or number; a
// name the session lacks (here a prefix of one it has) exits 2 with the
// line naming `repose ps`, before any attach.
func TestAttachNamedWindow(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n editor-2 'exec cat'", nil); err != nil {
		t.Fatal(err)
	}
	_, err := runSSH(ctx, f.target, attachNamedCommand(testSlug, "", "editor"), nil)
	var se *sshError
	if !errors.As(err, &se) || se.ExitCode != 2 || !strings.Contains(se.Stderr, testSlug+" has no window editor. `repose ps "+testSlug+"` lists them.") {
		t.Fatalf("a missing window: %v", err)
	}
	// Without a terminal the attach is refused after the window check,
	// and the session's current window stays where it was (I-633).
	before, err := runSSH(ctx, f.target, "tmux display-message -p -t "+testSlug+" '#{window_index}'", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"editor-2", "1"} {
		_, err = runSSH(ctx, f.target, attachNamedCommand(testSlug, "", w), nil)
		if !errors.As(err, &se) || se.ExitCode != 1 || !strings.Contains(se.Stderr, "Attaching needs a terminal") {
			t.Fatalf("window %s: %v", w, err)
		}
	}
	after, err := runSSH(ctx, f.target, "tmux display-message -p -t "+testSlug+" '#{window_index}'", nil)
	if err != nil || string(after) != string(before) {
		t.Fatalf("current window moved from %q to %q (%v)", before, after, err)
	}
	if got := tmuxWindowTarget("todo", "12"); got != "=todo:12" {
		t.Fatal(got)
	}
	if got := tmuxWindowTarget("todo", "api/claude"); got != "=todo:=api/claude" {
		t.Fatal(got)
	}
}
