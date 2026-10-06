package mcpshim

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/mcpreg"
)

// fakeLaptop is the laptop's end of a hold: a fakeServer per stream.
type fakeLaptop struct {
	srv   *fakeServer
	fw    *FrameWriter
	mu    sync.Mutex
	pipes map[uint32]net.Conn
	opens int
	ready chan Ready
	gone  chan string
	hello chan string
}

// startHold runs hold for names against a fake laptop; closing the
// returned writer is the laptop going away.
func startHold(t *testing.T, p mcpreg.Paths, srv *fakeServer, args ...string) (*fakeLaptop, io.WriteCloser, chan int) {
	t.Helper()
	toHoldR, toHoldW := io.Pipe()
	fromHoldR, fromHoldW := io.Pipe()
	fl := &fakeLaptop{srv: srv, fw: NewFrameWriter(toHoldW), pipes: map[uint32]net.Conn{}, ready: make(chan Ready, 8), gone: make(chan string, 8), hello: make(chan string, 1)}
	code := make(chan int, 1)
	go func() {
		code <- hold(p, args, toHoldR, fromHoldW, io.Discard)
		_ = fromHoldW.Close()
	}()
	go fl.run(fromHoldR)
	t.Cleanup(func() { _ = toHoldW.Close() })
	return fl, toHoldW, code
}

func (fl *fakeLaptop) run(r io.Reader) {
	for {
		f, err := ReadFrame(r)
		if err != nil {
			return
		}
		switch f.Type {
		case FrameHello:
			fl.hello <- string(f.Payload)
		case FrameOpen:
			a, b := net.Pipe()
			fl.mu.Lock()
			fl.pipes[f.ID] = a
			fl.opens++
			fl.mu.Unlock()
			go fl.srv.handle(b)
			go func(id uint32) {
				buf := make([]byte, 4096)
				for {
					n, err := a.Read(buf)
					if n > 0 {
						_ = fl.fw.Data(id, buf[:n])
					}
					if err != nil {
						_ = fl.fw.Write(FrameClose, id, nil)
						return
					}
				}
			}(f.ID)
		case FrameData:
			fl.mu.Lock()
			a := fl.pipes[f.ID]
			fl.mu.Unlock()
			if a != nil {
				_, _ = a.Write(f.Payload)
			}
		case FrameClose:
			fl.mu.Lock()
			a := fl.pipes[f.ID]
			delete(fl.pipes, f.ID)
			fl.mu.Unlock()
			if a != nil {
				_ = a.Close()
			}
		case FrameReady:
			var r Ready
			_ = json.Unmarshal(f.Payload, &r)
			fl.ready <- r
		case FrameGone:
			fl.gone <- string(f.Payload)
		}
	}
}

func readyOf(t *testing.T, fl *fakeLaptop) Ready {
	t.Helper()
	select {
	case r := <-fl.ready:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no ready frame")
	}
	return Ready{}
}

// hold fills forward/NAME.json through the laptop, says how many tools,
// serves the socket, and removes it when the laptop's side ends.
func TestHoldRegistersAndServes(t *testing.T) {
	fast(t)
	p := testPaths(t)
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search", "create"}, pings: true}
	fl, laptop, code := startHold(t, p, srv, "notes")
	if v := <-fl.hello; v != FrameVersion {
		t.Errorf("hello %q", v)
	}
	r := readyOf(t, fl)
	if r.Error != "" || r.Tools != 2 || !r.New || r.Changed {
		t.Errorf("ready = %+v", r)
	}
	f, ok, err := mcpreg.LoadForward(p, "notes")
	if !ok || err != nil || f.ProtocolVersion != "2025-06-18" || len(f.Tools) != 2 || !strings.Contains(string(f.Initialize), "probe") {
		t.Fatalf("forward file: %+v %v %v", f, ok, err)
	}
	if st, err := os.Stat(p.SocketPath("notes")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket: %v %v", st, err)
	}
	if st, _ := os.Stat(p.ForwardFile("notes")); st.Mode().Perm() != 0o600 {
		t.Errorf("forward file mode %v", st.Mode())
	}
	// A shim's session through the socket reaches the laptop's server.
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool {
		a.send(2, "tools/call", map[string]any{"name": "search"})
		text, _ := callText(t, a.next(2*time.Second))
		return text == "called search"
	})
	_ = laptop.Close()
	select {
	case c := <-code:
		if c != 0 {
			t.Errorf("hold exited %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hold outlived the laptop's side")
	}
	if _, err := os.Stat(p.SocketPath("notes")); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}
	a.send(3, "tools/call", map[string]any{"name": "search"})
	if text, isErr := callText(t, a.next(2*time.Second)); !isErr || text != AwayText("notes") {
		t.Errorf("after the hold ended: %q %v", text, isErr)
	}
	// The registration stays; a second forward with other tools says so.
	srv2 := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	fl2, _, _ := startHold(t, p, srv2, "notes")
	if r := readyOf(t, fl2); r.New || !r.Changed || r.Tools != 1 {
		t.Errorf("second ready = %+v", r)
	}
}

