//go:build !windows

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/mcpshim"
	"github.com/heracraft/repose/internal/testguest"
)

// testHelperEnv makes the test binary a helper process (TestMain).
const testHelperEnv = "REPOSE_TEST_HELPER"

func runTestHelper(kind string, args []string) int {
	switch kind {
	case "mcp-server":
		return helperMCPServer()
	case "repose-mcp":
		if len(args) > 0 && args[0] == "hold" {
			return mcpshim.Hold(args[1:], os.Stdin, os.Stdout, os.Stderr)
		}
		if len(args) == 1 {
			return mcpshim.Serve(args[0], os.Stdin, os.Stdout, os.Stderr)
		}
	}
	return 2
}

// helperMCPServer is a stdio MCP server: tools from MCP_TEST_TOOLS; pid
// answers its process id, pings how many of the shim's pings reached it,
// token the MCP_TEST_TOKEN it was started with.
func helperMCPServer() int {
	tools := strings.Split(os.Getenv("MCP_TEST_TOOLS"), ",")
	pings := 0
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	out := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		reply := func(res any) { _ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res}) }
		switch m.Method {
		case mcpshim.PingMethod:
			pings++
		case "initialize":
			reply(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "helper", "version": "1"}})
		case "tools/list":
			var ts []any
			for _, n := range tools {
				ts = append(ts, map[string]any{"name": n, "inputSchema": map[string]any{"type": "object"}})
			}
			reply(map[string]any{"tools": ts})
		case "tools/call":
			text := ""
			switch m.Params.Name {
			case "pid":
				text = strconv.Itoa(os.Getpid())
			case "pings":
				text = strconv.Itoa(pings)
			case "token":
				text = os.Getenv("MCP_TEST_TOKEN")
			}
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})
		}
	}
	return 0
}

func helperDef(t *testing.T, name, tools string) laptopMCP {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return laptopMCP{Name: name, Command: exe, Env: map[string]string{testHelperEnv: "mcp-server", "MCP_TEST_TOOLS": tools}, Dir: t.TempDir()}
}

