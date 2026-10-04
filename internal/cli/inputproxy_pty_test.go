//go:build !windows

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// syncBuffer is the laptop terminal's screen: what the proxy writes out.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// I-280, end to end: real ssh on a real pty under proxySession, to the
// fake guest's sshd, attached to a tmux window whose program asked for
// bracketed paste and prints what it receives (cat -v). A dropped file
// lands in the guest's paste directory and the program receives its guest
// path as a bracketed paste, before the keys typed after the drop; a file
// of the checkout arrives as its checkout path with nothing copied;
// Ctrl+V pastes a clipboard image, and without one reaches the program as
// Ctrl+V; detaching ends the proxy with ssh's status 0.
func TestInputProxyDropReachesTheSession(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("needs script(1) to give the fake guest's tmux client a terminal")
	}
	f, dir := pasteFixture(t)
	clip := &switchClipboard{}
	withClipboard(t, clip)
	ctx := context.Background()
	// The pane program: bracketed paste on, no line buffering, every byte
	// shown (ESC as ^[).
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n agent "+shQuote(`printf '\033[?2004h'; stty -icanon -echo; exec cat -v`), nil); err != nil {
		t.Fatal(err)
	}
	// The fake guest has no pty of its own for `ssh -t`; script(1) gives
	// the tmux client one, as sshd does on a real guest.
	remote := "TERM=xterm-256color script -qfc " + shQuote("stty cols 200 rows 50; tmux attach -t "+testSlug+":agent") + " /dev/null"
	args := append(append([]string{"-t"}, f.target.Args...), remote)
	cmd := exec.Command("ssh", args...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 200})
	if err != nil {
		t.Fatal(err)
	}
	stdinR, stdinW := io.Pipe()
	screen := &syncBuffer{}
	h := newDropHandler(f.target, testSlug, f.local)
	h.clip = clip
	notices := &syncBuffer{}
	tmuxNotify := h.notify
	h.notify = func(msg string) { _, _ = notices.Write([]byte(msg + "\n")); tmuxNotify(msg) }
	done := make(chan error, 1)
	go func() { done <- proxySession(cmd, ptmx, stdinR, screen, h, func() {}) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = stdinW.Close() })

	for i := 0; ; i++ {
		out, _ := runSSH(ctx, f.target, "tmux list-clients -t "+testSlug, nil)
		if strings.TrimSpace(string(out)) != "" {
			break
		}
		if i > 100 {
			t.Fatal("the proxied ssh never attached")
		}
		time.Sleep(50 * time.Millisecond)
	}
	typeIn := func(s string) {
		t.Helper()
		if _, err := stdinW.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	// A drop from outside the checkout: copied, and its guest path pasted.
	laptop := t.TempDir()
	shot := filepath.Join(laptop, "Screen Shot 1.png")
	img := testPNG(3000)
	if err := os.WriteFile(shot, img, 0o644); err != nil {
		t.Fatal(err)
	}
	typeIn("\x1b[200~" + strings.ReplaceAll(shot, " ", `\ `) + " \x1b[201~")
	typeIn("after")
	pane := waitPane(t, f, "agent", "after")
	files, _ := filepath.Glob(filepath.Join(dir, "*Screen-Shot-1.png"))
	if len(files) != 1 {
		t.Fatalf("files in the guest's paste dir = %v\n%s", files, pane)
	}
	if b, err := os.ReadFile(files[0]); err != nil || !bytes.Equal(b, img) {
		t.Fatalf("copied file differs (%d bytes, %v)", len(b), err)
	}
	want := "^[[200~" + files[0] + " ^[[201~after"
	if !strings.Contains(strings.ReplaceAll(pane, "\n", ""), want) {
		t.Fatalf("pane does not show %q:\n%s", want, pane)
	}
	if !strings.Contains(notices.String(), "copied Screen Shot 1.png to the machine") {
		t.Fatalf("the copy was not named on the status line: %q", notices.String())
	}

	// A plain name that links to a hidden file is not a drop: its path
	// is typed as it is, and nothing is copied.
	if err := os.MkdirAll(filepath.Join(laptop, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laptop, ".ssh", "id_ed25519"), []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(laptop, "notes.txt")
	if err := os.Symlink(filepath.Join(laptop, ".ssh", "id_ed25519"), notes); err != nil {
		t.Fatal(err)
	}
	typeIn("\x1b[200~" + notes + "\x1b[201~&")
	pane = waitPane(t, f, "agent", "~&")
	if !strings.Contains(strings.ReplaceAll(pane, "\n", ""), "^[[200~"+notes+"^[[201~&") {
		t.Fatalf("pane does not show the link's own path:\n%s", pane)
	}
	if all, _ := filepath.Glob(filepath.Join(dir, "*")); len(all) != 1 {
		t.Fatalf("a link to a hidden file was copied: %v", all)
	}

	// A file of the checkout: its path in the guest's checkout, nothing copied.
	typeIn("\x1b[200~'" + filepath.Join(f.local, "README.md") + "'\x1b[201~|")
	want = "^[[200~" + filepath.Join(f.guestRepo(), "README.md") + "^[[201~|"
	pane = waitPane(t, f, "agent", "~|")
	if !strings.Contains(strings.ReplaceAll(pane, "\n", ""), want) {
		t.Fatalf("pane does not show %q:\n%s", want, pane)
	}
	if all, _ := filepath.Glob(filepath.Join(dir, "*")); len(all) != 1 {
		t.Fatalf("a checkout file was copied: %v", all)
	}

	// Ctrl+V with an image on the clipboard.
	clip.set(testPNG(500))
	typeIn("\x16#")
	pane = waitPane(t, f, "agent", "~#")
	pngs := pngFiles(t, dir)
	var clipPath string
	for _, p := range pngs {
		if !strings.Contains(p, "Screen-Shot") {
			clipPath = p
		}
	}
	if clipPath == "" || !strings.Contains(strings.ReplaceAll(pane, "\n", ""), "^[[200~"+clipPath+"^[[201~#") {
		t.Fatalf("Ctrl+V image: files %v, pane:\n%s", pngs, pane)
	}

	// Ctrl+V with no image is Ctrl+V.
	clip.set(nil)
	typeIn("\x16%")
	waitPane(t, f, "agent", "^V%")

	// A text paste goes through untouched.
	typeIn("\x1b[200~hello there\x1b[201~=")
	waitPane(t, f, "agent", "^[[200~hello there^[[201~=")

	// Detach (Ctrl-b d): ssh exits 0 and so does the proxy.
	typeIn("\x02d")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxy after detach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("proxy did not end after the detach")
	}
	if !strings.Contains(screen.b.String(), "detached") {
		t.Errorf("the laptop's screen never showed tmux's detach line: %q", tail(screen.b.String(), 300))
	}

}

// switchClipboard is a clipboard a test changes between keys.
type switchClipboard struct {
	mu  sync.Mutex
	img []byte
}

func (c *switchClipboard) set(img []byte) { c.mu.Lock(); c.img = img; c.mu.Unlock() }

func (c *switchClipboard) ReadPNG(context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.img == nil {
		return nil, errNoImage
	}
	return c.img, nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// proxyExit reports ssh's own status: 0 is nil, anything else that code,
// a signal 128+N.
func TestProxyExitStatus(t *testing.T) {
	for _, c := range []struct {
		script string
		want   int
	}{{"exit 0", 0}, {"exit 3", 3}, {"kill -TERM $$", 128 + 15}} {
		cmd := exec.Command("sh", "-c", c.script)
		err := cmd.Run()
		got := proxyExit(cmd, err)
		code := 0
		if got != nil {
			ee, ok := got.(*exitError)
			if !ok {
				t.Fatalf("%s: %T %v", c.script, got, got)
			}
			code = ee.code
		}
		if code != c.want {
			t.Errorf("%s: exit %d, want %d", c.script, code, c.want)
		}
	}

}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
