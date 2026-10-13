package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/store"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// askEnv is a signed-in user with a running project whose fake guest can
// ask questions over the real host stream.
type askEnv struct {
	*env
	tok string
	u   *store.User
	p   *store.Project
}

func newAskEnv(t *testing.T, login string) *askEnv {
	t.Helper()
	e := newEnv(t)
	tok := e.signIn(t, "sub-"+login, login)
	var uid uuid.UUID
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select id from users where logto_sub = $1", "sub-"+login).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUser(e.h.Ctx, e.h.Pool, uid)
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.CreateRunning(u, "todo-"+login)
	return &askEnv{env: e, tok: tok, u: u, p: p}
}

// ask has the fake guest ask a question and waits until the api holds it.
func (a *askEnv) ask(t *testing.T, text string, options []string, timeoutS uint32) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	a.h.Fake.Ask(&hostdv1.AgentQuestion{GuestId: a.p.GuestID.String(), QuestionId: id.String(), Agent: "claude", TmuxWindow: "claude", Text: text, Options: options, TimeoutS: timeoutS})
	a.h.WaitFor("the question row", func() bool {
		var n int
		_ = a.h.Pool.QueryRow(a.h.Ctx, "select count(*) from questions q join events e on e.id = q.event_id where q.id = $1", id).Scan(&n)
		return n == 1
	})
	return id
}

// outboxMessage runs the outbox once and returns the message the channel's
// sender got for the event.
func (a *askEnv) outboxMessage(t *testing.T, channel string, eventID uuid.UUID) notify.Message {
	t.Helper()
	var got []notify.Message
	sender := notify.SenderFunc(func(_ context.Context, m notify.Message) error {
		if m.EventID == eventID {
			got = append(got, m)
		}
		return nil
	})
	o := notify.New(a.h.Pool, map[string]notify.Sender{"email": sender, "ntfy": sender}, a.h.Metrics, a.h.Log)
	o.Unsub = a.unsub
	o.APIBase = a.api.URL
	o.Dashboard = "https://dash.test"
	if _, err := o.Once(a.h.Ctx); err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		// Only the email carries the unsubscribe link.
		if (channel == "email") == (m.Unsubscribe != "") {
			return m
		}
	}
	t.Fatalf("no %s message for event %s (got %d)", channel, eventID, len(got))
	return notify.Message{}
}

func (a *askEnv) eventOf(t *testing.T, qid uuid.UUID) uuid.UUID {
	t.Helper()
	var eid uuid.UUID
	if err := a.h.Pool.QueryRow(a.h.Ctx, "select event_id from questions where id = $1", qid).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	return eid
}

