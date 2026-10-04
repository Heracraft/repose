package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What the bridge lets through (DECISIONS I-311). The front reads every
// CDP message between the machine's browser tools and the laptop's
// Chrome. An agent on the machine has a shell and can speak CDP to the
// guest's 127.0.0.1:9224 itself, so anything an MCP server is told
// (Playwright MCP's --allowed-origins and the like) is advice, not a
// boundary; this is the one place every message passes.
//
// Always, with or without --allow:
//   - methods that reach past the browser into the laptop are refused:
//     file inputs and drags from laptop paths, closing or crashing Chrome,
//     extensions, permissions (clipboard, camera, microphone), certificate
//     errors, tethering, the page-side protocol binding, and the
//     unflattened sessions whose traffic would be wrapped where this
//     cannot read it; setting the download folder is answered as done and
//     dropped (Playwright sets one on every connect);
//   - navigations to anything but web pages (file:, chrome:,
//     chrome-extension:, view-source: and so on) are refused, and targets
//     showing them (settings, extensions' pages and workers) are hidden;
//   - cookies do not leave Chrome: the cookie-reading and cookie-clearing
//     methods are refused and Cookie and Set-Cookie headers are removed
//     from the network events the tools see. Pages still use the cookies;
//     that is the point of the bridge.
//   - only the browser's websocket and /json/version are served; the
//     other /json endpoints and per-page websockets are 404.
//
// With --allow, additionally: the agents see only tabs on allowed hosts
// (and blank tabs they or their pages opened), cannot attach to any other
// target, cannot navigate or open a tab anywhere else, and cannot set or
// delete cookies for other hosts; and a watcher connection of the
// bridge's own (bridgewarden.go) fails every document request outside the
// list in the tabs they can see, so a click, a script's location=, a
// redirect or a form post is caught too.
//
// The navigation log is one line per top-level page load, printed on the
// user's own terminal: host and path, never the query or fragment, and
// nothing is written anywhere else.

// bridgeAllow is --allow's list: "example.com", or "*.example.com" for
// example.com and every subdomain.
type bridgeAllow []string

// parseBridgeAllow checks and normalises --allow's values.
func parseBridgeAllow(vals []string) (bridgeAllow, error) {
	var out bridgeAllow
	for _, v := range vals {
		for _, p := range strings.Split(v, ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			host := strings.TrimPrefix(p, "*.")
			if strings.ContainsAny(p, "/:@?#") {
				return nil, fmt.Errorf("takes a host, like github.com or *.github.com, not %q", v)
			}
			if host == "" || strings.Contains(host, "*") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
				return nil, fmt.Errorf("takes a host, like github.com or *.github.com, not %q", v)
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// has says host (no port) is on the list.
func (a bridgeAllow) has(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	for _, p := range a {
		if base, ok := strings.CutPrefix(p, "*."); ok {
			if host == base || strings.HasSuffix(host, "."+base) {
				return true
			}
		} else if host == p {
			return true
		}
	}
	return false
}

type urlKind int

const (
	urlWeb   urlKind = iota // http, https: a host to check
	urlBlank                // about:blank, about:srcdoc, "" (nothing loaded yet)
	urlData                 // data: (an opaque origin with no cookies)
	urlError                // chrome-error:// (Chrome's page for a failed load)
	urlOther                // file:, chrome:, chrome-extension:, devtools:, ...
)

// classifyURL is what kind of page u is and, for web pages (and blob:
// URLs, whose origin is inside), its host.
func classifyURL(raw string) (urlKind, string) {
	if raw == "" {
		return urlBlank, ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return urlOther, ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss":
		if u.Hostname() == "" {
			return urlOther, ""
		}
		return urlWeb, u.Hostname()
	case "blob":
		return classifyURL(u.Opaque + u.Path)
	case "about":
		if o := strings.ToLower(u.Opaque); o == "blank" || o == "srcdoc" {
			return urlBlank, ""
		}
	case "data":
		return urlData, ""
	case "chrome-error":
		return urlError, ""
	}
	return urlOther, ""
}

// logPlace is a URL as the navigation log prints it: host and path, no
// query or fragment (they carry tokens), the path cut at 80 characters.
func logPlace(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		kind, _ := classifyURL(raw)
		if kind == urlOther {
			if s, _, ok := strings.Cut(raw, ":"); ok {
				return s + ":…"
			}
		}
		return "(page)"
	}
	p := u.EscapedPath()
	if len(p) > 80 {
		p = p[:79] + "…"
	}
	return u.Host + p
}

// bridgeNav is one line of the navigation log.
type bridgeNav struct {
	At      time.Time
	Place   string // logPlace
	Blocked bool
}

func (n bridgeNav) String() string {
	if n.Blocked {
		return fmt.Sprintf("%s  blocked  %s", n.At.Format("15:04:05"), n.Place)
	}
	return fmt.Sprintf("%s  %s", n.At.Format("15:04:05"), n.Place)
}

// cdpTargetInfo is the part of CDP's TargetInfo the policy reads.
type cdpTargetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	OpenerID string `json:"openerId"`
}

func topLevelTarget(typ string) bool { return typ == "page" || typ == "tab" }

// bridgePolicy is the bridge's state, shared by every connection through
// the front and the warden.
type bridgePolicy struct {
	allow bridgeAllow // nil: no allowlist
	log   func(bridgeNav)

	mu sync.Mutex
	// lent is every top-level target the agents may see: once seen, a
	// tab stays theirs, and the warden holds its documents to the list.
	lent map[string]bool
	info map[string]cdpTargetInfo
	// seen is the loader ids already logged: both MCP servers see the
	// same navigation.
	seen map[string]bool
	// lastBlock dedupes blocked lines: a refused navigate and the
	// warden's failed request are one event.
	lastBlock map[string]time.Time
}

func newBridgePolicy(allow bridgeAllow, log func(bridgeNav)) *bridgePolicy {
	if log == nil {
		log = func(bridgeNav) {}
	}
	return &bridgePolicy{allow: allow, log: log, lent: map[string]bool{}, info: map[string]cdpTargetInfo{}, seen: map[string]bool{}, lastBlock: map[string]time.Time{}}
}

// allowlisted says --allow is set.
func (p *bridgePolicy) allowlisted() bool { return p.allow != nil }

// observe records a target and says whether the agents may see it. With
// --allow a top-level target they may see becomes lent.
func (p *bridgePolicy) observe(ti cdpTargetInfo) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.observeLocked(ti)
}

