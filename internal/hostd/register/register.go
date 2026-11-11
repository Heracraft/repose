// Package register turns a one-shot join token into the host identity:
// the mTLS certificate and key, host.json with the guest range and
// WireGuard material, and the 30-day rotation five days before expiry.
package register

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// ExitTokenUsed is the exit status for a consumed or invalid join token;
// the unit's RestartPreventExitStatus stops the retry loop.
const ExitTokenUsed = 3

// RotateBefore is how long before expiry the certificate is rotated.
const RotateBefore = 5 * 24 * time.Hour

// ErrTokenUsed is returned when the api rejects the token as used or
// invalid.
var ErrTokenUsed = errors.New("register: join token already used")

// ErrNoToken is returned when neither an identity nor a token exists.
var ErrNoToken = errors.New("register: waiting for join token")

// WG is the WireGuard half of host.json.
type WG struct {
	PrivateKey   string   `json:"private_key"`
	Address      string   `json:"address"`
	EdgePubkey   string   `json:"edge_pubkey"`
	EdgeEndpoint string   `json:"edge_endpoint"`
	AllowedIPs   []string `json:"allowed_ips,omitempty"`
}

// HostJSON is /var/lib/repose/hostd/host.json, the shape workstream 01
// reads at boot.
type HostJSON struct {
	HostID    string `json:"host_id"`
	GuestCIDR string `json:"guest_cidr"`
	WG        WG     `json:"wg"`
	HostCAPub string `json:"host_ca_pub,omitempty"`
	LokiURL   string `json:"loki_url,omitempty"`
}

// Identity is the loaded host identity.
type Identity struct {
	Dir      string
	Host     HostJSON
	CertPEM  []byte
	KeyPEM   []byte
	Cert     tls.Certificate
	NotAfter time.Time
}

// Files in the state directory.
const (
	CertFile = "cert.pem"
	KeyFile  = "key.pem"
	HostFile = "host.json"
)

// Load reads an existing identity; os.ErrNotExist when there is none.
func Load(dir string) (*Identity, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, CertFile))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, KeyFile))
	if err != nil {
		return nil, err
	}
	hb, err := os.ReadFile(filepath.Join(dir, HostFile))
	if err != nil {
		return nil, err
	}
	id := &Identity{Dir: dir, CertPEM: certPEM, KeyPEM: keyPEM}
	if err := json.Unmarshal(hb, &id.Host); err != nil {
		return nil, fmt.Errorf("host.json: %w", err)
	}
	if err := id.parse(); err != nil {
		return nil, err
	}
	return id, nil
}

func (id *Identity) parse() error {
	cert, err := tls.X509KeyPair(id.CertPEM, id.KeyPEM)
	if err != nil {
		return fmt.Errorf("host certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("host certificate: %w", err)
	}
	id.Cert, id.NotAfter = cert, leaf.NotAfter
	return nil
}

// TLSConfig is the client configuration for the api: the host
// certificate plus the trusted roots (system roots, or the PEM given).
func (id *Identity) TLSConfig(roots *x509.CertPool, serverName string) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{id.Cert}, RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS13}
}

// ShouldRotate reports whether now is within RotateBefore of expiry.
func (id *Identity) ShouldRotate(now time.Time) bool {
	return now.Add(RotateBefore).After(id.NotAfter)
}

// Config is what registration needs.
type Config struct {
	Dir        string // /var/lib/repose/hostd
	TokenPath  string // /run/repose/join-token
	APIAddr    string // api.repose.herakraft.co:443 (internal, I-1156)
	ServerName string
	Roots      *x509.CertPool // nil: system roots
	Info       *hostdv1.HostInfo
}

// Register performs first registration: token in, identity out, token
// deleted. Returns ErrTokenUsed (exit 3) when the api refuses the token
// and ErrNoToken when there is nothing to register with.
func Register(ctx context.Context, cfg Config) (*Identity, error) {
	tok, err := os.ReadFile(cfg.TokenPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoToken
	}
	if err != nil {
		return nil, fmt.Errorf("read join token: %w", err)
	}
	token := strings.TrimSpace(string(tok))
	if token == "" {
		return nil, ErrNoToken
	}
	creds := credentials.NewTLS(&tls.Config{RootCAs: cfg.Roots, ServerName: cfg.ServerName, MinVersion: tls.VersionTLS13})
	conn, err := grpc.NewClient(cfg.APIAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial api: %w", err)
	}
	defer func() { _ = conn.Close() }() // one call; the connection is not reused
	resp, err := hostdv1.NewHostServiceClient(conn).Register(ctx, &hostdv1.RegisterRequest{JoinToken: token, Info: cfg.Info})
	if err != nil {
		if st, ok := status.FromError(err); ok && (st.Code() == codes.PermissionDenied || st.Code() == codes.InvalidArgument || st.Code() == codes.Unauthenticated) {
			return nil, fmt.Errorf("%w: %s", ErrTokenUsed, st.Message())
		}
		return nil, fmt.Errorf("register: %w", err)
	}
	id, err := write(cfg.Dir, resp, nil)
	if err != nil {
		return nil, err
	}
	// host.json is the only file hostd writes for the network: the host
	// renders /run/repose/wg0.conf from it and the unit that runs hostd
	// register restarts repose-host-net (host-conventions.md, DECISIONS
	// I-18, I-137). A second wg0.conf under the state directory was the
	// review's M-5.
	if err := os.Remove(cfg.TokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("delete join token: %w", err)
	}
	return id, nil
}

