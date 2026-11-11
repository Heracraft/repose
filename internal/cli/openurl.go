package cli

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Links the machine asks a browser to open (DECISIONS I-634, amending
// I-541; I-1108 for the loopback ones). The machine's BROWSER,
// repose-print-url, prints the link and appends `SECONDS URL` to
// guestOpenURLs for an https link and for an http or https link whose
// host is the machine's own loopback. While a laptop is attached, its
// session helper takes that file each poll (forwarder.sync's ssh, or
// openURLsPoll's own with forwards off) and opens each link in the
// laptop's browser, as `repose login` opens its own: Claude Code's
// /login, `gh pr create --web`, a dev server's `--open`. With nobody
// attached the file waits, and a link older than openURLMaxAge when a
// laptop attaches is dropped, so an attach never opens yesterday's
// login page.
//
// A loopback link never opens on the laptop as written: localhost on
// the laptop is the laptop's own machine, and the port the machine
// named may be forwarded to another one, or to nothing. It opens on the
// laptop end of the forward that reaches it, after a short wait for the
// forward to appear, and otherwise does not open and says so.

// guestOpenURLs is the file under the machine's home folder.
const guestOpenURLs = ".cache/repose/open-urls"

// openURLMaxAge is how old a link may be when the helper takes it.
const openURLMaxAge = 2 * time.Minute

// openURLsPoll is how often a helper with forwards off reads the file.
const openURLsPoll = 2 * time.Second

// openURLForwardWait is how long a loopback link waits for the forward
// that reaches it: a dev server hands its URL to BROWSER a moment
// before `ss` shows the listener.
const openURLForwardWait = 5 * time.Second

// openURLRepeat is how often the same link opens: a dev server that
// restarts with --open in a loop must not fill the laptop with tabs.
const openURLRepeat = time.Minute

// takeOpenURLsScript prints the links waiting, as `#url SECONDS URL`
// lines after a `#now SECONDS` line, and removes them. The rename makes
// the take atomic, so of two laptops attached one opens each link.
const takeOpenURLsScript = `f="$HOME/` + guestOpenURLs + `"; if [ -s "$f" ] && mv "$f" "$f.$$" 2>/dev/null; then printf '#now %s\n' "$(date +%s)"; sed 's/^/#url /' "$f.$$"; rm -f "$f.$$"; fi`

// parseOpenURLs reads takeOpenURLsScript's lines out of out: the links
// young enough to open, oldest first.
func parseOpenURLs(out string) []string {
	var now int64
	var urls []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "#now "); ok {
			now, _ = strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, "#url ")
		if !ok {
			continue
		}
		ts, u, ok := strings.Cut(strings.TrimSpace(rest), " ")
		if !ok {
			continue
		}
		at, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || now == 0 || now-at > int64(openURLMaxAge/time.Second) {
			continue
		}
		if openableURL(u) {
			urls = append(urls, u)
		}
	}
	return urls
}

// openableURL is a link the laptop may open: an https link with a host,
// or an http one on the machine's loopback, and nothing a shell or the
// opener could read as more than one argument.
func openableURL(u string) bool {
	if strings.ContainsAny(u, " \t\r\n\"'`") {
		return false
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return false
	}
	return p.Scheme == "https" || (p.Scheme == "http" && loopbackHost(p.Hostname()))
}

// loopbackHost is a host that means "this machine".
func loopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// openURLPort is the machine port a loopback link names, and whether
// the link is a loopback one at all. A link with no port takes its
// scheme's, which is below 1024 and so never forwarded: such a link
// says no forward reaches it rather than opening the laptop's own.
func openURLPort(u string) (int, bool) {
	p, err := url.Parse(u)
	if err != nil || !loopbackHost(p.Hostname()) {
		return 0, false
	}
	if port := p.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	if p.Scheme == "https" {
		return 443, true
	}
	return 80, true
}

// openURLOnLaptop is the link rewritten onto the laptop's end of the
// forward: the host becomes localhost and the port the laptop's.
func openURLOnLaptop(u string, laptopPort int) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	p.Host = net.JoinHostPort("localhost", strconv.Itoa(laptopPort))
	return p.String()
}

// openURLNoForwardLine is what the user is told about a loopback link
// nothing reaches. It is a warning that work did not go, so it names
// what did not happen and stops.
func openURLNoForwardLine(port int) string {
	return fmt.Sprintf("A program on the machine opened localhost:%d, and no forward reaches it.", port)
}

// waitingURL is a link the machine handed to BROWSER, waiting for the
// forward that reaches it.
type waitingURL struct {
	URL   string
	Since time.Time
}

// takeURLs records the links this poll took. Links that need no forward
// are opened at once; a loopback one waits for its forward.
func (f *forwarder) takeURLs(out string, now time.Time) {
	for _, u := range parseOpenURLs(out) {
		if _, loopback := openURLPort(u); !loopback {
			f.openOnce(u, now)
			continue
		}
		f.urls = append(f.urls, waitingURL{URL: u, Since: now})
	}
}

// openWaitingURLs opens every waiting loopback link a forward now
// reaches, and drops the ones that waited longer than
// openURLForwardWait, saying so once each.
func (f *forwarder) openWaitingURLs(now time.Time) {
	var keep []waitingURL
	for _, w := range f.urls {
		port, _ := openURLPort(w.URL)
		if fe, ok := f.fwd[port]; ok {
			f.openOnce(openURLOnLaptop(w.URL, fe.Local), now)
			continue
		}
		if now.Sub(w.Since) < openURLForwardWait {
			keep = append(keep, w)
			continue
		}
		f.say(openURLNoForwardLine(port))
	}
	f.urls = keep
}

// openOnce opens a link, at most once every openURLRepeat.
func (f *forwarder) openOnce(u string, now time.Time) {
	if f.opened == nil {
		f.opened = map[string]time.Time{}
	}
	if at, ok := f.opened[u]; ok && now.Sub(at) < openURLRepeat {
		return
	}
	f.opened[u] = now
	_ = f.openURL(u)
}

// runOpenURLs is the helper's poll of the file when no forwarder reads
// it: forwards off (REPOSE_NO_FORWARD=1). With no forwards, a loopback
// link has nothing to reach it; it is not opened and says so once.
func runOpenURLs(ctx context.Context, t sshTarget, open func(string) error, say func(string), alive func() bool) {
	said := false
	opened := map[string]time.Time{}
	for alive() && ctx.Err() == nil {
		if out, err := runSSH(ctx, t, takeOpenURLsScript, nil); err == nil {
			for _, u := range parseOpenURLs(string(out)) {
				if port, loopback := openURLPort(u); loopback {
					// Forwards are off, so no loopback link can be
					// reached. Said once for the attach, not once a
					// link: the user turned them off on purpose.
					if !said {
						said = true
						say(openURLNoForwardLine(port))
					}
					continue
				}
				if at, ok := opened[u]; ok && time.Since(at) < openURLRepeat {
					continue
				}
				opened[u] = time.Now()
				_ = open(u)
			}
		}
		if sleepOrDone(ctx, openURLsPoll) != nil {
			return
		}
	}
}

// laptopOpensURLs is whether this laptop opens the machine's links: it
// has a browser, and REPOSE_NO_BROWSER is not 1.
func laptopOpensURLs() bool {
	o := loginOptsFromEnv(false, false)
	return browserAvailable(o.NoBrowser, o.GuestEnv, o.Display, o.GOOS)
}
