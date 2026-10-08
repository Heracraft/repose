// Package hostmgr is the server side of docs/interfaces/grpc-hostd.md:
// Register turns a join token into a host identity, Rotate reissues the
// host's mTLS certificate, and Session holds one stream per connected
// host, dispatching Hello, Heartbeat, Result, Samples, Event and BuildLog
// to the handlers the rest of the api registers, and sending commands
// down. A host silent for 90 s is marked unreachable by Sweep.
package hostmgr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/curve25519"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/pki"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// ErrHostNotConnected is returned by Send when the host has no stream.
var ErrHostNotConnected = errors.New("host not connected")

// UnreachableAfter is the heartbeat silence that marks a host unreachable.
const UnreachableAfter = 90 * time.Second

// JoinTokenValidity is how long a minted join token can be used.
const JoinTokenValidity = 24 * time.Hour

// Settings keys for the edge WireGuard hub (`repose-admin edge init`) and
// for the Loki every host ships to (`repose-admin edge loki`). They are
// settings rather than environment variables for the same reason: they
// name machines outside this deployment, they change without the api
// changing, and a redeploy of the api to move a log sink is a redeploy
// nobody should need (DECISIONS I-95).
const (
	SettingEdgeWGPubkey   = "edge_wg_pubkey"
	SettingEdgeWGEndpoint = "edge_wg_endpoint"
	SettingLokiURL        = "loki_url"
)

// Handlers are called for incoming messages. Every handler is optional.
type Handlers struct {
	Hello    func(ctx context.Context, hostID uuid.UUID, h *hostdv1.Hello)
	Result   func(ctx context.Context, hostID uuid.UUID, r *hostdv1.Result)
	Samples  func(ctx context.Context, hostID uuid.UUID, s *hostdv1.Samples)
	Event    func(ctx context.Context, hostID uuid.UUID, e *hostdv1.Event) bool
	BuildLog func(ctx context.Context, hostID uuid.UUID, l *hostdv1.BuildLog)
}

// Server is the HostService implementation.
type Server struct {
	hostdv1.UnimplementedHostServiceServer
	pool      *db.Pool
	ca        *pki.CA
	log       *slog.Logger
	m         *metrics.M
	replicaID string
	handlers  Handlers
	// hostCAPub returns the SSH Host CA's public line for RegisterResponse
	// (I-139); nil when no SSH CA is wired (some tests), which sends none.
	hostCAPub func() string

	mu       sync.Mutex
	sessions map[uuid.UUID]*session
}

type session struct {
	stream hostdv1.HostService_SessionServer
	sendMu sync.Mutex
	id     uint64
}

// New builds a server.
func New(pool *db.Pool, ca *pki.CA, replicaID string, m *metrics.M, log *slog.Logger) *Server {
	return &Server{pool: pool, ca: ca, log: log.With("component", "api"), m: m, replicaID: replicaID, sessions: map[uuid.UUID]*session{}}
}

// SetHostCAPub wires the SSH Host CA's public line into Register and
// Rotate responses (before serving).
func (s *Server) SetHostCAPub(f func() string) { s.hostCAPub = f }

func (s *Server) hostCAPubLine() string {
	if s.hostCAPub == nil {
		return ""
	}
	return strings.TrimSpace(s.hostCAPub())
}

// SetHandlers installs the message handlers (before serving).
func (s *Server) SetHandlers(h Handlers) { s.handlers = h }

// TLSConfig is the listener configuration: the server certificate given,
// or one issued by the host CA for names when none is; client
// certificates are verified against the host CA when presented (Register
// has none).
func (s *Server) TLSConfig(serverCert *tls.Certificate, names ...string) (*tls.Config, error) {
	cert := serverCert
	if cert == nil {
		c, err := s.ca.ServerTLS(names...)
		if err != nil {
			return nil, err
		}
		cert = &c
	}
	return &tls.Config{Certificates: []tls.Certificate{*cert}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: s.ca.Pool(), MinVersion: tls.VersionTLS13}, nil
}

