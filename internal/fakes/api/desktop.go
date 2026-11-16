package api

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/heracraft/repose/internal/wsframe"
)

// A static desktop behind the browser view's stream route (DECISIONS
// I-1216), so a test can reach the state the view only has while frames
// are coming: the picture, and the clipboard panel under it.
//
// Off by default. The suite's reconnect and Resume view cases need the
// stream to refuse, which is what a real api with no VIEW_RELAY_ADDR
// does, so `SetDesktop` turns this on for the tests that want frames.
//
// It speaks the server half of RFB 3.8 as far as ServerInit, which is
// where noVNC fires `connect`, then answers a full-screen update once
// and holds still. Both halves of the cut-text channel are here too: a
// paste the page sends arrives as ClientCutText and is recorded for the
// test to assert, and `copy` is handed to the page as ServerCutText, as
// a machine copying something would.
//
// Every pixel is grey (r == g == b), so the picture reads the same
// whatever pixel format the client asks for: this fake ignores
// SetPixelFormat, and a grey screen cannot show that.
const (
	deskWidth  = 1024
	deskHeight = 640
	// A client frame is never large here: the biggest is a paste.
	deskMaxFrame = 1 << 20
)

// SetDesktop turns the fake's desktop on or off. With `copy`, the
// desktop hands the page that text as the machine's own cut-text once
// the handshake is done.
func (f *Fake) SetDesktop(on bool, copy string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.desktopOn = on
	f.desktopCopy = copy
	if !on {
		f.desktopPastes = nil
	}
}

// DesktopPastes is what the page sent the desktop as ClientCutText, in
// order: what the clipboard panel's Send actually put on the wire.
func (f *Fake) DesktopPastes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Never nil: the admin route marshals this, and a nil slice is `null`,
	// which a test comparing against [] has to special-case.
	return append(make([]string, 0, len(f.desktopPastes)), f.desktopPastes...)
}

func (f *Fake) desktopWanted() (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.desktopOn, f.desktopCopy
}

func (f *Fake) recordPaste(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.desktopPastes = append(f.desktopPastes, text)
}

// rfbStream is the byte stream RFB runs on, carried in WebSocket binary
// frames: a message may be split across frames or share one, so the
// payloads are buffered and handed out by the byte.
type rfbStream struct {
	r    *bufio.Reader
	buf  []byte
	conn net.Conn
	mu   sync.Mutex
}

