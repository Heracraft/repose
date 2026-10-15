package gateway

import (
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/heracraft/repose/internal/ca/testca"
)

// fakeGuest is an in-process sshd standing in for a guest: it trusts the
// User CA for one principal (the project id, like AuthorizedPrincipalsFile),
// presents a host certificate for <slug>.<handle>, and implements enough
// of session, direct-tcpip, tcpip-forward and agent forwarding to assert
// what the relay delivered.
type fakeGuest struct {
	t         *testing.T
	ln        net.Listener
	projectID string
	config    *ssh.ServerConfig

	mu            sync.Mutex
	ptyReqs       int
	windowChanges int
	envs          map[string]string
	agentReq      bool
	lastCols      uint32
	lastRows      uint32
	conns         int
	listeners     map[string]net.Listener
}

func newFakeGuest(t *testing.T, ca *testca.CA, projectID, hostPrincipal string) *fakeGuest {
	t.Helper()
	g := &fakeGuest{t: t, projectID: projectID, envs: map[string]string{}, listeners: map[string]net.Listener{}}
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool { return keysEqual(auth, ca.User.Signer.PublicKey()) },
	}
	g.config = &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if conn.User() != "dev" {
				return nil, fmt.Errorf("AllowUsers dev")
			}
			cert, ok := key.(*ssh.Certificate)
			if !ok {
				return nil, fmt.Errorf("certificate required")
			}
			if err := checker.CheckCert(projectID, cert); err != nil {
				return nil, err
			}
			return &ssh.Permissions{Extensions: map[string]string{"key_id": cert.KeyId}}, nil
		},
	}
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
	certSigner, err := ssh.NewCertSigner(pk.(*ssh.Certificate), signer)
	if err != nil {
		t.Fatal(err)
	}
	g.config.AddHostKey(certSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g.ln = ln
	go g.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return g
}

// plainHostKey makes a guest that presents an uncertified host key (the
// throwaway key of DECISIONS I-35).
func (g *fakeGuest) usePlainHostKey(t *testing.T) {
	_, priv, err := newEd25519(t)
	if err != nil {
		t.Fatal(err)
	}
	cfg := *g.config
	cfg2 := ssh.ServerConfig{PublicKeyCallback: cfg.PublicKeyCallback}
	cfg2.AddHostKey(priv)
	g.mu.Lock()
	g.config = &cfg2
	g.mu.Unlock()
}

func (g *fakeGuest) addr() string { return g.ln.Addr().String() }

func (g *fakeGuest) serve() {
	for {
		c, err := g.ln.Accept()
		if err != nil {
			return
		}
		go g.handle(c)
	}
}

func (g *fakeGuest) handle(c net.Conn) {
	g.mu.Lock()
	cfg := g.config
	g.conns++
	g.mu.Unlock()
	sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	defer func() { _ = sconn.Close() }()
	go func() {
		for req := range reqs {
			g.globalRequest(sconn, req)
		}
	}()
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			ch, creqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go g.session(sconn, ch, creqs)
		case "direct-tcpip":
			var d struct {
				Host     string
				Port     uint32
				OrigHost string
				OrigPort uint32
			}
			if err := ssh.Unmarshal(nc.ExtraData(), &d); err != nil {
				_ = nc.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
				continue
			}
			target, err := net.Dial("tcp", net.JoinHostPort(d.Host, strconv.Itoa(int(d.Port))))
			if err != nil {
				_ = nc.Reject(ssh.ConnectionFailed, "connect failed: "+err.Error())
				continue
			}
			ch, creqs, err := nc.Accept()
			if err != nil {
				_ = target.Close()
				continue
			}
			go ssh.DiscardRequests(creqs)
			go pipeConn(ch, target)
		default:
			_ = nc.Reject(ssh.UnknownChannelType, "unknown channel type")
		}
	}
}

func pipeConn(ch ssh.Channel, c net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(ch, c); _ = ch.CloseWrite() }()
	go func() { defer wg.Done(); _, _ = io.Copy(c, ch); _ = c.(*net.TCPConn).CloseWrite() }()
	wg.Wait()
	_ = ch.Close()
	_ = c.Close()
}

