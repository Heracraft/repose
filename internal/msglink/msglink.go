// Package msglink is the one rule for the link an agent attaches to a
// message or a question with `--link URL [--label TEXT]` (DECISIONS
// I-931): repose-notify and repose-ask check it before sending, guestd,
// hostd and the api check it again, and the notification senders draw it
// as a button. The link is the agent's, so the rule keeps it to the web,
// keeps a host from hiding behind a user name, and has every button name
// its host, whatever the label says.
package msglink

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits.
const (
	MaxURL   = 2048
	MaxLabel = 40
)

// Errors, each a sentence a caller can show.
var (
	ErrURL      = errors.New("the link must be an http or https address with a host")
	ErrUserInfo = errors.New("the link has a user or password in it, which hides where it goes")
	ErrTooLong  = errors.New("the link is longer than 2048 bytes")
	ErrLabel    = errors.New("--label needs --link")
)

// Link is a checked link; the zero value is no link.
type Link struct {
	URL   string
	Label string
}

// Set reports whether there is a link.
func (l Link) Set() bool { return l.URL != "" }

// Check validates a link and its label and returns them cleaned: the URL
// as url.Parse prints it, the label without control, bidi or zero-width
// characters, its runs of space made one, at most MaxLabel characters.
// Both empty is no link and no error.
func Check(rawURL, label string) (Link, error) {
	rawURL = strings.TrimSpace(rawURL)
	label = CleanLabel(label)
	if rawURL == "" {
		if label != "" {
			return Link{}, ErrLabel
		}
		return Link{}, nil
	}
	if len(rawURL) > MaxURL {
		return Link{}, ErrTooLong
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return Link{}, ErrURL
	}
	if u.User != nil {
		return Link{}, ErrUserInfo
	}
	if strings.ContainsAny(u.Hostname(), "%") {
		return Link{}, ErrURL
	}
	out := u.String()
	if len(out) > MaxURL {
		return Link{}, ErrTooLong
	}
	return Link{URL: out, Label: label}, nil
}

// CleanLabel is a label as Check keeps it.
func CleanLabel(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > MaxLabel {
		out = string([]rune(out)[:MaxLabel])
		out = strings.TrimSpace(out)
	}
	return out
}

// Host is where the link goes, as a button names it: the host and its
// port, the loopback names as localhost, which is where the laptop opens
// a forwarded port.
func (l Link) Host() string {
	u, err := url.Parse(l.URL)
	if err != nil {
		return ""
	}
	host, port := u.Hostname(), u.Port()
	if ip := net.ParseIP(host); (ip != nil && ip.IsLoopback()) || host == "localhost" {
		host = "localhost"
	}
	if port != "" {
		return host + ":" + port
	}
	return host
}

// Button is the button's text: "Compare resumes · localhost:8742", or
// "Open localhost:8742" with no label. The host is always there, so a
// label the agent wrote cannot say the button goes somewhere it does not.
func (l Link) Button() string {
	if l.Label == "" {
		return "Open " + l.Host()
	}
	return l.Label + " · " + l.Host()
}