// The ninth connection to one name is turned away with the busy line, and
// the shim says why.
func TestHoldCapsSessions(t *testing.T) {
	fast(t)
	p := testPaths(t)
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	fl, _, _ := startHold(t, p, srv, "notes")
	readyOf(t, fl)
	var conns []net.Conn
	for i := 0; i < MaxSessions; i++ {
		c, err := net.Dial("unix", p.SocketPath("notes"))
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		_, _ = c.Write(request("x", "initialize", map[string]any{}))
		if _, err := newLineReader(c).next(); err != nil {
			t.Fatalf("session %d: %v", i+1, err)
		}
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	c, err := net.Dial("unix", p.SocketPath("notes"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(c)
	if !bytes.Contains(b, []byte(busyMethod)) {
		t.Errorf("ninth connection got %q", b)
	}
	fl.mu.Lock()
	opens := fl.opens
	fl.mu.Unlock()
	if opens != MaxSessions+1 { // the fill, and eight sessions
		t.Errorf("laptop saw %d opens", opens)
	}
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool {
		a.send(2, "tools/call", map[string]any{"name": "search"})
		text, isErr := callText(t, a.next(2*time.Second))
		return isErr && strings.Contains(text, "already runs 8 copies")
	})
}

// A second hold of the same name takes the socket; the first says so to
// its laptop and ends.
func TestHoldTakeover(t *testing.T) {
	fast(t)
	p := testPaths(t)
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	fl1, _, code1 := startHold(t, p, srv, "notes")
	readyOf(t, fl1)
	fl2, _, _ := startHold(t, p, srv, "notes")
	readyOf(t, fl2)
	select {
	case n := <-fl1.gone:
		if n != "notes" {
			t.Errorf("gone %q", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first hold never handed over")
	}
	select {
	case <-code1:
	case <-time.After(5 * time.Second):
		t.Fatal("the first hold did not end")
	}
	if _, err := os.Stat(p.SocketPath("notes")); err != nil {
		t.Errorf("the second hold's socket: %v", err)
	}
}

// --remove deletes the registration and names what was not there.
func TestHoldRemove(t *testing.T) {
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	var out, errb bytes.Buffer
	if c := hold(p, []string{"--remove", "notes", "figma"}, nil, &out, &errb); c != 0 {
		t.Fatalf("exit %d: %s", c, errb.String())
	}
	if _, ok, _ := mcpreg.LoadForward(p, "notes"); ok {
		t.Error("forward file still there")
	}
	if out.String() != "figma\n" {
		t.Errorf("stdout %q", out.String())
	}
	if c := hold(p, []string{"bad name"}, nil, &out, &errb); c != 2 {
		t.Errorf("a bad name exited %d", c)
	}
	if c := hold(p, []string{"sync"}, nil, &out, &errb); c != 2 {
		t.Errorf("a reserved name exited %d", c)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	fw := NewFrameWriter(&buf)
	big := bytes.Repeat([]byte("x"), chunk*2+5)
	_ = fw.Write(FrameOpen, 7, []byte("notes"))
	_ = fw.Data(7, big)
	f, err := ReadFrame(&buf)
	if err != nil || f.Type != FrameOpen || f.ID != 7 || string(f.Payload) != "notes" {
		t.Fatalf("%+v %v", f, err)
	}
	var got []byte
	for i := 0; i < 3; i++ {
		f, err := ReadFrame(&buf)
		if err != nil || f.Type != FrameData || f.ID != 7 {
			t.Fatalf("%+v %v", f, err)
		}
		got = append(got, f.Payload...)
	}
	if !bytes.Equal(got, big) {
		t.Error("data did not round-trip")
	}
	if _, err := ReadFrame(bytes.NewReader([]byte{'D', 0, 0, 0, 1, 0xff, 0xff, 0xff, 0xff})); err == nil {
		t.Error("an oversized frame was accepted")
	}
}

// The laptop's keepalive touches NAME.alive; status calls the laptop away
// once it is stale, and the file goes with the hold.
func TestHoldKeepalive(t *testing.T) {
	fast(t)
	p := testPaths(t)
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	fl, laptop, code := startHold(t, p, srv, "notes")
	readyOf(t, fl)
	if p.LaptopAway("notes", time.Now()) {
		t.Fatal("away with the socket up and no keepalive yet (a CLI that sends none)")
	}
	_ = fl.fw.Write(FrameAlive, 0, nil)
	waitFor(t, func() bool { _, err := os.Stat(p.AlivePath("notes")); return err == nil })
	if p.LaptopAway("notes", time.Now()) {
		t.Error("away right after a keepalive")
	}
	if !p.LaptopAway("notes", time.Now().Add(mcpreg.AliveStale+time.Second)) {
		t.Error("not away with a stale keepalive")
	}
	_ = laptop.Close()
	<-code
	if _, err := os.Stat(p.AlivePath("notes")); !os.IsNotExist(err) {
		t.Errorf("keepalive file left behind: %v", err)
	}
}

// The probe a waiting hold sends is answered by the hold itself: no
// stream opens, so no server starts on the laptop.
func TestHoldProbeStartsNoServer(t *testing.T) {
	fast(t)
	p := testPaths(t)
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	fl, _, _ := startHold(t, p, srv, "notes")
	readyOf(t, fl)
	fl.mu.Lock()
	before := fl.opens
	fl.mu.Unlock()
	if !holderLive(p.SocketPath("notes")) {
		t.Fatal("a serving hold is not live")
	}
	time.Sleep(100 * time.Millisecond)
	fl.mu.Lock()
	after := fl.opens
	fl.mu.Unlock()
	if after != before {
		t.Errorf("the probe opened %d streams", after-before)
	}
	if holderLive(p.SocketPath("figma")) {
		t.Error("a missing socket is live")
	}
	// A socket file a dead hold left is not live.
	l, err := net.Listen("unix", p.SocketPath("stale"))
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	if holderLive(p.SocketPath("stale")) {
		t.Error("a stale socket file is live")
	}
}

// Two attaches forward one name with --wait: the second waits while the
// first serves, says nothing, and takes the name when the first ends. A
// `repose mcp forward` (no --wait) takes it from a waiting hold, which
// takes it back once that forward ends, with no gone frame.
func TestHoldWaitKeepsNameWhileAnyAttachLives(t *testing.T) {
	fast(t)
	p := testPaths(t)
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	fl1, laptop1, code1 := startHold(t, p, srv, "--wait", "notes")
	readyOf(t, fl1)
	fl2, laptop2, _ := startHold(t, p, srv, "--wait", "notes")
	select {
	case r := <-fl2.ready:
		t.Fatalf("the second attach took the name while the first serves: %+v", r)
	case n := <-fl1.gone:
		t.Fatalf("the first attach lost %s", n)
	case <-time.After(500 * time.Millisecond):
	}
	_ = laptop1.Close() // the first attach ends
	<-code1
	if r := readyOf(t, fl2); r.Error != "" {
		t.Fatalf("second ready = %+v", r)
	}
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool {
		a.send(2, "tools/call", map[string]any{"name": "search"})
		text, _ := callText(t, a.next(2*time.Second))
		return text == "called search"
	})
	// An explicit forward takes over; the attach's hold waits quietly.
	fl3, laptop3, code3 := startHold(t, p, srv, "notes")
	readyOf(t, fl3)
	select {
	case n := <-fl2.gone:
		t.Fatalf("a waiting hold sent gone for %s", n)
	case <-time.After(500 * time.Millisecond):
	}
	_ = laptop3.Close()
	<-code3
	if r := readyOf(t, fl2); r.Error != "" {
		t.Fatalf("taken back: %+v", r)
	}
	waitFor(t, func() bool { return holderLive(p.SocketPath("notes")) })
	_ = laptop2.Close()
}

// A failure on the machine's side says so in the ready frame.
func TestHoldListenFailureIsTheMachines(t *testing.T) {
	fast(t)
	p := testPaths(t)
	// A directory where the socket goes: listen cannot replace it.
	if err := os.MkdirAll(p.SocketPath("notes")+"/x", 0o700); err != nil {
		t.Fatal(err)
	}
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	fl, _, _ := startHold(t, p, srv, "notes")
	r := readyOf(t, fl)
	if r.Where != WhereMachine || !strings.HasPrefix(r.Error, "cannot listen on notes's socket") {
		t.Errorf("ready = %+v", r)
	}
}
