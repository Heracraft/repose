package gateway

import (
	"encoding/pem"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// execLikeOpenSSH runs cmd on a raw session channel the way an OpenSSH
// client with a closed stdin does (a ControlMaster session with stdin
// from /dev/null): it sends its own EOF at once, answers channel requests
// at once (CHANNEL_FAILURE for anything it does not know), and sends
// CHANNEL_CLOSE as soon as the server's EOF arrives and the output is
// drained, without waiting for an exit status. It returns the output,
// the exit status (-1 when none came), and the channel keepalives the
// client saw.
func execLikeOpenSSH(t *testing.T, c *ssh.Client, cmd string) (string, int, int) {
	t.Helper()
	ch, reqs, err := c.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	status := -1
	keepalives := 0
	reqsDone := make(chan struct{})
	go func() {
		defer close(reqsDone)
		for r := range reqs {
			switch r.Type {
			case "exit-status":
				var s struct{ Status uint32 }
				_ = ssh.Unmarshal(r.Payload, &s)
				status = int(s.Status)
			case "keepalive@openssh.com":
				keepalives++
			}
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
		}
	}()
	ok, err := ch.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{cmd}))
	if err != nil || !ok {
		t.Fatalf("exec: %v %v", ok, err)
	}
	_ = ch.CloseWrite()
	out, _ := io.ReadAll(ch)
	_ = ch.Close()
	select {
	case <-reqsDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the channel's requests never ended")
	}
	return string(out), status, keepalives
}

// The same with the real OpenSSH client over a ControlMaster, the way the
// CLI runs every command (I-149): each of eight commands must exit 7.
func TestRelayExitStatusOverOpenSSHControlMaster(t *testing.T) {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh client")
	}
	h := newHarness(t, harnessOpts{keepalive: 150 * time.Millisecond})
	priv, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	dir, err := os.MkdirTemp("", "gw-ssh-") // short: the ControlPath is a unix socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(h.addr)
	files := map[string][]byte{
		"id":          pem.EncodeToMemory(pemBlock),
		"id-cert.pub": ssh.MarshalAuthorizedKey(cert.PublicKey()),
		"known_hosts": []byte("@cert-authority * " + string(ssh.MarshalAuthorizedKey(h.ca.Host.Signer.PublicKey()))),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-F", "/dev/null", "-p", port, "-l", h.login,
		"-o", "IdentityFile=" + filepath.Join(dir, "id"), "-o", "CertificateFile=" + filepath.Join(dir, "id-cert.pub"),
		"-o", "IdentitiesOnly=yes", "-o", "UserKnownHostsFile=" + filepath.Join(dir, "known_hosts"),
		"-o", "BatchMode=yes", "-o", "ControlMaster=auto", "-o", "ControlPath=" + filepath.Join(dir, "cm"),
		"-o", "ControlPersist=30", host}
	t.Cleanup(func() { _ = exec.Command(sshBin, append([]string{"-O", "exit"}, args...)...).Run() })
	for i := 0; i < 8; i++ {
		cmd := exec.Command(sshBin, append(args, "sshd-exit 300ms")...)
		cmd.Stdin = nil // /dev/null: the client's EOF goes at once, as in the failing runs
		out, err := cmd.Output()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != 7 || string(out) != "alive\n" {
			t.Fatalf("run %d: exit %d, output %q; want 7 and \"alive\\n\"", i, code, out)
		}
	}
}

// A guest that accepts the TCP connection and never speaks SSH is given up
// on after the dial timeout, with the "not ready" refusal, and a guest
// whose handshake completes within it keeps working afterwards (the
// handshake limit is a timer, not a socket deadline left to clear).
func TestGuestHandshakeTimeout(t *testing.T) {
	h := newHarness(t, harnessOpts{dialTimeout: 500 * time.Millisecond})
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
			defer func() { _ = c.Close() }() // silent until the test ends
		}
	}()
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	h.route(h.project.GuestIP, ln.Addr().String())
	c, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, errb, status := run(t, c, "true")
	_ = c.Close()
	if status != 255 || errb != MsgNotReady+"\r\n" || time.Since(start) > 5*time.Second {
		t.Fatalf("silent guest: status %d, stderr %q after %s", status, errb, time.Since(start))
	}
	// A real guest: the session outlives the dial timeout by far.
	h.route(h.project.GuestIP, h.guest.addr())
	c, _, err = h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if out, _, status := run(t, c, "echo first"); out != "first\n" || status != 0 {
		t.Fatalf("first: %q %d", out, status)
	}
	time.Sleep(1200 * time.Millisecond)
	if out, _, status := run(t, c, "echo later"); out != "later\n" || status != 0 {
		t.Fatalf("after the dial timeout passed: %q %d", out, status)
	}
}

// DECISIONS I-212. The guest's sshd ends a command with EOF and then the
// exit status; an OpenSSH client whose stdin is already closed answers
// the EOF with CHANNEL_CLOSE at once. The gateway relayed EOF the moment
// the guest's output ended but the exit status from another goroutine,
// so the client often closed first and reported 255 (first sync of
// golang/go over a ControlMaster). The gateway must deliver the exit
// status before the EOF, and answer the guest's channel keepalive itself
// rather than hold the request stream on a client round trip. Run with a
// short gateway keepalive and a command that outlives a few of them.
func TestRelayDeliversExitStatusBeforeEOF(t *testing.T) {
	h := newHarness(t, harnessOpts{keepalive: 150 * time.Millisecond})
	_, key := genKey(t)
	c, banner, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatalf("dial: %v (banner %q)", err, banner)
	}
	defer func() { _ = c.Close() }()
	for i := 0; i < 8; i++ {
		out, status, keepalives := execLikeOpenSSH(t, c, "sshd-exit 500ms")
		if out != "alive\n" || status != 7 {
			t.Fatalf("run %d: output %q, exit status %d; want \"alive\\n\" and 7", i, out, status)
		}
		if keepalives != 0 {
			t.Errorf("run %d: the guest's channel keepalive reached the client %d times", i, keepalives)
		}
	}
}

// I-740. An OpenSSH client with stdin from /dev/null sends its EOF right
// after the exec request, without waiting for the answer. The gateway
// relays the exec from one goroutine and the EOF from another, so the
// EOF could reach the guest's sshd first, where it closes nothing: the
// command's stdin never ended, and `repose stop`'s read of the checkout
// (which reads stdin) hung in 3 of 8 stops made just after a start on
// prod. The gateway holds a session's EOF until the guest has accepted
// the exec.
func TestRelayHoldsStdinEOFUntilTheExecIsAccepted(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	c, banner, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatalf("dial: %v (banner %q)", err, banner)
	}
	defer func() { _ = c.Close() }()
	for i := 0; i < 3; i++ {
		ch, reqs, err := c.OpenChannel("session", nil)
		if err != nil {
			t.Fatal(err)
		}
		go ssh.DiscardRequests(reqs)
		accepted := make(chan bool, 1)
		go func() {
			ok, _ := ch.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{"eof-order"}))
			accepted <- ok
		}()
		time.Sleep(20 * time.Millisecond) // the exec is on the wire; OpenSSH sends its EOF now
		_ = ch.CloseWrite()
		out, _ := io.ReadAll(ch)
		_ = ch.Close()
		if ok := <-accepted; !ok || string(out) != "eof-after-exec\n" {
			t.Fatalf("run %d: exec accepted %v, guest said %q; want \"eof-after-exec\\n\"", i, ok, out)
		}
	}
}
