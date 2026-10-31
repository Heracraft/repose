package notify

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"math"
	"path"
	"strconv"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"

	"github.com/heracraft/repose/internal/api/events"
)

// Every email is one HTML layout and one text layout, filled from a
// per-kind partial (DECISIONS I-291). The partial is a text/template that
// defines `heading`, `body` (paragraphs separated by a blank line) and,
// where the kind has them, `link_label` and `link_url` (the primary
// button), `command` and `command_label` (the mono row), `buttons_label`
// (an agent_question's reply buttons), `quiet_label` and `quiet_url` (a
// plain link under the buttons), `meta` (a grey line under the heading),
// `quote` (tenant text on a sunken block), `preheader` (the inbox
// preview), `lines` (small print, one a line) and `steps` (title and
// command, alternating lines). The partials
// produce plain strings; the HTML layout is html/template, so whatever a
// tenant wrote (a summary, a project name, a question) is escaped when it
// is placed, never before.
//
//go:embed templates/layout.html templates/layout.txt templates/kinds/*.tmpl
var templateFS embed.FS

// content is what the two layouts render.
type content struct {
	Subject      string
	Preheader    string
	Heading      string
	Meta         string
	Quote        string
	Paragraphs   []string
	Steps        []step
	CommandLabel string
	Command      string
	ButtonsLabel string
	Buttons      []link
	Primary      *link
	Quiet        *link
	Lines        []string
	Dashboard    string
	Unsubscribe  string
	// Why is the footer's reason for the email, beside the unsubscribe
	// link: the partial's `why` block, or the agent sentence.
	Why string
}

type step struct {
	N       int
	Title   string
	Command string
}

type link struct {
	Label string
	URL   string
}

// Meta is the grey line under a question's or a done report's heading:
// agent, window, when it was asked and, for a question, until when it is
// open, in the user's zone ("claude · main · asked 28 Sep 18:00 UTC · open
// until 18:30 UTC"; UTC when none is set).
func (d kindData) Meta() string {
	var parts []string
	if d.M.Agent != "" {
		parts = append(parts, d.M.Agent)
	}
	if d.M.Window != "" {
		parts = append(parts, d.M.Window)
	}
	loc := d.M.Loc
	if loc == nil {
		loc = time.UTC
	}
	at := d.M.At.In(loc)
	if !d.M.At.IsZero() {
		if d.M.Question != nil {
			parts = append(parts, "asked "+at.Format("2 Jan 15:04 MST"))
		} else {
			parts = append(parts, at.Format("2 Jan 15:04 MST"))
		}
	}
	if q := d.M.Question; q != nil && !q.Expires.IsZero() {
		parts = append(parts, "open until "+expiryAt(d.M.At, q.Expires, loc))
	}
	return strings.Join(parts, " · ")
}

// expiryAt is a question's expiry in the owner's zone, as the email and
// the Google Chat card both print it: with the date when it falls on
// another day than the ask, or the ask's time is unknown.
func expiryAt(asked, expires time.Time, loc *time.Location) string {
	exp := expires.In(loc)
	at := asked.In(loc)
	if !asked.IsZero() && exp.YearDay() == at.YearDay() && exp.Year() == at.Year() {
		return exp.Format("15:04 MST")
	}
	return exp.Format("2 Jan 15:04 MST")
}

// Rendered is one email, ready for Resend.
type Rendered struct {
	Subject string
	HTML    string
	Text    string
}

// kindData is what a kind partial sees.
type kindData struct {
	M          Message
	P          payload
	Title      string
	Dashboard  string
	Billing    string
	ProjectURL string
}

// payload is the account kinds' summary: a small JSON object of the
// fields the template renders (docs/features/notifications.md, "Account
// emails"). Every accessor answers gracefully when a field is missing or
// has the wrong shape, so a producer that sends less still gets an email
// out.
type payload map[string]any

