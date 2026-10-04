package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// session is one authenticated connection and its relay to the guest.
type session struct {
	gw     *Gateway
	conn   *ssh.ServerConn
	chans  <-chan ssh.NewChannel
	reqs   <-chan *ssh.Request
	route  *Route
	slug   string
	handle string
	serial uint64
	// validTo is the certificate's expiry; zero for none.
	validTo time.Time
	// id names this relay in the session reports (I-176): random, so two
	// connections under one certificate are two sessions.
	id     string
	prefix string

	startedAt time.Time
	relays    sync.WaitGroup
	guest     ssh.Conn
	toGuest   atomic.Int64
	toClient  atomic.Int64
	log       *slog.Logger

	// cancel ends the relay; endReason says why, for the close log line.
	cancel    context.CancelFunc
	endReason atomic.Value
}

// Why a relay ended before either side closed it (I-436).
const (
	endRevoked     = "revoked"
	endCertExpired = "cert_expired"
	endSessionCap  = "session_cap"
)

// end stops the relay; the first reason given is the one logged.
func (s *session) end(reason string) {
	s.endReason.CompareAndSwap(nil, reason)
	s.cancel()
}

// Channel types relayed in each direction (06-gateway-edge.md §5.4).
// auth-agent@openssh.com is not among them: agent forwarding is refused
// outright (I-247), so nothing in a guest can ask the laptop's agent to
// sign, even behind a client that still sends ForwardAgent.
var (
	clientChannelTypes = map[string]bool{"session": true, "direct-tcpip": true}
	guestChannelTypes  = map[string]bool{"forwarded-tcpip": true}
)

