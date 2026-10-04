package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCDP is a DevTools server as the front sees it: it upgrades every
// request, records the head, and hands each message it gets to handle,
// sending back whatever handle returns (answers and events, in order).
type fakeCDP struct {
	addr  string
	heads chan string
	got   chan map[string]any
}

func newFakeCDP(t *testing.T, handle func(m map[string]any) []string) *fakeCDP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f := &fakeCDP{addr: l.Addr().String(), heads: make(chan string, 16), got: make(chan map[string]any, 64)}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				head, _, _, err := readRequestHead(br)
				if err != nil {
					return
				}
				f.heads <- string(head)
				_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
				w := &lockedWriter{w: c}
				for {
					op, payload, _, err := readWSMessage(br, func(wsFrame) error { return nil })
					if err != nil || op != wsOpText {
						return
					}
					var m map[string]any
					dec := json.NewDecoder(bytes.NewReader(payload))
					dec.UseNumber()
					if dec.Decode(&m) != nil {
						return
					}
					f.got <- m
					if handle == nil {
						continue
					}
					for _, out := range handle(m) {
						_ = w.write(encodeWSFrame(wsOpText, []byte(out), false))
					}
				}
			}()
		}
	}()
	return f
}

// fakeDevTools is the fake that answers every command with its own
// params as the result.
func fakeDevTools(t *testing.T) (addr string, heads chan string) {
	f := newFakeCDP(t, echoResult)
	return f.addr, f.heads
}

func echoResult(m map[string]any) []string {
	b, _ := json.Marshal(map[string]any{"id": m["id"], "result": m["params"]})
	return []string{string(b)}
}

// cdpClient is a tool on the guest's side of the front.
type cdpClient struct {
	t  *testing.T
	c  net.Conn
	br *bufio.Reader
}

func dialFront(t *testing.T, front *cdpFront, path string) *cdpClient {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(front.Port())))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_, _ = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: 127.0.0.1:9224\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: x\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Extensions: permessage-deflate; client_max_window_bits\r\n\r\n", path)
	br := bufio.NewReader(c)
	_, status, err := readResponseHead(br)
	if err != nil || status != 101 {
		t.Fatalf("upgrade: %d %v", status, err)
	}
	return &cdpClient{t: t, c: c, br: br}
}

func (c *cdpClient) send(s string) {
	c.t.Helper()
	if _, err := c.c.Write(encodeWSFrame(wsOpText, []byte(s), true)); err != nil {
		c.t.Fatal(err)
	}
}

// recv is the next message, or nil after a quiet second.
func (c *cdpClient) recv() map[string]any {
	c.t.Helper()
	_ = c.c.SetReadDeadline(time.Now().Add(time.Second))
	defer func() { _ = c.c.SetReadDeadline(time.Time{}) }()
	_, payload, _, err := readWSMessage(c.br, func(wsFrame) error { return nil })
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		c.t.Fatalf("not JSON from the front: %q", payload)
	}
	return m
}

func errMessage(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	s, _ := e["message"].(string)
	return s
}

func startTestFront(t *testing.T, addr string, policy *bridgePolicy, onAttach func()) *cdpFront {
	t.Helper()
	front, err := startCDPFront(laptopChrome{Addr: addr, Path: "/devtools/browser/abc", Switch: true}, policy, onAttach)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(front.Close)
	return front
}

