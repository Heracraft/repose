package gateway

import (
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// VncAuth against the vector the guest's own client computes. The two
// values below came from nix/guest/tests/rfb-client.py's
// vnc_auth_response (the hand-written DES that exists because the guest's
// python has none), over the challenge 00..0f:
//
//	s3cr3tpw -> e2bf3bcaeab1bf09fac3bf9e9ab33412
//	Ab3      -> c25096b05fde044a0b03eb30c33b7f68
//
// The second is the short-password case, where VNC zero-pads the key to
// eight bytes. If this test fails, the relay and the guest disagree about
// the protocol and no view will ever authenticate.
func TestVncAuthResponseMatchesTheGuestsOwnClient(t *testing.T) {
	challenge := make([]byte, 16)
	for i := range challenge {
		challenge[i] = byte(i)
	}
	for _, tc := range []struct{ password, want string }{
		{"s3cr3tpw", "e2bf3bcaeab1bf09fac3bf9e9ab33412"},
		{"Ab3", "c25096b05fde044a0b03eb30c33b7f68"},
	} {
		got, err := vncAuthResponse(tc.password, challenge)
		if err != nil {
			t.Fatalf("%s: %v", tc.password, err)
		}
		if hex.EncodeToString(got) != tc.want {
			t.Errorf("%s: %s, want %s", tc.password, hex.EncodeToString(got), tc.want)
		}
	}
	// A password longer than eight bytes is truncated, as VNC does, so
	// these two must agree.
	long, _ := vncAuthResponse("s3cr3tpwAND-MORE", challenge)
	short, _ := vncAuthResponse("s3cr3tpw", challenge)
	if hex.EncodeToString(long) != hex.EncodeToString(short) {
		t.Error("VncAuth keys on the first eight bytes only")
	}
	if _, err := vncAuthResponse("x", challenge[:8]); err == nil {
		t.Error("a challenge that is not 16 bytes was accepted")
	}
}

// Every way the guest half can go wrong is an error and not a panic, and
// none of them leaves the caller thinking it is authenticated.
func TestRFBAuthenticateRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		serve func(c net.Conn)
	}{
		{"no version string", func(c net.Conn) { _, _ = c.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")) }},
		{"a short version string", func(c net.Conn) { _, _ = c.Write([]byte("RFB")) }},
		{"a refusal instead of security types", func(c net.Conn) {
			_, _ = c.Write([]byte("RFB 003.008\n"))
			_, _ = io.ReadFull(c, make([]byte, 12))
			_, _ = c.Write([]byte{0})
		}},
		{"no VncAuth offered", func(c net.Conn) {
			_, _ = c.Write([]byte("RFB 003.008\n"))
			_, _ = io.ReadFull(c, make([]byte, 12))
			_, _ = c.Write([]byte{1, rfbSecNone})
		}},
		{"a non-zero SecurityResult", func(c net.Conn) {
			_, _ = c.Write([]byte("RFB 003.008\n"))
			_, _ = io.ReadFull(c, make([]byte, 12))
			_, _ = c.Write([]byte{1, rfbSecVncAuth})
			_, _ = io.ReadFull(c, make([]byte, 1))
			_, _ = c.Write(make([]byte, 16))
			_, _ = io.ReadFull(c, make([]byte, 16))
			_, _ = c.Write([]byte{0, 0, 0, 1})
			// RFB 3.8 adds a reason string, which must never be read into
			// a log line: it is the guest's own text.
			_, _ = c.Write([]byte{0, 0, 0, 5, 'n', 'o', 'p', 'e', '!'})
		}},
		{"half a challenge", func(c net.Conn) {
			_, _ = c.Write([]byte("RFB 003.008\n"))
			_, _ = io.ReadFull(c, make([]byte, 12))
			_, _ = c.Write([]byte{1, rfbSecVncAuth})
			_, _ = io.ReadFull(c, make([]byte, 1))
			_, _ = c.Write(make([]byte, 8))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer func() { _ = client.Close() }()
			go func() {
				defer func() { _ = server.Close() }()
				tc.serve(server)
			}()
			_ = client.SetDeadline(time.Now().Add(5 * time.Second))
			err := rfbAuthenticate(client, "s3cr3tpw")
			if err == nil {
				t.Fatal("authenticated")
			}
			if strings.Contains(err.Error(), "nope!") {
				t.Fatalf("the guest's own text reached the error: %v", err)
			}
		})
	}
}

// The page's half: the relay offers None and nothing else, because a page
// that is told to expect a password is a page that holds one. A client
// that picks a type that was not offered is refused.
func TestRFBOfferNone(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	done := make(chan error, 1)
	go func() { done <- rfbOfferNone(server) }()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	var version [12]byte
	if _, err := io.ReadFull(client, version[:]); err != nil {
		t.Fatal(err)
	}
	if string(version[:]) != rfbVersion {
		t.Fatalf("version %q", version[:])
	}
	if _, err := client.Write([]byte(rfbVersion)); err != nil {
		t.Fatal(err)
	}
	var types [2]byte
	if _, err := io.ReadFull(client, types[:]); err != nil {
		t.Fatal(err)
	}
	if types != [2]byte{1, rfbSecNone} {
		t.Fatalf("offered %v, want one None", types)
	}
	if _, err := client.Write([]byte{rfbSecVncAuth}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("a client that chose VncAuth was accepted")
	}

	// And the whole exchange, as the page does it.
	client, server = net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { done <- rfbOfferNone(server) }()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(client, version[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte(rfbVersion)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, types[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte{rfbSecNone}); err != nil {
		t.Fatal(err)
	}
	var result [4]byte
	if _, err := io.ReadFull(client, result[:]); err != nil {
		t.Fatal(err)
	}
	if result != [4]byte{0, 0, 0, 0} {
		t.Fatalf("SecurityResult %v", result)
	}
	if err := <-done; err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("the handshake failed: %v", err)
	}
}
