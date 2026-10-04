package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/heracraft/repose/internal/ca/testca"
	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// These tests run the gateway as real processes, the way systemd does on
// the edge: the test binary is the gateway (TestMain), the test holds the
// listening socket as gateway-ssh.socket would, and plays systemd's
// notification socket (DECISIONS I-470, I-471).

const e2eGuestEnv = "GATEWAY_E2E_GUEST"

func TestMain(m *testing.M) {
	if addr := os.Getenv(e2eGuestEnv); addr != "" {
		// The gateway's dial to any guest goes to the test's fake guest.
		guestDial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// e2e is one fake api, one fake guest and the files a gateway reads.
type e2e struct {
	t        *testing.T
	ca       *testca.CA
	api      *fakeapi.Fake
	project  fakeapi.Project
	dir      string
	env      []string
	notify   *notifySink
	userAuth ssh.Signer
}

func newE2E(t *testing.T) *e2e { return newE2EIn(t, "") }

// newE2EIn keeps the files in dir, or a new temporary one when dir is "".
func newE2EIn(t *testing.T, dir string) *e2e {
	t.Helper()
	ca, err := testca.New()
	if err != nil {
		t.Fatal(err)
	}
	api := fakeapi.New(fakeapi.Options{CA: ca})
	t.Cleanup(api.Close)
	project, err := api.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	guest := startFakeGuest(t, ca, project.Slug+"."+fakeapi.CannedUser.Handle)
	if dir == "" {
		// The sockets live in a short path: a unix socket path has 108 bytes.
		if dir, err = os.MkdirTemp("", "gw-e2e"); err != nil {
			t.Fatal(err)
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	hostKey := writeKey(t, filepath.Join(dir, "host_key"))
	hostCert, err := ca.SignHostCert(hostKey.PublicKey(), "host:gateway", []string{"127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host_key-cert.pub"), ssh.MarshalAuthorizedKey(hostCert), 0o600); err != nil {
		t.Fatal(err)
	}
	writeKey(t, filepath.Join(dir, "gateway_key"))

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cert, err := ca.SignUserCert(signer.PublicKey(), fakeapi.CannedUser.ID+":"+fakeapi.CannedUser.Handle, []string{project.ID}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	userAuth, err := ssh.NewCertSigner(cert, signer)
	if err != nil {
		t.Fatal(err)
	}

	e := &e2e{t: t, ca: ca, api: api, project: project, dir: dir, userAuth: userAuth}
	e.notify = newNotifySink(t, filepath.Join(dir, "notify"))
	e.env = []string{
		"PATH=" + os.Getenv("PATH"),
		e2eGuestEnv + "=" + guest,
		"API_URL=" + api.URL(),
		"HOST_KEY=" + filepath.Join(dir, "host_key"),
		"HOST_CERT=" + filepath.Join(dir, "host_key-cert.pub"),
		"GATEWAY_SSH_KEY=" + filepath.Join(dir, "gateway_key"),
		"GATEWAY_CONTROL=" + filepath.Join(dir, "control.sock"),
		"NOTIFY_SOCKET=" + filepath.Join(dir, "notify"),
		"PREVIEW_TLS_CERT=",
		"LOG_LEVEL=debug",
	}
	return e
}

func writeKey(t *testing.T, path string) ssh.Signer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := ssh.NewSignerFromKey(priv)
	return s
}

// socket is the listening socket the test holds for the whole test, as
// systemd holds gateway-ssh.socket across restarts.
func socket(t *testing.T) (*os.File, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // f keeps the socket
	t.Cleanup(func() { _ = f.Close() })
	return f, addr
}

// startGateway starts exe as systemd starts the unit: the socket as fd 3,
// named ssh.
func (e *e2e) startGateway(exe string, sock *os.File) *exec.Cmd {
	e.t.Helper()
	cmd := exec.Command(exe, "serve")
	cmd.Env = append(append([]string{}, e.env...), "LISTEN_FDS=1", "LISTEN_FDNAMES=ssh")
	cmd.ExtraFiles = []*os.File{sock}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	e.notify.want(e.t, "READY=1")
	return cmd
}

// handover runs `exe handover` as ExecReload does.
func (e *e2e) handover(exe string) (string, error) {
	cmd := exec.Command(exe, "handover")
	cmd.Env = e.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *e2e) dial(addr string) *ssh.Client {
	e.t.Helper()
	c, err := e.tryDial(addr, 5*time.Second)
	if err != nil {
		e.t.Fatalf("dial %s: %v", addr, err)
	}
	return c
}

func (e *e2e) tryDial(addr string, timeout time.Duration) (*ssh.Client, error) {
	checker := &ssh.CertChecker{IsHostAuthority: func(auth ssh.PublicKey, _ string) bool {
		return string(auth.Marshal()) == string(e.ca.Host.Signer.PublicKey().Marshal())
	}}
	return ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            e.project.Slug + "." + fakeapi.CannedUser.Handle,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(e.userAuth)},
		HostKeyCallback: checker.CheckHostKey,
		Timeout:         timeout,
	})
}

// echoSession is a long-running session through the gateway: what is
// written comes back.
type echoSession struct {
	in  io.WriteCloser
	out *bufio.Reader
}

func openEcho(t *testing.T, c *ssh.Client) *echoSession {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	in, _ := s.StdinPipe()
	out, _ := s.StdoutPipe()
	if err := s.Start("cat"); err != nil {
		t.Fatal(err)
	}
	return &echoSession{in: in, out: bufio.NewReader(out)}
}

func (s *echoSession) roundTrip(t *testing.T, line string) {
	t.Helper()
	if _, err := fmt.Fprintln(s.in, line); err != nil {
		t.Fatalf("write %q: %v", line, err)
	}
	got := make(chan string, 1)
	go func() {
		l, _ := s.out.ReadString('\n')
		got <- strings.TrimSpace(l)
	}()
	select {
	case l := <-got:
		if l != line {
			t.Fatalf("session echoed %q, want %q", l, line)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("session did not echo %q", line)
	}
}

func runCmd(t *testing.T, c *ssh.Client, cmd string) string {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	out, err := s.Output(cmd)
	if err != nil {
		t.Fatalf("run %q: %v", cmd, err)
	}
	return strings.TrimSpace(string(out))
}

// copyExe is the "new build": the test binary at another path, so the
// handover must start the executable it was asked for, not its own.
func copyExe(t *testing.T, dir string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "gateway-new")
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitExit(t *testing.T, cmd *exec.Cmd, within time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("pid %d still running after %s", cmd.Process.Pid, within)
		return nil
	}
}

// A handover keeps the open session, serves new connections from the new
// binary, names it the main process, and the old process exits once its
// last relay ends (I-471).
func TestHandoverKeepsOpenSessions(t *testing.T) {
	e := newE2E(t)
	sock, addr := socket(t)
	old := e.startGateway(mustExe(t), sock)

	c1 := e.dial(addr)
	s1 := openEcho(t, c1)
	s1.roundTrip(t, "before")

	newExe := copyExe(t, e.dir)
	out, err := e.handover(newExe)
	if err != nil {
		t.Fatalf("handover: %v\n%s", err, out)
	}
	pid := parsePID(t, out)
	killAtEnd(t, pid)
	e.notify.want(t, "MAINPID="+strconv.Itoa(pid))
	if pid == old.Process.Pid {
		t.Fatal("handover named the old process")
	}
	if exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); exe != newExe {
		t.Fatalf("new main process runs %q, want %q", exe, newExe)
	}

	// The session opened before the handover carries on.
	s1.roundTrip(t, "after")
	// A new connection and a new session on the old connection both work.
	c2 := e.dial(addr)
	if got := runCmd(t, c2, "hello"); got != "hello" {
		t.Fatalf("new connection ran %q", got)
	}
	if got := runCmd(t, c1, "again"); got != "again" {
		t.Fatalf("old connection's new session ran %q", got)
	}
	// The old process holds c1 only; once it closes the process ends.
	time.Sleep(drainFloor)
	if err := old.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("old gateway ended with a relay open: %v", err)
	}
	_ = c1.Close()
	if err := waitExit(t, old, 10*time.Second); err != nil {
		t.Fatalf("old gateway exited with %v", err)
	}
	// The new one still serves, including a second handover back.
	if got := runCmd(t, c2, "still"); got != "still" {
		t.Fatalf("new gateway ran %q", got)
	}
	out, err = e.handover(mustExe(t))
	if err != nil {
		t.Fatalf("second handover: %v\n%s", err, out)
	}
	killAtEnd(t, parsePID(t, out))
	c3 := e.dial(addr)
	if got := runCmd(t, c3, "third"); got != "third" {
		t.Fatalf("after the second handover ran %q", got)
	}
}