func TestCDPFrontAnswersVersionAndCarriesTheBrowserWebsocket(t *testing.T) {
	f := newFakeCDP(t, echoResult)
	attached := make(chan struct{}, 4)
	front := startTestFront(t, f.addr, nil, func() { attached <- struct{}{} })
	frontAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(front.Port()))

	// Discovery, as the guest's MCP servers do it, through the tunnel:
	// the Host is the guest's endpoint, and the websocket URL leads back
	// through it.
	get := func(target string) (int, []byte) {
		c, err := net.Dial("tcp", frontAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		_, _ = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: 127.0.0.1:9224\r\nAccept: */*\r\n\r\n", target)
		res, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, body
	}
	for _, target := range []string{"/json/version", "/json/version/"} {
		code, body := get(target)
		var v map[string]string
		if err := json.Unmarshal(body, &v); err != nil || code != 200 {
			t.Fatalf("%s: %d %s %v", target, code, body, err)
		}
		if v["webSocketDebuggerUrl"] != "ws://127.0.0.1:9224/devtools/browser/abc" || v["Browser"] != "Chrome" {
			t.Errorf("%s: %v", target, v)
		}
	}
	// Everything else that would go round the policy is 404, and never
	// reaches Chrome.
	for _, target := range []string{"/json/list", "/json", "/json/new?file:///etc/passwd"} {
		if code, _ := get(target); code != 404 {
			t.Errorf("%s: %d, want 404", target, code)
		}
	}
	c, err := net.Dial("tcp", frontAddr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "GET /devtools/page/ABC HTTP/1.1\r\nHost: 127.0.0.1:9224\r\nUpgrade: websocket\r\n\r\n")
	if res, err := http.ReadResponse(bufio.NewReader(c), nil); err != nil || res.StatusCode != 404 {
		t.Errorf("a page's websocket: %v %v", res, err)
	}
	_ = c.Close()
	select {
	case h := <-f.heads:
		t.Errorf("reached Chrome: %q", h)
	case <-attached:
		t.Error("counted as an attach")
	default:
	}

	// The browser's websocket goes through, without the compression
	// the client offered, message by message both ways.
	cl := dialFront(t, front, "/devtools/browser/abc")
	head := <-f.heads
	if !strings.HasPrefix(head, "GET /devtools/browser/abc HTTP/1.1\r\n") || strings.Contains(head, "Sec-WebSocket-Extensions") {
		t.Errorf("Chrome got %q", head)
	}
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		t.Error("the upgrade did not count as an attach")
	}
	cl.send(`{"id":7,"method":"Runtime.evaluate","params":{"expression":"1+1"},"sessionId":"S1"}`)
	if m := <-f.got; m["method"] != "Runtime.evaluate" || m["sessionId"] != "S1" {
		t.Errorf("Chrome got %v", m)
	}
	if m := cl.recv(); m["id"] != 7.0 || m["result"].(map[string]any)["expression"] != "1+1" {
		t.Errorf("tool got %v", m)
	}
}

// What never passes, with or without --allow.
func TestBridgeRefusesWhatReachesPastTheBrowser(t *testing.T) {
	f := newFakeCDP(t, echoResult)
	front := startTestFront(t, f.addr, newBridgePolicy(nil, nil), nil)
	cl := dialFront(t, front, "/devtools/browser/abc")
	<-f.heads
	refused := []string{
		`{"id":1,"method":"Browser.close"}`,
		`{"id":2,"method":"Browser.grantPermissions","params":{"permissions":["clipboardReadWrite"]}}`,
		`{"id":3,"method":"Page.navigate","params":{"url":"file:///home/me/.ssh/id_ed25519"},"sessionId":"S"}`,
		`{"id":4,"method":"Target.createTarget","params":{"url":"chrome://settings/passwords"}}`,
		`{"id":5,"method":"Network.getAllCookies"}`,
		`{"id":6,"method":"Storage.getCookies","params":{}}`,
		`{"id":7,"method":"DOM.setFileInputFiles","params":{"files":["/home/me/.aws/credentials"],"nodeId":3}}`,
		`{"id":8,"method":"Target.sendMessageToTarget","params":{"message":"{}","sessionId":"S"}}`,
		`{"id":9,"method":"Target.attachToTarget","params":{"targetId":"T"}}`,
		`{"id":10,"method":"Extensions.loadUnpacked","params":{"path":"/tmp/x"}}`,
		`{"id":11,"method":"Input.dispatchDragEvent","params":{"type":"drop","x":1,"y":1,"data":{"items":[],"files":["/etc/passwd"],"dragOperationsMask":1}}}`,
		`{"id":12,"method":"Page.navigate","params":{"url":"view-source:file:///etc/passwd"}}`,
		// Two spellings of one key: the front decides on what it decoded
		// and forwards that, so Chrome can't read a different method.
		`{"id":13,"method":"Runtime.evaluate","method":"Browser.close"}`,
		// Bodies, and other sites' stored data, stay in Chrome.
		`{"id":14,"method":"Network.getResponseBody","params":{"requestId":"1"},"sessionId":"S"}`,
		`{"id":15,"method":"Network.getRequestPostData","params":{"requestId":"1"},"sessionId":"S"}`,
		`{"id":16,"method":"Fetch.getResponseBody","params":{"requestId":"i"},"sessionId":"S"}`,
		`{"id":17,"method":"Network.loadNetworkResource","params":{"frameId":"F","url":"https://other.test/","options":{"disableCache":true,"includeCredentials":true}},"sessionId":"S"}`,
		`{"id":18,"method":"Fetch.takeResponseBodyAsStream","params":{"requestId":"i"},"sessionId":"S"}`,
		`{"id":19,"method":"Page.getResourceContent","params":{"frameId":"F","url":"https://example.com/api"},"sessionId":"S"}`,
		`{"id":20,"method":"Audits.getEncodedResponse","params":{"requestId":"1","encoding":"webp"},"sessionId":"S"}`,
		`{"id":21,"method":"DOMStorage.getDOMStorageItems","params":{"storageId":{"securityOrigin":"https://mail.example","isLocalStorage":true}}}`,
		`{"id":22,"method":"CacheStorage.requestCachedResponse","params":{"cacheId":"c","requestURL":"https://mail.example/"}}`,
		`{"id":23,"method":"IndexedDB.requestData","params":{"securityOrigin":"https://mail.example","databaseName":"d","objectStoreName":"o","indexName":"","skipCount":0,"pageSize":10}}`,
		`{"id":24,"method":"Network.getResponseBodyForInterception","params":{"interceptionId":"x"},"sessionId":"S"}`,
	}
	for i, s := range refused {
		cl.send(s)
		m := cl.recv()
		if m == nil || m["id"] != float64(i+1) || !strings.HasPrefix(errMessage(m), "repose browser bridge: ") {
			t.Errorf("%s: got %v", s, m)
		}
	}
	allowed := []string{
		`{"id":40,"method":"Page.navigate","params":{"url":"https://example.com/"},"sessionId":"S"}`,
		`{"id":41,"method":"Target.createTarget","params":{"url":"about:blank"}}`,
		`{"id":42,"method":"Target.attachToTarget","params":{"targetId":"T","flatten":true}}`,
		`{"id":43,"method":"Target.setAutoAttach","params":{"autoAttach":false}}`,
		`{"id":44,"method":"Input.dispatchDragEvent","params":{"type":"drop","x":1,"y":1,"data":{"items":[],"dragOperationsMask":1}}}`,
		// IO.read stays: Playwright's PDFs come through it, and no
		// stream of a body can be opened.
		`{"id":45,"method":"IO.read","params":{"handle":"h"}}`,
	}
	for _, s := range allowed {
		cl.send(s)
		if m := cl.recv(); m == nil || m["error"] != nil {
			t.Errorf("%s: got %v", s, m)
		}
	}
	// Playwright sets a download folder on the machine whenever it
	// connects: answered as done, never passed.
	cl.send(`{"id":50,"method":"Browser.setDownloadBehavior","params":{"behavior":"allowAndName","downloadPath":"/home/me/.config/autostart"}}`)
	if m := cl.recv(); m == nil || m["error"] != nil || m["result"] == nil {
		t.Errorf("setDownloadBehavior: got %v", m)
	}
	// Only the allowed ones reached Chrome.
	close(f.got)
	var methods []string
	for m := range f.got {
		methods = append(methods, m["method"].(string))
	}
	if got := strings.Join(methods, " "); got != "Page.navigate Target.createTarget Target.attachToTarget Target.setAutoAttach Input.dispatchDragEvent IO.read" {
		t.Errorf("Chrome got %s", got)
	}
}

// Cookie values, credential headers and request bodies never reach the
// tools in network events.
func TestBridgeScrubsCredentialsFromEvents(t *testing.T) {
	f := newFakeCDP(t, func(m map[string]any) []string {
		return []string{
			`{"method":"Network.requestWillBeSent","params":{"requestId":"2","request":{"url":"https://example.com/token","method":"POST","headers":{"Authorization":"Bearer SECRET5","Accept":"*/*"},"hasPostData":true,"postData":"refresh_token=SECRET6","postDataEntries":[{"bytes":"cmVmcmVzaF90b2tlbj1TRUNSRVQ2"}]}},"sessionId":"S"}`,
			`{"method":"Network.requestWillBeSentExtraInfo","params":{"requestId":"2","headers":{"authorization":"Basic SECRET7","X-Api-Key":"SECRET8","proxy-authorization":"Negotiate SECRET9"}},"sessionId":"S"}`,
			`{"method":"Fetch.requestPaused","params":{"requestId":"j","request":{"url":"https://example.com/","headers":{"X-Amz-Security-Token":"SECRET10"},"postData":"password=SECRET11"}},"sessionId":"S"}`,
			`{"method":"Network.responseReceived","params":{"requestId":"2","response":{"status":200,"requestHeadersText":"GET / HTTP/1.1\r\nAuthorization: Bearer SECRET12\r\nAccept: */*\r\n\r\n"}},"sessionId":"S"}`,
			`{"method":"Network.webSocketFrameReceived","params":{"requestId":"w","timestamp":1,"response":{"opcode":1,"mask":false,"payloadData":"{\"token\":\"SECRET14\"}"}},"sessionId":"S"}`,
			`{"method":"Network.webSocketFrameSent","params":{"requestId":"w","timestamp":1,"response":{"opcode":1,"mask":true,"payloadData":"auth SECRET15"}},"sessionId":"S"}`,
			`{"method":"Network.eventSourceMessageReceived","params":{"requestId":"e","timestamp":1,"eventName":"message","eventId":"7","data":"SECRET16"},"sessionId":"S"}`,
			`{"method":"Network.dataReceived","params":{"requestId":"3","timestamp":1,"dataLength":9,"encodedDataLength":9,"data":"U0VDUkVUMTc="},"sessionId":"S"}`,
			`{"method":"Network.requestIntercepted","params":{"interceptionId":"x","request":{"url":"https://example.com/","headers":{"Authorization":"Bearer SECRET13"}}},"sessionId":"S"}`,
			`{"method":"Network.requestWillBeSentExtraInfo","params":{"requestId":"1","headers":{"Cookie":"sid=SECRET","Accept":"*/*"},"associatedCookies":[{"blockedReasons":[],"cookie":{"name":"sid","value":"SECRET"}}]},"sessionId":"S"}`,
			`{"method":"Network.responseReceivedExtraInfo","params":{"requestId":"1","headers":{"set-cookie":"sid=SECRET2","content-type":"text/html"},"blockedCookies":[],"headersText":"HTTP/1.1 200 OK\r\nSet-Cookie: sid=SECRET3\r\nContent-Type: text/html\r\n\r\n"},"sessionId":"S"}`,
			`{"method":"Fetch.requestPaused","params":{"requestId":"i","responseHeaders":[{"name":"Set-Cookie","value":"sid=SECRET4"},{"name":"Content-Type","value":"text/html"}]},"sessionId":"S"}`,
			`{"method":"Network.dataReceived","params":{"requestId":"1","dataLength":5}}`,
		}
	})
	front := startTestFront(t, f.addr, nil, nil)
	cl := dialFront(t, front, "/devtools/browser/abc")
	cl.send(`{"id":1,"method":"Network.enable","sessionId":"S"}`)
	var all []string
	for i := 0; i < 13; i++ {
		m := cl.recv()
		if m == nil {
			t.Fatalf("event %d missing", i)
		}
		b, _ := json.Marshal(m)
		all = append(all, string(b))
	}
	s := strings.Join(all, "\n")
	if l := strings.ToLower(s); strings.Contains(s, "SECRET") || strings.Contains(l, "cookie") || strings.Contains(l, "authorization") || strings.Contains(s, `"postData"`) || strings.Contains(s, "postDataEntries") || strings.Contains(s, "payloadData") || strings.Contains(s, "U0VDUkVUMTc") {
		t.Errorf("credentials reached the tool:\n%s", s)
	}
	for _, want := range []string{`"opcode":1`, `"eventName":"message"`, `"dataLength":9`, `"hasPostData":true`, `Accept: */*`, `"url":"https://example.com/token"`, `"Accept":"*/*"`, `"content-type":"text/html"`, `Content-Type: text/html`, `"name":"Content-Type"`, `"dataLength":5`} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %s:\n%s", want, s)
		}
	}
}

