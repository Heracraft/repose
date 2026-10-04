package main

import (
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// A TLS side listener whose certificate is not there is never opened, so
// the port refuses instead of holding clients (I-479); an inherited socket
// for it is closed with the other unused ones.
func TestTLSListenerWithoutCertificateStaysClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	missing := filepath.Join(t.TempDir(), "wildcard.crt")
	ls := &listenerSet{inherited: map[string]net.Listener{}}
	if got, ok := tlsListener(ls, log, lnPreview, addr, missing, missing); ok || got != nil {
		t.Fatal("opened a listener with no certificate")
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = c.Close()
		t.Fatalf("%s accepts a connection", addr)
	}

	inherited, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	iaddr := inherited.Addr().String()
	ls = &listenerSet{inherited: map[string]net.Listener{lnPreview: inherited}}
	if _, ok := tlsListener(ls, log, lnPreview, iaddr, missing, missing); ok {
		t.Fatal("served an inherited socket with no certificate")
	}
	ls.closeUnused()
	if c, err := net.DialTimeout("tcp", iaddr, time.Second); err == nil {
		_ = c.Close()
		t.Fatalf("inherited %s still accepts", iaddr)
	}
}