// Lookup order and shapes: Claude Code's local scope over its user
// scope, then Claude Desktop (macOS only), Codex, Gemini CLI; ${VAR}s
// from this laptop's environment; an unknown name lists what exists.
func TestFindLaptopMCP(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, root, "git", "init", "-q", "-b", "main")
	real, _ := filepath.EvalSymlinks(root)
	t.Setenv("MCP_TEST_SECRET", "s3cret")
	writeClaudeJSON(t, home, map[string]any{
		"mcpServers": map[string]any{
			"notes":  map[string]any{"command": "user-notes"},
			"tokens": map[string]any{"command": "tok", "args": []any{"--token", "${MCP_TEST_SECRET}"}, "env": map[string]any{"K": "${MCP_TEST_SECRET:-x}", "D": "${MCP_TEST_UNSET:-dflt}"}},
			"linear": map[string]any{"type": "http", "url": "https://mcp.linear.app/mcp"},
			"unset":  map[string]any{"command": "tok", "args": []any{"${MCP_TEST_UNSET}"}, "env": map[string]any{"B": "${MCP_TEST_UNSET_B}", "H": "${HOME}"}},
		},
		"projects": map[string]any{real: map[string]any{"mcpServers": map[string]any{"notes": map[string]any{"command": "local-notes"}}}},
	})
	desk := claudeDesktopConfig(home)
	_ = os.MkdirAll(filepath.Dir(desk), 0o755)
	_ = os.WriteFile(desk, []byte(`{"mcpServers":{"apple-notes":{"command":"/Applications/Notes MCP","env":{"A":"b"}}}}`), 0o600)
	codex := t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	_ = os.WriteFile(filepath.Join(codex, "config.toml"), []byte("[mcp_servers.xcode]\ncommand = \"xcrun\"\nargs = [\"mcpbridge\"]\ncwd = \"~/proj\"\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(home, ".gemini"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".gemini", "settings.json"), []byte(`{"mcpServers":{"figma":{"command":"figma-mcp","args":["--stdio"]}}}`), 0o600)

	got := func(name string) laptopMCP {
		t.Helper()
		d, err := findLaptopMCP(home, root, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return d
	}
	if d := got("notes"); d.Command != "local-notes" || d.Source != "Claude Code, this project" || d.Dir != root {
		t.Errorf("notes = %+v", d)
	}
	if d, _ := findLaptopMCP(home, "", "notes"); d.Command != "user-notes" || d.Dir != home {
		t.Errorf("notes outside the repository = %+v", d)
	}
	if d := got("tokens"); strings.Join(d.Args, " ") != "--token s3cret" || d.Env["K"] != "s3cret" || d.Env["D"] != "dflt" {
		t.Errorf("tokens = %+v", d)
	}
	if d := got("xcode"); d.Command != "xcrun" || d.Source != "Codex" || d.Dir != filepath.Join(home, "proj") {
		t.Errorf("xcode = %+v", d)
	}
	if d := got("figma"); d.Command != "figma-mcp" || d.Source != "Gemini CLI" || d.Args[0] != "--stdio" {
		t.Errorf("figma = %+v", d)
	}
	if _, err := findLaptopMCP(home, root, "linear"); err == nil || !strings.Contains(err.Error(), "is an HTTP server") {
		t.Errorf("linear: %v", err)
	}
	// An unset ${VAR} would reach the server as its literal text.
	_ = os.Unsetenv("MCP_TEST_UNSET")
	_ = os.Unsetenv("MCP_TEST_UNSET_B")
	if _, err := findLaptopMCP(home, root, "unset"); err == nil || err.Error() != "unset in your Claude Code config uses ${MCP_TEST_UNSET} and ${MCP_TEST_UNSET_B}, which this shell does not set. Set them and run the forward again." {
		t.Errorf("unset: %v", err)
	}
	t.Setenv("REPOSE_TEST_GOOS", "linux")
	if _, err := findLaptopMCP(home, root, "apple-notes"); err == nil {
		t.Error("Claude Desktop read on Linux")
	}
	t.Setenv("REPOSE_TEST_GOOS", "darwin")
	if d := got("apple-notes"); d.Command != "/Applications/Notes MCP" || d.Source != "Claude Desktop" || d.Env["A"] != "b" {
		t.Errorf("apple-notes = %+v", d)
	}
	_, err := findLaptopMCP(home, root, "nope")
	want := "No MCP server named nope on this laptop: found notes (Claude Code, this project); linear, notes, tokens, unset (Claude Code); apple-notes (Claude Desktop); xcode (Codex); figma (Gemini CLI)."
	if err == nil || !strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), "repose mcp forward nope -- COMMAND ARGS") {
		t.Errorf("unknown name: %v", err)
	}
	empty := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, err := findLaptopMCP(empty, "", "nope"); err == nil || !strings.Contains(err.Error(), "none in your Claude Code, Claude Desktop, Codex or Gemini CLI config") {
		t.Errorf("nothing configured: %v", err)
	}
}

// frameLines collects what the laptop end sends back, as lines per stream.
type frameLines struct {
	mu     sync.Mutex
	buf    map[uint32][]byte
	closed map[uint32]bool
	lines  chan streamLine
}

type streamLine struct {
	id   uint32
	line string
}

func readFrames(r io.Reader) *frameLines {
	fl := &frameLines{buf: map[uint32][]byte{}, closed: map[uint32]bool{}, lines: make(chan streamLine, 256)}
	go func() {
		for {
			f, err := mcpshim.ReadFrame(r)
			if err != nil {
				return
			}
			fl.mu.Lock()
			switch f.Type {
			case mcpshim.FrameData:
				b := append(fl.buf[f.ID], f.Payload...)
				for {
					i := bytes.IndexByte(b, '\n')
					if i < 0 {
						break
					}
					fl.lines <- streamLine{f.ID, string(b[:i])}
					b = b[i+1:]
				}
				fl.buf[f.ID] = b
			case mcpshim.FrameClose:
				fl.closed[f.ID] = true
			}
			fl.mu.Unlock()
		}
	}()
	return fl
}