func (p *bridgePolicy) observeLocked(ti cdpTargetInfo) bool {
	if ti.TargetID != "" {
		p.info[ti.TargetID] = ti
	}
	kind, host := classifyURL(ti.URL)
	if kind == urlOther {
		return false
	}
	if !p.allowlisted() || p.lent[ti.TargetID] {
		return true
	}
	vis := false
	switch kind {
	case urlWeb:
		vis = p.allow.has(host)
	case urlBlank, urlData:
		// A blank tab is the agents' when they (or a page of theirs)
		// opened it; a popup from one of your other tabs is not.
		vis = ti.OpenerID == "" || p.lent[ti.OpenerID]
	}
	if vis && topLevelTarget(ti.Type) && ti.TargetID != "" {
		p.lent[ti.TargetID] = true
	}
	return vis
}

// visibleID is observe for a target named only by id (attachToTarget).
func (p *bridgePolicy) visibleID(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lent[id] {
		return true
	}
	ti, ok := p.info[id]
	if !ok {
		return !p.allowlisted()
	}
	return p.observeLocked(ti)
}

func (p *bridgePolicy) isLent(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lent[id]
}

func (p *bridgePolicy) markLent(id string) {
	if !p.allowlisted() || id == "" {
		return
	}
	p.mu.Lock()
	p.lent[id] = true
	p.mu.Unlock()
}

func (p *bridgePolicy) forget(id string) {
	p.mu.Lock()
	delete(p.info, id)
	delete(p.lent, id)
	p.mu.Unlock()
}

// docAllowed says a document may load at u in a lent tab.
func (p *bridgePolicy) docAllowed(u string) bool {
	kind, host := classifyURL(u)
	switch kind {
	case urlWeb:
		return !p.allowlisted() || p.allow.has(host)
	case urlBlank, urlData, urlError:
		return true
	}
	return false
}

// navRefusal is why a navigation a tool asked for is refused, or "".
func (p *bridgePolicy) navRefusal(u string) string {
	if u == "" {
		return ""
	}
	kind, host := classifyURL(u)
	switch kind {
	case urlOther, urlError:
		return "repose browser bridge: only web pages open through the bridge (http and https), not " + logPlace(u)
	case urlWeb:
		if p.allowlisted() && !p.allow.has(host) {
			return bridgeBlockedMessage(host)
		}
	}
	return ""
}

