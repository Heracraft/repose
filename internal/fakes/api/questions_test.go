package api

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestFakeQuestions: the fake follows api.md "Questions" (DECISIONS I-245):
// listing, options held case-insensitively, first answer wins, dismissal,
// and the unauthenticated reply link.
func TestFakeQuestions(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p, err := f.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	q, err := f.AddQuestion(p.ID, "claude", "Drop the legacy table?", []string{"yes", "no"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Questions []Question }
	call(t, f, "GET", "/v1/questions", tok, nil).json(t, &list)
	if len(list.Questions) != 1 || list.Questions[0].Project != "todo-app" || list.Questions[0].State != "pending" {
		t.Fatalf("list %+v", list)
	}
	path := "/v1/projects/" + p.ID + "/questions/" + q.ID
	if r := call(t, f, "POST", path+"/answer", tok, map[string]any{"answer": "maybe"}); r.status != 400 || r.errCode(t) != "invalid" {
		t.Fatalf("not an option: %d %s", r.status, r.body)
	}
	var got Question
	r := call(t, f, "POST", path+"/answer", tok, map[string]any{"answer": "YES", "via": "cli"})
	r.json(t, &got)
	if r.status != 200 || *got.Answer != "yes" || *got.AnsweredVia != "cli" {
		t.Fatalf("answer: %d %s", r.status, r.body)
	}
	if r := call(t, f, "POST", path+"/answer", tok, map[string]any{"answer": "no"}); r.status != 409 {
		t.Fatalf("second answer: %d", r.status)
	}
	call(t, f, "GET", "/v1/questions", tok, nil).json(t, &list)
	if len(list.Questions) != 0 {
		t.Fatalf("answered question still pending: %+v", list)
	}

	q2, _ := f.AddQuestion(p.ID, "", "Ship?", []string{"ship", "wait"}, time.Hour)
	res, err := http.Post(f.URL()+"/v1/questions/reply?token="+ReplyToken(q2.ID, 1), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || f.Questions()[1].State != "answered" || *f.Questions()[1].Answer != "wait" {
		t.Fatalf("reply link: %d %+v", res.StatusCode, f.Questions()[1])
	}
	q3, _ := f.AddQuestion(p.ID, "codex", "Anything?", nil, time.Hour)
	if r := call(t, f, "POST", "/v1/projects/"+p.ID+"/questions/"+q3.ID+"/cancel", tok, nil); r.status != 200 {
		t.Fatalf("cancel: %d %s", r.status, r.body)
	}
	call(t, f, "GET", "/v1/projects/"+p.ID+"/questions", tok, nil).json(t, &list)
	if len(list.Questions) != 3 {
		t.Fatalf("project list %+v", list)
	}
}

// TestFakeGuestCloseAndGchatReply (DECISIONS I-690, I-692): a guest-side
// close with reason terminal reads as answered in the terminal, withdrawn
// as withdrawn by the agent, and a reply link with via=gchat answers via
// gchat.
func TestFakeGuestCloseAndGchatReply(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p, err := f.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	q1, _ := f.AddQuestion(p.ID, "claude", "One?", []string{"yes", "no"}, time.Hour)
	q2, _ := f.AddQuestion(p.ID, "claude", "Two?", nil, time.Hour)
	q3, _ := f.AddQuestion(p.ID, "claude", "Three?", []string{"a", "b"}, time.Hour)
	got, err := f.CloseQuestion(q1.ID, "", "terminal")
	if err != nil || got.State != "answered" || got.Answer != nil || *got.AnsweredVia != "terminal" || got.AnsweredAt == nil {
		t.Fatalf("terminal: %+v %v", got, err)
	}
	if got, err = f.CloseQuestion(q2.ID, "cancelled", "withdrawn"); err != nil || got.State != "cancelled" || *got.AnsweredVia != "agent" {
		t.Fatalf("withdrawn: %+v %v", got, err)
	}
	r := call(t, f, "POST", "/v1/projects/"+p.ID+"/questions/"+q1.ID+"/answer", tok, map[string]any{"answer": "yes"})
	if r.status != 409 || !contains(string(r.body), "answered in the terminal") {
		t.Fatalf("answer after a terminal close: %d %s", r.status, r.body)
	}
	r = call(t, f, "POST", "/v1/projects/"+p.ID+"/questions/"+q2.ID+"/cancel", tok, nil)
	if r.status != 409 || !contains(string(r.body), "withdrawn by the agent") {
		t.Fatalf("dismiss after a withdrawal: %d %s", r.status, r.body)
	}
	res, err := http.Post(f.URL()+"/v1/questions/reply?via=gchat&token="+ReplyToken(q3.ID, 0), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if q := f.Questions()[2]; res.StatusCode != 200 || *q.AnsweredVia != "gchat" {
		t.Fatalf("gchat reply: %d %+v", res.StatusCode, q)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
