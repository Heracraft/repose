//go:build linux || darwin

package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The menu on a real terminal (DECISIONS I-1100): the refusal and the
// three lines are on the screen, one key acts with no Enter, the key is
// echoed, and the terminal is back in cooked mode with echo afterwards.
func TestOverlapMenuOnARealTerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()
	oldIn, oldErr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = tty, tty
	defer func() { os.Stdin, os.Stderr = oldIn, oldErr }()

	if !canAskOverlap() {
		t.Fatal("canAskOverlap said no on a pty on both stdin and stderr")
	}
	d := &dirtyTreeError{files: []string{"src/session.ts", "notes.md"}}
	type result struct {
		ch  overlapChoice
		err error
	}
	done := make(chan result, 1)
	go func() {
		ch, err := askOverlap(os.Stderr, d)
		done <- result{ch, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, err := getTermios(tty); err == nil && !st.echo && !st.canonical {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the terminal never went raw, so the menu would need Enter")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// One key, no Enter.
	if _, err := ptmx.Write([]byte("l")); err != nil {
		t.Fatal(err)
	}
	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the menu did not return on one key")
	}
	if r.ch != overlapLaptop || r.err != nil {
		t.Fatalf("key = %q, err = %v", r.ch, r.err)
	}
	if st, err := getTermios(tty); err != nil || !st.echo || !st.canonical {
		t.Fatalf("the terminal was left %+v (%v), want cooked with echo", st, err)
	}
	_ = ptmx.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _ := ptmx.Read(buf)
	screen := string(buf[:n])
	// Every line ends CR LF: raw mode turns off the terminal's own
	// CR-for-LF, and without the CR each line starts under the end of
	// the one above it.
	for _, want := range []string{
		"Not synced: the machine changed 2 files that your laptop changed too:\r\n",
		"  src/session.ts\r\n",
		"  notes.md\r\n",
		"  l  use the laptop's copy; the machine's goes to its git stash\r\n",
		"  m  keep the machine's copy; sync the rest\r\n",
		"  s  move all of the machine's changes to its git stash, then sync\r\n",
		"Choose l, m or s, or Enter to stop: l\r\n",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen does not hold %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "--stash-machine") {
		t.Errorf("the flag line is still on the screen:\n%s", screen)
	}
}

// The whole user-visible path, from `repose sync`'s options down: a real
// terminal on stdin and stderr, the real askOverlap in raw mode, one
// key, and the summary `repose sync` prints afterwards (I-1100).
func TestSyncOverlapMenuEndToEndOnATerminal(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	writeAt(t, f.guestRepo(), "README.md", "the agent's README\n")
	writeAt(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	writeAt(t, f.local, "README.md", "laptop README\n")

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()
	oldIn, oldErr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = tty, tty
	defer func() { os.Stdin, os.Stderr = oldIn, oldErr }()

	// The pty's buffer is a few kilobytes and the run writes its
	// progress to the same terminal, so somebody has to read it.
	screen := &discardWriter{}
	go func() { _, _ = io.Copy(screen, ptmx) }()

	out, errOut := &discardWriter{}, &discardWriter{}
	env := *f.env
	env.Out, env.ErrOut = out, io.MultiWriter(errOut, tty)
	done := make(chan error, 1)
	go func() {
		done <- runRun(ctx, &env, RunOptions{NoAttach: true, Sync: true}, false)
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if st, err := getTermios(tty); err == nil && !st.echo && !st.canonical {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sync never asked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := ptmx.Write([]byte("l")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("repose sync after l: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the sync did not finish after l; screen:\n%s\nstderr:\n%s", screen.buf.String(), errOut.buf.String())
	}
	if !strings.Contains(errOut.buf.String(), "  l  use the laptop's copy; the machine's goes to its git stash") {
		t.Fatalf("stderr:\n%s", errOut.buf.String())
	}
	line := out.buf.String()
	if !strings.Contains(line, "stashed the machine's copy of 1 file (git stash ") {
		t.Fatalf("stdout:\n%s", line)
	}
	wantFile(t, f.guestRepo(), "README.md", "laptop README\n")
	wantFile(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	if list := mustRun(t, f.guestRepo(), "git", "stash", "list"); !strings.Contains(list, stashCopyMsg) {
		t.Fatalf("stash list %q", list)
	}
	t.Logf("what the user saw on stderr:\n%s", errOut.buf.String())
	t.Logf("and on stdout:\n%s", line)
}
