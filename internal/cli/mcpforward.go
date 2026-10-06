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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/heracraft/repose/internal/mcpreg"
	"github.com/heracraft/repose/internal/mcpshim"
)

// `repose mcp forward NAME...` (DECISIONS I-557): an MCP server that needs
// the laptop (an Apple app, a laptop file, a program only the laptop has)
// runs here, and the agents on the machine reach it through `repose-mcp
// NAME`, a shim that answers for the laptop while it is away. Three parts:
//
//  1. The definition, read from this laptop's agent configs or given
//     after --. Tokens and ${VAR}s stay here: the command runs here, with
//     this laptop's environment.
//  2. The hold: `ssh MACHINE repose-mcp hold NAME...`, on a connection of
//     its own. Its stdin and stdout carry frames (internal/mcpshim
//     frame.go), one stream per agent session on the machine. No -R: the
//     gateway relays forwarded-tcpip channels only (I-296).
//  3. The laptop end (mcpEnd): one server process per stream, so each
//     agent session gets its own, as a stdio server does on the laptop. It
//     answers the shim's pings itself, so a server never sees them.

// laptopMCP is one stdio server as this laptop's config defines it, with
// ${VAR}s expanded from this laptop's environment.
type laptopMCP struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Dir     string
	Source  string
	// Remote is a server with a url and no command: one a forward cannot
	// start yet (loopback HTTP servers are deferred).
	Remote bool
}

// mcpSource is one config on this laptop that can define a server.
type mcpSource struct {
	Label   string
	Servers map[string]map[string]any
}

// claudeDesktopConfig is Claude Desktop's config on macOS.
func claudeDesktopConfig(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
}