// run dials the guest and relays until either side closes, the session
// cap passes, or ctx ends.
func (s *session) run(ctx context.Context) {
	g := s.gw
	s.log = g.log.With("project_id", s.route.ProjectID, "cert_serial", s.serial)
	// A relay lasts at most SessionCap, and never past the certificate it
	// was opened with (I-436): ssh multiplexes later commands over this
	// connection without authenticating again.
	limit, reason := g.cfg.SessionCap, endSessionCap
	if !s.validTo.IsZero() {
		if d := s.validTo.Sub(g.cfg.Clock()); d < limit {
			limit, reason = d, endCertExpired
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.cancel = cancel
	capTimer := time.AfterFunc(max(limit, 0), func() { s.end(reason) })
	defer capTimer.Stop()
	// Registered before the dial, then checked once: a revocation that
	// landed between authentication and here ends the relay too.
	g.addSession(s)
	defer g.removeSession(s)
	if g.revoked.IsRevoked(s.serial) {
		s.end(endRevoked)
	}

	guest, gchans, greqs, err := s.dialGuest(ctx)
	if err != nil {
		g.cfg.Metrics.DialFailTotal.Inc()
		reason, msg := dialFailure(err)
		s.log.Warn("guest dial failed", "event", "dial_fail", "reason", reason)
		s.refuse(ctx, msg)
		return
	}
	s.guest = guest
	g.cfg.Metrics.Sessions.Inc()
	g.cfg.Metrics.SessionsTotal.Inc()
	s.log.Info("session opened", "event", "session_open", "source_prefix", s.prefix)
	// Reported on a context the session's end does not cancel: a client
	// that connects and leaves within the round trip used to cancel its
	// own "opened" mid-flight, and the "closed" below waits for this so the
	// api never sees a close before its open (I-123).
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		s.report(context.WithoutCancel(ctx), true)
	}()

	var wg sync.WaitGroup
	done := func() { cancel() }
	wg.Add(4)
	go func() {
		defer wg.Done()
		for nc := range s.chans {
			s.clientChannel(ctx, nc)
		}
		done()
	}()
	go func() {
		defer wg.Done()
		for nc := range gchans {
			s.guestChannel(ctx, nc)
		}
		done()
	}()
	go func() {
		defer wg.Done()
		for req := range s.reqs {
			s.forwardGlobal(req, s.guest)
		}
		done()
	}()
	go func() {
		defer wg.Done()
		for req := range greqs {
			s.guestGlobal(req)
		}
		done()
	}()
	go s.keepalive(ctx, cancel)

	<-ctx.Done()
	// Close the guest first: that ends every relay's source, so the pipes
	// finish forwarding the last of the guest's output and close their
	// client channels cleanly. Only then tear down the client transport, so
	// buffered output is not discarded by an abrupt transport close.
	_ = s.guest.Close()
	drained := make(chan struct{})
	go func() { s.relays.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
	}
	_ = s.conn.Close()
	wg.Wait()
	s.relays.Wait()

	g.cfg.Metrics.Sessions.Dec()
	dur := g.cfg.Clock().Sub(s.startedAt)
	g.cfg.Metrics.SessionSeconds.Observe(dur.Seconds())
	ended, _ := s.endReason.Load().(string)
	if ended == "" {
		ended = "closed"
	}
	s.log.Info("session closed", "event", "session_close", "source_prefix", s.prefix, "reason", ended,
		"duration_ms", dur.Milliseconds(), "bytes_to_guest", s.toGuest.Load(), "bytes_to_client", s.toClient.Load())
	go func() {
		<-opened
		s.report(context.WithoutCancel(ctx), false)
	}()
}

// dialGuest opens the SSH client connection to guest_ip:22 with a
// gateway-issued certificate and the guest's host certificate checked
// against the Host CA for principal <slug>.<handle> (ssh-gateway.md,
// DECISIONS I-42).
func (s *session) dialGuest(ctx context.Context) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	g := s.gw
	signer, err := g.certs.Signer(ctx, g.cfg.API, g.cfg.GatewayKey, s.route.ProjectID)
	if err != nil {
		return nil, nil, nil, &dialError{reason: "cert", err: err}
	}
	_, hostCA, err := g.cas.Keys()
	if err != nil {
		return nil, nil, nil, &dialError{reason: "ca", err: err}
	}
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool { return keysEqual(auth, hostCA) },
		Clock:           g.cfg.Clock,
	}
	dctx, cancel := context.WithTimeout(ctx, g.cfg.DialTimeout)
	defer cancel()
	nc, err := g.cfg.Dial(dctx, "tcp", net.JoinHostPort(s.route.GuestIP, strconv.Itoa(g.cfg.GuestPort)))
	if err != nil {
		return nil, nil, nil, &dialError{reason: "tcp", err: err}
	}
	// The handshake gets what is left of DialTimeout. Not a socket
	// deadline: that had to be cleared after the handshake, and a
	// handshake finishing just inside it could lose the race to its own
	// deadline, leaving the guest connection to fail its next read ("guest:
	// read tcp ...: i/o timeout"); it was also set from cfg.Clock, which
	// tests move. A timer that closes the socket only while the handshake
	// is still running has no such window.
	var hsMu sync.Mutex
	hsDone, hsTimedOut := false, false
	hsTimer := time.AfterFunc(time.Until(dialDeadline(dctx)), func() {
		hsMu.Lock()
		defer hsMu.Unlock()
		if !hsDone {
			hsTimedOut = true
			_ = nc.Close() // ends the handshake's reads and writes
		}
	})
	ccfg := &ssh.ClientConfig{
		User:            "dev",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: checker.CheckHostKey,
		Timeout:         g.cfg.DialTimeout,
	}
	// The address passed here is what CheckHostKey takes the expected
	// principal from: the login name, not the guest's address.
	c, chans, reqs, err := ssh.NewClientConn(nc, s.slug+"."+s.handle+":"+strconv.Itoa(g.cfg.GuestPort), ccfg)
	hsTimer.Stop()
	hsMu.Lock()
	hsDone = true
	timedOut := hsTimedOut
	hsMu.Unlock()
	if err == nil && timedOut {
		_ = c.Close() // finished as the timer closed its socket: unusable
		err = errHandshakeTimeout
	}
	if err != nil {
		_ = nc.Close() // the handshake failed; the socket is useless
		if timedOut {
			err = errHandshakeTimeout
		}
		return nil, nil, nil, &dialError{reason: "handshake", err: err}
	}
	return c, chans, reqs, nil
}

