package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

type recorder struct {
	mu    sync.Mutex
	sent  []notify.Message
	fails int
	perm  bool
}

func (r *recorder) Send(ctx context.Context, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fails > 0 {
		r.fails--
		if r.perm {
			return notify.Permanent{Err: errors.New("404")}
		}
		return errors.New("boom")
	}
	r.sent = append(r.sent, m)
	return nil
}

func seed(t *testing.T, pool *db.Pool, ntfy string) (userID, projectID string) {
	t.Helper()
	ctx := context.Background()
	uid := store.NewID()
	pid := store.NewID()
	var n *string
	if ntfy != "" {
		n = &ntfy
	}
	if _, err := pool.Exec(ctx, "insert into users (id, handle, email, ntfy_url) values ($1, $2, $3, $4)", uid, "u"+uid.String()[24:], "u@example.com", n); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'todo', 'todo', 'large', 'running', 1)", pid, uid); err != nil {
		t.Fatal(err)
	}
	return uid.String(), pid.String()
}

func TestOutboxDeliversOncePerChannelAndRetries(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := metrics.NewNop()
	_, pid := seed(t, pool, "https://ntfy.example/topic")
	ing := events.New(pool, m, log)
	email := &recorder{fails: 2}
	ntfy := &recorder{}
	now := time.Now()
	ob := notify.New(pool, map[string]notify.Sender{"email": email, "ntfy": ntfy}, m, log)
	ob.Now = func() time.Time { return now }
	id, inserted, err := ing.Insert(ctx, events.Incoming{ProjectID: mustUUID(pid), TS: now, Kind: "completed", Agent: "claude", Summary: "done"})
	if err != nil || !inserted {
		t.Fatalf("insert %v %v", inserted, err)
	}
	var rows int
	_ = pool.QueryRow(ctx, "select count(*) from events_outbox where event_id = $1", id).Scan(&rows)
	if rows != 2 {
		t.Fatalf("outbox rows %d", rows)
	}
	now = time.Now().Add(time.Second)
	if n, err := ob.Once(ctx); err != nil || n != 2 {
		t.Fatalf("first pass %d %v", n, err)
	}
	if len(ntfy.sent) != 1 || len(email.sent) != 0 {
		t.Fatalf("after first pass: ntfy=%d email=%d", len(ntfy.sent), len(email.sent))
	}
	var attempts int
	var nextAt time.Time
	if err := pool.QueryRow(ctx, "select attempts, next_at from events_outbox where event_id = $1 and channel = 'email'", id).Scan(&attempts, &nextAt); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || nextAt.Sub(now) < 9*time.Second || nextAt.Sub(now) > 11*time.Second {
		t.Fatalf("retry schedule: attempts=%d next in %v", attempts, nextAt.Sub(now))
	}
	// Not due yet: nothing happens.
	if n, _ := ob.Once(ctx); n != 0 {
		t.Fatalf("picked an undue row: %d", n)
	}
	now = now.Add(11 * time.Second)
	if n, _ := ob.Once(ctx); n != 1 {
		t.Fatalf("second attempt picked %d", n)
	}
	now = now.Add(2 * time.Minute)
	if n, _ := ob.Once(ctx); n != 1 || len(email.sent) != 1 {
		t.Fatalf("third attempt: picked %d sent %d", n, len(email.sent))
	}
	var delivered map[string]any
	if err := pool.QueryRow(ctx, "select delivered from events where id = $1", id).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if _, ok := delivered["email"]; !ok {
		t.Fatalf("delivered %v", delivered)
	}
	if _, ok := delivered["ntfy"]; !ok {
		t.Fatalf("delivered %v", delivered)
	}
	_ = pool.QueryRow(ctx, "select count(*) from events_outbox").Scan(&rows)
	if rows != 0 {
		t.Fatalf("outbox not drained: %d", rows)
	}
	if n, _ := ob.Once(ctx); n != 0 || len(ntfy.sent) != 1 || len(email.sent) != 1 {
		t.Fatal("delivered twice")
	}
	if email.sent[0].Summary != "done" || email.sent[0].Project != "todo" || email.sent[0].Email != "u@example.com" {
		t.Fatalf("message %+v", email.sent[0])
	}
}

