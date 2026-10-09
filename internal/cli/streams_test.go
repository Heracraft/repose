package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// DECISIONS I-609: --since is parsed in the CLI. Durations take d and w,
// a date is local midnight, RFC 3339 passes through; anything else is a
// usage error instead of every line.
func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"90m", now.Add(-90 * time.Minute)},
		{"2h30m", now.Add(-150 * time.Minute)},
		{"2d", now.Add(-48 * time.Hour)},
		{"1w", now.Add(-7 * 24 * time.Hour)},
		{"1d12h", now.Add(-36 * time.Hour)},
		{"2026-10-01", time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)},
		{"2026-10-01T08:00:00Z", time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)},
		{"2026-10-01T08:00:00.5-04:00", time.Date(2026, 10, 1, 12, 0, 0, 500000000, time.UTC)},
	}
	for _, c := range cases {
		got, err := parseSince(c.in, now)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"yesterday", "7days", "-1h", "0s", "2026-13-01", "1d-2h", "d", "2026-10-01 08:00"} {
		if _, err := parseSince(bad, now); err == nil || !strings.Contains(err.Error(), "--since takes") {
			t.Errorf("parseSince(%q) = %v, want the --since refusal", bad, err)
		}
	}
}

// A bad --since is exit 2 before the api is asked anything (no client).
func TestStreamsRefuseBadSince(t *testing.T) {
	e := &Env{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	for name, err := range map[string]error{
		"logs":   LogsCmd(context.Background(), e, "x", "", "yesterday", 0, false, nil),
		"events": EventsCmd(context.Background(), e, "x", "yesterday", false, nil),
	} {
		if _, ok := err.(cobraUsageError); !ok {
			t.Errorf("%s --since yesterday: %v (%T), want a usage error", name, err, err)
		}
	}
	if _, ok := LogsCmd(context.Background(), e, "x", "", "", -1, false, nil).(cobraUsageError); !ok {
		t.Error("logs --tail -1 is not a usage error")
	}
}

// -n/--tail keeps the last N lines; --json is one compact object per
// line, the api's object as sent.
func TestLogsTailAndNDJSON(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &bytes.Buffer{}
	e.Out = out
	if err := LogsCmd(ctx, e, "", "build", "", 2, false, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "built") {
		t.Fatalf("--tail 2 printed:\n%s", out.String())
	}
	out.Reset()
	e.JSON = true
	if err := LogsCmd(ctx, e, "", "build", "", 0, false, nil); err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("--json printed %d lines, want 3 (one per log line):\n%s", len(lines), out.String())
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["seq"] == nil {
			t.Fatalf("not the api's object on one line: %q (%v)", l, err)
		}
	}
}

// --kind ops prints each op's state, duration and error, which the CLI
// dropped against the real api (review 3.5).
func TestLogsOpsLines(t *testing.T) {
	ms := int64(3100)
	ts := time.Date(2026, 10, 8, 21, 40, 0, 0, time.UTC)
	cases := []struct {
		l    LogLine
		want string
	}{
		{LogLine{TS: ts, Kind: "start", OpID: "o1", State: "error", DurationMS: &ms, Error: &OpError{Code: "boot_timeout", Message: "the machine did not boot"}}, "start failed 3.1s boot_timeout: the machine did not boot"},
		{LogLine{TS: ts, Kind: "stop", OpID: "o2", State: "done", DurationMS: &ms}, "stop done 3.1s"},
		{LogLine{TS: ts, Kind: "create", OpID: "o3", State: "running"}, "create running"},
	}
	for _, c := range cases {
		if got := logLineText(c.l, "ops"); got != streamTime(ts)+" "+c.want {
			t.Errorf("got %q, want %q", got, streamTime(ts)+" "+c.want)
		}
	}

	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	if err := StopCmd(context.Background(), e, "", false); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	e.Out = out
	if err := LogsCmd(context.Background(), e, "", "ops", "", 0, false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), " stop done 0.0s\n") {
		t.Fatalf("ops from the fake:\n%s", out.String())
	}
}