// GRPCServer builds the gRPC server with keepalive settings matching
// hostd's client.
func (s *Server) GRPCServer(tlsCfg *tls.Config) *grpc.Server {
	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.MaxRecvMsgSize(64 << 20),
	}
	if tlsCfg != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}
	gs := grpc.NewServer(opts...)
	hostdv1.RegisterHostServiceServer(gs, s)
	return gs
}

// --- join tokens --------------------------------------------------------

// MintJoinToken creates a host row (or reissues for an existing
// unregistered host) and returns the single-use token; only its hash is
// stored.
func MintJoinToken(ctx context.Context, pool *db.Pool, name, provider, sku, region string, reissue bool) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	expires := time.Now().Add(JoinTokenValidity)
	return token, db.InTx(ctx, pool, func(tx db.Tx) error {
		h, err := store.GetHostByName(ctx, tx, name)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if h != nil {
			if h.RegisteredAt != nil && !reissue {
				return fmt.Errorf("host %s is already registered; pass --reissue to mint a new token for a re-imaged host", name)
			}
			_, err := tx.Exec(ctx, "update hosts set join_token_hash = $2, join_token_expires_at = $3, registered_at = null, state = 'registering' where id = $1", h.ID, hash, expires)
			return err
		}
		_, err = tx.Exec(ctx, "insert into hosts (id, name, provider, sku, region, state, join_token_hash, join_token_expires_at) values ($1, $2, $3, $4, $5, 'registering', $6, $7)",
			store.NewID(), name, nilIfEmpty(provider), nilIfEmpty(sku), nilIfEmpty(region), hash, expires)
		return err
	})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// allocate derives a host's guest /22 from 10.64.0.0/12 and its WireGuard
// address from 10.255.0.0/16 (the edge is 10.255.0.1) by sequence number.
func allocate(n int64) (netip.Prefix, netip.Addr, error) {
	if n < 1 || n > 1000 {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("host sequence %d out of range", n)
	}
	base := uint32(10)<<24 | uint32(64)<<16
	cidrInt := base + uint32(n-1)*1024
	cidr := netip.AddrFrom4([4]byte{byte(cidrInt >> 24), byte(cidrInt >> 16), byte(cidrInt >> 8), byte(cidrInt)})
	wgInt := uint32(10)<<24 | uint32(255)<<16 | uint32(n+1)
	wg := netip.AddrFrom4([4]byte{byte(wgInt >> 24), byte(wgInt >> 16), byte(wgInt >> 8), byte(wgInt)})
	return netip.PrefixFrom(cidr, 22), wg, nil
}

// wgKeypair generates a WireGuard key pair, base64 encoded.
func wgKeypair() (priv, pub string, err error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", "", err
	}
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
	p, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(p), nil
}

// --- RPCs ---------------------------------------------------------------

// hostFromPeer is the host id (the verified leaf's CN) and the serial of
// the certificate the peer presented.
func hostFromPeer(ctx context.Context) (uuid.UUID, string, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return uuid.Nil, "", false
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
		return uuid.Nil, "", false
	}
	leaf := ti.State.VerifiedChains[0][0]
	id, err := uuid.Parse(leaf.Subject.CommonName)
	if err != nil || leaf.SerialNumber == nil {
		return uuid.Nil, "", false
	}
	return id, leaf.SerialNumber.String(), true
}

// admit is the check behind Rotate, Session and every heartbeat (DECISIONS
// I-432): a host marked lost or retired is refused, and so is any
// certificate other than the host's current one or the one the last
// rotate replaced (kept until the host connects with the new one).
func admit(state string, certSerial, prevCertSerial *string, serial string) error {
	if state == "lost" || state == "retired" {
		return status.Error(codes.PermissionDenied, "host is "+state)
	}
	if certSerial != nil && *certSerial == serial {
		return nil
	}
	if prevCertSerial != nil && *prevCertSerial == serial {
		return nil
	}
	return status.Error(codes.PermissionDenied, "certificate is not the host's current one")
}