// Has says the field is present and not empty.
func (p payload) Has(k string) bool {
	v, ok := p[k]
	if !ok || v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

// Str is the field as text.
func (p payload) Str(k string) string {
	switch v := p[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	}
	return fmt.Sprint(p[k])
}

// Num is an integer field (a position).
func (p payload) Num(k string) string {
	if f, ok := p[k].(float64); ok {
		return strconv.FormatInt(int64(math.Round(f)), 10)
	}
	return p.Str(k)
}

// Date is an RFC 3339 field as "5 October 2026 at 14:00 UTC".
func (p payload) Date(k string) string {
	s := p.Str(k)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		if d, derr := time.Parse("2006-01-02", s); derr == nil {
			return d.Format("2 January 2006")
		}
		return s
	}
	return t.UTC().Format("2 January 2006 at 15:04 UTC")
}

// Money is a cents field as "$29.00" (plans are USD, docs/PRICING.md).
func (p payload) Money(k string) string {
	f, ok := p[k].(float64)
	if !ok {
		return p.Str(k)
	}
	return fmt.Sprintf("$%.2f", f/100)
}

// Plan is a plan id as its name.
func (p payload) Plan(k string) string {
	s := p.Str(k)
	switch s {
	case "solo":
		return "Solo"
	case "plus":
		return "Plus"
	case "pro":
		return "Pro"
	case "":
		return "your plan"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// GB is a gigabyte field as "312.5 GB".
func (p payload) GB(k string) string {
	f, ok := p[k].(float64)
	if !ok {
		return p.Str(k) + " GB"
	}
	return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64) + " GB"
}

var (
	tmplOnce sync.Once
	tmplErr  error
	htmlTmpl *htmltemplate.Template
	textTmpl *texttemplate.Template
	kinds    map[string]*texttemplate.Template
)

func loadTemplates() error {
	tmplOnce.Do(func() {
		htmlTmpl, tmplErr = htmltemplate.ParseFS(templateFS, "templates/layout.html")
		if tmplErr != nil {
			return
		}
		textTmpl, tmplErr = texttemplate.ParseFS(templateFS, "templates/layout.txt")
		if tmplErr != nil {
			return
		}
		entries, err := templateFS.ReadDir("templates/kinds")
		if err != nil {
			tmplErr = err
			return
		}
		kinds = map[string]*texttemplate.Template{}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".tmpl")
			t, err := texttemplate.ParseFS(templateFS, path.Join("templates/kinds", e.Name()))
			if err != nil {
				tmplErr = fmt.Errorf("%s: %w", e.Name(), err)
				return
			}
			kinds[name] = t
		}
	})
	return tmplErr
}

// partialFor picks the kind's partial; kinds without one are agent or
// project events and share `agent`.
func partialFor(kind string) *texttemplate.Template {
	if t, ok := kinds[kind]; ok {
		return t
	}
	return kinds["agent"]
}

// block executes one defined block of the partial, or "" when the
// partial does not define it.
func block(t *texttemplate.Template, name string, d kindData) (string, error) {
	if t.Lookup(name) == nil {
		return "", nil
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, d); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(b.String()), nil
}

// Heading is the email's heading for m's kind, or "" when its partial
// has none. Google Chat uses it as a card title (gchat.go).
func Heading(m Message) string {
	if err := loadTemplates(); err != nil {
		return ""
	}
	d := kindData{M: m, Title: Title(m), Dashboard: strings.TrimRight(m.Dashboard, "/"), P: payload{}}
	h, err := block(partialFor(m.Kind), "heading", d)
	if err != nil {
		return ""
	}
	return h
}

