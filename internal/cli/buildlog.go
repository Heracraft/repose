package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SSEFrame is one server-sent event.
type SSEFrame struct {
	ID    string
	Event string // "" means the default "message" event
	Data  string
}

// parseSSE reads r as text/event-stream, calling emit for each complete
// frame (docs/interfaces/api.md's build-log route: "id:" = seq, "data:" =
// the line; a bare "event: done" frame ends the stream).
func parseSSE(r io.Reader, emit func(SSEFrame)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var cur SSEFrame
	var data []string
	flush := func() {
		if len(data) == 0 && cur.Event == "" && cur.ID == "" {
			return
		}
		cur.Data = strings.Join(data, "\n")
		emit(cur)
		cur = SSEFrame{}
		data = nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "id:"):
			cur.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			cur.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return sc.Err()
}

// StreamBuildLog implements 07-cli.md §5.8: GET the SSE log, print each
// line with a dim "nix › " prefix, and return the terminal state from the
// "done" event.
func StreamBuildLog(ctx context.Context, c *Client, projectID, opID string, w io.Writer, sinceSeq int) (state string, lastSeq int, err error) {
	return streamBuildLines(ctx, c, projectID, opID, sinceSeq, func(line string) {
		_, _ = fmt.Fprintf(w, "nix › %s\n", line) // a gone client is noticed by ctx
	})
}

// streamBuildLines is StreamBuildLog handing each line to emit.
func streamBuildLines(ctx context.Context, c *Client, projectID, opID string, sinceSeq int, emit func(line string)) (state string, lastSeq int, err error) {
	path := fmt.Sprintf("%s/projects/%s/ops/%s/log", c.BaseURL, urlEscape(projectID), urlEscape(opID))
	if sinceSeq > 0 {
		path += fmt.Sprintf("?since=%d", sinceSeq)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", sinceSeq, err
	}
	req.Header.Set("User-Agent", userAgent())
	if c.Tokens != nil {
		tok, err := c.Tokens.AccessToken(ctx, false)
		if err != nil {
			return "", sinceSeq, tokenError(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	// The api client's 30 s timeout covers the whole body, which cut every
	// build longer than that off mid-log; a stream has no such bound (the
	// caller's ctx and the op poll's deadline end it).
	hc := http.Client{}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return "", sinceSeq, &unreachableError{host: req.URL.Host, cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		apiErr, decodeErr := readResponse(resp, nil)
		if decodeErr != nil {
			return "", sinceSeq, decodeErr
		}
		return "", sinceSeq, apiErr
	}

	lastSeq = sinceSeq
	parseErr := parseSSE(resp.Body, func(f SSEFrame) {
		if f.Event == "done" {
			var d struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal([]byte(f.Data), &d)
			state = d.State
			return
		}
		var line struct {
			Seq  int    `json:"seq"`
			Line string `json:"line"`
		}
		if err := json.Unmarshal([]byte(f.Data), &line); err == nil {
			emit(line.Line)
			lastSeq = line.Seq
		}
	})
	return state, lastSeq, parseErr
}

func urlEscape(s string) string {
	// project and op ids are UUIDv7 strings (docs/interfaces/README.md):
	// no characters that need percent-encoding, so a plain pass-through
	// keeps StreamBuildLog's URL building simple.
	return s
}

var fragmentRefRe = regexp.MustCompile(`(fragment|machine)\.nix:(\d+)(?::(\d+))?`)

// buildErrorPrefix is the CLI prefix nix-build-contract.md "What the user
// reads" gives each code: config errors and the api's own refusal of a
// fragment get "config error: ", the closure cap "config too large: ", a
// failed or timed-out build nothing (its summary line names itself), and
// anything else the plain "error: ".
func buildErrorPrefix(code string) string {
	switch code {
	case "eval_failed", "invalid":
		return "config error: "
	case "closure_too_large":
		return "config too large: "
	case "build_failed", "build_timeout":
		return ""
	default:
		return "error: "
	}
}

// RenderBuildError implements 07-cli.md §5.8's error block: the message
// (with the internal "fragment.nix" name swapped for the user's own
// fragment file), the marked line with two lines of context when the
// fragment source is available, and exit 10.
func RenderBuildError(w io.Writer, code, message, localFragmentPath string, fragmentSource []byte) {
	base := filepath.Base(localFragmentPath)
	if base == "" || base == "." {
		base = "repose.nix"
	}
	// machine.nix is the personal layer's own name (DECISIONS I-490): a
	// project's fragment.nix in a message about it is the project's
	// configuration, not the file being pushed.
	display := message
	if base != "machine.nix" {
		display = strings.ReplaceAll(message, "fragment.nix", base)
	}
	_, _ = fmt.Fprintf(w, "%s%s\n", buildErrorPrefix(code), firstLine(display)) // best effort: w is the user's terminal
	// The contract's message is a summary line, a blank line, then the
	// verbatim block (Nix's output, or the ten largest paths of a closure
	// over the cap); the block follows the fragment context (07-cli.md
	// §5.10). It was never printed before I-128, so `closure_too_large` on
	// host-01 named no path.
	defer func() {
		if rest := strings.Trim(restAfterFirstLine(display), "\n"); strings.TrimSpace(rest) != "" {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, rest)
		}
	}()

	m := fragmentRefRe.FindStringSubmatch(message)
	if m == nil {
		return
	}
	file := m[1] + ".nix"
	line, _ := strconv.Atoi(m[2])
	col := 0
	if m[3] != "" {
		col, _ = strconv.Atoi(m[3])
	}
	label := base
	if file == "machine.nix" || base == "machine.nix" {
		label = file
		if file == "fragment.nix" {
			label = "the project's repose.nix"
		}
		// The local copy is shown only when it is the file the error is
		// in: a machine.nix error under `repose config apply` points at
		// a file that is not repose.nix.
		if (file == "machine.nix") != (base == "machine.nix") {
			fragmentSource = nil
		}
	}
	_, _ = fmt.Fprintf(w, "   at %s:%d", label, line)
	if col > 0 {
		_, _ = fmt.Fprintf(w, ":%d", col)
	}
	_, _ = fmt.Fprintln(w)
	if len(fragmentSource) == 0 || line < 1 {
		return
	}
	lines := strings.Split(string(fragmentSource), "\n")
	if line > len(lines) {
		return
	}
	numWidth := len(strconv.Itoa(line))
	if line > 1 {
		_, _ = fmt.Fprintf(w, "      %*d | %s\n", numWidth, line-1, lines[line-2])
	}
	_, _ = fmt.Fprintf(w, "      %*d | %s\n", numWidth, line, lines[line-1])
	caretCol := col
	if caretCol <= 0 {
		caretCol = leadingSpaces(lines[line-1]) + 1
	}
	_, _ = fmt.Fprintf(w, "      %*s | %s^\n", numWidth, "", strings.Repeat(" ", max0(caretCol-1)))
}

func restAfterFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
