package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// The browser view's relay (DECISIONS I-1216, amending I-292): the edge
// end of the dashboard's view of a project's machine browser. The api
// terminates the WebSocket and does every authentication a user has;
// this side has the two things the api cannot reach -- a network path to
// a guest's sshd and the Host CA -- and does the RFB handshake so that
// the guest's per-boot VNC password never leaves the edge.
//
// The leg is one line of JSON in, one line of JSON out, and then the RFB
// stream:
//
//	-> {"project_id":"...","login":"...","guest_ip":"10.64.x.y"}
//	<- {"ok":true}   or   {"code":"view_busy"}
//
// after which the connection carries RFB in both directions, already
// authenticated to the guest. The relay is a byte pipe past that point,
// so nothing a rooted guest sends is parsed on the platform.
//
// What authenticates the api here is its client certificate from the
// api's own CA, with the CN pinned (cmd/gateway's VIEW_LISTEN block) --
// the mirror of the api's /internal listener, which admits the gateway's
// certificate and nothing else (I-431). A WireGuard source address is a
// filter in nftables, never the credential: the control VM is a shared
// Docker host, so "it came from 10.255.255.1" says only that something
// on that box sent it.
//
// The api holds the byte budget, the idle close and the hard session cap,
// because the api is on the byte path and the budget has to survive a
// reconnect. This side owns the handshake deadline and the concurrency
// counters.

// View refusal codes, the closed set the api maps to its own refusals.
// Every one of them is a word a log line may carry.
const (
	ViewBusy          = "view_busy"           // another view holds this project
	ViewCapacity      = "view_capacity"       // the edge is at its global view cap
	ViewBadRequest    = "view_bad_request"    // the request line is not one the relay will act on
	ViewDialFailed    = "view_dial_failed"    // the guest's sshd did not answer
	ViewDesktopFailed = "view_desktop_failed" // `desktop start` failed, or printed no password
	ViewAuthFailed    = "view_auth_failed"    // the RFB handshake with Xvnc failed
)

// ViewRelayed is the result of a view that reached the byte pipe. The
// words above and this one are the whole `result` label of
// repose_gateway_view_requests_total, listed as
// obsmetrics.ViewResults so the series exist before the first view; a
// test holds the two lists together.
const ViewRelayed = "relayed"

// View relay limits.
const (
	// viewsPerProject is 1: Xvnc is -AlwaysShared (nix/guest/base/
	// desktop.nix) so a second viewer would work and double the frame
	// cost for nothing.
	viewsPerProject = 1
	// viewsMax bounds the edge's share of this feature however many
	// projects ask at once, which the per-project counter alone does not
	// (the gateway's own DefaultMaxConns is the same idea).
	viewsMax = 64
	// viewHandshake bounds `desktop start` plus the RFB handshake,
	// separately from the SSH dial's own DialTimeout. A guest that stalls
	// costs one goroutine for this long and no more.
	viewHandshake = 10 * time.Second
	// viewRequestMax bounds the request line.
	viewRequestMax = 4 << 10
	// viewPasswordMax bounds what the relay reads from `desktop start`:
	// the password is eight characters, and a guest that prints a
	// gigabyte instead must not be buffered.
	viewPasswordMax = 256
	// viewReplyWait is the room the reply line gets, on top of whatever
	// the phase before it spent. Without it a guest that used the whole
	// handshake budget would leave the api with a closed connection
	// instead of the code naming which leg failed.
	viewReplyWait = 5 * time.Second
)

// desktopStartCmd starts the socket-activated desktop chain in the guest
// and prints the boot's VNC password, which is what the relay needs and
// what `repose browser` already runs (internal/cli/browsercmd.go).
const desktopStartCmd = "repose-guest-profile desktop start"

// desktopVNCPort is Xvnc's loopback port inside the guest
// (nix/guest/base/desktop.nix). The relay dials it over the SSH
// connection, so the guest opens no port to anyone.
const desktopVNCPort = 5900

// ViewRelay serves the api's view requests on the WireGuard address.
type ViewRelay struct {
	g   *Gateway
	log *slog.Logger
	m   *obsmetrics.GatewayMetrics

	perProject *keyedCounter
	global     *connCounter
	// handshake is viewHandshake; a test shortens it.
	handshake time.Duration
}

