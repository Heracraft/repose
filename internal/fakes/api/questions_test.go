package api

import (
	"net/http"
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

// TestFakeQuestionGuestClose: a guest-side close with a reason (I-690)
// lands as the api stores it: terminal is answered with no answer and
// answered_via terminal, withdrawn is cancelled by the agent; a later
// answer from the dashboard is a conflict that says so.
func TestFakeQuestionGuestClose(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	p, err := f.CreateProject("todo-app", "large")
	if err != nil {
		t.Fatal(err)
	}
	q1, _ := f.AddQuestion(p.ID, "claude", "Which port?", nil, time.Hour)
	q2, _ := f.AddQuestion(p.ID, "claude", "Ship?", []string{"yes", "no"}, time.Hour)
	if err := f.CloseQuestion(q1.ID, "terminal"); err != nil {
		t.Fatal(err)
	}
	if err := f.CloseQuestion(q2.ID, "withdrawn"); err != nil {
		t.Fatal(err)
	}
	if err := f.CloseQuestion(q2.ID, "terminal"); err == nil {
		t.Fatal("a closed question closed again")
	}
	if err := f.CloseQuestion(q1.ID, "bogus"); err == nil {
		t.Fatal("an unknown reason accepted")
	}
	got := f.Questions()
	if got[0].State != "answered" || got[0].Answer != nil || got[0].AnsweredVia == nil || *got[0].AnsweredVia != "terminal" {
		t.Fatalf("terminal: %+v", got[0])
	}
	if got[1].State != "cancelled" || got[1].AnsweredVia == nil || *got[1].AnsweredVia != "agent" {
		t.Fatalf("withdrawn: %+v", got[1])
	}
	r := call(t, f, "POST", "/v1/projects/"+p.ID+"/questions/"+q1.ID+"/answer", tok, map[string]any{"answer": "4173"})
	if r.status != 409 || r.errCode(t) != "conflict" {
		t.Fatalf("answer after terminal: %d %s", r.status, r.body)
	}
}
