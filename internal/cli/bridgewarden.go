package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/heracraft/repose/internal/wsframe"
)

// cdpWarden is --allow's backstop (DECISIONS I-311): a CDP connection of
// the bridge's own to the laptop's Chrome, made before any tool can
// connect. It attaches to every tab, and to the frames inside them, as
// each appears and before it loads anything (waitForDebuggerOnStart), and
// intercepts document requests there (Fetch, Document requests only).
// In a tab the agents can see, a document from a host off the list fails
// with ERR_BLOCKED_BY_CLIENT, whatever started it: a tool's navigate, a
// click, a script's location=, a redirect, a form post, a popup. Your
// other tabs load as usual, one round trip to this process later.
//
// If this connection ends, so does the bridge: without it --allow would
// hold for navigations the tools ask for and not for the rest.
type cdpWarden struct {
	policy *bridgePolicy
	conn   net.Conn
	w      *lockedWriter

	mu     sync.Mutex
	nextID int64
	calls  map[int64]chan wardenReply
	// sessionTab is each of the warden's sessions' tab (a frame's
	// session maps to the tab it is in); tabSession is the reverse, for
	// tabs.
	sessionTab map[string]string
	tabSession map[string]string
	// blocked is each tab's last blocked URL: Chrome's error page for it
	// carries that URL, and is not a page that got through.
	blocked map[string]string

	done chan struct{}
	err  error
}

type wardenReply struct {
	result json.RawMessage
	err    error
}

// startWarden connects and starts watching. With Chrome's switch, Chrome
// asks the user to allow this connection first; ctx bounds the wait.
func startWarden(ctx context.Context, chrome laptopChrome, policy *bridgePolicy) (*cdpWarden, error) {
	conn, br, err := dialWS(ctx, chrome.Addr, chrome.Path)
	if err != nil {
		return nil, err
	}
	w := &cdpWarden{
		policy: policy, conn: conn, w: &lockedWriter{w: conn},
		calls: map[int64]chan wardenReply{}, sessionTab: map[string]string{}, tabSession: map[string]string{}, blocked: map[string]string{},
		done: make(chan struct{}),
	}
	go w.read(br)
	// Every target first, so the policy knows them all before a tool
	// connects; then attach to each tab, now and as they come.
	if _, err := w.call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}); err != nil {
		w.Close()
		return nil, err
	}
	if _, err := w.call(ctx, "", "Target.setAutoAttach", map[string]any{
		"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
		"filter": []map[string]any{{"type": "page"}},
	}); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

// Done is closed when the warden's connection ends.
func (w *cdpWarden) Done() <-chan struct{} { return w.done }

func (w *cdpWarden) Close() { _ = w.conn.Close() }

func (w *cdpWarden) send(sid, method string, params any) int64 {
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	w.mu.Unlock()
	m := map[string]any{"id": id, "method": method, "params": params}
	if sid != "" {
		m["sessionId"] = sid
	}
	b, _ := json.Marshal(m)
	_ = w.w.write(encodeWSFrame(wsOpText, b, true))
	return id
}

func (w *cdpWarden) call(ctx context.Context, sid, method string, params any) (json.RawMessage, error) {
	ch := make(chan wardenReply, 1)
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	w.calls[id] = ch
	w.mu.Unlock()
	m := map[string]any{"id": id, "method": method, "params": params}
	if sid != "" {
		m["sessionId"] = sid
	}
	b, _ := json.Marshal(m)
	if err := w.w.write(encodeWSFrame(wsOpText, b, true)); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-w.done:
		return nil, errors.New("the connection to Chrome closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (w *cdpWarden) read(br *bufio.Reader) {
	defer close(w.done)
	for {
		op, payload, _, err := readWSMessage(br, func(f wsFrame) error {
			if f.Op == wsframe.OpPing {
				return w.w.write(encodeWSFrame(wsframe.OpPong, f.Payload, true))
			}
			return nil
		})
		if err != nil {
			w.err = err
			return
		}
		if op != wsOpText {
			continue
		}
		var m struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Params    json.RawMessage `json:"params"`
			Result    json.RawMessage `json:"result"`
			Error     *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &m) != nil {
			continue
		}
		if m.Method == "" {
			w.mu.Lock()
			ch := w.calls[m.ID]
			delete(w.calls, m.ID)
			w.mu.Unlock()
			if ch != nil {
				r := wardenReply{result: m.Result}
				if m.Error != nil {
					r.err = fmt.Errorf("chrome: %s", m.Error.Message)
				}
				ch <- r
			}
			continue
		}
		w.event(m.Method, m.SessionID, m.Params)
	}
}

func (w *cdpWarden) event(method, sid string, raw json.RawMessage) {
	switch method {
	case "Target.targetCreated", "Target.targetInfoChanged":
		var p struct {
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		w.policy.observe(p.TargetInfo)
		if method == "Target.targetInfoChanged" {
			w.checkTab(p.TargetInfo)
		}
	case "Target.targetDestroyed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(raw, &p) == nil {
			w.policy.forget(p.TargetID)
			w.mu.Lock()
			delete(w.blocked, p.TargetID)
			delete(w.tabSession, p.TargetID)
			w.mu.Unlock()
		}
	case "Target.attachedToTarget":
		var p struct {
			SessionID  string        `json:"sessionId"`
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		w.policy.observe(p.TargetInfo)
		w.mu.Lock()
		tab := p.TargetInfo.TargetID
		if !topLevelTarget(p.TargetInfo.Type) {
			tab = w.sessionTab[sid]
		} else {
			w.tabSession[tab] = p.SessionID
		}
		w.sessionTab[p.SessionID] = tab
		w.mu.Unlock()
		// In order, before the target runs: intercept its documents,
		// attach to its frames the same way, then let it go.
		w.send(p.SessionID, "Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}}})
		w.send(p.SessionID, "Target.setAutoAttach", map[string]any{
			"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
			"filter": []map[string]any{{"type": "iframe"}},
		})
		w.send(p.SessionID, "Runtime.runIfWaitingForDebugger", map[string]any{})
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &p) == nil {
			w.mu.Lock()
			delete(w.sessionTab, p.SessionID)
			w.mu.Unlock()
		}
	case "Fetch.requestPaused":
		var p struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		w.mu.Lock()
		tab := w.sessionTab[sid]
		w.mu.Unlock()
		if w.policy.isLent(tab) && !w.policy.docAllowed(p.Request.URL) {
			w.mu.Lock()
			w.blocked[tab] = p.Request.URL
			w.mu.Unlock()
			w.send(sid, "Fetch.failRequest", map[string]any{"requestId": p.RequestID, "errorReason": "BlockedByClient"})
			w.policy.logNav("", p.Request.URL, true)
			return
		}
		w.send(sid, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID})
	}
}

// checkTab catches a page in an agents' tab that got there without a
// document request the warden saw: one restored from the back-forward
// cache, or a chrome:// page typed into it. It is replaced with a blank
// page.
func (w *cdpWarden) checkTab(ti cdpTargetInfo) {
	if !topLevelTarget(ti.Type) || !w.policy.isLent(ti.TargetID) || w.policy.docAllowed(ti.URL) {
		return
	}
	w.mu.Lock()
	sid := w.tabSession[ti.TargetID]
	wasBlocked := w.blocked[ti.TargetID] == ti.URL
	w.mu.Unlock()
	if wasBlocked || sid == "" {
		return
	}
	w.send(sid, "Page.navigate", map[string]any{"url": "about:blank"})
	w.policy.logNav("", ti.URL, true)
}
