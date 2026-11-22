// Package vsockrpc is the framing shared by hostd's client side and guestd's
// server side of docs/interfaces/vsock-guestd.md: one uvarint length prefix,
// then one serialised Envelope. It carries requests, responses matched by
// request_id, and unsolicited Notify messages from the guest.
package vsockrpc

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
)

// MaxFrame bounds a single frame. Exec output is capped at 64 KB per stream
// and Switch output at 32 KB, so anything near this size is a protocol error.
const MaxFrame = 4 << 20

// ErrClosed is returned by calls on a closed connection.
var ErrClosed = errors.New("vsockrpc: connection closed")

// WriteFrame writes one length-prefixed message.
func WriteFrame(w io.Writer, m proto.Message) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("vsockrpc: marshal: %w", err)
	}
	if len(b) > MaxFrame {
		return fmt.Errorf("vsockrpc: frame of %d bytes exceeds %d", len(b), MaxFrame)
	}
	var hdr [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(hdr[:], uint64(len(b)))
	if _, err := w.Write(append(hdr[:n], b...)); err != nil {
		return fmt.Errorf("vsockrpc: write: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed message into m.
func ReadFrame(r *bufio.Reader, m proto.Message) error {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	if n > MaxFrame {
		return fmt.Errorf("vsockrpc: frame of %d bytes exceeds %d", n, MaxFrame)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("vsockrpc: read body: %w", err)
	}
	if err := proto.Unmarshal(buf, m); err != nil {
		return fmt.Errorf("vsockrpc: unmarshal: %w", err)
	}
	return nil
}

// Conn is a framed connection. Reads are single-goroutine; writes are
// serialised by a mutex so several goroutines may send.
type Conn struct {
	r    *bufio.Reader
	c    net.Conn
	wmu  sync.Mutex
	done atomic.Bool
}

// NewConn wraps a net.Conn.
func NewConn(c net.Conn) *Conn {
	return &Conn{r: bufio.NewReaderSize(c, 64<<10), c: c}
}

// Write sends one envelope.
func (c *Conn) Write(env *guestdv1.Envelope) error {
	if c.done.Load() {
		return ErrClosed
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return WriteFrame(c.c, env)
}

// Read receives one envelope.
func (c *Conn) Read() (*guestdv1.Envelope, error) {
	env := &guestdv1.Envelope{}
	if err := ReadFrame(c.r, env); err != nil {
		return nil, err
	}
	return env, nil
}

// Close closes the underlying connection.
func (c *Conn) Close() error {
	if c.done.Swap(true) {
		return nil
	}
	return c.c.Close()
}

// RemoteError is a Response with ok=false.
type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string { return "guestd: " + e.Code + ": " + e.Message }

// Client multiplexes requests over one Conn and surfaces notifications.
type Client struct {
	conn    *Conn
	mu      sync.Mutex
	pending map[string]chan *guestdv1.Response
	seq     atomic.Uint64
	notify  chan *guestdv1.Notify
	done    chan struct{}
	err     error
	prefix  string
	dropped atomic.Uint64
	// streams holds the progress handler of each in-flight call that
	// takes Notify frames under its own request_id (ApplyMenu, DECISIONS
	// I-1460); guarded by mu.
	streams map[string]func(*guestdv1.Notify)
}

// NewClient starts the read loop on c. Notifications the caller does not
// drain are dropped once notifyBuf are queued; Dropped reports how many.
func NewClient(c net.Conn, notifyBuf int) *Client {
	cl := &Client{
		conn:    NewConn(c),
		pending: map[string]chan *guestdv1.Response{},
		notify:  make(chan *guestdv1.Notify, notifyBuf),
		done:    make(chan struct{}),
		prefix:  fmt.Sprintf("%p-", c),
	}
	go cl.readLoop()
	return cl
}

func (cl *Client) readLoop() {
	defer close(cl.notify)
	for {
		env, err := cl.conn.Read()
		if err != nil {
			// Done closes before any waiting call is answered, so a caller
			// that gets this failure as its error and then asks Done sees
			// the connection gone. The other order let a wake read a dead
			// session as live between the two (DECISIONS I-1264).
			cl.mu.Lock()
			cl.err = err
			close(cl.done)
			for id, ch := range cl.pending {
				close(ch)
				delete(cl.pending, id)
			}
			cl.mu.Unlock()
			_ = cl.conn.Close() // read side failed; nothing more to flush, the error is cl.err
			return
		}
		switch b := env.Body.(type) {
		case *guestdv1.Envelope_Response:
			cl.mu.Lock()
			ch, ok := cl.pending[env.RequestId]
			if ok {
				delete(cl.pending, env.RequestId)
			}
			cl.mu.Unlock()
			if ok {
				ch <- b.Response
				close(ch)
			}
		case *guestdv1.Envelope_Notify:
			if env.RequestId != "" {
				// A Notify that names a request belongs to that call's
				// progress, never to the guest's notification stream; one
				// for a call this client does not know (it ended, or was
				// never made) is dropped, as vsock-guestd.md says.
				cl.mu.Lock()
				h := cl.streams[env.RequestId]
				cl.mu.Unlock()
				if h != nil {
					h(b.Notify)
				}
				continue
			}
			select {
			case cl.notify <- b.Notify:
			default:
				cl.dropped.Add(1)
			}
		}
	}
}

// Call sends req and waits for its response or ctx.
func (cl *Client) Call(ctx context.Context, req *guestdv1.Request) (*guestdv1.Response, error) {
	return cl.CallStream(ctx, req, nil)
}

// CallStream is Call for a request whose progress arrives as Notify
// frames carrying its request_id before the Response (ApplyMenu,
// DECISIONS I-1460): each one is handed to progress, on the read loop's
// goroutine and in order, until the Response arrives. progress must not
// block. A nil progress is Call.
func (cl *Client) CallStream(ctx context.Context, req *guestdv1.Request, progress func(*guestdv1.Notify)) (*guestdv1.Response, error) {
	id := cl.prefix + fmt.Sprint(cl.seq.Add(1))
	ch := make(chan *guestdv1.Response, 1)
	cl.mu.Lock()
	if cl.err != nil {
		cl.mu.Unlock()
		return nil, fmt.Errorf("vsockrpc: %w", cl.err)
	}
	cl.pending[id] = ch
	if progress != nil {
		if cl.streams == nil {
			cl.streams = map[string]func(*guestdv1.Notify){}
		}
		cl.streams[id] = progress
		defer func() {
			cl.mu.Lock()
			delete(cl.streams, id)
			cl.mu.Unlock()
		}()
	}
	cl.mu.Unlock()
	env := &guestdv1.Envelope{RequestId: id, Body: &guestdv1.Envelope_Request{Request: req}}
	if err := cl.conn.Write(env); err != nil {
		cl.mu.Lock()
		delete(cl.pending, id)
		cl.mu.Unlock()
		return nil, err
	}
	select {
	case <-ctx.Done():
		cl.mu.Lock()
		delete(cl.pending, id)
		cl.mu.Unlock()
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			cl.mu.Lock()
			err := cl.err
			cl.mu.Unlock()
			if err == nil {
				err = ErrClosed
			}
			return nil, fmt.Errorf("vsockrpc: %w", err)
		}
		if !resp.Ok {
			code, msg := "internal", "no error detail"
			if resp.Error != nil {
				code, msg = resp.Error.Code, resp.Error.Message
			}
			return resp, &RemoteError{Code: code, Message: msg}
		}
		return resp, nil
	}
}

// Notifications yields Notify messages; closed when the connection ends.
func (cl *Client) Notifications() <-chan *guestdv1.Notify { return cl.notify }

// Done is closed when the read loop exits.
func (cl *Client) Done() <-chan struct{} { return cl.done }

// Err is the error that ended the connection, if any.
func (cl *Client) Err() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.err
}

// Dropped is the count of notifications dropped for lack of a reader.
func (cl *Client) Dropped() uint64 { return cl.dropped.Load() }

// Close closes the connection and ends the read loop.
func (cl *Client) Close() error { return cl.conn.Close() }

// Handler serves one request. It runs in its own goroutine.
type Handler interface {
	Handle(ctx context.Context, req *guestdv1.Request) *guestdv1.Response
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, req *guestdv1.Request) *guestdv1.Response

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, req *guestdv1.Request) *guestdv1.Response {
	return f(ctx, req)
}

// ServerConn is the guest side of one connection: it dispatches requests to
// a Handler and lets the owner push notifications.
type ServerConn struct {
	conn *Conn
	done chan struct{}
	err  error
	mu   sync.Mutex
}

// Serve starts dispatching requests from c to h until the connection ends
// or ctx is cancelled.
func Serve(ctx context.Context, c net.Conn, h Handler) *ServerConn {
	s := &ServerConn{conn: NewConn(c), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			<-ctx.Done()
			_ = s.conn.Close() // unblocks Read; the loop records ctx.Err
		}()
		for {
			env, err := s.conn.Read()
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
			req, ok := env.Body.(*guestdv1.Envelope_Request)
			if !ok {
				continue
			}
			go func(id string, r *guestdv1.Request) {
				resp := h.Handle(context.WithValue(ctx, requestIDKey{}, id), r)
				if resp == nil {
					resp = &guestdv1.Response{Ok: false, Error: &guestdv1.Error{Code: "internal", Message: "handler returned nothing"}}
				}
				_ = s.conn.Write(&guestdv1.Envelope{RequestId: id, Body: &guestdv1.Envelope_Response{Response: resp}}) // a failed reply means the peer is gone; Read sees it next
			}(env.RequestId, req.Request)
		}
	}()
	return s
}

// Notify pushes an unsolicited message to the host side.
func (s *ServerConn) Notify(n *guestdv1.Notify) error {
	return s.conn.Write(&guestdv1.Envelope{Body: &guestdv1.Envelope_Notify{Notify: n}})
}

// NotifyFor pushes a progress Notify of the request named id (an
// ApplyMenu's lines, DECISIONS I-1460): the one Notify that carries a
// request_id.
func (s *ServerConn) NotifyFor(id string, n *guestdv1.Notify) error {
	return s.conn.Write(&guestdv1.Envelope{RequestId: id, Body: &guestdv1.Envelope_Notify{Notify: n}})
}

type requestIDKey struct{}

// RequestID is the request_id of the request a Handler is serving, for a
// handler that sends progress with NotifyFor.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Done is closed when the connection ends.
func (s *ServerConn) Done() <-chan struct{} { return s.done }

// Err is the error that ended the connection.
func (s *ServerConn) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close ends the connection.
func (s *ServerConn) Close() error { return s.conn.Close() }
