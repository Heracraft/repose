// Package testguest is an in-process SSH server standing in for a repose
// guest, for the CLI's own tests. docs/workstreams/07-cli.md §7 asks for a
// "Docker test fixture test/guest-sshd/ with tmux and git, trusting the
// test CA"; this package covers the same ground (a real sshd endpoint
// running real git and tmux binaries against a scratch home directory)
// without a container, the same trade the gateway-edge workstream made
// ("an in-process SSH server standing in for a guest", 06-gateway-edge.md
// §7, DECISIONS I-49). It authenticates with a plain authorized key rather
// than the CA certificate chain: certificate verification is the
// gateway's contract (docs/interfaces/ssh-gateway.md) and is covered by
// internal/ca/testca's own tests and internal/cli's cert_test.go; what
// this package exercises is what the CLI does once it is talking to a
// guest's shell: git, tar and tmux over a real SSH session.
package testguest

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// Guest is a running fake guest sshd. Call Close when done.
type Guest struct {
	Addr string // "127.0.0.1:port"
	Home string // the fake guest's $HOME

	listener net.Listener
	config   *ssh.ServerConfig
	sockDir  string
	wg       sync.WaitGroup
	conns    atomic.Int64

	// sessions counts the commands running now; mu guards running, their
	// process groups. Close ends and waits for both, so a test's cleanup
	// never removes the fake guest's home while a command it started is
	// still writing there (TestSyncRefusesAnUnrelatedCheckout on a loaded
	// box, 2026-10-15).
	sessions sync.WaitGroup
	mu       sync.Mutex
	running  map[int]struct{}
	closed   bool
}

// New starts a fake guest that accepts authorizedKey and runs every exec
// request as `bash -c <cmd>` with HOME=home (so `~` expansion, git and
// tmux all behave as they do on a real guest) and TMUX_TMPDIR pinned so a
// tmux server started by one exec is found by the next, as it is on a
// real, long-lived guest.
func New(home string, authorizedKey ssh.PublicKey) (*Guest, error) {
	return start(home, func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(authorizedKey.Marshal()) {
			return nil, fmt.Errorf("unrecognised key")
		}
		return nil, nil
	})
}

// start builds the server with its auth callback fixed before the first
// connection is accepted: setting the callback after serve() has started
// is a data race with every handshake (go test -race, CI).
func start(home string, auth func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error)) (*Guest, error) {
	hostKey, err := generateHostKey()
	if err != nil {
		return nil, fmt.Errorf("testguest: host key: %w", err)
	}
	sockDir := filepath.Join(home, ".tmux-sockets")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		return nil, err
	}

	g := &Guest{Home: home, sockDir: sockDir}
	g.config = &ssh.ServerConfig{PublicKeyCallback: auth}
	g.config.AddHostKey(hostKey)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("testguest: listen: %w", err)
	}
	g.listener = l
	g.Addr = l.Addr().String()

	g.wg.Add(1)
	go g.serve()
	return g, nil
}

// NewWithCA starts a fake guest that, like the gateway, accepts only a
// user certificate signed by userCA whose principals include
// principalFor(login), where login is the SSH user name the client sent
// (`<slug>.<handle>`). principalFor returns "" for a login it does not
// know. It is for tests of the laptop's own ssh config (DECISIONS I-281):
// the right User, key and certificate, or no connection.
func NewWithCA(home string, userCA ssh.PublicKey, principalFor func(login string) string) (*Guest, error) {
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(userCA.Marshal())
		},
	}
	return start(home, func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		principal := principalFor(conn.User())
		if principal == "" {
			return nil, fmt.Errorf("unknown login %q", conn.User())
		}
		cert, ok := key.(*ssh.Certificate)
		if !ok {
			return nil, fmt.Errorf("certificate required")
		}
		if !checker.IsUserAuthority(cert.SignatureKey) {
			return nil, fmt.Errorf("certificate not signed by the repose ca")
		}
		if err := checker.CheckCert(principal, cert); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

// Close stops accepting connections and kills any tmux server the fake
// guest started.
func (g *Guest) Close() {
	_ = g.listener.Close()
	g.wg.Wait()
	g.mu.Lock()
	g.closed = true
	for pgid := range g.running {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	g.mu.Unlock()
	done := make(chan struct{})
	go func() { g.sessions.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
	killTmux(g.sockDir)
}

// Connections is how many SSH connections have authenticated so far, for
// tests that prove a client multiplexes its sessions over one.
func (g *Guest) Connections() int { return int(g.conns.Load()) }

func (g *Guest) serve() {
	defer g.wg.Done()
	for {
		nc, err := g.listener.Accept()
		if err != nil {
			return // listener closed: the test is over
		}
		go g.handleConn(nc)
	}
}

func (g *Guest) handleConn(nc net.Conn) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, g.config)
	if err != nil {
		return // auth failure or reset; nothing to report from a background goroutine
	}
	g.conns.Add(1)
	defer func() { _ = sc.Close() }()
	go g.handleGlobal(sc, reqs)
	for ch := range chans {
		if ch.ChannelType() == "direct-tcpip" {
			go g.handleDirectTCPIP(ch)
			continue
		}
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "only session and direct-tcpip channels are supported")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go g.handleSession(channel, requests)
	}
}

