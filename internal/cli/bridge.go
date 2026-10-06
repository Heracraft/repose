package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// `repose browser bridge` (DECISIONS I-296): the agents on a machine drive
// the laptop's own Chrome, logins and extensions included, for as long as
// the command runs. Three parts, none of which the agent sees:
//
//  1. The laptop's Chrome. Chrome 144 and later has its own switch,
//     chrome://inspect/#remote-debugging, which starts a DevTools server on
//     a random port and writes its port and the browser's websocket path to
//     DevToolsActivePort in the profile directory. That server speaks
//     websocket only (no /json/version), and Chrome asks the user to allow
//     each connection. A browser started with --remote-debugging-port, or
//     any other DevTools server, is bridged as is with --cdp.
//  2. The front, a listener on the laptop's loopback that the tunnel
//     reaches. It answers /json/version itself, naming the browser's
//     websocket at the address the request came to (the guest's
//     127.0.0.1:9224), because that is how Playwright MCP and
//     chrome-devtools-mcp discover the websocket and Chrome's own server
//     does not answer it. The browser's websocket goes to Chrome message
//     by message through the bridge's policy (bridgecdp.go, I-311), and
//     with --allow a CDP connection of its own holds the agents' tabs to
//     the list (bridgewarden.go); anything else is 404.
//  3. The tunnel: an ssh with a reverse forward from the guest's 9226 to
//     the front, whose remote command is `repose-guest-profile browser
//     bridge hold`. The hold switches the guest's 9224 endpoint from the
//     machine's Chromium to the tunnel and switches it back when the ssh
//     ends, however it ends. The MCP servers keep the one endpoint they were
//     registered with; their next call reconnects to whatever is behind it.

const (
	// bridgeGuestPort is where the guest's sshd listens for the reverse
	// forward and repose-browser-bridge.socket sends 9224 (browser.nix).
	bridgeGuestPort = 9226
	// bridgeEndpoint is the guest's DevTools endpoint, what the front
	// names when a request carries no Host header.
	bridgeEndpoint = "127.0.0.1:9224"
	// bridgeInspectURL is Chrome's switch.
	bridgeInspectURL = "chrome://inspect/#remote-debugging"
	// bridgeMinChrome is the first Chrome with the switch.
	bridgeMinChrome = 144
	// bridgeToggleWait is how long a bridge waits for the switch to be
	// turned on before giving up.
	bridgeToggleWait = 5 * time.Minute
	// bridgeHoldCommand is the guest's side, run as the tunnel's remote
	// command (profile.nix).
	bridgeHoldCommand = "repose-guest-profile browser bridge hold"
)

// BridgeOptions are `repose browser bridge`'s flags.
type BridgeOptions struct {
	// CDP is a DevTools server on the laptop to bridge as is
	// (http://127.0.0.1:9222 for a browser started with
	// --remote-debugging-port), instead of Chrome's own switch.
	CDP string
	// UserDataDir is the profile directory to read DevToolsActivePort
	// from, for a Chrome (or Chromium, Brave, Edge) that is not Google
	// Chrome's default profile.
	UserDataDir string
	// NoBrowser leaves chrome://inspect for the user to open.
	NoBrowser bool
	// Allow is --allow: the hosts the agents may open (I-311). Empty is
	// no allowlist.
	Allow []string
}

// laptopChrome is a DevTools server on the laptop the bridge can reach.
type laptopChrome struct {
	Addr    string // host:port of the DevTools server
	Path    string // the browser's websocket path, /devtools/browser/<id>
	Browser string // "Chrome/144.0.7559.1" when the server said, else ""
	// Switch is true when found through Chrome's own switch, where Chrome
	// asks the user to allow each connection.
	Switch bool
}

// Name is the browser as the CLI names it: "Chrome 144", or "Chrome".
func (c laptopChrome) Name() string {
	product, version, _ := strings.Cut(c.Browser, "/")
	if product == "" {
		product = "Chrome"
	}
	if major, _, _ := strings.Cut(version, "."); major != "" {
		return product + " " + major
	}
	return product
}

// chromeUserDataDir is Google Chrome's default profile directory, where
// Chrome writes DevToolsActivePort: the same place puppeteer and
// chrome-devtools-mcp look. Empty when the platform has none.
func chromeUserDataDir(goos, home string, getenv func(string) string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "linux":
		base := getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "google-chrome")
	case "windows":
		if local := getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Google", "Chrome", "User Data")
		}
	}
	return ""
}