func TestPermanentFailureIsNotRetried(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := metrics.NewNop()
	_, pid := seed(t, pool, "https://ntfy.example/topic")
	ing := events.New(pool, m, log)
	ntfy := &recorder{fails: 1, perm: true}
	ob := notify.New(pool, map[string]notify.Sender{"ntfy": ntfy, "email": &recorder{}}, m, log)
	id, _, err := ing.Insert(ctx, events.Incoming{ProjectID: mustUUID(pid), TS: time.Now(), Kind: "needs_input", Agent: "codex", Summary: "?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ob.Once(ctx); err != nil {
		t.Fatal(err)
	}
	var delivered map[string]any
	_ = pool.QueryRow(ctx, "select delivered from events where id = $1", id).Scan(&delivered)
	s, _ := delivered["ntfy"].(string)
	if len(s) < 7 || s[:7] != "failed:" {
		t.Fatalf("ntfy delivered value %q", s)
	}
	var rows int
	_ = pool.QueryRow(ctx, "select count(*) from events_outbox where channel = 'ntfy'").Scan(&rows)
	if rows != 0 {
		t.Fatal("permanent failure left a retry row")
	}
}

func TestNtfyAndEmailSenders(t *testing.T) {
	var gotTitle, gotPrio, gotTags, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/topic":
			gotTitle, gotPrio, gotTags = r.Header.Get("Title"), r.Header.Get("Priority"), r.Header.Get("Tags")
			w.WriteHeader(200)
		case "/emails":
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
		case "/bad":
			w.WriteHeader(404)
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	n := &notify.Ntfy{HTTP: srv.Client()} // loopback test server; the default client refuses it
	if err := n.Send(context.Background(), notify.Message{Kind: "needs_input", Agent: "claude", Project: "todo", Summary: "?", NtfyURL: srv.URL + "/topic", Dashboard: "https://d"}); err != nil {
		t.Fatal(err)
	}
	if gotTitle != "todo: claude needs input" || gotPrio != "5" || gotTags != "question" {
		t.Fatalf("headers %q %q %q", gotTitle, gotPrio, gotTags)
	}
	err := n.Send(context.Background(), notify.Message{Kind: "completed", NtfyURL: srv.URL + "/bad"})
	var perm notify.Permanent
	if !errors.As(err, &perm) {
		t.Fatalf("4xx should be permanent: %v", err)
	}
	if err := n.Send(context.Background(), notify.Message{Kind: "completed", NtfyURL: srv.URL + "/down"}); err == nil || errors.As(err, &perm) {
		t.Fatalf("5xx should be retried: %v", err)
	}
	e := &notify.Email{APIKey: "re_test", URL: srv.URL + "/emails"}
	if err := e.Send(context.Background(), notify.Message{Kind: "completed", Agent: "claude", Project: "todo", Summary: "ok", Email: "u@example.com"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer re_test" {
		t.Fatalf("auth %q", gotAuth)
	}
}

// TestEmailTemplate is the golden test docs/workstreams/13-notifications.md
// §9 asks for: title, summary, attach hint, dashboard link and (when set)
// the unsubscribe link, all present in the body Resend receives.
func TestEmailTemplate(t *testing.T) {
	var gotSubject, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Subject string            `json:"subject"`
			Text    string            `json:"text"`
			HTML    string            `json:"html"`
			Headers map[string]string `json:"headers"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotSubject, gotBody = payload.Subject, payload.Text
		// RFC 8058 one-click unsubscribe headers point at the same link.
		if payload.Headers["List-Unsubscribe"] != "<https://api.repose.herakraft.co/v1/notify/unsubscribe?token=abc.def>" || payload.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
			t.Errorf("list-unsubscribe headers %v", payload.Headers)
		}
		if !strings.Contains(payload.HTML, "ran tests, 3 failures fixed") || !strings.Contains(payload.HTML, "https://api.repose.herakraft.co/v1/notify/unsubscribe?token=abc.def") {
			t.Errorf("html lacks the summary or the unsubscribe link:\n%s", payload.HTML)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := &notify.Email{APIKey: "re_test", URL: srv.URL}
	m := notify.Message{
		Kind: "completed", Agent: "claude", Project: "todo-app", Summary: "ran tests, 3 failures fixed",
		Email: "u@example.com", Dashboard: "https://repose.herakraft.co",
		Unsubscribe: "https://api.repose.herakraft.co/v1/notify/unsubscribe?token=abc.def",
	}
	if err := e.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if gotSubject != "[repose] todo-app: claude finished" {
		t.Fatalf("subject %q", gotSubject)
	}
	for _, want := range []string{
		"todo-app: claude finished",
		"ran tests, 3 failures fixed",
		"repose attach --project todo-app",
		"https://repose.herakraft.co/projects",
		"https://api.repose.herakraft.co/v1/notify/unsubscribe?token=abc.def",
	} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body missing %q, got:\n%s", want, gotBody)
		}
	}
}

// TestEmailTemplateWithoutUnsubscribe covers the no-Unsubscriber-configured
// case (dev, or a platform secret write that failed): the email still
// sends, just without the link.
func TestEmailTemplateWithoutUnsubscribe(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotBody = payload.Text
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := &notify.Email{APIKey: "re_test", URL: srv.URL}
	if err := e.Send(context.Background(), notify.Message{Kind: "completed", Project: "todo", Summary: "ok", Email: "u@example.com"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotBody, "unsubscribe") {
		t.Fatalf("body should carry no unsubscribe mention without a link: %s", gotBody)
	}
}

// TestSubjectUsesPlatformWording is the checklist's "Platform events flow
// through the same pipeline" for the one kind DESIGN.md §13 gives its own
// exact subject; the rest fall back to Title.
func TestSubjectUsesPlatformWording(t *testing.T) {
	got := notify.Subject(notify.Message{Kind: "billing_stopped", Project: "todo-app"})
	if got != "Your machines were stopped: a payment failed" {
		t.Fatalf("subject %q", got)
	}
	got = notify.Subject(notify.Message{Kind: "snapshot_failed", Project: "todo-app"})
	if got != "todo-app: snapshot failed" {
		t.Fatalf("subject %q", got)
	}
	// The idle-cost warning (DECISIONS I-262).
	got = notify.Subject(notify.Message{Kind: "idle_running", Project: "todo-app"})
	if got != "todo-app: idle, still billing" {
		t.Fatalf("subject %q", got)
	}
}

// DECISIONS I-269, I-290, I-291: an account email names no project, so
// it has its own subject, no attach line and no unsubscribe line, and
// carries the payload's fields.
func TestAccountEmail(t *testing.T) {
	var got struct {
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html"`
		To      []string `json:"to"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := &notify.Email{APIKey: "re_test", URL: srv.URL}
	m := notify.Message{Kind: "waitlist_invited", Summary: `{"hold_until":"2026-09-30T14:00:00Z"}`, Email: "new@example.com", Dashboard: "https://dash.test",
		Unsubscribe: "https://api.test/v1/notify/unsubscribe?token=x"} // the outbox never sets it for account mail; the template ignores it anyway
	if err := e.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if got.Subject != "[repose] A seat is yours for 72 hours" || len(got.To) != 1 || got.To[0] != "new@example.com" {
		t.Fatalf("subject %q to %v", got.Subject, got.To)
	}
	if !strings.Contains(got.Text, "30 September 2026 at 14:00 UTC") || !strings.Contains(got.Text, "https://dash.test/billing") || strings.Contains(got.Text, "repose attach") || strings.Contains(got.Text, "unsubscribe") {
		t.Fatalf("body: %s", got.Text)
	}
	if strings.Contains(got.HTML, "unsubscribe") {
		t.Fatalf("html carries an unsubscribe link for account mail")
	}
}

func mustUUID(s string) uuid.UUID {
	u, err := parseUUID(s)
	if err != nil {
		panic(err) // test helper on a literal
	}
	return u
}

// The default ntfy client reaches public addresses only and follows no
// redirect (DECISIONS I-444): a URL pointing at loopback, or a public one
// redirecting elsewhere, fails permanently without a request arriving.
func TestNtfyDefaultClientRefusesNonPublic(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	var perm notify.Permanent
	for _, u := range []string{srv.URL + "/t", "http://localhost:" + srv.URL[strings.LastIndex(srv.URL, ":")+1:] + "/t"} {
		err := (&notify.Ntfy{}).Send(context.Background(), notify.Message{Kind: "completed", NtfyURL: u, Dashboard: "https://d"})
		if !errors.Is(err, notify.ErrNonPublicAddress) || !errors.As(err, &perm) {
			t.Fatalf("%s: %v, want a permanent ErrNonPublicAddress", u, err)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests reached the loopback server", hits)
	}
	for addr, want := range map[string]bool{
		"127.0.0.1": false, "::1": false, "10.255.0.1": false, "10.64.4.9": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "168.63.129.16": false, "100.100.100.100": false, "fd7a:115c:a1e0::1": false, "fe80::1": false,
		"::ffff:127.0.0.1": false, "0.0.0.0": false, "224.0.0.1": false,
		"1.1.1.1": true, "159.69.1.1": true, "2606:4700::1111": true,
	} {
		if got := notify.PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
	// A redirect is refused: PublicClient's CheckRedirect, exercised with a
	// transport that can reach the test server.
	redir := httptest.NewServer(http.RedirectHandler("http://example.invalid/", http.StatusFound))
	defer redir.Close()
	c := notify.PublicClient(5 * time.Second)
	c.Transport = redir.Client().Transport
	err := (&notify.Ntfy{HTTP: c}).Send(context.Background(), notify.Message{Kind: "completed", NtfyURL: redir.URL, Dashboard: "https://d"})
	if !errors.Is(err, notify.ErrRedirect) || !errors.As(err, &perm) {
		t.Fatalf("redirect: %v, want a permanent ErrRedirect", err)
	}
}
