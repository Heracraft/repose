// Package mcpshim is the guest side of `repose mcp forward` (DECISIONS
// I-557): `repose-mcp NAME`, the stdio server agents start for a server
// that runs on the laptop, and `repose-mcp hold NAME...`, the endpoint the
// laptop's ssh holds open. repose-mcp's dispatch (cmd/repose-hook/mcp.go)
// routes both here.
//
// The shim answers initialize and tools/list from ~/.repose/mcp/forward/
// NAME.json, so an agent's start never waits on the laptop. Once the agent
// has initialized it connects to /run/repose/mcp/NAME.sock, replays the
// agent's initialize to the laptop's server and from then on passes lines
// both ways. It pings through the link every 10 s; two missed pings, or
// the socket closing, mark the laptop away, and from then on each
// tools/call answers isError with a line for the agent to act on. It
// retries every 5 s and sends notifications/tools/list_changed when the
// tools it finds on reconnect differ from the ones the agent has.
//
// Nothing here logs what passes through: stderr lines name the server and
// protocol versions only.
package mcpshim

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/heracraft/repose/internal/mcpreg"
)

var (
	pingEvery  = newDur(10 * time.Second)
	retryEvery = newDur(5 * time.Second)
	// replayWait bounds the replay on a connect: the laptop starts the
	// server for it.
	replayWait = newDur(60 * time.Second)
)

// dur is a timing tests shorten while goroutines of an earlier test may
// still read it.
type dur struct{ v atomic.Int64 }

func newDur(d time.Duration) *dur {
	x := &dur{}
	x.v.Store(int64(d))
	return x
}

func (d *dur) get() time.Duration   { return time.Duration(d.v.Load()) }
func (d *dur) set(to time.Duration) { d.v.Store(int64(to)) }

// AwayText is what a tools/call answers while the laptop is away.
func AwayText(name string) string {
	return fmt.Sprintf("%s runs on the user's laptop, which isn't connected. Ask them to run repose mcp forward %s there.", name, name)
}

// busyText is what a tools/call answers while hold turns this shim away.
func busyText(name string) string {
	return fmt.Sprintf("%s already runs %d copies on the user's laptop, the most one forward starts. End an agent session that uses it, then try again.", name, MaxSessions)
}

// Serve runs the shim for the forwarded server name on stdin and stdout.
// It returns the process exit code.
func Serve(name string, stdin io.Reader, stdout, stderr io.Writer) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(stderr, "repose-mcp: no home directory")
		return 1
	}
	return serve(mcpreg.DefaultPaths(home), name, stdin, stdout, stderr)
}

type shim struct {
	name   string
	sock   string
	stderr io.Writer
	cache  *mcpreg.Forward

	outMu sync.Mutex
	out   io.Writer

	mu          sync.Mutex
	initParams  json.RawMessage
	told        string // the protocol version the agent was told
	initialized bool
	tools       []json.RawMessage // what the agent has
	link        *link
	pending     map[string]string // agent request id -> method, sent over link
	busy        bool
	seq         int
	wake        chan struct{}
	quit        chan struct{}
}

func serve(p mcpreg.Paths, name string, stdin io.Reader, stdout, stderr io.Writer) int {
	s := &shim{
		name:    name,
		sock:    p.SocketPath(name),
		stderr:  stderr,
		out:     stdout,
		pending: map[string]string{},
		wake:    make(chan struct{}, 1),
		quit:    make(chan struct{}),
	}
	if f, ok, err := mcpreg.LoadForward(p, name); ok && err == nil {
		s.cache = f
		s.tools = f.Tools
	} else if err != nil {
		fmt.Fprintf(stderr, "repose-mcp: %s: ~/.repose/mcp/forward/%s.json does not parse; answering with no tools\n", name, name)
	}
	if s.tools == nil {
		s.tools = []json.RawMessage{}
	}
	go s.connector()
	lr := newLineReader(stdin)
	for {
		line, err := lr.next()
		if err != nil {
			break
		}
		s.fromAgent(line)
	}
	close(s.quit)
	s.mu.Lock()
	l := s.link
	s.link = nil
	s.mu.Unlock()
	if l != nil {
		l.close()
	}
	return 0
}

func (s *shim) toAgent(b []byte) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = s.out.Write(b)
}

func (s *shim) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *shim) privateID(kind string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("$repose-%s-%d", kind, s.seq)
}

func (s *shim) fromAgent(line []byte) {
	m, ok := parse(line)
	if !ok {
		s.mu.Lock()
		l := s.link
		s.mu.Unlock()
		if l != nil {
			_ = l.send(line)
		}
		return
	}
	switch {
	case m.Method == "initialize" && m.hasID():
		s.answerInitialize(m)
		return
	case m.Method == "notifications/initialized":
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
		s.poke()
		return
	}
	s.mu.Lock()
	l := s.link
	if l != nil && m.request() {
		s.pending[string(m.ID)] = m.Method
	}
	s.mu.Unlock()
	if l != nil {
		if err := l.send(line); err != nil {
			s.down(l) // answers this request with the others pending
		}
		return
	}
	s.answerLocally(m)
}

