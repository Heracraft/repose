// Package wsframe reads and writes RFC 6455 frames, and accepts an
// upgrade on the server side. It is the framing the CDP bridge has used
// since DECISIONS I-292's sibling work (internal/cli/bridgews.go), lifted
// out of internal/cli so the api's browser-view stream can share it
// rather than taking a WebSocket dependency.
//
// The message cap is the caller's, not a constant here: the bridge needs
// tens of MB for a full-page screenshot, and the view's stream needs
// 1 MiB. A shared default would be whichever caller was written first.
//
// No extension is ever agreed to: Accept answers with no
// Sec-WebSocket-Extensions header, so no peer compresses, and a frame
// with a reserved bit set ends the connection.
package wsframe

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// Opcodes of RFC 6455 §5.2. Op >= OpClose is a control frame.
const (
	OpCont   = 0x0
	OpText   = 0x1
	OpBinary = 0x2
	OpClose  = 0x8
	OpPing   = 0x9
	OpPong   = 0xA
)

// Frame is one frame as it was read.
type Frame struct {
	Fin     bool
	RSV     byte
	Op      byte
	Payload []byte // unmasked
	Raw     []byte // as it was read, for a proxy that passes it on whole
}

// ReadFrame reads one frame, refusing a payload longer than max bytes
// before any of it is allocated.
func ReadFrame(r *bufio.Reader, max int64) (Frame, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	f := Frame{Fin: h[0]&0x80 != 0, RSV: h[0] & 0x70, Op: h[0] & 0x0f}
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7f)
	raw := append(make([]byte, 0, 14), h[:]...)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Frame{}, err
		}
		raw = append(raw, b[:]...)
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Frame{}, err
		}
		raw = append(raw, b[:]...)
		n = binary.BigEndian.Uint64(b[:])
	}
	if max > 0 && n > uint64(max) {
		return Frame{}, fmt.Errorf("websocket frame of %d bytes", n)
	}
	var key [4]byte
	if masked {
		if _, err := io.ReadFull(r, key[:]); err != nil {
			return Frame{}, err
		}
		raw = append(raw, key[:]...)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return Frame{}, err
	}
	f.Raw = append(raw, p...)
	if masked {
		for i := range p {
			p[i] ^= key[i%4]
		}
	}
	f.Payload = p
	return f, nil
}

// Encode is one final frame; masked is the client's side.
func Encode(op byte, payload []byte, masked bool) []byte {
	b := make([]byte, 0, len(payload)+14)
	b = append(b, 0x80|op)
	var m byte
	if masked {
		m = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		b = append(b, m|byte(n))
	case n <= 0xffff:
		b = append(b, m|126, byte(n>>8), byte(n))
	default:
		b = append(b, m|127)
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}
	if !masked {
		return append(b, payload...)
	}
	var key [4]byte
	_, _ = rand.Read(key[:])
	b = append(b, key[:]...)
	start := len(b)
	b = append(b, payload...)
	for i := range payload {
		b[start+i] ^= key[i%4]
	}
	return b
}

// ReadMessage reads one whole data message, of at most max bytes. Control
// frames, which may arrive between a message's fragments, go to onControl
// as they come.
func ReadMessage(r *bufio.Reader, max int64, onControl func(Frame) error) (op byte, payload, raw []byte, err error) {
	for {
		f, err := ReadFrame(r, max)
		if err != nil {
			return 0, nil, nil, err
		}
		if f.RSV != 0 {
			return 0, nil, nil, errors.New("websocket frame with a reserved bit set (an extension that was not agreed to)")
		}
		if f.Op >= OpClose {
			if err := onControl(f); err != nil {
				return 0, nil, nil, err
			}
			continue
		}
		switch {
		case op == 0 && f.Op == OpCont:
			return 0, nil, nil, errors.New("websocket continuation with no message")
		case op != 0 && f.Op != OpCont:
			return 0, nil, nil, errors.New("websocket message inside another")
		case op == 0:
			op = f.Op
		}
		payload = append(payload, f.Payload...)
		raw = append(raw, f.Raw...)
		if max > 0 && int64(len(payload)) > max {
			return 0, nil, nil, fmt.Errorf("websocket message over %d bytes", max)
		}
		if f.Fin {
			return op, payload, raw, nil
		}
	}
}

// acceptGUID is RFC 6455 §1.3's magic value.
const acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// AcceptKey is the Sec-WebSocket-Accept value for a client's key.
func AcceptKey(key string) string {
	// sha1 is what RFC 6455 §4.2.2 names for this value; it carries no
	// security claim and is not a choice this code gets to make.
	sum := sha1.Sum([]byte(key + acceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// Accept answers an upgrade and hands back the hijacked connection. It
// agrees to no extension and no subprotocol: the answer carries neither
// header, so a client that offered compression does not get it.
//
// The caller owns the connection from here and must close it.
func Accept(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, nil, errors.New("not a websocket upgrade")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, nil, errors.New("websocket version 13 only")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, nil, errors.New("no Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the response cannot be hijacked")
	}
	c, brw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	_, err = fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", AcceptKey(key))
	if err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	return c, brw, nil
}
