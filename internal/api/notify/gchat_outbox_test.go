package notify_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// seedGChat is a user with only Google Chat on (email off) and a project.
func seedGChat(t *testing.T, pool *db.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid, pid := store.NewID(), store.NewID()
	if _, err := pool.Exec(ctx, "insert into users (id, handle, email, notify_email, gchat_url) values ($1, $2, 'u@example.com', false, $3)", uid, "g"+uid.String()[24:], hookURL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'todo', 'todo', 'large', 'running', 1)", pid, uid); err != nil {
		t.Fatal(err)
	}
	return uid, pid
}

// A Chat webhook Google refuses is marked broken once, with one account
// email, and nothing more is sent to it until a test gets through; the
// stored failure never carries the URL.
func TestGChatBrokenOnce(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	m := metrics.NewNop()
	uid, pid := seedGChat(t, pool)
	var status atomic.Int32
	status.Store(403)
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	c, _ := gchatClient(srv)
	ob := notify.New(pool, map[string]notify.Sender{"gchat": &notify.GChat{HTTP: c, Pace: time.Millisecond}, "email": &recorder{}}, m, log)
	now := time.Now()
	ob.Now = func() time.Time { return now }
	ing := events.New(pool, m, log)
	var ids []uuid.UUID
	for i := 0; i < 2; i++ {
		id, _, err := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now.Add(time.Duration(i) * time.Second), Kind: "agent_message", Agent: "claude", Summary: "hello " + string(rune('a'+i))})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	var chans []string
	if err := pool.QueryRow(ctx, "select array_agg(channel order by channel) from events_outbox where event_id = $1", ids[0]).Scan(&chans); err != nil || strings.Join(chans, ",") != "gchat" {
		t.Fatalf("outbox channels %v %v", chans, err)
	}
	now = now.Add(5 * time.Second)
	if _, err := ob.Once(ctx); err != nil {
		t.Fatal(err)
	}
	// The first row was refused and marked the channel; the second was
	// dropped without a post.
	if posts.Load() != 1 {
		t.Fatalf("posts %d, want 1", posts.Load())
	}
	var broken *time.Time
	if err := pool.QueryRow(ctx, "select gchat_broken_at from users where id = $1", uid).Scan(&broken); err != nil || broken == nil {
		t.Fatalf("not marked broken: %v", err)
	}
	var mails int
	if err := pool.QueryRow(ctx, "select count(*) from events e join events_outbox o on o.event_id = e.id and o.channel = 'email' where e.user_id = $1 and e.kind = 'notify_channel_broken'", uid).Scan(&mails); err != nil || mails != 1 {
		t.Fatalf("broken emails queued %d %v", mails, err)
	}
	var delivered string
	if err := pool.QueryRow(ctx, "select delivered->>'gchat' from events where id = $1", ids[0]).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(delivered, "failed: ") {
		t.Errorf("delivered %q", delivered)
	}
	noSecret(t, "delivered", delivered)
	// A broken channel takes no new rows and counts as no channel.
	chs, err := events.UserChannels(ctx, pool, uid)
	if err != nil || len(chs) != 0 {
		t.Fatalf("channels while broken %v %v", chs, err)
	}
	id3, _, err := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now, Kind: "agent_message", Agent: "claude", Summary: "third"})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, "select count(*) from events_outbox where event_id = $1", id3).Scan(&n)
	if n != 0 {
		t.Fatalf("a broken channel took a row")
	}
	// A second refusal (a test) does not mail again.
	u, err := store.GetUser(ctx, pool, uid)
	if err != nil {
		t.Fatal(err)
	}
	res := ob.Test(ctx, u)
	if res["gchat"] != "error" || !strings.Contains(res["gchat_error"], "403") {
		t.Fatalf("test while refused: %v", res)
	}
	noSecret(t, "test result", res["gchat_error"])
	_ = pool.QueryRow(ctx, "select count(*) from events where user_id = $1 and kind = 'notify_channel_broken'", uid).Scan(&mails)
	if mails != 1 {
		t.Fatalf("broken emails %d after a second refusal", mails)
	}
	// A test that gets through clears the mark.
	status.Store(200)
	if res := ob.Test(ctx, u); res["gchat"] != "ok" {
		t.Fatalf("test: %v", res)
	}
	if err := pool.QueryRow(ctx, "select gchat_broken_at from users where id = $1", uid).Scan(&broken); err != nil || broken != nil {
		t.Fatalf("still broken after a good test: %v", err)
	}
	if chs, _ := events.UserChannels(ctx, pool, uid); len(chs) != 1 || chs[0] != "gchat" {
		t.Fatalf("channels after clearing %v", chs)
	}
}

