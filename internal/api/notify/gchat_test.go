package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/notify"
)

// A webhook URL with a key and token that must never show up anywhere
// but the request to Google.
const (
	hookKey   = "AIzaSyDdI0hCZtE6vySjMm-WEfRq3CPzqKqqsHI"
	hookToken = "s3cr3tT0k3n-xyz_123%3D"
	hookSpace = "AAAAx1yZ"
	hookURL   = "https://chat.googleapis.com/v1/spaces/" + hookSpace + "/messages?key=" + hookKey + "&token=" + hookToken
)

func TestCanonicalGChatURL(t *testing.T) {
	good := map[string]string{
		hookURL: "https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=" + hookKey + "&token=s3cr3tT0k3n-xyz_123%3D",
		// A copied URL with a thread and spaces round it keeps only its key and token.
		"  https://chat.googleapis.com:443/v1/spaces/AAAAx1yZ/messages?token=t&key=k&threadKey=x&messageReplyOption=REPLY_MESSAGE_OR_FAIL \n": "https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
		"https://CHAT.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&token=t":                                                               "https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
	}
	for in, want := range good {
		got, space, err := notify.CanonicalGChatURL(in)
		if err != nil || got != want || space != hookSpace {
			t.Errorf("%q: %q %q %v, want %q", in, got, space, err, want)
		}
	}
	for _, in := range []string{
		"",
		"http://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
		"https://chat.googleapis.com.evil.example/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
		"https://evil.example/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
		"https://user:pw@chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
		"https://chat.googleapis.com:8443/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
		"https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k",
		"https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?token=t",
		"https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=&token=t",
		"https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&key=k2&token=t",
		"https://chat.googleapis.com/v1/spaces/AAAA/../x/messages?key=k&token=t",
		"https://chat.googleapis.com/v1/spaces/AAAAx1yZ/members?key=k&token=t",
		"https://chat.googleapis.com/v1/spaces//messages?key=k&token=t",
		"https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&token=t#frag",
		"chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=k&token=t",
	} {
		if got, _, err := notify.CanonicalGChatURL(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
	if notify.GChatSpace(hookURL) != hookSpace || notify.GChatSpace("nonsense") != "" {
		t.Error("GChatSpace")
	}
}

// toServer sends every request to srv, keeping the original URL for the
// test to read.
type toServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []*url.URL
}

func (t *toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	u := *r.URL
	t.seen = append(t.seen, &u)
	t.mu.Unlock()
	target, _ := url.Parse(t.srv.URL)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host, r2.Host = target.Scheme, target.Host, target.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func gchatClient(srv *httptest.Server) (*http.Client, *toServer) {
	tr := &toServer{srv: srv}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return notify.ErrRedirect }}, tr
}

func noSecret(t *testing.T, what, s string) {
	t.Helper()
	for _, secret := range []string{hookKey, hookToken, "s3cr3tT0k3n", "chat.googleapis.com"} {
		if strings.Contains(s, secret) {
			t.Errorf("%s carries the webhook URL: %q", what, s)
		}
	}
}

func gchatMsg() notify.Message {
	return notify.Message{EventID: uuid.MustParse("01a0c14a-0000-7000-8000-000000000001"), Kind: "agent_message", Agent: "claude", Project: "todo-app", Summary: "Deploy to staging is green.", GChatURL: hookURL, Dashboard: dash, ProjectID: fixtureProject}
}