// bridgeBlockedMessage is what a tool is told when --allow stops it; the
// agent guide tells agents what to do with it.
func bridgeBlockedMessage(host string) string {
	return "repose browser bridge: " + host + " is not on the bridge's allowlist (--allow). Ask the user to add it; do not work around it"
}

// logNav prints a line, once per navigation.
func (p *bridgePolicy) logNav(loaderID, u string, blocked bool) {
	kind, _ := classifyURL(u)
	if kind != urlWeb && !blocked {
		return
	}
	now := time.Now()
	place := logPlace(u)
	p.mu.Lock()
	if loaderID != "" {
		if p.seen[loaderID] {
			p.mu.Unlock()
			return
		}
		p.seen[loaderID] = true
		if len(p.seen) > 4096 {
			p.seen = map[string]bool{loaderID: true}
		}
	}
	if blocked {
		if t, ok := p.lastBlock[place]; ok && now.Sub(t) < 2*time.Second {
			p.mu.Unlock()
			return
		}
		p.lastBlock[place] = now
	}
	p.mu.Unlock()
	p.log(bridgeNav{At: now, Place: place, Blocked: blocked})
}

// alwaysRefused are the methods the bridge never passes, and why.
var alwaysRefused = map[string]string{
	"Browser.close":                       "the bridge doesn't let tools close your Chrome",
	"Browser.crash":                       "the bridge doesn't let tools close your Chrome",
	"Browser.crashGpuProcess":             "the bridge doesn't let tools close your Chrome",
	"Browser.grantPermissions":            "the bridge doesn't grant permissions (clipboard, camera, microphone, location) in your Chrome",
	"Browser.setPermission":               "the bridge doesn't grant permissions (clipboard, camera, microphone, location) in your Chrome",
	"DOM.setFileInputFiles":               "file uploads through the bridge would read files on the laptop; upload by hand, or use the machine's own browser",
	"Page.setFileInputFiles":              "file uploads through the bridge would read files on the laptop; upload by hand, or use the machine's own browser",
	"DOM.getFileInfo":                     "the bridge doesn't show paths on the laptop",
	"Target.exposeDevToolsProtocol":       "the bridge doesn't give pages the DevTools protocol",
	"Target.sendMessageToTarget":          "the bridge takes flattened sessions only (flatten: true)",
	"Security.setIgnoreCertificateErrors": "the bridge doesn't turn off certificate checks in your Chrome",
	"Network.getCookies":                  "the bridge doesn't hand out your cookies; pages still use them",
	"Network.getAllCookies":               "the bridge doesn't hand out your cookies; pages still use them",
	"Storage.getCookies":                  "the bridge doesn't hand out your cookies; pages still use them",
	"Network.clearBrowserCookies":         "the bridge doesn't clear your cookies",
	"Storage.clearCookies":                "the bridge doesn't clear your cookies",
	// Bodies of requests and responses can carry what a login hands out
	// (tokens, session ids), including bodies a page's own JavaScript may
	// not read (another site's, hidden by CORS). The tools read pages,
	// not traffic.
	"Network.getResponseBody":                         bodyRefusal,
	"Network.getRequestPostData":                      bodyRefusal,
	"Network.getResponseBodyForInterception":          bodyRefusal,
	"Network.takeResponseBodyForInterceptionAsStream": bodyRefusal,
	"Network.searchInResponseBody":                    bodyRefusal,
	"Network.streamResourceContent":                   bodyRefusal,
	"Network.loadNetworkResource":                     bodyRefusal,
	"Fetch.getResponseBody":                           bodyRefusal,
	"Fetch.takeResponseBodyAsStream":                  bodyRefusal,
	"Page.getResourceContent":                         bodyRefusal,
	"Page.searchInResource":                           bodyRefusal,
	"Audits.getEncodedResponse":                       bodyRefusal,
}

const bodyRefusal = "the bridge doesn't hand out request or response bodies; read the page instead"

