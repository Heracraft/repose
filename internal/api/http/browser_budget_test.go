package httpapi_test

import (
	"bytes"
	"net"
	"net/http"
	"testing"
	"time"

	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/wsframe"
)

// The byte budget and the idle close, which are the api's to hold because
// the api is on the byte path (DECISIONS I-1216).

// floodGuest writes until the api stops reading it: a rooted guest using
// the view as free edge bandwidth.
func floodGuest(c net.Conn) {
	defer func() { _ = c.Close() }()
	buf := bytes.Repeat([]byte("y"), 64<<10)
	for {
		if _, err := c.Write(buf); err != nil {
			return
		}
	}
}

// THE RECONNECT-RESETS-THE-CAP ATTACK. The budget is on the ticket row,
// not in the api's memory, so the bytes a flood spent are still spent
// when it reconnects on the same ticket: the stream closes with
// `budget`, the row carries the spend, and the next connect with that
// ticket is refused before anything is dialled.
func TestStreamBudgetSurvivesAReconnect(t *testing.T) {
	relay := &fakeRelay{serve: floodGuest}
	e := newViewEnv(t, relay, nil)
	tok, pid := newViewProject(t, e, "sub-view-budget", "vbudget")
	ticket := mintTicket(t, e, tok, pid)

	// A budget this test can reach in a second: the row starts with
	// everything but a little already spent, which is exactly the state a
	// reconnect finds.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update browser_view_tickets set bytes = $1 where project_id = $2", int64(httpapi.ViewBudgetBytes-(8<<20)), pid); err != nil {
		t.Fatal(err)
	}
	res := openStream(t, e, ticket, "https://repose.run")
	if res.status != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d %s", res.status, res.code)
	}
	// Read until the api closes the stream on the budget.
	_ = res.conn.SetDeadline(time.Now().Add(30 * time.Second))
	for {
		if _, _, _, err := wsframe.ReadMessage(res.br, 4<<20, func(wsframe.Frame) error { return nil }); err != nil {
			break
		}
	}
	e.waitForLog(t, `"outcome":"budget"`)

	var spent int64
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select bytes from browser_view_tickets where project_id = $1", pid).Scan(&spent); err != nil {
		t.Fatal(err)
	}
	if spent < httpapi.ViewBudgetBytes {
		t.Fatalf("the row carries %d of the %d budget: a reconnect would get a fresh one", spent, int64(httpapi.ViewBudgetBytes))
	}
	// The same ticket again: refused before the relay is dialled.
	before := len(relay.asks())
	if res := openStream(t, e, ticket, "https://repose.run"); res.code != "view_budget" {
		t.Fatalf("the reconnect: %d %s", res.status, res.code)
	}
	if after := len(relay.asks()); after != before {
		t.Fatalf("the refused reconnect still dialled the relay (%d -> %d)", before, after)
	}
}

// A tab left open on a still desktop stops costing frames: no frame in
// either direction for the idle window and the api closes it, which is
// deliberately long before the guest's own 30-minute viewer stop.
func TestStreamClosesWhenIdle(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	relay := &fakeRelay{serve: func(c net.Conn) {
		defer func() { _ = c.Close() }()
		<-stop
	}}
	e := newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		d.ViewRelay = relay
		d.ViewOrigins = []string{"https://repose.run"}
		// A second instead of two minutes, so the test takes a second.
		d.ViewIdle = time.Second
	})
	tok, pid := newViewProject(t, e, "sub-view-idle", "vidle")
	ticket := mintTicket(t, e, tok, pid)
	res := openStream(t, e, ticket, "https://repose.run")
	if res.status != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d %s", res.status, res.code)
	}
	_ = res.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, _, _, err := wsframe.ReadMessage(res.br, 4<<20, func(wsframe.Frame) error { return nil }); err == nil {
		t.Fatal("the idle stream stayed open")
	}
	e.waitForLog(t, `"outcome":"idle"`)
}