// NewViewRelay builds the relay over a running gateway, whose SSH dial,
// Host CA and per-project certificates it uses.
func NewViewRelay(g *Gateway, log *slog.Logger, m *obsmetrics.GatewayMetrics) *ViewRelay {
	if log == nil {
		log = obs.Nop(obs.ComponentGateway)
	}
	return &ViewRelay{
		g:          g,
		log:        log,
		m:          m,
		perProject: newKeyedCounter(viewsPerProject),
		global:     &connCounter{max: viewsMax},
		handshake:  viewHandshake,
	}
}

// viewRequest is the api's line. Every field comes from the ticket's row
// in Postgres; the relay trusts none of it for identity -- the login is
// what the guest's host certificate is checked against, and guest_ip must
// be a guest address.
type viewRequest struct {
	ProjectID string `json:"project_id"`
	Login     string `json:"login"`
	GuestIP   string `json:"guest_ip"`
}

// viewReply is the relay's line: ok, or a refusal code from the closed
// set above. Never a message: the api has the user's words.
type viewReply struct {
	OK   bool   `json:"ok,omitempty"`
	Code string `json:"code,omitempty"`
}

func (v *ViewRelay) result(result string) {
	if v.m != nil {
		v.m.ViewRequestsTotal.WithLabelValues(result).Inc()
	}
}

// Serve accepts view requests until ln is closed.
func (v *ViewRelay) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go v.handle(c)
	}
}

// handle is one view, from the request line to the last byte piped.
func (v *ViewRelay) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	// The whole exchange before the pipe has the handshake budget; the
	// pipe itself has none here, because the api holds the idle close and
	// the session cap.
	_ = c.SetDeadline(time.Now().Add(v.handshake))
	// Read the request line a byte at a time rather than through a
	// bufio.Reader: a buffered read would swallow the first RFB bytes
	// after it, and the pipe below reads the connection itself.
	line, err := readViewLine(c, viewRequestMax)
	if err != nil {
		v.refuse(c, ViewBadRequest)
		return
	}
	var req viewRequest
	if err := json.Unmarshal(line, &req); err != nil || !validViewRequest(req) {
		v.refuse(c, ViewBadRequest)
		return
	}
	if !v.global.acquire() {
		v.refuse(c, ViewCapacity)
		return
	}
	defer v.global.release()
	if !v.perProject.acquire(req.ProjectID) {
		v.refuse(c, ViewBusy)
		return
	}
	defer v.perProject.release(req.ProjectID)

	client, code := v.connect(req)
	if code != "" {
		v.refuse(c, code)
		return
	}
	defer func() { _ = client.Close() }()

	guest, pw, code := v.desktop(client)
	if code != "" {
		v.refuse(c, code)
		return
	}
	defer func() { _ = guest.Close() }()

	// VncAuth with the guest, then the answer, then the handshake the
	// page's noVNC expects. The password is in this local and in the
	// relay's memory for the length of the handshake; it is never
	// written, never logged, never returned to the api and never sent to
	// the browser.
	if err := rfbAuthenticate(guest, pw); err != nil {
		v.refuse(c, ViewAuthFailed)
		return
	}
	_ = c.SetDeadline(time.Now().Add(viewReplyWait))
	if err := writeViewReply(c, viewReply{OK: true}); err != nil {
		return
	}
	_ = c.SetDeadline(time.Now().Add(v.handshake))
	if err := rfbOfferNone(c); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	v.result(ViewRelayed)
	v.pipe(c, guest)
}

// connect dials the project's guest over SSH. It is g.dialGuestSSH and
// never webhookClient: the webhook path's cache closes a client 30
// seconds after its last use (webhook.IdleReuse), which would cut an open
// view mid-frame. A view holds its own client for the session.
func (v *ViewRelay) connect(req viewRequest) (*ssh.Client, string) {
	ctx, cancel := context.WithTimeout(context.Background(), v.g.cfg.DialTimeout)
	defer cancel()
	client, err := v.g.dialGuestSSH(ctx, req.ProjectID, req.Login, req.GuestIP)
	if err != nil {
		// project_id and the reason, never the address or the error's
		// own text beyond what the ssh library wrote.
		v.log.Warn("view dial failed", "event", obs.EventDialFail, "reason", ViewDialFailed, "project_id", req.ProjectID)
		return nil, ViewDialFailed
	}
	return client, ""
}