// quietlyIgnored are answered as done without reaching Chrome. Playwright
// sets the download folder whenever it connects, to a folder on the
// machine; refusing would fail its connect, and passing it would let a
// tool choose where on the laptop a download is written. Downloads go
// where Chrome puts them.
var quietlyIgnored = map[string]bool{
	"Browser.setDownloadBehavior": true,
	"Page.setDownloadBehavior":    true,
}

// refusedDomains are whole domains the bridge never passes.
var refusedDomains = map[string]string{
	"Extensions": "the bridge doesn't reach your extensions",
	"Tethering":  "the bridge doesn't open ports on the laptop",
	// These read what any site stored in your Chrome, not only the page's
	// own: another site's local storage, databases and cached responses.
	"DOMStorage":   "the bridge doesn't read sites' stored data; a page can still read its own",
	"IndexedDB":    "the bridge doesn't read sites' stored data; a page can still read its own",
	"CacheStorage": "the bridge doesn't read sites' stored data; a page can still read its own",
}

// refusal is why a command from a tool is refused, or "". m is the whole
// message as decoded, which is what is forwarded if nothing is refused,
// so what was checked is exactly what Chrome gets.
func (p *bridgePolicy) refusal(m map[string]any) string {
	method, _ := m["method"].(string)
	params, _ := m["params"].(map[string]any)
	if why, ok := alwaysRefused[method]; ok {
		return "repose browser bridge: " + why + "."
	}
	domain, _, _ := strings.Cut(method, ".")
	if why, ok := refusedDomains[domain]; ok {
		return "repose browser bridge: " + why + "."
	}
	str := func(k string) string { s, _ := params[k].(string); return s }
	switch method {
	case "Input.dispatchDragEvent":
		if d, ok := params["data"].(map[string]any); ok {
			if f, ok := d["files"].([]any); ok && len(f) > 0 {
				return "repose browser bridge: file drags through the bridge would read files on the laptop."
			}
		}
	case "Page.navigate", "Target.createTarget":
		return p.navRefusal(str("url"))
	case "Target.attachToTarget", "Target.setAutoAttach":
		on, _ := params["autoAttach"].(bool)
		if f, _ := params["flatten"].(bool); !f && (method == "Target.attachToTarget" || on) {
			return "repose browser bridge: the bridge takes flattened sessions only (flatten: true)."
		}
	}
	switch method {
	case "Target.attachToTarget", "Target.activateTarget", "Target.closeTarget", "Target.getTargetInfo", "Target.detachFromTarget":
		if id := str("targetId"); id != "" && !p.visibleID(id) {
			return "No target with given id found"
		}
	}
	if !p.allowlisted() {
		return ""
	}
	hostOK := func(v any) bool {
		c, _ := v.(map[string]any)
		if c == nil {
			return true
		}
		if u, _ := c["url"].(string); u != "" {
			if k, h := classifyURL(u); k != urlWeb || !p.allow.has(h) {
				return false
			}
		}
		if d, _ := c["domain"].(string); d != "" && !p.allow.has(strings.TrimPrefix(d, ".")) {
			return false
		}
		return true
	}
	switch method {
	case "Network.setCookie", "Network.deleteCookies":
		if !hostOK(params) {
			return "repose browser bridge: cookies for hosts off the allowlist (--allow) are left alone."
		}
	case "Network.setCookies", "Storage.setCookies":
		cs, _ := params["cookies"].([]any)
		for _, c := range cs {
			if !hostOK(c) {
				return "repose browser bridge: cookies for hosts off the allowlist (--allow) are left alone."
			}
		}
	case "Storage.clearDataForOrigin", "Storage.clearDataForStorageKey":
		o := str("origin")
		if o == "" {
			o = str("storageKey")
		}
		if k, h := classifyURL(o); k != urlWeb || !p.allow.has(h) {
			return "repose browser bridge: data for hosts off the allowlist (--allow) is left alone."
		}
	}
	return ""
}

// cdpConn is one tool's connection through the front: what it sends is
// checked, what Chrome sends back is filtered.
type cdpConn struct {
	policy   *bridgePolicy
	toClient *lockedWriter // unmasked frames
	toChrome *lockedWriter // masked frames

	mu sync.Mutex
	// pending is the commands whose answers are filtered, by
	// "<sessionId>/<id>"; pendingIDs counts them by id alone, so an
	// answer to anything else passes without being decoded.
	pending    map[string]string
	pendingIDs map[string]int
	// injected is the ids of the front's own commands (detaching a hidden
	// target the tool's auto-attach picked up); their answers are dropped.
	injected map[string]bool
	nextID   int64
}

