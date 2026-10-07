package mcpshim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/mcpreg"
)

// MaxSessions is the most connections hold runs at once for one name, and
// so the most server processes the laptop starts for it.
const MaxSessions = 8

// Timings, variables so tests can shorten them.
var (
	// fillWait bounds the cache fill: a first `npx -y` download is slow.
	fillWait = newDur(90 * time.Second)
	// watchEvery is how often hold checks that its socket is still its own.
	watchEvery = newDur(time.Second)
)

// Hold is `repose-mcp hold NAME...`, the guest end of `repose mcp forward`
// (DECISIONS I-557), and `repose-mcp hold --remove NAME...`. Its stdin and
// stdout are the laptop's ssh, carrying frames (frame.go). For each NAME
// it fills forward/NAME.json through the laptop, syncs the agents on a
// first registration, listens on /run/repose/mcp/NAME.sock, and carries
// each shim's connection to the laptop as a stream of its own. It returns
// when stdin ends or every NAME has gone to another hold.
func Hold(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(stderr, "repose-mcp: no home directory")
		return 1
	}
	return hold(mcpreg.DefaultPaths(home), args, stdin, stdout, stderr)
}

const holdUsage = "usage: repose-mcp hold [--wait] NAME...\n       repose-mcp hold --remove NAME...\n"

func hold(p mcpreg.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	remove := len(args) > 0 && args[0] == "--remove"
	if remove {
		args = args[1:]
	}
	wait := !remove && len(args) > 0 && args[0] == "--wait"
	if wait {
		args = args[1:]
	}
	var names []string
	seen := map[string]bool{}
	for _, n := range args {
		if !mcpreg.ValidName(n) || mcpreg.Reserved[n] {
			fmt.Fprintf(stderr, "repose-mcp: %q is not a server name repose can forward\n", n)
			return 2
		}
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		fmt.Fprint(stderr, holdUsage)
		return 2
	}
	if remove {
		return removeForwards(p, names, stdout, stderr)
	}
	h := newHolder(p, stdin, stdout, stderr)
	h.wait = wait
	return h.run(names)
}

// removeForwards deletes each forward/NAME.json and syncs. Names with no
// file are printed on stdout, one per line, for the CLI to report.
func removeForwards(p mcpreg.Paths, names []string, stdout, stderr io.Writer) int {
	removed := false
	for _, n := range names {
		ok, err := mcpreg.RemoveForward(p, n)
		if err != nil {
			fmt.Fprintf(stderr, "repose-mcp: cannot remove ~/.repose/mcp/forward/%s.json: %v\n", n, err)
			return 1
		}
		if !ok {
			fmt.Fprintln(stdout, n)
		}
		removed = removed || ok
	}
	if removed {
		mcpreg.SyncRendered(p, stderr)
	}
	return 0
}

type holder struct {
	p      mcpreg.Paths
	in     io.Reader
	fw     *FrameWriter
	stderr io.Writer

	mu      sync.Mutex
	next    uint32
	streams map[uint32]*stream
	// serving is each name whose socket this hold owns now.
	serving map[string]bool
	// done closes when the laptop's side ends.
	done chan struct{}
	// wait is --wait, the attach's forward: take NAME only while no
	// other live hold serves it, and take it back when that one ends
	// or hands over, instead of ending (DECISIONS I-557).
	wait bool
}

// stream is one connection carried to the laptop.
type stream struct {
	name     string
	conn     net.Conn
	q        *Queue // from the laptop, written to conn in order
	internal bool   // the cache fill, not a shim
}

func (s *stream) end() { s.q.Close() }

func newHolder(p mcpreg.Paths, in io.Reader, out, stderr io.Writer) *holder {
	return &holder{p: p, in: in, fw: NewFrameWriter(out), stderr: stderr, streams: map[uint32]*stream{}, serving: map[string]bool{}, done: make(chan struct{})}
}

