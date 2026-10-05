package gateway

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/heracraft/repose/internal/ca/sshca"
	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// firstLine connects from localIP ("" for any) and returns the first line
// the gateway sends, without its line ending.
func firstLine(t *testing.T, localIP, addr string) string {
	t.Helper()
	c, err := dialFrom(localIP, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, _ := bufio.NewReader(c).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func dialFrom(localIP, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 3 * time.Second}
	if localIP != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(localIP)}
	}
	return d.Dial("tcp", addr)
}

// dialSSHFrom is harness.dial from a chosen loopback address.
func (h *harness) dialSSHFrom(localIP, login string, auth ssh.Signer) (*ssh.Client, error) {
	nc, err := dialFrom(localIP, h.addr)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(auth)},
		HostKeyCallback: h.hostKeyCallback(),
		Timeout:         5 * time.Second,
	}
	sc, chans, reqs, err := ssh.NewClientConn(nc, h.addr, cfg)
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	return ssh.NewClient(sc, chans, reqs), nil
}

// waitClosed waits for the client connection to end.
func waitClosed(t *testing.T, c *ssh.Client, within time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("connection still open after %v", within)
	}
}

// waitLog waits for a log line containing want.
func (h *harness) waitLog(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(h.logs.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no log line with %s:\n%s", want, h.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The revocation set must hold a serial for as long as a certificate can
// live (I-434).
func TestRevocationKeptForTheCertLifetime(t *testing.T) {
	if revocationKeep < sshca.UserCertTTL+30*time.Minute {
		t.Fatalf("revocationKeep %v does not cover UserCertTTL %v plus skew", revocationKeep, sshca.UserCertTTL)
	}
	h := newHarness(t, harnessOpts{revRefresh: 50 * time.Millisecond})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, sshca.UserCertTTL)
	h.api.RevokeSerial(cert.PublicKey().(*ssh.Certificate).Serial)
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, banner, err := h.dial(h.login, cert)
		if err != nil && banner == MsgRevoked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revocation not picked up: err=%v banner=%q", err, banner)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// 23 hours later the certificate is still valid; it must still be
	// refused, through many incremental refreshes.
	for _, hours := range []int64{6, 13, 18, 23} {
		h.offset.Store(hours * 3600)
		time.Sleep(300 * time.Millisecond) // a few refreshes at the new time
		c, banner, err := h.dial(h.login, cert)
		if err == nil {
			_ = c.Close()
			t.Fatalf("revoked certificate accepted %dh after revocation", hours)
		}
		if banner != MsgRevoked {
			t.Fatalf("%dh after revocation: banner %q", hours, banner)
		}
	}
}

func TestCertLongerThanRevocationMemoryRefused(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, revocationKeep+time.Hour)
	c, banner, err := h.dial(h.login, cert)
	if err == nil {
		_ = c.Close()
		t.Fatal("certificate valid past the revocation memory accepted")
	}
	if banner != MsgBadCA {
		t.Fatalf("banner %q", banner)
	}
	// The api's own lifetime passes.
	c, banner, err = h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, sshca.UserCertTTL))
	if err != nil {
		t.Fatalf("UserCertTTL certificate: %v (banner %q)", err, banner)
	}
	_ = c.Close()
}

// One source opening many idle connections holds only its own pre-auth
// slots; another client still gets in (I-435).
func TestOneSourceCannotFillTheGateway(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	var idle []net.Conn
	defer func() {
		for _, c := range idle {
			_ = c.Close()
		}
	}()
	for range 2 * DefaultMaxConns {
		c, err := dialFrom("127.0.0.1", h.addr)
		if err != nil {
			t.Fatal(err)
		}
		idle = append(idle, c)
	}
	time.Sleep(200 * time.Millisecond)
	if n := h.gw.preAuth.open(); n > DefaultMaxAuthPerIP {
		t.Fatalf("one source holds %d pre-auth slots, want at most %d", n, DefaultMaxAuthPerIP)
	}
	if n := h.gw.conns.open(); n != 0 {
		t.Fatalf("unauthenticated connections hold %d relay slots", n)
	}
	_, key := genKey(t)
	c, err := h.dialSSHFrom("127.0.0.2", h.login, h.userCert(t, key, []string{h.project.ID}, time.Hour))
	if err != nil {
		t.Fatalf("second source refused while the first idles: %v", err)
	}
	out, _, status := run(t, c, "echo still here")
	if out != "still here\n" || status != 0 {
		t.Fatalf("echo: %q %d", out, status)
	}
	_ = c.Close()
}

