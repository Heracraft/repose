package edge_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/edge"
	"github.com/heracraft/repose/internal/api/pki"
)

// relay is the edge's half: it reads the request line and answers as the
// real relay does.
type relay struct {
	addr string
	reqs chan string
}

func newRelay(t *testing.T, ca *pki.CA, reply string, stream string) *relay {
	t.Helper()
	serverCert, err := ca.ServerTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := pki.RequireClientName(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    ca.Pool(),
		MinVersion:   tls.VersionTLS13,
	}, pki.APIClientName)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	r := &relay{addr: ln.Addr().String(), reqs: make(chan string, 4)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				buf := make([]byte, 512)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				select {
				case r.reqs <- string(buf[:n]):
				default:
				}
				if _, err := c.Write([]byte(reply)); err != nil {
					return
				}
				if stream != "" {
					_, _ = c.Write([]byte(stream))
				}
				time.Sleep(200 * time.Millisecond)
			}()
		}
	}()
	return r
}

func newCA(t *testing.T) *pki.CA {
	t.Helper()
	ca, err := pki.Generate("repose-api-test")
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func client(t *testing.T, ca *pki.CA, addr string, roots *x509.CertPool) *edge.Client {
	t.Helper()
	cert, err := ca.ClientTLS(pki.APIClientName)
	if err != nil {
		t.Fatal(err)
	}
	c, err := edge.New(addr, cert, roots)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The happy path: the request carries exactly what the ticket's row said,
// and the connection comes back positioned at the first stream byte.
func TestDialSendsTheTicketsProjectAndReturnsTheStream(t *testing.T) {
	ca := newCA(t)
	r := newRelay(t, ca, "{\"ok\":true}\n", "RFB 003.008\n")
	conn, err := client(t, ca, r.addr, ca.Pool()).Dial(context.Background(), "0193f0c8-2c5a-7000-8000-00000000beef", "todo-app.ada", "10.64.1.2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	req := <-r.reqs
	for _, want := range []string{"0193f0c8-2c5a-7000-8000-00000000beef", "todo-app.ada", "10.64.1.2"} {
		if !strings.Contains(req, want) {
			t.Fatalf("the request %q is missing %q", req, want)
		}
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var first [12]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	if string(first[:]) != "RFB 003.008\n" {
		t.Fatalf("the first stream bytes were %q", first[:])
	}
}

// A refusal comes back as a typed error carrying the code, which is what
// the mint and stream routes turn into the user's words.
func TestDialRefusalIsATypedError(t *testing.T) {
	ca := newCA(t)
	r := newRelay(t, ca, "{\"code\":\"view_busy\"}\n", "")
	_, err := client(t, ca, r.addr, ca.Pool()).Dial(context.Background(), "p", "l", "10.64.1.2")
	if edge.Code(err) != "view_busy" {
		t.Fatalf("err %v, code %q, want view_busy", err, edge.Code(err))
	}
	var refused *edge.Refused
	if !errors.As(err, &refused) {
		t.Fatalf("err %v is not a *edge.Refused", err)
	}
	// An ok:false with no code is still an error, not a usable stream.
	r2 := newRelay(t, ca, "{}\n", "")
	if _, err := client(t, ca, r2.addr, ca.Pool()).Dial(context.Background(), "p", "l", "10.64.1.2"); err == nil {
		t.Fatal("an answer with neither ok nor a code was accepted")
	}
}

// A relay whose certificate the api does not trust is not dialled: the
// pinning runs both ways on this leg.
func TestDialRefusesARelayFromAnotherCA(t *testing.T) {
	ca := newCA(t)
	other := newCA(t)
	r := newRelay(t, ca, "{\"ok\":true}\n", "")
	if _, err := client(t, ca, r.addr, other.Pool()).Dial(context.Background(), "p", "l", "10.64.1.2"); err == nil {
		t.Fatal("a relay certificate from an unknown CA was accepted")
	}
}

// An empty VIEW_RELAY_ADDR is the feature switched off: a nil client, no
// error, and the caller answers `unavailable`. Everything else that is
// missing is a configuration error at start, not at the first view.
func TestNewWithNoAddressIsTheFeatureOff(t *testing.T) {
	ca := newCA(t)
	cert, err := ca.ClientTLS(pki.APIClientName)
	if err != nil {
		t.Fatal(err)
	}
	c, err := edge.New("", cert, ca.Pool())
	if c != nil || err != nil {
		t.Fatalf("New(\"\") = %v, %v", c, err)
	}
	if _, err := edge.New("10.255.0.1", cert, ca.Pool()); err == nil {
		t.Fatal("an address with no port was accepted")
	}
	if _, err := edge.New("10.255.0.1:6081", tls.Certificate{}, ca.Pool()); err == nil {
		t.Fatal("no client certificate was accepted")
	}
	if _, err := edge.New("10.255.0.1:6081", cert, nil); err == nil {
		t.Fatal("no root pool was accepted")
	}
}

// A relay that never answers ends with the dial's own deadline rather
// than holding a request goroutine for ever.
func TestDialGivesUpOnASilentRelay(t *testing.T) {
	ca := newCA(t)
	serverCert, err := ca.ServerTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{serverCert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { time.Sleep(30 * time.Second); _ = c.Close() }()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := client(t, ca, ln.Addr().String(), ca.Pool()).Dial(ctx, "p", "l", "10.64.1.2"); err == nil {
		t.Fatal("a silent relay answered")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the dial held for %v", took)
	}
}
