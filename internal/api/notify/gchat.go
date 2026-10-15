package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/eventverbs"
)

// Google Chat is the third channel (DECISIONS I-692): a space's incoming
// webhook, which posts one cardsV2 card per notification. The webhook URL
// carries the key and token that let anyone post to the space, so it is a
// credential: it is stored in one canonical form, sent to Google only, and
// never logged, returned by the api or written into an error.

// gchatHost is the only host a webhook URL may name.
const gchatHost = "chat.googleapis.com"

var gchatPath = regexp.MustCompile(`^/v1/spaces/([A-Za-z0-9_-]+)/messages$`)

// ErrGChatURL is CanonicalGChatURL's answer to anything but a webhook URL.
var ErrGChatURL = errors.New("gchat_url must be a Google Chat webhook URL (https://chat.googleapis.com/v1/spaces/.../messages?key=...&token=...)")

// CanonicalGChatURL checks raw is a Google Chat incoming webhook and
// returns it rebuilt from its parts (https, the fixed host, the space's
// path, key and token, nothing else: a pasted threadKey is dropped) and
// the space id.
func CanonicalGChatURL(raw string) (canonical, space string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", "", ErrGChatURL
	}
	if strings.ToLower(u.Hostname()) != gchatHost || (u.Port() != "" && u.Port() != "443") {
		return "", "", ErrGChatURL
	}
	m := gchatPath.FindStringSubmatch(u.EscapedPath())
	if m == nil {
		return "", "", ErrGChatURL
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["key"]) != 1 || len(q["token"]) != 1 || q.Get("key") == "" || q.Get("token") == "" {
		return "", "", ErrGChatURL
	}
	c := url.Values{"key": {q.Get("key")}, "token": {q.Get("token")}}
	return "https://" + gchatHost + "/v1/spaces/" + m[1] + "/messages?" + c.Encode(), m[1], nil
}

// GChatSpace is the space id of a stored webhook URL, or "".
func GChatSpace(stored string) string {
	_, space, err := CanonicalGChatURL(stored)
	if err != nil {
		return ""
	}
	return space
}

// GChatPace is the least time between two posts to one space: Google
// allows one a second per space, shared by every webhook in it.
const GChatPace = 1100 * time.Millisecond

// gchatPacer spaces posts per space across every GChat sender of the
// process (the outbox and POST /me/notify-test share it).
var gchatPacer = &pacer{next: map[string]time.Time{}}

type pacer struct {
	mu   sync.Mutex
	next map[string]time.Time
}

// wait reserves the next slot for key and sleeps until it.
func (p *pacer) wait(ctx context.Context, key string, now time.Time, gap time.Duration) error {
	p.mu.Lock()
	at := p.next[key]
	if at.Before(now) {
		at = now
	}
	p.next[key] = at.Add(gap)
	for k, t := range p.next { // a space idle for a while needs no entry
		if t.Before(now) {
			delete(p.next, k)
		}
	}
	p.mu.Unlock()
	d := at.Sub(now)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ErrChannelBroken is a webhook Google refused with 401, 403 or 404: it
// was deleted, its space removed it, or the space is gone. The outbox
// marks the channel broken and stops delivering to it (I-692).
type ErrChannelBroken struct{ Code int }

func (e *ErrChannelBroken) Error() string { return fmt.Sprintf("gchat: status %d", e.Code) }

// GChat posts to the user's Google Chat webhook.
type GChat struct {
	HTTP *http.Client
	// Pace is the least gap between posts to one space; 0 is GChatPace.
	Pace time.Duration
}

// gchatClient is the default client, built once so connections are reused.
var gchatClient = sync.OnceValue(func() *http.Client { return PublicClient(15 * time.Second) })

// Send implements Sender.
func (g *GChat) Send(ctx context.Context, m Message) error {
	canonical, space, err := CanonicalGChatURL(m.GChatURL)
	if err != nil {
		return Permanent{errors.New("gchat: no webhook url")}
	}
	body, err := json.Marshal(RenderGChat(m))
	if err != nil {
		return Permanent{err}
	}
	// No requestId: Google refuses it on an incoming webhook with
	// `400 Request Id is not supported for webhooks.`, which made every
	// Chat message fail (DECISIONS I-731, amends I-692). I-692 took that
	// parameter from the REST API's docs and said in as many words that
	// it had never been sent to a real webhook. threadKey still groups a
	// project's messages, so a retry after a lost answer can post a
	// second message into the same thread; that is the cost of the
	// parameter being unavailable here.
	target := canonical + "&" + url.Values{
		"threadKey":          {GChatThread(m)},
		"messageReplyOption": {"REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"},
	}.Encode()
	pace := g.Pace
	if pace == 0 {
		pace = GChatPace
	}
	if err := gchatPacer.wait(ctx, space, time.Now(), pace); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return Permanent{errors.New("gchat: bad request")}
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	client := g.HTTP
	if client == nil {
		client = gchatClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error's text is the whole URL, key and token included:
		// keep only what went wrong.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		for _, refused := range []error{ErrNonPublicAddress, ErrRedirect} {
			if errors.Is(err, refused) {
				return Permanent{fmt.Errorf("gchat: %w", refused)}
			}
		}
		return fmt.Errorf("gchat: %w", err)
	}
	_ = resp.Body.Close() // status is all we need
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return &statusError{"gchat", resp.StatusCode}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
		return Permanent{&ErrChannelBroken{resp.StatusCode}}
	case resp.StatusCode >= 400:
		return Permanent{&statusError{"gchat", resp.StatusCode}}
	}
	return nil
}

