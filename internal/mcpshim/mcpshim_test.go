package mcpshim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/mcpreg"
)

func testPaths(t *testing.T) mcpreg.Paths {
	t.Helper()
	home := t.TempDir()
	sock, err := os.MkdirTemp("", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sock) })
	return mcpreg.Paths{Home: home, Platform: filepath.Join(home, "none.json"), SecretsDir: filepath.Join(home, "secrets"), SocketDir: sock, Etc: home}
}

func fast(t *testing.T) {
	t.Helper()
	vars := []*dur{pingEvery, retryEvery, replayWait, fillWait, watchEvery}
	short := []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 2 * time.Second, 5 * time.Second, 50 * time.Millisecond}
	var old []time.Duration
	for i, v := range vars {
		old = append(old, v.get())
		v.set(short[i])
	}
	t.Cleanup(func() {
		for i, v := range vars {
			v.set(old[i])
		}
	})
}

func tool(name string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"name":%q,"inputSchema":{"type":"object"}}`, name))
}

func writeCache(t *testing.T, p mcpreg.Paths, name, version string, tools ...string) {
	t.Helper()
	f := &mcpreg.Forward{Version: 1, Name: name, ProtocolVersion: version,
		Initialize: json.RawMessage(`{"protocolVersion":"` + version + `","capabilities":{"tools":{}},"serverInfo":{"name":"probe","version":"1"}}`)}
	for _, n := range tools {
		f.Tools = append(f.Tools, tool(n))
	}
	if err := mcpreg.WriteForward(p, f); err != nil {
		t.Fatal(err)
	}
}

// fakeServer is the laptop's server as the shim sees it through the
// socket: one MCP server per connection.
type fakeServer struct {
	version   string
	tools     []string
	pings     bool // answer the shim's pings
	initDelay time.Duration
	// askRoots sends roots/list right after notifications/initialized;
	// rootsAnswer is the client's answer to it.
	askRoots    bool
	rootsAnswer string
	// closeFirst closes the first connection right after its tools/list.
	closeFirst bool
	// stalled accepts and never answers anything, pings included.
	stalled   bool
	mu        sync.Mutex
	inits     []json.RawMessage
	sawPing   bool
	conns     []net.Conn
	callsSeen int
}

func (f *fakeServer) listen(t *testing.T, path string) net.Listener {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			go f.handle(c)
		}
	}()
	return l
}

func (f *fakeServer) dropAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.conns = nil
}

func (f *fakeServer) handle(c io.ReadWriteCloser) {
	defer func() { _ = c.Close() }()
	lr := newLineReader(c)
	for {
		line, err := lr.next()
		if err != nil {
			return
		}
		m, _ := parse(line)
		if f.stalled {
			continue // a hold whose laptop sleeps: nothing answers
		}
		if m.Method == "initialize" && f.initDelay > 0 {
			// A laptop server that starts slowly; the laptop end still
			// answers pings meanwhile.
			go func() {
				time.Sleep(f.initDelay)
				f.mu.Lock()
				defer f.mu.Unlock()
				f.inits = append(f.inits, m.Params)
				_, _ = c.Write(result(m.ID, map[string]any{"protocolVersion": f.version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "probe", "version": "1"}}))
			}()
			continue
		}
		f.mu.Lock()
		switch m.Method {
		case "notifications/initialized":
			if f.askRoots {
				_, _ = c.Write(request("srv-roots", "roots/list", nil))
			}
		case "":
			if string(m.ID) == `"srv-roots"` {
				f.rootsAnswer = string(m.Result)
			}
		case "initialize":
			f.inits = append(f.inits, m.Params)
			_, _ = c.Write(result(m.ID, map[string]any{"protocolVersion": f.version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "probe", "version": "1"}}))
		case "tools/list":
			var ts []json.RawMessage
			for _, n := range f.tools {
				ts = append(ts, tool(n))
			}
			_, _ = c.Write(result(m.ID, map[string]any{"tools": ts}))
			if f.closeFirst && len(f.inits) == 1 {
				f.mu.Unlock()
				return
			}
		case "tools/call":
			f.callsSeen++
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.Name != "hang" {
				_, _ = c.Write(result(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "called " + p.Name}}}))
			}
		case PingMethod:
			f.sawPing = true
			if f.pings {
				_, _ = c.Write(Pong(m.ID))
			}
		}
		f.mu.Unlock()
	}
}

// agent drives a shim over pipes the way an agent's MCP client does.
type agent struct {
	t      *testing.T
	in     io.WriteCloser
	lines  chan msg
	stderr *syncBuf
	done   chan int
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startShim(t *testing.T, p mcpreg.Paths, name string) *agent {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	a := &agent{t: t, in: inW, lines: make(chan msg, 64), stderr: &syncBuf{}, done: make(chan int, 1)}
	go func() { a.done <- serve(p, name, inR, outW, a.stderr); _ = outW.Close() }()
	go func() {
		lr := newLineReader(outR)
		for {
			line, err := lr.next()
			if err != nil {
				close(a.lines)
				return
			}
			m, ok := parse(line)
			if !ok {
				t.Errorf("shim wrote %q", line)
			}
			a.lines <- m
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return a
}

func (a *agent) send(id int, method string, params any) {
	a.t.Helper()
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id > 0 {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	if _, err := a.in.Write(encode(m)); err != nil {
		a.t.Fatal(err)
	}
}

// next is the next message the shim wrote, or fails after d.
func (a *agent) next(d time.Duration) msg {
	a.t.Helper()
	select {
	case m, ok := <-a.lines:
		if !ok {
			a.t.Fatal("the shim closed its stdout")
		}
		return m
	case <-time.After(d):
		a.t.Fatalf("nothing from the shim in %s", d)
	}
	return msg{}
}

func (a *agent) initialize(version string) map[string]any {
	a.t.Helper()
	a.send(1, "initialize", map[string]any{"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "claude-code", "version": "2"}})
	m := a.next(2 * time.Second)
	var res map[string]any
	if err := json.Unmarshal(m.Result, &res); err != nil || string(m.ID) != "1" {
		a.t.Fatalf("initialize answered %+v", m)
	}
	a.send(0, "notifications/initialized", nil)
	return res
}

func callText(t *testing.T, m msg) (string, bool) {
	t.Helper()
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(m.Result, &r); err != nil || len(r.Content) == 0 {
		t.Fatalf("not a tool result: %+v", m)
	}
	return r.Content[0].Text, r.IsError
}

// With no laptop, the shim answers from the cache at once and each call is
// a tool error that says what to do.
func TestShimAnswersWhileAway(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-03-26", "search", "create")
	a := startShim(t, p, "notes")
	res := a.initialize("2025-06-18")
	if res["protocolVersion"] != "2025-03-26" {
		t.Errorf("told %v, want the older cached revision", res["protocolVersion"])
	}
	caps := res["capabilities"].(map[string]any)["tools"].(map[string]any)
	if caps["listChanged"] != true {
		t.Errorf("capabilities.tools = %v, want listChanged", caps)
	}
	a.send(2, "tools/list", nil)
	m := a.next(time.Second)
	var tl struct {
		Tools []json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(m.Result, &tl)
	if len(tl.Tools) != 2 {
		t.Errorf("tools/list = %s", m.Result)
	}
	start := time.Now()
	a.send(3, "tools/call", map[string]any{"name": "search", "arguments": map[string]any{}})
	text, isErr := callText(t, a.next(time.Second))
	if !isErr || text != AwayText("notes") || time.Since(start) > time.Second {
		t.Errorf("call = %q isError=%v after %s", text, isErr, time.Since(start))
	}
	a.send(4, "resources/list", nil)
	if m := a.next(time.Second); !strings.Contains(string(m.Result), `"resources":[]`) {
		t.Errorf("resources/list = %+v", m)
	}
	a.send(5, "completion/complete", nil)
	if m := a.next(time.Second); len(m.Error) == 0 {
		t.Errorf("unknown method = %+v, want an error", m)
	}
	a.send(6, "ping", nil)
	if m := a.next(time.Second); string(m.Result) != "{}" {
		t.Errorf("ping = %+v", m)
	}
}

// Once the socket is up, the agent's initialize is replayed to the
// laptop's server and calls pass through; the same tools send no
// list_changed.
func TestShimReplaysAndPasses(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return len(srv.inits) == 1 })
	srv.mu.Lock()
	if !strings.Contains(string(srv.inits[0]), `"claude-code"`) {
		t.Errorf("replayed initialize = %s, want the agent's clientInfo", srv.inits[0])
	}
	srv.mu.Unlock()
	text := ""
	waitFor(t, func() bool {
		a.send(7, "tools/call", map[string]any{"name": "search"})
		m := a.next(2 * time.Second)
		text, _ = callText(t, m)
		return text == "called search"
	})
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.sawPing })
	select {
	case m := <-a.lines:
		t.Errorf("unexpected %+v", m)
	default:
	}
}

// Tools that differ on reconnect reach the agent as list_changed.
func TestShimListChangedOnReconnect(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search", "create"}, pings: true}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	if m := a.next(3 * time.Second); m.Method != "notifications/tools/list_changed" {
		t.Fatalf("got %+v, want list_changed", m)
	}
	a.send(8, "tools/list", nil)
	if m := a.next(2 * time.Second); !strings.Contains(string(m.Result), `"create"`) {
		t.Errorf("tools/list after the change = %s", m.Result)
	}
}

// A server that chose another revision than the agent was told: the shim
// says so on stderr and keeps passing calls.
func TestShimProtocolMismatch(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	srv := &fakeServer{version: "2024-11-05", tools: []string{"search"}, pings: true}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool {
		a.send(9, "tools/call", map[string]any{"name": "search"})
		text, _ := callText(t, a.next(2*time.Second))
		return text == "called search"
	})
	if s := a.stderr.String(); !strings.Contains(s, "chose protocol 2024-11-05; the agent was told 2025-06-18") {
		t.Errorf("stderr = %q", s)
	}
}

// Two unanswered pings mark the laptop away: the call in flight and the
// next one are tool errors, well before sshd would notice.
func TestShimPingMissesMarkAway(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search", "hang")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search", "hang"}}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.sawPing })
	a.send(10, "tools/call", map[string]any{"name": "hang"})
	text, isErr := callText(t, a.next(2*time.Second))
	if !isErr || text != AwayText("notes") {
		t.Errorf("pending call = %q, %v", text, isErr)
	}
}

// A socket that closes answers the call in flight at once.
func TestShimSocketClosed(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "hang")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"hang"}, pings: true}
	l := srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.sawPing })
	a.send(11, "tools/call", map[string]any{"name": "hang"})
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.callsSeen == 1 })
	_ = l.Close()
	srv.dropAll()
	text, isErr := callText(t, a.next(2*time.Second))
	if !isErr || text != AwayText("notes") {
		t.Errorf("call in flight = %q, %v", text, isErr)
	}
	a.send(12, "tools/call", map[string]any{"name": "hang"})
	if text, isErr := callText(t, a.next(time.Second)); !isErr || text != AwayText("notes") {
		t.Errorf("next call = %q, %v", text, isErr)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// While the first connect is still replaying to a slow laptop server, a
// call waits for it and reaches the server; the list methods answer from
// the cache at once.
func TestShimHoldsCallsWhileConnecting(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true, initDelay: 700 * time.Millisecond}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	start := time.Now()
	a.send(2, "tools/list", nil)
	if m := a.next(time.Second); string(m.ID) != "2" || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("tools/list = %+v after %s, want the cache at once", m, time.Since(start))
	}
	a.send(3, "tools/call", map[string]any{"name": "search"})
	text, isErr := callText(t, a.next(3*time.Second))
	if isErr || text != "called search" {
		t.Fatalf("call during the connect = %q isError=%v, want it passed to the server", text, isErr)
	}
}

// A request the server sends before the link is up (roots/list right
// after initialized) reaches the agent, and the agent's answer reaches
// the server.
func TestShimPassesServerRequestsBeforeUp(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true, askRoots: true}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	m := a.next(3 * time.Second)
	if m.Method != "roots/list" || string(m.ID) != `"srv-roots"` {
		t.Fatalf("got %+v, want the server's roots/list", m)
	}
	if _, err := a.in.Write(result(m.ID, map[string]any{"roots": []any{}})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.rootsAnswer != "" })
}

// A link that closes between its last tools/list and becoming live is
// not installed: the shim reconnects without waiting for the agent.
func TestShimReconnectsAfterEarlyClose(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, pings: true, closeFirst: true}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	waitFor(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return len(srv.inits) >= 2 })
	waitFor(t, func() bool {
		a.send(4, "tools/call", map[string]any{"name": "search"})
		text, _ := callText(t, a.next(2*time.Second))
		return text == "called search"
	})
}

// A Queue never blocks the one who pushes: past MaxQueued it refuses and
// ends, and what it took comes out in order.
func TestQueueNeverBlocks(t *testing.T) {
	q := NewQueue()
	b := make([]byte, 1<<20)
	done := make(chan int)
	go func() {
		n := 0
		for q.Push(b) {
			n++
		}
		done <- n
	}()
	select {
	case n := <-done:
		if n != MaxQueued/len(b) {
			t.Fatalf("took %d MB before refusing", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Push blocked with no reader")
	}
	if _, ok := q.Next(); ok {
		t.Fatal("a queue past its bound still gives data")
	}
	q2 := NewQueue()
	q2.Push([]byte("a"))
	q2.Push([]byte("b"))
	q2.Close()
	var got []string
	for {
		x, ok := q2.Next()
		if !ok {
			break
		}
		got = append(got, string(x))
	}
	if strings.Join(got, "") != "ab" {
		t.Fatalf("got %q", got)
	}
}

// A cache whose initialize is null (a laptop server that answered with a
// null result before hold refused one) still answers initialize.
func TestShimNullCachedInitialize(t *testing.T) {
	fast(t)
	p := testPaths(t)
	if err := os.MkdirAll(filepath.Join(p.Home, ".repose/mcp/forward"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, init := range []string{"null", `"x"`, "[]"} {
		if err := os.WriteFile(filepath.Join(p.Home, ".repose/mcp/forward/notes.json"), []byte(`{"version":1,"name":"notes","initialize":`+init+`,"tools":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		a := startShim(t, p, "notes")
		res := a.initialize("2025-06-18")
		if res["protocolVersion"] != "2025-06-18" || res["capabilities"] == nil {
			t.Errorf("initialize %s: %v", init, res)
		}
	}
}