// authHost resolves and admits the calling host.
func (s *Server) authHost(ctx context.Context, rpc string) (*store.Host, string, error) {
	hostID, serial, ok := hostFromPeer(ctx)
	if !ok {
		return nil, "", status.Error(codes.Unauthenticated, "client certificate required")
	}
	h, err := store.GetHost(ctx, s.pool, hostID)
	if err != nil {
		return nil, "", status.Error(codes.PermissionDenied, "unknown host")
	}
	if err := admit(h.State, h.CertSerial, h.PrevCertSerial, serial); err != nil {
		s.log.Warn("host refused", "event", rpc, "host_id", hostID.String(), "state", h.State, "code", status.Code(err).String())
		return nil, "", err
	}
	return h, serial, nil
}

// Register implements the unary registration: one token, one host.
func (s *Server) Register(ctx context.Context, req *hostdv1.RegisterRequest) (*hostdv1.RegisterResponse, error) {
	tok := strings.TrimSpace(req.JoinToken)
	if tok == "" {
		return nil, status.Error(codes.InvalidArgument, "join token required")
	}
	sum := sha256.Sum256([]byte(tok))
	hash := hex.EncodeToString(sum[:])
	var resp *hostdv1.RegisterResponse
	var refused error
	err := db.InTx(ctx, s.pool, func(tx db.Tx) error {
		var h store.Host
		row, err := tx.Query(ctx, "select id, name, registered_at, join_token_expires_at from hosts where join_token_hash = $1 for update", hash)
		if err != nil {
			return err
		}
		found := row.Next()
		if found {
			if err := row.Scan(&h.ID, &h.Name, &h.RegisteredAt, &h.JoinTokenExpiresAt); err != nil {
				row.Close()
				return err
			}
		}
		row.Close()
		if !found {
			refused = status.Error(codes.PermissionDenied, "join token invalid")
			return nil
		}
		if h.RegisteredAt != nil {
			refused = status.Error(codes.PermissionDenied, "join token already used")
			return nil
		}
		if h.JoinTokenExpiresAt != nil && time.Now().After(*h.JoinTokenExpiresAt) {
			refused = status.Error(codes.PermissionDenied, "join token expired")
			return nil
		}
		var seq int64
		if err := tx.QueryRow(ctx, "select nextval('host_seq')").Scan(&seq); err != nil {
			return err
		}
		cidr, wgIP, err := allocate(seq)
		if err != nil {
			return err
		}
		wgPriv, wgPub, err := wgKeypair()
		if err != nil {
			return err
		}
		certPEM, keyPEM, serial, err := s.ca.IssueClient(h.ID.String(), pki.HostCertValidity)
		if err != nil {
			return err
		}
		info := req.Info
		if info == nil {
			info = &hostdv1.HostInfo{}
		}
		if _, err := tx.Exec(ctx, `update hosts set registered_at = now(), join_token_hash = null, guest_cidr = $2, wg_ip = $3, wg_pubkey = $4,
			hostname = $5, sku = coalesce(nullif($6, ''), sku), mem_bytes = $7, vcpus = $8, pool_bytes = $9, nixos_system = $10, ch_version = $11,
			cert_serial = $12, prev_cert_serial = null, cert_expires_at = $13, state = 'registering' where id = $1`,
			h.ID, cidr, wgIP, wgPub, nilIfEmpty(info.Hostname), info.Sku, int64(info.MemBytes), int32(info.Vcpus), int64(info.PoolBytes), nilIfEmpty(info.NixosSystem), nilIfEmpty(info.ChVersion),
			serial, time.Now().Add(pki.HostCertValidity)); err != nil {
			return err
		}
		resp = &hostdv1.RegisterResponse{HostId: h.ID.String(), ClientCert: certPEM, ClientKey: keyPEM, GuestCidr: cidr.String(), HostCaPub: s.hostCAPubLine()}
		edgePub, err := store.Setting(ctx, tx, SettingEdgeWGPubkey)
		if err != nil {
			return err
		}
		edgeEndpoint, err := store.Setting(ctx, tx, SettingEdgeWGEndpoint)
		if err != nil {
			return err
		}
		if edgePub != "" && edgeEndpoint != "" {
			resp.WgPrivateKey = wgPriv
			resp.Edge = &hostdv1.WireguardPeer{Endpoint: edgeEndpoint, PublicKey: edgePub, Address: wgIP.String() + "/16", AllowedIps: []string{"10.255.0.0/16"}}
		}
		// Empty when no Loki has been recorded, which the host renders as
		// an empty LOKI_HOST and Fluent Bit reads as "do not ship".
		if resp.LokiUrl, err = store.Setting(ctx, tx, SettingLokiURL); err != nil {
			return err
		}
		_, err = store.Audit(ctx, tx, "host:"+h.ID.String(), "host_register", h.Name, map[string]any{"guest_cidr": cidr.String()})
		return err
	})
	if err != nil {
		s.log.Error("register failed", "event", "register", "err", err.Error())
		return nil, status.Error(codes.Internal, "registration failed")
	}
	if refused != nil {
		s.log.Warn("register refused", "event", "register", "code", status.Code(refused).String())
		return nil, refused
	}
	s.log.Info("host registered", "event", "register", "host_id", resp.HostId)
	return resp, nil
}