// Chrome writes a whole Set-Cookie line into an issue about a cookie it
// rejected; the tools get the issue without it.
func TestBridgeScrubsRawCookieLineFromAudits(t *testing.T) {
	f := newFakeCDP(t, func(m map[string]any) []string {
		return []string{
			`{"method":"Audits.issueAdded","params":{"issue":{"code":"CookieIssue","details":{"cookieIssueDetails":{"rawCookieLine":"sid=SECRET; Domain=b\u00fccher.example; HttpOnly","cookieWarningReasons":["WarnDomainNonASCII"],"operation":"SetCookie","request":{"requestId":"1","url":"https://example.com/"}}}}},"sessionId":"S"}`,
		}
	})
	front := startTestFront(t, f.addr, nil, nil)
	cl := dialFront(t, front, "/devtools/browser/abc")
	cl.send(`{"id":1,"method":"Audits.enable","sessionId":"S"}`)
	var all []string
	for i := 0; i < 1; i++ {
		m := cl.recv()
		if m == nil {
			t.Fatalf("message %d missing", i)
		}
		b, _ := json.Marshal(m)
		all = append(all, string(b))
	}
	s := strings.Join(all, "\n")
	if strings.Contains(s, "SECRET") || strings.Contains(s, "rawCookieLine") {
		t.Errorf("a cookie line reached the tool:\n%s", s)
	}
	for _, want := range []string{`"WarnDomainNonASCII"`, `"url":"https://example.com/"`} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %s:\n%s", want, s)
		}
	}
}

