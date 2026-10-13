package httpapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/store"
)

const (
	gchatKey   = "AIzaSyHookKeyForTests0123456789"
	gchatToken = "hookTokenForTests-abc_DEF"
	gchatHook  = "https://chat.googleapis.com/v1/spaces/AAAAtest1/messages?key=" + gchatKey + "&token=" + gchatToken
)

// PATCH /me notify.gchat_url takes only a Google Chat webhook, stores it
// in canonical form, and nothing returns it: GET /me shows the space and
// whether it is broken; an error never quotes it; null clears it and its
// queued rows; saving a URL clears the broken mark (DECISIONS I-692).
func TestPatchMeGChat(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-chat", "chatter")
	var uid string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select id from users where logto_sub = 'sub-chat'").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	gchat := func() any {
		t.Helper()
		r := e.do(t, tok, "GET", "/me", nil)
		if strings.Contains(string(r.raw), gchatKey) || strings.Contains(string(r.raw), gchatToken) {
			t.Fatalf("GET /me echoes the webhook: %s", r.raw)
		}
		return r.body["notify"].(map[string]any)["gchat"]
	}
	if got := gchat(); got != nil {
		t.Fatalf("gchat before any: %v", got)
	}
	r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"gchat_url": " " + gchatHook + "&threadKey=x "}})
	if r.status != 200 || strings.Contains(string(r.raw), gchatKey) || strings.Contains(string(r.raw), gchatToken) {
		t.Fatalf("set: %d %s", r.status, r.raw)
	}
	g, _ := gchat().(map[string]any)
	if g == nil || g["space"] != "AAAAtest1" || g["broken_at"] != nil {
		t.Fatalf("gchat after set: %v", g)
	}
	var stored string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select gchat_url from users where id = $1", uid).Scan(&stored); err != nil || stored != gchatHook {
		t.Fatalf("stored %q %v", stored, err)
	}
	for _, bad := range []any{
		"https://chat.googleapis.com.evil.example/v1/spaces/A/messages?key=" + gchatKey + "&token=" + gchatToken,
		"http://chat.googleapis.com/v1/spaces/A/messages?key=" + gchatKey + "&token=" + gchatToken,
		"https://chat.googleapis.com/v1/spaces/A/messages?key=" + gchatKey,
		"https://hooks.slack.com/services/" + gchatToken,
		5,
	} {
		r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"gchat_url": bad}})
		if r.status != 400 || strings.Contains(string(r.raw), gchatKey) || strings.Contains(string(r.raw), gchatToken) {
			t.Fatalf("gchat_url %v: %d %s", bad, r.status, r.raw)
		}
	}
	if strings.Contains(e.logs.String(), gchatKey) || strings.Contains(e.logs.String(), gchatToken) {
		t.Fatal("a log line carries the webhook")
	}
	// Broken, then a new URL saved: the mark goes.
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update users set gchat_broken_at = $2 where id = $1", uid, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if g, _ := gchat().(map[string]any); g == nil || g["broken_at"] != "2026-10-12T09:00:00Z" {
		t.Fatalf("broken: %v", g)
	}
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"gchat_url": gchatHook}}); r.status != 200 {
		t.Fatalf("resave: %d %s", r.status, r.raw)
	}
	if g, _ := gchat().(map[string]any); g == nil || g["broken_at"] != nil {
		t.Fatalf("saving did not clear broken: %v", g)
	}
	// A queued user notice and its gchat row go when the channel does.
	var nid string
	if err := e.h.Pool.QueryRow(e.h.Ctx, "insert into events (id, user_id, ts, ts_second, kind, summary, source) values ($1, $2, now(), 0, 'machines_restarted', 'x', 'api') returning id", store.NewID(), uid).Scan(&nid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(e.h.Ctx, "insert into events_outbox (event_id, channel) values ($1, 'gchat')", nid); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"gchat_url": nil}}); r.status != 200 {
		t.Fatalf("clear: %d %s", r.status, r.raw)
	}
	if got := gchat(); got != nil {
		t.Fatalf("gchat after null: %v", got)
	}
	var n int
	_ = e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from events_outbox where channel = 'gchat' and event_id = $1", nid).Scan(&n)
	if n != 0 {
		t.Fatal("the gchat row of a user notice stayed")
	}
	// POST /me/notify-test reports gchat once it is set.
	if r := e.do(t, tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"gchat_url": gchatHook}}); r.status != 200 {
		t.Fatal(r.raw)
	}
	r = e.do(t, tok, "POST", "/me/notify-test", nil)
	if r.status != 200 || r.body["gchat"] != "ok" {
		t.Fatalf("notify-test: %d %s", r.status, r.raw)
	}
}

// A reply link from a Google Chat card records the answer as gchat.
func TestQuestionReplyViaGChat(t *testing.T) {
	a := newAskEnv(t, "gia")
	if r := a.do(t, a.tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"gchat_url": gchatHook}}); r.status != 200 {
		t.Fatalf("set gchat: %d %s", r.status, r.raw)
	}
	qid := a.ask(t, "Ship it?", []string{"yes", "no"}, 1800)
	var expires time.Time
	if err := a.h.Pool.QueryRow(a.h.Ctx, "select expires_at from questions where id = $1", qid).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	link := a.unsub.ReplyURL(a.api.URL, qid, 0, expires, "gchat")
	if code, page := postForm(t, link); code != 200 || !strings.Contains(page, "Answered: yes") {
		t.Fatalf("POST reply: %d %s", code, page)
	}
	r := a.do(t, a.tok, "GET", "/projects/"+a.p.ID.String()+"/questions", nil)
	q0 := r.body["questions"].([]any)[0].(map[string]any)
	if q0["answered_via"] != "gchat" {
		t.Fatalf("answered_via %v", q0["answered_via"])
	}
}
