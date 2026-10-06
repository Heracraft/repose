package main

// repose-notify and repose-ask (DECISIONS I-244): the two commands an agent,
// or the user, runs in any shell in the guest to message the project owner
// or ask them something and wait for the reply. They are this binary under
// two more names (symlinks in the guest base), or `repose-hook notify` and
// `repose-hook ask`.
//
// Unlike the hook mode, these exit non-zero on failure: the caller asked
// for something and must know whether it happened.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/heracraft/repose/internal/guestd/hooks"
)

// Exit codes of repose-notify and repose-ask
// (docs/interfaces/guest-conventions.md "Asking the user").
const (
	ExitOK          = 0
	ExitError       = 1 // guestd unreachable, or refused the request
	ExitUsage       = 2
	ExitTimeout     = 3 // no answer before --timeout
	ExitNoChannel   = 4 // the owner has neither email nor ntfy turned on
	ExitCancelled   = 5 // dismissed, the machine stopped, or the question was lost
	ExitInterrupted = 130
)

// LostAfter is how long repose-ask keeps retrying a guestd it cannot reach
// (a guestd restart takes seconds) before it gives up with ExitError.
var LostAfter = 2 * time.Minute

// pollWait is the long-poll length asked of guestd.
var pollWait = 25

const notifyUsage = `usage: repose-notify [--agent NAME] MESSAGE...

Sends MESSAGE to the project owner's notification channels (email, ntfy)
and returns at once. Without MESSAGE it reads the message from stdin.
Messages are capped at 1 KB and share the project's notification limit of
30 an hour.
`

const askUsage = `usage: repose-ask [--options A,B,C] [--timeout 30m] [--agent NAME] QUESTION...

Sends QUESTION to the project owner's channels and waits for the reply,
which it prints on stdout. With --options (at most three) the reply is one
of them, and ntfy and email offer one-click buttons. The owner can also
reply on the dashboard or with ` + "`repose reply`" + ` on their laptop.

exit status: 0 answered, 1 error, 2 usage, 3 timed out, 4 no notification
channel is on, 5 cancelled (dismissed, the machine stopped, or lost), 130
interrupted
`

// isSub reports whether this binary is running as notify or ask, from its
// name or its first argument, and returns the arguments that follow.
func isSub(argv []string) (string, []string) {
	switch filepath.Base(argv[0]) {
	case "repose-notify":
		return "notify", argv[1:]
	case "repose-ask":
		return "ask", argv[1:]
	}
	if len(argv) > 1 && (argv[1] == "notify" || argv[1] == "ask") {
		return argv[1], argv[2:]
	}
	return "", nil
}

type subOpts struct {
	agent   string
	socket  string
	window  string
	options []string
	timeout time.Duration
	help    bool
	rest    []string
}

// parseSub reads flags anywhere among the words, so
// `repose-ask "ok to push?" --options yes,no` works as well as the flags
// first; `--` ends flag parsing.
func parseSub(args []string, ask bool) (subOpts, error) {
	o := subOpts{agent: callerAgentName(), socket: socketDefault(), window: windowDefault()}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			o.rest = append(o.rest, args[i+1:]...)
			break
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || a == "-" {
			o.rest = append(o.rest, a)
			continue
		}
		take := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("--%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "h", "help":
			o.help = true
		case "agent":
			o.agent, err = take()
		case "socket":
			o.socket, err = take()
		case "window":
			o.window, err = take()
		case "options":
			if !ask {
				return o, fmt.Errorf("unknown flag %s", a)
			}
			var v string
			v, err = take()
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					o.options = append(o.options, p)
				}
			}
		case "timeout":
			if !ask {
				return o, fmt.Errorf("unknown flag %s", a)
			}
			var v string
			if v, err = take(); err == nil {
				o.timeout, err = parseTimeout(v)
			}
		default:
			return o, fmt.Errorf("unknown flag %s", a)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// parseTimeout takes a Go duration (30m, 2h, 90s) or plain seconds.