// A new build that does not come up leaves the running gateway serving,
// and the reload fails with the reason.
func TestHandoverFailureKeepsServing(t *testing.T) {
	e := newE2E(t)
	sock, addr := socket(t)
	e.startGateway(mustExe(t), sock)
	c1 := e.dial(addr)
	s1 := openEcho(t, c1)

	broken := filepath.Join(e.dir, "broken")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A "new build" whose serve exits at once.
	reply := rawHandover(t, filepath.Join(e.dir, "control.sock"), broken, e.env)
	if !strings.Contains(reply, "exited before it served") {
		t.Fatalf("handover to a broken build answered %q", reply)
	}
	s1.roundTrip(t, "still here")
	c2 := e.dial(addr)
	if got := runCmd(t, c2, "ok"); got != "ok" {
		t.Fatalf("after a failed handover ran %q", got)
	}
}

// With no gateway running, systemd's socket keeps taking connections; the
// gateway that starts next serves them (I-470).
func TestRestartQueuesConnections(t *testing.T) {
	e := newE2E(t)
	sock, addr := socket(t)
	first := e.startGateway(mustExe(t), sock)
	_ = first.Process.Signal(syscall.SIGTERM)
	if err := waitExit(t, first, 10*time.Second); err != nil {
		t.Fatalf("gateway exited with %v on SIGTERM", err)
	}

	type result struct {
		c   *ssh.Client
		err error
	}
	got := make(chan result, 1)
	go func() {
		c, err := e.tryDial(addr, 20*time.Second)
		got <- result{c, err}
	}()
	time.Sleep(time.Second) // the client waits in the socket's backlog
	e.startGateway(mustExe(t), sock)
	r := <-got
	if r.err != nil {
		t.Fatalf("connection made while no gateway ran: %v", r.err)
	}
	if out := runCmd(t, r.c, "queued"); out != "queued" {
		t.Fatalf("ran %q", out)
	}
}

