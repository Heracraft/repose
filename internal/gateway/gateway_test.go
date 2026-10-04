package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func TestRelayExecExitStatusAndStderr(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	c, banner, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatalf("dial: %v (banner %q)", err, banner)
	}
	defer func() { _ = c.Close() }()
	out, _, status := run(t, c, "echo hello relay")
	if out != "hello relay\n" || status != 0 {
		t.Fatalf("echo: %q %d", out, status)
	}
	_, _, status = run(t, c, "exit 3")
	if status != 3 {
		t.Fatalf("exit status %d, want 3", status)
	}
	_, errb, _ := run(t, c, "stderr on the error stream")
	if errb != "on the error stream\n" {
		t.Fatalf("stderr %q", errb)
	}
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	in := bytes.Repeat([]byte("abc"), 100000)
	sess.Stdin = bytes.NewReader(in)
	var out2 bytes.Buffer
	sess.Stdout = &out2
	if err := sess.Run("cat"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out2.Bytes(), in) {
		t.Fatalf("cat relayed %d bytes, want %d", out2.Len(), len(in))
	}
	if counterValue(t, h.metrics.SessionsTotal) < 1 {
		t.Fatal("sessions_total not incremented on an accepted relay")
	}
}

func TestRelayEnvFilter(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	c, _, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.Setenv("TERM", "xterm-256color"); err != nil {
		t.Fatalf("TERM refused: %v", err)
	}
	if err := sess.Setenv("LC_ALL", "C.UTF-8"); err != nil {
		t.Fatalf("LC_ALL refused: %v", err)
	}
	if err := sess.Setenv("LD_PRELOAD", "/tmp/evil.so"); err == nil {
		t.Fatal("LD_PRELOAD was accepted")
	}
	var out strings.Builder
	sess.Stdout = &out
	if err := sess.Run("env"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "LC_ALL=C.UTF-8\nTERM=xterm-256color\n" {
		t.Fatalf("guest saw %q", out.String())
	}
	envs, _, _, _ := h.guest.snapshot()
	if _, leaked := envs["LD_PRELOAD"]; leaked {
		t.Fatal("LD_PRELOAD reached the guest")
	}
}

func TestRelayPtyAndWindowChange(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	c, _, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("pty-req: %v", err)
	}
	if err := sess.WindowChange(50, 132); err != nil {
		t.Fatalf("window-change: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	rd := newLineReader(stdout)
	if l := rd.line(t); l != "shell ready" {
		t.Fatalf("first line %q", l)
	}
	if _, err := io.WriteString(stdin, "hello\n"); err != nil {
		t.Fatal(err)
	}
	if l := rd.line(t); l != "you said: hello" {
		t.Fatalf("echo line %q", l)
	}
	if _, err := io.WriteString(stdin, "exit\n"); err != nil {
		t.Fatal(err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("shell exit: %v", err)
	}
	_, _, ptys, winch := h.guest.snapshot()
	if ptys != 1 || winch != 1 {
		t.Fatalf("guest saw pty=%d winch=%d", ptys, winch)
	}
	out, _, _ := run(t, c, "pty")
	if !strings.Contains(out, "cols=132 rows=50") {
		t.Fatalf("window size not relayed: %q", out)
	}
}

func TestRelayLocalForward(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	echo := echoServer(t)
	_, key := genKey(t)
	c, _, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	conn, err := c.Dial("tcp", echo)
	if err != nil {
		t.Fatalf("direct-tcpip: %v", err)
	}
	defer func() { _ = conn.Close() }()
	msg := bytes.Repeat([]byte("forward me "), 20000)
	go func() { _, _ = conn.Write(msg) }()
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("echo mismatch through -L")
	}
	if _, err := c.Dial("tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("a refused target did not fail the channel open")
	}
}

func TestRelayRemoteForward(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	c, _, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ln, err := c.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcpip-forward: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			rc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = rc.Close() }()
				_, _ = io.Copy(rc, rc)
			}()
		}
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("connect to the remote-forward port: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("reverse")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "reverse" {
		t.Fatalf("-R echo: %q %v", buf, err)
	}
}

