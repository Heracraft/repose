package cli

import (
	"bytes"
	"testing"
	"time"
)

// I-634: the stop line counts what it leaves on the machine, and status
// on the stopped machine shows the rows this laptop read before it.
func TestStopSaysWhatItLeaves(t *testing.T) {
	n := func(i int) *int { return &i }
	rows := []gitRow{
		{Worktree: "todo-app", Branch: "main", NotOnLaptop: n(3), Uncommitted: n(4)},
		{Worktree: "worktree-1", Branch: "worktree-1", NotOnLaptop: n(0), Uncommitted: n(0)},
	}
	line := withLeft("Stopped todo-app in 11s with a 2.1 GB snapshot.", leftClause(rows))
	if line != "Stopped todo-app in 11s with a 2.1 GB snapshot; 3 commits on main not fetched, 4 files not committed." {
		t.Fatalf("line %q", line)
	}
	rows[1].NotOnLaptop = n(2)
	if c := leftClause(rows); c != "5 commits on 2 branches not fetched, 4 files not committed" {
		t.Fatalf("two branches %q", c)
	}
	// Outside the checkout the laptop cannot count commits; clean says nothing.
	if c := leftClause([]gitRow{{Branch: "main", Uncommitted: n(0)}}); c != "" {
		t.Fatalf("clean %q", c)
	}
	if l := withLeft("Stopped a in 3s.", ""); l != "Stopped a in 3s." {
		t.Fatalf("no clause %q", l)
	}

	dir := t.TempDir()
	at := time.Now().Add(-9 * time.Hour)
	saveStopLeft(dir, "id-1", rows[:1], at)
	p := &Project{ID: "id-1", Slug: "todo-app", State: "stopped", Class: "large"}
	l, ok := stoppedRows(dir, p)
	if !ok {
		t.Fatal("no rows for the stopped project")
	}
	var b bytes.Buffer
	writeStatus(&b, statusView{p: p, mux: "tmux", now: time.Now(), left: l, hasLeft: true})
	if !bytes.Contains(b.Bytes(), []byte("\n  checkout   main: 3 commits not on this laptop, 4 files not committed (at the stop, 9h ago)\n")) {
		t.Fatalf("status:\n%s", b.String())
	}
	// Started since: the rows are old news.
	later := time.Now()
	p.StartedAt = &later
	if _, ok := stoppedRows(dir, p); ok {
		t.Fatal("rows shown for a machine that ran after the stop")
	}
	// A stop that could not read the machine forgets the old rows.
	saveStopLeft(dir, "id-1", nil, time.Now())
	p.StartedAt = nil
	if _, ok := stoppedRows(dir, p); ok {
		t.Fatal("rows kept after a stop that read none")
	}
}