// An event row: local time, agent and window, the kind as a verb, the
// summary on one line; the project column only across projects.
func TestEventLine(t *testing.T) {
	ts := time.Date(2026, 10, 9, 0, 59, 0, 0, time.UTC)
	tm := streamTime(ts)
	cases := []struct {
		ev   Event
		w    eventsWidths
		want string
	}{
		{Event{TS: ts, Kind: "completed", Agent: "claude", Window: "2", Summary: "Added tests\nfor billing"}, eventsWidths{agent: 10, verb: 8},
			tm + "  claude (2)  finished  Added tests for billing"},
		{Event{TS: ts, Kind: "guest_state_changed", Summary: "running"}, eventsWidths{agent: 10, verb: 8},
			tm + "  -           machine   running"},
		// The noun is said once (I-631).
		{Event{TS: ts, Kind: "snapshot.created", Summary: "snapshot taken (stop)"}, eventsWidths{agent: 10, verb: 8},
			tm + "  -           snapshot  taken (stop)"},
		{Event{TS: ts, Kind: "project.created", Summary: "project created as large"}, eventsWidths{agent: 1, verb: 7},
			tm + "  -  project  created as large"},
		{Event{TS: ts, Kind: "config.applied", Summary: "revision 3"}, eventsWidths{agent: 1, verb: 14},
			tm + "  -  config applied  revision 3"},
		{Event{TS: ts, Kind: "agent_question", Agent: "codex", Summary: "Drop it?", Project: "api"}, eventsWidths{all: true, project: 8, agent: 5, verb: 4},
			tm + "  api       codex  asks  Drop it?"},
	}
	// The columns fit the longest cell of the batch.
	var fw eventsWidths
	fw.fit([]Event{{Kind: "volume.resized", Summary: "40 GB"}, {Kind: "completed", Agent: "claude", Window: "claude-2"}})
	if fw.verb != len("volume resized") || fw.agent != len("claude (claude-2)") {
		t.Errorf("fit: %+v", fw)
	}
	for _, c := range cases {
		if got := eventLine(c.ev, c.w); got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
	}
}