func postForm(t *testing.T, link string) (int, string) {
	t.Helper()
	res, err := http.Post(link, "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func getPage(t *testing.T, link string) (int, string) {
	t.Helper()
	res, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// TestQuestionReplyLinkEndToEnd: a guest's question notifies the owner
// with one signed link per option; the link's GET only shows a button, its
// POST answers once, a second POST is refused, and the answer travels back
// to the guest as AnswerQuestion, whose result closes the delivery.
func TestQuestionReplyLinkEndToEnd(t *testing.T) {
	a := newAskEnv(t, "ria")
	if r := a.do(t, a.tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"ntfy_url": "https://ntfy.example/topic"}}); r.status != 200 {
		t.Fatalf("set ntfy: %d %s", r.status, r.raw)
	}
	qid := a.ask(t, "Drop the legacy sessions table?", []string{"yes", "no"}, 1800)

	// Listed as pending, with its options.
	r := a.do(t, a.tok, "GET", "/questions", nil)
	qs, _ := r.body["questions"].([]any)
	if r.status != 200 || len(qs) != 1 {
		t.Fatalf("list: %d %s", r.status, r.raw)
	}
	q0 := qs[0].(map[string]any)
	if q0["state"] != "pending" || q0["text"] != "Drop the legacy sessions table?" || q0["project"] != a.p.Slug || len(q0["options"].([]any)) != 2 {
		t.Fatalf("question: %v", q0)
	}

	// The ntfy message carries a reply link per option and the project page.
	m := a.outboxMessage(t, "ntfy", a.eventOf(t, qid))
	if m.Kind != "agent_question" || m.Question == nil || len(m.Question.Replies) != 2 || m.Summary != "Drop the legacy sessions table?" {
		t.Fatalf("message: %+v", m)
	}
	if notify.Title(m) != a.p.Slug+": claude asks" {
		t.Fatalf("title %q", notify.Title(m))
	}
	yes := m.Question.Replies[0]
	if !strings.HasPrefix(yes, a.api.URL+"/v1/questions/reply?token=") {
		t.Fatalf("reply link %q", yes)
	}

	// GET shows the question and a button; it does not answer.
	code, page := getPage(t, yes)
	if code != 200 || !strings.Contains(page, "Drop the legacy sessions table?") || !strings.Contains(page, `method="post"`) {
		t.Fatalf("GET reply: %d %s", code, page)
	}
	if r := a.do(t, a.tok, "GET", "/questions", nil); len(r.body["questions"].([]any)) != 1 {
		t.Fatal("a GET of the reply link answered the question")
	}

	// A tampered token is refused and changes nothing.
	u, _ := url.Parse(yes)
	tok := u.Query().Get("token")
	bad := strings.Replace(yes, tok, tok[:len(tok)-2]+"AA", 1)
	if code, _ := postForm(t, bad); code != 400 {
		t.Fatalf("tampered token: %d", code)
	}

	// POST answers, once.
	code, page = postForm(t, yes)
	if code != 200 || !strings.Contains(page, "Answered: yes") {
		t.Fatalf("POST reply: %d %s", code, page)
	}
	code, page = postForm(t, m.Question.Replies[1])
	if code != 409 || !strings.Contains(page, "Already answered: yes") {
		t.Fatalf("second answer: %d %s", code, page)
	}
	r = a.do(t, a.tok, "GET", "/projects/"+a.p.ID.String()+"/questions", nil)
	q0 = r.body["questions"].([]any)[0].(map[string]any)
	if q0["state"] != "answered" || q0["answer"] != "yes" || q0["answered_via"] != "ntfy" {
		t.Fatalf("after reply: %v", q0)
	}

	// The worker carries the answer to the guest; the result closes it.
	if err := a.h.Questions.Once(a.h.Ctx); err != nil {
		t.Fatal(err)
	}
	a.h.WaitFor("the answer at the guest", func() bool {
		got := a.h.Fake.Answer(qid.String())
		return got != nil && got.Status == "answered" && got.Answer == "yes"
	})
	a.h.WaitFor("the delivery acknowledged", func() bool {
		var d *string
		_ = a.h.Pool.QueryRow(a.h.Ctx, "select delivery from questions where id = $1", qid).Scan(&d)
		return d != nil && *d == "ok"
	})
	// The guest re-announcing the question (a hostd restart) changes nothing.
	a.h.Fake.Ask(&hostdv1.AgentQuestion{GuestId: a.p.GuestID.String(), QuestionId: qid.String(), Agent: "claude", Text: "Drop the legacy sessions table?", Options: []string{"yes", "no"}, TimeoutS: 1800})
	time.Sleep(200 * time.Millisecond)
	var n int
	_ = a.h.Pool.QueryRow(a.h.Ctx, "select count(*) from events where kind = 'agent_question' and project_id = $1", a.p.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("a re-announced question made %d events", n)
	}

	// Nothing the tenant typed reached the log.
	if s := a.logs.String(); strings.Contains(s, "legacy sessions") || strings.Contains(s, `"answer":"yes"`) {
		t.Fatalf("question or answer text in the log:\n%s", s)
	}
}

// TestQuestionAnswerRouteAuthAndOptions: the answer route needs the owner,
// holds the answer to the options, and the first answer wins.
func TestQuestionAnswerRouteAuthAndOptions(t *testing.T) {
	a := newAskEnv(t, "ola")
	qid := a.ask(t, "Which db?", []string{"Postgres", "SQLite"}, 600)
	path := "/projects/" + a.p.ID.String() + "/questions/" + qid.String()

	if r := a.do(t, "", "POST", path+"/answer", map[string]any{"answer": "Postgres"}); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	other := a.signIn(t, "sub-mallory", "mallory")
	if r := a.do(t, other, "POST", path+"/answer", map[string]any{"answer": "Postgres"}); r.status != 404 {
		t.Fatalf("another user's question: %d %s", r.status, r.raw)
	}
	if r := a.do(t, other, "GET", "/questions", nil); len(r.body["questions"].([]any)) != 0 {
		t.Fatalf("another user sees the question: %s", r.raw)
	}
	r := a.do(t, a.tok, "POST", path+"/answer", map[string]any{"answer": "MySQL"})
	if r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("not an option: %d %s", r.status, r.raw)
	}
	r = a.do(t, a.tok, "POST", path+"/answer", map[string]any{"answer": "sqlite", "via": "cli"})
	if r.status != 200 || r.body["answer"] != "SQLite" || r.body["answered_via"] != "cli" {
		t.Fatalf("answer: %d %s", r.status, r.raw)
	}
	r = a.do(t, a.tok, "POST", path+"/answer", map[string]any{"answer": "Postgres"})
	if r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("second answer: %d %s", r.status, r.raw)
	}

	// Free text, then dismissal.
	q2 := a.ask(t, "Anything else?", nil, 600)
	p2 := "/projects/" + a.p.ID.String() + "/questions/" + q2.String()
	if r := a.do(t, a.tok, "POST", p2+"/answer", map[string]any{"answer": "  "}); r.status != 400 {
		t.Fatalf("empty answer: %d", r.status)
	}
	if r := a.do(t, a.tok, "POST", p2+"/cancel", nil); r.status != 200 || r.body["state"] != "cancelled" || r.body["answered_via"] != "dashboard" {
		t.Fatalf("cancel: %d %s", r.status, r.raw)
	}
	// The CLI's dismissal says where it came from.
	q3 := a.ask(t, "Ship it?", nil, 600)
	p3 := "/projects/" + a.p.ID.String() + "/questions/" + q3.String()
	if r := a.do(t, a.tok, "POST", p3+"/cancel", map[string]any{"via": "cli"}); r.status != 200 || r.body["answered_via"] != "cli" {
		t.Fatalf("cancel from the CLI: %d %s", r.status, r.raw)
	}
	if err := a.h.Questions.Once(a.h.Ctx); err != nil {
		t.Fatal(err)
	}
	a.h.WaitFor("the cancel at the guest", func() bool {
		got := a.h.Fake.Answer(q2.String())
		return got != nil && got.Status == "cancelled"
	})
}