// readDevToolsActivePort reads Chrome's DevToolsActivePort: the port on
// the first line, the browser's websocket path on the second. Missing
// means Chrome is not running with remote debugging on (or is a Chrome
// before 144, which has no switch).
func readDevToolsActivePort(dir string) (port int, path string, err error) {
	b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		return 0, "", err
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 2 {
		return 0, "", fmt.Errorf("DevToolsActivePort has %d lines, want a port and a path", len(lines))
	}
	port, err = strconv.Atoi(lines[0])
	if err != nil || port < 1 || port > 65535 {
		return 0, "", fmt.Errorf("DevToolsActivePort names port %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "/") {
		return 0, "", fmt.Errorf("DevToolsActivePort names path %q", lines[1])
	}
	return port, lines[1], nil
}

// devToolsVersion asks a DevTools server for /json/version. found says an
// HTTP server answered at all: a browser started with
// --remote-debugging-port answers 200 with its name and websocket, and
// Chrome's switch answers 404 to every HTTP request (it speaks websocket
// only); nothing listening, or something that is not HTTP, is not found.
func devToolsVersion(ctx context.Context, addr string) (found bool, browser, wsPath string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/json/version", nil)
	if err != nil {
		return false, "", ""
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", ""
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return true, "", ""
	}
	var v struct {
		Browser string `json:"Browser"`
		WS      string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&v); err != nil {
		return true, "", ""
	}
	if u, err := url.Parse(v.WS); err == nil {
		wsPath = u.Path
	}
	return true, v.Browser, wsPath
}

// bridgeFromCDP is --cdp: the server must answer /json/version with the
// browser's websocket, which is how a client finds it.
func bridgeFromCDP(ctx context.Context, raw string) (laptopChrome, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "ws") {
		return laptopChrome{}, exitf(ExitUsage, "--cdp takes the DevTools server's URL, like http://127.0.0.1:9222; got %q.", raw)
	}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "80")
	}
	found, browser, wsPath := devToolsVersion(ctx, addr)
	if !found {
		return laptopChrome{}, exitf(ExitGeneric, "Nothing answers at %s. A browser started with --remote-debugging-port=%s would; is it running?", raw, u.Port())
	}
	if wsPath == "" {
		return laptopChrome{}, exitf(ExitGeneric, "%s answers, but not with a browser websocket at /json/version, so it is not a DevTools server the bridge can use.", raw)
	}
	return laptopChrome{Addr: addr, Path: wsPath, Browser: browser}, nil
}

// chromeAtSwitch reads what Chrome's switch wrote and checks the server
// is up: a DevToolsActivePort left by a Chrome that quit names a port
// nothing answers on.
func chromeAtSwitch(ctx context.Context, dir string) (laptopChrome, bool) {
	port, path, err := readDevToolsActivePort(dir)
	if err != nil {
		return laptopChrome{}, false
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	found, browser, wsPath := devToolsVersion(ctx, addr)
	if !found {
		return laptopChrome{}, false
	}
	if wsPath != "" {
		// A Chrome started with --remote-debugging-port writes the same
		// file and asks nothing.
		return laptopChrome{Addr: addr, Path: wsPath, Browser: browser}, true
	}
	return laptopChrome{Addr: addr, Path: path, Switch: true}, true
}

// openInChrome opens a chrome:// page in Chrome itself: the default
// handler for http URLs may be another browser, and chrome:// is not a
// scheme the system routes.
func openInChrome(goos, page string) error {
	switch goos {
	case "darwin":
		return exec.Command("open", "-a", "Google Chrome", page).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "chrome", page).Start()
	}
	for _, bin := range []string{"google-chrome", "google-chrome-stable", "chrome"} {
		if p, err := exec.LookPath(bin); err == nil {
			return exec.Command(p, page).Start()
		}
	}
	return errors.New("no google-chrome on PATH")
}

// bridgeSwitchHint is what to do when Chrome is not accepting
// connections: one sentence, printed once, then the bridge waits.
func bridgeSwitchHint(opened bool) string {
	where := "Open " + bridgeInspectURL + " in Chrome"
	if opened {
		where = "In the Chrome tab that just opened (" + bridgeInspectURL + ")"
	}
	return fmt.Sprintf("Chrome isn't accepting connections yet. %s, turn remote debugging on, and the bridge connects by itself (Chrome %d or newer). Waiting…", where, bridgeMinChrome)
}