// A question's close is posted in the question's thread.
func TestGChatQuestionClosedReply(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	uid, _ := seedGChat(t, pool)
	var threads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		threads = append(threads, r.URL.Query().Get("threadKey"))
	}))
	defer srv.Close()
	c, _ := gchatClient(srv)
	ob := notify.New(pool, map[string]notify.Sender{"gchat": &notify.GChat{HTTP: c, Pace: time.Millisecond}}, metrics.NewNop(), log)
	qid := store.NewID()
	ok, err := events.InsertQuestionClosed(ctx, pool, uid, time.Now(), events.QuestionClosed{QuestionID: qid, Text: "Answered in the terminal"})
	if err != nil || !ok {
		t.Fatalf("queued %v %v", ok, err)
	}
	ob.Now = func() time.Time { return time.Now().Add(time.Second) }
	if n, err := ob.Once(ctx); err != nil || n != 1 {
		t.Fatalf("once %d %v", n, err)
	}
	if len(threads) != 1 || threads[0] != "q-"+qid.String() {
		t.Fatalf("threads %v", threads)
	}
	// With Google Chat off, nothing is queued.
	if _, err := pool.Exec(ctx, "update users set gchat_url = null where id = $1", uid); err != nil {
		t.Fatal(err)
	}
	if ok, err := events.InsertQuestionClosed(ctx, pool, uid, time.Now(), events.QuestionClosed{QuestionID: qid, Text: "x"}); err != nil || ok {
		t.Fatalf("queued with chat off: %v %v", ok, err)
	}
}

// A `notify test` that Google refuses marks an unmarked channel broken,
// with one account email, as an outbox refusal does.
func TestGChatTestMarksBroken(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, _ := seedGChat(t, pool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer srv.Close()
	c, _ := gchatClient(srv)
	ob := notify.New(pool, map[string]notify.Sender{"gchat": &notify.GChat{HTTP: c, Pace: time.Millisecond}}, metrics.NewNop(), slog.New(slog.DiscardHandler))
	u, err := store.GetUser(ctx, pool, uid)
	if err != nil {
		t.Fatal(err)
	}
	res := ob.Test(ctx, u)
	if want := "the server answered 404: the webhook was deleted, or its space removed it"; res["gchat"] != "error" || res["gchat_error"] != want {
		t.Fatalf("test result %v", res)
	}
	var broken *time.Time
	if err := pool.QueryRow(ctx, "select gchat_broken_at from users where id = $1", uid).Scan(&broken); err != nil || broken == nil {
		t.Fatalf("a refused test did not mark the channel: %v", err)
	}
	var mails int
	if err := pool.QueryRow(ctx, "select count(*) from events e join events_outbox o on o.event_id = e.id and o.channel = 'email' where e.user_id = $1 and e.kind = 'notify_channel_broken'", uid).Scan(&mails); err != nil || mails != 1 {
		t.Fatalf("broken emails queued %d %v", mails, err)
	}
}

// A refusal of a URL the user has since replaced does not mark the new one.
func TestGChatRefusalOfReplacedURL(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	uid, pid := seedGChat(t, pool)
	const second = "https://chat.googleapis.com/v1/spaces/BBBBx2yZ/messages?key=k2&token=t2"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The user saves a new webhook while the post to the old one is
		// in flight; Google then refuses the old one.
		if _, err := pool.Exec(context.Background(), "update users set gchat_url = $2 where id = $1", uid, second); err != nil {
			t.Error(err)
		}
		w.WriteHeader(403)
	}))
	defer srv.Close()
	c, _ := gchatClient(srv)
	ob := notify.New(pool, map[string]notify.Sender{"gchat": &notify.GChat{HTTP: c, Pace: time.Millisecond}, "email": &recorder{}}, metrics.NewNop(), slog.New(slog.DiscardHandler))
	now := time.Now()
	ob.Now = func() time.Time { return now.Add(5 * time.Second) }
	if _, _, err := events.New(pool, metrics.NewNop(), slog.New(slog.DiscardHandler)).Insert(ctx, events.Incoming{ProjectID: pid, TS: now, Kind: "agent_message", Agent: "claude", Summary: "hello"}); err != nil {
		t.Fatal(err)
	}
	if n, err := ob.Once(ctx); err != nil || n != 1 {
		t.Fatalf("once %d %v", n, err)
	}
	var broken *time.Time
	if err := pool.QueryRow(ctx, "select gchat_broken_at from users where id = $1", uid).Scan(&broken); err != nil || broken != nil {
		t.Fatalf("the new webhook was marked broken by the old one's refusal: %v", err)
	}
	var mails int
	_ = pool.QueryRow(ctx, "select count(*) from events where user_id = $1 and kind = 'notify_channel_broken'", uid).Scan(&mails)
	if mails != 0 {
		t.Fatalf("broken emails %d", mails)
	}
}