func (s *rfbStream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		op, payload, _, err := wsframe.ReadMessage(s.r, deskMaxFrame, nil)
		if err != nil {
			return 0, err
		}
		if op == opClose {
			return 0, io.EOF
		}
		if op != opBinary && op != opText {
			continue
		}
		s.buf = payload
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// send is one RFB message, in one binary frame. It takes a lock because
// the cut-text the fake offers is written from the same goroutine as the
// framebuffer, and a future caller may not be.
func (s *rfbStream) send(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.conn.Write(wsframe.Encode(opBinary, b, false))
	return err
}

const (
	opText   = 1
	opBinary = 2
	opClose  = 8
)

// serveFakeDesktop runs the handshake and then answers what the client
// asks, until it goes away. Every error ends the stream: a test that
// breaks the protocol should see the view fail, not hang.
func (f *Fake) serveFakeDesktop(conn net.Conn, rw *bufio.ReadWriter, copyText string) {
	s := &rfbStream{r: rw.Reader, conn: conn}
	if err := f.desktopHandshake(s, copyText); err != nil {
		return
	}
	_ = f.desktopServe(s)
}

func (f *Fake) desktopHandshake(s *rfbStream, copyText string) error {
	if err := s.send([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	var version [12]byte
	if _, err := io.ReadFull(s, version[:]); err != nil {
		return err
	}
	// One security type, None (1): the real path arrives past
	// SecurityResult because the relay has done VncAuth already, and the
	// page has no credential to send either way.
	if err := s.send([]byte{1, 1}); err != nil {
		return err
	}
	var chosen [1]byte
	if _, err := io.ReadFull(s, chosen[:]); err != nil {
		return err
	}
	if chosen[0] != 1 {
		return errors.New("fake desktop: the client chose another security type")
	}
	if err := s.send([]byte{0, 0, 0, 0}); err != nil { // SecurityResult: OK
		return err
	}
	var shared [1]byte
	if _, err := io.ReadFull(s, shared[:]); err != nil {
		return err
	}
	name := []byte("todo-app")
	init := make([]byte, 0, 24+len(name))
	init = binary.BigEndian.AppendUint16(init, deskWidth)
	init = binary.BigEndian.AppendUint16(init, deskHeight)
	init = append(init,
		32,     // bits per pixel
		24,     // depth
		0,      // little-endian
		1,      // true colour
		0, 255, // red max
		0, 255, // green max
		0, 255, // blue max
		16, 8, 0, // red, green, blue shift
		0, 0, 0, // padding
	)
	init = binary.BigEndian.AppendUint32(init, uint32(len(name)))
	init = append(init, name...)
	if err := s.send(init); err != nil {
		return err
	}
	if copyText != "" {
		// The machine copied something: ServerCutText (message 3), which
		// the page puts in its panel and nowhere else.
		cut := []byte{3, 0, 0, 0}
		cut = binary.BigEndian.AppendUint32(cut, uint32(len(copyText)))
		cut = append(cut, copyText...)
		if err := s.send(cut); err != nil {
			return err
		}
	}
	return nil
}

// desktopServe answers the client's messages. A non-incremental
// framebuffer update request gets the whole screen; an incremental one
// gets nothing, which is a screen that is not changing.
func (f *Fake) desktopServe(s *rfbStream) error {
	for {
		var kind [1]byte
		if _, err := io.ReadFull(s, kind[:]); err != nil {
			return err
		}
		switch kind[0] {
		case 0: // SetPixelFormat: ignored, see the grey note above
			if err := skip(s, 19); err != nil {
				return err
			}
		case 2: // SetEncodings
			var head [3]byte
			if _, err := io.ReadFull(s, head[:]); err != nil {
				return err
			}
			if err := skip(s, 4*int(binary.BigEndian.Uint16(head[1:]))); err != nil {
				return err
			}
		case 3: // FramebufferUpdateRequest
			var req [9]byte
			if _, err := io.ReadFull(s, req[:]); err != nil {
				return err
			}
			if req[0] == 0 {
				if err := s.send(greyScreen()); err != nil {
					return err
				}
			}
		case 4: // KeyEvent
			if err := skip(s, 7); err != nil {
				return err
			}
		case 5: // PointerEvent
			if err := skip(s, 5); err != nil {
				return err
			}
		case 6: // ClientCutText: the panel's Send reached the machine
			var head [7]byte
			if _, err := io.ReadFull(s, head[:]); err != nil {
				return err
			}
			n := binary.BigEndian.Uint32(head[3:])
			if n > deskMaxFrame {
				return errors.New("fake desktop: a paste that large is not a paste")
			}
			text := make([]byte, n)
			if _, err := io.ReadFull(s, text); err != nil {
				return err
			}
			f.recordPaste(string(text))
		default:
			return errors.New("fake desktop: unknown client message")
		}
	}
}

func skip(r io.Reader, n int) error {
	if n <= 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, int64(n))
	return err
}

// greyScreen is one FramebufferUpdate of the whole screen, raw encoded:
// a light page under a darker bar, so the picture reads as a window and
// not as a failure to draw.
func greyScreen() []byte {
	const bar = 48
	out := make([]byte, 0, 16+deskWidth*deskHeight*4)
	out = append(out, 0, 0) // FramebufferUpdate, padding
	out = binary.BigEndian.AppendUint16(out, 1)
	out = binary.BigEndian.AppendUint16(out, 0) // x
	out = binary.BigEndian.AppendUint16(out, 0) // y
	out = binary.BigEndian.AppendUint16(out, deskWidth)
	out = binary.BigEndian.AppendUint16(out, deskHeight)
	out = binary.BigEndian.AppendUint32(out, 0) // Raw
	for y := 0; y < deskHeight; y++ {
		shade := byte(0xf4)
		if y < bar {
			shade = 0x33
		}
		for x := 0; x < deskWidth; x++ {
			out = append(out, shade, shade, shade, 0)
		}
	}
	return out
}
