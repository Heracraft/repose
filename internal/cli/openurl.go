package cli

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Links the machine asks a browser to open (DECISIONS I-634, amending
// I-541). The machine's BROWSER, repose-print-url, prints the link and,
// for an https one, appends `SECONDS URL` to guestOpenURLs. While a
// laptop is attached, its session helper takes that file each poll
// (forwarder.sync's ssh, or openURLsPoll's own with forwards off) and
// opens each link in the laptop's browser, as `repose login` opens its
// own: Claude Code's /login, `gh pr create --web`. With nobody attached
// the file waits, and a link older than openURLMaxAge when a laptop
// attaches is dropped, so an attach never opens yesterday's login page.

// guestOpenURLs is the file under the machine's home folder.
const guestOpenURLs = ".cache/repose/open-urls"

// openURLMaxAge is how old a link may be when the helper takes it.
const openURLMaxAge = 2 * time.Minute

// openURLsPoll is how often a helper with forwards off reads the file.
const openURLsPoll = 2 * time.Second

// takeOpenURLsScript prints the links waiting, as `#url SECONDS URL`
// lines after a `#now SECONDS` line, and removes them. The rename makes
// the take atomic, so of two laptops attached one opens each link.
const takeOpenURLsScript = `f="$HOME/` + guestOpenURLs + `"; if [ -s "$f" ] && mv "$f" "$f.$$" 2>/dev/null; then printf '#now %s\n' "$(date +%s)"; sed 's/^/#url /' "$f.$$"; rm -f "$f.$$"; fi`

// parseOpenURLs reads takeOpenURLsScript's lines out of out: the https
// links young enough to open, oldest first.
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

// openableURL is an https link with a host and nothing a shell or the
// opener could read as more than one argument.
func openableURL(u string) bool {
	if strings.ContainsAny(u, " \t\r\n\"'`") {
		return false
	}
	p, err := url.Parse(u)
	return err == nil && p.Scheme == "https" && p.Host != ""
}

// runOpenURLs is the helper's poll of the file when no forwarder reads
// it: forwards off (REPOSE_NO_FORWARD=1).
func runOpenURLs(ctx context.Context, t sshTarget, open func(string) error, alive func() bool) {
	for alive() && ctx.Err() == nil {
		if out, err := runSSH(ctx, t, takeOpenURLsScript, nil); err == nil {
			for _, u := range parseOpenURLs(string(out)) {
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