func newCDPConn(p *bridgePolicy, toClient, toChrome *lockedWriter) *cdpConn {
	return &cdpConn{policy: p, toClient: toClient, toChrome: toChrome, pending: map[string]string{}, pendingIDs: map[string]int{}, injected: map[string]bool{}, nextID: 1 << 40}
}

// fromClient decides one message from a tool: out is what to send
// Chrome, or reply is the error to answer with instead.
func (c *cdpConn) fromClient(payload []byte) (out, reply []byte) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, cdpError("", "", "repose browser bridge: not a CDP message")
	}
	id, _ := m["id"].(json.Number)
	sid, _ := m["sessionId"].(string)
	method, _ := m["method"].(string)
	if quietlyIgnored[method] {
		r := map[string]any{"id": id, "result": map[string]any{}}
		if sid != "" {
			r["sessionId"] = sid
		}
		b, _ := json.Marshal(r)
		return nil, b
	}
	if why := c.policy.refusal(m); why != "" {
		if method == "Page.navigate" || method == "Target.createTarget" {
			if params, ok := m["params"].(map[string]any); ok {
				u, _ := params["url"].(string)
				c.policy.logNav("", u, true)
			}
		}
		return nil, cdpError(id, sid, why)
	}
	switch method {
	case "Target.getTargets", "Target.createTarget", "Target.getTargetInfo":
		if id != "" {
			c.mu.Lock()
			c.pending[sid+"/"+id.String()] = method
			c.pendingIDs[id.String()]++
			c.mu.Unlock()
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, cdpError(id, sid, "repose browser bridge: not a CDP message")
	}
	return out, nil
}

func cdpError(id json.Number, sid, msg string) []byte {
	m := map[string]any{"error": map[string]any{"code": -32000, "message": msg}}
	if id != "" {
		m["id"] = id
	}
	if sid != "" {
		m["sessionId"] = sid
	}
	b, _ := json.Marshal(m)
	return b
}

// inject sends a command of the front's own on the tool's connection.
func (c *cdpConn) inject(sid, method string, params any) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.injected[strconv.FormatInt(id, 10)] = true
	c.mu.Unlock()
	m := map[string]any{"id": id, "method": method, "params": params}
	if sid != "" {
		m["sessionId"] = sid
	}
	b, _ := json.Marshal(m)
	_ = c.toChrome.write(encodeWSFrame(wsOpText, b, true))
}

// filteredEvents are the events fromChrome decodes, with every event of
// filteredDomains; everything else passes as Chrome sent it.
var filteredEvents = map[string]bool{
	"Target.targetCreated":                       true,
	"Target.targetInfoChanged":                   true,
	"Target.attachedToTarget":                    true,
	"Target.receivedMessageFromTarget":           true,
	"Page.frameNavigated":                        true,
	"Network.requestWillBeSent":                  true,
	"Network.requestWillBeSentExtraInfo":         true,
	"Network.responseReceived":                   true,
	"Network.responseReceivedExtraInfo":          true,
	"Network.responseReceivedEarlyHints":         true,
	"Network.webSocketWillSendHandshakeRequest":  true,
	"Network.webSocketHandshakeResponseReceived": true,
	"Fetch.requestPaused":                        true,
	"Fetch.authRequired":                         true,
}

// filteredDomains are the domains whose events carry requests and
// responses, and so may carry credentials.
var filteredDomains = map[string]bool{"Network": true, "Fetch": true, "Audits": true}

func filteredEvent(method string) bool {
	domain, _, _ := strings.Cut(method, ".")
	return filteredEvents[method] || filteredDomains[domain]
}

// cdpPeek reads the id or the method from the front of a message as
// Chrome writes it ({"id":N,... or {"method":"X",...) without decoding
// the rest, which may be megabytes of screenshot.
func cdpPeek(payload []byte) (id, method string, ok bool) {
	if rest, found := bytes.CutPrefix(payload, []byte(`{"id":`)); found {
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i > 0 && i < len(rest) && (rest[i] == ',' || rest[i] == '}') {
			return string(rest[:i]), "", true
		}
		return "", "", false
	}
	if rest, found := bytes.CutPrefix(payload, []byte(`{"method":"`)); found {
		if i := bytes.IndexByte(rest, '"'); i > 0 {
			return "", string(rest[:i]), true
		}
	}
	return "", "", false
}

