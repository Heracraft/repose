package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseClipSet(t *testing.T) {
	for _, tc := range []struct {
		line, count, path string
		ok                bool
	}{
		{"set 42 /Users/a/Library/Caches/repose/clipboard/clipboard-1.png", "42", "/Users/a/Library/Caches/repose/clipboard/clipboard-1.png", true},
		{"set 7 /Users/a b/clip.png", "7", "/Users/a b/clip.png", true},
		{".", "", "", false},
		{"set x /p.png", "", "", false},
		{"set 3", "", "", false},
		{"set 3 ", "", "", false},
	} {
		count, path, ok := parseClipSet(tc.line)
		if count != tc.count || path != tc.path || ok != tc.ok {
			t.Errorf("parseClipSet(%q) = %q, %q, %v", tc.line, count, path, ok)
		}
	}
}

// The watcher's last "set" line is what stop hands the restore; a
// watcher that set nothing restores nothing.
func TestClipWatchStopRestoresTheLastSet(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "restore")
	old := clipRestoreCommand
	t.Cleanup(func() { clipRestoreCommand = old })
	clipRestoreCommand = func(ctx context.Context, count, path string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf '%s|%s' "$1" "$2" > "$0"`, record, count, path)
	}

	stop := runClipWatch(exec.Command("sh", "-c", "echo .; echo 'set 5 /tmp/a b.png'; echo .; echo 'set 9 /tmp/c.png'; exec sleep 60"))
	time.Sleep(300 * time.Millisecond) // the lines are read
	if _, err := os.Stat(record); err == nil {
		t.Fatal("restored before stop")
	}
	stop()
	b, err := os.ReadFile(record)
	if err != nil || string(b) != "9|/tmp/c.png" {
		t.Fatalf("restore got %q, %v; want the last set, 9|/tmp/c.png", b, err)
	}

	_ = os.Remove(record)
	runClipWatch(exec.Command("sh", "-c", "echo .; exec sleep 60"))()
	if _, err := os.Stat(record); err == nil {
		t.Fatal("a watcher that set nothing ran the restore")
	}
}

func TestPruneClipboardDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range clipKeep + 3 {
		write("clipboard-"+strings.Repeat("a", i+1)+".png", time.Duration(i)*time.Minute)
	}
	write("clipboard-old.png", 25*time.Hour)
	write("notes.txt", 48*time.Hour)
	pruneClipboardDir(dir, now)
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != clipKeep+1 {
		t.Fatalf("after prune: %d files %v, want %d copies and notes.txt", len(names), names, clipKeep)
	}
	for _, n := range names {
		if n == "clipboard-old.png" || n == "clipboard-"+strings.Repeat("a", clipKeep+1)+".png" {
			t.Fatalf("%s survived the prune", n)
		}
	}
}

// The path the watcher offers is one the proxy copies: not hidden, not
// under a system folder (/Library/ is, ~/Library/ is not).
func TestClipboardPathIsADrop(t *testing.T) {
	p := "/Users/ada/Library/Caches/repose/clipboard/clipboard-20260928-152233-041.png"
	if got, ok := dropPath(p); !ok || got != p {
		t.Fatalf("dropPath(%q) = %q, %v", p, got, ok)
	}
	files, trail, ok := parseDrop(p, func(string) bool { return true })
	if !ok || len(files) != 1 || files[0] != p || trail != "" {
		t.Fatalf("parseDrop = %v %q %v", files, trail, ok)
	}
}

// The watcher is macOS only and has its own switch.
func TestClipboardWatchEnabled(t *testing.T) {
	t.Setenv("REPOSE_TEST_GOOS", "linux")
	t.Setenv(envClipboardPath, "")
	if clipboardWatchEnabled() {
		t.Fatal("enabled on linux")
	}
	t.Setenv("REPOSE_TEST_GOOS", "darwin")
	if !clipboardWatchEnabled() {
		t.Fatal("off on darwin")
	}
	t.Setenv(envClipboardPath, "0")
	if clipboardWatchEnabled() {
		t.Fatal("REPOSE_CLIPBOARD_PATH=0 did not turn it off")
	}
	// REPOSE_NO_CLIPBOARD_PATH=1 is the spelling of every other switch
	// (I-621).
	t.Setenv(envClipboardPath, "")
	t.Setenv(envNoClipboardPath, "1")
	if clipboardWatchEnabled() {
		t.Fatal("REPOSE_NO_CLIPBOARD_PATH=1 did not turn it off")
	}
}

func TestInputProxySwitches(t *testing.T) {
	t.Setenv("REPOSE_TEST_GOOS", "linux")
	for _, tc := range []struct {
		no, old string
		want    bool
	}{
		{"", "", true},
		{"1", "", false},
		{"", "0", false},
		{"0", "", true},
	} {
		t.Setenv(envNoInputProxy, tc.no)
		t.Setenv(envInputProxy, tc.old)
		if got := inputProxyEnabled(); got != tc.want {
			t.Errorf("REPOSE_NO_INPUT_PROXY=%q REPOSE_INPUT_PROXY=%q: on = %v, want %v", tc.no, tc.old, got, tc.want)
		}
	}
}