// handleGlobal answers the connection's global requests. tcpip-forward is
// what `ssh -R` asks for: listen on the guest's side and hand each
// connection back to the client as a forwarded-tcpip channel, so the
// CLI's reverse tunnel (`repose browser bridge`, DECISIONS I-296) can be
// tested end to end; the listener closes with the connection, as sshd's
// does. Everything else is refused, as sshd refuses what it does not know.
func (g *Guest) handleGlobal(sc *ssh.ServerConn, reqs <-chan *ssh.Request) {
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	for req := range reqs {
		if req.Type != "tcpip-forward" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		var fwd struct {
			Addr string
			Port uint32
		}
		if err := ssh.Unmarshal(req.Payload, &fwd); err != nil {
			_ = req.Reply(false, nil)
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort(fwd.Addr, fmt.Sprint(fwd.Port)))
		if err != nil {
			_ = req.Reply(false, nil)
			continue
		}
		listeners = append(listeners, l)
		port := uint32(l.Addr().(*net.TCPAddr).Port)
		_ = req.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				go g.forwardBack(sc, conn, fwd.Addr, port)
			}
		}()
	}
}

// forwardBack opens the forwarded-tcpip channel for one accepted
// connection and splices the two.
func (g *Guest) forwardBack(sc *ssh.ServerConn, conn net.Conn, addr string, port uint32) {
	defer func() { _ = conn.Close() }()
	origin := conn.RemoteAddr().(*net.TCPAddr)
	payload := ssh.Marshal(struct {
		Addr       string
		Port       uint32
		OriginAddr string
		OriginPort uint32
	}{addr, port, origin.IP.String(), uint32(origin.Port)})
	ch, reqs, err := sc.OpenChannel("forwarded-tcpip", payload)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(ch, conn); _ = ch.CloseWrite(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, ch); done <- struct{}{} }()
	<-done
	_ = ch.Close()
}

// handleDirectTCPIP is what `ssh -L` (and `ssh -O forward -L`) asks for:
// connect to host:port as the guest would and splice the two, so the
// CLI's port forwards can be tested end to end (DECISIONS I-199). The
// "guest" is this machine, so a listener the test starts is the guest's.
func (g *Guest) handleDirectTCPIP(nc ssh.NewChannel) {
	var req struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &req); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, "bad direct-tcpip request")
		return
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(req.Host, fmt.Sprint(req.Port)))
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(ch, conn); _ = ch.CloseWrite(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, ch); done <- struct{}{} }()
	<-done
	_ = ch.Close()
	_ = conn.Close()
}

func (g *Guest) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer func() { _ = channel.Close() }()
	for req := range requests {
		switch req.Type {
		case "exec":
			var payload struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &payload)
			_ = req.Reply(true, nil)
			code := g.run(payload.Command, channel)
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{uint32(code)}))
			return
		case "pty-req", "shell", "env", "window-change":
			_ = req.Reply(true, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (g *Guest) run(command string, channel ssh.Channel) int {
	cmd := exec.Command("bash", "-c", command)
	cmd.Dir = g.Home
	// TMUX (set when the test runner itself runs inside a tmux session)
	// must not leak in: a client that inherits it talks to the outer
	// server instead of the one this fake guest's TMUX_TMPDIR points at.
	// XDG_CONFIG_HOME is the test process's (the "laptop's"); a real
	// guest has none, and git would read the laptop's config through it.
	// Git's auto-maintenance and auto-gc are off: git 2.47 and later
	// start `git maintenance run --auto --detach` after a commit or a
	// fetch, in a session of its own that Close cannot end.
	cmd.Env = append(filterEnv(os.Environ(), "TMUX", "XDG_CONFIG_HOME", "GIT_CONFIG_COUNT"),
		"HOME="+g.Home,
		"TMUX_TMPDIR="+g.sockDir,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=maintenance.auto", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=gc.auto", "GIT_CONFIG_VALUE_1=0",
	)
	// Its own process group, which Close kills if it is still running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Stdin through a pipe the command's exit does not wait on: a client
	// such as scp sends EOF only after the remote command has exited, so
	// exec's own stdin copy (which Wait waits for) would never finish.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_, _ = fmt.Fprintln(channel.Stderr(), err)
		return 1
	}
	go func() { _, _ = io.Copy(stdin, channel); _ = stdin.Close() }()
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return 1
	}
	if err := cmd.Start(); err != nil {
		g.mu.Unlock()
		_, _ = fmt.Fprintln(channel.Stderr(), err)
		return 1
	}
	pgid := cmd.Process.Pid
	if g.running == nil {
		g.running = map[int]struct{}{}
	}
	g.running[pgid] = struct{}{}
	g.sessions.Add(1)
	g.mu.Unlock()
	err = cmd.Wait()
	g.mu.Lock()
	delete(g.running, pgid)
	g.mu.Unlock()
	g.sessions.Done()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		_, _ = fmt.Fprintln(channel.Stderr(), err)
		return 1
	}
	return 0
}

// GenerateClientKey makes an ed25519 keypair for a test to use as the
// authorized identity, writing the private key into dir and returning its
// path and the public key.
func GenerateClientKey(dir string) (privPath string, pub ssh.PublicKey, err error) {
	priv, pubKey, err := generateEd25519()
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, priv, 0o600); err != nil {
		return "", nil, err
	}
	return path, pubKey, nil
}

// filterEnv drops every "KEY=..." entry whose key is in drop.
func filterEnv(env []string, drop ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		skip := false
		for _, d := range drop {
			if key == d {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	return out
}

func killTmux(sockDir string) {
	// tmux places its socket at $TMUX_TMPDIR/tmux-<uid>/default.
	socket := filepath.Join(sockDir, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
	cmd := exec.Command("tmux", "-S", socket, "kill-server")
	_ = cmd.Run() // best effort; nothing to clean up if no server ever started
}
