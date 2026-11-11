package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/msglink"
)

// TestAgentLinkOnEveryChannel (DECISIONS I-931): the screenshot's done
// report with `--link`: a Chat button named label and host, an ntfy
// action, the email's main button; and without `--link` an address in
// the text gets no button. Since I-1096 the link is the only button a
// done report carries: nothing leads back to the project page.
func TestAgentLinkOnEveryChannel(t *testing.T) {
	pid := uuid.New()
	link, _ := msglink.Check("http://localhost:8742/out/compare-simple.html", "Compare resumes")
	m := notify.Message{Kind: "agent_done", Agent: "claude", Project: "job", Summary: "Simple resume done.", Dashboard: "https://d", ProjectID: pid, Link: link}

	b, _ := json.Marshal(notify.RenderGChat(m))
	card := string(b)
	if !strings.Contains(card, `"Compare resumes · localhost:8742"`) || !strings.Contains(card, `"url":"http://localhost:8742/out/compare-simple.html"`) {
		t.Fatalf("card %s", card)
	}
	if strings.Contains(card, `"Open project"`) || strings.Count(card, `"openLink"`) != 1 {
		t.Fatalf("a done report carries the agent's link and nothing else (I-1096): %s", card)
	}

	var actions, click string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions, click = r.Header.Get("Actions"), r.Header.Get("Click")
	}))
	defer srv.Close()
	m.NtfyURL = srv.URL + "/t"
	if err := (&notify.Ntfy{HTTP: srv.Client()}).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var acts []map[string]any
	if err := json.Unmarshal([]byte(actions), &acts); err != nil || len(acts) != 1 || acts[0]["url"] != link.URL || click != "https://d/projects/"+pid.String() {
		t.Fatalf("ntfy actions %q click %q", actions, click)
	}
	if !strings.Contains(actions, "Compare resumes \\u00b7 localhost:8742") {
		t.Fatalf("ntfy label %q", actions)
	}

	r, err := notify.Render(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text, "Compare resumes · localhost:8742: "+link.URL) {
		t.Fatalf("email text, the link as the main button:\n%s", r.Text)
	}
	if strings.Contains(r.Text, "https://d/projects/"+pid.String()) || !strings.Contains(r.HTML, link.URL) {
		t.Fatalf("the project page is no button on a done report (I-1096):\n%s", r.Text)
	}

	// No --link: an address in the text stays text.
	plain := m
	plain.Link = msglink.Link{}
	plain.Summary = "open http://localhost:8742/out/compare-simple.html"
	b, _ = json.Marshal(notify.RenderGChat(plain))
	if strings.Contains(string(b), "localhost:8742\"") || strings.Contains(string(b), `"openLink"`) {
		t.Fatalf("a link guessed from the text, or a way in nobody needs: %s", b)
	}
	if err := (&notify.Ntfy{HTTP: srv.Client()}).Send(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	if actions != "" {
		t.Fatalf("ntfy actions without --link: %q", actions)
	}

	// A question keeps its answers first; three options leave ntfy no room.
	q := m
	q.Kind = "agent_question"
	q.Question = &notify.QuestionLinks{ID: uuid.New(), Options: []string{"a", "b", "c"}, Replies: []string{"https://r/1", "https://r/2", "https://r/3"}}
	b, _ = json.Marshal(notify.RenderGChat(q))
	if s := string(b); strings.Index(s, `"Compare resumes · localhost:8742"`) < strings.Index(s, `"c"`) {
		t.Fatalf("question card %s", s)
	}
	if err := (&notify.Ntfy{HTTP: srv.Client()}).Send(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(actions, "localhost") {
		t.Fatalf("question ntfy actions %q", actions)
	}
	// The question's email: its answers stay the buttons, the link is a
	// plain link under the question.
	r, err = notify.Render(q)
	if err != nil {
		t.Fatal(err)
	}
	about, answers := strings.Index(r.Text, "Compare resumes · localhost:8742: "), strings.Index(r.Text, "Answer with one click")
	if about < 0 || answers < about || strings.Contains(r.HTML, ">Compare resumes · localhost:8742</a></td>") {
		t.Fatalf("question email:\n%s", r.Text)
	}
}

// A long label gives way to the host on a Chat button (64 bytes).
func TestChatButtonKeepsTheHost(t *testing.T) {
	link, _ := msglink.Check("https://aaaaaaaaaaaaaaaaaaaaaaaaaaaa.evil.example/", "Sign in at the accounts google page now")
	b, _ := json.Marshal(notify.RenderGChat(notify.Message{Kind: "agent_message", Agent: "claude", Project: "p", Summary: "x", Dashboard: "https://d", ProjectID: uuid.New(), Link: link}))
	if !strings.Contains(string(b), " · aaaaaaaaaaaaaaaaaaaaaaaaaaaa.evil.example\"") {
		t.Fatalf("card %s", b)
	}
}