// findLaptopChrome is what the bridge connects to: --cdp as given, else
// Chrome's switch, waited for. say prints the one hint while waiting.
func findLaptopChrome(ctx context.Context, e *Env, opts BridgeOptions, say func(string)) (laptopChrome, error) {
	if opts.CDP != "" {
		return bridgeFromCDP(ctx, opts.CDP)
	}
	dir := opts.UserDataDir
	if dir == "" {
		dir = chromeUserDataDir(goos(), e.HomeDir, os.Getenv)
	}
	if dir == "" {
		return laptopChrome{}, exitf(ExitGeneric, "repose browser bridge needs Google Chrome on this laptop, or --cdp http://127.0.0.1:9222 for another browser started with --remote-debugging-port.")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if opts.UserDataDir != "" {
			return laptopChrome{}, exitf(ExitGeneric, "%s is not a browser profile directory.", dir)
		}
		return laptopChrome{}, exitf(ExitGeneric, "No Google Chrome profile on this laptop (looked in %s). --user-data-dir DIR names another Chromium browser's profile; --cdp http://127.0.0.1:9222 bridges a browser started with --remote-debugging-port.", dir)
	}
	if c, ok := chromeAtSwitch(ctx, dir); ok {
		return c, nil
	}
	opened := false
	if !opts.NoBrowser && opts.UserDataDir == "" {
		opened = openInChrome(goos(), bridgeInspectURL) == nil
	}
	say(bridgeSwitchHint(opened))
	deadline := time.Now().Add(bridgeToggleWait)
	for time.Now().Before(deadline) {
		if err := sleepOrDone(ctx, time.Second); err != nil {
			return laptopChrome{}, err
		}
		if c, ok := chromeAtSwitch(ctx, dir); ok {
			return c, nil
		}
	}
	return laptopChrome{}, exitf(ExitGeneric, "Chrome still isn't accepting connections after %s. Turn remote debugging on at %s (Chrome %d or newer) and run repose browser bridge again.", bridgeToggleWait, bridgeInspectURL, bridgeMinChrome)
}

// cdpFront is the laptop's end of the tunnel: what the guest's 9224
// reaches. It answers /json/version, carries the browser's websocket to
// the DevTools server message by message through the policy
// (bridgecdp.go), and answers 404 to anything else.
type cdpFront struct {
	ln     net.Listener
	chrome laptopChrome
	policy *bridgePolicy
	// onAttach is called for every connection passed to the browser: an
	// MCP server attaching (each holds one for its whole life).
	onAttach func()

	mu   sync.Mutex
	done bool
}

// startCDPFront listens on a free loopback port and serves until Close.
func startCDPFront(chrome laptopChrome, policy *bridgePolicy, onAttach func()) (*cdpFront, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	if policy == nil {
		policy = newBridgePolicy(nil, nil)
	}
	f := &cdpFront{ln: ln, chrome: chrome, policy: policy, onAttach: onAttach}
	go f.serve()
	return f, nil
}

// Port is the front's port on the laptop's loopback.
func (f *cdpFront) Port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *cdpFront) Close() {
	f.mu.Lock()
	f.done = true
	f.mu.Unlock()
	_ = f.ln.Close()
}

func (f *cdpFront) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			f.mu.Lock()
			done := f.done
			f.mu.Unlock()
			if done {
				return
			}
			continue
		}
		go f.handle(c)
	}
}

// readRequestHead reads one HTTP request's request line and headers, as
// they were sent, and returns them with the request target and the Host.
func readRequestHead(br *bufio.Reader) (head []byte, target, host string, err error) {
	var buf bytes.Buffer
	first := true
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, "", "", err
		}
		buf.WriteString(line)
		if buf.Len() > 64<<10 {
			return nil, "", "", errors.New("request head over 64 KiB")
		}
		t := strings.TrimRight(line, "\r\n")
		if first {
			f := strings.Fields(t)
			if len(f) < 2 {
				return nil, "", "", fmt.Errorf("bad request line %q", t)
			}
			target = f[1]
			first = false
			continue
		}
		if t == "" {
			return buf.Bytes(), target, host, nil
		}
		if k, v, ok := strings.Cut(t, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Host") {
			host = strings.TrimSpace(v)
		}
	}
}

// versionJSON is the front's /json/version: the fields the two MCP
// servers read. host is where the request came to, from the guest's side
// of the tunnel, so the websocket URL leads back through it.
func versionJSON(chrome laptopChrome, host string) []byte {
	if host == "" {
		host = bridgeEndpoint
	}
	browser := chrome.Browser
	if browser == "" {
		browser = "Chrome"
	}
	b, _ := json.Marshal(map[string]string{
		"Browser":              browser,
		"Protocol-Version":     "1.3",
		"webSocketDebuggerUrl": "ws://" + host + chrome.Path,
	})
	return b
}