// Rotate reissues the certificate of the authenticated host.
func (s *Server) Rotate(ctx context.Context, req *hostdv1.RegisterRequest) (*hostdv1.RegisterResponse, error) {
	h, presented, err := s.authHost(ctx, "rotate")
	if err != nil {
		return nil, err
	}
	certPEM, keyPEM, serial, err := s.ca.IssueClient(h.ID.String(), pki.HostCertValidity)
	if err != nil {
		return nil, status.Error(codes.Internal, "issue failed")
	}
	// The presented certificate stays valid until the host connects with
	// the new one; the state guard closes the race with mark-lost.
	tag, err := s.pool.Exec(ctx, "update hosts set cert_serial = $2, prev_cert_serial = $4, cert_expires_at = $3 where id = $1 and state not in ('lost', 'retired')", h.ID, serial, time.Now().Add(pki.HostCertValidity), presented)
	if err != nil {
		return nil, status.Error(codes.Internal, "rotate failed")
	}
	if tag.RowsAffected() == 0 {
		return nil, status.Error(codes.PermissionDenied, "host is no longer active")
	}
	s.log.Info("certificate rotated", "event", "rotate", "host_id", h.ID.String())
	resp := &hostdv1.RegisterResponse{HostId: h.ID.String(), ClientCert: certPEM, ClientKey: keyPEM, HostCaPub: s.hostCAPubLine()}
	if h.GuestCIDR != nil {
		resp.GuestCidr = h.GuestCIDR.String()
	}
	// Rotate is the only later chance to tell a host about a Loki that was
	// recorded after it registered, so it carries the current value too.
	if resp.LokiUrl, err = store.Setting(ctx, s.pool, SettingLokiURL); err != nil {
		s.log.Error("rotate: loki setting", "event", "rotate", "err", err.Error())
		return nil, status.Error(codes.Internal, "rotate failed")
	}
	return resp, nil
}

var sessionSeq uint64

