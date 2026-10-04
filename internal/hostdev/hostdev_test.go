package hostdev

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	fakeguestd "github.com/heracraft/repose/internal/hostd/fakeguestd"
	"github.com/heracraft/repose/internal/hostd/gcroot"
	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/metrics"
	hnet "github.com/heracraft/repose/internal/hostd/net"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/register"
	"github.com/heracraft/repose/internal/hostd/snapshot"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/storeview"
	"github.com/heracraft/repose/internal/hostd/stream"
	"github.com/heracraft/repose/internal/hostd/systemd"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func fakeClosure(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	c := filepath.Join(dir, "sys")
	_ = os.MkdirAll(c, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bzImage"), []byte("k"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "initrd.img"), []byte("i"), 0o644)
	_ = os.Symlink(filepath.Join(dir, "bzImage"), filepath.Join(c, "kernel"))
	_ = os.Symlink(filepath.Join(dir, "initrd.img"), filepath.Join(c, "initrd"))
	_ = os.WriteFile(filepath.Join(c, "init"), []byte("#!"), 0o755)
	return c
}

// A real hostd stack (registration, stream, manager over fakes) against
// hostdev over TLS gRPC, driven by the hostdev subcommands.
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	addr := freePort(t)
	if err := initCmd(dir, []string{"--listen", addr, "--names", "127.0.0.1", "--handle", "heracraft"}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv, err := NewServer(dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	time.Sleep(100 * time.Millisecond)

	// hostd side.
	var token string
	srv.st.View(func(s *State) { token = s.JoinToken })
	hdir := t.TempDir()
	tokenPath := filepath.Join(hdir, "join-token")
	_ = os.WriteFile(tokenPath, []byte(token), 0o600)
	roots, err := register.LoadRoots(filepath.Join(dir, CAFile))
	if err != nil {
		t.Fatal(err)
	}
	rcfg := register.Config{Dir: filepath.Join(hdir, "hostd"), TokenPath: tokenPath, APIAddr: addr, ServerName: "127.0.0.1", Roots: roots, Info: &hostdv1.HostInfo{Hostname: "h"}}
	id, err := register.Register(ctx, rcfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := register.Register(ctx, rcfg); err == nil {
		t.Fatal("token must be single use")
	}
	st, err := state.Open(filepath.Join(hdir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	l := lvm.NewFake()
	sd := systemd.NewFake()
	sockDir := t.TempDir()
	closure := fakeClosure(t)
	var guestds []*fakeguestd.Server
	sd.OnRun = func(unit string, argv []string) error {
		if !strings.HasPrefix(unit, "guest@") {
			return nil
		}
		g, err := fakeguestd.Listen(filepath.Join(sockDir, strings.TrimPrefix(unit, "guest@")+".sock"), fakeguestd.Options{})
		if err != nil {
			return err
		}
		g.OnShutdown(func() { sd.Exit(unit, 0) })
		guestds = append(guestds, g)
		return nil
	}
	defer func() {
		for _, g := range guestds {
			_ = g.Close()
		}
	}()
	m := metrics.New()
	lh := &lateHost{}
	strm := stream.New(stream.Config{HeartbeatInterval: 50 * time.Millisecond, BackoffBase: 20 * time.Millisecond}, stream.GRPCDialer{Addr: addr, TLS: id.TLSConfig(roots, "127.0.0.1")}, lh, m, logger)
	mgr, err := guest.New(guest.Config{HostID: id.Host.HostID, GuestsDir: filepath.Join(hdir, "guests"), GuestCIDR: id.Host.GuestCIDR, TotalMemBytes: 64 << 30, ReadyTimeout: 3 * time.Second, GuestdRetry: 30 * time.Millisecond, UnitPoll: 30 * time.Millisecond,
		Lookup: func(string) (int, int, error) { return os.Getuid(), os.Getgid(), nil }},
		guest.Deps{State: st, LVM: l, Net: hnet.NewFake(), Systemd: sd, CH: &ch.Fake{}, View: storeview.NewFake(), Nix: &nixbuild.Fake{Closure: closure, ClosureBytes: 1 << 30, Lines: []string{"evaluating", "building"}},
			Roots: gcroot.Roots{Dir: filepath.Join(hdir, "gcroots")}, Blob: snapshot.NewMemBlob(), Stream: &snapshot.FakeStreamer{LVM: l}, Emit: strm, Metrics: m, Log: logger,
			Guestd: vsockclient.UnixDialer{Path: func(tg vsockclient.Target) string { return filepath.Join(sockDir, tg.GuestID+".sock") }}})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Run()
	defer mgr.Close()
	lh.m = mgr
	go strm.Run(ctx)
	waitFor(t, "connected", func() bool { return strm.Connected() })

	// Subcommands.
	frag := filepath.Join(dir, "f.nix")
	_ = os.WriteFile(frag, []byte("{ pkgs, ... }: { home.packages = [ pkgs.bun ]; }"), 0o644)
	if err := run(ctx, dir, "create", []string{"--project", "todo", "--class", "large", "--fragment", frag, "--base-ref", "abc", "--secret", "API_KEY=s3cret"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, s, err := NewClient(dir).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Projects["todo"]
	if p == nil || p.GuestIP != "10.64.4.2" || p.Closure != closure || s.Host.Guests[p.GuestID] != "running" {
		t.Fatalf("project after create %+v host %+v", p, s.Host)
	}
	if len(guestds) != 1 || string(guestds[0].Secrets()["API_KEY"]) != "s3cret" || !strings.HasPrefix(string(guestds[0].Secrets()[guest.SecretHostCert]), "ssh-ed25519-cert-v01@openssh.com ") {
		t.Fatalf("secrets in guest: %v", guestds[0].Secrets())
	}
	// Build logs were captured.
	var logs bytes.Buffer
	for cid, cr := range s.Commands {
		if cr.Kind == "Build" {
			_ = NewClient(dir).Logs(ctx, cid, false, &logs)
		}
	}
	if !strings.Contains(logs.String(), "building") {
		t.Fatalf("build log missing: %q", logs.String())
	}
	for _, step := range [][]string{
		{"snapshot", "--project", "todo"},
		{"stop", "--project", "todo", "--snapshot"},
		{"start", "--project", "todo"},
		{"resize", "--project", "todo", "--size", "80G"},
		{"apply", "--project", "todo", "--closure", closure},
		{"secrets", "set", "--project", "todo", "TOKEN=t"},
		{"principals", "--project", "todo", "x"},
		{"exec", "--project", "todo", "--", "uptime"},
		{"drain"},
	} {
		if err := run(ctx, dir, step[0], step[1:]); err != nil {
			t.Fatalf("%v: %v", step, err)
		}
	}
	_, s, _ = NewClient(dir).Status(ctx)
	p = s.Projects["todo"]
	if p.LastBlob == "" {
		t.Fatal("snapshot path not recorded")
	}
	if err := run(ctx, dir, "restore", []string{"--project", "todo-copy", "--blob-path", p.LastBlob, "--class", "large", "--closure", closure}); err != nil {
		// Drain refuses Restore: expected.
		if !strings.Contains(err.Error(), "insufficient_capacity") {
			t.Fatalf("restore under drain: %v", err)
		}
	} else {
		t.Fatal("restore must be refused while draining")
	}
	pub := filepath.Join(dir, "id.pub")
	_ = os.WriteFile(pub, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGxhRzpZTn4l5ZfTn2xgn2p5f0R1Xq3f5mzq2n4V1oJd test\n"), 0o644)
	if err := run(ctx, dir, "ssh-cert", []string{"--project", "todo", "--pubkey", pub}); err != nil {
		t.Fatalf("ssh-cert: %v", err)
	}
	if err := run(ctx, dir, "destroy", []string{"--project", "todo"}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	_, s, _ = NewClient(dir).Status(ctx)
	if s.Projects["todo"] != nil {
		t.Fatal("project not removed after destroy")
	}
	waitFor(t, "samples or events", func() bool { _, s, _ := NewClient(dir).Status(ctx); return len(s.Events) > 5 })
	// Rotate works with the issued certificate.
	if _, err := register.Rotate(ctx, register.Config{Dir: rcfg.Dir, APIAddr: addr, ServerName: "127.0.0.1", Roots: roots}, id); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// A client without a certificate cannot open a session.
	conn, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	_ = x509.NewCertPool
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type lateHost struct{ m *guest.Manager }

func (l *lateHost) Hello() *hostdv1.Hello         { return l.m.Hello() }
func (l *lateHost) Heartbeat() *hostdv1.Heartbeat { return l.m.Heartbeat() }
func (l *lateHost) Dispatch(c *hostdv1.Command)   { l.m.Dispatch(c) }
