package hostmgr_test

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/pki"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

type harness struct {
	pool   *db.Pool
	srv    *hostmgr.Server
	ca     *pki.CA
	addr   string
	stop   func()
	mu     sync.Mutex
	result []*hostdv1.Result
	hello  int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := testdb.Open(t)
	ca, err := pki.Generate("test host ca")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{pool: pool, ca: ca}
	h.srv = hostmgr.New(pool, ca, "replica-1", metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	h.srv.SetHandlers(hostmgr.Handlers{
		Hello: func(ctx context.Context, hostID uuid.UUID, hl *hostdv1.Hello) error {
			h.mu.Lock()
			h.hello++
			h.mu.Unlock()
			return nil
		},
		Result: func(ctx context.Context, hostID uuid.UUID, r *hostdv1.Result) {
			h.mu.Lock()
			h.result = append(h.result, r)
			h.mu.Unlock()
		},
	})
	tlsCfg, err := h.srv.TLSConfig(nil, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	gs := h.srv.GRPCServer(tlsCfg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(ln) }()
	h.addr = ln.Addr().String()
	h.stop = gs.Stop
	t.Cleanup(gs.Stop)
	return h
}

func (h *harness) dial(t *testing.T, cert *tls.Certificate) *grpc.ClientConn {
	t.Helper()
	cfg := &tls.Config{RootCAs: h.ca.Pool(), ServerName: "127.0.0.1", MinVersion: tls.VersionTLS13}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	conn, err := grpc.NewClient(h.addr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRegisterSessionSendSweep(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := hostmgr.MintJoinToken(ctx, h.pool, "host-01", "azure", "Standard_D16s_v7", "eastus", false); err != nil {
		t.Fatal(err)
	}
	if _, err := hostmgr.MintJoinToken(ctx, h.pool, "host-01", "", "", "", false); err != nil {
		t.Fatalf("reminting for an unregistered host should work: %v", err)
	}
	token, err := hostmgr.MintJoinToken(ctx, h.pool, "host-01", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	// The Loki every host ships to travels in RegisterResponse
	// (DECISIONS I-95); recorded before this host registers, so Register
	// carries it and Rotate carries it again.
	if err := store.SetSetting(ctx, h.pool, hostmgr.SettingLokiURL, "http://10.255.0.3:3100"); err != nil {
		t.Fatal(err)
	}
	// The Host CA reaches the host in the same message (I-139); before
	// it, /run/repose/host_ca.pub was always empty and operator access was
	// the bootstrap key (security review M-1).
	h.srv.SetHostCAPub(func() string { return "ssh-ed25519 AAAAtest repose-host-ca\n" })
	client := hostdv1.NewHostServiceClient(h.dial(t, nil))
	info := &hostdv1.HostInfo{Hostname: "host-01", Sku: "Standard_D16s_v7", MemBytes: 64 << 30, Vcpus: 16, Cores: 8, PoolBytes: 500 << 30, NixosSystem: "/nix/store/x", ChVersion: "53"}
	if _, err := client.Register(ctx, &hostdv1.RegisterRequest{JoinToken: "wrong", Info: info}); err == nil {
		t.Fatal("wrong token accepted")
	}
	resp, err := client.Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: info})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GuestCidr != "10.64.0.0/22" && resp.GuestCidr != "10.64.4.0/22" && resp.GuestCidr != "10.64.8.0/22" {
		t.Fatalf("guest cidr %s", resp.GuestCidr)
	}
	if resp.LokiUrl != "http://10.255.0.3:3100" {
		t.Fatalf("register loki_url %q: a host with none renders an empty LOKI_HOST and ships nothing", resp.LokiUrl)
	}
	if resp.HostCaPub != "ssh-ed25519 AAAAtest repose-host-ca" {
		t.Fatalf("register host_ca_pub %q: the host would trust no operator certificate", resp.HostCaPub)
	}
	if _, err := client.Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: info}); err == nil {
		t.Fatal("token accepted twice")
	}
	hostID := uuid.MustParse(resp.HostId)
	hostRow, err := store.GetHost(ctx, h.pool, hostID)
	if err != nil || hostRow.RegisteredAt == nil || hostRow.MemBytes != 64<<30 || hostRow.JoinTokenHash != nil {
		t.Fatalf("host row after register: %+v %v", hostRow, err)
	}
	// Threads and cores are both recorded, and they are not the same
	// number: placement counts committed guest vCPUs against the cores
	// (DECISIONS I-735, I-736).
	if hostRow.VCPUs != 16 || hostRow.Cores != 8 {
		t.Fatalf("host row vcpus %d cores %d; want 16 threads on 8 cores", hostRow.VCPUs, hostRow.Cores)
	}
	if _, err := hostmgr.MintJoinToken(ctx, h.pool, "host-01", "", "", "", false); err == nil {
		t.Fatal("minting for a registered host without --reissue should refuse")
	}
	cert, err := tls.X509KeyPair(resp.ClientCert, resp.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	// A stream without a client certificate is refused.
	nocert := hostdv1.NewHostServiceClient(h.dial(t, nil))
	if s, err := nocert.Session(ctx); err == nil {
		if _, err := s.Recv(); err == nil {
			t.Fatal("session without client cert accepted")
		}
	}
	fake := fakehostd.New(fakehostd.Options{HostID: resp.HostId, Heartbeat: 50 * time.Millisecond, SampleInterval: time.Hour})
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn := h.dial(t, &cert)
	go func() { _ = fake.Run(sctx, conn) }()
	waitFor(t, "connected", func() bool { return h.srv.Connected(hostID) })
	waitFor(t, "ready after hello", func() bool {
		hr, _ := store.GetHost(ctx, h.pool, hostID)
		return hr != nil && hr.State == "ready" && hr.LastHeartbeatAt != nil
	})
	// The pool's size follows hello and the heartbeat past the one
	// registered (500 GB here; the fake host's pool is 1 TB), since
	// autoextend grows it and placement reads it (DECISIONS I-586).
	waitFor(t, "pool size from the heartbeat", func() bool {
		hr, _ := store.GetHost(ctx, h.pool, hostID)
		return hr != nil && hr.PoolBytes == 1<<40
	})
	var replica string
	if err := h.pool.QueryRow(ctx, "select replica_id from host_sessions where host_id = $1", hostID).Scan(&replica); err != nil || replica != "replica-1" {
		t.Fatalf("host_sessions: %q %v", replica, err)
	}
	cmdID := store.NewID().String()
	err = h.srv.Send(ctx, hostID, &hostdv1.Command{CommandId: cmdID, Cmd: &hostdv1.Command_CreateGuest{CreateGuest: &hostdv1.CreateGuest{ProjectId: "p", GuestId: store.NewID().String(), Class: "large"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "result", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.result) == 1 })
	if h.result[0].CommandId != cmdID || !h.result[0].Ok {
		t.Fatalf("result %+v", h.result[0])
	}
	// Rotate reissues with the same host id.
	rot, err := hostdv1.NewHostServiceClient(conn).Rotate(ctx, &hostdv1.RegisterRequest{Info: info})
	if err != nil || rot.HostId != resp.HostId || len(rot.ClientCert) == 0 {
		t.Fatalf("rotate %+v %v", rot, err)
	}
	if rot.LokiUrl != "http://10.255.0.3:3100" {
		t.Fatalf("rotate loki_url %q: rotate is how a host registered before a Loki existed learns it", rot.LokiUrl)
	}
	// Silence marks the host unreachable; a heartbeat brings it back.
	cancel()
	waitFor(t, "disconnected", func() bool { return !h.srv.Connected(hostID) })
	if _, err := h.pool.Exec(ctx, "update hosts set last_heartbeat_at = now() - interval '2 minutes' where id = $1", hostID); err != nil {
		t.Fatal(err)
	}
	marked, err := h.srv.Sweep(ctx)
	if err != nil || len(marked) != 1 {
		t.Fatalf("sweep %v %v", marked, err)
	}
	if err := h.srv.Send(ctx, hostID, &hostdv1.Command{CommandId: "x"}); err == nil {
		t.Fatal("send to a disconnected host succeeded")
	}
	fake2 := fakehostd.New(fakehostd.Options{HostID: resp.HostId, Heartbeat: 50 * time.Millisecond, SampleInterval: time.Hour})
	sctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go func() { _ = fake2.Run(sctx2, h.dial(t, &cert)) }()
	waitFor(t, "ready again", func() bool {
		hr, _ := store.GetHost(ctx, h.pool, hostID)
		return hr != nil && hr.State == "ready"
	})
	// The host row turns ready before the hello handler has necessarily
	// returned, so wait for the second call rather than asserting at once
	// (a -race run on CI saw 1 at this point on 2026-09-21).
	waitFor(t, "second hello", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.hello == 2
	})
}

// A Hello the api could not record (the resume rows, DECISIONS I-645) is
// refused: the stream closes, and the host sends Hello again when it
// reconnects, so what it carried is not lost.
func TestHelloRefusedClosesTheStream(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	token, err := hostmgr.MintJoinToken(ctx, h.pool, "host-01", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hostdv1.NewHostServiceClient(h.dial(t, nil)).Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: &hostdv1.HostInfo{Hostname: "host-01"}})
	if err != nil {
		t.Fatal(err)
	}
	hostID := uuid.MustParse(resp.HostId)
	cert, err := tls.X509KeyPair(resp.ClientCert, resp.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	// The api last heard from the host 20 minutes ago, before an outage.
	before := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	if _, err := h.pool.Exec(ctx, "update hosts set last_heartbeat_at = $2 where id = $1", hostID, before); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	refuse, hellos := true, 0
	var lastHeard []*time.Time
	h.srv.SetHandlers(hostmgr.Handlers{Hello: func(ctx context.Context, _ uuid.UUID, _ *hostdv1.Hello) error {
		mu.Lock()
		defer mu.Unlock()
		hellos++
		lastHeard = append(lastHeard, hostmgr.LastHeardBefore(ctx))
		if refuse {
			return errors.New("insert failed")
		}
		return nil
	}})
	fake := fakehostd.New(fakehostd.Options{HostID: resp.HostId, Heartbeat: 50 * time.Millisecond, SampleInterval: time.Hour})
	err = fake.Run(ctx, h.dial(t, &cert))
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("a refused Hello ended the stream with %v, want Unavailable", err)
	}
	mu.Lock()
	refuse = false
	mu.Unlock()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = fake.Run(sctx, h.dial(t, &cert)) }()
	waitFor(t, "connected after the second Hello", func() bool { return h.srv.Connected(hostID) })
	mu.Lock()
	defer mu.Unlock()
	if hellos != 2 {
		t.Fatalf("%d Hellos, want 2", hellos)
	}
	// The handler sees when the api last heard from the host before the
	// outage, for both Hellos: the refused one does not stand in for it,
	// or a stop the owner asked for during the outage would not count.
	for i, lh := range lastHeard {
		if lh == nil || !lh.Equal(before) {
			t.Fatalf("last heard before Hello %d: %v, want %v", i+1, lh, before)
		}
	}
}