// With --allow: other hosts are refused, other tabs are hidden, and a
// hidden tab the tool's auto-attach picked up is let go.
func TestBridgeAllowlistHidesAndRefuses(t *testing.T) {
	targets := `[{"targetId":"MINE","type":"page","url":"https://github.com/x","attached":false},` +
		`{"targetId":"MAIL","type":"page","url":"https://mail.example.org/inbox","attached":false},` +
		`{"targetId":"EXT","type":"service_worker","url":"chrome-extension://abc/bg.js","attached":false},` +
		`{"targetId":"POP","type":"page","url":"about:blank","openerId":"MAIL","attached":false},` +
		`{"targetId":"NEW","type":"page","url":"about:blank","attached":false}]`
	f := newFakeCDP(t, func(m map[string]any) []string {
		switch m["method"] {
		case "Target.getTargets":
			return []string{fmt.Sprintf(`{"id":%v,"result":{"targetInfos":%s}}`, m["id"], targets)}
		case "Target.setAutoAttach":
			return []string{
				`{"method":"Target.attachedToTarget","params":{"sessionId":"S-MAIL","targetInfo":{"targetId":"MAIL","type":"page","url":"https://mail.example.org/inbox"},"waitingForDebugger":true}}`,
				`{"method":"Target.attachedToTarget","params":{"sessionId":"S-MINE","targetInfo":{"targetId":"MINE","type":"page","url":"https://github.com/x"},"waitingForDebugger":true}}`,
				fmt.Sprintf(`{"id":%v,"result":{}}`, m["id"]),
			}
		case "Target.detachFromTarget":
			return []string{fmt.Sprintf(`{"id":%v,"result":{}}`, m["id"])}
		}
		return echoResult(m)
	})
	allow, err := parseBridgeAllow([]string{"*.github.com"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lines []string
	policy := newBridgePolicy(allow, func(n bridgeNav) { mu.Lock(); lines = append(lines, n.String()); mu.Unlock() })
	front := startTestFront(t, f.addr, policy, nil)
	cl := dialFront(t, front, "/devtools/browser/abc")
	<-f.heads

	cl.send(`{"id":1,"method":"Target.getTargets"}`)
	m := cl.recv()
	var ids []string
	for _, ti := range m["result"].(map[string]any)["targetInfos"].([]any) {
		ids = append(ids, ti.(map[string]any)["targetId"].(string))
	}
	if got := strings.Join(ids, " "); got != "MINE NEW" {
		t.Errorf("getTargets shows %s, want MINE NEW", got)
	}
	<-f.got

	cl.send(`{"id":2,"method":"Target.setAutoAttach","params":{"autoAttach":true,"waitForDebuggerOnStart":true,"flatten":true}}`)
	if m := cl.recv(); m["method"] != "Target.attachedToTarget" || m["params"].(map[string]any)["sessionId"] != "S-MINE" {
		t.Errorf("first thing the tool sees: %v", m)
	}
	if m := cl.recv(); m["id"] != 2.0 {
		t.Errorf("then: %v", m)
	}
	if m := cl.recv(); m != nil {
		t.Errorf("the front's own detach was answered to the tool: %v", m)
	}
	<-f.got
	if d := <-f.got; d["method"] != "Target.detachFromTarget" || d["params"].(map[string]any)["sessionId"] != "S-MAIL" {
		t.Errorf("the hidden tab was not let go: %v", d)
	}

	for _, s := range []string{
		`{"id":3,"method":"Target.attachToTarget","params":{"targetId":"MAIL","flatten":true}}`,
		`{"id":4,"method":"Target.activateTarget","params":{"targetId":"POP"}}`,
		`{"id":5,"method":"Page.navigate","params":{"url":"https://mail.example.org/inbox?token=abc#x"},"sessionId":"S-MINE"}`,
		`{"id":6,"method":"Target.createTarget","params":{"url":"https://evil.example/"}}`,
		`{"id":7,"method":"Network.setCookie","params":{"name":"a","value":"b","domain":".example.org"}}`,
	} {
		cl.send(s)
		if m := cl.recv(); m == nil || m["error"] == nil {
			t.Errorf("%s: got %v", s, m)
		} else if strings.Contains(s, "navigate") && !strings.Contains(errMessage(m), "mail.example.org is not on the bridge's allowlist") {
			t.Errorf("navigate refusal: %q", errMessage(m))
		}
	}
	for _, s := range []string{
		`{"id":8,"method":"Page.navigate","params":{"url":"https://gist.github.com/"},"sessionId":"S-MINE"}`,
		`{"id":9,"method":"Page.navigate","params":{"url":"https://github.com/login"},"sessionId":"S-MINE"}`,
		`{"id":10,"method":"Target.attachToTarget","params":{"targetId":"NEW","flatten":true}}`,
	} {
		cl.send(s)
		if m := cl.recv(); m == nil || m["error"] != nil {
			t.Errorf("%s: got %v", s, m)
		}
	}
	// Events about hidden targets don't reach the tool.
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "  blocked  mail.example.org/inbox") || !strings.HasSuffix(lines[1], "  blocked  evil.example/") {
		t.Errorf("log: %q", lines)
	}
}

// The navigation log: one line per top-level load, once however many
// tools see it, host and path only.
func TestBridgeNavigationLog(t *testing.T) {
	nav := `{"method":"Page.frameNavigated","params":{"frame":{"id":"F","loaderId":"L1","url":"https://github.com/heracraft/repose/pull/7?token=SECRET#frag","securityOrigin":"https://github.com","mimeType":"text/html"},"type":"Navigation"},"sessionId":"S"}`
	sub := `{"method":"Page.frameNavigated","params":{"frame":{"id":"G","parentId":"F","loaderId":"L2","url":"https://ads.example/frame"},"type":"Navigation"},"sessionId":"S"}`
	f := newFakeCDP(t, func(m map[string]any) []string { return []string{nav, sub} })
	var mu sync.Mutex
	var lines []string
	policy := newBridgePolicy(nil, func(n bridgeNav) { mu.Lock(); lines = append(lines, n.String()); mu.Unlock() })
	front := startTestFront(t, f.addr, policy, nil)
	a := dialFront(t, front, "/devtools/browser/abc")
	b := dialFront(t, front, "/devtools/browser/abc")
	a.send(`{"id":1,"method":"Page.enable","sessionId":"S"}`)
	b.send(`{"id":1,"method":"Page.enable","sessionId":"S"}`)
	for _, c := range []*cdpClient{a, b} {
		if m := c.recv(); m["method"] != "Page.frameNavigated" {
			t.Errorf("the tool got %v", m)
		}
		c.recv()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "  github.com/heracraft/repose/pull/7") || strings.Contains(lines[0], "SECRET") {
		t.Errorf("log: %q", lines)
	}
}

func TestParseBridgeAllow(t *testing.T) {
	a, err := parseBridgeAllow([]string{"GitHub.com", "*.vercel.app, localhost"})
	if err != nil || strings.Join(a, " ") != "github.com *.vercel.app localhost" {
		t.Fatalf("%v %v", a, err)
	}
	for host, want := range map[string]bool{
		"github.com": true, "api.github.com": false, "vercel.app": true, "x.y.vercel.app": true,
		"evilvercel.app": false, "localhost": true, "github.com.": true, "": false,
	} {
		if a.has(host) != want {
			t.Errorf("has(%q) = %v", host, !want)
		}
	}
	for _, bad := range []string{"https://github.com", "github.com/x", "github.com:443", "*", "*.", "a.*.com", "*github.com", ".github.com", "a..b"} {
		if _, err := parseBridgeAllow([]string{bad}); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
	if a, _ := parseBridgeAllow(nil); a != nil {
		t.Error("no --allow is no allowlist")
	}
}

func TestClassifyURLAndLogPlace(t *testing.T) {
	cases := map[string]urlKind{
		"https://a.example/x": urlWeb, "http://a.example": urlWeb, "about:blank": urlBlank, "": urlBlank,
		"about:srcdoc": urlBlank, "data:text/html,hi": urlData, "chrome-error://chromewebdata/": urlError,
		"blob:https://a.example/uuid": urlWeb, "file:///etc/passwd": urlOther, "chrome://settings": urlOther,
		"chrome-extension://abc/x.html": urlOther, "view-source:https://a.example": urlOther, "about:settings": urlOther,
		"devtools://devtools/x": urlOther, "javascript:alert(1)": urlOther,
	}
	for u, want := range cases {
		if got, _ := classifyURL(u); got != want {
			t.Errorf("%q: %v, want %v", u, got, want)
		}
	}
	if _, h := classifyURL("blob:https://a.example/uuid"); h != "a.example" {
		t.Errorf("blob host %q", h)
	}
	for u, want := range map[string]string{
		"https://a.example/reset/x?token=1#y":           "a.example/reset/x",
		"https://a.example":                             "a.example",
		"file:///home/me/secret":                        "file:…",
		"https://a.example/" + strings.Repeat("p", 200): "a.example/" + strings.Repeat("p", 78) + "…",
	} {
		if got := logPlace(u); got != want {
			t.Errorf("logPlace(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestWSFramesRoundTrip(t *testing.T) {
	for _, n := range []int{0, 5, 125, 126, 65535, 65536, 200000} {
		for _, masked := range []bool{false, true} {
			p := bytes.Repeat([]byte("x"), n)
			raw := encodeWSFrame(wsOpText, p, masked)
			f, err := readWSFrame(bufio.NewReader(bytes.NewReader(raw)))
			if err != nil || !f.fin || f.op != wsOpText || !bytes.Equal(f.payload, p) || !bytes.Equal(f.raw, raw) {
				t.Errorf("n=%d masked=%v: %v", n, masked, err)
			}
		}
	}
	// A fragmented message with a ping between its fragments.
	var buf bytes.Buffer
	buf.Write([]byte{0x01, 0x03, 'a', 'b', 'c'})
	buf.Write([]byte{0x89, 0x00})
	buf.Write([]byte{0x80, 0x02, 'd', 'e'})
	var controls int
	op, payload, _, err := readWSMessage(bufio.NewReader(&buf), func(f wsFrame) error { controls++; return nil })
	if err != nil || op != wsOpText || string(payload) != "abcde" || controls != 1 {
		t.Errorf("%v %q %d %v", op, payload, controls, err)
	}
	// A compressed frame (RSV1) is refused.
	_, _, _, err = readWSMessage(bufio.NewReader(bytes.NewReader([]byte{0xC1, 0x01, 'x'})), func(wsFrame) error { return nil })
	if err == nil {
		t.Error("RSV1 frame accepted")
	}
}

func TestWithoutWSExtensions(t *testing.T) {
	head := []byte("GET / HTTP/1.1\r\nHost: x\r\nsec-websocket-extensions: permessage-deflate\r\nUpgrade: websocket\r\n\r\n")
	if got := string(withoutWSExtensions(head)); got != "GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\n\r\n" {
		t.Errorf("%q", got)
	}
}
