package gateway

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/heracraft/repose/internal/gateway/webhook"
)

// DialGuestPort opens a stream to 127.0.0.1:<port> inside a project's
// guest, over the gateway's own SSH connection to that guest's sshd and
// a direct-tcpip channel (DECISIONS I-1110). It is what `repose open`
// does, with the gateway as the client: no new inbound port on the
// guest, and no change to the host's nftables.
//
// A delivery is not a gateway session: nothing is posted to
// /internal/sessions, so a run of deliveries never holds a machine awake
// past the session window (amends I-818, I-822).
func (g *Gateway) DialGuestPort(ctx context.Context, projectID, login, guestIP string, port int) (net.Conn, error) {
	client, err := g.webhookClient(ctx, projectID, login, guestIP)
	if err != nil {
		return nil, err
	}
	conn, err := dialLoopback(client, port)
	if err == nil {
		return conn, nil
	}
	// A connection the guest closed while it was idle fails here and
	// not at the handshake, so one retry on a fresh connection is the
	// difference between a delivery and a 502.
	g.webhookConns.drop(projectID, client)
	client, err2 := g.webhookClient(ctx, projectID, login, guestIP)
	if err2 != nil {
		return nil, err
	}
	return dialLoopback(client, port)
}

// dialLoopback opens the direct-tcpip channel, on 127.0.0.1 and then on
// ::1 for an app that listens only there.
func dialLoopback(client *ssh.Client, port int) (net.Conn, error) {
	conn, err := client.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err == nil {
		return conn, nil
	}
	conn6, err6 := client.Dial("tcp", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err6 == nil {
		return conn6, nil
	}
	return nil, err
}

// webhookClient is the project's SSH connection, reused while it has
// been used inside webhook.IdleReuse and opened otherwise. A provider
// that sends a burst pays one handshake; one that sends an event an
// hour pays one each time.
func (g *Gateway) webhookClient(ctx context.Context, projectID, login, guestIP string) (*ssh.Client, error) {
	if c := g.webhookConns.get(projectID); c != nil {
		return c, nil
	}
	client, err := g.dialGuestSSH(ctx, projectID, login, guestIP)
	if err != nil {
		return nil, err
	}
	g.webhookConns.put(projectID, client)
	return client, nil
}

// dialGuestSSH is the handshake: a five-minute gateway certificate for
// the project, the guest's host certificate checked against the Host CA,
// and the user `dev`, as a relay's dial does.
func (g *Gateway) dialGuestSSH(ctx context.Context, projectID, login, guestIP string) (*ssh.Client, error) {
	signer, err := g.certs.Signer(ctx, g.cfg.API, g.cfg.GatewayKey, projectID)
	if err != nil {
		return nil, fmt.Errorf("gateway certificate: %w", err)
	}
	_, hostCA, err := g.cas.Keys()
	if err != nil {
		return nil, err
	}
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool { return keysEqual(auth, hostCA) },
		Clock:           g.cfg.Clock,
	}
	dctx, cancel := context.WithTimeout(ctx, g.cfg.DialTimeout)
	defer cancel()
	nc, err := g.cfg.Dial(dctx, "tcp", net.JoinHostPort(guestIP, strconv.Itoa(g.cfg.GuestPort)))
	if err != nil {
		return nil, err
	}
	// The handshake gets what is left of the dial budget; the timer
	// closes the socket only while it is still running, as the relay's
	// does.
	var mu sync.Mutex
	done := false
	timer := time.AfterFunc(time.Until(dialDeadline(dctx)), func() {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			_ = nc.Close()
		}
	})
	// The address here is what CheckHostKey takes the expected
	// principal from: the login name the api sent with the route, not
	// the guest's address, as a relay's dial does. A delivery has no
	// login of its own, which is why /internal/route carries one.
	c, chans, reqs, err := ssh.NewClientConn(nc, login+":"+strconv.Itoa(g.cfg.GuestPort), &ssh.ClientConfig{
		User:            "dev",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: checker.CheckHostKey,
		Timeout:         g.cfg.DialTimeout,
	})
	timer.Stop()
	mu.Lock()
	done = true
	mu.Unlock()
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

var _ webhook.Dialer = (*Gateway)(nil)

// webhookConnCache holds one SSH connection per project for
// webhook.IdleReuse after its last use.
type webhookConnCache struct {
	mu      sync.Mutex
	entries map[string]*webhookConn
	clock   func() time.Time
}

type webhookConn struct {
	client *ssh.Client
	last   time.Time
}

func newWebhookConnCache(clock func() time.Time) *webhookConnCache {
	if clock == nil {
		clock = time.Now
	}
	return &webhookConnCache{entries: map[string]*webhookConn{}, clock: clock}
}

func (c *webhookConnCache) get(projectID string) *ssh.Client {
	if c == nil {
		return nil
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[projectID]
	if !ok {
		return nil
	}
	if now.Sub(e.last) > webhook.IdleReuse {
		delete(c.entries, projectID)
		go func() { _ = e.client.Close() }()
		return nil
	}
	e.last = now
	return e.client
}

func (c *webhookConnCache) put(projectID string, client *ssh.Client) {
	if c == nil {
		_ = client.Close()
		return
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[projectID]; ok && old.client != client {
		go func() { _ = old.client.Close() }()
	}
	c.entries[projectID] = &webhookConn{client: client, last: now}
}

// drop closes a connection that failed and forgets it, unless another
// one has already taken its place.
func (c *webhookConnCache) drop(projectID string, client *ssh.Client) {
	if c == nil {
		return
	}
	c.mu.Lock()
	e, ok := c.entries[projectID]
	if ok && e.client == client {
		delete(c.entries, projectID)
	}
	c.mu.Unlock()
	_ = client.Close()
}

// sweep closes every connection idle longer than webhook.IdleReuse.
func (c *webhookConnCache) sweep() {
	if c == nil {
		return
	}
	now := c.clock()
	c.mu.Lock()
	var stale []*ssh.Client
	for id, e := range c.entries {
		if now.Sub(e.last) > webhook.IdleReuse {
			stale = append(stale, e.client)
			delete(c.entries, id)
		}
	}
	c.mu.Unlock()
	for _, cl := range stale {
		_ = cl.Close()
	}
}

// closeAll ends every held connection; the gateway's shutdown calls it.
func (c *webhookConnCache) closeAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	entries := c.entries
	c.entries = map[string]*webhookConn{}
	c.mu.Unlock()
	for _, e := range entries {
		_ = e.client.Close()
	}
}