// answerInitialize answers from the cache, so the agent's start never
// waits on the laptop. The version is the agent's or the cached one,
// whichever is older (revisions are dates).
func (s *shim) answerInitialize(m msg) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(m.Params, &params)
	res := map[string]any{}
	cached := ""
	if s.cache != nil && len(s.cache.Initialize) > 0 {
		_ = json.Unmarshal(s.cache.Initialize, &res)
		cached = s.cache.ProtocolVersion
	}
	told := params.ProtocolVersion
	if told == "" || (cached != "" && cached < told) {
		told = cached
	}
	if told == "" {
		told = LatestProtocol
	}
	res["protocolVersion"] = told
	caps, _ := res["capabilities"].(map[string]any)
	if caps == nil {
		caps = map[string]any{}
	}
	tools, _ := caps["tools"].(map[string]any)
	if tools == nil {
		tools = map[string]any{}
	}
	// opencode registers its list_changed handler only when the server
	// declares tools (critic correction 9).
	tools["listChanged"] = true
	caps["tools"] = tools
	res["capabilities"] = caps
	if _, ok := res["serverInfo"]; !ok {
		res["serverInfo"] = map[string]any{"name": s.name, "version": "0"}
	}
	s.mu.Lock()
	s.initParams = m.Params
	s.told = told
	s.mu.Unlock()
	s.toAgent(result(m.ID, res))
}

// answerLocally answers while there is no link.
func (s *shim) answerLocally(m msg) {
	if !m.request() {
		return // notifications, and the agent's answers to a server gone away
	}
	s.mu.Lock()
	tools := s.tools
	text := AwayText(s.name)
	if s.busy {
		text = busyText(s.name)
	}
	s.mu.Unlock()
	switch m.Method {
	case "tools/list":
		s.toAgent(result(m.ID, map[string]any{"tools": tools}))
	case "ping":
		s.toAgent(result(m.ID, map[string]any{}))
	case "resources/list":
		s.toAgent(result(m.ID, map[string]any{"resources": []any{}}))
	case "resources/templates/list":
		s.toAgent(result(m.ID, map[string]any{"resourceTemplates": []any{}}))
	case "prompts/list":
		s.toAgent(result(m.ID, map[string]any{"prompts": []any{}}))
	case "tools/call":
		s.toAgent(toolError(m.ID, text))
	default:
		s.toAgent(rpcError(m.ID, -32000, text))
	}
}

func toolError(id json.RawMessage, text string) []byte {
	return result(id, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": true,
	})
}

// fromLaptop passes a line from the laptop's server to the agent. A
// response is passed only to a request still pending, so one that arrives
// after a reconnect never answers twice.
func (s *shim) fromLaptop(line []byte, m msg) {
	if m.response() {
		key := string(m.ID)
		s.mu.Lock()
		method, ok := s.pending[key]
		delete(s.pending, key)
		if ok && method == "tools/list" {
			var tl struct {
				Tools []json.RawMessage `json:"tools"`
			}
			if json.Unmarshal(m.Result, &tl) == nil && tl.Tools != nil {
				s.tools = tl.Tools
			}
		}
		s.mu.Unlock()
		if !ok {
			return
		}
	}
	s.toAgent(append(append([]byte{}, line...), '\n'))
}

// down ends l, answering every request still pending on it.
func (s *shim) down(l *link) {
	l.close()
	s.mu.Lock()
	if s.link != l {
		s.mu.Unlock()
		return
	}
	s.link = nil
	pending := s.pending
	s.pending = map[string]string{}
	text := AwayText(s.name)
	s.mu.Unlock()
	for id, method := range pending {
		if method == "tools/call" {
			s.toAgent(toolError(json.RawMessage(id), text))
		} else {
			s.toAgent(rpcError(json.RawMessage(id), -32000, text))
		}
	}
	s.poke()
}

// connector keeps a link up while the agent is initialized: at once when
// it initializes, then every retryEvery while the link is down.
func (s *shim) connector() {
	for {
		s.mu.Lock()
		init, up := s.initialized, s.link != nil
		s.mu.Unlock()
		if init && !up {
			s.connect()
			s.mu.Lock()
			up = s.link != nil
			s.mu.Unlock()
			if !up {
				if !s.sleep(retryEvery.get()) {
					return
				}
				continue
			}
		}
		select {
		case <-s.quit:
			return
		case <-s.wake:
		}
		s.mu.Lock()
		down := s.link == nil
		s.mu.Unlock()
		if up && down && !s.sleep(retryEvery.get()) {
			return // a link that just went down waits out one retry first
		}
	}
}

// sleep waits d, or returns false when the agent has gone.
func (s *shim) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.quit:
		return false
	case <-t.C:
		return true
	}
}