// TestRelayRefusesAgentForwarding is I-247: a client that still asks for
// agent forwarding (an old ~/.ssh/repose/config, or ssh -A) is told no,
// the request never reaches the guest, and an agent channel the guest
// opens anyway is rejected, so nothing in a guest can sign with the
// laptop's keys. The exec still returns (the I-110 hang stays fixed).
func TestRelayRefusesAgentForwarding(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	priv, key := genKey(t)
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	c, _, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := agent.ForwardToAgent(c, keyring); err != nil {
		t.Fatal(err)
	}
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if err := agent.RequestAgentForwarding(sess); err == nil {
		t.Fatal("auth-agent-req was accepted; the gateway must refuse agent forwarding")
	}
	var out, errb strings.Builder
	sess.Stdout, sess.Stderr = &out, &errb
	done := make(chan error, 1)
	go func() { done <- sess.Run("agent") }()
	select {
	case err := <-done:
		var ee *ssh.ExitError
		if !errors.As(err, &ee) || ee.ExitStatus() != 1 {
			t.Fatalf("agent: want exit 1 from a rejected agent channel, got %v (out %q, err %q)", err, out.String(), errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("exec did not return after the agent channel was rejected")
	}
	if strings.Contains(out.String(), "keys=") || !strings.Contains(errb.String(), "agent channel") {
		t.Fatalf("guest reached the agent: out %q, err %q", out.String(), errb.String())
	}
	_, agentReq, _, _ := h.guest.snapshot()
	if agentReq {
		t.Fatal("auth-agent-req reached the guest")
	}
}

func TestSessionReportsAndCertCache(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	for i := 0; i < 3; i++ {
		c, _, err := h.dial(h.login, cert)
		if err != nil {
			t.Fatal(err)
		}
		run(t, c, "echo x")
		_ = c.Close()
	}
	if n := h.api.GatewayCertCalls(); n != 1 {
		t.Fatalf("gateway-certs called %d times for three connections; the 4-minute cache should make it one", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		reports := h.api.SessionReports()
		opened, closed := 0, 0
		for _, r := range reports {
			if r.ProjectID != h.project.ID || r.CertSerial == 0 {
				t.Fatalf("bad report %+v", r)
			}
			if r.Event == "opened" {
				opened++
			} else {
				closed++
			}
			// A close is reported only after its open (I-123): in report
			// order, closes never outnumber opens.
			if closed > opened {
				t.Fatalf("a session's close was reported before its open: %+v", reports)
			}
		}
		if opened == 3 && closed == 3 {
			// Three connections under one certificate are three sessions
			// (I-176): each has its own id, and its close names it.
			ids := map[string]int{}
			for _, r := range reports {
				if len(r.SessionID) != 32 {
					t.Fatalf("session id %q", r.SessionID)
				}
				ids[r.SessionID]++
			}
			if len(ids) != 3 {
				t.Fatalf("three connections of one certificate reported %d session ids: %+v", len(ids), reports)
			}
			for id, n := range ids {
				if n != 2 {
					t.Fatalf("session %s reported %d times, want an open and a close", id, n)
				}
			}
			break
		}
		if opened > 3 || closed > 3 {
			t.Fatalf("a session report was delivered twice: %+v", reports)
		}
		if time.Now().After(deadline) {
			t.Fatalf("session reports: %+v", reports)
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, `"event":"session_open"`) || !strings.Contains(logs, `"event":"session_close"`) {
		t.Fatalf("session log lines missing:\n%s", logs)
	}
	if strings.Contains(logs, "heracraft") || strings.Contains(logs, "127.0.0.1:") {
		t.Fatalf("log carries the handle or a full source address:\n%s", logs)
	}
}

func TestAuthRefusals(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	other, err := h.api.CreateProject("other", "small")
	if err != nil {
		t.Fatal(err)
	}
	otherCA, err := newTestCA()
	if err != nil {
		t.Fatal(err)
	}
	_, key := genKey(t)
	foreign, err := otherCA.SignUserCert(key.PublicKey(), "x", []string{h.project.ID}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	foreignSigner, err := ssh.NewCertSigner(foreign, key)
	if err != nil {
		t.Fatal(err)
	}
	expired := h.userCert(t, key, []string{h.project.ID}, -30*time.Minute)
	revokedCert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	h.gw.Revoke(revokedCert.PublicKey().(*ssh.Certificate).Serial)
	noPrincipals := h.userCert(t, key, nil, time.Hour)

	cases := []struct {
		name   string
		login  string
		auth   ssh.Signer
		banner string
		result string
	}{
		{"plain key", h.login, key, MsgCertRequired, ResultNoCert},
		{"foreign CA", h.login, foreignSigner, MsgBadCA, ResultBadCA},
		{"expired", h.login, expired, MsgExpired, ResultExpired},
		{"revoked", h.login, revokedCert, MsgRevoked, ResultRevoked},
		{"wrong principal", other.Slug + "." + fakeapi.CannedUser.Handle, h.userCert(t, key, []string{h.project.ID}, time.Hour), MsgWrongPrincipal, ResultWrongPrincipal},
		{"no principals", h.login, noPrincipals, MsgWrongPrincipal, ResultWrongPrincipal},
		{"bad login", "todo-app", h.userCert(t, key, []string{h.project.ID}, time.Hour), BadLoginMessage, ResultBadLogin},
		{"unknown project", "nope." + fakeapi.CannedUser.Handle, h.userCert(t, key, []string{h.project.ID}, time.Hour), fmt.Sprintf(MsgNotFoundFmt, "nope."+fakeapi.CannedUser.Handle), ResultNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := counterValue(t, h.metrics.AuthFailTotal.WithLabelValues(c.result))
			conn, banner, err := h.dial(c.login, c.auth)
			if err == nil {
				_ = conn.Close()
				t.Fatal("connection accepted")
			}
			if !strings.Contains(banner, c.banner) {
				t.Fatalf("banner %q, want %q", banner, c.banner)
			}
			if after := counterValue(t, h.metrics.AuthFailTotal.WithLabelValues(c.result)); after != before+1 {
				t.Fatalf("auth_total{result=%s} %v -> %v", c.result, before, after)
			}
			t.Logf("%s: banner %q result=%s", c.name, banner, c.result)
		})
	}
	t.Log("plain key refused with certificate required; a valid certificate for the right project succeeds (TestRelayExecExitStatusAndStderr); wrong principal, expired and revoked each produced their message above")
}

func TestStoppedAndOtherStates(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	h.api.SetState(h.project.ID, "stopped")
	_, banner, err := h.dial(h.login, cert)
	if err == nil {
		t.Fatal("connected to a stopped project")
	}
	if want := fmt.Sprintf(MsgStoppedFmt, h.project.Slug, h.project.Slug); banner != want {
		t.Fatalf("banner %q, want %q", banner, want)
	}
	t.Logf("stopped banner: %q", banner)
	h.api.SetState(h.project.ID, "starting")
	h.offset.Add(6)
	_, banner, err = h.dial(h.login, cert)
	if err == nil {
		t.Fatal("connected to a starting project")
	}
	if banner != "todo-app is starting and not accepting connections yet; try again in a few seconds" {
		t.Fatalf("banner %q", banner)
	}
	h.api.SetState(h.project.ID, "destroying")
	h.offset.Add(6)
	if _, banner, _ = h.dial(h.login, cert); banner != "todo-app is being destroyed; `repose restore todo-app` brings it back once that is done" {
		t.Fatalf("destroying banner %q", banner)
	}
	h.api.SetState(h.project.ID, "running")
	h.offset.Add(6)
	h.route(h.projectRoute(t).GuestIP, h.guest.addr())
	c, banner, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatalf("running again: %v %q", err, banner)
	}
	_ = c.Close()
}

func (h *harness) projectRoute(t *testing.T) *Route {
	t.Helper()
	client, err := NewClient(h.api.URL(), nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r, err := client.Route(context.Background(), h.login)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAPIUnreachableAndStaleCaches(t *testing.T) {
	h := newHarness(t, harnessOpts{revRefresh: 100 * time.Millisecond})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	c, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	// An existing session is one already relaying: warm it up so the guest
	// dial (and its gateway certificate fetch) completes before the api
	// goes down.
	if out, _, _ := run(t, c, "echo warmup"); out != "warmup\n" {
		t.Fatalf("warmup: %q", out)
	}
	h.api.Fail("GET /internal/route", "internal")
	h.api.Fail("GET /internal/revoked", "internal")
	h.api.Fail("GET /internal/ca", "internal")
	h.api.Fail("POST /internal/gateway-certs", "internal")
	out, _, _ := run(t, c, "echo still here")
	if out != "still here\n" {
		t.Fatalf("existing session broke: %q", out)
	}
	// New connections use the caches: the route cache (5 s) then the api.
	h.offset.Add(6)
	_, banner, err := h.dial(h.login, cert)
	if err == nil {
		t.Fatal("route lookup succeeded with the api down")
	}
	if banner != MsgControlPlane {
		t.Fatalf("banner %q", banner)
	}
	// Restore the route and gateway-cert routes; the cached revocation list
	// is used within the hour ...
	h.api.Unfail("GET /internal/route")
	h.api.Unfail("POST /internal/gateway-certs")
	h.offset.Add(6)
	c2, banner, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatalf("cached revocation list not used: %v %q", err, banner)
	}
	_ = c2.Close()
	// ... and past the stale window new connections are refused again.
	h.offset.Add(int64(StaleAfter.Seconds()) + 60)
	time.Sleep(300 * time.Millisecond) // a refresh attempt fails against the api
	_, banner, err = h.dial(h.login, cert)
	if err == nil {
		t.Fatal("authenticated with a revocation list older than the stale window")
	}
	if banner != MsgControlPlane {
		t.Fatalf("banner %q", banner)
	}
	if out, _, _ := run(t, c, "echo still here"); out != "still here\n" {
		t.Fatalf("the existing session was dropped by the stale window: %q", out)
	}
	_ = c.Close()
	t.Logf("api outage: existing session kept working, new ones worked from the cache, then failed with %q", banner)
}

func TestRevocationTakesEffectWithinRefresh(t *testing.T) {
	h := newHarness(t, harnessOpts{revRefresh: 200 * time.Millisecond})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	c, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	serial := cert.PublicKey().(*ssh.Certificate).Serial
	h.api.RevokeSerial(serial)
	time.Sleep(700 * time.Millisecond)
	_, banner, err := h.dial(h.login, cert)
	if err == nil {
		t.Fatal("revoked certificate accepted after the refresh window")
	}
	if banner != MsgRevoked {
		t.Fatalf("banner %q", banner)
	}
}

func TestGuestNotReady(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	// sshd not up: the dial is refused.
	h.route(h.project.GuestIP, "127.0.0.1:1")
	c, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatalf("auth should succeed before the dial: %v", err)
	}
	_, errb, status := run(t, c, "true")
	if !strings.Contains(errb, MsgNotReady) || status != 255 {
		t.Fatalf("stderr %q status %d", errb, status)
	}
	_ = c.Close()
	// No route: the WireGuard peer is missing.
	h.mu.Lock()
	delete(h.dials, h.project.GuestIP+":22")
	h.mu.Unlock()
	c, _, err = h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	_, errb, _ = run(t, c, "true")
	if !strings.Contains(errb, "no route to host") {
		t.Fatalf("stderr %q", errb)
	}
	_ = c.Close()
	// A guest with a throwaway host key is not trusted.
	h.guest.usePlainHostKey(t)
	h.route(h.project.GuestIP, h.guest.addr())
	c, _, err = h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	_, errb, _ = run(t, c, "true")
	if !strings.Contains(errb, MsgNotReady) {
		t.Fatalf("stderr %q", errb)
	}
	_ = c.Close()
	if v := counterValue(t, h.metrics.DialFailTotal); v != 3 {
		t.Fatalf("dial_errors_total %v", v)
	}
	if !strings.Contains(h.logs.String(), `"reason":"no_route"`) {
		t.Fatalf("no dial_fail no_route log line:\n%s", h.logs.String())
	}
}

func TestConnectionCapAndPerSourceAuthLimit(t *testing.T) {
	// The connection cap: with room for two relays, the third is busy.
	h := newHarness(t, harnessOpts{maxConns: 2, authTimeout: 2 * time.Second})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	c1, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c1.Close() }()
	c2, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	_, banner, err := h.dial(h.login, cert)
	if err == nil || banner != MsgBusy {
		t.Fatalf("third connection: err=%v banner=%q", err, banner)
	}
	if v := counterValue(t, h.metrics.AuthFailTotal.WithLabelValues(ResultBusy)); v != 1 {
		t.Fatalf("auth_total{result=busy} %v", v)
	}

	// The per-source concurrent-auth cap, with the connection cap kept out of
	// the way: one raw connection parked in the auth phase holds the single
	// slot, so a second concurrent auth from the same address is refused
	// before any handshake, with one plain line (I-435).
	h2 := newHarness(t, harnessOpts{maxConns: 50, maxAuthPerIP: 1, authTimeout: 3 * time.Second})
	cert2 := h2.userCert(t, key, []string{h2.project.ID}, time.Hour)
	parked, err := net.Dial("tcp", h2.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parked.Close() }()
	time.Sleep(100 * time.Millisecond)
	if line := firstLine(t, "", h2.addr); line != "repose gateway: "+MsgRateLimited {
		t.Fatalf("second concurrent auth: first line %q", line)
	}
	if v := counterValue(t, h2.metrics.AuthFailTotal.WithLabelValues(ResultRateLimited)); v != 1 {
		t.Fatalf("auth_total{result=rate_limited} %v", v)
	}
	_ = parked.Close()
	time.Sleep(100 * time.Millisecond)
	c, _, err := h2.dial(h2.login, cert2)
	if err != nil {
		t.Fatalf("after the parked connection closed: %v", err)
	}
	_ = c.Close()
}

// soakDialTimeout is the gateway's guest dial timeout in the soak. 100
// simultaneous connections are 300 SSH handshakes (client->gateway,
// gateway->guest, both ends in this process), which under -race on a
// 2-core CI runner took past the harness's former 3 s: the dial failed and
// the gateway answered that session with its refusal, exit 255 and "not
// ready" (the CI failure), or the guest connection's handshake deadline
// fired just after it completed ("guest: read tcp ...: i/o timeout", the
// earlier flake). The soak measures relaying, not dial latency.
var soakDialTimeout = 60 * time.Second

func TestSoakHundredConnections(t *testing.T) {
	h := newHarness(t, harnessOpts{maxConns: 300, maxConnsPerUser: 300, maxPreAuth: 300, maxAuthPerIP: 300, dialTimeout: soakDialTimeout})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	runtime.GC()
	baseline := runtime.NumGoroutine()
	const n = 100
	const bytesEach = 1 << 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := h.dial(h.login, cert)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = c.Close() }()
			sess, err := c.NewSession()
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = sess.Close() }()
			var cw countWriter
			var stderr syncBuffer
			sess.Stdout = &cw
			sess.Stderr = &stderr
			if err := sess.Run(fmt.Sprintf("big %d", bytesEach)); err != nil {
				errs <- fmt.Errorf("%w (stderr %q)", err, stderr.String())
				return
			}
			if cw.n != bytesEach {
				errs <- fmt.Errorf("got %d bytes", cw.n)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	deadline := time.Now().Add(10 * time.Second)
	var now int
	for {
		runtime.GC()
		now = runtime.NumGoroutine()
		if now <= baseline+5 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if now > baseline+5 {
		t.Fatalf("goroutines: baseline %d, after soak %d", baseline, now)
	}
	if open := h.gw.Open(); open != 0 {
		t.Fatalf("%d connections still counted open", open)
	}
	relayed := counterValue(t, h.metrics.RelayBytesTotal.WithLabelValues("guest_to_client"))
	t.Logf("soak: %d connections x %d bytes in %s; goroutines baseline=%d after=%d; relay_bytes_total{guest_to_client}=%.0f", n, bytesEach, elapsed.Round(time.Millisecond), baseline, now, relayed)
	if relayed < n*bytesEach {
		t.Fatalf("relay_bytes_total %.0f < %d", relayed, n*bytesEach)
	}
}

func TestLogsCarryNoChannelContents(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	c, _, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	planted := "PLANTED-SECRET-7f3a9c"
	out, _, _ := run(t, c, "echo "+planted)
	if !strings.Contains(out, planted) {
		t.Fatal("relay lost the planted string")
	}
	_ = c.Close()
	time.Sleep(100 * time.Millisecond)
	logs := h.logs.String()
	if strings.Contains(logs, planted) {
		t.Fatalf("planted string in the log:\n%s", logs)
	}
	if strings.Contains(logs, "AAAA") {
		t.Fatalf("a key or certificate body in the log:\n%s", logs)
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if strings.Contains(line, `"source_prefix"`) && !strings.Contains(line, `"source_prefix":"127.0.0.0/24"`) {
			t.Fatalf("source not truncated: %s", line)
		}
	}
	t.Logf("planted %q relayed and absent from %d log lines", planted, strings.Count(logs, "\n"))
}

type countWriter struct{ n int }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	return len(p), nil
}

type lineReader struct {
	r   io.Reader
	buf []byte
}

func newLineReader(r io.Reader) *lineReader { return &lineReader{r: r} }

func (l *lineReader) line(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if i := bytes.IndexAny(l.buf, "\r\n"); i >= 0 {
			line := string(l.buf[:i])
			l.buf = bytes.TrimLeft(l.buf[i:], "\r\n")
			if line != "" {
				return line
			}
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("no line within 5 s; have %q", l.buf)
		}
		b := make([]byte, 256)
		n, err := l.r.Read(b)
		if err != nil {
			t.Fatalf("read: %v (have %q)", err, l.buf)
		}
		l.buf = append(l.buf, b[:n]...)
	}
}

// TestOneBannerPerRefusal is the conductor's 2026-09-23 transcript: ssh
// offered the certificate, then the plain key, on one connection, and the
// two banners arrived glued together ("...not accepting connections
// yetpermission denied (certificate required)"). Now the first refusal's
// banner ends in a newline and the plain key's "certificate required"
// after it is not shown (I-189).
func TestOneBannerPerRefusal(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	h.api.SetState(h.project.ID, "destroying")
	var banners []string
	cfg := &ssh.ClientConfig{
		User:            h.login,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(cert, key)},
		HostKeyCallback: h.hostKeyCallback(),
		BannerCallback: func(m string) error {
			banners = append(banners, m)
			return nil
		},
		Timeout: 5 * time.Second,
	}
	if c, err := ssh.Dial("tcp", h.addr, cfg); err == nil {
		_ = c.Close()
		t.Fatal("connected to a destroying project")
	}
	want := "todo-app is being destroyed; `repose restore todo-app` brings it back once that is done\n"
	if len(banners) != 1 || banners[0] != want {
		t.Fatalf("banners %q, want exactly [%q]", banners, want)
	}
	t.Logf("banners: %q", banners)
}
