package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// I-634: the links the machine's BROWSER was handed are taken once, and
// only fresh https ones are opened.
func TestOpenURLsTakenOnce(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, guestOpenURLs)
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	lines := fmt.Sprintf("%d https://claude.com/cai/oauth/authorize?code=true&x=1\n%d https://github.com/old\n%d http://plain.example/\n%d https://bad.example/a'b\n", now-5, now-3600, now-1, now-1)
	if err := os.WriteFile(f, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	take := func() string {
		cmd := exec.Command("sh", "-c", "ss() { :; }; "+takeOpenURLsScript)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	got := parseOpenURLs(take())
	if len(got) != 1 || got[0] != "https://claude.com/cai/oauth/authorize?code=true&x=1" {
		t.Fatalf("opened %q", got)
	}
	if out := take(); out != "" {
		t.Fatalf("second take printed %q", out)
	}

	// The forwarder's poll carries them, and its listeners still parse.
	var opened []string
	fw := newForwarder(sshTarget{}, "todo-app", func(string) {})
	fw.openURL = func(u string) error { opened = append(opened, u); return nil }
	fw.listeners = func(context.Context) (string, error) {
		return fmt.Sprintf("LISTEN 0 4096 0.0.0.0:5173 0.0.0.0:*\n#now %d\n#url %d https://github.com/login\n", now, now), nil
	}
	fw.ctl = func(context.Context, string, string) error { return nil }
	fw.localFree = func(int) bool { return true }
	fw.master = nil
	if _, err := fw.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "https://github.com/login" {
		t.Fatalf("opened %q", opened)
	}
	if _, ok := fw.fwd[5173]; !ok {
		t.Fatalf("forwards %v", fw.fwd)
	}
	if strings.Contains(takeOpenURLsScript, "\n") {
		t.Fatal("the script is one line, after ss on the same command")
	}
}