// connect replays the agent's initialize through the socket and makes the
// link the live one.
func (s *shim) connect() {
	c, err := net.DialTimeout("unix", s.sock, 2*time.Second)
	if err != nil {
		return
	}
	l := newLink(c)
	go l.readLoop(s)
	s.mu.Lock()
	params := s.initParams
	told := s.told
	s.mu.Unlock()
	res, err := l.call(s.privateID("init"), "initialize", params, replayWait.get())
	if err != nil {
		if l.wasBusy() {
			s.mu.Lock()
			s.busy = true
			s.mu.Unlock()
		}
		l.close()
		return
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(res, &ir)
	if ir.ProtocolVersion != "" && ir.ProtocolVersion != told {
		fmt.Fprintf(s.stderr, "repose-mcp: %s: the server on the laptop chose protocol %s; the agent was told %s\n", s.name, ir.ProtocolVersion, told)
	}
	if err := l.send(notification("notifications/initialized")); err != nil {
		l.close()
		return
	}
	var tools []json.RawMessage
	cursor := ""
	for page := 0; page < 100; page++ {
		var p any
		if cursor != "" {
			p = map[string]any{"cursor": cursor}
		}
		res, err := l.call(s.privateID("tools"), "tools/list", p, replayWait.get())
		if err != nil {
			l.close()
			return
		}
		var tl struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		_ = json.Unmarshal(res, &tl)
		tools = append(tools, tl.Tools...)
		if tl.NextCursor == "" {
			break
		}
		cursor = tl.NextCursor
	}
	if tools == nil {
		tools = []json.RawMessage{}
	}
	s.mu.Lock()
	select {
	case <-s.quit:
		s.mu.Unlock()
		l.close()
		return
	default:
	}
	changed := !sameJSON(tools, s.tools)
	s.tools = tools
	s.busy = false
	s.link = l
	l.setUp()
	s.mu.Unlock()
	if changed {
		s.toAgent(notification("notifications/tools/list_changed"))
	}
	go s.pinger(l)
}

// pinger marks the link down after two pings in a row go unanswered.
func (s *shim) pinger(l *link) {
	t := time.NewTicker(pingEvery.get())
	defer t.Stop()
	var last chan json.RawMessage
	missed := 0
	for {
		select {
		case <-l.closed:
			return
		case <-t.C:
		}
		if last != nil {
			select {
			case <-last:
				missed = 0
			default:
				missed++
			}
		}
		if missed >= 2 {
			s.down(l)
			return
		}
		id := s.privateID("ping")
		last = l.expect(id)
		if err := l.send(request(id, PingMethod, nil)); err != nil {
			s.down(l)
			return
		}
	}
}

// link is one connection to hold's socket.
type link struct {
	conn   net.Conn
	wmu    sync.Mutex
	mu     sync.Mutex
	waits  map[string]chan json.RawMessage
	up     bool
	busy   bool
	closed chan struct{}
	once   sync.Once
}

func newLink(c net.Conn) *link {
	return &link{conn: c, waits: map[string]chan json.RawMessage{}, closed: make(chan struct{})}
}

func (l *link) close() {
	l.once.Do(func() {
		close(l.closed)
		_ = l.conn.Close()
	})
}

func (l *link) setUp() {
	l.mu.Lock()
	l.up = true
	l.mu.Unlock()
}

func (l *link) wasBusy() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.busy
}

// send writes one line; a line from the agent is written whole.
func (l *link) send(line []byte) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line = append(append([]byte{}, line...), '\n')
	}
	_ = l.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := l.conn.Write(line)
	return err
}

// expect registers a private id whose response the shim keeps.
func (l *link) expect(id string) chan json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	l.mu.Lock()
	l.waits[`"`+id+`"`] = ch
	l.mu.Unlock()
	return ch
}

// call sends a private request and waits for its result.
func (l *link) call(id, method string, params any, wait time.Duration) (json.RawMessage, error) {
	ch := l.expect(id)
	var p any
	if len(asRaw(params)) > 0 {
		p = params
	}
	if err := l.send(request(id, method, p)); err != nil {
		return nil, err
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("%s failed", method)
		}
		return r, nil
	case <-l.closed:
		return nil, io.EOF
	case <-t.C:
		return nil, fmt.Errorf("no answer to %s", method)
	}
}

func asRaw(v any) json.RawMessage {
	switch x := v.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return x
	}
	return json.RawMessage("{}")
}

// readLoop takes lines from the laptop: private responses to their
// waiters, and, once the link is up, the rest to the agent.
func (l *link) readLoop(s *shim) {
	lr := newLineReader(l.conn)
	for {
		line, err := lr.next()
		if err != nil {
			break
		}
		m, ok := parse(line)
		if !ok {
			continue
		}
		if m.Method == busyMethod {
			l.mu.Lock()
			l.busy = true
			l.mu.Unlock()
			break
		}
		if m.response() {
			l.mu.Lock()
			ch, private := l.waits[string(m.ID)]
			delete(l.waits, string(m.ID))
			l.mu.Unlock()
			if private {
				if len(m.Error) > 0 {
					close(ch)
				} else {
					ch <- m.Result
				}
				continue
			}
		}
		l.mu.Lock()
		up := l.up
		l.mu.Unlock()
		if up {
			s.fromLaptop(line, m)
		}
	}
	l.close()
	s.mu.Lock()
	live := s.link == l
	s.mu.Unlock()
	if live {
		s.down(l)
	}
}
