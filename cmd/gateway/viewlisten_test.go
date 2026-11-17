package main

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/pki"
)

// The browser-view relay's listener admits the api's own client
// certificate and no other identity from the same CA, which is the mirror
// of the api's /internal listener admitting the gateway's and no other
// (I-431, I-1216).
//
// This is the one control that stands between the relay and anything else
// that can send a packet from the control VM's WireGuard address -- a
// shared Docker host, so that address says only "something on that box".
// Without the pinning, a source /32 in nftables would be the whole
// credential for a stream of any tenant's desktop.
func TestViewListenerAdmitsOnlyTheAPI(t *testing.T) {
	ca, err := pki.Generate("repose-api-test")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	serverCert, serverKey, err := ca.IssueServer(time.Hour, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	certFile := write("edge-internal.crt", serverCert)
	keyFile := write("edge-internal.key", serverKey)
	caFile := write("api-ca.pem", ca.CertPEM)

	ls := &listenerSet{inherited: map[string]net.Listener{}}
	raw, cfg, err := viewListener(ls, "127.0.0.1:0", certFile, keyFile, caFile)
	if err != nil {
		t.Fatal(err)
	}
	ln := tls.NewListener(raw, cfg)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// The handshake is what this test is about; a connection that
			// gets through is read once so the handshake completes.
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = c.Write([]byte("{}\n"))
			}()
		}
	}()

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	dial := func(cn string) error {
		var certs []tls.Certificate
		if cn != "" {
			pair, err := ca.ClientTLS(cn)
			if err != nil {
				t.Fatal(err)
			}
			certs = []tls.Certificate{pair}
		}
		c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			RootCAs:      roots,
			ServerName:   "127.0.0.1",
			Certificates: certs,
			MinVersion:   tls.VersionTLS13,
		})
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		// Under TLS 1.3 the client's own handshake call returns before
		// the server has looked at its certificate, so a refusal arrives
		// as an error on the first read.
		var buf [8]byte
		if _, err := c.Read(buf[:]); err != nil {
			return err
		}
		return nil
	}

	// No certificate at all, the gateway's own certificate from the same
	// CA, a host id, and a near miss on the name.
	for _, who := range []string{"", pki.GatewayClientName, "host-02", "api-2", "API"} {
		if err := dial(who); err == nil {
			t.Errorf("client %q reached the view listener", who)
		}
	}
	if err := dial(pki.APIClientName); err != nil {
		t.Fatalf("the api itself was refused: %v", err)
	}
}

// The relay is reachable over WireGuard only, so an address that binds
// every interface is a configuration error and not a listener.
func TestViewListenerRefusesEveryInterface(t *testing.T) {
	ls := &listenerSet{inherited: map[string]net.Listener{}}
	dir := t.TempDir()
	for _, addr := range []string{":6081", "0.0.0.0:6081", "[::]:6081"} {
		if _, _, err := viewListener(ls, addr, filepath.Join(dir, "c"), filepath.Join(dir, "k"), filepath.Join(dir, "ca")); err == nil {
			t.Errorf("%s was accepted", addr)
		}
	}
	// And no certificate, or no api CA, is a refusal rather than an open
	// port with nothing authenticating it.
	if _, _, err := viewListener(ls, "127.0.0.1:0", "", "", filepath.Join(dir, "ca")); err == nil {
		t.Error("a view listener with no certificate was opened")
	}
	if _, _, err := viewListener(ls, "127.0.0.1:0", filepath.Join(dir, "c"), filepath.Join(dir, "k"), ""); err == nil {
		t.Error("a view listener with no API_CA was opened")
	}
}