// laptopMCPSources are the configs a NAME is looked up in, in order:
// Claude Code's local scope for this repository, its user scope, Claude
// Desktop (macOS), Codex, Gemini CLI.
func laptopMCPSources(home, repoDir string) []mcpSource {
	var out []mcpSource
	decode := func(raw map[string]json.RawMessage) map[string]map[string]any {
		m := map[string]map[string]any{}
		for n, r := range raw {
			var s map[string]any
			if json.Unmarshal(r, &s) == nil && s != nil {
				m[n] = s
			}
		}
		return m
	}
	if user, project, _, err := readClaudeMCP(home, repoDir); err == nil {
		out = append(out, mcpSource{"Claude Code, this project", decode(project)}, mcpSource{"Claude Code", decode(user)})
	}
	jsonServers := func(path, key string) map[string]map[string]any {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal(b, &doc) != nil {
			return nil
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(doc[key], &raw) != nil {
			return nil
		}
		return decode(raw)
	}
	if goos() == "darwin" {
		out = append(out, mcpSource{"Claude Desktop", jsonServers(claudeDesktopConfig(home), "mcpServers")})
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	var codex struct {
		Servers map[string]map[string]any `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(filepath.Join(codexHome, "config.toml"), &codex); err == nil {
		out = append(out, mcpSource{"Codex", codex.Servers})
	}
	out = append(out, mcpSource{"Gemini CLI", jsonServers(filepath.Join(home, ".gemini", "settings.json"), "mcpServers")})
	return out
}

// toLaptopMCP reads one config entry; every agent's stdio shape has
// command, args, env and (Codex, Gemini CLI) cwd.
func toLaptopMCP(name, source, home, repoDir string, s map[string]any) laptopMCP {
	exp := func(v string) string { return mcpreg.Expand(v, "") }
	l := laptopMCP{Name: name, Source: source, Env: map[string]string{}}
	cmd, _ := s["command"].(string)
	if cmd == "" {
		_, url := s["url"]
		_, httpURL := s["httpUrl"]
		l.Remote = url || httpURL
		return l
	}
	l.Command = exp(cmd)
	for _, a := range stringList(s["args"]) {
		l.Args = append(l.Args, exp(a))
	}
	if env, ok := s["env"].(map[string]any); ok {
		for k, v := range env {
			if sv, ok := v.(string); ok {
				l.Env[k] = exp(sv)
			} else {
				l.Env[k] = fmt.Sprint(v)
			}
		}
	}
	l.Dir = repoDir
	if cwd, _ := s["cwd"].(string); cwd != "" {
		cwd = exp(cwd)
		if rest, ok := strings.CutPrefix(cwd, "~/"); ok {
			cwd = filepath.Join(home, rest)
		}
		l.Dir = cwd
	}
	if l.Dir == "" {
		l.Dir = home
	}
	return l
}

// refList is ${A}, ${A} and ${B}, or ${A}, ${B} and ${C}.
func refList(names []string) string {
	refs := make([]string, len(names))
	for i, n := range names {
		refs[i] = "${" + n + "}"
	}
	if len(refs) == 1 {
		return refs[0]
	}
	return strings.Join(refs[:len(refs)-1], ", ") + " and " + refs[len(refs)-1]
}

// findLaptopMCP is NAME's definition, from the first config that has it.
func findLaptopMCP(home, repoDir, name string) (laptopMCP, error) {
	srcs := laptopMCPSources(home, repoDir)
	for _, src := range srcs {
		if s, ok := src.Servers[name]; ok {
			l := toLaptopMCP(name, src.Label, home, repoDir, s)
			if l.Remote {
				return l, exitf(ExitGeneric, "%s in your %s config is an HTTP server; repose mcp forward runs servers that start with a command.", name, src.Label)
			}
			// An unset ${VAR} would reach the server as its literal text,
			// which it then sends to its service as a token.
			if need := mcpreg.Needs(s, ""); len(need) > 0 {
				them := "it"
				if len(need) > 1 {
					them = "them"
				}
				return l, exitf(ExitGeneric, "%s in your %s config uses %s, which this shell does not set. Set %s and run the forward again.", name, src.Label, refList(need), them)
			}
			return l, nil
		}
	}
	var found []string
	for _, src := range srcs {
		if len(src.Servers) == 0 {
			continue
		}
		names := make([]string, 0, len(src.Servers))
		for n := range src.Servers {
			names = append(names, n)
		}
		sort.Strings(names)
		found = append(found, strings.Join(names, ", ")+" ("+src.Label+")")
	}
	where := "none in your Claude Code, Claude Desktop, Codex or Gemini CLI config"
	if len(found) > 0 {
		where = "found " + strings.Join(found, "; ")
	}
	return laptopMCP{}, exitf(ExitGeneric, "No MCP server named %s on this laptop: %s. For another server, give its command after --: repose mcp forward %s -- COMMAND ARGS", name, where, name)
}

// mcpEnd is the laptop's end of one hold: a server process per stream.
type mcpEnd struct {
	defs map[string]laptopMCP
	fw   *mcpshim.FrameWriter
	// onCall is told of each tools/call, by agent, server and tool name.
	onCall  func(agent, server, tool string)
	onReady func(mcpshim.Ready)
	onGone  func(name string)

	mu    sync.Mutex
	procs map[uint32]*mcpProc
	tails map[string]string // each server's last stderr line, for a failed start
	hello bool
}

func newMCPEnd(defs map[string]laptopMCP, w io.Writer) *mcpEnd {
	return &mcpEnd{defs: defs, fw: mcpshim.NewFrameWriter(w), procs: map[uint32]*mcpProc{}, tails: map[string]string{}}
}

// errMCPNotFrames is a hold whose output does not start with its hello:
// something on the machine printed first (a shell startup file that
// writes for commands run over ssh).
var errMCPNotFrames = errors.New("the machine printed text before repose-mcp hold started; a shell startup file there that prints for commands run over ssh does this")

// serve reads frames from the hold until it ends, then stops every
// server it started. It returns nil at the end of the hold's output, and
// an error for output that is not frames.
func (m *mcpEnd) serve(r io.Reader) error {
	defer m.stopAll()
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		f, err := mcpshim.ReadFrame(br)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if !m.saidHello() {
				return errMCPNotFrames
			}
			return err
		}
		if !m.saidHello() && f.Type != mcpshim.FrameHello {
			return errMCPNotFrames
		}
		switch f.Type {
		case mcpshim.FrameHello:
			m.mu.Lock()
			m.hello = true
			m.mu.Unlock()
		case mcpshim.FrameOpen:
			m.open(f.ID, string(f.Payload))
		case mcpshim.FrameData:
			m.mu.Lock()
			p := m.procs[f.ID]
			m.mu.Unlock()
			if p != nil {
				p.feed(f.Payload)
			}
		case mcpshim.FrameClose:
			m.mu.Lock()
			p := m.procs[f.ID]
			delete(m.procs, f.ID)
			m.mu.Unlock()
			if p != nil {
				p.stop()
			}
		case mcpshim.FrameReady:
			// The machine's strings reach the terminal: only names this
			// laptop forwards, and an error with no control characters.
			var rd mcpshim.Ready
			if json.Unmarshal(f.Payload, &rd) == nil && m.onReady != nil && m.known(rd.Name) {
				rd.Error = terminalText(rd.Error)
				m.onReady(rd)
			}
		case mcpshim.FrameGone:
			if name := string(f.Payload); m.onGone != nil && m.known(name) {
				m.onGone(name)
			}
		}
	}
}

// known reports whether name is one this laptop forwards.
func (m *mcpEnd) known(name string) bool {
	_, ok := m.defs[name]
	return ok
}

// terminalText drops control characters, C1 included, and keeps at most
// 300 bytes, so a string from the machine cannot move the cursor, set the
// clipboard or fake a prompt on the laptop's terminal.
func terminalText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
	if len(s) > 300 {
		s = strings.ToValidUTF8(s[:300], "")
	}
	return s
}

func (m *mcpEnd) saidHello() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hello
}

// tail is name's last stderr line on this laptop.
func (m *mcpEnd) tail(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tails[name]
}

// stopAll stops every server and waits for them, up to 2 s before KILL,
// so none outlives the CLI: each runs in a process group of its own,
// which the terminal's Ctrl-C does not reach.
func (m *mcpEnd) stopAll() {
	m.mu.Lock()
	ps := m.procs
	m.procs = map[uint32]*mcpProc{}
	m.mu.Unlock()
	for _, p := range ps {
		p.stop()
	}
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	expired := false
	for _, p := range ps {
		if !expired {
			select {
			case <-p.ended:
				continue
			case <-timeout.C:
				expired = true
			}
		}
		select {
		case <-p.ended:
		default:
			signalGroup(p.cmd, true)
		}
	}
}

// open starts NAME's server for a new stream. The command is the laptop's
// definition: the machine names a server and nothing else.
func (m *mcpEnd) open(id uint32, name string) {
	def, ok := m.defs[name]
	m.mu.Lock()
	n := 0
	for _, p := range m.procs {
		if p.name == name {
			n++
		}
	}
	_, taken := m.procs[id]
	m.mu.Unlock()
	// One more than the hold's cap: the cache fill's own stream. An id
	// already in use is refused, so the map holds every server started.
	if !ok || taken || n > mcpshim.MaxSessions {
		_ = m.fw.Write(mcpshim.FrameClose, id, nil)
		return
	}
	cmd := exec.Command(def.Command, def.Args...)
	cmd.Dir = def.Dir
	cmd.Env = os.Environ()
	for _, k := range sortedStringKeys(def.Env) {
		cmd.Env = append(cmd.Env, k+"="+def.Env[k])
	}
	setProcGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = m.fw.Write(mcpshim.FrameClose, id, nil)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = m.fw.Write(mcpshim.FrameClose, id, nil)
		return
	}
	cmd.Stderr = &tailWriter{set: func(l string) {
		m.mu.Lock()
		m.tails[name] = l
		m.mu.Unlock()
	}}
	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		m.tails[name] = err.Error()
		m.mu.Unlock()
		_ = m.fw.Write(mcpshim.FrameClose, id, nil)
		return
	}
	p := &mcpProc{id: id, name: name, cmd: cmd, in: mcpshim.NewQueue(), ended: make(chan struct{}), end: m}
	m.mu.Lock()
	m.procs[id] = p
	m.mu.Unlock()
	go func() {
		for {
			b, ok := p.in.Next()
			if !ok {
				break
			}
			if _, err := stdin.Write(b); err != nil {
				p.in.Abort()
				break
			}
		}
		_ = stdin.Close()
	}()
	go func() {
		br := bufio.NewReaderSize(stdout, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				if m.fw.Data(id, line) != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		_ = cmd.Wait()
		close(p.ended)
		m.mu.Lock()
		still := m.procs[id] == p
		if still {
			delete(m.procs, id)
		}
		m.mu.Unlock()
		p.closeIn()
		if still {
			_ = m.fw.Write(mcpshim.FrameClose, id, nil)
		}
	}()
}

// mcpProc is one server process, for one agent session on the machine.
type mcpProc struct {
	id    uint32
	name  string
	cmd   *exec.Cmd
	end   *mcpEnd
	in    *mcpshim.Queue // to the server's stdin; never blocks the frame loop
	buf   []byte
	agent string
	ended chan struct{}
	once  sync.Once
}

func (p *mcpProc) closeIn() { p.in.Close() }

// feed takes bytes from the machine, line by line: a ping is answered
// here, everything else goes to the server's stdin.
func (p *mcpProc) feed(b []byte) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			if len(p.buf) > 64<<20 { // a line no MCP client sends
				p.stop()
			}
			return
		}
		line := append([]byte{}, p.buf[:i+1]...)
		p.buf = p.buf[i+1:]
		if id, ok := mcpshim.IsPing(line); ok {
			_ = p.end.fw.Data(p.id, mcpshim.Pong(id))
			continue
		}
		p.notice(line)
		if !p.in.Push(line) {
			p.stop() // a server that stopped reading its stdin
			return
		}
	}
}

// notice reads which agent this is from its initialize, and reports each
// tools/call by tool name: never its arguments.
func (p *mcpProc) notice(line []byte) {
	if !bytes.Contains(line, []byte(`"initialize"`)) && !bytes.Contains(line, []byte(`"tools/call"`)) {
		return
	}
	var m struct {
		Method string `json:"method"`
		Params struct {
			Name       string `json:"name"`
			ClientInfo struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		} `json:"params"`
	}
	if json.Unmarshal(line, &m) != nil {
		return
	}
	switch m.Method {
	case "initialize":
		p.agent = agentOfClient(m.Params.ClientInfo.Name)
	case "tools/call":
		if p.agent != "" && p.end.onCall != nil {
			p.end.onCall(p.agent, p.name, printableName(m.Params.Name))
		}
	}
}

// stop ends the server: stdin closed, TERM to its process group, KILL
// after 2 s.
func (p *mcpProc) stop() {
	p.once.Do(func() {
		p.closeIn()
		signalGroup(p.cmd, false)
		go func() {
			select {
			case <-p.ended:
			case <-time.After(2 * time.Second):
				signalGroup(p.cmd, true)
			}
		}()
	})
}

// agentOfClient names the agent from an initialize's clientInfo.name;
// "" is hold's own cache fill, which is no agent's call.
func agentOfClient(client string) string {
	c := strings.ToLower(client)
	switch {
	case c == "repose-mcp":
		return ""
	case strings.Contains(c, "claude"):
		return "claude"
	case strings.Contains(c, "codex"):
		return "codex"
	case strings.Contains(c, "gemini"):
		return "gemini"
	case strings.Contains(c, "opencode"):
		return "opencode"
	case c == "pi" || strings.HasPrefix(c, "pi-") || strings.HasPrefix(c, "pi "):
		return "pi"
	}
	return "an agent"
}

// printableName keeps a tool name to what a terminal shows plainly.
func printableName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 64 {
			break
		}
		if r == '_' || r == '-' || r == '.' || r == '/' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// tailWriter keeps the last non-empty line written to it.
type tailWriter struct {
	mu  sync.Mutex
	buf []byte
	set func(string)
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > 4096 {
		w.buf = w.buf[len(w.buf)-4096:]
	}
	lines := strings.Split(strings.TrimSpace(string(w.buf)), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		if len(l) > 300 {
			l = l[:300]
		}
		w.set(l)
	}
	return len(p), nil
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mcpHoldCommand is the hold as the remote command. Names are checked
// against mcpreg.ValidName before they get here, so they need no quoting.
func mcpHoldCommand(names []string, flag string) string {
	c := "repose-mcp hold "
	if flag != "" {
		c += flag + " "
	}
	return c + strings.Join(names, " ")
}

// mcpSSHArgs is the hold's ssh: a connection of its own, so the hold ends
// with the command and not with the ControlMaster (I-149). An attach's
// forward holds with --wait, so two attaches share a name.
func mcpSSHArgs(t sshTarget, names []string, attach bool) []string {
	args := append(ownConnection(), t.Args...)
	flag := ""
	if attach {
		flag = "--wait"
	}
	return append(args, mcpHoldCommand(names, flag))
}

// mcpOldHoldText is what the hold of a base before the forward printed.
const mcpOldHoldText = "forwarding is not built in this base"

// errMCPOldBase is a machine whose base has no forward.
var errMCPOldBase = errors.New("the machine's base predates repose mcp forward")

// mcpHoldStarted, when set (tests), is told of each hold's ssh process.
var mcpHoldStarted func(*os.Process)

// holdMCP runs one hold until ctx ends or the ssh does.
func holdMCP(ctx context.Context, t sshTarget, names []string, attach bool, end func(io.Writer) *mcpEnd) (*mcpEnd, error) {
	cmd := exec.Command("ssh", mcpSSHArgs(t, names, attach)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	m := end(stdin)
	if err := cmd.Start(); err != nil {
		return m, &sshError{ExitCode: -1, Err: err}
	}
	if mcpHoldStarted != nil {
		mcpHoldStarted(cmd.Process)
	}
	served := make(chan struct{})
	var serveErr error
	go func() { serveErr = m.serve(stdout); close(served) }()
	go func() {
		// The keepalive: status on the machine tells a laptop that went
		// away from one that is still connected (mcpshim.FrameAlive).
		t := time.NewTicker(mcpshim.AliveEvery)
		defer t.Stop()
		for {
			select {
			case <-served:
				return
			case <-t.C:
				if m.fw.Write(mcpshim.FrameAlive, 0, nil) != nil {
					return
				}
			}
		}
	}()
	waited := make(chan error, 1)
	go func() {
		<-served
		// The hold ends with its stdin; one whose output stopped making
		// sense still holds it, and this end no longer reads.
		_ = stdin.Close()
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case err := <-exited:
			waited <- err
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			waited <- <-exited
		}
	}()
	select {
	case err = <-waited:
	case <-ctx.Done():
		_ = stdin.Close()
		select {
		case <-waited:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-waited
		}
		return m, ctx.Err()
	}
	m.stopAll()
	if errors.Is(serveErr, errMCPNotFrames) {
		return m, serveErr
	}
	if err == nil {
		return m, nil
	}
	code := -1
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		code = xe.ExitCode()
	}
	if !m.saidHello() && (code == 127 || strings.Contains(stderr.String(), mcpOldHoldText)) {
		return m, errMCPOldBase
	}
	return m, &sshError{ExitCode: code, Stderr: stderr.String(), Err: err}
}

// mcpForwardUI is what a forward reports, to a terminal or to tmux.
type mcpForwardUI struct {
	ready func(r mcpshim.Ready, again bool)
	// failed is a server that did not start: tail is its last stderr line.
	failed func(name, reason, tail string)
	gone   func(name string)
	call   func(agent, server, tool string)
	// lost is a connection that dropped, once per outage: the hold that
	// follows says ready again, or the next lost is a new outage.
	lost func()
	// attach is an attach's forward: holds with --wait.
	attach bool
	// running, when set, is asked after a reconnect fails: an error (the
	// machine stopped) ends the forward with it.
	running func() error
}

// errMCPNothingLeft is a forward with no name left: each failed to start
// or went to a newer forward.
var errMCPNothingLeft = errors.New("nothing left to forward")

// runMCPForward keeps the hold up, reconnecting with a backoff when the
// connection drops, until ctx ends (nil) or no name is left.
func runMCPForward(ctx context.Context, t sshTarget, defs map[string]laptopMCP, ui mcpForwardUI) error {
	active := map[string]bool{}
	for n := range defs {
		active[n] = true
	}
	var amu sync.Mutex
	backoff := time.Second
	again := false
	for {
		amu.Lock()
		var names []string
		for n := range active {
			names = append(names, n)
		}
		amu.Unlock()
		if len(names) == 0 {
			return errMCPNothingLeft
		}
		sort.Strings(names)
		started := time.Now()
		m, err := holdMCP(ctx, t, names, ui.attach, func(w io.Writer) *mcpEnd {
			e := newMCPEnd(defs, w)
			e.onCall = ui.call
			wasAgain := again
			e.onReady = func(r mcpshim.Ready) {
				if r.Error != "" {
					amu.Lock()
					delete(active, r.Name)
					amu.Unlock()
					if r.Where == mcpshim.WhereMachine {
						ui.failed(r.Name, machineFailure+r.Error, "")
						return
					}
					ui.failed(r.Name, r.Error, e.tail(r.Name))
					return
				}
				ui.ready(r, wasAgain)
			}
			e.onGone = func(name string) {
				amu.Lock()
				delete(active, name)
				amu.Unlock()
				ui.gone(name)
			}
			return e
		})
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errMCPOldBase) || errors.Is(err, errMCPNotFrames) {
			return err
		}
		amu.Lock()
		left := len(active)
		amu.Unlock()
		if left == 0 {
			return errMCPNothingLeft
		}
		if m != nil && !m.saidHello() && err != nil && time.Since(started) < 30*time.Second && !again {
			// The first hold never started: nothing to reconnect to.
			return err
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}
		if m != nil && m.saidHello() {
			// A hold that ran: this is a new outage.
			ui.lost()
		} else if ui.running != nil {
			// A reconnect that failed: a stopped machine ends it.
			if rerr := ui.running(); rerr != nil {
				return rerr
			}
		}
		if sleepOrDone(ctx, backoff) != nil {
			return nil
		}
		backoff = min(backoff*2, 30*time.Second)
		again = true
	}
}