// desktop starts the guest's desktop chain, reads the boot's VNC password
// and opens Xvnc's loopback port, all under the handshake deadline. The
// SSH channel the dial returns cannot carry a deadline of its own, so the
// budget is enforced by closing the client, as the gateway's own dial
// does.
func (v *ViewRelay) desktop(client *ssh.Client) (net.Conn, string, string) {
	var mu sync.Mutex
	done := false
	timer := time.AfterFunc(v.handshake, func() {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			_ = client.Close()
		}
	})
	stop := func() {
		timer.Stop()
		mu.Lock()
		done = true
		mu.Unlock()
	}
	defer stop()

	sess, err := client.NewSession()
	if err != nil {
		return nil, "", ViewDesktopFailed
	}
	var out bytes.Buffer
	sess.Stdout = &limitedWriter{w: &out, left: viewPasswordMax}
	sess.Stderr = io.Discard
	err = sess.Run(desktopStartCmd)
	_ = sess.Close()
	if err != nil {
		// The helper exits non-zero when the chain did not start, and
		// then whatever it printed is not a password. Its output is never
		// logged: on a working guest it is the password.
		v.log.Warn("view desktop start failed", "event", obs.EventRouteFail, "reason", ViewDesktopFailed)
		return nil, "", ViewDesktopFailed
	}
	pw := lastLine(out.Bytes())
	if !plausiblePassword(pw) {
		// A guest that prints something else instead of the password is
		// refused here rather than at the handshake, so the refusal says
		// which leg failed. The output itself is never logged: it is the
		// password on a working guest.
		v.log.Warn("view desktop printed no password", "event", obs.EventRouteFail, "reason", ViewDesktopFailed)
		return nil, "", ViewDesktopFailed
	}
	guest, err := dialLoopback(client, desktopVNCPort)
	if err != nil {
		return nil, "", ViewDesktopFailed
	}
	return guest, pw, ""
}

// pipe copies in both directions until either side ends, then ends the
// other. No byte of the stream is read, counted by content or logged:
// framebuffer rectangles are the contents of a tenant's screen.
func (v *ViewRelay) pipe(client net.Conn, guest net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(guest, client)
		_ = guest.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, guest)
		_ = client.Close()
	}()
	wg.Wait()
}

// refuse answers one code and counts it. A refusal is the whole answer:
// the connection closes, and the api turns the code into the user's
// words.
func (v *ViewRelay) refuse(c net.Conn, code string) {
	v.result(code)
	// The phase that failed may have spent the whole handshake budget,
	// and the code is what tells the api which leg to look at.
	_ = c.SetDeadline(time.Now().Add(viewReplyWait))
	_ = writeViewReply(c, viewReply{Code: code})
}

func writeViewReply(w io.Writer, reply viewReply) error {
	b, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// validViewRequest is the relay's own check on what it was asked to dial,
// independent of the api's: a uuid project, a login that is a login, and
// a guest address inside 10.64.0.0/12. The api derives all three from the
// ticket's row, so a mismatch here is a bug or a caller that should not
// have reached the listener.
func validViewRequest(req viewRequest) bool {
	if _, err := uuid.Parse(req.ProjectID); err != nil {
		return false
	}
	if !isGuestAddr(req.GuestIP) {
		return false
	}
	return validLogin(req.Login)
}

// plausiblePassword is the shape of the per-boot password
// (nix/guest/base/desktop.nix generates eight base62 characters): one
// line, printable, no space. A guest that exits 0 and prints a sentence
// instead is refused here, where the code says which leg failed, rather
// than at the RFB handshake.
func plausiblePassword(pw string) bool {
	if pw == "" || len(pw) > 64 {
		return false
	}
	for _, r := range pw {
		if r <= ' ' || r > '~' {
			return false
		}
	}
	return true
}

// validLogin bounds the principal the host certificate is checked
// against: the `<slug>.<handle>` shape of docs/interfaces/ssh-gateway.md.
func validLogin(login string) bool {
	if login == "" || len(login) > 128 || strings.ContainsAny(login, " \t\r\n:/\\") {
		return false
	}
	return true
}

// lastLine is the last non-empty line of a command's output, which is
// where `repose-guest-profile desktop start` prints the password
// (internal/cli/open.go does the same with the same output).
func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// limitedWriter stops after left bytes with an error, so a guest that
// writes for ever on this path ends its own session instead of filling
// the edge's memory.
type limitedWriter struct {
	w    io.Writer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.left {
		if l.left > 0 {
			_, _ = l.w.Write(p[:l.left])
			l.left = 0
		}
		return 0, fmt.Errorf("output over %d bytes", viewPasswordMax)
	}
	n, err := l.w.Write(p)
	l.left -= n
	return n, err
}

// readViewLine reads one newline-terminated line of at most max bytes,
// leaving everything after the newline on the connection.
func readViewLine(r io.Reader, max int) ([]byte, error) {
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
	return nil, errors.New("gateway: a view request line over the cap")
}
