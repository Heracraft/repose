// Package parkkey is the contract between a repose host that runs inside a
// virtual machine on hardware the customer owns and the launcher that
// holds that VM (DECISIONS I-1386, I-1394): hostd asks the launcher to
// wrap and unwrap the keys of the images a park writes, and the launcher
// answers with one wrapping key it keeps where the VM cannot read it, the
// Mac's Keychain. Only two things ever cross: a fresh image key on its way
// to being wrapped, and a wrapped key on its way back. The wrapping key
// itself never enters the VM, and no secret of a tenant's ever reaches the
// launcher: an image key is random bytes hostd made a moment before.
//
// The channel is a vsock the launcher's hypervisor forwards to a unix
// socket the launcher listens on (vfkit `virtio-vsock,port=1386`); the
// package is transport-agnostic and the tests use a unix socket pair.
// One request per connection, one JSON object each way, newline-ended.
//
//	{"v":1,"op":"ready"}                                → {"v":1,"ok":true,"key_id":"..."}
//	{"v":1,"op":"wrap","key":"<base64>"}                → {"v":1,"ok":true,"wrapped":"<base64>","key_id":"..."}
//	{"v":1,"op":"unwrap","wrapped":"<base64>","key_id":"..."} → {"v":1,"ok":true,"key":"<base64>"}
//	anything that fails                                  → {"v":1,"ok":false,"error":"..."}
//
// The wrapping is AES-256-GCM under the launcher's 256-bit key, the nonce
// in front of the ciphertext, with the key id as additional data, so a
// record from one wrapping key cannot be unwrapped by another. The key id
// is a hash of the wrapping key, which is how hostd and the launcher
// notice the item in the Keychain was replaced: the unwrap says so instead
// of returning garbage that the image's key check would catch later.
package parkkey

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Port is the vsock port the host dials on the launcher's CID (2, the
// hypervisor's host). 1386 after the decision that put the key there.
const Port = 1386

// WrappingKeyBytes is the size of the launcher's key: AES-256.
const WrappingKeyBytes = 32

// Version is the protocol version both ends send.
const Version = 1

// maxLine bounds one message: an image key is 64 bytes, a wrapped one 92,
// base64 and the JSON around them well under this.
const maxLine = 4096

// Request is what the host sends.
type Request struct {
	V       int    `json:"v"`
	Op      string `json:"op"`
	Key     []byte `json:"key,omitempty"`
	Wrapped []byte `json:"wrapped,omitempty"`
	KeyID   string `json:"key_id,omitempty"`
}