func TestPreAuthGlobalCap(t *testing.T) {
	h := newHarness(t, harnessOpts{maxPreAuth: 2, maxAuthPerIP: 10, authTimeout: 3 * time.Second})
	for range 2 {
		c, err := dialFrom("127.0.0.1", h.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
	}
	time.Sleep(100 * time.Millisecond)
	if line := firstLine(t, "127.0.0.2", h.addr); line != "repose gateway: "+MsgBusy {
		t.Fatalf("third pre-auth connection: first line %q", line)
	}
}

// The pre-auth deadline closes a client that never authenticates.
func TestAuthDeadline(t *testing.T) {
	h := newHarness(t, harnessOpts{authTimeout: 300 * time.Millisecond})
	c, err := dialFrom("", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 512)
	for {
		if _, err := c.Read(buf); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("idle connection still open after the auth deadline")
			}
			break
		}
	}
	// The client sees the close before the server's connection goroutine
	// has released its slot, so the count is read until it settles.
	deadline := time.Now().Add(2 * time.Second)
	for h.gw.preAuth.open() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := h.gw.preAuth.open(); n != 0 {
		t.Fatalf("pre-auth slots held after the deadline: %d", n)
	}
}

func TestPerUserRelayCap(t *testing.T) {
	h := newHarness(t, harnessOpts{maxConnsPerUser: 1})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	c1, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	h.waitOpen(t, 1)
	c2, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, status := run(t, c2, "echo no")
	_ = c2.Close()
	if status != 255 || !strings.Contains(stderr, MsgUserBusy) {
		t.Fatalf("second relay of one user: status %d stderr %q", status, stderr)
	}
	_ = c1.Close()
	h.waitOpen(t, 0)
	c, _, err := h.dial(h.login, cert)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if out, _, _ := run(t, c, "echo again"); out != "again\n" {
		t.Fatalf("slot not released after the first relay closed: %q", out)
	}
}

