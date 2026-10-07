package mcpshim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// LatestProtocol is the MCP revision hold asks a laptop server for when it
// fills the cache.
const LatestProtocol = "2025-06-18"

// msg is a JSON-RPC message, kept raw where the shim only passes it on.
type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func (m msg) hasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

// request: a method and an id. A notification has a method and no id; a
// response has an id and no method.
func (m msg) request() bool  { return m.Method != "" && m.hasID() }
func (m msg) response() bool { return m.Method == "" && m.hasID() }

func parse(line []byte) (msg, bool) {
	var m msg
	if err := json.Unmarshal(line, &m); err != nil {
		return msg{}, false
	}
	return m, true
}

func encode(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // ends with the newline the stdio transport wants
	return buf.Bytes()
}

func result(id json.RawMessage, res any) []byte {
	return encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
}

func rpcError(id json.RawMessage, code int, message string) []byte {
	return encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func request(id string, method string, params any) []byte {
	m := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	return encode(m)
}

func notification(method string) []byte {
	return encode(map[string]any{"jsonrpc": "2.0", "method": method})
}

// PingMethod is the shim's liveness probe. The laptop end answers it and
// never passes it to the server.
const PingMethod = "$/repose/ping"

// busyMethod is what hold writes to a shim it turns away at the cap.
const busyMethod = "$/repose/busy"

// IsPing reports whether a line from the machine is the shim's ping, and
// returns its id.
func IsPing(line []byte) (json.RawMessage, bool) {
	if !bytes.Contains(line, []byte(PingMethod)) {
		return nil, false
	}
	m, ok := parse(line)
	if !ok || m.Method != PingMethod || !m.hasID() {
		return nil, false
	}
	return m.ID, true
}

// Pong is the laptop end's answer to a ping.
func Pong(id json.RawMessage) []byte { return result(id, map[string]any{}) }

// lineReader reads newline-ended lines, the MCP stdio framing.
type lineReader struct{ r *bufio.Reader }

func newLineReader(r io.Reader) *lineReader { return &lineReader{bufio.NewReaderSize(r, 64<<10)} }

// next returns the next non-empty line without its newline.
func (l *lineReader) next() ([]byte, error) {
	for {
		b, err := l.r.ReadBytes('\n')
		if t := bytes.TrimSpace(b); len(t) > 0 {
			return t, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// sameJSON compares two lists of JSON values by their decoded form.
func sameJSON(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	norm := func(r json.RawMessage) string {
		var v any
		if json.Unmarshal(r, &v) != nil {
			return string(r)
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	for i := range a {
		if norm(a[i]) != norm(b[i]) {
			return false
		}
	}
	return true
}
