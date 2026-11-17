package gateway

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// The view relay against the fake guest: the api's half of the leg, the
// guest's Xvnc, and the page's noVNC are all stood in for, so that what
// is tested is the relay's own refusals, its deadline and the two RFB
// handshakes (DECISIONS I-1216).

// fakeXvnc is Xvnc's RFB server side: version 3.8, VncAuth with one
// password, and after SecurityResult it sends `frame` and echoes
// whatever the viewer sends, so a test can assert the pipe carries both
// directions.
type fakeXvnc struct {
	password string
	frame    []byte
	// offerNone makes it offer security type None instead of VncAuth,
	// which the relay must refuse rather than fall back to.
	offerNone bool
	// silent never sends the version string.
	silent bool
	// refuse answers a non-zero SecurityResult.
	refuse bool

	ln   net.Listener
	sent chan []byte
}

func newFakeXvnc(t *testing.T, x *fakeXvnc) *fakeXvnc {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	x.ln = ln
	x.sent = make(chan []byte, 8)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go x.serve(c)
		}
	}()
	return x
}

func (x *fakeXvnc) addr() string { return x.ln.Addr().String() }

func (x *fakeXvnc) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	if x.silent {
		time.Sleep(30 * time.Second)
		return
	}
	if _, err := c.Write([]byte("RFB 003.008\n")); err != nil {
		return
	}
	var version [12]byte
	if _, err := io.ReadFull(c, version[:]); err != nil {
		return
	}
	if x.offerNone {
		_, _ = c.Write([]byte{1, rfbSecNone})
		return
	}
	if _, err := c.Write([]byte{1, rfbSecVncAuth}); err != nil {
		return
	}
	var chosen [1]byte
	if _, err := io.ReadFull(c, chosen[:]); err != nil || chosen[0] != rfbSecVncAuth {
		return
	}
	challenge := bytes.Repeat([]byte{0x5a}, 16)
	if _, err := c.Write(challenge); err != nil {
		return
	}
	got := make([]byte, 16)
	if _, err := io.ReadFull(c, got); err != nil {
		return
	}
	want, err := vncAuthResponse(x.password, challenge)
	if err != nil || !bytes.Equal(got, want) || x.refuse {
		_, _ = c.Write([]byte{0, 0, 0, 1})
		return
	}
	if _, err := c.Write([]byte{0, 0, 0, 0}); err != nil {
		return
	}
	if len(x.frame) > 0 {
		_, _ = c.Write(x.frame)
	}
	// Everything the viewer sends after the handshake: ClientInit and the
	// key and pointer events.
	buf := make([]byte, 64)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			select {
			case x.sent <- append([]byte(nil), buf[:n]...):
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// viewHarness is the gateway harness with a relay in front of it.
type viewHarness struct {
	*harness
	relay *ViewRelay
	addr  string
	xvnc  *fakeXvnc
}

// newViewHarness builds the relay and starts its accept loop. A test that
// needs a relay knob (the handshake budget, the global cap) sets it in an
// option, before Serve: a write after it races the handler goroutines'
// reads, which the race detector reports (r20261117-1).
func newViewHarness(t *testing.T, x *fakeXvnc, opts ...func(*ViewRelay)) *viewHarness {
	t.Helper()
	h := newHarness(t, harnessOpts{})
	xvnc := newFakeXvnc(t, x)
	h.guest.mu.Lock()
	h.guest.redirect[desktopVNCPort] = xvnc.addr()
	h.guest.desktop = desktopBehaviour{password: x.password}
	h.guest.mu.Unlock()

	relay := NewViewRelay(h.gw, slog.New(slog.NewJSONHandler(h.logs, nil)), h.metrics)
	for _, o := range opts {
		o(relay)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = relay.Serve(ln) }()
	return &viewHarness{harness: h, relay: relay, addr: ln.Addr().String(), xvnc: xvnc}
}

// request opens one view leg and returns the connection and the relay's
// reply line.
func (v *viewHarness) request(t *testing.T, req viewRequest) (net.Conn, viewReply) {
	t.Helper()
	c, err := net.DialTimeout("tcp", v.addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := readViewLine(c, 4096)
	if err != nil {
		t.Fatalf("read the relay's reply: %v", err)
	}
	var reply viewReply
	if err := json.Unmarshal(line, &reply); err != nil {
		t.Fatalf("the relay's reply %q: %v", line, err)
	}
	return c, reply
}

// ok is the request the api makes for the harness project.
func (v *viewHarness) ok() viewRequest {
	return viewRequest{ProjectID: v.project.ID, Login: v.login, GuestIP: v.project.GuestIP}
}

// clientHandshake is what the page's noVNC does: read the version, answer
// it, take the one security type offered, read SecurityResult, send
// ClientInit.
func clientHandshake(t *testing.T, c net.Conn) {
	t.Helper()
	var version [12]byte
	if _, err := io.ReadFull(c, version[:]); err != nil {
		t.Fatalf("read the relay's version string: %v", err)
	}
	if string(version[:]) != "RFB 003.008\n" {
		t.Fatalf("version %q", version[:])
	}
	if _, err := c.Write([]byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	var types [2]byte
	if _, err := io.ReadFull(c, types[:]); err != nil {
		t.Fatalf("read the security types: %v", err)
	}
	if types[0] != 1 || types[1] != rfbSecNone {
		t.Fatalf("the relay offered %v, want one None: the page must never be asked for a password", types)
	}
	if _, err := c.Write([]byte{rfbSecNone}); err != nil {
		t.Fatal(err)
	}
	var result [4]byte
	if _, err := io.ReadFull(c, result[:]); err != nil {
		t.Fatalf("read SecurityResult: %v", err)
	}
	if binary.BigEndian.Uint32(result[:]) != 0 {
		t.Fatalf("SecurityResult %v", result)
	}
	if _, err := c.Write([]byte{1}); err != nil { // ClientInit, shared
		t.Fatal(err)
	}
}

// The happy path: the relay authenticates to the guest with the boot's
// password, offers the page None, and the guest's frames arrive. The
// password is never on the leg the page reads.
func TestViewRelayReachesTheBytePipe(t *testing.T) {
	v := newViewHarness(t, &fakeXvnc{password: "s3cr3tpw", frame: []byte("FRAME-ONE")})
	c, reply := v.request(t, v.ok())
	if !reply.OK || reply.Code != "" {
		t.Fatalf("reply %+v", reply)
	}
	clientHandshake(t, c)
	got := make([]byte, len("FRAME-ONE"))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read a frame: %v", err)
	}
	if string(got) != "FRAME-ONE" {
		t.Fatalf("frame %q", got)
	}
	select {
	case sent := <-v.xvnc.sent:
		if !bytes.Equal(sent, []byte{1}) {
			t.Fatalf("the guest saw %v, want the page's ClientInit", sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the page's ClientInit never reached the guest")
	}
	if n := viewResults(t, v.harness, ViewRelayed); n != 1 {
		t.Fatalf("relayed counter %d", n)
	}
	// The one thing the page must never see.
	if strings.Contains(v.logs.String(), "s3cr3tpw") {
		t.Fatal("the guest's VNC password reached a log line")
	}
}

// One view per project: Xvnc is -AlwaysShared, so a second viewer would
// work and double the frame cost for nothing. The slot comes back when
// the first view ends.
func TestViewRelayRefusesASecondViewOfOneProject(t *testing.T) {
	v := newViewHarness(t, &fakeXvnc{password: "pw123456", frame: []byte("F")})
	first, reply := v.request(t, v.ok())
	if !reply.OK {
		t.Fatalf("the first view: %+v", reply)
	}
	clientHandshake(t, first)
	if _, reply := v.request(t, v.ok()); reply.Code != ViewBusy {
		t.Fatalf("the second view: %+v, want %s", reply, ViewBusy)
	}
	_ = first.Close()
	// The relay frees the slot as its goroutine unwinds.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, reply := v.request(t, v.ok())
		if reply.OK {
			_ = c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the project's slot never came back: %+v", reply)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The global cap is the peer the per-project counter does not give: one
// project each, past the cap, is refused.
func TestViewRelayRefusesPastTheGlobalCap(t *testing.T) {
	v := newViewHarness(t, &fakeXvnc{password: "pw123456", frame: []byte("F")}, func(r *ViewRelay) { r.global = &connCounter{max: 1} })
	open, reply := v.request(t, v.ok())
	if !reply.OK {
		t.Fatalf("the first view: %+v", reply)
	}
	clientHandshake(t, open)
	// Another project entirely: the global cap is checked before the
	// per-project one, so this is view_capacity and not view_busy.
	other := viewRequest{ProjectID: "0193f0c8-2c5a-7000-8000-00000000beef", Login: "other.handle", GuestIP: "10.64.9.9"}
	if _, reply := v.request(t, other); reply.Code != ViewCapacity {
		t.Fatalf("past the cap: %+v, want %s", reply, ViewCapacity)
	}
	_ = open.Close()
}

// A request the relay will not act on is refused before anything is
// dialled: the relay checks the project is a uuid, the login is a login
// and the address is inside the guest range, however the api got them.
func TestViewRelayRefusesARequestItWillNotActOn(t *testing.T) {
	v := newViewHarness(t, &fakeXvnc{password: "pw123456"})
	for _, tc := range []struct {
		name string
		req  viewRequest
	}{
		{"no project", viewRequest{Login: v.login, GuestIP: v.project.GuestIP}},
		{"not a uuid", viewRequest{ProjectID: "../../etc", Login: v.login, GuestIP: v.project.GuestIP}},
		{"an address that is not a guest", viewRequest{ProjectID: v.project.ID, Login: v.login, GuestIP: "10.255.255.1"}},
		{"no login", viewRequest{ProjectID: v.project.ID, GuestIP: v.project.GuestIP}},
		{"a login with a port in it", viewRequest{ProjectID: v.project.ID, Login: "a.b:22", GuestIP: v.project.GuestIP}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, reply := v.request(t, tc.req); reply.Code != ViewBadRequest {
				t.Fatalf("%+v, want %s", reply, ViewBadRequest)
			}
		})
	}
}

// A guest that prints something else instead of the password is refused
// with a code from the closed set, and so is one that prints for ever.
// Neither answer carries what the guest printed.
func TestViewRelayRefusesAGuestWithNoPassword(t *testing.T) {
	for _, d := range []desktopBehaviour{{garbage: true}, {flood: true}} {
		v := newViewHarness(t, &fakeXvnc{password: "pw123456"})
		v.guest.mu.Lock()
		v.guest.desktop = d
		v.guest.mu.Unlock()
		if _, reply := v.request(t, v.ok()); reply.Code != ViewDesktopFailed {
			t.Fatalf("%+v, want %s", reply, ViewDesktopFailed)
		}
	}
}

// A guest that never answers costs one goroutine for the handshake
// budget and no more: the deadline ends the attempt and nothing is left
// behind.
func TestViewRelayDropsAStalledGuestAndLeaksNoGoroutine(t *testing.T) {
	v := newViewHarness(t, &fakeXvnc{password: "pw123456"}, func(r *ViewRelay) { r.handshake = 300 * time.Millisecond })
	v.guest.mu.Lock()
	v.guest.desktop = desktopBehaviour{hang: true}
	v.guest.mu.Unlock()

	before := runtime.NumGoroutine()
	start := time.Now()
	c, reply := v.request(t, v.ok())
	if reply.Code != ViewDesktopFailed {
		t.Fatalf("%+v, want %s", reply, ViewDesktopFailed)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the stalled guest held the relay for %v", took)
	}
	_ = c.Close()
	// Give the relay's goroutines time to unwind, then compare.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines %d -> %d after a stalled handshake", before, after)
	}
}

// An Xvnc that offers no VncAuth, and one that refuses the password, both
// end as a refusal and never as a stream the page could draw.
func TestViewRelayRefusesAFailedRFBHandshake(t *testing.T) {
	for _, x := range []*fakeXvnc{
		{password: "pw123456", offerNone: true},
		{password: "pw123456", refuse: true},
	} {
		v := newViewHarness(t, x)
		if _, reply := v.request(t, v.ok()); reply.Code != ViewAuthFailed {
			t.Fatalf("%+v, want %s", reply, ViewAuthFailed)
		}
	}
}

func viewResults(t *testing.T, h *harness, result string) int {
	t.Helper()
	c, err := h.metrics.ViewRequestsTotal.GetMetricWithLabelValues(result)
	if err != nil {
		t.Fatal(err)
	}
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return int(m.GetCounter().GetValue())
}
