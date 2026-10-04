package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/heracraft/repose/internal/ca/testca"
	fakeapi "github.com/heracraft/repose/internal/fakes/api"
	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// harness is one gateway in front of one fake guest, both against the
// fake api with a real test CA (06-gateway-edge.md §7).
type harness struct {
	t       *testing.T
	ca      *testca.CA
	api     *fakeapi.Fake
	gw      *Gateway
	addr    string
	project fakeapi.Project
	guest   *fakeGuest
	login   string
	offset  atomic.Int64 // seconds added to the clock
	cancel  context.CancelFunc
	metrics *obsmetrics.GatewayMetrics
	logs    *syncBuffer

	mu    sync.Mutex
	dials map[string]string // guest_ip:port -> fake guest address
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harnessOpts struct {
	maxConns        int
	maxConnsPerUser int
	maxPreAuth      int
	maxAuthPerIP    int
	// users, when set, are the fake api's accounts by bearer token; it
	// must include fakeapi.CannedUser, who owns the harness project.
	users       map[string]fakeapi.User
	authTimeout time.Duration
	keepalive   time.Duration
	revRefresh  time.Duration
	dialTimeout time.Duration
}

func genKey(t *testing.T) (ed25519.PrivateKey, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, s
}

func newEd25519(t *testing.T) (ssh.PublicKey, ssh.Signer, error) {
	t.Helper()
	_, s := genKey(t)
	return s.PublicKey(), s, nil
}

func newHarness(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	ca, err := testca.New()
	if err != nil {
		t.Fatal(err)
	}
	api := fakeapi.New(fakeapi.Options{CA: ca, Users: o.users})
	t.Cleanup(api.Close)
	project, err := api.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ca: ca, api: api, project: project, dials: map[string]string{}, logs: &syncBuffer{}}
	h.login = project.Slug + "." + fakeapi.CannedUser.Handle
	h.guest = newFakeGuest(t, ca, project.ID, h.login)
	h.route(project.GuestIP, h.guest.addr())

	client, err := NewClient(api.URL(), nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The gateway's host key, certified by the Host CA for the addresses a
	// client may use.
	_, hostKey := genKey(t)
	hostCert, err := ca.SignHostCert(hostKey.PublicKey(), "host:gateway", []string{"127.0.0.1", "ssh.repose.herakraft.co"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewCertSigner(hostCert, hostKey)
	if err != nil {
		t.Fatal(err)
	}
	_, gwKey := genKey(t)
	h.metrics = obsmetrics.NewGatewayMetrics(obsmetrics.New(obs.ComponentGateway))
	gw, err := New(Config{
		API:               client,
		HostKey:           hostSigner,
		GatewayKey:        gwKey,
		MaxConns:          o.maxConns,
		MaxConnsPerUser:   o.maxConnsPerUser,
		MaxPreAuth:        o.maxPreAuth,
		MaxAuthPerIP:      o.maxAuthPerIP,
		AuthTimeout:       o.authTimeout,
		Keepalive:         o.keepalive,
		RevocationRefresh: o.revRefresh,
		DialTimeout:       o.dialTimeout,
		Log:               slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Metrics:           h.metrics,
		Clock:             func() time.Time { return time.Now().Add(time.Duration(h.offset.Load()) * time.Second) },
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h.mu.Lock()
			target, ok := h.dials[addr]
			h.mu.Unlock()
			if !ok {
				return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}
			}
			return (&net.Dialer{}).DialContext(ctx, network, target)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.gw = gw
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	if err := gw.Prime(ctx); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.addr = ln.Addr().String()
	go func() { _ = gw.Serve(ctx, ln) }()
	go gw.RefreshLoop(ctx)
	t.Cleanup(cancel)
	return h
}

// route maps a guest address the api hands out to a fake guest listener.
func (h *harness) route(guestIP, target string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dials[guestIP+":22"] = target
}

// userCert signs a user certificate for the project ids.
func (h *harness) userCert(t *testing.T, signer ssh.Signer, projectIDs []string, ttl time.Duration) ssh.Signer {
	t.Helper()
	cert, err := h.ca.SignUserCert(signer.PublicKey(), fakeapi.CannedUser.ID+":"+fakeapi.CannedUser.Handle, projectIDs, ttl)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := ssh.NewCertSigner(cert, signer)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// dial connects a Go client; the banner the gateway sent is returned with
// the error.
func (h *harness) dial(login string, auth ssh.Signer) (*ssh.Client, string, error) {
	var banner strings.Builder
	cfg := &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(auth)},
		HostKeyCallback: h.hostKeyCallback(),
		BannerCallback: func(m string) error {
			banner.WriteString(m)
			return nil
		},
		Timeout: 5 * time.Second,
	}
	c, err := ssh.Dial("tcp", h.addr, cfg)
	return c, strings.TrimSpace(banner.String()), err
}

func (h *harness) hostKeyCallback() ssh.HostKeyCallback {
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool { return keysEqual(auth, h.ca.Host.Signer.PublicKey()) },
	}
	return checker.CheckHostKey
}

// run executes a command over a client and returns stdout, stderr and the
// exit status.
func run(t *testing.T, c *ssh.Client, cmd string) (string, string, int) {
	t.Helper()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer func() { _ = sess.Close() }()
	var out, errb strings.Builder
	sess.Stdout, sess.Stderr = &out, &errb
	err = sess.Run(cmd)
	status := 0
	if err != nil {
		var ee *ssh.ExitError
		if !asExit(err, &ee) {
			t.Fatalf("run %q: %v (stderr %q)", cmd, err, errb.String())
		}
		status = ee.ExitStatus()
	}
	return out.String(), errb.String(), status
}

func asExit(err error, target **ssh.ExitError) bool {
	ee, ok := err.(*ssh.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// echoServer is a TCP server that echoes bytes, for forward tests.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}
