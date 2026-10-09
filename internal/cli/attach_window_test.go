//go:build !windows

package cli

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose attach --bridge` over a live master (the no-api fast path)
// dropped --bridge from the session helper (I-305).
func TestFastAttachKeepsBridge(t *testing.T) {
	e := &Env{Cwd: t.TempDir(), HomeDir: t.TempDir()}
	guess := &Project{ID: "p1", Slug: "izma"}
	for _, bridge := range []bool{true, false} {
		h := fastAttachHelper(e, guess, sshTarget{Args: []string{"izma.repose"}}, "UTC", "izma", bridge)
		if h.Bridge != bridge || !h.Carry || h.Slug != "izma" || h.RepoDir != "" {
			t.Fatalf("bridge=%v: %+v", bridge, h)
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
			out, _ := runSSH(ctx, f.target, "tmux list-clients -t "+testSlug+" -F '#{window_name}'", nil)
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
		if _, err := runSSH(ctx, f.target, "tmux detach-client -s "+testSlug, nil); err != nil {
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
