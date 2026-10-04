// Package pki is the X.509 authority behind host mTLS
// (docs/interfaces/grpc-hostd.md): it issues the client certificate a host
// receives at Register (CN = host id), the gateway's client certificate
// for /internal/*, and can issue a server certificate for the gRPC
// listener when the deployment has no other one.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"
)

// HostCertValidity is how long a host's client certificate lasts; hostd
// rotates five days before expiry.
const HostCertValidity = 30 * 24 * time.Hour

// GatewayClientName is the CN of the gateway's client certificate, the
// one identity the /internal listener admits (DECISIONS I-431). Host
// certificates carry a host id there and are refused.
const GatewayClientName = "gateway"

// CA is the authority.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
}

// Generate creates a ten-year CA.
func Generate(cn string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

// Load reads a CA from PEM.
func Load(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("pki: certificate or key is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: certificate: %w", err)
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: key: %w", err)
	}
	return &CA{Cert: cert, Key: key, CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cb.Bytes})}, nil
}

// KeyPEM encodes the CA private key.
func (c *CA) KeyPEM() ([]byte, error) {
	b, err := x509.MarshalECPrivateKey(c.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b}), nil
}

// Pool holds only this CA.
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.Cert)
	return p
}

func (c *CA) issue(tmpl *x509.Certificate) (certPEM, keyPEM []byte, serial string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, nil, "", err
	}
	tmpl.SerialNumber = sn
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		return nil, nil, "", err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), sn.String(), nil
}

// SignCSR signs a certificate signing request whose private key stays where
// it was generated (the edge's gateway key, DECISIONS I-92): the CSR's
// public key gets a client certificate with CN = name, or a server
// certificate for the names (an IP becomes an IP SAN) when server is set.
// The CSR's own subject and extensions are ignored; only its key is used.
func (c *CA) SignCSR(csrPEM []byte, server bool, validity time.Duration, names ...string) (certPEM []byte, serial string, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, "", errors.New("pki: not a PEM certificate request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("pki: parse request: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("pki: request signature: %w", err)
	}
	if len(names) == 0 || names[0] == "" {
		return nil, "", errors.New("pki: a name is required")
	}
	tmpl := &x509.Certificate{
		Subject:   pkix.Name{CommonName: names[0]},
		NotBefore: time.Now().Add(-time.Minute),
		NotAfter:  time.Now().Add(validity),
		KeyUsage:  x509.KeyUsageDigitalSignature,
	}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		for _, n := range names {
			if ip := net.ParseIP(n); ip != nil {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			} else {
				tmpl.DNSNames = append(tmpl.DNSNames, n)
			}
		}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, "", err
	}
	tmpl.SerialNumber = sn
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, csr.PublicKey, c.Key)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), sn.String(), nil
}

// IssueClient issues a client certificate with CN = name.
func (c *CA) IssueClient(name string, validity time.Duration) (certPEM, keyPEM []byte, serial string, err error) {
	return c.issue(&x509.Certificate{
		Subject:     pkix.Name{CommonName: name},
		NotBefore:   time.Now().Add(-time.Minute),
		NotAfter:    time.Now().Add(validity),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
}

// IssueServer issues a server certificate for the names or IPs.
func (c *CA) IssueServer(validity time.Duration, names ...string) (certPEM, keyPEM []byte, err error) {
	if len(names) == 0 {
		return nil, nil, errors.New("pki: server certificate needs a name")
	}
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: names[0]},
		NotBefore:   time.Now().Add(-time.Minute),
		NotAfter:    time.Now().Add(validity),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	certPEM, keyPEM, _, err = c.issue(tmpl)
	return certPEM, keyPEM, err
}

// ServerTLS issues a server certificate as a tls.Certificate.
func (c *CA) ServerTLS(names ...string) (tls.Certificate, error) {
	cert, key, err := c.IssueServer(5*365*24*time.Hour, names...)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(cert, key)
}

// ClientTLS issues a client certificate as a tls.Certificate (tests, the
// gateway).
func (c *CA) ClientTLS(name string) (tls.Certificate, error) {
	cert, key, _, err := c.IssueClient(name, HostCertValidity)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(cert, key)
}

// RequireClientName returns a copy of cfg that demands a client
// certificate verified against cfg.ClientCAs whose leaf CN is name; any
// other identity from the same CA fails the handshake.
func RequireClientName(cfg *tls.Config, name string) *tls.Config {
	out := cfg.Clone()
	out.ClientAuth = tls.RequireAndVerifyClientCert
	out.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
			return errors.New("pki: no verified client certificate")
		}
		if cn := cs.VerifiedChains[0][0].Subject.CommonName; cn != name {
			return fmt.Errorf("pki: client %q is not %q", cn, name)
		}
		return nil
	}
	return out
}
