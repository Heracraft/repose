package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// -v writes one line per api or login-server request and per ssh the CLI
// runs to stderr (DECISIONS I-624): method, host for anything but the
// api, path without its query (the log stream's carries a token), status,
// duration and the api's X-Request-Id, plus the body of a refusal; ssh's
// options and target, with the remote command named the way
// REPOSE_TIMING names it (a word, or "script" and its size). Never a
// header, a token or a request body.

var (
	verboseMu  sync.Mutex
	verboseOut io.Writer // nil unless -v
)

func setVerbose(w io.Writer) {
	verboseMu.Lock()
	verboseOut = w
	verboseMu.Unlock()
}

func verboseEnabled() bool {
	verboseMu.Lock()
	defer verboseMu.Unlock()
	return verboseOut != nil
}

// verbosef writes one -v line.
func verbosef(format string, args ...any) {
	verboseMu.Lock()
	defer verboseMu.Unlock()
	if verboseOut == nil {
		return
	}
	_, _ = fmt.Fprintf(verboseOut, "repose: "+format+"\n", args...)
}

// verboseBodyMax bounds the refusal body a -v line shows.
const verboseBodyMax = 400

// verboseTransport logs each round trip under -v.
type verboseTransport struct {
	next    http.RoundTripper
	apiHost string // requests to it are shown by path alone
}

func (t verboseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(r)
	if !verboseEnabled() {
		return resp, err
	}
	where := r.URL.Path
	if r.URL.Host != t.apiHost {
		where = r.URL.Host + r.URL.Path
	}
	ms := time.Since(start).Milliseconds()
	if err != nil {
		verbosef("%s %s -> %s (%dms)", r.Method, where, netReason(err), ms)
		return resp, err
	}
	rid := ""
	if id := resp.Header.Get("X-Request-Id"); id != "" {
		rid = " request " + id
	}
	line := fmt.Sprintf("%s %s -> %d %dms%s", r.Method, where, resp.StatusCode, ms, rid)
	if resp.StatusCode >= 400 && resp.Body != nil {
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		if rerr == nil && len(b) > 0 {
			s := strings.Join(strings.Fields(string(b)), " ")
			if len(s) > verboseBodyMax {
				s = s[:verboseBodyMax] + "..."
			}
			line += "\nrepose:   " + s
		}
	}
	verbosef("%s", line)
	return resp, err
}

// withVerbose wraps c's transport so -v can log it; the wrapper checks
// verboseOut on each request, so it costs nothing without -v.
func withVerbose(c *http.Client, apiURL string) *http.Client {
	next := c.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	host := ""
	if i := strings.Index(apiURL, "://"); i >= 0 {
		host = apiURL[i+3:]
		if j := strings.IndexByte(host, '/'); j >= 0 {
			host = host[:j]
		}
	}
	c.Transport = verboseTransport{next: next, apiHost: host}
	return c
}

// verboseSSH logs one ssh the CLI runs or execs.
func verboseSSH(args []string, remoteCmd string) {
	if !verboseEnabled() {
		return
	}
	line := "ssh " + strings.Join(args, " ")
	if remoteCmd != "" {
		line += " " + sshLabel(remoteCmd)
	}
	verbosef("%s", line)
}
