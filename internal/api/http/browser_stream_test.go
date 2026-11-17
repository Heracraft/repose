package httpapi_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/edge"
	"github.com/heracraft/repose/internal/wsframe"
)

// fakeRelay is the edge's view relay: it records exactly what the api
// asked for, which is what the cross-tenant assertion reads, and serves
// whatever the test wants on the stream.
type fakeRelay struct {
	mu   sync.Mutex
	reqs []viewAsk
	// code is a refusal from the relay's closed set; serve is the guest's
	// side of the stream when it does not refuse.
	code  string
	serve func(c net.Conn)
}

type viewAsk struct{ ProjectID, Login, GuestIP string }

func (f *fakeRelay) Dial(_ context.Context, projectID, login, guestIP string) (net.Conn, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, viewAsk{projectID, login, guestIP})
	code, serve := f.code, f.serve
	f.mu.Unlock()
	if code != "" {
		return nil, &edge.Refused{Code: code}
	}
	api, guest := net.Pipe()
	if serve != nil {
		go serve(guest)
	} else {
		go func() { _, _ = io.Copy(io.Discard, guest) }()
	}
	return api, nil
}

func (f *fakeRelay) asks() []viewAsk {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]viewAsk(nil), f.reqs...)
}

// stream opens a raw connection to the api, sends the upgrade with the
// cookie and returns the connection once the api answered 101, or the
// status and error code it answered instead.
type streamResult struct {
	conn   net.Conn
	br     *bufio.Reader
	status int
	code   string
}

func openStream(t *testing.T, e *env, ticket, origin string) streamResult {
	t.Helper()
	u, err := url.Parse(e.api.URL)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	var key [16]byte
	_, _ = rand.Read(key[:])
	req := fmt.Sprintf("GET /v1/browser/stream HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n",
		u.Host, base64.StdEncoding.EncodeToString(key[:]))
	if ticket != "" {
		req += "Cookie: repose_view=" + ticket + "\r\n"
	}
	if origin != "" {
		req += "Origin: " + origin + "\r\n"
	}
	req += "\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read the api's answer: %v", err)
	}
	out := streamResult{status: res.StatusCode}
	if res.StatusCode == http.StatusSwitchingProtocols {
		out.conn, out.br = c, br
		return out
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	out.code = env.Error.Code
	return out
}

// mint takes a ticket for a project the signed-in user owns.
func mintTicket(t *testing.T, e *env, token, pid string) string {
	t.Helper()
	r := e.do(t, token, "POST", "/projects/"+pid+"/browser/view", nil)
	if r.status != 200 {
		t.Fatalf("mint: %d %s", r.status, r.raw)
	}
	for _, c := range (&http.Response{Header: r.hdr}).Cookies() {
		if c.Name == "repose_view" {
			return c.Value
		}
	}
	t.Fatalf("no cookie in %v", r.hdr)
	return ""
}

// echoGuest is a guest that answers every message with the same bytes and
// sends one frame of its own first.
func echoGuest(first string) func(net.Conn) {
	return func(c net.Conn) {
		defer func() { _ = c.Close() }()
		if first != "" {
			if _, err := c.Write([]byte(first)); err != nil {
				return
			}
		}
		_, _ = io.Copy(c, c)
	}
}

// readFrame reads one data frame off the api's side of the socket.
func readFrame(t *testing.T, br *bufio.Reader) (byte, []byte) {
	t.Helper()
	op, payload, _, err := wsframe.ReadMessage(br, 1<<20, func(wsframe.Frame) error { return nil })
	if err != nil {
		t.Fatalf("read a frame: %v", err)
	}
	return op, payload
}

// THE OTHER CROSS-TENANT TEST.
//
// The stream route carries no project id: the ticket names the project,
// and the handler derives project_id, login and guest_ip from the
// ticket's row alone. So there is nothing for user A to point at user B's
// machine, and what the relay is asked for is always the ticket's own
// project. The first assertion is on the registered pattern, because the
// hole this closes is a route *shape*: an id in the path that an
// implementer resolves with the lookup that has no owner check.
func TestStreamReachesOnlyTheTicketsProject(t *testing.T) {
	relay := &fakeRelay{serve: echoGuest("A-FRAME")}
	e := newViewEnv(t, relay, nil)
	tokA, pidA := newViewProject(t, e, "sub-view-xa", "viewxa")
	tokB, pidB := newViewProject(t, e, "sub-view-xb", "viewxb")

	var streamRoutes []string
	for _, r := range e.srv.Routes() {
		if strings.Contains(r, "/browser/stream") {
			streamRoutes = append(streamRoutes, r)
		}
	}
	if len(streamRoutes) != 1 || streamRoutes[0] != "GET /v1/browser/stream" {
		t.Fatalf("the stream routes are %v; a project id in the path is the hole this closes", streamRoutes)
	}

	// B's project exists and is running, and A's ticket must not reach it.
	ticketA := mintTicket(t, e, tokA, pidA)
	_ = mintTicket(t, e, tokB, pidB)

	res := openStream(t, e, ticketA, "https://repose.run")
	if res.status != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d %s", res.status, res.code)
	}
	op, payload := readFrame(t, res.br)
	if op != wsframe.OpBinary || string(payload) != "A-FRAME" {
		t.Fatalf("op %d payload %q", op, payload)
	}
	asks := relay.asks()
	if len(asks) != 1 {
		t.Fatalf("%d relay requests", len(asks))
	}
	if asks[0].ProjectID != pidA {
		t.Fatalf("the relay was asked for %s, want A's %s", asks[0].ProjectID, pidA)
	}
	if !strings.HasPrefix(asks[0].Login, "watched.") || !strings.Contains(asks[0].Login, "viewxa") {
		t.Fatalf("login %q is not A's", asks[0].Login)
	}
	for _, ask := range asks {
		if ask.ProjectID == pidB {
			t.Fatal("A's ticket reached B's project")
		}
	}
}