func (fl *frameLines) isClosed(id uint32) bool {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.closed[id]
}

// answer waits for the response to id on stream sid and returns its
// result's first text, or the raw result.
func (fl *frameLines) answer(t *testing.T, sid uint32, id string) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case l := <-fl.lines:
			var m struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if l.id != sid || json.Unmarshal([]byte(l.line), &m) != nil || string(m.ID) != id {
				continue
			}
			var r struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(m.Result, &r) == nil && len(r.Content) > 0 {
				return r.Content[0].Text
			}
			return string(m.Result)
		case <-deadline:
			t.Fatalf("no answer to %s on stream %d", id, sid)
		}
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// The laptop end: a process per stream, pings answered here and never
// passed to the server, a server stopped with its stream, and a name the
// laptop did not define refused (the machine names a server, never a
// command).
func TestMCPEndProcesses(t *testing.T) {
	toEndR, toEndW := io.Pipe()
	fromEndR, fromEndW := io.Pipe()
	var calls []string
	var cmu sync.Mutex
	end := newMCPEnd(map[string]laptopMCP{"probe": helperDef(t, "probe", "pid,pings")}, fromEndW)
	end.onCall = func(agent, server, tool string) {
		cmu.Lock()
		calls = append(calls, agent+" called "+server+"."+tool)
		cmu.Unlock()
	}
	served := make(chan struct{})
	go func() { _ = end.serve(toEndR); close(served) }()
	fl := readFrames(fromEndR)
	fw := mcpshim.NewFrameWriter(toEndW)
	_ = fw.Write(mcpshim.FrameHello, 0, []byte(mcpshim.FrameVersion))
	send := func(id uint32, v any) {
		b, _ := json.Marshal(v)
		if err := fw.Data(id, append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	pids := map[uint32]int{}
	for id, client := range map[uint32]string{1: "claude-code", 2: "codex-mcp-client"} {
		_ = fw.Write(mcpshim.FrameOpen, id, []byte("probe"))
		send(id, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": client}}})
		send(id, map[string]any{"jsonrpc": "2.0", "id": "$repose-ping-9", "method": mcpshim.PingMethod})
		send(id, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "pid"}})
		pid, err := strconv.Atoi(fl.answer(t, id, "2"))
		if err != nil {
			t.Fatal(err)
		}
		pids[id] = pid
		send(id, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "pings"}})
		if n := fl.answer(t, id, "3"); n != "0" {
			t.Errorf("stream %d: the server saw %s pings", id, n)
		}
	}
	if pids[1] == pids[2] || !alive(pids[1]) || !alive(pids[2]) {
		t.Fatalf("pids %v", pids)
	}
	cmu.Lock()
	got := strings.Join(calls, "\n")
	cmu.Unlock()
	for _, want := range []string{"claude called probe.pid", "codex called probe.pings"} {
		if !strings.Contains(got, want) {
			t.Errorf("calls %q lack %q", got, want)
		}
	}
	// A name the laptop did not define is refused, whatever follows it.
	_ = fw.Write(mcpshim.FrameOpen, 3, []byte("probe; rm -rf ~"))
	waitUntil(t, func() bool { return fl.isClosed(3) })
	// The stream's end stops its server.
	_ = fw.Write(mcpshim.FrameClose, 1, nil)
	waitUntil(t, func() bool { return !alive(pids[1]) })
	if !alive(pids[2]) {
		t.Error("closing one stream stopped the other's server")
	}
	// An id already in use is refused: the server on it stays the one
	// the map holds, so the hold's end still stops it.
	_ = fw.Write(mcpshim.FrameOpen, 2, []byte("probe"))
	waitUntil(t, func() bool { return fl.isClosed(2) })
	end.mu.Lock()
	n := len(end.procs)
	end.mu.Unlock()
	if n != 1 {
		t.Errorf("%d servers after a reused id, want 1", n)
	}
	// The hold's end stops the rest.
	_ = toEndW.Close()
	<-served
	waitUntil(t, func() bool { return !alive(pids[2]) })
}