func (h *holder) run(names []string) int {
	if err := h.fw.Write(FrameHello, 0, []byte(FrameVersion)); err != nil {
		return 1
	}
	go h.readLoop()
	if err := os.MkdirAll(h.p.SocketDir, 0o700); err != nil {
		fmt.Fprintf(h.stderr, "repose-mcp: cannot make %s: %v\n", h.p.SocketDir, err)
		return 1
	}
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			h.serveName(n)
		}(n)
	}
	wg.Wait()
	return 0
}

// readLoop takes frames from the laptop until its side ends.
func (h *holder) readLoop() {
	defer func() {
		close(h.done)
		h.mu.Lock()
		for id, s := range h.streams {
			delete(h.streams, id)
			s.end()
		}
		h.mu.Unlock()
	}()
	for {
		f, err := ReadFrame(h.in)
		if err != nil {
			return
		}
		switch f.Type {
		case FrameData:
			h.mu.Lock()
			s := h.streams[f.ID]
			h.mu.Unlock()
			if s != nil && !s.q.Push(f.Payload) {
				// A shim that stopped reading: end its stream rather
				// than stall every other one behind it.
				_ = h.fw.Write(FrameClose, f.ID, nil)
				h.drop(f.ID)
			}
		case FrameClose:
			h.drop(f.ID)
		case FrameAlive:
			h.alive()
		}
	}
}

func (h *holder) drop(id uint32) {
	h.mu.Lock()
	s := h.streams[id]
	delete(h.streams, id)
	h.mu.Unlock()
	if s != nil {
		s.end()
	}
}

// alive touches NAME.alive for each name this hold serves.
func (h *holder) alive() {
	h.mu.Lock()
	var names []string
	for n := range h.serving {
		names = append(names, n)
	}
	h.mu.Unlock()
	now := time.Now()
	for _, n := range names {
		path := h.p.AlivePath(n)
		if err := os.Chtimes(path, now, now); err != nil {
			_ = os.WriteFile(path, nil, 0o600)
		}
	}
}

// setServing marks name as this hold's, or no longer; on, it drops a
// keepalive file an earlier hold left, which a laptop that sends none
// would leave stale.
func (h *holder) setServing(name string, on bool) {
	h.mu.Lock()
	if on {
		h.serving[name] = true
	} else {
		delete(h.serving, name)
	}
	h.mu.Unlock()
	if on {
		_ = os.Remove(h.p.AlivePath(name))
	}
}

// live is how many shims NAME has connected now.
func (h *holder) live(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, s := range h.streams {
		if s.name == name && !s.internal {
			n++
		}
	}
	return n
}

// open carries conn to the laptop as a new stream for name; first is what
// was already read from conn.
func (h *holder) open(name string, conn net.Conn, internal bool, first []byte) {
	s := &stream{name: name, conn: conn, q: NewQueue(), internal: internal}
	h.mu.Lock()
	h.next++
	id := h.next
	h.streams[id] = s
	h.mu.Unlock()
	select {
	case <-h.done:
		h.drop(id)
	default:
	}
	go func() {
		for {
			b, ok := s.q.Next()
			if !ok {
				break
			}
			if _, err := conn.Write(b); err != nil {
				s.q.Abort()
				break
			}
		}
		_ = conn.Close()
	}()
	if err := h.fw.Write(FrameOpen, id, []byte(name)); err != nil {
		h.drop(id)
		return
	}
	if len(first) > 0 && h.fw.Data(id, first) != nil {
		h.drop(id)
		return
	}
	go func() {
		buf := make([]byte, chunk)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if h.fw.Data(id, buf[:n]) != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		h.mu.Lock()
		_, still := h.streams[id]
		h.mu.Unlock()
		if still {
			_ = h.fw.Write(FrameClose, id, nil)
			h.drop(id)
		}
	}()
}

// serveName registers name and serves its socket until the laptop's side
// ends or another hold takes the socket. With --wait it first waits for
// no other live hold to serve name, and after a takeover waits again.
func (h *holder) serveName(name string) {
	for {
		if h.wait && !h.waitFree(name) {
			return
		}
		if !h.serveOnce(name) || !h.wait {
			return
		}
	}
}

