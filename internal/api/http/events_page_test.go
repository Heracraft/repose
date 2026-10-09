package httpapi_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// DECISIONS I-414: GET /projects/:id/events pages back with limit and
// before; with neither it is the newest 50, as before.
func TestEventsPageBack(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-events", "eventer")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "busy", "large")
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	for i := 0; i < 120; i++ {
		// Two events share each second, so the page boundary has to break
		// ties by id.
		ts := base.Add(time.Duration(i/2) * time.Second)
		if _, err := e.h.Pool.Exec(ctx, `insert into events (id, project_id, ts, ts_second, kind, summary, source)
			values ($1, $2, $3, $4, 'agent_message', $5, 'api')`, uuid.New(), p.ID, ts, ts.Unix(), fmt.Sprintf("m%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	path := "/projects/" + p.ID.String() + "/events"
	if r := e.do(t, tok, "GET", path, nil); r.status != 200 || len(r.list) != 50 {
		t.Fatalf("default page: %d, %d events", r.status, len(r.list))
	}
	seen := map[string]bool{}
	var before string
	pages := 0
	for {
		q := path + "?limit=25"
		if before != "" {
			q += "&before=" + before
		}
		r := e.do(t, tok, "GET", q, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d %s", q, r.status, r.raw)
		}
		if len(r.list) == 0 {
			break
		}
		pages++
		var lastTS string
		for _, it := range r.list {
			m := it.(map[string]any)
			s := m["summary"].(string)
			if seen[s] {
				t.Fatalf("%s came twice", s)
			}
			seen[s] = true
			if ts := m["ts"].(string); lastTS != "" && ts > lastTS {
				t.Fatalf("not newest first: %s after %s", ts, lastTS)
			}
			lastTS = m["ts"].(string)
			before = m["id"].(string)
		}
	}
	if len(seen) != 120 || pages != 5 {
		t.Fatalf("paged %d events in %d pages, want 120 in 5", len(seen), pages)
	}
	for _, q := range []string{"?limit=0", "?limit=201", "?limit=x", "?before=nope"} {
		if r := e.do(t, tok, "GET", path+q, nil); r.status != 400 {
			t.Errorf("%s: %d, want 400", q, r.status)
		}
	}
	// Another project's event id pages nothing.
	other := e.h.NewProject(u, "quiet", "large")
	if r := e.do(t, tok, "GET", "/projects/"+other.ID.String()+"/events?before="+before, nil); r.status != 200 || len(r.list) != 0 {
		t.Fatalf("foreign before: %d %d", r.status, len(r.list))
	}
}

// DECISIONS I-609: since= on events and logs is RFC 3339 or absent; any
// other value is 400 invalid, where it used to mean no limit.
func TestEventsSinceInvalid(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-since", "sincer")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(e.h.Ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "since", "small")
	base := "/projects/" + p.ID.String()
	for _, q := range []string{"/events?since=7d", "/events?since=yesterday", "/logs?since=2026-10-01", "/logs?kind=ops&since=1h"} {
		if r := e.do(t, tok, "GET", base+q, nil); r.status != 400 || !strings.Contains(string(r.raw), "invalid") {
			t.Errorf("%s: %d %s, want 400 invalid", q, r.status, r.raw)
		}
	}
	for _, q := range []string{"/events?since=2026-10-01T00:00:00Z", "/events?since=2026-10-01T00:00:00.123456789-04:00", "/logs?kind=ops&since=2026-10-01T00:00:00Z", "/events"} {
		if r := e.do(t, tok, "GET", base+q, nil); r.status != 200 {
			t.Errorf("%s: %d %s, want 200", q, r.status, r.raw)
		}
	}
}