// Session is the long-lived stream.
func (s *Server) Session(stream hostdv1.HostService_SessionServer) error {
	ctx := stream.Context()
	h, serial, err := s.authHost(ctx, "stream_connect")
	if err != nil {
		return err
	}
	hostID := h.ID
	if h.PrevCertSerial != nil && h.CertSerial != nil && *h.CertSerial == serial {
		// The rotated certificate reached the host; the old one is done.
		if _, err := s.pool.Exec(ctx, "update hosts set prev_cert_serial = null where id = $1 and cert_serial = $2", hostID, serial); err != nil {
			s.log.Error("clear previous certificate failed", "event", "stream_connect", "host_id", hostID.String(), "err", err.Error())
		}
	}
	log := s.log.With("host_id", hostID.String())
	s.mu.Lock()
	sessionSeq++
	sess := &session{stream: stream, id: sessionSeq}
	s.sessions[hostID] = sess
	s.mu.Unlock()
	s.m.GRPCStreams.Set(float64(s.connectedCount()))
	if _, err := s.pool.Exec(ctx, "insert into host_sessions (host_id, replica_id, since) values ($1, $2, now()) on conflict (host_id) do update set replica_id = excluded.replica_id, since = now()", hostID, s.replicaID); err != nil {
		log.Error("host session record failed", "event", "stream_connect", "err", err.Error())
	}
	log.Info("host connected", "event", "stream_connect")
	defer func() {
		s.mu.Lock()
		if cur := s.sessions[hostID]; cur != nil && cur.id == sess.id {
			delete(s.sessions, hostID)
		}
		s.mu.Unlock()
		s.m.GRPCStreams.Set(float64(s.connectedCount()))
		_, _ = s.pool.Exec(context.Background(), "delete from host_sessions where host_id = $1 and replica_id = $2", hostID, s.replicaID) // best effort at disconnect; Sweep fixes the rest
		log.Info("host disconnected", "event", "stream_disconnect")
	}()
	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch m := msg.Msg.(type) {
		case *hostdv1.HostMessage_Hello:
			s.onHello(ctx, hostID, m.Hello)
		case *hostdv1.HostMessage_Heartbeat:
			// A host marked lost or retired, or whose certificate was
			// superseded, loses an open stream at its next heartbeat.
			if err := s.onHeartbeat(ctx, hostID, serial, m.Heartbeat); err != nil {
				return err
			}
		case *hostdv1.HostMessage_Result:
			if s.handlers.Result != nil {
				s.handlers.Result(ctx, hostID, m.Result)
			}
		case *hostdv1.HostMessage_Samples:
			s.m.SamplesTotal.Inc()
			if s.handlers.Samples != nil {
				s.handlers.Samples(ctx, hostID, m.Samples)
			}
		case *hostdv1.HostMessage_Event:
			ack := true
			if s.handlers.Event != nil {
				ack = s.handlers.Event(ctx, hostID, m.Event)
			}
			if ack {
				_ = s.sendTo(sess, &hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Ack{Ack: &hostdv1.Ack{EventId: m.Event.EventId}}}) // the host re-sends unacked events
			}
		case *hostdv1.HostMessage_Log:
			if s.handlers.BuildLog != nil {
				s.handlers.BuildLog(ctx, hostID, m.Log)
			}
		}
	}
}

func (s *Server) connectedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Server) onHello(ctx context.Context, hostID uuid.UUID, h *hostdv1.Hello) {
	// pool_bytes follows the pool as autoextend grows it; a hostd older
	// than I-586 sends 0, which keeps the registered size.
	if _, err := s.pool.Exec(ctx, "update hosts set state = case when state in ('registering','unreachable') then 'ready' else state end, free_mem_bytes = $2, pool_free_bytes = $3, pool_bytes = case when $4::bigint > 0 then $4::bigint else pool_bytes end, last_heartbeat_at = now() where id = $1",
		hostID, int64(h.FreeMemBytes), int64(h.PoolFreeBytes), int64(h.PoolBytes)); err != nil {
		s.log.Error("hello update failed", "event", "hello", "host_id", hostID.String(), "err", err.Error())
	}
	s.log.Info("hello", "event", "hello", "host_id", hostID.String(), "guests", len(h.Guests))
	if s.handlers.Hello != nil {
		s.handlers.Hello(ctx, hostID, h)
	}
}

func (s *Server) onHeartbeat(ctx context.Context, hostID uuid.UUID, serial string, hb *hostdv1.Heartbeat) error {
	var state string
	var certSerial, prevCertSerial *string
	err := s.pool.QueryRow(ctx, `update hosts set free_mem_bytes = $2, pool_free_bytes = $3, load1 = $4, running_guests = $5, draining = $6,
		pool_bytes = case when $7::bigint > 0 then $7::bigint else pool_bytes end,
		state = case when state = 'unreachable' then 'ready' when $6 and state = 'ready' then 'draining' when not $6 and state = 'draining' then 'ready' else state end,
		last_heartbeat_at = now() where id = $1 returning state, cert_serial, prev_cert_serial`,
		hostID, int64(hb.FreeMemBytes), int64(hb.PoolFreeBytes), hb.Load1, int32(hb.RunningGuests), hb.Draining, int64(hb.PoolBytes)).Scan(&state, &certSerial, &prevCertSerial)
	if err != nil {
		s.log.Error("heartbeat update failed", "event", "heartbeat", "host_id", hostID.String(), "err", err.Error())
		return nil
	}
	if err := admit(state, certSerial, prevCertSerial, serial); err != nil {
		s.log.Warn("host refused", "event", "heartbeat", "host_id", hostID.String(), "state", state, "code", status.Code(err).String())
		return err
	}
	return nil
}