func parseTimeout(v string) (time.Duration, error) {
	if n, err := strconv.Atoi(v); err == nil {
		v = strconv.Itoa(n) + "s"
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--timeout %q is not a duration like 30m or 2h", v)
	}
	if d > 24*time.Hour {
		return 0, errors.New("--timeout is at most 24h")
	}
	return d, nil
}

// callerAgentName is the agent this command runs under: the variable the
// agent wrappers export (inherited by every shell an agent starts), else
// the first known agent among this process's ancestors, else "shell".
func callerAgentName() string {
	if a := agentDefault(); a != "" {
		return a
	}
	if a := agentInAncestry("/proc", os.Getppid()); a != "" {
		return a
	}
	return hooks.Shell
}

var knownAgents = map[string]bool{"claude": true, "codex": true, "opencode": true, "gemini": true, "pi": true}

// agentInAncestry walks the parent chain from pid reading each process's
// comm (a process name, never its arguments). Nix wrappers show up as
// ".claude-wrapped", truncated to 15 bytes.
func agentInAncestry(proc string, pid int) string {
	for i := 0; i < 32 && pid > 1; i++ {
		b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "comm"))
		if err != nil {
			return ""
		}
		name := strings.TrimPrefix(strings.TrimSpace(string(b)), ".")
		if j := strings.Index(name, "-wrap"); j > 0 {
			name = name[:j]
		}
		if knownAgents[name] {
			return name
		}
		pid = parentOf(proc, pid)
	}
	return ""
}

// parentOf reads the ppid from /proc/<pid>/stat, whose comm field may
// contain spaces and parentheses, so it is read after the last ')'.
func parentOf(proc string, pid int) int {
	b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

func socketClient(socket string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
}

// call does one request on the hook socket and returns the status and body.
func call(ctx context.Context, c *http.Client, method, path string, body any) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://guestd"+path, r)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // drained below
	out, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	return resp.StatusCode, out, err
}

func words(rest []string, stdin io.Reader) (string, error) {
	if len(rest) > 0 {
		return strings.Join(rest, " "), nil
	}
	if f, ok := stdin.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			return "", nil
		}
	}
	b, err := io.ReadAll(io.LimitReader(stdin, 8<<10))
	return string(b), err
}

// runNotify is repose-notify.
func runNotify(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, err := parseSub(args, false)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-notify: %v\n\n%s", err, notifyUsage)
		return ExitUsage
	}
	if o.help {
		_, _ = fmt.Fprint(stdout, notifyUsage)
		return ExitOK
	}
	text, err := words(o.rest, stdin)
	if err != nil || strings.TrimSpace(text) == "" {
		_, _ = fmt.Fprintf(stderr, "repose-notify: no message\n\n%s", notifyUsage)
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	code, body, err := call(ctx, socketClient(o.socket, Timeout), http.MethodPost, "/notify", hooks.MessagePayload{Agent: o.agent, Window: o.window, Text: text})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-notify: guestd is not answering on %s: %v\n", o.socket, err)
		return ExitError
	}
	if code != http.StatusNoContent {
		_, _ = fmt.Fprintf(stderr, "repose-notify: guestd refused the message: %s\n", strings.TrimSpace(string(body)))
		return ExitError
	}
	return ExitOK
}