// seedGChatQuestion is a pending question with options whose event has a
// gchat outbox row.
func seedGChatQuestion(t *testing.T, pool *db.Pool, pid uuid.UUID, now time.Time) (qid, eid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	qid, eid = store.NewID(), store.NewID()
	if _, err := pool.Exec(ctx, "insert into events (id, project_id, ts, ts_second, kind, agent, summary, source) values ($1, $2, $3, $4, 'agent_question', 'claude', 'Ship it?', 'host')", eid, pid, now, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into questions (id, project_id, guest_id, event_id, agent, text, options, expires_at, state) values ($1, $2, $5, $3, 'claude', 'Ship it?', '{yes,no}', $4, 'pending')", qid, pid, eid, now.Add(time.Hour), store.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into events_outbox (event_id, channel) values ($1, 'gchat')", eid); err != nil {
		t.Fatal(err)
	}
	return qid, eid
}

// A question card still queued when its question closes is not posted:
// its buttons would answer nothing, and no close reply would follow it.
func TestGChatClosedQuestionCardDropped(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	_, pid := seedGChat(t, pool)
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1) }))
	defer srv.Close()
	c, _ := gchatClient(srv)
	ob := notify.New(pool, map[string]notify.Sender{"gchat": &notify.GChat{HTTP: c, Pace: time.Millisecond}}, metrics.NewNop(), slog.New(slog.DiscardHandler))
	var hooked []uuid.UUID
	ob.QuestionCardPosted = func(_ context.Context, id uuid.UUID) { hooked = append(hooked, id) }
	now := time.Now()
	ob.Now = func() time.Time { return now.Add(5 * time.Second) }
	qid, eid := seedGChatQuestion(t, pool, pid, now)
	if _, err := pool.Exec(ctx, "update questions set state = 'answered', answer = 'yes', answered_via = 'terminal' where id = $1", qid); err != nil {
		t.Fatal(err)
	}
	if _, err := ob.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 0 || len(hooked) != 0 {
		t.Fatalf("posts %d, hooked %v: a closed question's card went out", posts.Load(), hooked)
	}
	var left int
	_ = pool.QueryRow(ctx, "select count(*) from events_outbox where event_id = $1", eid).Scan(&left)
	if left != 0 {
		t.Fatalf("the closed question's row stayed queued")
	}
}

// A question card that posts calls QuestionCardPosted, so a question that
// closed while its card was in flight still gets its close reply.
func TestGChatQuestionCardPostedHook(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	_, pid := seedGChat(t, pool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c, _ := gchatClient(srv)
	ob := notify.New(pool, map[string]notify.Sender{"gchat": &notify.GChat{HTTP: c, Pace: time.Millisecond}}, metrics.NewNop(), slog.New(slog.DiscardHandler))
	var hooked []uuid.UUID
	var delivered string
	ob.QuestionCardPosted = func(ctx context.Context, id uuid.UUID) {
		hooked = append(hooked, id)
		// The delivered timestamp is written before the hook runs, so the
		// close reply's own check sees the card as posted.
		_ = pool.QueryRow(ctx, "select coalesce(e.delivered->>'gchat', '') from events e join questions q on q.event_id = e.id where q.id = $1", id).Scan(&delivered)
	}
	now := time.Now()
	ob.Now = func() time.Time { return now.Add(5 * time.Second) }
	qid, _ := seedGChatQuestion(t, pool, pid, now)
	if _, err := ob.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if len(hooked) != 1 || hooked[0] != qid {
		t.Fatalf("hooked %v, want [%s]", hooked, qid)
	}
	if delivered == "" || strings.HasPrefix(delivered, "failed") {
		t.Fatalf("delivered at hook time %q", delivered)
	}
}