// GChatThread is the threadKey of a message: a question's thread, which
// its close reply joins, or one thread per event, so each notification
// alerts like a new message (a reply in a thread the owner does not
// follow may not).
func GChatThread(m Message) string {
	if m.Question != nil {
		return "q-" + m.Question.ID.String()
	}
	if m.EventID != uuid.Nil {
		return "e-" + m.EventID.String()
	}
	return "t-" + uuid.NewString()
}

// QuestionClosedKind is the Google Chat reply that says how a question
// closed, posted in the question's thread (events.QuestionClosedKind).
const QuestionClosedKind = events.QuestionClosedKind

// gchat* are the JSON shapes of a cardsV2 message.
type gchatMessage struct {
	FallbackText string      `json:"fallbackText"`
	CardsV2      []gchatCard `json:"cardsV2"`
}

type gchatCard struct {
	CardID string        `json:"cardId"`
	Card   gchatCardBody `json:"card"`
}

type gchatCardBody struct {
	Header   *gchatHeader   `json:"header,omitempty"`
	Sections []gchatSection `json:"sections"`
}

type gchatHeader struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

type gchatSection struct {
	Widgets []map[string]any `json:"widgets"`
}

type gchatButton struct {
	Text    string       `json:"text"`
	Type    string       `json:"type,omitempty"`
	OnClick gchatOnClick `json:"onClick"`
}

type gchatOnClick struct {
	OpenLink struct {
		URL string `json:"url"`
	} `json:"openLink"`
}

func button(text, typ, link string) gchatButton {
	b := gchatButton{Text: text, Type: typ}
	b.OnClick.OpenLink.URL = link
	return b
}

// gchatPlain is text Google Chat shows as written: a card header and a
// fallbackText are plain, but drop control characters all the same.
func gchatPlain(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' {
			return ' '
		}
		return r
	}, s)
	if len(s) > n {
		cut := n
		for cut > 0 && (s[cut]&0xC0) == 0x80 {
			cut--
		}
		s = s[:cut]
	}
	return s
}