// A hostd older than I-735 reports threads and no cores, and the api
// records half the threads: every shape repose can run is two threads to
// a core (DESIGN.md §4). Register is single use, so a rotate is such a
// host's only way to report cores once it is upgraded, and it does
// (DECISIONS I-735).
func TestRegisterAndRotateRecordCoresAndTheThreadBridge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	token, err := hostmgr.MintJoinToken(ctx, h.pool, "host-old", "azure", "Standard_D16s_v7", "eastus", false)
	if err != nil {
		t.Fatal(err)
	}
	client := hostdv1.NewHostServiceClient(h.dial(t, nil))
	old := &hostdv1.HostInfo{Hostname: "host-old", MemBytes: 64 << 30, Vcpus: 16, PoolBytes: 500 << 30}
	resp, err := client.Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: old})
	if err != nil {
		t.Fatal(err)
	}
	hostID := uuid.MustParse(resp.HostId)
	row, err := store.GetHost(ctx, h.pool, hostID)
	if err != nil || row.Cores != 8 {
		t.Fatalf("cores %d from a hostd that reported 16 threads and no cores; want 8: %v", row.Cores, err)
	}
	// An upgraded hostd rotates and reports the real figure; a host that
	// reports nothing keeps what is recorded.
	cert, err := tls.X509KeyPair(resp.ClientCert, resp.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	rot := hostdv1.NewHostServiceClient(h.dial(t, &cert))
	if _, err := rot.Rotate(ctx, &hostdv1.RegisterRequest{Info: &hostdv1.HostInfo{Hostname: "host-old", Vcpus: 16, Cores: 8}}); err != nil {
		t.Fatal(err)
	}
	if row, err = store.GetHost(ctx, h.pool, hostID); err != nil || row.Cores != 8 || row.VCPUs != 16 {
		t.Fatalf("after a rotate that reported 8 cores: vcpus %d cores %d %v", row.VCPUs, row.Cores, err)
	}
}