// fromChrome filters one message from Chrome: keep false drops it, out
// non-nil replaces it.
func (c *cdpConn) fromChrome(payload []byte) (out []byte, keep bool) {
	if id, method, ok := cdpPeek(payload); ok {
		if id != "" {
			c.mu.Lock()
			interesting := c.pendingIDs[id] > 0 || c.injected[id]
			c.mu.Unlock()
			if !interesting {
				return nil, true
			}
		} else if !filteredEvent(method) {
			return nil, true
		}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, true
	}
	var sid, method string
	_ = json.Unmarshal(m["sessionId"], &sid)
	_ = json.Unmarshal(m["method"], &method)
	if raw, ok := m["id"]; ok {
		id := string(raw)
		c.mu.Lock()
		if c.injected[id] {
			delete(c.injected, id)
			c.mu.Unlock()
			return nil, false
		}
		kind, ok := c.pending[sid+"/"+id]
		if ok {
			delete(c.pending, sid+"/"+id)
			if c.pendingIDs[id]--; c.pendingIDs[id] <= 0 {
				delete(c.pendingIDs, id)
			}
		}
		c.mu.Unlock()
		if !ok || m["result"] == nil {
			return nil, true
		}
		return c.filterResult(kind, m)
	}
	return c.filterEvent(method, sid, m)
}