func (s *Server) sendTo(sess *session, m *hostdv1.ApiMessage) error {
	sess.sendMu.Lock()
	defer sess.sendMu.Unlock()
	return sess.stream.Send(m)
}

// Connected reports whether a host has a stream on this replica.
func (s *Server) Connected(hostID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[hostID]
	return ok
}

// Send delivers a command to a connected host. When the host's stream is
// on another replica (host_sessions) the caller retries; with one replica
// that never happens.
func (s *Server) Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error {
	s.mu.Lock()
	sess := s.sessions[hostID]
	s.mu.Unlock()
	if sess == nil {
		return ErrHostNotConnected
	}
	if err := s.sendTo(sess, &hostdv1.ApiMessage{Msg: &hostdv1.ApiMessage_Command{Command: cmd}}); err != nil {
		return fmt.Errorf("%w: %v", ErrHostNotConnected, err)
	}
	s.log.Info("command sent", "event", "command_send", "host_id", hostID.String(), "command_id", cmd.CommandId, "kind", Kind(cmd))
	return nil
}

// Kind names a command.
func Kind(cmd *hostdv1.Command) string {
	switch cmd.Cmd.(type) {
	case *hostdv1.Command_CreateGuest:
		return "CreateGuest"
	case *hostdv1.Command_StartGuest:
		return "StartGuest"
	case *hostdv1.Command_StopGuest:
		return "StopGuest"
	case *hostdv1.Command_DestroyGuest:
		return "DestroyGuest"
	case *hostdv1.Command_ResizeVolume:
		return "ResizeVolume"
	case *hostdv1.Command_Build:
		return "Build"
	case *hostdv1.Command_ApplyConfig:
		return "ApplyConfig"
	case *hostdv1.Command_Snapshot:
		return "Snapshot"
	case *hostdv1.Command_Restore:
		return "Restore"
	case *hostdv1.Command_UpdateSecrets:
		return "UpdateSecrets"
	case *hostdv1.Command_SetPrincipals:
		return "SetPrincipals"
	case *hostdv1.Command_Exec:
		return "Exec"
	case *hostdv1.Command_Drain:
		return "Drain"
	case *hostdv1.Command_AnswerQuestion:
		return "AnswerQuestion"
	}
	return "unknown"
}

// Sweep marks hosts unreachable after UnreachableAfter of silence, flags
// their projects, and refreshes the hosts-by-state gauge. It returns the
// hosts newly marked.
func (s *Server) Sweep(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, "update hosts set state = 'unreachable' where state in ('ready','draining') and (last_heartbeat_at is null or last_heartbeat_at < now() - $1::interval) returning id", UnreachableAfter.String())
	if err != nil {
		return nil, err
	}
	var marked []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		marked = append(marked, id)
	}
	rows.Close()
	for _, id := range marked {
		s.log.Warn("host unreachable", "event", "host_unreachable", "host_id", id.String())
	}
	if _, err := s.pool.Exec(ctx, "update projects set host_unreachable = (select state = 'unreachable' from hosts h where h.id = projects.host_id) where host_id is not null and destroyed_at is null"); err != nil {
		return marked, err
	}
	if _, err := s.pool.Exec(ctx, "delete from host_sessions where host_id in (select id from hosts where state = 'unreachable')"); err != nil {
		return marked, err
	}
	s.refreshGauges(ctx)
	return marked, nil
}

func (s *Server) refreshGauges(ctx context.Context) {
	rows, err := s.pool.Query(ctx, "select state, count(*) from hosts group by state")
	if err != nil {
		return
	}
	counts := map[string]float64{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err == nil {
			counts[st] = float64(n)
		}
	}
	rows.Close()
	for _, st := range []string{"registering", "ready", "draining", "unreachable", "retired", "lost"} {
		s.m.Hosts.WithLabelValues(st).Set(counts[st])
	}
}
