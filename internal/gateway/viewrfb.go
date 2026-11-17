package gateway

import (
	"crypto/des"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

// The two halves of RFB the view relay speaks (DECISIONS I-1216). Both
// are here because this is the one place in the tree that knows the
// protocol, and keeping them together is what stops the browser's half
// from ever being written to expect a password.
//
//   - rfbAuthenticate is the client's half, to the guest's Xvnc: VncAuth
//     with the boot's password, which never leaves the edge.
//   - rfbOfferNone is the server's half, to the page: the same version
//     exchange with security type None, because the page's noVNC starts
//     its own state machine at the version string and has no entry point
//     for a stream that is already authenticated.
//
// So the browser speaks a complete, ordinary RFB session that happens to
// need no credential, and the guest speaks a complete, ordinary VncAuth
// session whose password the browser never sees. The relay joins them at
// ClientInit and is a byte pipe from there.

// rfbVersion is the version this relay speaks in both directions. 3.8 is
// what the guest's Xvnc and the viewer's noVNC both speak, and it is the
// version that gives SecurityResult a reason string on failure.
const rfbVersion = "RFB 003.008\n"

// RFB security types (RFB 3.8 §7.1.2).
const (
	rfbSecNone    = 1
	rfbSecVncAuth = 2
)

// rfbAuthenticate performs VncAuth against the guest's Xvnc and returns
// with the stream positioned where a client would send ClientInit.
//
// VncAuth is DES on the server's 16-byte challenge, keyed on the
// password's first 8 bytes with the bits of each byte reversed -- the
// protocol's own mistake, not a choice here, and the reason crypto/des
// (which Go keeps for exactly this kind of legacy protocol) is the right
// import and not a weak-cipher decision. The same algorithm is in
// nix/guest/tests/rfb-client.py, by hand, because the guest's python has
// no DES; here the standard library writes it.
func rfbAuthenticate(guest net.Conn, password string) error {
	var version [12]byte
	if _, err := io.ReadFull(guest, version[:]); err != nil {
		return fmt.Errorf("rfb: read the server version: %w", err)
	}
	if string(version[:8]) != "RFB 003." {
		return errors.New("rfb: the guest did not answer with a version string")
	}
	if _, err := guest.Write([]byte(rfbVersion)); err != nil {
		return err
	}
	var count [1]byte
	if _, err := io.ReadFull(guest, count[:]); err != nil {
		return fmt.Errorf("rfb: read the security-type count: %w", err)
	}
	if count[0] == 0 {
		// A zero count means the server refused, with a reason string
		// after it. The reason is never read into a log line: it is the
		// guest's own text.
		return errors.New("rfb: the guest refused the connection")
	}
	types := make([]byte, count[0])
	if _, err := io.ReadFull(guest, types); err != nil {
		return fmt.Errorf("rfb: read the security types: %w", err)
	}
	var offered bool
	for _, t := range types {
		if t == rfbSecVncAuth {
			offered = true
		}
	}
	if !offered {
		return errors.New("rfb: the guest offered no VncAuth")
	}
	if _, err := guest.Write([]byte{rfbSecVncAuth}); err != nil {
		return err
	}
	var challenge [16]byte
	if _, err := io.ReadFull(guest, challenge[:]); err != nil {
		return fmt.Errorf("rfb: read the challenge: %w", err)
	}
	response, err := vncAuthResponse(password, challenge[:])
	if err != nil {
		return err
	}
	if _, err := guest.Write(response); err != nil {
		return err
	}
	var result [4]byte
	if _, err := io.ReadFull(guest, result[:]); err != nil {
		return fmt.Errorf("rfb: read the security result: %w", err)
	}
	if binary.BigEndian.Uint32(result[:]) != 0 {
		return errors.New("rfb: the guest refused the password")
	}
	return nil
}

// rfbOfferNone is the server's half, to the page: the version exchange,
// security type None, and a zero SecurityResult. After it the page sends
// ClientInit, which is the first byte the relay pipes to the guest.
func rfbOfferNone(client net.Conn) error {
	if _, err := client.Write([]byte(rfbVersion)); err != nil {
		return err
	}
	var version [12]byte
	if _, err := io.ReadFull(client, version[:]); err != nil {
		return fmt.Errorf("rfb: read the client version: %w", err)
	}
	if string(version[:8]) != "RFB 003." {
		return errors.New("rfb: the client did not answer with a version string")
	}
	// One type, None: the stream is already past the guest's VncAuth, and
	// a page that is told to expect a password is a page that holds one.
	if _, err := client.Write([]byte{1, rfbSecNone}); err != nil {
		return err
	}
	var chosen [1]byte
	if _, err := io.ReadFull(client, chosen[:]); err != nil {
		return fmt.Errorf("rfb: read the client's security type: %w", err)
	}
	if chosen[0] != rfbSecNone {
		return errors.New("rfb: the client chose a security type that was not offered")
	}
	_, err := client.Write([]byte{0, 0, 0, 0})
	return err
}

// vncAuthResponse is the 32-byte answer to a 16-byte challenge: two ECB
// blocks under the bit-reversed password key.
func vncAuthResponse(password string, challenge []byte) ([]byte, error) {
	if len(challenge) != 16 {
		return nil, fmt.Errorf("rfb: a challenge of %d bytes", len(challenge))
	}
	var key [8]byte
	copy(key[:], password) // longer than 8 is truncated, shorter is zero-padded
	for i, b := range key {
		key[i] = reverseBits(b)
	}
	block, err := des.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	block.Encrypt(out[:8], challenge[:8])
	block.Encrypt(out[8:], challenge[8:])
	return out, nil
}

func reverseBits(b byte) byte {
	var out byte
	for i := 0; i < 8; i++ {
		out <<= 1
		out |= b & 1
		b >>= 1
	}
	return out
}
