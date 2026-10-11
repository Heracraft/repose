package httpapi_test

import (
	"fmt"
	"strconv"
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

// DECISIONS I-662: after= pages forward, kind= filters, and every answer
// carries Repose-Event-Count; before and after together, or a kind that
// is not one, is 400 invalid.
func TestEventsAfterKindCount(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-pager", "pager")
	me := e.do(t, tok, "GET", "/me", nil)
	u, err := store.GetUser(ctx, e.h.Pool, uuid.MustParse(me.body["id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.NewProject(u, "pager", "large")
	path := "/projects/" + p.ID.String() + "/events"
	created := len(e.do(t, tok, "GET", path+"?limit=200", nil).list)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	for i := 0; i < 60; i++ {
		kind := "agent_message"
		if i%5 == 0 {
			kind = "error"
		}
		// Two to a second: the cursors break ties by id.
		ts := base.Add(time.Duration(i/2) * time.Second)
		if _, err := e.h.Pool.Exec(ctx, `insert into events (id, project_id, ts, ts_second, kind, summary, source)
			values ($1, $2, $3, $4, $5, $6, 'api')`, uuid.New(), p.ID, ts, ts.Unix(), kind, fmt.Sprintf("m%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	total := 60 + created
	ids := func(r resp) []string {
		var out []string
		for _, it := range r.list {
			out = append(out, it.(map[string]any)["id"].(string))
		}
		return out
	}
	get := func(q string, wantCount int) resp {
		t.Helper()
		r := e.do(t, tok, "GET", path+q, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d %s", q, r.status, r.raw)
		}
		if got := r.hdr.Get("Repose-Event-Count"); got != strconv.Itoa(wantCount) {
			t.Fatalf("%s: Repose-Event-Count %q, want %d", q, got, wantCount)
		}
		return r
	}
	if got := e.do(t, tok, "GET", path, nil).hdr.Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Repose-Event-Count") {
		t.Fatalf("Access-Control-Expose-Headers %q", got)
	}

	// Older down, then newer back up: the same pages.
	p1 := ids(get("?limit=25", total))
	p2 := ids(get("?limit=25&before="+p1[24], total))
	p3 := ids(get("?limit=25&before="+p2[24], total))
	if back := ids(get("?limit=25&after="+p3[0], total)); strings.Join(back, ",") != strings.Join(p2, ",") {
		t.Fatalf("after p3 gave %v, want p2 %v", back, p2)
	}
	if back := ids(get("?limit=25&after="+p2[0], total)); strings.Join(back, ",") != strings.Join(p1, ",") {
		t.Fatalf("after p2 gave %v, want p1", back)
	}
	// Short of a page: the nearest ones, newest first.
	if back := ids(get("?limit=25&after="+p1[3], total)); strings.Join(back, ",") != strings.Join(p1[:3], ",") {
		t.Fatalf("after p1[3] gave %v, want %v", back, p1[:3])
	}
	// after ignores since, and an unknown id gives [].
	if back := get("?limit=5&since="+time.Now().UTC().Format(time.RFC3339)+"&after="+p2[0], total); len(back.list) != 5 {
		t.Fatalf("after with since: %d", len(back.list))
	}
	if r := get("?after="+uuid.NewString(), total); len(r.list) != 0 {
		t.Fatalf("unknown after: %d events", len(r.list))
	}

	// kind keeps only those kinds; the count follows it.
	errs := get("?kind=error&limit=200", 12)
	if len(errs.list) != 12 {
		t.Fatalf("kind=error: %d events", len(errs.list))
	}
	for _, it := range errs.list {
		if k := it.(map[string]any)["kind"]; k != "error" {
			t.Fatalf("kind=error gave %v", k)
		}
	}
	eids := ids(errs)
	if r := get("?kind=error,needs_input&limit=4&before="+eids[3], 12); len(r.list) != 4 || ids(r)[0] != eids[4] {
		t.Fatalf("kind with before: %v", ids(r))
	}
	if r := get("?kind=error&limit=4&after="+eids[8], 12); strings.Join(ids(r), ",") != strings.Join(eids[4:8], ",") {
		t.Fatalf("kind with after: %v, want %v", ids(r), eids[4:8])
	}
	get("?kind=nothing_here", 0)

	for _, q := range []string{"?before=" + p2[0] + "&after=" + p1[0], "?after=nope", "?kind=Error", "?kind=error,", "?kind=a-b", "?kind=" + strings.Repeat("a", 65)} {
		if r := e.do(t, tok, "GET", path+q, nil); r.status != 400 || !strings.Contains(string(r.raw), "invalid") {
			t.Errorf("%s: %d %s, want 400 invalid", q, r.status, r.raw)
		}
	}
}
