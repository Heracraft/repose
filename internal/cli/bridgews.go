package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/wsframe"
)

// The websocket framing the bridge's front reads (RFC 6455) lives in
// internal/wsframe, shared with the api's browser-view stream; just
// enough to see every CDP message between the machine's browser tools and
// the laptop's Chrome, one whole message at a time (bridgecdp.go). No
// extensions: the front removes Sec-WebSocket-Extensions from the
// upgrade, so Chrome never compresses, and a frame with a reserved bit
// set ends the connection.

const (
	wsOpCont  = wsframe.OpCont
	wsOpText  = wsframe.OpText
	wsOpClose = wsframe.OpClose
)

// wsMaxMessage bounds one message: a full-page screenshot is tens of MB.
// The view's stream passes a far smaller cap of its own, which is why the
// shared package takes one rather than holding this number.
const wsMaxMessage = 256 << 20

type wsFrame = wsframe.Frame

func readWSFrame(r *bufio.Reader) (wsFrame, error) { return wsframe.ReadFrame(r, wsMaxMessage) }

// encodeWSFrame is one final frame; masked is the client's side.
func encodeWSFrame(op byte, payload []byte, masked bool) []byte {
	return wsframe.Encode(op, payload, masked)
}

// readWSMessage reads one whole data message. Control frames, which may
// arrive between a message's fragments, go to onControl as they come.
func readWSMessage(r *bufio.Reader, onControl func(wsFrame) error) (op byte, payload, raw []byte, err error) {
	return wsframe.ReadMessage(r, wsMaxMessage, onControl)
}

// lockedWriter keeps whole messages whole when two goroutines write.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) write(b []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.w.Write(b)
	return err
}

// withoutWSExtensions drops Sec-WebSocket-Extensions from a request head,
// so the upgrade agrees to no compression.
func withoutWSExtensions(head []byte) []byte {
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(string(head), "\n") {
		if k, _, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Sec-WebSocket-Extensions") {
			continue
		}
		out.WriteString(line)
	}
	return out.Bytes()
}

// headerValue is one header of a request or response head.
func headerValue(head []byte, name string) string {
	for _, line := range strings.Split(string(head), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// readResponseHead reads a response's status line and headers.
func readResponseHead(br *bufio.Reader) (head []byte, status int, err error) {
	var buf bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, 0, err
		}
		buf.WriteString(line)
		if buf.Len() > 64<<10 {
			return nil, 0, errors.New("response head over 64 KiB")
		}
		if status == 0 {
			f := strings.Fields(line)
			if len(f) < 2 {
				return nil, 0, fmt.Errorf("bad status line %q", strings.TrimSpace(line))
			}
			if _, err := fmt.Sscanf(f[1], "%d", &status); err != nil {
				return nil, 0, fmt.Errorf("bad status line %q", strings.TrimSpace(line))
			}
			continue
		}
		if strings.TrimRight(line, "\r\n") == "" {
			return buf.Bytes(), status, nil
		}
	}
}

// dialWS opens a websocket client connection to a DevTools server's
// path. Chrome's switch holds the upgrade until the user answers its
// dialog, so the wait is ctx's.
func dialWS(ctx context.Context, addr, path string) (net.Conn, *bufio.Reader, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()) })
	defer stop()
	var key [16]byte
	_, _ = rand.Read(key[:])
	_, err = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, addr, base64.StdEncoding.EncodeToString(key[:]))
	if err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(c)
	_, status, err := readResponseHead(br)
	if err == nil && status != 101 {
		err = fmt.Errorf("the DevTools server answered %d", status)
	}
	if err != nil {
		_ = c.Close()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, err
	}
	return c, br, nil
}