func (g *fakeGuest) globalRequest(sconn *ssh.ServerConn, req *ssh.Request) {
	switch req.Type {
	case "tcpip-forward":
		var f struct {
			Addr string
			Port uint32
		}
		if err := ssh.Unmarshal(req.Payload, &f); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(f.Port))))
		if err != nil {
			_ = req.Reply(false, nil)
			return
		}
		port := uint32(ln.Addr().(*net.TCPAddr).Port)
		g.mu.Lock()
		g.listeners[f.Addr+":"+strconv.Itoa(int(f.Port))] = ln
		g.mu.Unlock()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				ra := c.RemoteAddr().(*net.TCPAddr)
				payload := ssh.Marshal(struct {
					Addr     string
					Port     uint32
					OrigAddr string
					OrigPort uint32
				}{f.Addr, port, ra.IP.String(), uint32(ra.Port)})
				ch, creqs, err := sconn.OpenChannel("forwarded-tcpip", payload)
				if err != nil {
					_ = c.Close()
					continue
				}
				go ssh.DiscardRequests(creqs)
				go pipeConn(ch, c)
			}
		}()
		_ = req.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
	case "cancel-tcpip-forward":
		var f struct {
			Addr string
			Port uint32
		}
		_ = ssh.Unmarshal(req.Payload, &f)
		g.mu.Lock()
		if ln, ok := g.listeners[f.Addr+":"+strconv.Itoa(int(f.Port))]; ok {
			_ = ln.Close()
		}
		g.mu.Unlock()
		_ = req.Reply(true, nil)
	case "keepalive@openssh.com":
		_ = req.Reply(true, nil)
	default:
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

func (g *fakeGuest) session(sconn *ssh.ServerConn, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	envs := map[string]string{}
	exit := func(status uint32) {
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
	}
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p struct {
				Term          string
				Cols, Rows    uint32
				Width, Height uint32
				Modes         string
			}
			_ = ssh.Unmarshal(req.Payload, &p)
			g.mu.Lock()
			g.ptyReqs++
			g.lastCols, g.lastRows = p.Cols, p.Rows
			g.mu.Unlock()
			_ = req.Reply(true, nil)
		case "window-change":
			var w struct{ Cols, Rows, Width, Height uint32 }
			_ = ssh.Unmarshal(req.Payload, &w)
			g.mu.Lock()
			g.windowChanges++
			g.lastCols, g.lastRows = w.Cols, w.Rows
			g.mu.Unlock()
		case "env":
			var e envRequest
			_ = ssh.Unmarshal(req.Payload, &e)
			envs[e.Name] = e.Value
			g.mu.Lock()
			g.envs[e.Name] = e.Value
			g.mu.Unlock()
			_ = req.Reply(true, nil)
		case "auth-agent-req@openssh.com":
			g.mu.Lock()
			g.agentReq = true
			g.mu.Unlock()
			_ = req.Reply(true, nil)
		case "shell":
			_ = req.Reply(true, nil)
			_, _ = fmt.Fprintf(ch, "shell ready\r\n")
			go func() {
				sc := make([]byte, 1)
				var line []byte
				for {
					n, err := ch.Read(sc)
					if err != nil {
						return
					}
					if n == 0 {
						continue
					}
					if sc[0] == '\r' || sc[0] == '\n' {
						cmd := strings.TrimSpace(string(line))
						line = line[:0]
						if cmd == "exit" {
							exit(0)
							_ = ch.Close()
							return
						}
						_, _ = fmt.Fprintf(ch, "you said: %s\r\n", cmd)
						continue
					}
					line = append(line, sc[0])
				}
			}()
		case "exec":
			var e struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &e)
			if e.Command == "eof-order" {
				exit(g.eofOrder(ch, req))
				return
			}
			_ = req.Reply(true, nil)
			status := g.exec(sconn, ch, e.Command, envs)
			exit(status)
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// eofOrder is OpenSSH's sshd on an exec whose stdin is empty: the
// command's stdin exists only once sshd has accepted the exec, and an
// EOF that arrives before it closes nothing, so `cat` waits for ever
// (I-740). It takes its time to accept, as a guest just after a boot
// does, says "early-eof" (exit 1) when the EOF was already there, and
// "eof-after-exec" when it came after the accept.
func (g *fakeGuest) eofOrder(ch ssh.Channel, req *ssh.Request) uint32 {
	time.Sleep(300 * time.Millisecond)
	eof := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, ch)
		close(eof)
	}()
	select {
	case <-eof:
		_ = req.Reply(true, nil)
		_, _ = fmt.Fprintf(ch, "early-eof\n")
		return 1
	case <-time.After(100 * time.Millisecond):
	}
	_ = req.Reply(true, nil)
	select {
	case <-eof:
		_, _ = fmt.Fprintf(ch, "eof-after-exec\n")
		return 0
	case <-time.After(5 * time.Second):
		_, _ = fmt.Fprintf(ch, "no-eof\n")
		return 2
	}
}