// killAtEnd ends a gateway the test did not start itself (one a handover
// started), which would otherwise outlive the test holding its output.
func killAtEnd(t *testing.T, pid int) {
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
}

func mustExe(t *testing.T) string {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func parsePID(t *testing.T, out string) int {
	t.Helper()
	_, rest, ok := strings.Cut(out, "pid ")
	if !ok {
		t.Fatalf("no pid in %q", out)
	}
	f := strings.FieldsFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	n, err := strconv.Atoi(f[0])
	if err != nil {
		t.Fatalf("no pid in %q", out)
	}
	return n
}

// rawHandover sends a handover request naming exe, as `gateway handover`
// would from that build.
func rawHandover(t *testing.T, path, exe string, env []string) string {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprintf(conn, `{"exe":%q,"env":[%s]}`+"\n", exe, quoteAll(env)); err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(conn)
	return string(b)
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return strings.Join(q, ",")
}

// notifySink plays systemd's NOTIFY_SOCKET: it records each state line and
// answers BARRIER=1 by closing the descriptor it carries.
type notifySink struct {
	mu   sync.Mutex
	msgs []string
	cond chan struct{}
}

func newNotifySink(t *testing.T, path string) *notifySink {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	n := &notifySink{cond: make(chan struct{}, 1)}
	go func() {
		buf := make([]byte, 4096)
		oob := make([]byte, 256)
		for {
			k, ok, _, _, err := conn.ReadMsgUnix(buf, oob)
			if err != nil {
				return
			}
			if msgs, err := syscall.ParseSocketControlMessage(oob[:ok]); err == nil {
				for _, m := range msgs {
					if fds, err := syscall.ParseUnixRights(&m); err == nil {
						for _, fd := range fds {
							_ = syscall.Close(fd)
						}
					}
				}
			}
			n.mu.Lock()
			n.msgs = append(n.msgs, string(buf[:k]))
			n.mu.Unlock()
			select {
			case n.cond <- struct{}{}:
			default:
			}
		}
	}()
	return n
}

// want waits for a message and consumes it.
func (n *notifySink) want(t *testing.T, msg string) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		n.mu.Lock()
		for i, m := range n.msgs {
			if m == msg {
				n.msgs = append(n.msgs[:i], n.msgs[i+1:]...)
				n.mu.Unlock()
				return
			}
		}
		seen := append([]string{}, n.msgs...)
		n.mu.Unlock()
		select {
		case <-n.cond:
		case <-deadline:
			t.Fatalf("systemd never got %q (got %q)", msg, seen)
		}
	}
}

// startFakeGuest is a guest sshd that takes any key: `cat` echoes stdin,
// any other command prints itself.
func startFakeGuest(t *testing.T, ca *testca.CA, hostPrincipal string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	// The gateway checks the guest's host certificate (ssh-gateway.md).
	privPEM, certLine, err := ca.NewHostKey([]string{hostPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certLine))
	if err != nil {
		t.Fatal(err)
	}
	hk, err := ssh.NewCertSigner(pk.(*ssh.Certificate), signer)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(hk)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveGuestConn(c, cfg)
		}
	}()
	return ln.Addr().String()
}

func serveGuestConn(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for r := range creqs {
				if r.Type != "exec" {
					_ = r.Reply(true, nil)
					continue
				}
				_ = r.Reply(true, nil)
				cmd := string(r.Payload[4:])
				go func() {
					if cmd == "cat" {
						_, _ = io.Copy(ch, ch)
					} else {
						_, _ = fmt.Fprintln(ch, cmd)
					}
					status := make([]byte, 4)
					binary.BigEndian.PutUint32(status, 0)
					_, _ = ch.SendRequest("exit-status", false, status)
					_ = ch.Close()
				}()
			}
		}()
	}
}