// Response is what the launcher answers.
type Response struct {
	V       int    `json:"v"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Key     []byte `json:"key,omitempty"`
	Wrapped []byte `json:"wrapped,omitempty"`
	KeyID   string `json:"key_id,omitempty"`
}

// Keeper is where the launcher gets the wrapping key from: the Keychain
// on a Mac, memory in tests. It returns the same WrappingKeyBytes every
// time until the item is replaced, and creates the key the first time it
// is asked.
type Keeper interface {
	WrappingKey(ctx context.Context) ([]byte, error)
}

// MemoryKeeper is a Keeper for tests: one key, made on first use.
type MemoryKeeper struct {
	mu  sync.Mutex
	key []byte
	// Err, when set, is what WrappingKey returns (a locked keychain).
	Err error
}

// WrappingKey implements Keeper.
func (m *MemoryKeeper) WrappingKey(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	if m.key == nil {
		k, err := NewWrappingKey()
		if err != nil {
			return nil, err
		}
		m.key = k
	}
	return append([]byte(nil), m.key...), nil
}

// Replace puts a new key in, as a user deleting the Keychain item would.
func (m *MemoryKeeper) Replace() {
	m.mu.Lock()
	m.key = nil
	m.mu.Unlock()
}

// NewWrappingKey returns a random wrapping key.
func NewWrappingKey() ([]byte, error) {
	k := make([]byte, WrappingKeyBytes)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// KeyID names a wrapping key without revealing it: `keychain-` and the
// first 16 hex digits of a salted hash. What a park record carries.
func KeyID(wk []byte) string {
	h := sha256.New()
	h.Write([]byte("repose-park-wrapping-key-id\x00"))
	h.Write(wk)
	return "keychain-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// Wrap seals key under wk: nonce, then AES-256-GCM ciphertext bound to
// the key id.
func Wrap(wk, key []byte) (wrapped []byte, keyID string, err error) {
	if len(wk) != WrappingKeyBytes {
		return nil, "", fmt.Errorf("wrapping key of %d bytes, want %d", len(wk), WrappingKeyBytes)
	}
	if len(key) == 0 {
		return nil, "", errors.New("no key to wrap")
	}
	blk, err := aes.NewCipher(wk)
	if err != nil {
		return nil, "", err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", err
	}
	id := KeyID(wk)
	out := g.Seal(nonce, nonce, key, []byte(id))
	return out, id, nil
}

// ErrWrongKey is the unwrap of a record wrapped under another key: the
// Keychain item was deleted and made again, or the record is from another
// Mac. The image cannot be read and the guest starts cold.
var ErrWrongKey = errors.New("the image was wrapped under a key this Mac no longer has")

// Unwrap opens what Wrap sealed, under the key whose id matches.
func Unwrap(wk, wrapped []byte, keyID string) ([]byte, error) {
	if len(wk) != WrappingKeyBytes {
		return nil, fmt.Errorf("wrapping key of %d bytes, want %d", len(wk), WrappingKeyBytes)
	}
	if keyID != KeyID(wk) {
		return nil, ErrWrongKey
	}
	blk, err := aes.NewCipher(wk)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < g.NonceSize()+g.Overhead() {
		return nil, errors.New("wrapped key too short")
	}
	key, err := g.Open(nil, wrapped[:g.NonceSize()], wrapped[g.NonceSize():], []byte(keyID))
	if err != nil {
		return nil, errors.New("wrapped key does not open")
	}
	return key, nil
}

// Logger receives the launcher's lines about the service: events, ids
// and error texts, never a key.
type Logger func(format string, args ...any)

// Serve answers requests on l until ctx ends or l closes. Each
// connection carries one request and gets one response; a request that
// is not JSON, not version 1 or over maxLine is answered with an error
// and the connection closed.
func Serve(ctx context.Context, l net.Listener, k Keeper, log Logger) {
	if log == nil {
		log = func(string, ...any) {}
	}
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			log("park-key: event=listener_closed err=%v", err)
			return
		}
		go serveOne(ctx, c, k, log)
	}
}

func serveOne(ctx context.Context, c net.Conn, k Keeper, log Logger) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	resp := handle(ctx, bufio.NewReaderSize(c, maxLine), k, log)
	b, _ := json.Marshal(resp)
	_, _ = c.Write(append(b, '\n'))
}

func handle(ctx context.Context, r *bufio.Reader, k Keeper, log Logger) Response {
	line, err := r.ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, bufio.ErrBufferFull) {
			return fail("request over %d bytes", maxLine)
		}
		return fail("request not read: %v", err)
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return fail("request is not JSON")
	}
	if req.V != Version {
		return fail("protocol version %d, want %d", req.V, Version)
	}
	wk, err := k.WrappingKey(ctx)
	if err != nil {
		log("park-key: event=key_unavailable op=%s err=%v", req.Op, err)
		return fail("wrapping key unavailable: %v", err)
	}
	switch req.Op {
	case "ready":
		return Response{V: Version, OK: true, KeyID: KeyID(wk)}
	case "wrap":
		w, id, err := Wrap(wk, req.Key)
		if err != nil {
			return fail("wrap: %v", err)
		}
		log("park-key: event=wrapped key_id=%s", id)
		return Response{V: Version, OK: true, Wrapped: w, KeyID: id}
	case "unwrap":
		key, err := Unwrap(wk, req.Wrapped, req.KeyID)
		if err != nil {
			log("park-key: event=unwrap_failed key_id=%s err=%v", req.KeyID, err)
			return fail("unwrap: %v", err)
		}
		log("park-key: event=unwrapped key_id=%s", req.KeyID)
		return Response{V: Version, OK: true, Key: key}
	default:
		return fail("unknown op %q", req.Op)
	}
}

func fail(format string, args ...any) Response {
	return Response{V: Version, OK: false, Error: fmt.Sprintf(format, args...)}
}

// Dialer opens a connection to the launcher.
type Dialer func(ctx context.Context) (net.Conn, error)

// UnixDialer dials a unix socket at path; the tests' transport.
func UnixDialer(path string) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
}

// Client is hostd's side: a park.Sealer that asks the launcher. Wrap
// takes no context (the Sealer contract) and is bounded by RequestTimeout.
type Client struct {
	Dial Dialer
	// RequestTimeout bounds one exchange when the caller gives no
	// deadline. 0 is 15 s.
	RequestTimeout time.Duration

	mu    sync.Mutex
	keyID string // the launcher's current key id, from the last ready
}

// NewClient returns a Client over d.
func NewClient(d Dialer) *Client { return &Client{Dial: d} }

func (c *Client) timeout() time.Duration {
	if c.RequestTimeout > 0 {
		return c.RequestTimeout
	}
	return 15 * time.Second
}

func (c *Client) do(ctx context.Context, req Request) (Response, error) {
	req.V = Version
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	conn, err := c.Dial(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("launcher: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return Response{}, fmt.Errorf("launcher: %w", err)
	}
	line, err := bufio.NewReaderSize(conn, maxLine).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Response{}, fmt.Errorf("launcher: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, errors.New("launcher: answer is not JSON")
	}
	if resp.V != Version {
		return Response{}, fmt.Errorf("launcher: protocol version %d, want %d", resp.V, Version)
	}
	if !resp.OK {
		return Response{}, fmt.Errorf("launcher: %s", resp.Error)
	}
	return resp, nil
}

// Refresh implements park.Sealer: asks the launcher whether its key is
// there. The park's key loop calls it at start and hourly, so the
// appliance's journal says when the Keychain stops answering.
func (c *Client) Refresh(ctx context.Context) error {
	resp, err := c.do(ctx, Request{Op: "ready"})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.keyID = resp.KeyID
	c.mu.Unlock()
	return nil
}

// Ready implements park.Sealer. A park at shutdown asks it once before
// snapshotting anything: it is a live question to the launcher, because
// a key the loop saw an hour ago may be behind a keychain locked since.
func (c *Client) Ready() error { return c.Refresh(context.Background()) }

// Wrap implements park.Sealer.
func (c *Client) Wrap(key []byte) ([]byte, string, error) {
	resp, err := c.do(context.Background(), Request{Op: "wrap", Key: key})
	if err != nil {
		return nil, "", err
	}
	if len(resp.Wrapped) == 0 || resp.KeyID == "" {
		return nil, "", errors.New("launcher: wrap answered without a wrapped key")
	}
	return resp.Wrapped, resp.KeyID, nil
}

// Unwrap implements park.Sealer.
func (c *Client) Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error) {
	resp, err := c.do(ctx, Request{Op: "unwrap", Wrapped: wrapped, KeyID: keyID})
	if err != nil {
		return nil, err
	}
	if len(resp.Key) == 0 {
		return nil, errors.New("launcher: unwrap answered without a key")
	}
	return resp.Key, nil
}