// What the machine says in R and G frames reaches the laptop's terminal
// only for a name the laptop forwards, and with no control characters.
func TestMCPEndMachineStrings(t *testing.T) {
	toEndR, toEndW := io.Pipe()
	end := newMCPEnd(map[string]laptopMCP{"probe": {Name: "probe"}}, io.Discard)
	var mu sync.Mutex
	var got []string
	end.onReady = func(r mcpshim.Ready) { mu.Lock(); got = append(got, "ready "+r.Name+" "+r.Error); mu.Unlock() }
	end.onGone = func(n string) { mu.Lock(); got = append(got, "gone "+n); mu.Unlock() }
	served := make(chan struct{})
	go func() { _ = end.serve(toEndR); close(served) }()
	fw := mcpshim.NewFrameWriter(toEndW)
	_ = fw.Write(mcpshim.FrameHello, 0, []byte(mcpshim.FrameVersion))
	_ = fw.Write(mcpshim.FrameReady, 0, mcpshim.ReadyFrame(mcpshim.Ready{Name: "probe", Error: "bad\x1b]52;c;aGk=\x07 start\u009b2J"}))
	_ = fw.Write(mcpshim.FrameReady, 0, mcpshim.ReadyFrame(mcpshim.Ready{Name: "\x1b[2Kfake"}))
	_ = fw.Write(mcpshim.FrameGone, 0, []byte("\x1b[2Jother"))
	_ = fw.Write(mcpshim.FrameGone, 0, []byte("probe"))
	_ = toEndW.Close()
	<-served
	mu.Lock()
	defer mu.Unlock()
	want := []string{"ready probe bad]52;c;aGk= start2J", "gone probe"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Output that does not start with the hold's hello is not frames: serve
// says so instead of reading on; output after a hello that ends is the
// hold's end.
func TestMCPEndNotFrames(t *testing.T) {
	end := newMCPEnd(map[string]laptopMCP{}, io.Discard)
	if err := end.serve(strings.NewReader("Welcome to the machine\n")); !errors.Is(err, errMCPNotFrames) {
		t.Errorf("text: %v", err)
	}
	var b bytes.Buffer
	fw := mcpshim.NewFrameWriter(&b)
	_ = fw.Write(mcpshim.FrameReady, 0, []byte("{}"))
	if err := newMCPEnd(map[string]laptopMCP{}, io.Discard).serve(&b); !errors.Is(err, errMCPNotFrames) {
		t.Errorf("a frame before the hello: %v", err)
	}
	b.Reset()
	_ = fw.Write(mcpshim.FrameHello, 0, []byte(mcpshim.FrameVersion))
	if err := newMCPEnd(map[string]laptopMCP{}, io.Discard).serve(&b); err != nil {
		t.Errorf("hello then the end: %v", err)
	}
	if err := newMCPEnd(map[string]laptopMCP{}, io.Discard).serve(strings.NewReader("")); err != nil {
		t.Errorf("nothing at all (an old base's 127): %v", err)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// mcpGuest is a fake guest whose repose-mcp is this test binary.
func mcpGuest(t *testing.T) (sshTarget, string) {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh")
	}
	home := t.TempDir()
	privPath, pub, err := testguest.GenerateClientKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	guest, err := testguest.New(home, pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(guest.Close)
	sock, err := os.MkdirTemp("", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sock) })
	t.Setenv("REPOSE_MCP_SOCKET_DIR", sock)
	exe, _ := os.Executable()
	bin := t.TempDir()
	script := "#!/bin/sh\n" + testHelperEnv + "=repose-mcp exec " + shQuote(exe) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "repose-mcp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	host, port, _ := strings.Cut(guest.Addr, ":")
	return sshTarget{Args: []string{
		"-p", port,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-i", privPath,
		"guest@" + host,
	}}, home
}

// forwardRun is one runMCPForward with its events.
type forwardRun struct {
	events chan string
	cancel context.CancelFunc
	done   chan error
}

func startForward(t *testing.T, target sshTarget, def laptopMCP) *forwardRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	fr := &forwardRun{events: make(chan string, 64), cancel: cancel, done: make(chan error, 1)}
	ui := mcpForwardUI{
		ready: func(r mcpshim.Ready, again bool) {
			fr.events <- fmt.Sprintf("ready %s %d new=%v changed=%v", r.Name, r.Tools, r.New, r.Changed)
		},
		failed: func(name, reason, tail string) { fr.events <- "failed " + name + ": " + reason + " " + tail },
		gone:   func(name string) { fr.events <- "gone " + name },
		call:   func(agent, server, tool string) { fr.events <- agent + " called " + server + "." + tool },
		lost:   func() { fr.events <- "lost" },
	}
	go func() { fr.done <- runMCPForward(ctx, target, map[string]laptopMCP{def.Name: def}, ui) }()
	t.Cleanup(func() {
		cancel()
		<-fr.done
	})
	return fr
}

func (fr *forwardRun) want(t *testing.T, want string, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case e := <-fr.events:
			if e == want {
				return
			}
			if strings.HasPrefix(e, "failed") {
				t.Fatalf("%s (wanted %q)", e, want)
			}
		case <-deadline:
			t.Fatalf("no %q in %s", want, d)
		}
	}
}