// waitFree waits until no live hold answers on name's socket; false when
// the laptop's side ended first.
func (h *holder) waitFree(name string) bool {
	t := time.NewTicker(watchEvery.get())
	defer t.Stop()
	for holderLive(h.p.SocketPath(name)) {
		select {
		case <-h.done:
			return false
		case <-t.C:
		}
	}
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

// holderLive asks the socket's hold whether it serves: a probe line it
// answers itself, so no server starts on the laptop. A hold at its cap
// answers busy, which is live too.
func holderLive(path string) bool {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return false
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write(notification(probeMethod)); err != nil {
		return false
	}
	_, err = newLineReader(c).next()
	return err == nil
}

// probeMethod is the line a waiting hold sends to ask whether a hold
// serves a socket; the hold answers it and opens no stream.
const probeMethod = "$/repose/holder"

// firstLineWait bounds how long hold waits for a connection's first line;
// a shim sends its replayed initialize at once.
var firstLineWait = newDur(30 * time.Second)

// accept reads a connection's first line: the probe is answered here,
// anything else opens a stream that starts with it.
func (h *holder) accept(name string, c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(firstLineWait.get()))
	br := bufio.NewReaderSize(c, chunk)
	line, err := br.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	if m, ok := parse(bytes.TrimSpace(line)); ok && m.Method == probeMethod {
		_, _ = c.Write(notification(probeMethod))
		_ = c.Close()
		return
	}
	first := append(line, make([]byte, br.Buffered())...)
	_, _ = io.ReadFull(br, first[len(line):])
	h.open(name, c, false, first)
}

// serveOnce is one registration of name: it reports whether another hold
// took the socket over.
func (h *holder) serveOnce(name string) bool {
	r := h.fill(name)
	var l net.Listener
	var mine os.FileInfo
	if r.Error == "" {
		var err error
		l, mine, err = h.listen(name)
		if err != nil {
			r = Ready{Name: name, Error: "cannot listen on " + name + "'s socket: " + err.Error(), Where: WhereMachine}
		}
	}
	if l != nil {
		h.setServing(name, true)
		defer h.setServing(name, false)
	}
	_ = h.fw.Write(FrameReady, 0, ReadyFrame(r))
	if l == nil {
		return false
	}
	path := h.p.SocketPath(name)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			if h.live(name) >= MaxSessions {
				_, _ = c.Write(notification(busyMethod))
				_ = c.Close()
				continue
			}
			go h.accept(name, c)
		}
	}()
	t := time.NewTicker(watchEvery.get())
	defer t.Stop()
	for {
		select {
		case <-h.done:
			_ = l.Close()
			if fi, err := os.Stat(path); err == nil && os.SameFile(fi, mine) {
				_ = os.Remove(path)
				_ = os.Remove(h.p.AlivePath(name))
			}
			return false
		case <-t.C:
			if fi, err := os.Stat(path); err != nil || !os.SameFile(fi, mine) {
				// Another hold bound the name (a newer forward of it):
				// hand over, ending this one's shims so they reconnect.
				// A waiting hold says nothing and waits to take it back.
				_ = l.Close()
				h.endName(name)
				if !h.wait {
					_ = h.fw.Write(FrameGone, 0, []byte(name))
				}
				return true
			}
		}
	}
}

// endName ends every stream of name.
func (h *holder) endName(name string) {
	h.mu.Lock()
	var ids []uint32
	for id, s := range h.streams {
		if s.name == name {
			ids = append(ids, id)
		}
	}
	h.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		_ = h.fw.Write(FrameClose, id, nil)
		h.drop(id)
	}
}

