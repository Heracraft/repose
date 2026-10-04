package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/notify"
)

func signer(t *testing.T) *notify.Unsubscriber {
	t.Helper()
	u, err := notify.LoadOrCreateUnsubscriber(context.Background(), newFakePlatformSecrets())
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestReplyTokenRoundTripExpiryAndDomain(t *testing.T) {
	u := signer(t)
	id := uuid.New()
	exp := time.Now().Add(time.Hour)
	tok := u.ReplyToken(id, 2, exp)
	gotID, opt, err := u.VerifyReply(tok, time.Now())
	if err != nil || gotID != id || opt != 2 {
		t.Fatalf("round trip: %v %v %v", gotID, opt, err)
	}
	if _, _, err := u.VerifyReply(tok, exp.Add(2*time.Second)); !errors.Is(err, notify.ErrReplyExpired) {
		t.Fatalf("after expiry: %v", err)
	}
	// Another key does not verify it.
	if _, _, err := signer(t).VerifyReply(tok, time.Now()); err == nil {
		t.Fatal("a token verified under another key")
	}
	// The option is inside the signature: changing it breaks the token.
	p, s, _ := strings.Cut(tok, ".")
	other, _, _ := strings.Cut(u.ReplyToken(id, 0, exp), ".")
	if _, _, err := u.VerifyReply(other+"."+s, time.Now()); err == nil {
		t.Fatal("a token with a swapped option verified")
	}
	_ = p
	// An unsubscribe token is not a reply token, though the key is shared.
	if _, _, err := u.VerifyReply(u.Sign(id, exp), time.Now()); err == nil {
		t.Fatal("an unsubscribe token verified as a reply")
	}
	if url := u.ReplyURL("https://api.test/", id, 1, exp, "email"); !strings.HasPrefix(url, "https://api.test/v1/questions/reply?token=") || !strings.HasSuffix(url, "&via=email") {
		t.Fatalf("url %q", url)
	}
}

// TestNtfyQuestionHasButtons: an agent_question with options carries ntfy's
// JSON action list, one http POST button per option, ASCII-safe; without
// options it carries one view button to the project.
func TestNtfyQuestionHasButtons(t *testing.T) {
	var actions, click, prio, title string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions, click, prio, title = r.Header.Get("Actions"), r.Header.Get("Click"), r.Header.Get("Priority"), r.Header.Get("Title")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pid := uuid.New()
	m := notify.Message{Kind: "agent_question", Agent: "codex", Project: "todo", Summary: "Ship it?", NtfyURL: srv.URL + "/t", Dashboard: "https://d", ProjectID: pid,
		Question: &notify.QuestionLinks{ID: uuid.New(), Options: []string{"yes, ship", "nö"}, Replies: []string{"https://a/1", "https://a/2"}, Expires: time.Now().Add(time.Hour)}}
	if err := (&notify.Ntfy{HTTP: srv.Client()}).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if title != "todo: codex asks" || prio != "5" || click != "https://d/projects/"+pid.String() {
		t.Fatalf("title %q prio %q click %q", title, prio, click)
	}
	for _, r := range actions {
		if r > 0x7e {
			t.Fatalf("Actions header is not ASCII: %q", actions)
		}
	}
	var acts []map[string]any
	if err := json.Unmarshal([]byte(actions), &acts); err != nil {
		t.Fatalf("Actions %q: %v", actions, err)
	}
	if len(acts) != 2 || acts[0]["action"] != "http" || acts[0]["label"] != "yes, ship" || acts[0]["url"] != "https://a/1" || acts[0]["method"] != "POST" || acts[0]["clear"] != true || acts[1]["label"] != "nö" {
		t.Fatalf("actions %v", acts)
	}
	m.Question.Options, m.Question.Replies = nil, nil
	if err := (&notify.Ntfy{HTTP: srv.Client()}).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(actions), &acts)
	if len(acts) != 1 || acts[0]["action"] != "view" || acts[0]["url"] != "https://d/projects/"+pid.String() {
		t.Fatalf("free-text actions %v", acts)
	}
	// A message is a plain notification.
	if err := (&notify.Ntfy{HTTP: srv.Client()}).Send(context.Background(), notify.Message{Kind: "agent_message", Agent: "shell", Project: "todo", Summary: "deploy green", NtfyURL: srv.URL + "/t", Dashboard: "https://d"}); err != nil {
		t.Fatal(err)
	}
	if title != "todo: shell says" || prio != "3" || actions != "" {
		t.Fatalf("message: title %q prio %q actions %q", title, prio, actions)
	}
}

func TestEmailQuestionHasReplyLinks(t *testing.T) {
	var subject, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Subject string `json:"subject"`
			Text    string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		subject, body = p.Subject, p.Text
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pid := uuid.New()
	exp := time.Date(2026, 9, 24, 18, 30, 0, 0, time.UTC)
	m := notify.Message{Kind: "agent_question", Agent: "claude", Project: "todo-app", Summary: "Drop the legacy table?", Email: "u@example.com", Dashboard: "https://dash", ProjectID: pid,
		Question: &notify.QuestionLinks{ID: uuid.New(), Options: []string{"yes", "no"}, Replies: []string{"https://api/v1/questions/reply?token=a&via=email", "https://api/v1/questions/reply?token=b&via=email"}, Expires: exp}}
	if err := (&notify.Email{APIKey: "k", URL: srv.URL}).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if subject != "[repose] todo-app: claude asks" {
		t.Fatalf("subject %q", subject)
	}
	for _, want := range []string{"Drop the legacy table?", "yes: https://api/v1/questions/reply?token=a&via=email", "no: https://api/v1/questions/reply?token=b&via=email",
		"https://dash/projects/" + pid.String(), "repose reply todo-app", "2026-09-24 18:30 UTC"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
}
