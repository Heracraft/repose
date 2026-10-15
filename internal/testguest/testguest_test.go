package testguest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func dial(t *testing.T, g *Guest, priv string) *ssh.Client {
	t.Helper()
	pem, err := os.ReadFile(priv)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ssh.Dial("tcp", g.Addr, &ssh.ClientConfig{
		User:            "dev",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A command still running when the test closes the guest is ended with
// it: a test's cleanup removes the guest's home next, and a command
// writing there made RemoveAll fail with "directory not empty"
// (TestSyncRefusesAnUnrelatedCheckout, 2026-10-15).
func TestCloseEndsRunningCommands(t *testing.T) {
	home := t.TempDir()
	priv, pub, err := GenerateClientKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(home, pub)
	if err != nil {
		t.Fatal(err)
	}
	c := dial(t, g, priv)
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start("touch started; sleep 2; touch late"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(home, "started")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The client goes without waiting, as a CLI that returned does.
	_ = c.Close()
	start := time.Now()
	g.Close()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Close took %s", d)
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(filepath.Join(home, "late")); err == nil {
		t.Fatal("a command ran on after Close and wrote into the guest's home")
	}
}

// The guest's git starts no detached maintenance, which would run in a
// session of its own that Close cannot end.
func TestGitAutoMaintenanceIsOff(t *testing.T) {
	home := t.TempDir()
	priv, pub, err := GenerateClientKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(home, pub)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := dial(t, g, priv)
	defer func() { _ = c.Close() }()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Output("git config --get maintenance.auto; git config --get gc.auto")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(out)); len(got) != 2 || got[0] != "false" || got[1] != "0" {
		t.Fatalf("git's maintenance.auto and gc.auto in the guest: %q", out)
	}
}
