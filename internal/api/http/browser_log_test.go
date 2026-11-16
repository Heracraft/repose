package httpapi_test

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/wsframe"
)

// The browser view's two log lines, in I-1118's shape: plant a distinct
// marker in the ticket and in the stream, run a view and a refusal, and
// grep the captured logs for each marker -- 0 hits. The probe is the
// other half, because an empty log would pass that too: the same logs
// must carry browser_view_open, browser_view_close, an outcome, a
// bytes_out and a source_prefix, and the ticket's row id.
func TestBrowserViewLogsCarryIdsAndNoTenantBytes(t *testing.T) {
	const frameMarker = "FRAMEBUFFER-MARKER-7d3a"
	const clipMarker = "CLIPBOARD-MARKER-91fe"
	relay := &fakeRelay{serve: func(c net.Conn) {
		defer func() { _ = c.Close() }()
		// What a compromised guest sends: a framebuffer rectangle and a
		// ServerCutText, both of which are the contents of a tenant's
		// screen and must never reach a log line.
		if _, err := c.Write([]byte(frameMarker + clipMarker)); err != nil {
			return
		}
		buf := make([]byte, 64)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}}
	e := newViewEnv(t, relay, nil)
	tok, pid := newViewProject(t, e, "sub-view-log", "vlog")
	ticket := mintTicket(t, e, tok, pid)

	res := openStream(t, e, ticket, "https://repose.run")
	if res.status != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d %s", res.status, res.code)
	}
	if _, payload := readFrame(t, res.br); !strings.Contains(string(payload), frameMarker) {
		t.Fatalf("the first frame was %q", payload)
	}
	// What a page sends: a key event, and a cut-text the dashboard does
	// not bridge but a hand-written client could.
	if _, err := res.conn.Write(wsframe.Encode(wsframe.OpBinary, []byte(clipMarker), true)); err != nil {
		t.Fatal(err)
	}
	_ = res.conn.Close()
	e.waitForLog(t, `"event":"browser_view_close"`)

	// One refused ticket as well, so the refusal path is in the logs too.
	if r := openStream(t, e, "a-ticket-nobody-minted", "https://repose.run"); r.code != "view_expired" {
		t.Fatalf("the refusal: %d %s", r.status, r.code)
	}
	// And let the close line's write finish.
	time.Sleep(200 * time.Millisecond)
	logs := e.logText()

	for _, marker := range []string{frameMarker, clipMarker, ticket, "a-ticket-nobody-minted"} {
		if strings.Contains(logs, marker) {
			t.Errorf("the logs carry %q", marker)
		}
	}
	for _, want := range []string{
		`"event":"browser_view_open"`,
		`"event":"browser_view_close"`,
		`"outcome":"closed"`,
		`"bytes_out":`,
		`"source_prefix":"`,
		`"ticket_id":"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the logs carry no %s", want)
		}
	}
	// ticket_id is the row's opaque id, which the redaction list must let
	// through: the whole point of the exact-name allow list.
	if strings.Contains(logs, `"ticket_id":"[redacted]"`) {
		t.Error("ticket_id came out redacted")
	}
	// The source address itself is never logged, only the prefix.
	if strings.Contains(logs, `"source_prefix":"127.0.0.1"`) {
		t.Error("the source address was logged instead of its prefix")
	}
}