// runAsk is repose-ask.
func runAsk(args []string, stdin io.Reader, stdout, stderr io.Writer, sigs <-chan os.Signal) int {
	o, err := parseSub(args, true)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-ask: %v\n\n%s", err, askUsage)
		return ExitUsage
	}
	if o.help {
		_, _ = fmt.Fprint(stdout, askUsage)
		return ExitOK
	}
	text, err := words(o.rest, stdin)
	if err != nil || strings.TrimSpace(text) == "" {
		_, _ = fmt.Fprintf(stderr, "repose-ask: no question\n\n%s", askUsage)
		return ExitUsage
	}
	if len(o.options) > 3 {
		_, _ = fmt.Fprintf(stderr, "repose-ask: at most 3 --options (ntfy shows three buttons)\n")
		return ExitUsage
	}
	short := socketClient(o.socket, Timeout)
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	code, body, err := call(ctx, short, http.MethodPost, "/ask", hooks.AskPayload{Agent: o.agent, Window: o.window, Text: text, Options: o.options, TimeoutS: uint32(o.timeout / time.Second)})
	cancel()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-ask: guestd is not answering on %s: %v\n", o.socket, err)
		return ExitError
	}
	if code != http.StatusCreated {
		if code == http.StatusBadRequest {
			_, _ = fmt.Fprintf(stderr, "repose-ask: guestd refused the question: %s\n", strings.TrimSpace(string(body)))
			return ExitUsage
		}
		_, _ = fmt.Fprintf(stderr, "repose-ask: guestd refused the question: %s\n", strings.TrimSpace(string(body)))
		return ExitError
	}
	var st hooks.AskState
	if err := json.Unmarshal(body, &st); err != nil || st.ID == "" {
		_, _ = fmt.Fprintf(stderr, "repose-ask: guestd answered something unexpected\n")
		return ExitError
	}

	type outcome struct {
		st   hooks.AskState
		code int
		msg  string
	}
	done := make(chan outcome, 1)
	pollCtx, stopPoll := context.WithCancel(context.Background())
	defer stopPoll()
	go func() {
		long := socketClient(o.socket, time.Duration(pollWait+10)*time.Second)
		var lostSince time.Time
		path := "/ask/" + st.ID + "?wait=" + strconv.Itoa(pollWait)
		for {
			code, body, err := call(pollCtx, long, http.MethodGet, path, nil)
			if pollCtx.Err() != nil {
				return
			}
			if err != nil {
				// guestd restarting: its questions are on a tmpfs, so wait
				// for it to come back and ask again.
				if lostSince.IsZero() {
					lostSince = time.Now()
				}
				if time.Since(lostSince) > LostAfter {
					done <- outcome{code: ExitError, msg: "lost guestd while waiting: " + err.Error()}
					return
				}
				select {
				case <-pollCtx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			lostSince = time.Time{}
			if code == http.StatusNotFound {
				done <- outcome{code: ExitCancelled, msg: "the question is gone (the machine restarted)"}
				return
			}
			var s hooks.AskState
			if code != http.StatusOK || json.Unmarshal(body, &s) != nil {
				done <- outcome{code: ExitError, msg: "guestd answered " + strconv.Itoa(code)}
				return
			}
			if s.State != "open" {
				done <- outcome{st: s}
				return
			}
		}
	}()

	select {
	case sig := <-sigs:
		stopPoll()
		cctx, ccancel := context.WithTimeout(context.Background(), Timeout)
		_, _, _ = call(cctx, short, http.MethodDelete, "/ask/"+st.ID, nil) // best effort; guestd expires it at the timeout otherwise
		ccancel()
		_, _ = fmt.Fprintf(stderr, "repose-ask: %v; the question is cancelled\n", sig)
		return ExitInterrupted
	case out := <-done:
		if out.msg != "" {
			_, _ = fmt.Fprintf(stderr, "repose-ask: %s\n", out.msg)
			return out.code
		}
		switch out.st.State {
		case "answered":
			_, _ = fmt.Fprintln(stdout, out.st.Answer)
			return ExitOK
		case "expired":
			_, _ = fmt.Fprintln(stderr, "repose-ask: no answer before the timeout")
			return ExitTimeout
		case "no_channel":
			_, _ = fmt.Fprintln(stderr, "repose-ask: no notification channel is on; the owner can turn one on with `repose notify set`")
			return ExitNoChannel
		default:
			_, _ = fmt.Fprintln(stderr, "repose-ask: the question was cancelled")
			return ExitCancelled
		}
	}
}

// runSub runs notify or ask with the process's real stdio and signals.
func runSub(name string, args []string) int {
	if name == "notify" {
		return runNotify(args, os.Stdin, os.Stdout, os.Stderr)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return runAsk(args, os.Stdin, os.Stdout, os.Stderr, sigs)
}