// Rotate asks for a fresh certificate with the current one and swaps the
// files atomically.
func Rotate(ctx context.Context, cfg Config, cur *Identity) (*Identity, error) {
	creds := credentials.NewTLS(cur.TLSConfig(cfg.Roots, cfg.ServerName))
	conn, err := grpc.NewClient(cfg.APIAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial api: %w", err)
	}
	defer func() { _ = conn.Close() }() // one call; the connection is not reused
	resp, err := hostdv1.NewHostServiceClient(conn).Rotate(ctx, &hostdv1.RegisterRequest{Info: cfg.Info})
	if err != nil {
		return nil, fmt.Errorf("rotate: %w", err)
	}
	if resp.HostId == "" {
		resp.HostId = cur.Host.HostID
	}
	if resp.GuestCidr == "" {
		resp.GuestCidr = cur.Host.GuestCIDR
	}
	return write(cfg.Dir, resp, &cur.Host)
}

func write(dir string, resp *hostdv1.RegisterResponse, prev *HostJSON) (*Identity, error) {
	if len(resp.ClientCert) == 0 || len(resp.ClientKey) == 0 || resp.HostId == "" {
		return nil, errors.New("register: api returned no certificate")
	}
	if p, _ := pem.Decode(resp.ClientCert); p == nil {
		return nil, errors.New("register: certificate is not PEM")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	id := &Identity{Dir: dir, CertPEM: resp.ClientCert, KeyPEM: resp.ClientKey}
	id.Host = HostJSON{HostID: resp.HostId, GuestCIDR: resp.GuestCidr}
	if prev != nil {
		id.Host = *prev
		id.Host.HostID, id.Host.GuestCIDR = resp.HostId, resp.GuestCidr
	}
	// An empty loki_url from an api that predates the field leaves
	// whatever the previous host.json had, so a rotate against an older
	// api does not silently stop a host shipping logs.
	if resp.LokiUrl != "" {
		id.Host.LokiURL = resp.LokiUrl
	}
	// The same rule for the Host CA (I-139): an api that predates the field
	// sends nothing and the host keeps trusting what it trusted.
	if resp.HostCaPub != "" {
		id.Host.HostCAPub = strings.TrimSpace(resp.HostCaPub)
	}
	if resp.Edge != nil || resp.WgPrivateKey != "" {
		id.Host.WG = WG{PrivateKey: resp.WgPrivateKey}
		if resp.Edge != nil {
			id.Host.WG.Address = resp.Edge.Address
			id.Host.WG.EdgePubkey = resp.Edge.PublicKey
			id.Host.WG.EdgeEndpoint = resp.Edge.Endpoint
			id.Host.WG.AllowedIPs = resp.Edge.AllowedIps
		}
	}
	if err := id.parse(); err != nil {
		return nil, err
	}
	hb, err := json.MarshalIndent(id.Host, "", "  ")
	if err != nil {
		return nil, err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{KeyFile, id.KeyPEM}, {CertFile, id.CertPEM}, {HostFile, hb}} {
		if err := atomicWrite(filepath.Join(dir, f.name), f.data, 0o600); err != nil {
			return nil, err
		}
	}
	return id, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WGConf renders a wg-quick config from the registration material. hostd
// no longer writes one (I-137); it stays for tests and operators comparing
// what the host rendered with what registration returned.
func WGConf(w WG) string {
	allowed := strings.Join(w.AllowedIPs, ", ")
	if allowed == "" {
		allowed = "10.255.0.0/16"
	}
	return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s\n\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = %s\nPersistentKeepalive = 25\n",
		w.PrivateKey, w.Address, w.EdgePubkey, w.EdgeEndpoint, allowed)
}

// LoadRoots reads a PEM bundle into a pool; nil path means system roots.
func LoadRoots(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("%s: no certificates", path)
	}
	return pool, nil
}