func (f *cdpFront) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	head, target, host, err := readRequestHead(br)
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	if p := strings.TrimSuffix(target, "/"); p == "/json/version" {
		body := versionJSON(f.chrome, host)
		_, _ = fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json; charset=UTF-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(body))
		_, _ = c.Write(body)
		return
	}
	// Only the browser's websocket, which is what both MCP servers use:
	// the other /json endpoints (/json/new?url among them) and a page's
	// own websocket would go round the policy.
	path, _, _ := strings.Cut(target, "?")
	if !strings.EqualFold(headerValue(head, "Upgrade"), "websocket") || path != f.chrome.Path {
		_, _ = io.WriteString(c, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	up, err := net.DialTimeout("tcp", f.chrome.Addr, 5*time.Second)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	defer func() { _ = up.Close() }()
	if _, err := up.Write(withoutWSExtensions(head)); err != nil {
		return
	}
	ubr := bufio.NewReader(up)
	rhead, status, err := readResponseHead(ubr)
	if err != nil {
		return
	}
	if _, err := c.Write(rhead); err != nil {
		return
	}
	if status != http.StatusSwitchingProtocols {
		_, _ = io.Copy(c, ubr)
		return
	}
	if f.onAttach != nil {
		f.onAttach()
	}
	conn := newCDPConn(f.policy, &lockedWriter{w: c}, &lockedWriter{w: up})
	conn.proxy(
		func(on func(wsFrame) error) (byte, []byte, []byte, error) { return readWSMessage(br, on) },
		func(on func(wsFrame) error) (byte, []byte, []byte, error) { return readWSMessage(ubr, on) })
}

// bridgeSSHArgs is the tunnel's ssh: its own connection (a reverse
// forward through the ControlMaster would outlive the command, I-149),
// exit if the guest's port is taken, the forward, and the hold as the
// remote command.
func bridgeSSHArgs(t sshTarget, frontPort int) []string {
	args := append(ownConnection(), "-o", "ExitOnForwardFailure=yes",
		"-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", bridgeGuestPort, frontPort))
	args = append(args, t.Args...)
	return append(args, bridgeHoldCommand)
}

// errBridgeHeld is the guest's port taken by an earlier bridge.
var errBridgeHeld = errors.New("another bridge holds the machine's port")

// holdBridge runs the tunnel until ctx ends or the ssh does. ready is
// called once the guest reports the endpoint switched. The hold's stdin
// is the CLI's pipe: closing it, on ctx's end, is how the guest learns the
// bridge is over, before ssh itself is ended.
func holdBridge(ctx context.Context, t sshTarget, frontPort int, ready func()) error {
	cmd := exec.Command("ssh", bridgeSSHArgs(t, frontPort)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return &sshError{ExitCode: -1, Err: err}
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "on" && ready != nil {
				ready()
				ready = nil
			}
		}
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
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
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	if strings.Contains(stderr.String(), "remote port forwarding failed") {
		return errBridgeHeld
	}
	code := -1
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		code = xe.ExitCode()
	}
	return &sshError{ExitCode: code, Stderr: stderr.String(), Err: err}
}

// bridgeRelease asks the guest to end an earlier bridge's ssh so the port
// is free: a laptop that slept keeps its listener until sshd gives up on
// it. Best effort; a port still held fails the tunnel with errBridgeHeld.
func bridgeRelease(ctx context.Context, t sshTarget) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = runSSHOK(ctx, t, "repose-guest-profile browser bridge release")
}

// bridgeStopWait bounds the closing ssh: over the ControlMaster it takes
// a fraction of a second, and a machine that doesn't answer in this long
// has switched back by itself anyway (the hold's EXIT trap, or the idle
// guard within two minutes).
const bridgeStopWait = 4 * time.Second

// bridgeStop switches the guest's endpoint back, after the hold has done
// so itself: belt and braces, so the closing line is true. With a tmux
// line, the same ssh shows it to whoever is attached, and to nobody when
// nobody is: one round trip, never a wait for a client to turn up.
func bridgeStop(t sshTarget, slug, tmuxLine string) {
	ctx, cancel := context.WithTimeout(context.Background(), bridgeStopWait)
	defer cancel()
	cmd := "repose-guest-profile browser bridge stop"
	if tmuxLine != "" {
		cmd += "; " + tmuxIfAttached(slug, tmuxLine)
	}
	_ = runSSHOK(ctx, t, cmd)
}

// tmuxIfAttached is a shell command that shows msg to whoever is
// attached, and does nothing when nobody is: on the tmux session's
// clients, or, with no tmux server and herdr running, as a herdr
// notification (I-509). It serves callers that do not know the machine's
// multiplexer; guestMessageScript is the same thing by its other name.
func tmuxIfAttached(slug, msg string) string { return guestMessageScript(slug, msg) }

// guestMessageScript is shell that shows msg on whichever multiplexer the
// machine runs, without asking first: tmux's clients when the tmux
// server answers, else herdr's notification when its socket is there.
func guestMessageScript(slug, msg string) string {
	return fmt.Sprintf("if tmux list-sessions >/dev/null 2>&1; then %s; elif [ -S %s ]; then %s; fi",
		tmuxMux{}.MessageScript(slug, msg), multiplexer.HerdrSocket, herdrMux{}.MessageScript(slug, msg))
}
