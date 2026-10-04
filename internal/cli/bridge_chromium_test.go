package cli

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The bridge's policy against a real Chromium: set BRIDGE_TEST_CHROMIUM
// to a chromium binary to run it (it is skipped otherwise). Two hosts,
// ok.test and other.test, both served here; --allow ok.test. What the
// test proves is that no request for a document on other.test reaches
// the server from the tool's tab, however it is started.

// rawCDP is a CDP client straight to a websocket: a tool on the machine,
// or the test itself playing the user.
type rawCDP struct {
	t      *testing.T
	c      net.Conn
	w      *lockedWriter
	mu     sync.Mutex
	nextID int
	calls  map[int]chan map[string]any
	events chan map[string]any
}

func newRawCDP(t *testing.T, c net.Conn, br *bufio.Reader) *rawCDP {
	r := &rawCDP{t: t, c: c, w: &lockedWriter{w: c}, calls: map[int]chan map[string]any{}, events: make(chan map[string]any, 1024)}
	t.Cleanup(func() { _ = c.Close() })
	go func() {
		for {
			_, payload, _, err := readWSMessage(br, func(wsFrame) error { return nil })
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(payload, &m) != nil {
				continue
			}
			if id, ok := m["id"].(float64); ok {
				r.mu.Lock()
				ch := r.calls[int(id)]
				r.mu.Unlock()
				if ch != nil {
					ch <- m
				}
				continue
			}
			select {
			case r.events <- m:
			default:
			}
		}
	}()
	return r
}

func (r *rawCDP) call(sid, method string, params any) map[string]any {
	r.t.Helper()
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	ch := make(chan map[string]any, 1)
	r.calls[id] = ch
	r.mu.Unlock()
	m := map[string]any{"id": id, "method": method, "params": params}
	if sid != "" {
		m["sessionId"] = sid
	}
	b, _ := json.Marshal(m)
	if err := r.w.write(encodeWSFrame(wsOpText, b, true)); err != nil {
		r.t.Fatal(err)
	}
	select {
	case res := <-ch:
		return res
	case <-time.After(15 * time.Second):
		r.t.Fatalf("%s: no answer", method)
		return nil
	}
}

func resultOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	if m["error"] != nil {
		t.Fatalf("CDP error: %v", m["error"])
	}
	r, _ := m["result"].(map[string]any)
	return r
}

type hitLog struct {
	mu   sync.Mutex
	hits []string
}

func (h *hitLog) add(s string) { h.mu.Lock(); h.hits = append(h.hits, s); h.mu.Unlock() }
func (h *hitLog) has(s string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range h.hits {
		if x == s {
			return true
		}
	}
	return false
}

