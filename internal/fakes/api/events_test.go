package api

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// TestFakeEventsPages: the fake pages events as api.md says (I-414,
// I-662): before and after cursors ordered by (ts, id), a kind filter,
// the Repose-Event-Count header, and 400 for both cursors or a bad kind.
func TestFakeEventsPages(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p, err := f.CreateProject("pager", "large")
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/projects/" + p.ID + "/events"
	var start []Event
	call(t, f, "GET", base+"?limit=200", tok, nil).json(t, &start)
	created := len(start)
	// 60 events, two to a second so the cursor has to break ties by id;
	// every fifth is an error.
	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for i := 0; i < 60; i++ {
		kind := "agent_message"
		if i%5 == 0 {
			kind = "error"
		}
		f.AddEventAs(p.ID, t0.Add(time.Duration(i/2)*time.Second), kind, "claude", "claude", fmt.Sprintf("m%03d", i))
	}
	total := 60 + created

	page := func(q string) ([]Event, int) {
		t.Helper()
		r := call(t, f, "GET", base+q, tok, nil)
		want(t, r, 200)
		var evs []Event
		r.json(t, &evs)
		n, err := strconv.Atoi(r.header.Get("Repose-Event-Count"))
		if err != nil {
			t.Fatalf("%s: count header %q", q, r.header.Get("Repose-Event-Count"))
		}
		return evs, n
	}

	// Older all the way down, then newer all the way back up: the same
	// pages in the same order.
	var pages [][]Event
	before := ""
	for {
		q := "?limit=25"
		if before != "" {
			q += "&before=" + before
		}
		evs, n := page(q)
		if n != total {
			t.Fatalf("%s: count %d, want %d", q, n, total)
		}
		if len(evs) == 0 {
			break
		}
		for i := 1; i < len(evs); i++ {
			a, b := evs[i-1], evs[i]
			if a.TS.Before(b.TS) || (a.TS.Equal(b.TS) && a.ID < b.ID) {
				t.Fatalf("%s: not newest first at %d", q, i)
			}
		}
		pages = append(pages, evs)
		before = evs[len(evs)-1].ID
	}
	if seen := 25*(len(pages)-1) + len(pages[len(pages)-1]); seen != total {
		t.Fatalf("paged %d events, want %d", seen, total)
	}
	for i := len(pages) - 1; i > 0; i-- {
		evs, _ := page("?limit=25&after=" + pages[i][0].ID)
		if len(evs) != 25 || evs[0].ID != pages[i-1][0].ID || evs[24].ID != pages[i-1][24].ID {
			t.Fatalf("after page %d: %d events, %v", i, len(evs), evs)
		}
	}
	// after the newest event, or an unknown one, is empty.
	if evs, _ := page("?after=" + pages[0][0].ID); len(evs) != 0 {
		t.Fatalf("after the newest: %d events", len(evs))
	}
	if evs, _ := page("?after=01900000-0000-7000-8000-999999999999"); len(evs) != 0 {
		t.Fatalf("unknown after: %d events", len(evs))
	}
	// before ignores since.
	if evs, _ := page("?limit=5&since=" + time.Now().UTC().Format(time.RFC3339) + "&before=" + pages[0][0].ID); len(evs) != 5 {
		t.Fatalf("before with since: %d events", len(evs))
	}

	// kind keeps only those kinds, and the count follows it.
	evs, n := page("?kind=error&limit=200")
	if n != 12 || len(evs) != 12 {
		t.Fatalf("kind=error: %d events, count %d, want 12", len(evs), n)
	}
	for _, ev := range evs {
		if ev.Kind != "error" || ev.Agent != "claude" || ev.Window != "claude" {
			t.Fatalf("kind=error gave %+v", ev)
		}
	}
	if evs, n := page("?kind=error,needs_input&limit=5&before=" + evs[4].ID); n != 12 || len(evs) != 5 {
		t.Fatalf("kind with before: %d events, count %d", len(evs), n)
	}
	if evs, n := page("?kind=nothing_here"); n != 0 || len(evs) != 0 {
		t.Fatalf("unknown kind: %d events, count %d", len(evs), n)
	}

	for _, q := range []string{"?before=" + pages[1][0].ID + "&after=" + pages[0][0].ID, "?kind=Error", "?kind=error,", "?kind=a-b", "?limit=0", "?limit=201"} {
		if r := call(t, f, "GET", base+q, tok, nil); r.status != 400 || r.errCode(t) != "invalid" {
			t.Errorf("%s: %d %s, want 400 invalid", q, r.status, r.body)
		}
	}
}

// TestFakeBackdateCreation: events seeded into the past still come after
// the project's creation, which stays its oldest event.
func TestFakeBackdateCreation(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p, err := f.CreateProject("old", "small")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	f.BackdateCreation(p.ID, at)
	f.AddEventAs(p.ID, at.Add(time.Minute), "completed", "codex", "codex", "done")
	var evs []Event
	call(t, f, "GET", "/v1/projects/"+p.ID+"/events", tok, nil).json(t, &evs)
	if last := evs[len(evs)-1]; last.Kind != "project.created" || !last.TS.Equal(at) {
		t.Fatalf("oldest event %+v, want project.created at %s", last, at)
	}
	var got Project
	call(t, f, "GET", "/v1/projects/"+p.ID, tok, nil).json(t, &got)
	if !got.CreatedAt.Equal(at) {
		t.Fatalf("created_at %s, want %s", got.CreatedAt, at)
	}
}
