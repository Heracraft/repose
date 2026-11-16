// Package edge is the api's client for the one leg where the api dials
// the edge: the browser view's relay (DECISIONS I-1216). Every other
// api/edge call in the tree runs the other way, with the gateway as the
// client of /internal, which is why this one is pinned at both ends --
// the api presents a client certificate named `api` from its own CA, and
// the relay admits nothing else.
package edge

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// DialTimeout bounds the whole exchange: the TLS dial, the request line
// and the relay's answer. The relay's own handshake budget for the guest
// is inside it (10 s there, plus its dial), so this is longer than the
// api would wait for anything a user is not already watching.
const DialTimeout = 25 * time.Second

// replyMax bounds the relay's answer line.
const replyMax = 4 << 10

// Client dials the edge's view relay.
type Client struct {
	addr string
	tls  *tls.Config
}

// New builds the client, or (nil, nil) when addr is empty: the feature is
// then off and the mint route answers `unavailable` rather than the api
// failing to start. roots verifies the relay's server certificate, which
// the api's own CA issued (`repose-admin ca sign-server --name
// 10.255.0.1`, docs/ops/RUNBOOK.md "Edge"), so it is the api's CA pool.
func New(addr string, cert tls.Certificate, roots *x509.CertPool) (*Client, error) {
	if addr == "" {
		return nil, nil
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("VIEW_RELAY_ADDR %q is not host:port: %w", addr, err)
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("edge: the api's client certificate is required")
	}
	if roots == nil {
		return nil, errors.New("edge: a root pool for the relay's certificate is required")
	}
	return &Client{addr: addr, tls: &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		MinVersion:   tls.VersionTLS13,
	}}, nil
}

// Refused is a refusal code from the relay's closed set (view_busy,
// view_capacity, view_dial_failed, view_desktop_failed, view_auth_failed,
// view_bad_request). The api maps it to the user's words; the code itself
// is a word a log line may carry.
type Refused struct{ Code string }

func (e *Refused) Error() string { return "edge: the view relay refused: " + e.Code }

// Code returns the refusal code of err, or "" when err is not a refusal.
func Code(err error) string {
	var r *Refused
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// Dial opens one view leg and returns it positioned at the first RFB byte
// the page should see. Every argument comes from the ticket's row: the
// relay checks them again and the guest's host certificate is checked
// against the login, never against the address.
func (c *Client) Dial(ctx context.Context, projectID, login, guestIP string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	d := &tls.Dialer{Config: c.tls}
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("edge: dial the view relay: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	if deadline, has := ctx.Deadline(); has {
		_ = conn.SetDeadline(deadline)
	}
	req, err := json.Marshal(struct {
		ProjectID string `json:"project_id"`
		Login     string `json:"login"`
		GuestIP   string `json:"guest_ip"`
	}{projectID, login, guestIP})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("edge: send the view request: %w", err)
	}
	line, err := readLine(conn, replyMax)
	if err != nil {
		return nil, fmt.Errorf("edge: read the relay's answer: %w", err)
	}
	var reply struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		return nil, fmt.Errorf("edge: the relay's answer is not json")
	}
	if !reply.OK {
		if reply.Code == "" {
			return nil, errors.New("edge: the relay refused without a code")
		}
		return nil, &Refused{Code: reply.Code}
	}
	// No deadline on the stream: the api's own idle close and session cap
	// bound it, and a frame can be minutes apart on a still desktop.
	_ = conn.SetDeadline(time.Time{})
	ok = true
	return conn, nil
}

// readLine reads one newline-terminated line of at most max bytes,
// leaving everything after the newline on the connection: the bytes after
// it are the stream.
func readLine(r io.Reader, max int) ([]byte, error) {
	line := make([]byte, 0, 128)
	var b [1]byte
	for len(line) < max {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		if b[0] == '\n' {
			return line, nil
		}
		line = append(line, b[0])
	}
	return nil, errors.New("edge: the relay's answer is over the cap")
}
