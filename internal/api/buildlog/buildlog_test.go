package buildlog_test

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/buildlog"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

// A build line that echoes a current secret value is stored with the
// value replaced, and subscribers see the redacted line too.
func TestRedactionBatchingAndSubscribe(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid, opID := store.NewID(), store.NewID(), store.NewID()
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'bl')", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'p', 'p', 'small', 'running', 1)", pid, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into ops (id, project_id, kind, state) values ($1, $2, 'build', 'running')", opID, pid); err != nil {
		t.Fatal(err)
	}
	s := buildlog.New(pool, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	s.SetRedactions(opID, []string{"sk-live-PLANTED", "x"}) // short values are never redacted (too many false hits)
	ch, cancel := s.Subscribe(opID)
	defer cancel()
	for i := 1; i <= 120; i++ {
		line := "building"
		if i == 7 {
			line = "export API_KEY=sk-live-PLANTED # leaked by a build hook"
		}
		s.Append(opID, int64(i), line)
	}
	s.Flush(ctx)
	lines, err := s.Read(ctx, opID, 0, 0)
	if err != nil || len(lines) != 120 {
		t.Fatalf("read %d %v", len(lines), err)
	}
	if lines[6].Line != "export API_KEY=[redacted] # leaked by a build hook" {
		t.Fatalf("line 7 stored as %q", lines[6].Line)
	}
	var stored string
	if err := pool.QueryRow(ctx, "select line from build_logs where op_id = $1 and seq = 7", opID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "PLANTED") {
		t.Fatal("secret value reached the table")
	}
	got := 0
	timeout := time.After(2 * time.Second)
	for got < 120 {
		select {
		case l := <-ch:
			got++
			if strings.Contains(l.Line, "PLANTED") {
				t.Fatal("secret value reached a subscriber")
			}
		case <-timeout:
			t.Fatalf("subscriber saw %d of 120 lines", got)
		}
	}
	// A partial tail read.
	tail, _ := s.Read(ctx, opID, 118, 0)
	if len(tail) != 2 || tail[0].Seq != 119 {
		t.Fatalf("tail %+v", tail)
	}
	s.ClearRedactions(opID)
	if n, err := s.Trim(ctx, 20); err != nil || n != 0 {
		t.Fatalf("trim %d %v", n, err)
	}
}

// A reader that arrives while a flush is in flight must see the batch that
// flush is inserting (DECISIONS I-117). Appends and flushes race a reader
// that expects every line appended before it started.
func TestReadWaitsForTheFlushInFlight(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid, opID := store.NewID(), store.NewID(), store.NewID()
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'bl2')", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'p2', 'p2', 'small', 'running', 1)", pid, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into ops (id, project_id, kind, state) values ($1, $2, 'build', 'running')", opID, pid); err != nil {
		t.Fatal(err)
	}
	s := buildlog.New(pool, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	for round := int64(1); round <= 40; round++ {
		s.Append(opID, round, "line")
		done := make(chan struct{})
		go func() { s.Flush(ctx); close(done) }()
		lines, err := s.Read(ctx, opID, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(lines)) < round {
			t.Fatalf("round %d: read %d lines, want at least %d", round, len(lines), round)
		}
		<-done
	}
}

// A multi-line value (a PEM key) never appears whole on one log line, so
// each of its lines is redacted on its own, as is its base64 form; the PEM
// armour stays readable and values under MinRedact are left alone. Redact
// applies the same set to the error a failed build carries.
func TestRedactionOfMultiLineAndEncodedValues(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ\r\nAAAAMwAAAAtzc2gtZWQyNTUxOQAAACDPLANTED\n-----END OPENSSH PRIVATE KEY-----\n"
	s := buildlog.New(nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	opID := store.NewID()
	s.SetRedactions(opID, []string{key, "sk-live-PLANTED", "abc"})
	cases := map[string]string{
		"key: b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ":                     "key: [redacted]",
		"  AAAAMwAAAAtzc2gtZWQyNTUxOQAAACDPLANTED":                            "  [redacted]",
		"-----BEGIN OPENSSH PRIVATE KEY-----":                                 "-----BEGIN OPENSSH PRIVATE KEY-----",
		"b64 " + base64.StdEncoding.EncodeToString([]byte("sk-live-PLANTED")): "b64 [redacted]",
		"url " + base64.URLEncoding.EncodeToString([]byte(key)):               "url [redacted]",
		"abcdef": "abcdef",
	}
	for in, want := range cases {
		if got := s.Redact(opID, in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	if got := s.Redact(opID, "failed:\n"+key); strings.Contains(got, "PLANTED") || strings.Contains(got, "b3BlbnNzaC1rZXkt") {
		t.Fatalf("whole key in a message: %q", got)
	}
	// Longest first: the whole value goes before any line of it, so the
	// result is one marker, not a value broken around markers.
	if got := s.Redact(opID, key); got != "[redacted]" {
		t.Fatalf("whole key = %q", got)
	}
	s.ClearRedactions(opID)
	if got := s.Redact(opID, "sk-live-PLANTED"); got != "sk-live-PLANTED" {
		t.Fatalf("after clear: %q", got)
	}
}