// TestQuestionExpiry: an expired reply link is 410, and a question past its
// timeout cannot be answered and is closed by the worker.
func TestQuestionExpiry(t *testing.T) {
	a := newAskEnv(t, "eve")
	qid := a.ask(t, "Still there?", []string{"yes"}, 60)
	old := a.unsub.ReplyURL(a.api.URL, qid, 0, time.Now().Add(-time.Second), "ntfy")
	if code, page := postForm(t, old); code != 410 || !strings.Contains(page, "expired") {
		t.Fatalf("expired link: %d %s", code, page)
	}
	if _, err := a.h.Pool.Exec(a.h.Ctx, "update questions set expires_at = now() - interval '1 minute' where id = $1", qid); err != nil {
		t.Fatal(err)
	}
	r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/questions/"+qid.String()+"/answer", map[string]any{"answer": "yes"})
	if r.status != 409 {
		t.Fatalf("answer after expiry: %d %s", r.status, r.raw)
	}
	if r := a.do(t, a.tok, "GET", "/questions", nil); len(r.body["questions"].([]any)) != 0 {
		t.Fatalf("an expired question is listed as pending: %s", r.raw)
	}
	if err := a.h.Questions.Once(a.h.Ctx); err != nil {
		t.Fatal(err)
	}
	var st string
	_ = a.h.Pool.QueryRow(a.h.Ctx, "select state from questions where id = $1", qid).Scan(&st)
	if st != "expired" {
		t.Fatalf("state after the worker: %s", st)
	}
}

// TestQuestionWithNoChannel: with email and ntfy both off, the question is
// closed at once as no_channel and the guest is told, so repose-ask exits 4.
func TestQuestionWithNoChannel(t *testing.T) {
	a := newAskEnv(t, "nia")
	if r := a.do(t, a.tok, "PATCH", "/me", map[string]any{"notify": map[string]any{"email": false}}); r.status != 200 {
		t.Fatalf("email off: %d %s", r.status, r.raw)
	}
	qid := a.ask(t, "Anyone?", nil, 600)
	if err := a.h.Questions.Once(a.h.Ctx); err != nil {
		t.Fatal(err)
	}
	a.h.WaitFor("no_channel at the guest", func() bool {
		got := a.h.Fake.Answer(qid.String())
		return got != nil && got.Status == "no_channel"
	})
}

// TestQuestionsEndWithTheGuest: stopping the project cancels its pending
// questions; a guest that forgot a question (not_found) ends the delivery.
func TestQuestionsEndWithTheGuest(t *testing.T) {
	a := newAskEnv(t, "sam")
	q1 := a.ask(t, "one", nil, 600)
	q2 := a.ask(t, "two", nil, 600)
	a.h.Fake.ForgetQuestion(q2.String())
	r := a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/questions/"+q2.String()+"/answer", map[string]any{"answer": "ok"})
	if r.status != 200 {
		t.Fatalf("answer: %d %s", r.status, r.raw)
	}
	if err := a.h.Questions.Once(a.h.Ctx); err != nil {
		t.Fatal(err)
	}
	a.h.WaitFor("gone", func() bool {
		var d *string
		_ = a.h.Pool.QueryRow(a.h.Ctx, "select delivery from questions where id = $1", q2).Scan(&d)
		return d != nil && *d == "gone"
	})
	r = a.do(t, a.tok, "POST", "/projects/"+a.p.ID.String()+"/stop", nil)
	if r.status != 202 && r.status != 200 {
		t.Fatalf("stop: %d %s", r.status, r.raw)
	}
	a.waitOp(t, r)
	a.h.WaitFor("q1 cancelled", func() bool {
		var st string
		_ = a.h.Pool.QueryRow(a.h.Ctx, "select state from questions where id = $1", q1).Scan(&st)
		return st == "cancelled"
	})
}

// TestReplyLinkRateLimit: a question's reply link takes at most
// ReplyLinkRate tries a minute.
func TestReplyLinkRateLimit(t *testing.T) {
	a := newAskEnv(t, "rex")
	qid := a.ask(t, "ok?", []string{"yes", "no"}, 600)
	link := a.unsub.ReplyURL(a.api.URL, qid, 0, time.Now().Add(time.Hour), "email")
	var last int
	for i := 0; i < 25; i++ {
		last, _ = getPage(t, link)
		if last == 429 {
			break
		}
	}
	if last != 429 {
		t.Fatalf("25 tries in a row never hit the limit (last %d)", last)
	}
}