// exec emulates the handful of commands the tests run.
func (g *fakeGuest) exec(sconn *ssh.ServerConn, ch ssh.Channel, command string, envs map[string]string) uint32 {
	name, rest, _ := strings.Cut(command, " ")
	switch name {
	case "echo":
		_, _ = fmt.Fprintf(ch, "%s\n", rest)
		return 0
	case "exit":
		n, _ := strconv.Atoi(rest)
		return uint32(n)
	case "env":
		keys := make([]string, 0, len(envs))
		for k := range envs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = fmt.Fprintf(ch, "%s=%s\n", k, envs[k])
		}
		return 0
	case "cat":
		_, _ = io.Copy(ch, ch)
		return 0
	case "stderr":
		_, _ = fmt.Fprintf(ch.Stderr(), "%s\n", rest)
		return 0
	case "sshd-exit":
		// OpenSSH's order when a program ends: its output, EOF once the
		// pipe drains, and only then the exit status when the child is
		// reaped, a moment later (the caller sends it on return). A
		// channel keepalive@openssh.com (sshd's ClientAliveInterval) may
		// sit in between; it wants a reply.
		d, _ := time.ParseDuration(rest)
		time.Sleep(d)
		_, _ = fmt.Fprintf(ch, "alive\n")
		_, _ = ch.SendRequest("keepalive@openssh.com", true, nil)
		_ = ch.CloseWrite()
		time.Sleep(50 * time.Millisecond)
		return 7
	case "pty":
		g.mu.Lock()
		defer g.mu.Unlock()
		_, _ = fmt.Fprintf(ch, "pty=%d cols=%d rows=%d winch=%d\n", g.ptyReqs, g.lastCols, g.lastRows, g.windowChanges)
		return 0
	case "big":
		n, _ := strconv.Atoi(rest)
		buf := make([]byte, 32*1024)
		for i := range buf {
			buf[i] = 'y'
		}
		for n > 0 {
			w := len(buf)
			if n < w {
				w = n
			}
			if _, err := ch.Write(buf[:w]); err != nil {
				return 1
			}
			n -= w
		}
		return 0
	case "agent":
		ach, areqs, err := sconn.OpenChannel("auth-agent@openssh.com", nil)
		if err != nil {
			_, _ = fmt.Fprintf(ch.Stderr(), "agent channel: %v\n", err)
			return 1
		}
		go ssh.DiscardRequests(areqs)
		// A plain io.ReadWriter, not the channel itself: given a Closer,
		// agent.NewClient (x/crypto v0.55) starts a pipelined reader that
		// keeps reading the channel after List returns, and x/crypto's
		// channel EOF wakes only one of two readers, which would leave the
		// io.Copy below asleep. sshd has one reader; so does this fake.
		keys, err := agent.NewClient(struct{ io.ReadWriter }{ach}).List()
		// Tear the agent channel down the way sshd does once the program's
		// agent socket closes: EOF towards the peer, then wait for the
		// peer's EOF before the full close. A gateway that does not relay
		// the EOF to the client leaves this waiting for ever (I-110).
		_ = ach.CloseWrite()
		_, _ = io.Copy(io.Discard, ach)
		_ = ach.Close()
		if err != nil {
			_, _ = fmt.Fprintf(ch.Stderr(), "agent list: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(ch, "keys=%d\n", len(keys))
		return 0
	}
	_, _ = fmt.Fprintf(ch.Stderr(), "unknown command %q\n", command)
	return 127
}

func (g *fakeGuest) snapshot() (envs map[string]string, agentReq bool, ptys, winch int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	envs = map[string]string{}
	for k, v := range g.envs {
		envs[k] = v
	}
	return envs, g.agentReq, g.ptyReqs, g.windowChanges
}