// listen binds name's socket, replacing one an earlier hold left or holds.
func (h *holder) listen(name string) (net.Listener, os.FileInfo, error) {
	path := h.p.SocketPath(name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, nil, err
	}
	if ul, ok := l.(*net.UnixListener); ok {
		// Close removes the path; hold removes it itself, and only when
		// it is still its own.
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	return l, fi, nil
}

// fill asks the laptop's server for its initialize result and tools and
// writes forward/NAME.json.
func (h *holder) fill(name string) Ready {
	a, b := net.Pipe()
	h.open(name, a, true, nil)
	defer func() { _ = b.Close() }()
	_ = b.SetDeadline(time.Now().Add(fillWait.get()))
	init, version, tools, err := fetch(b)
	if err != nil {
		return Ready{Name: name, Error: err.Error()}
	}
	old, had, _ := mcpreg.LoadForward(h.p, name)
	f := &mcpreg.Forward{
		Version:         1,
		Name:            name,
		ProtocolVersion: version,
		Initialize:      init,
		Tools:           tools,
		Updated:         time.Now().UTC().Format(time.RFC3339),
	}
	if f.Tools == nil {
		f.Tools = []json.RawMessage{}
	}
	if err := mcpreg.WriteForward(h.p, f); err != nil {
		return Ready{Name: name, Error: "cannot write ~/.repose/mcp/forward/" + name + ".json: " + err.Error(), Where: WhereMachine}
	}
	r := Ready{Name: name, Tools: len(tools), New: !had}
	if had && old != nil {
		r.Changed = !sameJSON(old.Tools, f.Tools)
	}
	if r.New {
		mcpreg.SyncRendered(h.p, h.stderr)
	}
	return r
}

// errEnded is a server that closed before it answered.
var errEnded = errors.New("the server ended before it answered")

// fetch is an MCP client's start, then tools/list page by page.
func fetch(c net.Conn) (init json.RawMessage, version string, tools []json.RawMessage, err error) {
	lr := newLineReader(c)
	call := func(id, method string, params any) (json.RawMessage, error) {
		if _, err := c.Write(request(id, method, params)); err != nil {
			return nil, errEnded
		}
		for {
			line, err := lr.next()
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) {
					return nil, fmt.Errorf("no answer to %s in %s", method, fillWait.get())
				}
				return nil, errEnded
			}
			m, ok := parse(line)
			if !ok {
				continue
			}
			if m.request() {
				// roots/list and the like: this client offers nothing.
				_, _ = c.Write(rpcError(m.ID, -32601, "not offered"))
				continue
			}
			if !m.response() || string(m.ID) != `"`+id+`"` {
				continue
			}
			if len(m.Error) > 0 {
				var e struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(m.Error, &e)
				return nil, fmt.Errorf("%s failed: %s", method, e.Message)
			}
			return m.Result, nil
		}
	}
	init, err = call("repose-init", "initialize", map[string]any{
		"protocolVersion": LatestProtocol,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "repose-mcp", "version": "1"},
	})
	if err != nil {
		return nil, "", nil, err
	}
	var ir struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(init, &ir); err != nil {
		return nil, "", nil, fmt.Errorf("initialize answered %v", err)
	}
	if !jsonObject(init) {
		return nil, "", nil, errors.New("initialize answered with no result object")
	}
	if _, err := c.Write(notification("notifications/initialized")); err != nil {
		return nil, "", nil, errEnded
	}
	tools = []json.RawMessage{}
	if _, ok := ir.Capabilities["tools"]; !ok {
		return init, ir.ProtocolVersion, tools, nil
	}
	var cursor string
	for page := 0; page < 100; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		res, err := call(fmt.Sprintf("repose-tools-%d", page), "tools/list", params)
		if err != nil {
			return nil, "", nil, err
		}
		var tl struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(res, &tl); err != nil {
			return nil, "", nil, fmt.Errorf("tools/list answered %v", err)
		}
		tools = append(tools, tl.Tools...)
		if tl.NextCursor == "" {
			break
		}
		cursor = tl.NextCursor
	}
	return init, ir.ProtocolVersion, tools, nil
}

// jsonObject is whether b is a JSON object; null decodes into a struct
// without error.
func jsonObject(b json.RawMessage) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '{'
}
