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
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
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
	// The host as a browser resolves it: lowercase, and an international
	// name in its ASCII form, so a button shows "xn--pple-43d.com" for
	// a Cyrillic "аpple.com" and the dashboard and the channels agree.
	host, err := asciiHost(u.Hostname())
	if err != nil {
		return Link{}, ErrURL
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return Link{}, ErrURL
		}
		host = net.JoinHostPort(host, p)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	out := u.String()
	// Printable ASCII only: url.String leaves a query's raw bytes, and a
	// byte past ASCII would fail the events insert for ever.
	for i := 0; i < len(out); i++ {
		if out[i] <= 0x20 || out[i] >= 0x7f {
			return Link{}, ErrURL
		}
	}
	if len(out) > MaxURL {
		return Link{}, ErrTooLong
	}
	return Link{URL: out, Label: label}, nil
}

// dots are the separator a button puts between label and host, and the
// characters that pass for it: a label cannot draw a second host.
var dots = map[rune]bool{'·': true, '•': true, '‧': true, '・': true, '･': true, '∙': true, '⋅': true, '\u0387': true, '|': true}

// CleanLabel is a label as Check keeps it.
func CleanLabel(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r), dots[r]:
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

// asciiHost is a host as Check keeps it: an IP address as written, a name
// lowercased in its ASCII (punycode) form; err for a name a browser would
// not resolve.
func asciiHost(h string) (string, error) {
	if ip := net.ParseIP(h); ip != nil {
		return strings.ToLower(h), nil
	}
	a, err := idna.Lookup.ToASCII(strings.TrimSuffix(h, "."))
	if err != nil || a == "" {
		return "", ErrURL
	}
	return strings.ToLower(a), nil
}

// Host is where the link goes, as a button names it: the host and its
// port, an IPv6 address in its brackets, the loopback addresses (an IPv4
// one mapped into IPv6 too) as localhost, which is where the laptop opens
// a forwarded port.
func (l Link) Host() string {
	u, err := url.Parse(l.URL)
	if err != nil {
		return ""
	}
	host, port := u.Hostname(), u.Port()
	ip := net.ParseIP(host)
	switch {
	case (ip != nil && ip.IsLoopback()) || host == "localhost":
		host = "localhost"
	case ip != nil && strings.Contains(host, ":"):
		host = "[" + host + "]"
	}
	if port != "" {
		return host + ":" + port
	}
	return host
}

// ButtonFit is Button in at most n bytes: the label gives way, never the
// host. A host too long for n is the host alone, cut at the end, since a
// button must still say where it goes before anything else.
func (l Link) ButtonFit(n int) string {
	b := l.Button()
	if len(b) <= n {
		return b
	}
	host := l.Host()
	room := n - len(" · ") - len(host)
	if l.Label == "" || room < 4 {
		return cutBytes(host, n)
	}
	return strings.TrimSpace(cutBytes(l.Label, room-len("…"))) + "…" + " · " + host
}

func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
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
