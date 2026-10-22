package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// fakeClipboard is the laptop clipboard for tests.
type fakeClipboard struct {
	img []byte
	err error
}

func (f fakeClipboard) ReadPNG(context.Context) ([]byte, error) { return f.img, f.err }

func withClipboard(t *testing.T, c clipboardReader) {
	t.Helper()
	old := pasteClipboard
	pasteClipboard = c
	t.Cleanup(func() { pasteClipboard = old })
}

func testPNG(size int) []byte {
	b := append([]byte{}, pngMagic...)
	for len(b) < size {
		b = append(b, byte(len(b)))
	}
	return b
}

// pasteFixture is a running project in the local sshd harness with its
// tmux session, and pasteGuestDir moved off this machine's shared /tmp.
func pasteFixture(t *testing.T) (*runFixture, string) {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	t.Cleanup(func() { _, _ = runSSH(context.Background(), f.target, "tmux kill-server", nil) })
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "repose-paste")
	old := pasteGuestDir
	pasteGuestDir = dir
	t.Cleanup(func() { pasteGuestDir = old })
	return f, dir
}

func pngFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// I-252: the image lands in the guest as a 0600 file in a 0700 directory,
// and its path is pasted into the session's active pane with no Enter.
func TestPasteSavesImageAndPastesPath(t *testing.T) {
	f, dir := pasteFixture(t)
	img := testPNG(4096)
	withClipboard(t, fakeClipboard{img: img})
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n agent cat && tmux select-window -t "+testSlug+":agent", nil); err != nil {
		t.Fatal(err)
	}
	out := &discardWriter{}
	f.env.Out = out
	if err := PasteCmd(ctx, f.env, PasteOptions{}); err != nil {
		t.Fatalf("paste: %v", err)
	}
	files := pngFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("files in the guest = %v", files)
	}
	if b, err := os.ReadFile(files[0]); err != nil || !bytes.Equal(b, img) {
		t.Fatalf("saved image differs (%d bytes, %v)", len(b), err)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, files[0]: 0o600} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != want {
			t.Errorf("%s mode = %v %v, want %v", p, st.Mode().Perm(), err, want)
		}
	}
	if got := out.buf.String(); got != fmt.Sprintf("Pasted %s into %s:agent.\n", filepath.Base(files[0]), testSlug) {
		t.Errorf("output = %q", got)
	}
	// cat echoes what it reads only after a newline: nothing yet means no
	// Enter was sent; the pane itself shows the pasted path.
	pane := waitPane(t, f, "agent", files[0])
	if strings.Count(pane, files[0]) != 1 {
		t.Errorf("pane shows the path %d times (an Enter was sent?):\n%s", strings.Count(pane, files[0]), pane)
	}
}

func waitPane(t *testing.T, f *runFixture, window, want string) string {
	t.Helper()
	var pane string
	for i := 0; i < 50; i++ {
		out, err := runSSH(context.Background(), f.target, fmt.Sprintf("tmux capture-pane -p -J -t %s:%s", testSlug, window), nil)
		pane = string(out)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pane, want) {
			return pane
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane %s never showed %q:\n%s", window, want, pane)
	return ""
}

func TestPastePrintAndWindow(t *testing.T) {
	f, dir := pasteFixture(t)
	withClipboard(t, fakeClipboard{img: testPNG(100)})
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n other cat", nil); err != nil {
		t.Fatal(err)
	}
	out := &discardWriter{}
	f.env.Out = out
	if err := PasteCmd(ctx, f.env, PasteOptions{Print: true}); err != nil {
		t.Fatalf("paste --print: %v", err)
	}
	files := pngFiles(t, dir)
	if len(files) != 1 || out.buf.String() != files[0]+"\n" {
		t.Fatalf("--print printed %q, files %v", out.buf.String(), files)
	}
	out.buf.Reset()
	if err := PasteCmd(ctx, f.env, PasteOptions{Window: "other"}); err != nil {
		t.Fatalf("paste --window other: %v", err)
	}
	files = pngFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	waitPane(t, f, "other", files[1])
	// A window that isn't there: the image is saved and the path given.
	err := PasteCmd(ctx, f.env, PasteOptions{Window: "nope"})
	if exitCode(err) != ExitGeneric || !strings.Contains(err.Error(), "Saved "+dir+"/") || !strings.Contains(err.Error(), "no window nope") {
		t.Fatalf("missing window: %v", err)
	}
}

// Old pastes go: over a day old, or past the newest 50.
func TestPasteCleansOldFiles(t *testing.T) {
	f, dir := pasteFixture(t)
	withClipboard(t, fakeClipboard{img: testPNG(100)})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "20260101-000000-000.png")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	day := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(old, day, day); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		p := filepath.Join(dir, fmt.Sprintf("20260925-0000%02d-000.png", i))
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(time.Duration(i-120) * time.Second)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := PasteCmd(context.Background(), f.env, PasteOptions{Print: true}); err != nil {
		t.Fatal(err)
	}
	files := pngFiles(t, dir)
	if len(files) != pasteKeep {
		t.Errorf("%d files kept, want %d", len(files), pasteKeep)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a day-old paste was kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "20260925-000059-000.png")); err != nil {
		t.Errorf("the newest old paste went: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "20260925-000010-000.png")); !os.IsNotExist(err) {
		t.Errorf("the 51st newest paste was kept: %v", err)
	}
}