// shimAgent is an agent on the guest with the shim as its MCP server.
type shimAgent struct {
	t     *testing.T
	in    io.WriteCloser
	lines chan map[string]any
	next  int
}

func startShimAgent(t *testing.T, home, name string) *shimAgent {
	t.Helper()
	exe, _ := os.Executable()
	cmd := exec.Command(exe, name)
	cmd.Env = append(os.Environ(), testHelperEnv+"=repose-mcp", "HOME="+home)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Wait() })
	a := &shimAgent{t: t, in: in, lines: make(chan map[string]any, 64), next: 100}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				a.lines <- m
			}
		}
		close(a.lines)
	}()
	a.write(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "claude-code", "version": "2"}}})
	if m := a.read(5 * time.Second); m["result"] == nil {
		t.Fatalf("initialize: %v", m)
	}
	a.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return a
}

func (a *shimAgent) write(v any) {
	b, _ := json.Marshal(v)
	if _, err := a.in.Write(append(b, '\n')); err != nil {
		a.t.Fatal(err)
	}
}

func (a *shimAgent) read(d time.Duration) map[string]any {
	a.t.Helper()
	select {
	case m, ok := <-a.lines:
		if !ok {
			a.t.Fatal("the shim ended")
		}
		return m
	case <-time.After(d):
		a.t.Fatalf("nothing from the shim in %s", d)
	}
	return nil
}