func (c *cdpConn) filterResult(kind string, m map[string]json.RawMessage) ([]byte, bool) {
	switch kind {
	case "Target.createTarget":
		var r struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(m["result"], &r) == nil {
			c.policy.markLent(r.TargetID)
		}
		return nil, true
	case "Target.getTargetInfo":
		var r struct {
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(m["result"], &r) == nil && !c.policy.observe(r.TargetInfo) {
			delete(m, "result")
			m["error"], _ = json.Marshal(map[string]any{"code": -32602, "message": "No target with given id found"})
			b, _ := json.Marshal(m)
			return b, true
		}
		return nil, true
	case "Target.getTargets":
		var r struct {
			TargetInfos []json.RawMessage `json:"targetInfos"`
		}
		if json.Unmarshal(m["result"], &r) != nil {
			return nil, true
		}
		kept := make([]json.RawMessage, 0, len(r.TargetInfos))
		for _, raw := range r.TargetInfos {
			var ti cdpTargetInfo
			if json.Unmarshal(raw, &ti) == nil && c.policy.observe(ti) {
				kept = append(kept, raw)
			}
		}
		m["result"], _ = json.Marshal(map[string]any{"targetInfos": kept})
		b, _ := json.Marshal(m)
		return b, true
	}
	return nil, true
}

func (c *cdpConn) filterEvent(method, sid string, m map[string]json.RawMessage) ([]byte, bool) {
	switch method {
	case "Target.receivedMessageFromTarget":
		return nil, false
	case "Target.targetCreated", "Target.targetInfoChanged":
		var p struct {
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(m["params"], &p) == nil && !c.policy.observe(p.TargetInfo) {
			return nil, false
		}
		return nil, true
	case "Target.attachedToTarget":
		var p struct {
			SessionID  string        `json:"sessionId"`
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(m["params"], &p) == nil && !c.policy.observe(p.TargetInfo) {
			// The tool's auto-attach picked up a target it may not see:
			// let it go (which also lets it run, if it was waiting).
			c.inject(sid, "Target.detachFromTarget", map[string]string{"sessionId": p.SessionID})
			return nil, false
		}
		return nil, true
	case "Page.frameNavigated":
		var p struct {
			Frame struct {
				ParentID string `json:"parentId"`
				LoaderID string `json:"loaderId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		if json.Unmarshal(m["params"], &p) == nil && p.Frame.ParentID == "" {
			c.policy.logNav(p.Frame.LoaderID, p.Frame.URL, false)
		}
		return nil, true
	}
	// Network, Fetch and Audits events: no credentials.
	var params any
	if err := json.Unmarshal(m["params"], &params); err != nil {
		return nil, true
	}
	var changed bool
	if strings.HasPrefix(method, "Audits.") {
		params, changed = scrubKeys(params, auditKeys)
	} else {
		params, changed = scrubCredentials(params)
	}
	if !changed {
		return nil, true
	}
	m["params"], _ = json.Marshal(params)
	b, _ := json.Marshal(m)
	return b, true
}

// credentialHeaders are the headers removed from what the tools are
// sent: cookies, and the ones that carry a login or a key. Chrome still
// sends and receives them; only the copy for the tools loses them.
var credentialHeaders = map[string]bool{
	"cookie":               true,
	"set-cookie":           true,
	"set-cookie2":          true,
	"authorization":        true,
	"proxy-authorization":  true,
	"x-api-key":            true,
	"x-auth-token":         true,
	"x-access-token":       true,
	"x-amz-security-token": true,
	"x-goog-api-key":       true,
	"x-vault-token":        true,
	"private-token":        true,
}

// credentialKeys are the fields of network and fetch events that carry
// cookie values or a request body (a login form, a token exchange).
var credentialKeys = map[string]bool{
	"associatedCookies": true, "blockedCookies": true, "exemptedCookies": true,
	"postData": true, "postDataEntries": true,
}

// auditKeys are the fields of Audits events that carry a cookie value:
// Chrome puts a whole Set-Cookie line there for a cookie it rejected.
var auditKeys = map[string]bool{"rawCookieLine": true}

// scrubCredentials removes credential headers, the header text lines
// that carry them, the cookie lists and request bodies from a decoded
// event: the value to send, and whether anything was removed.
func scrubCredentials(v any) (any, bool) {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			switch {
			case credentialHeaders[strings.ToLower(k)] || credentialKeys[k]:
				delete(t, k)
				changed = true
			case k == "headersText" || k == "requestHeadersText":
				if s, ok := x.(string); ok {
					if s2 := scrubHeaderText(s); s2 != s {
						t[k] = s2
						changed = true
					}
				}
			default:
				if nx, ch := scrubCredentials(x); ch {
					t[k] = nx
					changed = true
				}
			}
		}
		return t, changed
	case []any:
		kept := make([]any, 0, len(t))
		for _, x := range t {
			if h, ok := x.(map[string]any); ok {
				if n, ok := h["name"].(string); ok && credentialHeaders[strings.ToLower(n)] {
					changed = true
					continue
				}
			}
			nx, ch := scrubCredentials(x)
			changed = changed || ch
			kept = append(kept, nx)
		}
		return kept, changed
	}
	return v, false
}

// scrubKeys removes every field named in keys, at any depth.
func scrubKeys(v any, keys map[string]bool) (any, bool) {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if keys[k] {
				delete(t, k)
				changed = true
			} else if nx, ch := scrubKeys(x, keys); ch {
				t[k] = nx
				changed = true
			}
		}
	case []any:
		for i, x := range t {
			if nx, ch := scrubKeys(x, keys); ch {
				t[i] = nx
				changed = true
			}
		}
	}
	return v, changed
}

func scrubHeaderText(s string) string {
	lines := strings.SplitAfter(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		k, _, _ := strings.Cut(l, ":")
		if credentialHeaders[strings.ToLower(strings.TrimSpace(k))] {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "")
}

// proxyCDP carries one tool's upgraded websocket between client and
// Chrome, message by message, until either side ends.
func (c *cdpConn) proxy(cbr, ubr readerFunc) {
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			op, payload, _, err := cbr(func(f wsFrame) error { return c.toChrome.write(f.raw) })
			if err != nil {
				return
			}
			if op != wsOpText {
				return // CDP is text; anything else ends the connection
			}
			out, reply := c.fromClient(payload)
			if reply != nil {
				if c.toClient.write(encodeWSFrame(wsOpText, reply, false)) != nil {
					return
				}
				continue
			}
			if c.toChrome.write(encodeWSFrame(wsOpText, out, true)) != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			op, payload, raw, err := ubr(func(f wsFrame) error { return c.toClient.write(f.raw) })
			if err != nil {
				return
			}
			if op != wsOpText {
				return
			}
			out, keep := c.fromChrome(payload)
			switch {
			case !keep:
			case out == nil:
				err = c.toClient.write(raw)
			default:
				err = c.toClient.write(encodeWSFrame(wsOpText, out, false))
			}
			if err != nil {
				return
			}
		}
	}()
	<-done
}

// readerFunc is readWSMessage bound to one side's reader.
type readerFunc func(onControl func(wsFrame) error) (byte, []byte, []byte, error)
