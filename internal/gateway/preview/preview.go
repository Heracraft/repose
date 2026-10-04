// Package preview is the HTTPS preview-proxy stub on the edge's :443
// (docs/workstreams/06-gateway-edge.md §5.8, docs/features/ports-and-previews.md).
// The listener, the wildcard-certificate path and the hostname parser exist
// so the certificate pipeline is proven before the feature is built; the
// authentication and the reverse proxy return ErrNotImplemented until
// features/ports-and-previews.md is delivered.
package preview

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// ErrNotImplemented is returned by the parts that wait on
// features/ports-and-previews.md.
var ErrNotImplemented = errors.New("previews are not enabled yet")

// Target is a parsed preview host: the guest port and the label that
// names the project. The label is not split into a slug and a handle: both
// may contain dashes, so `3000-todo-app-hera-craft` has more than one
// reading and a split would let one user's project answer for another's
// preview (I-438). The proxy, when it is built, resolves Label as one
// per-project preview name the api keeps unique.
type Target struct {
	Port  int
	Label string
}

// hostRe matches `<port>-<label>.repose.herakraft.co`: the port is 1-5
// digits, the label one [a-z0-9-] run that starts and ends with a letter
// or digit.
var hostRe = regexp.MustCompile(`^([0-9]{1,5})-([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\.repose\.herakraft\.co$`)

// Route parses a preview hostname into its port and label. Port 0 and
// ports above 65535 are rejected.
func Route(host string) (Target, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i] // strip any :port the client sent
	}
	m := hostRe.FindStringSubmatch(host)
	if m == nil {
		return Target{}, fmt.Errorf("preview host %q is not <port>-<name>.repose.herakraft.co", host)
	}
	port, err := strconv.Atoi(m[1])
	if err != nil || port < 1 || port > 65535 {
		return Target{}, fmt.Errorf("preview host %q: port out of range", host)
	}
	return Target{Port: port, Label: m[2]}, nil
}

// Authenticate will check the Logto session cookie and return the user id.
// Until features/ports-and-previews.md is built it returns ErrNotImplemented.
func Authenticate(r *http.Request) (userID string, err error) {
	return "", ErrNotImplemented
}

// disabledPage is what the stub serves on every path but /healthz.
const disabledPage = `<!doctype html>
<title>repose previews</title>
<h1>Previews are not enabled yet</h1>
<p>Per-project preview URLs are on the roadmap. Use <code>repose open &lt;port&gt;</code>
to reach a dev server over SSH in the meantime.</p>
`

// Handler is the stub's HTTP handler: /healthz for the load path and a
// static page everywhere else. It never proxies; the reverse-proxy skeleton
// below is where the real behaviour will attach.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Parse the host so a misconfigured DNS entry is visible in the
		// stub's logs, but serve the disabled page regardless.
		if _, err := Route(r.Host); err == nil {
			if _, aerr := Authenticate(r); errors.Is(aerr, ErrNotImplemented) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(disabledPage))
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(disabledPage))
	})
	return mux
}