// Nothing but a live ticket opens a stream: no cookie, a tampered cookie,
// an expired row, a ticket whose project has since stopped, a ticket
// whose user has since been suspended, and an origin that is not the
// dashboard's.
func TestStreamRefusals(t *testing.T) {
	relay := &fakeRelay{serve: echoGuest("F")}
	e := newViewEnv(t, relay, nil)
	tok, pid := newViewProject(t, e, "sub-view-ref", "vref")

	if res := openStream(t, e, "", "https://repose.run"); res.code != "view_expired" {
		t.Fatalf("no cookie: %d %s", res.status, res.code)
	}
	if res := openStream(t, e, "not-a-real-ticket", "https://repose.run"); res.code != "view_expired" {
		t.Fatalf("a tampered cookie: %d %s", res.status, res.code)
	}
	ticket := mintTicket(t, e, tok, pid)
	if res := openStream(t, e, ticket, "https://evil.example"); res.code != "forbidden" {
		t.Fatalf("another origin: %d %s", res.status, res.code)
	}
	// Expiry is the revocation.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update browser_view_tickets set expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if res := openStream(t, e, ticket, "https://repose.run"); res.code != "view_expired" {
		t.Fatalf("an expired ticket: %d %s", res.status, res.code)
	}

	// A machine that stopped after the mint.
	ticket = mintTicket(t, e, tok, pid)
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update projects set state = 'stopped' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	if res := openStream(t, e, ticket, "https://repose.run"); res.code != "conflict" {
		t.Fatalf("a stopped machine: %d %s", res.status, res.code)
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update projects set state = 'running' where id = $1", pid); err != nil {
		t.Fatal(err)
	}

	// A user suspended after the mint: the stream route is outside
	// `authed`, so it re-reads the suspension itself.
	ticket = mintTicket(t, e, tok, pid)
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update users set suspended_at = now() where handle = 'vref' or github_login = 'vref'"); err != nil {
		t.Fatal(err)
	}
	if res := openStream(t, e, ticket, "https://repose.run"); res.code != "view_expired" {
		t.Fatalf("a suspended user: %d %s", res.status, res.code)
	}
	if n := len(relay.asks()); n != 0 {
		t.Fatalf("%d relay requests for refused streams", n)
	}
}

// The relay's refusals come back as the api's own codes, before the
// upgrade, so the page can read them.
func TestStreamPassesTheRelaysRefusalOn(t *testing.T) {
	for _, tc := range []struct{ relayCode, apiCode string }{
		{"view_busy", "view_busy"},
		{"view_capacity", "view_capacity"},
		{"view_desktop_failed", "conflict"},
		{"view_auth_failed", "conflict"},
	} {
		relay := &fakeRelay{code: tc.relayCode}
		e := newViewEnv(t, relay, nil)
		tok, pid := newViewProject(t, e, "sub-view-rr-"+tc.relayCode, "vrr"+tc.relayCode[5:9])
		ticket := mintTicket(t, e, tok, pid)
		if res := openStream(t, e, ticket, "https://repose.run"); res.code != tc.apiCode {
			t.Fatalf("relay %s: api answered %d %s, want %s", tc.relayCode, res.status, res.code, tc.apiCode)
		}
	}
}

// A page's own messages reach the guest, bounded: one over the message
// cap closes the stream instead of being buffered.
func TestStreamCarriesBothDirectionsAndBoundsAMessage(t *testing.T) {
	relay := &fakeRelay{serve: echoGuest("HELLO")}
	e := newViewEnv(t, relay, nil)
	tok, pid := newViewProject(t, e, "sub-view-both", "vboth")
	ticket := mintTicket(t, e, tok, pid)
	res := openStream(t, e, ticket, "https://repose.run")
	if res.status != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d %s", res.status, res.code)
	}
	if _, payload := readFrame(t, res.br); string(payload) != "HELLO" {
		t.Fatalf("first frame %q", payload)
	}
	if _, err := res.conn.Write(wsframe.Encode(wsframe.OpBinary, []byte("CLIENT-INIT"), true)); err != nil {
		t.Fatal(err)
	}
	if _, payload := readFrame(t, res.br); string(payload) != "CLIENT-INIT" {
		t.Fatalf("the echo came back as %q", payload)
	}
	// 2 MiB, over the 1 MiB cap: the stream ends rather than allocating.
	big := make([]byte, 2<<20)
	_, _ = res.conn.Write(wsframe.Encode(wsframe.OpBinary, big, true))
	_ = res.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, _, _, err := wsframe.ReadMessage(res.br, 4<<20, func(wsframe.Frame) error { return nil }); err == nil {
		t.Fatal("the stream survived a message over the cap")
	}
	e.waitForLog(t, `"outcome":"too_large"`)
}

// waitForLog waits for a line containing want to reach the test's log
// buffer: the close line is written as the stream's goroutines unwind,
// just after the socket ends.
func (e *env) waitForLog(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(e.logText(), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no log line carrying %q", want)
}