// call calls tool and returns its text and isError; notifications seen on
// the way go to notes.
func (a *shimAgent) call(tool string, notes *[]string) (string, bool) {
	a.t.Helper()
	a.next++
	id := float64(a.next)
	a.write(map[string]any{"jsonrpc": "2.0", "id": a.next, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": map[string]any{}}})
	for {
		m := a.read(30 * time.Second)
		if meth, _ := m["method"].(string); meth != "" {
			*notes = append(*notes, meth)
			continue
		}
		if m["id"] != id {
			continue
		}
		res, _ := m["result"].(map[string]any)
		content, _ := res["content"].([]any)
		if len(content) == 0 {
			a.t.Fatalf("call %s: %v", tool, m)
		}
		text, _ := content[0].(map[string]any)["text"].(string)
		isErr, _ := res["isError"].(bool)
		return text, isErr
	}
}

// callUntil calls tool until ok holds of the answer.
func (a *shimAgent) callUntil(tool string, within time.Duration, notes *[]string, ok func(string, bool) bool) string {
	a.t.Helper()
	deadline := time.Now().Add(within)
	for {
		text, isErr := a.call(tool, notes)
		if ok(text, isErr) {
			return text
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("%s: last answer %q (isError %v) after %s", tool, text, isErr, within)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The forward end to end, through a real ssh and the fake guest: the hold
// registers the server, an agent's call through the shim reaches a
// process on the laptop and is reported there; with the forward ended
// (Ctrl-C: the ssh closes, the hold removes the socket and the link
// closes, so the shim answers away at once; TestMCPForwardLaptopSilent
// covers a laptop that stops answering without closing) the next call is
// a tool error; a new forward makes calls work again and tells the agent
// the tools changed; a second forward of the same NAME takes over from
// the first.
func TestMCPForwardEndToEnd(t *testing.T) {
	target, home := mcpGuest(t)
	first := startForward(t, target, helperDef(t, "probe", "pid,pings"))
	first.want(t, "ready probe 2 new=true changed=false", 60*time.Second)
	f, err := os.ReadFile(filepath.Join(home, ".repose", "mcp", "forward", "probe.json"))
	if err != nil || !bytes.Contains(f, []byte(`"name": "pings"`)) {
		t.Fatalf("forward file: %s %v", f, err)
	}

	a := startShimAgent(t, home, "probe")
	var notes []string
	isPid := func(text string, isErr bool) bool { _, err := strconv.Atoi(text); return !isErr && err == nil }
	pid1 := a.callUntil("pid", 20*time.Second, &notes, isPid)
	first.want(t, "claude called probe.pid", 5*time.Second)
	if n, _ := a.call("pings", &notes); n != "0" {
		t.Errorf("the server saw %s pings", n)
	}

	first.cancel()
	if err := <-first.done; err != nil {
		t.Errorf("forward ended with %v", err)
	}
	first.done <- nil // for the cleanup
	start := time.Now()
	a.callUntil("pid", 25*time.Second, &notes, func(text string, isErr bool) bool {
		return isErr && text == mcpshim.AwayText("probe")
	})
	t.Logf("away answer %s after the forward ended", time.Since(start).Round(time.Millisecond))

	second := startForward(t, target, helperDef(t, "probe", "pid,pings,extra"))
	second.want(t, "ready probe 3 new=false changed=true", 60*time.Second)
	notes = nil
	pid2 := a.callUntil("pid", 25*time.Second, &notes, isPid)
	if pid2 == pid1 {
		t.Errorf("same server process %s after a new forward", pid2)
	}
	if !strings.Contains(strings.Join(notes, " "), "notifications/tools/list_changed") {
		t.Errorf("no list_changed on reconnect; saw %v", notes)
	}

	third := startForward(t, target, helperDef(t, "probe", "pid,pings,extra"))
	third.want(t, "ready probe 3 new=false changed=false", 60*time.Second)
	second.want(t, "gone probe", 10*time.Second)
	if err := <-second.done; !errors.Is(err, errMCPNothingLeft) {
		t.Errorf("the forward taken over ended with %v", err)
	}
	second.done <- nil
	a.callUntil("pid", 25*time.Second, &notes, func(text string, isErr bool) bool { return isPid(text, isErr) && text != pid2 })
	third.want(t, "claude called probe.pid", 5*time.Second)
}

// A laptop that goes silent without closing anything (it slept: the ssh
// stops reading, the machine's sshd keeps the connection for minutes and
// the hold keeps the socket) makes the call in flight, and the next one,
// a tool error within 25 s, from the shim's missed pings alone.
func TestMCPForwardLaptopSilent(t *testing.T) {
	target, home := mcpGuest(t)
	var pmu sync.Mutex
	var procs []*os.Process
	mcpHoldStarted = func(p *os.Process) { pmu.Lock(); procs = append(procs, p); pmu.Unlock() }
	t.Cleanup(func() { mcpHoldStarted = nil })
	fr := startForward(t, target, helperDef(t, "probe", "pid,hang"))
	fr.want(t, "ready probe 2 new=true changed=false", 60*time.Second)
	a := startShimAgent(t, home, "probe")
	var notes []string
	a.callUntil("pid", 20*time.Second, &notes, func(text string, isErr bool) bool { _, err := strconv.Atoi(text); return !isErr && err == nil })

	pmu.Lock()
	ssh := procs[len(procs)-1]
	pmu.Unlock()
	if err := ssh.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ssh.Signal(syscall.SIGCONT) }) // so the forward can end
	start := time.Now()
	text, isErr := a.call("hang", &notes)
	took := time.Since(start)
	if !isErr || text != mcpshim.AwayText("probe") {
		t.Fatalf("call while the laptop is silent = %q isError=%v", text, isErr)
	}
	if took > 25*time.Second {
		t.Errorf("away after %s, want within 25 s", took)
	}
	t.Logf("away answer %s after the laptop went silent", took.Round(time.Millisecond))
	start = time.Now()
	if text, isErr := a.call("pid", &notes); !isErr || text != mcpshim.AwayText("probe") || time.Since(start) > 2*time.Second {
		t.Errorf("next call = %q isError=%v after %s, want away at once", text, isErr, time.Since(start))
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("REPOSE_MCP_SOCKET_DIR"), "probe.sock")); err != nil {
		t.Errorf("the hold let go of the socket, so this did not test silence: %v", err)
	}
}

// A connection that drops says so once, however many reconnects fail;
// after each failed one the forward asks whether the machine still runs,
// and ends with that answer when it does not (exit 5, not a line every
// 30 s forever).
func TestMCPForwardOutageSaidOnce(t *testing.T) {
	target, _ := mcpGuest(t)
	var pmu sync.Mutex
	var procs []*os.Process
	mcpHoldStarted = func(p *os.Process) { pmu.Lock(); procs = append(procs, p); pmu.Unlock() }
	t.Cleanup(func() { mcpHoldStarted = nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan string, 64)
	checks := 0
	stopped := exitf(ExitGuestNotRunning, "todo-app is stopped.")
	ui := mcpForwardUI{
		ready:  func(r mcpshim.Ready, again bool) { events <- "ready" },
		failed: func(name, reason, tail string) { events <- "failed " + reason },
		gone:   func(string) {},
		call:   func(string, string, string) {},
		lost:   func() { events <- "lost" },
		running: func() error {
			checks++
			events <- "check"
			if checks == 2 {
				return stopped
			}
			return nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- runMCPForward(ctx, target, map[string]laptopMCP{"probe": helperDef(t, "probe", "pid")}, ui)
	}()
	if e := <-events; e != "ready" {
		t.Fatalf("first event %q", e)
	}
	// From now on the machine answers nothing: every reconnect fails.
	fakeMCPStatus(t, "exit 1\n")
	pmu.Lock()
	_ = procs[0].Kill()
	pmu.Unlock()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the forward did not end on a stopped machine")
	}
	if err != stopped {
		t.Errorf("err = %v", err)
	}
	close(events)
	var seen []string
	for e := range events {
		seen = append(seen, e)
	}
	if got := strings.Join(seen, " "); got != "lost check check" {
		t.Errorf("events %q, want one lost and a check per failed reconnect", got)
	}
}

// An attach's forward holds with --wait; `repose mcp forward` does not.
func TestMCPHoldWaitForAttach(t *testing.T) {
	tg := sshTarget{Args: []string{"x.repose"}}
	if a := mcpSSHArgs(tg, []string{"notes"}, true); a[len(a)-1] != "repose-mcp hold --wait notes" {
		t.Errorf("attach: %q", a[len(a)-1])
	}
	if a := mcpSSHArgs(tg, []string{"notes", "figma"}, false); a[len(a)-1] != "repose-mcp hold notes figma" {
		t.Errorf("forward: %q", a[len(a)-1])
	}
}

// A failure the machine reports names the machine, never the laptop.
func TestMCPFailedLine(t *testing.T) {
	if l := mcpFailedLine("todo-app", "notes", machineFailure+"cannot listen on notes's socket: bind: no space left on device", ""); l != "notes: todo-app could not take the forward: cannot listen on notes's socket: bind: no space left on device." {
		t.Errorf("machine: %q", l)
	}
	if l := mcpFailedLine("todo-app", "notes", "the server ended before it answered", "Error: \x1b[31mno token"); l != "notes did not start on this laptop: the server ended before it answered. It said: Error: [31mno token" {
		t.Errorf("laptop: %q", l)
	}
}

// SIGHUP (the terminal closed) and SIGTERM end the forward's context, so
// the servers in their own process groups are stopped with it.
func TestForwardEndsOnHangup(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		ctx, stop := endOnHangup(context.Background())
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Errorf("%v did not end the forward", sig)
		}
		stop()
	}
}

// Text on the hold's stdout before its hello (a shell startup file on the
// machine that prints for ssh commands) ends the forward with a line that
// says so, instead of hanging.
func TestMCPForwardTextBeforeHello(t *testing.T) {
	target, _ := mcpGuest(t)
	fakeMCPStatus(t, "echo 'Welcome to the machine'\nexec cat >/dev/null\n")
	fr := startForward(t, target, helperDef(t, "probe", "pid"))
	select {
	case err := <-fr.done:
		if !errors.Is(err, errMCPNotFrames) {
			t.Errorf("err = %v", err)
		}
		fr.done <- nil
	case <-time.After(20 * time.Second):
		t.Fatal("the forward hung on text before the hello")
	}
}

// --remove takes the registration away and names what was not there.
func TestMCPForwardRemove(t *testing.T) {
	target, home := mcpGuest(t)
	fr := startForward(t, target, helperDef(t, "probe", "pid"))
	fr.want(t, "ready probe 1 new=true changed=false", 60*time.Second)
	fr.cancel()
	<-fr.done
	fr.done <- nil
	if err := mcpRemove(context.Background(), io.Discard, target, "todo-app", []string{"probe"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".repose", "mcp", "forward", "probe.json")); !os.IsNotExist(err) {
		t.Errorf("forward file still there: %v", err)
	}
	err := mcpRemove(context.Background(), io.Discard, target, "todo-app", []string{"probe"})
	if err == nil || err.Error() != "todo-app has no forwarded MCP server named probe." {
		t.Errorf("second remove: %v", err)
	}
}

// A machine whose base predates the forward (no repose-mcp: the shell's
// 127, as on every base before this one) says so, and is not retried.
func TestMCPForwardOldBase(t *testing.T) {
	target, _ := mcpGuest(t)
	hideCommand(t, "repose-mcp")
	fr := startForward(t, target, helperDef(t, "probe", "pid"))
	select {
	case err := <-fr.done:
		if !errors.Is(err, errMCPOldBase) {
			t.Errorf("err = %v", err)
		}
		fr.done <- nil
	case <-time.After(30 * time.Second):
		t.Fatal("the forward kept going against an old base")
	}
	if err := mcpRemove(context.Background(), io.Discard, target, "todo-app", []string{"probe"}); err == nil || !strings.Contains(err.Error(), "predates the MCP forward") {
		t.Errorf("remove on an old base: %v", err)
	}
}

// [mcp] forward and [projects.NAME.mcp] forward: both, each name once.
func TestConfigMCPForward(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[mcp]\nforward = [\"apple-notes\", \"figma\"]\n\n[projects.todo-app.mcp]\nforward = [\"figma\", \"xcode\"]\n"), 0o600)
	cfg, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.mcpForward("todo-app"), " "); got != "apple-notes figma xcode" {
		t.Errorf("todo-app: %q", got)
	}
	if got := strings.Join(cfg.mcpForward("other"), " "); got != "apple-notes figma" {
		t.Errorf("other: %q", got)
	}
}