// testChromium starts a headless Chromium with a tab on other.test open,
// serving ok.test and other.test from this process.
func testChromium(t *testing.T) (chrome laptopChrome, hits *hitLog, ok, other func(string) string, port string) {
	t.Helper()
	bin := os.Getenv("BRIDGE_TEST_CHROMIUM")
	if bin == "" {
		t.Skip("BRIDGE_TEST_CHROMIUM not set")
	}
	hits = &hitLog{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		hits.add(host + r.URL.Path)
		port := strings.TrimPrefix(r.Host, host+":")
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "http://other.test:"+port+"/landing", http.StatusFound)
			return
		case "/creds":
			_, _ = fmt.Fprint(w, `<html><body>creds<script>fetch("/api", {method: "POST", headers: {"Authorization": "Bearer SECRET-A", "X-Api-Key": "SECRET-K"}, body: "refresh_token=SECRET-B"});
new EventSource("/events");
var ws = new WebSocket("ws://" + location.host + "/ws"); ws.onopen = function () { ws.send("SECRET-S") }</script></body></html>`)
			return
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: SECRET-E\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(time.Second)
			return
		case "/ws":
			sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			c, brw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			_, _ = fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
			_, _ = c.Write(encodeWSFrame(wsOpText, []byte("SECRET-W"), false))
			if _, p, _, err := readWSMessage(brw.Reader, func(wsFrame) error { return nil }); err == nil {
				hits.add("ws:" + string(p))
			}
			return
		case "/api":
			hits.add("auth:" + r.Header.Get("Authorization"))
			w.Header().Set("Set-Cookie", "sid=SECRET-C; Domain=b\xc3\xbccher.test; Path=/; HttpOnly")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"refresh_token":"SECRET-D"}`)
			return
		case "/framed":
			_, _ = fmt.Fprintf(w, `<html><body>framed<iframe src="http://other.test:%s/frame"></iframe></body></html>`, port)
			return
		}
		_, _ = fmt.Fprintf(w, "<html><head><title>%s%s</title></head><body>%s</body></html>", host, r.URL.Path, r.URL.Path)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	port = strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ok = func(p string) string { return "http://ok.test:" + port + p }
	other = func(p string) string { return "http://other.test:" + port + p }

	// Not t.TempDir: Chromium's helpers are still writing the profile
	// for a moment after it is killed, and that cleanup fails the test.
	dir, err := os.MkdirTemp("", "bridge-chromium-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { time.Sleep(500 * time.Millisecond); _ = os.RemoveAll(dir) })
	cmd := exec.Command(bin, "--headless=new", "--no-sandbox", "--no-first-run", "--disable-gpu",
		"--remote-debugging-port=0", "--user-data-dir="+dir,
		"--host-resolver-rules=MAP *.test 127.0.0.1", "--proxy-server=direct://", "--proxy-bypass-list=*",
		other("/secret"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for i := 0; i < 100; i++ {
		if c, found := chromeAtSwitch(context.Background(), dir); found {
			chrome = c
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if chrome.Addr == "" {
		t.Fatal("chromium never wrote DevToolsActivePort")
	}
	for i := 0; i < 50 && !hits.has("other.test/secret"); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	return chrome, hits, ok, other, port
}

func TestBridgeAgainstChromium(t *testing.T) {
	chrome, hits, ok, other, port := testChromium(t)
	ctx := context.Background()

	allow, _ := parseBridgeAllow([]string{"ok.test"})
	var logMu sync.Mutex
	var log []string
	policy := newBridgePolicy(allow, func(n bridgeNav) { logMu.Lock(); log = append(log, n.String()); logMu.Unlock() })
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	warden, err := startWarden(wctx, chrome, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer warden.Close()
	front, err := startCDPFront(chrome, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()

	// A tool connects through the front, as Playwright MCP does.
	fc := dialFront(t, front, chrome.Path)
	tool := newRawCDP(t, fc.c, fc.br)
	targets := resultOf(t, tool.call("", "Target.getTargets", map[string]any{}))["targetInfos"].([]any)
	for _, ti := range targets {
		if u := ti.(map[string]any)["url"].(string); strings.Contains(u, "other.test") {
			t.Errorf("the tool sees the user's tab %s", u)
		}
	}
	tid := resultOf(t, tool.call("", "Target.createTarget", map[string]any{"url": "about:blank"}))["targetId"].(string)
	sid := resultOf(t, tool.call("", "Target.attachToTarget", map[string]any{"targetId": tid, "flatten": true}))["sessionId"].(string)
	resultOf(t, tool.call(sid, "Page.enable", map[string]any{}))
	resultOf(t, tool.call(sid, "Runtime.enable", map[string]any{}))

	settle := func() { time.Sleep(1500 * time.Millisecond) }
	eval := func(expr string) {
		tool.call(sid, "Runtime.evaluate", map[string]any{"expression": expr, "userGesture": true})
		settle()
	}

	resultOf(t, tool.call(sid, "Page.navigate", map[string]any{"url": ok("/start?token=SECRET")}))
	settle()
	if !hits.has("ok.test/start") {
		t.Fatal("the allowed page never loaded")
	}
	if m := tool.call(sid, "Page.navigate", map[string]any{"url": other("/navigate")}); !strings.Contains(errMessage(m), "not on the bridge's allowlist") {
		t.Errorf("navigate to other.test: %v", m)
	}
	eval(`location.href = "` + other("/script") + `"`)
	eval(`location.href = "` + ok("/redirect") + `"`)
	resultOf(t, tool.call(sid, "Page.navigate", map[string]any{"url": ok("/after")}))
	settle()
	eval(`window.open("` + other("/popup") + `")`)
	eval(`window.open("` + other("/popup-noopener") + `", "_blank", "noopener")`)
	eval(`var f = document.createElement("form"); f.method = "POST"; f.action = "` + other("/form") + `"; document.body.appendChild(f); f.submit()`)
	resultOf(t, tool.call(sid, "Page.navigate", map[string]any{"url": ok("/framed")}))
	settle()

	for _, p := range []string{"/navigate", "/script", "/landing", "/popup", "/popup-noopener", "/form", "/frame"} {
		if hits.has("other.test" + p) {
			t.Errorf("other.test%s was loaded", p)
		}
	}
	for _, p := range []string{"/redirect", "/after", "/framed"} {
		if !hits.has("ok.test" + p) {
			t.Errorf("ok.test%s never loaded", p)
		}
	}
	// The user's own tab still loads anything.
	user, ubr, err := dialWS(ctx, chrome.Addr, chrome.Path)
	if err != nil {
		t.Fatal(err)
	}
	u := newRawCDP(t, user, ubr)
	var userTab string
	for _, ti := range resultOf(t, u.call("", "Target.getTargets", map[string]any{}))["targetInfos"].([]any) {
		m := ti.(map[string]any)
		if strings.Contains(m["url"].(string), "other.test:"+port+"/secret") {
			userTab = m["targetId"].(string)
		}
	}
	if userTab == "" {
		t.Fatal("the user's tab is gone")
	}
	if m := tool.call("", "Target.attachToTarget", map[string]any{"targetId": userTab, "flatten": true}); m["error"] == nil {
		t.Error("the tool attached to the user's tab")
	}
	usid := resultOf(t, u.call("", "Target.attachToTarget", map[string]any{"targetId": userTab, "flatten": true}))["sessionId"].(string)
	resultOf(t, u.call(usid, "Page.navigate", map[string]any{"url": other("/users-own")}))
	settle()
	if !hits.has("other.test/users-own") {
		t.Error("the user's own tab was held to the allowlist")
	}

	logMu.Lock()
	defer logMu.Unlock()
	all := strings.Join(log, "\n")
	t.Logf("navigation log:\n%s", all)
	if strings.Contains(all, "SECRET") || !strings.Contains(all, "ok.test:"+port+"/start") || !strings.Contains(all, "blocked  other.test:"+port+"/script") {
		t.Errorf("log:\n%s", all)
	}
}

// What Chrome sends and receives for a page keeps its credentials; the
// tool watching the network through the front never sees them, and
// can't ask for a body.
func TestBridgeCredentialsAgainstChromium(t *testing.T) {
	chrome, hits, ok, _, _ := testChromium(t)
	front, err := startCDPFront(chrome, newBridgePolicy(nil, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	fc := dialFront(t, front, chrome.Path)
	tool := newRawCDP(t, fc.c, fc.br)
	tid := resultOf(t, tool.call("", "Target.createTarget", map[string]any{"url": "about:blank"}))["targetId"].(string)
	sid := resultOf(t, tool.call("", "Target.attachToTarget", map[string]any{"targetId": tid, "flatten": true}))["sessionId"].(string)
	for _, m := range []string{"Page.enable", "Network.enable", "Audits.enable"} {
		resultOf(t, tool.call(sid, m, map[string]any{}))
	}
	resultOf(t, tool.call(sid, "Page.navigate", map[string]any{"url": ok("/creds")}))
	time.Sleep(2 * time.Second)
	if !hits.has("auth:Bearer SECRET-A") || !hits.has("ws:SECRET-S") {
		t.Fatal("the page's requests never reached the server with their credentials")
	}
	var events []string
	var apiRequest string
	for drained := false; !drained; {
		select {
		case e := <-tool.events:
			b, _ := json.Marshal(e)
			events = append(events, string(b))
			if p, _ := e["params"].(map[string]any); p != nil && e["method"] == "Network.requestWillBeSent" {
				if r, _ := p["request"].(map[string]any); r != nil && strings.HasSuffix(r["url"].(string), "/api") {
					apiRequest, _ = p["requestId"].(string)
				}
			}
		default:
			drained = true
		}
	}
	all := strings.Join(events, "\n")
	if apiRequest == "" || !strings.Contains(all, "Network.responseReceivedExtraInfo") || !strings.Contains(all, "Network.webSocketFrameReceived") || !strings.Contains(all, "Network.webSocketFrameSent") || !strings.Contains(all, "Network.eventSourceMessageReceived") {
		t.Fatalf("the tool never saw the request and its response:\n%s", all)
	}
	if strings.Contains(all, "SECRET") {
		t.Errorf("a credential reached the tool:\n%s", all)
	}
	if m := tool.call(sid, "Network.getResponseBody", map[string]any{"requestId": apiRequest}); !strings.HasPrefix(errMessage(m), "repose browser bridge: ") {
		t.Errorf("getResponseBody: %v", m)
	}
}

// Playwright's connectOverCDP, the call Playwright MCP makes, through
// the front: with no allowlist and with one. BRIDGE_TEST_PLAYWRIGHT is a
// playwright-core directory (the guest's, from repose-playwright-mcp).
func TestBridgeWithPlaywright(t *testing.T) {
	pw := os.Getenv("BRIDGE_TEST_PLAYWRIGHT")
	if pw == "" {
		t.Skip("BRIDGE_TEST_PLAYWRIGHT not set")
	}
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow=%v", allowed), func(t *testing.T) {
			chrome, hits, ok, other, _ := testChromium(t)
			var allow bridgeAllow
			if allowed {
				allow, _ = parseBridgeAllow([]string{"ok.test"})
			}
			policy := newBridgePolicy(allow, func(n bridgeNav) { t.Log(n.String()) })
			if allowed {
				wctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				w, err := startWarden(wctx, chrome, policy)
				if err != nil {
					t.Fatal(err)
				}
				defer w.Close()
			}
			front, err := startCDPFront(chrome, policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer front.Close()
			out, err := exec.Command("node", "testdata/bridge_playwright.cjs", pw, strconv.Itoa(front.Port()), ok("/pw"), other("/pw")).CombinedOutput()
			if err != nil {
				t.Fatalf("playwright: %v\n%s", err, out)
			}
			var r struct {
				Pages     []string
				Title     string
				OtherErr  string
				ClickErr  string
				Shot      int
				CookieErr string
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			t.Logf("%+v", r)
			if !strings.HasPrefix(r.Title, "ok.test/pw") || r.Shot == 0 || !strings.Contains(r.CookieErr, "hand out your cookies") {
				t.Errorf("%+v", r)
			}
			if allowed {
				for _, u := range r.Pages {
					if strings.Contains(u, "other.test") {
						t.Errorf("Playwright sees the user's tab %s", u)
					}
				}
				if !strings.Contains(r.OtherErr, "not on the bridge's allowlist") || r.ClickErr == "" || hits.has("other.test/pwclick") || hits.has("other.test/pw") {
					t.Errorf("other.test got through: %+v", r)
				}
			} else if r.OtherErr != "" || !hits.has("other.test/pwclick") {
				t.Errorf("without --allow other.test is fine: %+v", r)
			}
		})
	}
}

// The guest's chrome-devtools-mcp, run as an agent runs it, through the
// front with an allowlist. BRIDGE_TEST_DEVTOOLS_MCP is its entry script
// (build/src/bin/chrome-devtools-mcp.js of repose-chrome-devtools-mcp).
func TestBridgeWithChromeDevtoolsMCP(t *testing.T) {
	entry := os.Getenv("BRIDGE_TEST_DEVTOOLS_MCP")
	if entry == "" {
		t.Skip("BRIDGE_TEST_DEVTOOLS_MCP not set")
	}
	chrome, hits, ok, other, _ := testChromium(t)
	allow, _ := parseBridgeAllow([]string{"ok.test"})
	policy := newBridgePolicy(allow, func(n bridgeNav) { t.Log(n.String()) })
	wctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	w, err := startWarden(wctx, chrome, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	front, err := startCDPFront(chrome, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	out, err := exec.Command("node", "testdata/bridge_devtools_mcp.cjs", entry, strconv.Itoa(front.Port()), ok("/mcp"), other("/mcp")).CombinedOutput()
	if err != nil {
		t.Fatalf("chrome-devtools-mcp: %v\n%s", err, out)
	}
	t.Logf("%s", out)
	var r struct{ NewPage, Other, List string }
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !hits.has("ok.test/mcp") || hits.has("other.test/mcp") {
		t.Errorf("hits: %v", hits.hits)
	}
	if !strings.Contains(r.Other, "not on the bridge's allowlist") {
		t.Errorf("navigate_page to other.test said: %s", r.Other)
	}
	if strings.Contains(r.List, "other.test") {
		t.Errorf("list_pages shows the user's tab: %s", r.List)
	}
}