// hold refuses a server whose initialize result is null, so no such
// cache is written.
func TestFetchRefusesNullInitialize(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	go func() {
		lr := newLineReader(b)
		line, err := lr.next()
		if err != nil {
			return
		}
		m, _ := parse(line)
		_, _ = b.Write([]byte(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":null}` + "\n"))
		_, _ = io.Copy(io.Discard, b)
	}()
	_, _, _, err := fetch(a)
	if err == nil || !strings.Contains(err.Error(), "no result object") {
		t.Errorf("fetch = %v", err)
	}
}

// A hold that holds the socket for a laptop that sleeps (sshd has not
// reaped it yet) answers nothing: the call waiting on the connect gets
// the away text after two missed pings, well before replayWait, and the
// next call answers at once.
func TestShimStalledHoldAnswersAway(t *testing.T) {
	fast(t)
	p := testPaths(t)
	writeCache(t, p, "notes", "2025-06-18", "search")
	srv := &fakeServer{version: "2025-06-18", tools: []string{"search"}, stalled: true}
	srv.listen(t, p.SocketPath("notes"))
	a := startShim(t, p, "notes")
	a.initialize("2025-06-18")
	start := time.Now()
	a.send(3, "tools/call", map[string]any{"name": "search"})
	text, isErr := callText(t, a.next(3*time.Second))
	if !isErr || text != AwayText("notes") {
		t.Fatalf("call = %q isError=%v", text, isErr)
	}
	if d := time.Since(start); d > replayWait.get()/2 {
		t.Errorf("away after %s; replayWait is %s", d, replayWait.get())
	}
	start = time.Now()
	a.send(4, "tools/call", map[string]any{"name": "search"})
	if text, isErr := callText(t, a.next(time.Second)); !isErr || text != AwayText("notes") || time.Since(start) > 200*time.Millisecond {
		t.Errorf("next call = %q isError=%v after %s, want away at once", text, isErr, time.Since(start))
	}
}