var errHandshakeTimeout = errors.New("guest ssh handshake timed out")

// dialDeadline is the dial context's deadline (it always has one).
func dialDeadline(ctx context.Context) time.Time {
	d, ok := ctx.Deadline()
	if !ok {
		return time.Now().Add(10 * time.Second)
	}
	return d
}

type dialError struct {
	reason string
	err    error
}

func (e *dialError) Error() string { return e.reason + ": " + e.err.Error() }
func (e *dialError) Unwrap() error { return e.err }

// dialFailure maps a dial error to the log reason and the user's message
// (§5.3, §6). Addresses never reach either.
func dialFailure(err error) (reason, msg string) {
	var de *dialError
	if !errors.As(err, &de) {
		return "unknown", MsgNotReady
	}
	switch de.reason {
	case "cert", "ca":
		return de.reason, MsgControlPlane
	case "tcp":
		switch {
		case errors.Is(de.err, syscall.EHOSTUNREACH), errors.Is(de.err, syscall.ENETUNREACH):
			return "no_route", "cannot reach environment: no route to host"
		case errors.Is(de.err, syscall.ECONNREFUSED):
			return "refused", MsgNotReady
		}
		var ne net.Error
		if errors.As(de.err, &ne) && ne.Timeout() {
			return "timeout", MsgNotReady
		}
		return "tcp", MsgNotReady
	default:
		return "handshake", MsgNotReady
	}
}