// Outside a checkout `repose events` covers every project, with a project
// column; an empty answer says so on stderr; --json is NDJSON with the
// project named.
func TestEventsEveryProjectAndEmpty(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	a, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "api", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "web", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	fake.AddEvent(a.ID, now.Add(-2*time.Minute), "completed", "a-done")
	fake.AddEvent(b.ID, now.Add(-time.Minute), "agent_message", "b-said")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	e.Out, e.ErrOut = out, errOut
	if err := EventsCmd(ctx, e, "", "1h", false, nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	ia, ib := strings.Index(s, "a-done"), strings.Index(s, "b-said")
	if ia < 0 || ib < ia || !strings.Contains(s, "  api  ") || !strings.Contains(s, "  web  ") {
		t.Fatalf("every project, oldest first, with a project column:\n%s", s)
	}

	out.Reset()
	e.JSON = true
	if err := EventsCmd(ctx, e, "web", "1h", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var ev Event
		if err := json.Unmarshal([]byte(l), &ev); err != nil || ev.Project != "web" || ev.TS.Location() != time.UTC {
			t.Fatalf("--json line %q: %+v %v", l, ev, err)
		}
	}

	out.Reset()
	e.JSON = false
	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if err := EventsCmd(ctx, e, "web", later, false, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || strings.TrimSpace(errOut.String()) != "No events on web since "+later+"." {
		t.Fatalf("empty: stdout %q stderr %q", out.String(), errOut.String())
	}
	if got := emptyEventsLine([]Project{{Slug: "web"}}, false, "1h"); got != "No events on web in the last 1h." {
		t.Fatalf("%q", got)
	}
	if got := emptyEventsLine([]Project{{Slug: "web"}}, true, "24h"); got != "No events on any of your projects in the last 24h." {
		t.Fatalf("%q", got)
	}
}

// Off a terminal a phase prints its start, a heartbeat while it runs and
// its ✓ line when it ends (review 3.7).
func TestProgressOffTerminal(t *testing.T) {
	var buf safeBuffer
	p := newProgress(&buf, false)
	p.heartbeat = 20 * time.Millisecond
	p.Phase("Booting todo-app", "Booted todo-app")
	time.Sleep(70 * time.Millisecond)
	p.End()
	p.Phase("Syncing", "")
	p.End()
	got := buf.String()
	if !strings.HasPrefix(got, "Booting todo-app...\nBooting todo-app... ") {
		t.Fatalf("no heartbeat:\n%s", got)
	}
	if !strings.Contains(got, "✓ Booted todo-app  ") || !strings.HasSuffix(got, "Syncing...\n") {
		t.Fatalf("end lines:\n%s", got)
	}
}

// A phase resumed after a line printed inside it (run's Worktree: line)
// prints its start once off a terminal (I-633).
func TestProgressResumeOffTerminal(t *testing.T) {
	var buf safeBuffer
	p := newProgress(&buf, false)
	p.heartbeat = 0
	p.Phase("Starting claude", "")
	p.End()
	_, _ = buf.Write([]byte("Worktree: ~/todo-app-worktree-1 on branch worktree-1\n"))
	p.Resume("Starting claude", "")
	p.End()
	if got := buf.String(); got != "Starting claude...\nWorktree: ~/todo-app-worktree-1 on branch worktree-1\n" {
		t.Fatalf("%q", got)
	}
}

// questions -q prints the ids; --json carries the terminal waits as kind
// "terminal" beside the questions (kind "question").
func TestQuestionsQuietAndTerminalJSON(t *testing.T) {
	// The Question array as before I-609, so `jq -r '.[].id'` gives ids
	// to `reply --question` (I-631).
	qs := []Question{{ID: "q-1", Project: "api", Agent: "claude", Text: "Drop it?"}}
	b, err := json.Marshal(questionsJSON(qs))
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["id"] != "q-1" || got[0]["kind"] != nil {
		t.Fatalf("%s", b)
	}
	if b, _ := json.Marshal(questionsJSON(nil)); string(b) != "[]" {
		t.Fatalf("none: %s", b)
	}
}

// start, stop and snapshots create under --json print the object on
// stdout and nothing else there.
func TestStateChangesPrintJSON(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	e.JSON = true
	out := &bytes.Buffer{}
	e.Out = out
	if err := withProjectJSON(ctx, e, "", func() error { return StopCmd(ctx, e, "", true) }, nil); err != nil {
		t.Fatal(err)
	}
	var got Project
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.ID != p.ID || got.State != "stopped" {
		t.Fatalf("stop --json: %q %v", out.String(), err)
	}
	out.Reset()
	if err := withProjectJSON(ctx, e, "", func() error { return StartCmd(ctx, e, "") }, nil); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.State != "running" {
		t.Fatalf("start --json: %q %v", out.String(), err)
	}
	if e.Out != out {
		t.Fatal("withProjectJSON did not give stdout back")
	}
	out.Reset()
	if err := SnapshotsCreateCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err := json.Unmarshal(out.Bytes(), &s); err != nil || s.ID == "" {
		t.Fatalf("snapshots create --json: %q %v", out.String(), err)
	}
}

// The flags the review found missing: status -f (--watch hidden),
// secrets list --json, snapshots create --json, questions -q, logs -n,
// and --json on start, stop, resize and sync.
func TestStreamFlags(t *testing.T) {
	root := newRootCmd("test")
	for _, c := range []struct{ path, flag string }{
		{"status", "f"}, {"logs", "n"}, {"questions", "q"},
	} {
		cmd, _, err := root.Find(strings.Fields(c.path))
		if err != nil || cmd.Flags().ShorthandLookup(c.flag) == nil {
			t.Errorf("%s has no -%s", c.path, c.flag)
		}
	}
	for _, path := range []string{"secrets list", "snapshots create", "start", "stop", "resize", "sync"} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil || cmd.Flags().Lookup("json") == nil {
			t.Errorf("%s has no --json", path)
		}
	}
	st, _, _ := root.Find([]string{"status"})
	if f := st.Flags().Lookup("watch"); f == nil || !f.Hidden {
		t.Error("status --watch is gone or still shown")
	}
}

// safeBuffer is a bytes.Buffer the heartbeat goroutine can write to while
// the test reads it.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// elapsedRE matches fmtElapsed's output at the end of a ✓ line, which
// a loaded machine can push past 0.0s.
var elapsedRE = regexp.MustCompile(`  [0-9m.]+s\n`)