// waitOpen waits until n relay slots are held.
func (h *harness) waitOpen(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.gw.Open() != n {
		if time.Now().After(deadline) {
			t.Fatalf("relay slots held: %d, want %d", h.gw.Open(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// queryMeta is the ConnMetadata of a connection still authenticating.
type queryMeta struct{ user string }

func (m queryMeta) User() string          { return m.user }
func (m queryMeta) SessionID() []byte     { return []byte("s") }
func (m queryMeta) ClientVersion() []byte { return []byte("SSH-2.0-test") }
func (m queryMeta) ServerVersion() []byte { return []byte("SSH-2.0-gw") }
func (m queryMeta) RemoteAddr() net.Addr  { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 3), Port: 1} }
func (m queryMeta) LocalAddr() net.Addr   { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22} }

// ssh calls the public key callback for every key a client asks about,
// and a query without a signature is not an authentication: however many
// accepted queries a connection makes, it holds no relay or per-user slot
// until its handshake finishes, and then one of each (I-435).
func TestKeyQueriesTakeNoRelaySlot(t *testing.T) {
	h := newHarness(t, harnessOpts{maxConns: 3})
	var certs []ssh.Signer
	for range 2 {
		_, k := genKey(t)
		certs = append(certs, h.userCert(t, k, []string{h.project.ID}, time.Hour))
	}
	st := &connState{accepted: map[uint64]*authRecord{}}
	cb := h.gw.serverConfig(context.Background(), st).PublicKeyCallback
	for i := range 20 {
		if _, err := cb(queryMeta{h.login}, certs[i%2].PublicKey()); err != nil {
			t.Fatalf("query %d refused: %v", i, err)
		}
	}
	if n := h.gw.Open(); n != 0 {
		t.Fatalf("20 accepted key queries hold %d relay slots", n)
	}
	if len(st.accepted) != 2 {
		t.Fatalf("records for %d certificates, want 2", len(st.accepted))
	}
	// More distinct certificates than maxAcceptedKeys on one connection
	// are refused.
	for i := range maxAcceptedKeys {
		_, k := genKey(t)
		_, err := cb(queryMeta{h.login}, h.userCert(t, k, []string{h.project.ID}, time.Hour).PublicKey())
		if i < maxAcceptedKeys-2 && err != nil {
			t.Fatalf("certificate %d refused: %v", i, err)
		}
		if i == maxAcceptedKeys-1 && err == nil {
			t.Fatal("more than maxAcceptedKeys certificates accepted on one connection")
		}
	}
	// A client offering both keys authenticates with the first, holds one
	// relay and one user slot, and gives both back.
	for n := range 3 {
		c, banner, err := h.dial(h.login, certs[n%2])
		if err != nil {
			t.Fatalf("dial %d: %v (banner %q)", n, err, banner)
		}
		if out, _, status := run(t, c, "echo ok"); out != "ok\n" || status != 0 {
			t.Fatalf("dial %d: %q %d", n, out, status)
		}
		h.waitOpen(t, 1)
		_ = c.Close()
		h.waitOpen(t, 0)
	}
	h.gw.users.mu.Lock()
	held := len(h.gw.users.n)
	h.gw.users.mu.Unlock()
	if held != 0 {
		t.Fatalf("per-user slots held after every connection closed: %d", held)
	}
}

// An open relay ends when its certificate is revoked, whether the
// revocation arrives as a push or through the api refresh (I-436).
func TestRevocationEndsOpenRelays(t *testing.T) {
	h := newHarness(t, harnessOpts{revRefresh: 50 * time.Millisecond})
	_, key := genKey(t)
	pushed := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	refreshed := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	untouched := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	var clients []*ssh.Client
	for _, cert := range []ssh.Signer{pushed, refreshed, untouched} {
		c, banner, err := h.dial(h.login, cert)
		if err != nil {
			t.Fatalf("dial: %v (banner %q)", err, banner)
		}
		defer func() { _ = c.Close() }()
		if out, _, _ := run(t, c, "echo up"); out != "up\n" {
			t.Fatalf("echo: %q", out)
		}
		clients = append(clients, c)
	}
	h.gw.Revoke(pushed.PublicKey().(*ssh.Certificate).Serial)
	waitClosed(t, clients[0], 3*time.Second)
	h.api.RevokeSerial(refreshed.PublicKey().(*ssh.Certificate).Serial)
	waitClosed(t, clients[1], 3*time.Second)
	if out, _, _ := run(t, clients[2], "echo still up"); out != "still up\n" {
		t.Fatalf("a relay on another certificate was ended: %q", out)
	}
	h.waitLog(t, `"reason":"revoked"`)
}

// An open relay ends when its certificate expires, so a multiplexed
// connection cannot outlive it.
func TestCertExpiryEndsOpenRelay(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	c, banner, err := h.dial(h.login, h.userCert(t, key, []string{h.project.ID}, 2*time.Second))
	if err != nil {
		t.Fatalf("dial: %v (banner %q)", err, banner)
	}
	defer func() { _ = c.Close() }()
	if out, _, _ := run(t, c, "echo up"); out != "up\n" {
		t.Fatalf("echo: %q", out)
	}
	waitClosed(t, c, 5*time.Second)
	h.waitLog(t, `"reason":"cert_expired"`)
}

// A login under another user's handle gets one answer whether or not the
// project exists (I-437); the caller's own missing project still says so.
func TestOtherUsersLoginsAnswerAlike(t *testing.T) {
	other := fakeapi.User{ID: "00000000-0000-7000-8000-000000000002", Handle: "craft", Email: "craft@example.com", GitHubLogin: "craft"}
	h := newHarness(t, harnessOpts{users: map[string]fakeapi.User{"t1": fakeapi.CannedUser, "t2": other}})
	if _, err := h.api.CreateProjectFor(other.ID, "secret-plan", "small"); err != nil {
		t.Fatal(err)
	}
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	var banners []string
	for _, login := range []string{"secret-plan.craft", "no-such-plan.craft"} {
		before := counterValue(t, h.metrics.AuthFailTotal.WithLabelValues(ResultWrongPrincipal))
		c, banner, err := h.dial(login, cert)
		if err == nil {
			_ = c.Close()
			t.Fatalf("%s accepted", login)
		}
		if after := counterValue(t, h.metrics.AuthFailTotal.WithLabelValues(ResultWrongPrincipal)); after != before+1 {
			t.Fatalf("%s: wrong_principal %v -> %v", login, before, after)
		}
		banners = append(banners, banner)
	}
	if banners[0] != banners[1] || banners[0] != MsgWrongPrincipal {
		t.Fatalf("existing and missing projects of another user answer differently: %q", banners)
	}
	_, banner, err := h.dial("no-such-plan."+fakeapi.CannedUser.Handle, cert)
	if err == nil || !strings.Contains(banner, "no such project") {
		t.Fatalf("own missing project: err=%v banner=%q", err, banner)
	}
}

// A certificate the api issued for the gateway's own dial to a guest is
// never accepted from a client, even for the right project.
func TestGatewayCertFromClientRefused(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert, err := h.ca.SignUserCert(key.PublicKey(), fakeapi.CannedUser.ID+":"+fakeapi.CannedUser.Handle+":via-gateway", []string{h.project.ID}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewCertSigner(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	c, banner, err := h.dial(h.login, signer)
	if err == nil {
		_ = c.Close()
		t.Fatal("gateway certificate accepted from a client")
	}
	if banner != MsgWrongPrincipal {
		t.Fatalf("banner %q", banner)
	}
}

func TestCertUser(t *testing.T) {
	for _, c := range []struct {
		keyID, user, handle string
		ok                  bool
	}{
		{"u1:heracraft", "u1", "heracraft", true},
		{"u1:heracraft:via-gateway", "", "", false},
		{"heracraft", "", "", false},
		{":heracraft", "", "", false},
		{"u1:", "", "", false},
	} {
		u, hd, ok := certUser(c.keyID)
		if u != c.user || hd != c.handle || ok != c.ok {
			t.Errorf("%q: %q %q %v", c.keyID, u, hd, ok)
		}
	}
}