// The sender's answers to each status: 200 is sent, 429 and 5xx are retried, 401, 403 and 404 mark
// the channel broken, 400 fails the row, a redirect is refused. No error
// carries the URL.
func TestGChatSendStatuses(t *testing.T) {
	for _, tc := range []struct {
		status    int
		ok, perm  bool
		broken    bool
		redirects bool
	}{
		{status: 200, ok: true},
		{status: 429},
		{status: 500},
		{status: 503},
		{status: 400, perm: true},
		{status: 401, perm: true, broken: true},
		{status: 403, perm: true, broken: true},
		{status: 404, perm: true, broken: true},
		{status: 302, perm: true, redirects: true},
	} {
		var body []byte
		var ctype string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ = io.ReadAll(r.Body)
			ctype = r.Header.Get("Content-Type")
			if tc.redirects {
				http.Redirect(w, r, "https://elsewhere.example/", tc.status)
				return
			}
			w.WriteHeader(tc.status)
		}))
		c, tr := gchatClient(srv)
		err := (&notify.GChat{HTTP: c, Pace: time.Millisecond}).Send(context.Background(), gchatMsg())
		srv.Close()
		if (err == nil) != tc.ok {
			t.Errorf("%d: err %v", tc.status, err)
			continue
		}
		var perm notify.Permanent
		var broken *notify.ErrChannelBroken
		if err != nil {
			noSecret(t, "error", err.Error())
			if errors.As(err, &perm) != tc.perm || errors.As(err, &broken) != tc.broken || errors.Is(err, notify.ErrRedirect) != tc.redirects {
				t.Errorf("%d: %v: permanent=%v broken=%v", tc.status, err, errors.As(err, &perm), errors.As(err, &broken))
			}
		}
		if tc.status != 200 {
			continue
		}
		q := tr.seen[0].Query()
		if tr.seen[0].Host != "chat.googleapis.com" || tr.seen[0].Path != "/v1/spaces/"+hookSpace+"/messages" || q.Get("key") != hookKey || q.Get("token") != "s3cr3tT0k3n-xyz_123=" {
			t.Errorf("target %v", tr.seen[0].Redacted())
		}
		if q.Get("threadKey") != "e-01a0c14a-0000-7000-8000-000000000001" || q.Get("messageReplyOption") != "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD" || q.Get("requestId") != "01a0c14a-0000-7000-8000-000000000001" {
			t.Errorf("query %v", q)
		}
		if ctype != "application/json; charset=UTF-8" || !json.Valid(body) || !strings.Contains(string(body), `"cardsV2"`) || !strings.Contains(string(body), `"fallbackText":"todo-app: claude says: Deploy to staging is green."`) {
			t.Errorf("body %s %s", ctype, body)
		}
		noSecret(t, "body", string(body))
	}
	// No answer at all: the error names what happened, not where.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c, _ := gchatClient(srv)
	srv.Close()
	err := (&notify.GChat{HTTP: c, Pace: time.Millisecond}).Send(context.Background(), gchatMsg())
	if err == nil {
		t.Fatal("a closed server answered")
	}
	noSecret(t, "dial error", err.Error())
	// No URL: permanent, without a request.
	m := gchatMsg()
	m.GChatURL = ""
	if err := (&notify.GChat{}).Send(context.Background(), m); !errors.As(err, new(notify.Permanent)) {
		t.Fatalf("no url: %v", err)
	}
}

