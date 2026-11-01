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
// report with `--link`: a Chat button named label and host before Open
// project, an ntfy action, the email's main button; and without `--link`
// an address in the text gets no button.
func TestAgentLinkOnEveryChannel(t *testing.T) {
	pid := uuid.New()
	link, _ := msglink.Check("http://localhost:8742/out/compare-simple.html", "Compare resumes")
	m := notify.Message{Kind: "agent_done", Agent: "claude", Project: "job", Summary: "Simple resume done.", Dashboard: "https://d", ProjectID: pid, Link: link}

	b, _ := json.Marshal(notify.RenderGChat(m))
	card := string(b)
	at, open := strings.Index(card, `"Compare resumes · localhost:8742"`), strings.Index(card, `"Open project"`)
	if at < 0 || open < at || !strings.Contains(card, `"url":"http://localhost:8742/out/compare-simple.html"`) {
		t.Fatalf("card %s", card)
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
	if !strings.Contains(r.Text, "Compare resumes · localhost:8742: "+link.URL) || !strings.Contains(r.Text, "Open job: https://d/projects/"+pid.String()) {
		t.Fatalf("email text:\n%s", r.Text)
	}

	// No --link: an address in the text stays text.
	plain := m
	plain.Link = msglink.Link{}
	plain.Summary = "open http://localhost:8742/out/compare-simple.html"
	b, _ = json.Marshal(notify.RenderGChat(plain))
	if strings.Contains(string(b), "localhost:8742\"") || strings.Count(string(b), `"openLink"`) != 1 {
		t.Fatalf("a link guessed from the text: %s", b)
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
}
