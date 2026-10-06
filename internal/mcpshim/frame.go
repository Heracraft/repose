package mcpshim

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// The hold's stdio carries every connection of a forward (DECISIONS I-557,
// the design's critic correction 1): the laptop runs `ssh MACHINE
// repose-mcp hold NAME...` with no -R, since the gateway relays only
// forwarded-tcpip channels back to a client (I-296). Each frame is a type
// byte, a stream id and a length, all big-endian, then the payload.
//
//	H  hold -> laptop  hello; payload is the frame version, "1"
//	O  hold -> laptop  a shim connected for NAME (payload): start a server
//	D  both            bytes of that stream
//	C  both            the stream ended (a closed socket, or the server exited)
//	R  hold -> laptop  NAME is registered, or failed to; payload is Ready JSON
//	G  hold -> laptop  another hold took NAME over; payload is NAME
const (
	FrameHello byte = 'H'
	FrameOpen  byte = 'O'
	FrameData  byte = 'D'
	FrameClose byte = 'C'
	FrameReady byte = 'R'
	FrameGone  byte = 'G'
)

// FrameVersion is the hello's payload.
const FrameVersion = "1"

// maxFrame bounds a payload; data is sent in chunks well under it.
const maxFrame = 1 << 20

// chunk is the most data one D frame carries.
const chunk = 32 << 10

// Frame is one frame of the hold's stdio.
type Frame struct {
	Type    byte
	ID      uint32
	Payload []byte
}

// Ready is the R frame: NAME's tools as the cache fill found them.
type Ready struct {
	Name  string `json:"name"`
	Tools int    `json:"tools"`
	// New is a first registration: agents already running lack NAME.
	New bool `json:"new,omitempty"`
	// Changed is a tool list that differs from the cached one.
	Changed bool   `json:"changed,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (Frame, error) {
	var h [9]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(h[5:])
	if n > maxFrame {
		return Frame{}, fmt.Errorf("frame of %d bytes", n)
	}
	f := Frame{Type: h[0], ID: binary.BigEndian.Uint32(h[1:5]), Payload: make([]byte, n)}
	if _, err := io.ReadFull(r, f.Payload); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// FrameWriter writes whole frames from many goroutines.
type FrameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewFrameWriter writes frames to w.
func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

// Write sends one frame.
func (fw *FrameWriter) Write(typ byte, id uint32, payload []byte) error {
	if len(payload) > maxFrame {
		return errors.New("frame too large")
	}
	b := make([]byte, 9+len(payload))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:5], id)
	binary.BigEndian.PutUint32(b[5:9], uint32(len(payload)))
	copy(b[9:], payload)
	fw.mu.Lock()
	defer fw.mu.Unlock()
	_, err := fw.w.Write(b)
	return err
}

// Data sends p as D frames on stream id, in chunks, under one lock so a
// whole line arrives before anything else on that stream.
func (fw *FrameWriter) Data(id uint32, p []byte) error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	for len(p) > 0 {
		n := len(p)
		if n > chunk {
			n = chunk
		}
		b := make([]byte, 9+n)
		b[0] = FrameData
		binary.BigEndian.PutUint32(b[1:5], id)
		binary.BigEndian.PutUint32(b[5:9], uint32(n))
		copy(b[9:], p[:n])
		if _, err := fw.w.Write(b); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// ReadyFrame encodes r for an R frame.
func ReadyFrame(r Ready) []byte {
	b, _ := json.Marshal(r)
	return b
}