// A directory in the shared /tmp that is a symlink is refused, not
// followed.
func TestPasteRefusesSymlinkDir(t *testing.T) {
	f, dir := pasteFixture(t)
	withClipboard(t, fakeClipboard{img: testPNG(100)})
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}
	err := PasteCmd(context.Background(), f.env, PasteOptions{Print: true})
	if exitCode(err) != ExitGeneric || !strings.Contains(err.Error(), "not a directory of its own") {
		t.Fatalf("symlinked dir: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(elsewhere, "*")); len(m) != 0 {
		t.Fatalf("wrote through the symlink: %v", m)
	}
}

// Clipboard failures stop before the api and ssh: an Env with no client
// would panic otherwise.
func TestPasteClipboardErrors(t *testing.T) {
	e := &Env{Out: &discardWriter{}, ErrOut: &discardWriter{}}
	for name, c := range map[string]struct {
		clip clipboardReader
		want string
	}{
		"no image":  {fakeClipboard{err: errNoImage}, "Nothing to paste: there is no image on the clipboard. Copy an image"},
		"not a png": {fakeClipboard{img: []byte("hello")}, "Nothing to paste"},
		"too big":   {fakeClipboard{img: testPNG(pasteMaxBytes + 1)}, "repose paste takes up to 50.0 MB"},
		"no tool":   {fakeClipboard{err: &clipboardToolError{"repose paste needs xclip to read the X11 clipboard; install xclip."}}, "install xclip"},
		"other":     {fakeClipboard{err: errors.New("boom")}, "Could not read the clipboard: boom."},
	} {
		withClipboard(t, c.clip)
		err := PasteCmd(context.Background(), e, PasteOptions{})
		if exitCode(err) != ExitGeneric || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The system reader picks the tool from the platform and display, names
// the tool to install, and tells "no image" from a PNG.
func TestSystemClipboardTools(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("REPOSE_TEST_GOOS", "linux")
	t.Setenv(envWaylandDisplay, "")
	t.Setenv(envDisplay, "")
	ctx := context.Background()
	var te *clipboardToolError
	if _, err := (systemClipboard{}).ReadPNG(ctx); !errors.As(err, &te) || !strings.Contains(te.msg, "neither WAYLAND_DISPLAY nor DISPLAY") {
		t.Errorf("no display: %v", err)
	}
	t.Setenv(envDisplay, ":0")
	if _, err := (systemClipboard{}).ReadPNG(ctx); !errors.As(err, &te) || !strings.Contains(te.msg, "install xclip") {
		t.Errorf("no xclip: %v", err)
	}
	t.Setenv(envWaylandDisplay, "wayland-0")
	if _, err := (systemClipboard{}).ReadPNG(ctx); !errors.As(err, &te) || !strings.Contains(te.msg, "install wl-clipboard") {
		t.Errorf("no wl-paste: %v", err)
	}
	sh, err := lookPathIn("sh")
	if err != nil {
		t.Skip("no sh")
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!"+sh+"\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("wl-paste", `case "$1" in --list-types) echo text/plain;; *) exit 1;; esac`+"\n")
	if _, err := (systemClipboard{}).ReadPNG(ctx); !errors.Is(err, errNoImage) {
		t.Errorf("text on the clipboard: %v", err)
	}
	write("wl-paste", `case "$1" in --list-types) printf 'text/plain\nimage/png\n';; *) printf 'PNGDATA';; esac`+"\n")
	if b, err := (systemClipboard{}).ReadPNG(ctx); err != nil || string(b) != "PNGDATA" {
		t.Errorf("image: %q %v", b, err)
	}
	t.Setenv("REPOSE_TEST_GOOS", "windows")
	if _, err := (systemClipboard{}).ReadPNG(ctx); !errors.As(err, &te) || !strings.Contains(te.msg, "macOS and Linux only") {
		t.Errorf("windows: %v", err)
	}
}

// lookPathIn finds a program on the PATH the test started with.
func lookPathIn(name string) (string, error) {
	for _, d := range []string{"/bin", "/usr/bin", "/run/current-system/sw/bin"} {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

func TestPasteScriptQuotesPaths(t *testing.T) {
	s := pasteScript("izma", "/tmp/repose-paste/x.png", PasteOptions{Window: "claude-2"})
	for _, want := range []string{"d='/tmp/repose-paste'", "f='/tmp/repose-paste/x.png'", "-t '=izma:claude-2'", "paste-buffer -p -d", "umask 077"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Enter") || strings.Contains(pasteScript("izma", "/x", PasteOptions{Print: true}), "tmux") {
		t.Errorf("script sends Enter, or --print touches tmux")
	}
}

// A program that turned on bracketed paste (Claude Code does) gets the
// path as a paste, between ESC[200~ and ESC[201~, as a dropped file
// arrives from a terminal; the tty echoes ESC as ^[.
func TestPasteIsBracketedWhenAsked(t *testing.T) {
	f, dir := pasteFixture(t)
	withClipboard(t, fakeClipboard{img: testPNG(100)})
	ctx := context.Background()
	if _, err := runSSH(ctx, f.target, `tmux new-window -d -t `+testSlug+` -n agent "printf '\033[?2004h'; exec cat" && tmux select-window -t `+testSlug+`:agent`, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := PasteCmd(ctx, f.env, PasteOptions{}); err != nil {
		t.Fatal(err)
	}
	files := pngFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	waitPane(t, f, "agent", "^[[200~"+files[0]+"^[[201~")
}