// Tenant text cannot add a link, a mention or markup: it is escaped in
// the card, and the fallback carries it as plain text.
func TestGChatEscapesTenantText(t *testing.T) {
	m := gchatMsg()
	m.Summary = `<https://evil.example|click here> <users/all> <b>bold</b> & "q"`
	m.Project = "<i>todo</i>"
	b, err := json.Marshal(notify.RenderGChat(m))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		CardsV2 []struct {
			Card struct {
				Header   struct{ Title string }
				Sections []struct {
					Widgets []struct {
						TextParagraph *struct{ Text string }
					}
				}
			}
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	text := got.CardsV2[0].Card.Sections[0].Widgets[0].TextParagraph.Text
	if strings.ContainsAny(text, "<>") || !strings.Contains(text, "&lt;https://evil.example|click here&gt;") || !strings.Contains(text, "&lt;users/all&gt;") || !strings.Contains(text, "&amp;") {
		t.Fatalf("paragraph not escaped: %q", text)
	}
	if got.CardsV2[0].Card.Header.Title != "<i>todo</i>" {
		t.Fatalf("header %q", got.CardsV2[0].Card.Header.Title)
	}
}

// Two posts to one space are at least the pace apart; another space does
// not wait.
func TestGChatPacesPerSpace(t *testing.T) {
	var mu sync.Mutex
	var at []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
	}))
	defer srv.Close()
	c, _ := gchatClient(srv)
	g := &notify.GChat{HTTP: c, Pace: 300 * time.Millisecond}
	m := gchatMsg()
	m.GChatURL = strings.Replace(hookURL, hookSpace, "PaceSpace1", 1)
	start := time.Now()
	for i := 0; i < 2; i++ {
		if err := g.Send(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	if at[1].Sub(at[0]) < 290*time.Millisecond {
		t.Fatalf("two posts %v apart", at[1].Sub(at[0]))
	}
	other := m
	other.GChatURL = strings.Replace(hookURL, hookSpace, "PaceSpace2", 1)
	t0 := time.Now()
	if err := g.Send(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) > 250*time.Millisecond {
		t.Fatalf("another space waited %v (total %v)", time.Since(t0), time.Since(start))
	}
	if notify.GChatPace < 1100*time.Millisecond {
		t.Fatalf("default pace %v", notify.GChatPace)
	}
}

// gchatFixtures are the kinds that reach Google Chat: agent and platform
// events, the user notice, and the reply that closes a question.
func gchatFixtures() map[string]notify.Message {
	fx := fixtures()
	out := map[string]notify.Message{}
	for _, k := range []string{"agent_message", "needs_input", "error", "snapshot_failed", "billing_stopped", "notifications_paused", "machines_restarted"} {
		m := fx[k]
		m.EventID = uuid.MustParse("01a0c14a-0000-7000-8000-0000000000aa")
		out[k] = m
	}
	q := fx["agent_question"]
	q.EventID = uuid.MustParse("01a0c14a-0000-7000-8000-0000000000ab")
	q.TZ = "Europe/Berlin"
	q.Question.Replies = []string{
		"https://api.repose.herakraft.co/v1/questions/reply?token=aaa.bbb&via=gchat",
		"https://api.repose.herakraft.co/v1/questions/reply?token=ccc.ddd&via=gchat",
	}
	out["agent_question"] = q
	free := q
	free.Question = &notify.QuestionLinks{ID: q.Question.ID, Expires: q.Question.Expires}
	free.Summary = "Which branch should the release come from?"
	out["agent_question_free"] = free
	done := fx["agent_message"]
	done.Kind = "agent_done"
	done.EventID = uuid.MustParse("01a0c14a-0000-7000-8000-0000000000ac")
	done.Summary = "Login survives a token refresh. git fetch repose fix-auth"
	out["agent_done"] = done
	out["question_closed"] = notify.Message{Kind: notify.QuestionClosedKind, Summary: "Answered yes from the dashboard", Dashboard: dash, Question: &notify.QuestionLinks{ID: q.Question.ID}}
	return out
}

// TestGChatGolden pins the card of each kind; -update rewrites them.
func TestGChatGolden(t *testing.T) {
	fx := gchatFixtures()
	names := make([]string, 0, len(fx))
	for k := range fx {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, kind := range names {
		m := fx[kind]
		b, err := json.MarshalIndent(notify.RenderGChat(m), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got := string(b) + "\n" + "threadKey: " + notify.GChatThread(m) + "\n"
		if strings.ContainsAny(got, "!—–") {
			t.Errorf("%s: an exclamation mark or a dash in the copy", kind)
		}
		path := filepath.Join("testdata", kind+".gchat")
		if *updateGolden {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update)", kind, err)
		}
		if string(want) != got {
			t.Errorf("%s.gchat differs from the golden file (run with -update and read the diff)\n--- got ---\n%s", kind, got)
		}
	}
}
