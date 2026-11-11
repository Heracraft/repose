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

// A link on the machine's own loopback never opens on the laptop as
// written: it opens on the laptop end of the forward that reaches it,
// waits a few seconds for that forward to appear, and opens nothing
// when none does (DECISIONS I-1108, amending I-634).
func TestLoopbackLinksOpenOnTheForwardedPort(t *testing.T) {
	for _, c := range []struct {
		url      string
		openable bool
		port     int
	}{
		{"https://github.com/login", true, 0},
		{"http://localhost:3000/x", true, 3000},
		{"http://127.0.0.1:3000/", true, 3000},
		{"http://[::1]:5173/", true, 5173},
		{"https://localhost:8443/", true, 8443},
		{"http://localhost/", true, 80},
		{"http://example.com/", false, 0},
		{"http://localhost.evil.example/", false, 0},
		{"https://bad.example/a'b", false, 0},
	} {
		if got := openableURL(c.url); got != c.openable {
			t.Errorf("openableURL(%q) = %v, want %v", c.url, got, c.openable)
		}
		port, loopback := openURLPort(c.url)
		if loopback != (c.port > 0) || port != c.port {
			t.Errorf("openURLPort(%q) = %d %v, want %d", c.url, port, loopback, c.port)
		}
	}
	if got := openURLOnLaptop("http://localhost:3000/x?y=1", 3001); got != "http://localhost:3001/x?y=1" {
		t.Errorf("openURLOnLaptop = %q", got)
	}

	now := time.Now()
	newFwd := func() (*forwarder, *[]string, *[]string) {
		var opened, said []string
		f := &forwarder{fwd: map[int]forwardEntry{}, failed: map[int]time.Time{}}
		f.say = func(m string) { said = append(said, m) }
		f.openURL = func(u string) error { opened = append(opened, u); return nil }
		return f, &opened, &said
	}
	out := func(u string) string {
		return fmt.Sprintf("#now %d\n#url %d %s\n", now.Unix(), now.Unix(), u)
	}

	// The forward moved the machine's 3000 to the laptop's 3001.
	f, opened, said := newFwd()
	f.fwd[3000] = forwardEntry{Local: 3001, Host: "127.0.0.1"}
	f.takeURLs(out("http://localhost:3000/x"), now)
	f.openWaitingURLs(now)
	if len(*opened) != 1 || (*opened)[0] != "http://localhost:3001/x" || len(*said) != 0 {
		t.Errorf("through a moved forward: opened %q, said %q", *opened, *said)
	}
	t.Logf("machine's http://localhost:3000/x with 3000 → 3001: opened %q", *opened)

	// A link opens once a minute, however often the machine hands it over.
	f.takeURLs(out("http://localhost:3000/x"), now.Add(time.Second))
	f.openWaitingURLs(now.Add(time.Second))
	if len(*opened) != 1 {
		t.Errorf("the same link opened again within a minute: %q", *opened)
	}
	f.takeURLs(out("http://localhost:3000/x"), now.Add(openURLRepeat+time.Second))
	f.openWaitingURLs(now.Add(openURLRepeat + time.Second))
	if len(*opened) != 2 {
		t.Errorf("the link did not open again after a minute: %q", *opened)
	}
	t.Logf("the once-a-minute cap: %d tabs for three handovers", len(*opened))

	// No forward reaches it: nothing opens, and the line says so once
	// the wait is over.
	f, opened, said = newFwd()
	f.takeURLs(out("https://localhost:8443/"), now)
	f.openWaitingURLs(now.Add(openURLForwardWait - time.Second))
	if len(*opened) != 0 || len(*said) != 0 {
		t.Errorf("inside the wait: opened %q, said %q", *opened, *said)
	}
	f.openWaitingURLs(now.Add(openURLForwardWait))
	if len(*opened) != 0 || len(*said) != 1 || (*said)[0] != "A program on the machine opened localhost:8443, and no forward reaches it." {
		t.Errorf("after the wait: opened %q, said %q", *opened, *said)
	}
	t.Logf("with no forward: opened %q, said %q", *opened, *said)

	// The forward appears a moment after the link, inside the wait.
	f, opened, said = newFwd()
	f.takeURLs(out("http://localhost:3000/"), now)
	f.openWaitingURLs(now)
	f.fwd[3000] = forwardEntry{Local: 3000, Host: "127.0.0.1"}
	f.openWaitingURLs(now.Add(2 * time.Second))
	if len(*opened) != 1 || (*opened)[0] != "http://localhost:3000/" || len(*said) != 0 {
		t.Errorf("a forward that came a moment later: opened %q, said %q", *opened, *said)
	}
	t.Logf("a forward two seconds later: opened %q", *opened)

	// A link that needs no forward still opens at once.
	f, opened, _ = newFwd()
	f.takeURLs(out("https://github.com/login"), now)
	if len(*opened) != 1 || (*opened)[0] != "https://github.com/login" {
		t.Errorf("an https link: opened %q", *opened)
	}
}