// Render builds the subject, the HTML and the text of a message.
func Render(m Message) (Rendered, error) {
	if err := loadTemplates(); err != nil {
		return Rendered{}, err
	}
	d := kindData{M: m, Title: Title(m), Dashboard: strings.TrimRight(m.Dashboard, "/")}
	d.Billing = d.Dashboard + "/billing"
	d.ProjectURL = m.ProjectURL()
	if events.AccountKinds[m.Kind] && strings.HasPrefix(strings.TrimSpace(m.Summary), "{") {
		var p payload
		if err := json.Unmarshal([]byte(m.Summary), &p); err == nil {
			d.P = p
		}
	}
	if d.P == nil {
		d.P = payload{}
	}
	t := partialFor(m.Kind)
	c := content{Subject: Subject(m), Dashboard: d.Dashboard}
	// Account mail is transactional: no unsubscribe line, whatever the
	// message carries (the outbox sets none, but the template is the last
	// word).
	if !events.AccountKinds[m.Kind] {
		c.Unsubscribe = m.Unsubscribe
	}
	var err error
	if c.Why, err = block(t, "why", d); err != nil {
		return Rendered{}, err
	}
	if c.Why == "" {
		c.Why = "About agents on your machines."
	}
	if c.Heading, err = block(t, "heading", d); err != nil {
		return Rendered{}, err
	}
	if c.Meta, err = block(t, "meta", d); err != nil {
		return Rendered{}, err
	}
	if c.Quote, err = block(t, "quote", d); err != nil {
		return Rendered{}, err
	}
	if c.Preheader, err = block(t, "preheader", d); err != nil {
		return Rendered{}, err
	}
	body, err := block(t, "body", d)
	if err != nil {
		return Rendered{}, err
	}
	c.Paragraphs = paragraphs(body)
	if c.Command, err = block(t, "command", d); err != nil {
		return Rendered{}, err
	}
	if c.CommandLabel, err = block(t, "command_label", d); err != nil {
		return Rendered{}, err
	}
	if c.ButtonsLabel, err = block(t, "buttons_label", d); err != nil {
		return Rendered{}, err
	}
	label, err := block(t, "link_label", d)
	if err != nil {
		return Rendered{}, err
	}
	url, err := block(t, "link_url", d)
	if err != nil {
		return Rendered{}, err
	}
	if label != "" && url != "" {
		c.Primary = &link{Label: label, URL: url}
	}
	qlabel, err := block(t, "quiet_label", d)
	if err != nil {
		return Rendered{}, err
	}
	qurl, err := block(t, "quiet_url", d)
	if err != nil {
		return Rendered{}, err
	}
	if qlabel != "" && qurl != "" {
		c.Quiet = &link{Label: qlabel, URL: qurl}
	}
	lines, err := block(t, "lines", d)
	if err != nil {
		return Rendered{}, err
	}
	for _, l := range strings.Split(lines, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			c.Lines = append(c.Lines, l)
		}
	}
	steps, err := block(t, "steps", d)
	if err != nil {
		return Rendered{}, err
	}
	if steps != "" {
		ls := strings.Split(steps, "\n")
		for i := 0; i < len(ls); i += 2 {
			st := step{N: len(c.Steps) + 1, Title: strings.TrimSpace(ls[i])}
			if i+1 < len(ls) {
				st.Command = strings.TrimSpace(ls[i+1])
			}
			c.Steps = append(c.Steps, st)
		}
	}
	if q := m.Question; q != nil && len(q.Replies) == len(q.Options) {
		for i, o := range q.Options {
			c.Buttons = append(c.Buttons, link{Label: o, URL: q.Replies[i]})
		}
	}
	if len(c.Buttons) == 0 {
		c.ButtonsLabel = ""
	}
	var h, x bytes.Buffer
	if err := htmlTmpl.Execute(&h, c); err != nil {
		return Rendered{}, err
	}
	if err := textTmpl.Execute(&x, c); err != nil {
		return Rendered{}, err
	}
	return Rendered{Subject: c.Subject, HTML: h.String(), Text: tidy(x.String())}, nil
}

// paragraphs splits a body on blank lines, keeping single line breaks
// inside a paragraph (a summary with several lines).
func paragraphs(body string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// tidy collapses runs of blank lines the text layout leaves behind.
func tidy(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s) + "\n"
}