// refuse answers each session the client opens with msg on stderr and exit
// status 255, so an interactive user or a scripted CLI sees why the guest
// could not be reached. It keeps the connection until the client hangs up
// (so the exit status is delivered), a grace period passes, or ctx ends;
// closing the transport the instant the channel closes would cut delivery
// off and the client would see only EOF.
func (s *session) refuse(ctx context.Context, msg string) {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	defer func() { _ = s.conn.Close() }()
	go func() {
		for req := range s.reqs {
			if req.WantReply {
				_ = req.Reply(false, nil) // no global request is honoured on a refused connection
			}
		}
	}()
	for {
		select {
		case nc, ok := <-s.chans:
			if !ok {
				return // the client hung up
			}
			if nc.ChannelType() != "session" {
				s.reject(nc, ssh.ConnectionFailed, msg)
				continue
			}
			ch, reqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go s.refuseSession(ch, reqs, msg)
		case <-deadline.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// refuseSession delivers the message and a 255 exit on one session channel.
func (s *session) refuseSession(ch ssh.Channel, reqs <-chan *ssh.Request, msg string) {
	// Reply to the client's requests (exec, pty-req, shell) so it proceeds
	// to read output, then send the message once the command request has
	// been seen.
	sent := false
	deliver := func() {
		if sent {
			return
		}
		sent = true
		_, _ = fmt.Fprintf(ch.Stderr(), "%s\r\n", msg) // best effort: the peer may be gone
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{255}))
		_ = ch.Close()
	}
	for req := range reqs {
		switch req.Type {
		case "exec", "shell", "subsystem":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			deliver()
		default:
			if req.WantReply {
				_ = req.Reply(true, nil) // pty-req, env, window-change: accepted so the message shows on a pty
			}
		}
	}
	deliver() // a client that opened a session without a command still gets the message
}

func (s *session) reject(nc ssh.NewChannel, reason ssh.RejectionReason, msg string) {
	if err := nc.Reject(reason, msg); err != nil {
		s.log.Debug("channel reject failed", "event", "relay", "err", err.Error())
	}
}

// clientChannel relays a channel the client opened to the guest.
func (s *session) clientChannel(ctx context.Context, nc ssh.NewChannel) {
	if !clientChannelTypes[nc.ChannelType()] {
		s.reject(nc, ssh.UnknownChannelType, "channel type not relayed by the gateway")
		return
	}
	gch, greqs, err := s.guest.OpenChannel(nc.ChannelType(), nc.ExtraData())
	if err != nil {
		var oe *ssh.OpenChannelError
		if errors.As(err, &oe) {
			s.reject(nc, oe.Reason, oe.Message)
		} else {
			s.reject(nc, ssh.ConnectionFailed, "guest: "+err.Error())
		}
		return
	}
	cch, creqs, err := nc.Accept()
	if err != nil {
		_ = gch.Close() // the client withdrew; drop the guest half
		return
	}
	isSession := nc.ChannelType() == "session"
	s.relays.Add(1)
	go func() {
		defer s.relays.Done()
		s.pipe(cch, creqs, gch, greqs, isSession)
	}()
}

// guestChannel relays a channel the guest opened (a remote forward) back
// to the client. An agent channel is rejected (I-247).
func (s *session) guestChannel(ctx context.Context, nc ssh.NewChannel) {
	if !guestChannelTypes[nc.ChannelType()] {
		s.reject(nc, ssh.Prohibited, "channel type not relayed by the gateway")
		return
	}
	cch, creqs, err := s.conn.OpenChannel(nc.ChannelType(), nc.ExtraData())
	if err != nil {
		var oe *ssh.OpenChannelError
		if errors.As(err, &oe) {
			s.reject(nc, oe.Reason, oe.Message)
		} else {
			s.reject(nc, ssh.ConnectionFailed, "client: "+err.Error())
		}
		return
	}
	gch, greqs, err := nc.Accept()
	if err != nil {
		_ = cch.Close() // the guest withdrew; drop the client half
		return
	}
	s.relays.Add(1)
	go func() {
		defer s.relays.Done()
		s.pipe(cch, creqs, gch, greqs, false)
	}()
}

// pipe relays data, extended data (stderr) and channel requests both ways
// between a client channel and a guest channel until both are torn down.
//
// The ordering that matters, learned from the SSH channel lifecycle:
//   - A program like `cat` needs stdin EOF while it may still be writing, so
//     the half-close towards a side is sent as soon as that side's inbound
//     data ends (CloseWrite), not at full close.
//   - An OpenSSH-style client surfaces exit-status only when the channel's
//     request stream closes, which happens on CHANNEL_CLOSE. So each
//     destination is fully closed as soon as its *source* channel is drained
//     (data ended and its request stream ended), rather than waiting for the
//     far side to close first, which would deadlock: the client will not
//     close until it has the exit status, and the exit status does not
//     surface until the gateway closes the channel to it.
func (s *session) pipe(cch ssh.Channel, creqs <-chan *ssh.Request, gch ssh.Channel, greqs <-chan *ssh.Request, isSession bool) {
	var wg sync.WaitGroup

	var c2gData, g2cData sync.WaitGroup
	var creqsDone, greqsDone sync.WaitGroup
	c2gData.Add(2)
	g2cData.Add(2)
	creqsDone.Add(1)
	greqsDone.Add(1)
	// exited closes once a session's exit-status or exit-signal has been
	// relayed to the client, or the guest's request stream has ended.
	exited := make(chan struct{})
	var exitOnce sync.Once
	markExited := func() { exitOnce.Do(func() { close(exited) }) }

	copyStream := func(group *sync.WaitGroup, dst io.Writer, src io.Reader, counter *atomic.Int64) {
		defer wg.Done()
		defer group.Done()
		n, _ := io.Copy(dst, src) // ends on EOF or when the channel is closed
		counter.Add(n)
	}
	wg.Add(4)
	go copyStream(&c2gData, gch, cch, &s.toGuest)
	go copyStream(&c2gData, gch.Stderr(), cch.Stderr(), &s.toGuest)
	go copyStream(&g2cData, cch, gch, &s.toClient)
	go copyStream(&g2cData, cch.Stderr(), gch.Stderr(), &s.toClient)

	// The client's inbound data (stdin) is done: let the guest see EOF while
	// it may still be producing output. A client sends no extended data, so
	// waiting for both never blocks a normal client.
	go func() {
		c2gData.Wait()
		_ = gch.CloseWrite() // an error means the guest already closed
	}()
	// The same half-close towards the client. For a session this is what
	// sshd does after the program's stdout ends; for a channel the guest
	// opened (the agent) it is the only way the client learns the guest's
	// side is done: OpenSSH's client keeps its agent socket open until it
	// sees EOF, sends its own EOF only then, and sshd sends CHANNEL_CLOSE
	// only after both directions are closed. Without this the agent channel
	// stayed half-open and an exec with -A never returned (DECISIONS I-110).
	//
	// For a session, the EOF waits for the exit status (DECISIONS I-212).
	// sshd sends EOF when the program's output drains and the exit status
	// when it reaps the child, a moment later; an OpenSSH client whose stdin
	// is already closed answers EOF with CHANNEL_CLOSE at once, and after
	// that nothing more reaches it. Relayed from two goroutines, the EOF
	// often won and the client reported 255. Exit status before EOF is
	// valid SSH (sshd itself sends it that way when the child is reaped
	// first).
	go func() {
		g2cData.Wait()
		if isSession {
			<-exited
		}
		_ = cch.CloseWrite() // an error means the client already closed
	}()

	// Closing cch (a full CHANNEL_CLOSE) is what surfaces exit-status to the
	// client. It must come after two things beyond the guest being done:
	//   - all guest->client data is flushed (g2cData), and
	//   - no reply to a client channel request is still in flight on cch.
	// The reply to a client's exec travels on cch; a fast guest can finish
	// and close before that reply is delivered, and closing cch first fails
	// the client's request with EOF and discards the buffered output. The
	// mutex below hands the close to whichever of the two finishes last.
	var mu sync.Mutex
	inflight := 0
	guestDone := false
	closed := false
	tryClose := func() { // mu held
		if guestDone && inflight == 0 && !closed {
			closed = true
			_ = cch.Close()
		}
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		defer creqsDone.Done()
		for req := range creqs {
			mu.Lock()
			inflight++
			mu.Unlock()
			s.forwardChannelRequest(req, gch, isSession, true) // reply lands on cch
			mu.Lock()
			inflight--
			tryClose()
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		defer greqsDone.Done()
		defer markExited()
		for req := range greqs {
			if req.Type == "keepalive@openssh.com" {
				// sshd's ClientAliveInterval asks whether the gateway, its
				// client, is alive; the gateway answers. Relayed, it held
				// every later request of the channel (the exit status
				// among them) on a round trip to the laptop.
				s.reply(req, true)
				continue
			}
			s.forwardChannelRequest(req, cch, isSession, false)
			if req.Type == "exit-status" || req.Type == "exit-signal" {
				markExited()
			}
		}
	}()

	go func() {
		greqsDone.Wait()
		g2cData.Wait()
		mu.Lock()
		guestDone = true
		tryClose()
		mu.Unlock()
	}()
	go func() {
		creqsDone.Wait()
		c2gData.Wait()
		_ = gch.Close() // the client is done
	}()

	wg.Wait()
	s.gw.cfg.Metrics.RelayBytesTotal.WithLabelValues("client_to_guest").Add(float64(s.toGuest.Swap(0)))
	s.gw.cfg.Metrics.RelayBytesTotal.WithLabelValues("guest_to_client").Add(float64(s.toClient.Swap(0)))
}

// forwardChannelRequest sends one channel request to the other side with
// WantReply honoured. `env` is filtered on the way to the guest; X11 and
// agent forwarding are refused (I-247).
func (s *session) forwardChannelRequest(req *ssh.Request, to ssh.Channel, isSession, toGuest bool) {
	if toGuest && isSession {
		switch req.Type {
		case "env":
			if _, ok := filterEnv(req.Payload); !ok {
				s.reply(req, false)
				return
			}
		case "x11-req", "auth-agent-req@openssh.com":
			s.reply(req, false)
			return
		}
	}
	ok, err := to.SendRequest(req.Type, req.WantReply, req.Payload)
	if err != nil {
		// The far side went away mid-request; the connection is tearing down.
		s.reply(req, false)
		return
	}
	s.reply(req, ok)
}

func (s *session) reply(req *ssh.Request, ok bool) {
	if !req.WantReply {
		return
	}
	if err := req.Reply(ok, nil); err != nil {
		s.log.Debug("request reply failed", "event", "relay", "err", err.Error())
	}
}

// forwardGlobal relays a global request from the client to the guest:
// tcpip-forward and its cancel, no-more-sessions, and anything else the
// guest may understand. Keepalives are answered here.
func (s *session) forwardGlobal(req *ssh.Request, to ssh.Conn) {
	if req.Type == "keepalive@openssh.com" {
		s.reply(req, true)
		return
	}
	ok, payload, err := to.SendRequest(req.Type, req.WantReply, req.Payload)
	if err != nil {
		s.reply(req, false)
		return
	}
	if req.WantReply {
		if err := req.Reply(ok, payload); err != nil {
			s.log.Debug("global reply failed", "event", "relay", "err", err.Error())
		}
	}
}

// guestGlobal handles a global request from the guest's sshd. Its
// keepalives are answered; hostkeys-00@openssh.com is not forwarded (the
// client must not learn the guest's keys as the gateway's); nothing else
// is expected from a server.
func (s *session) guestGlobal(req *ssh.Request) {
	switch req.Type {
	case "keepalive@openssh.com":
		s.reply(req, true)
	default:
		s.reply(req, false)
	}
}

// keepalive sends keepalive@openssh.com to both sides every interval and
// ends the session when a side stops answering for three intervals.
func (s *session) keepalive(ctx context.Context, cancel context.CancelFunc) {
	t := time.NewTicker(s.gw.cfg.Keepalive)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !s.ping(ctx, s.conn) || !s.ping(ctx, s.guest) {
			s.log.Info("keepalive unanswered", "event", "session_close", "reason", "keepalive")
			cancel()
			return
		}
	}
}

func (s *session) ping(ctx context.Context, c ssh.Conn) bool {
	done := make(chan bool, 1)
	go func() {
		_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
		// OpenSSH answers an unknown request with failure; any answer is
		// liveness. Only a transport error means the peer is gone.
		done <- err == nil
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(3 * s.gw.cfg.Keepalive):
		return false
	case <-ctx.Done():
		return true
	}
}

// report posts the session event with one retry; a failure is a log line
// (§5.5).
// report posts one session event, synchronously, retrying once only when
// the request never reached the api (a connection error): a retry after a
// timeout or a refused answer could deliver the same event twice, and the
// api's `/internal/sessions` is what feeds the ssh_sessions signal (I-123).
func (s *session) report(ctx context.Context, opened bool) {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.gw.cfg.API.ReportSession(rctx, s.route.ProjectID, opened, s.serial, s.id)
		cancel()
		var ne *net.OpError
		if err == nil || !errors.As(err, &ne) {
			break
		}
	}
	if err != nil {
		s.log.Warn("session report failed", "event", "route_fail", "reason", "sessions", "opened", opened, "err", err.Error())
	}
}

// newSessionID is a relay's id in the session reports (I-176): 16 random
// bytes in hex, never derived from anything the user sent.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails on Linux (it panics instead)
	return hex.EncodeToString(b[:])
}