// RenderGChat is a message as one cardsV2 card plus the plain fallbackText
// a phone shows in its notification. Whatever a tenant wrote is escaped
// into a textParagraph, which reads a small HTML subset, so a summary
// cannot add a link or markup. No emoji.
func RenderGChat(m Message) any {
	head, body := gchatHeading(m)
	text := html.EscapeString(strings.TrimSpace(body))
	text = strings.ReplaceAll(text, "\n", "<br>")
	var widgets []map[string]any
	if text != "" {
		widgets = append(widgets, map[string]any{"textParagraph": map[string]any{"text": text}})
	}
	if q := m.Question; q != nil && m.Kind != QuestionClosedKind && !q.Expires.IsZero() {
		loc := m.Location()
		widgets = append(widgets, map[string]any{"textParagraph": map[string]any{"text": html.EscapeString("Open until " + expiryAt(m.At, q.Expires, loc))}})
	}
	if bs := gchatButtons(m); len(bs) > 0 {
		widgets = append(widgets, map[string]any{"buttonList": map[string]any{"buttons": bs}})
	}
	if len(widgets) == 0 {
		widgets = append(widgets, map[string]any{"textParagraph": map[string]any{"text": html.EscapeString(head.Title)}})
	}
	card := gchatCardBody{Sections: []gchatSection{{Widgets: widgets}}}
	if m.Kind != QuestionClosedKind {
		card.Header = head
	}
	// fallbackText is what a phone's notification shows: the heading
	// once, and no run of colons.
	b := strings.TrimSpace(body)
	fallback := head.Title
	switch {
	case m.Kind == QuestionClosedKind:
		fallback = b
	case head.Subtitle != "" && b != "" && m.Agent == "":
		// A platform subtitle may carry its own colon ("Your machines
		// were stopped: a payment failed"), so the body takes a new line.
		fallback += ": " + head.Subtitle + "\n" + b
	case head.Subtitle != "":
		fallback += ": " + head.Subtitle
		if b != "" {
			fallback += ": " + b
		}
	case strings.HasPrefix(b, Subject(m)):
		fallback = b // the notice opens with its own subject
	case b != "":
		fallback += ": " + b
	}
	return gchatMessage{FallbackText: gchatPlain(fallback, 400), CardsV2: []gchatCard{{CardID: "repose", Card: card}}}
}

// gchatHeading is the card's title and subtitle and the body text.
func gchatHeading(m Message) (*gchatHeader, string) {
	switch {
	case m.Kind == QuestionClosedKind:
		return &gchatHeader{Title: gchatPlain(m.Project, 200)}, m.Summary
	case events.UserNoticeKinds[m.Kind] || events.AccountKinds[m.Kind] || m.Project == "":
		title := Subject(m)
		if strings.HasPrefix(strings.TrimSpace(m.Summary), title) {
			// The notice opens with its subject; the header takes the
			// email's shorter heading so the card does not say it twice.
			if h := Heading(m); h != "" {
				title = h
			}
		}
		return &gchatHeader{Title: gchatPlain(title, 200)}, m.Summary
	case m.Agent != "":
		return &gchatHeader{Title: gchatPlain(m.Project, 200), Subtitle: gchatPlain(m.Agent+" "+gchatVerb(m.Kind), 200)}, m.Summary
	}
	if s, ok := platformSubjects[m.Kind]; ok {
		return &gchatHeader{Title: gchatPlain(m.Project, 200), Subtitle: gchatPlain(s, 200)}, m.Summary
	}
	return &gchatHeader{Title: gchatPlain(m.Project, 200), Subtitle: gchatPlain(eventverbs.Verb(m.Kind), 200)}, m.Summary
}

// gchatVerb is what an agent did, as the card's subtitle says it.
func gchatVerb(kind string) string {
	if kind == "agent_done" {
		if _, ok := eventverbs.Verbs[kind]; !ok {
			return "is done"
		}
	}
	return eventverbs.Verb(kind)
}

// gchatButtons are the card's openLink buttons: one per option of a
// question, each to its signed reply link (whose GET shows a confirm page,
// so a link preview answers nothing); "Answer" to the project for a
// question without reply links; "Dashboard" for a notice with no project;
// else "Open project". A close reply has none: its thread holds the card.
func gchatButtons(m Message) []gchatButton {
	switch {
	case m.Kind == QuestionClosedKind:
		return nil
	case m.Question != nil && len(m.Question.Options) > 0 && len(m.Question.Replies) == len(m.Question.Options):
		var bs []gchatButton
		for i, o := range m.Question.Options {
			bs = append(bs, button(gchatPlain(o, 64), "OUTLINED", m.Question.Replies[i]))
		}
		return bs
	case m.Question != nil:
		return []gchatButton{button("Answer", "OUTLINED", m.ProjectURL())}
	case events.UserNoticeKinds[m.Kind] || m.ProjectID == uuid.Nil:
		return []gchatButton{button("Dashboard", "OUTLINED", strings.TrimRight(m.Dashboard, "/")+"/projects")}
	}
	return []gchatButton{button("Open project", "OUTLINED", m.ProjectURL())}
}
