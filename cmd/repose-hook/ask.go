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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/guestd/hooks"
	"github.com/heracraft/repose/internal/guestd/questions"
	"github.com/heracraft/repose/internal/msglink"
)

// Exit codes of repose-notify and repose-ask
// (docs/interfaces/guest-conventions.md, "Messages and questions").
const (
	ExitOK = 0
	// ExitError: no question of the caller's is waiting for an answer.
	// For repose-notify, guestd was unreachable or refused the message.
	ExitError     = 1
	ExitUsage     = 2
	ExitTimeout   = 3 // no answer before --timeout
	ExitNoChannel = 4 // the owner has no channel on: email, ntfy or Google Chat
	ExitCancelled = 5 // dismissed, withdrawn, the machine stopped, or the question was lost
	// ExitAnsweredInTerminal: the user sent the agent a prompt, which
	// closed its open questions (DECISIONS I-690).
	ExitAnsweredInTerminal = 6
	// ExitAskedNotAnswered: the question is open and this command stopped
	// waiting for it, so the owner may still answer (DECISIONS I-1406).
	// It is the other half of what ExitError used to mean: 1 says no
	// question of the caller's is waiting for an answer, 7 says one is.
	// An agent must not read 7 as "the owner said nothing".
	ExitAskedNotAnswered = 7
	ExitInterrupted      = 130
)

// outcome is how the question ended: a state guestd reported, or a code
// and the one line that says why.
type outcome struct {
	st   hooks.AskState
	code int
	msg  string
}

// LostAfter is how long repose-ask keeps trying a guestd it cannot reach
// (a guestd restart takes seconds) before it gives up: while it opens the
// question and while it waits for the answer. guest-conventions.md
// promises these two minutes; before I-1404 only the wait honoured them.
// The user's --timeout shortens it.
var LostAfter = 2 * time.Minute

// AskCall bounds one call on the ask path: the POST that opens the
// question, and the list that reconciles it. It is deliberately not
// Timeout, the hook POST's three seconds. A hook that misses its socket
// loses a notification; an ask that gives up on a guestd which is merely
// slow leaves the owner answering a question nobody is waiting for, and
// the agent carrying on as if they had said nothing (ENG-105, I-1404).
var AskCall = 30 * time.Second

// retryEvery is the pause between attempts at an unreachable guestd,
// the same pause the wait uses.
var retryEvery = time.Second

// pollWait is the long-poll length asked of guestd.
var pollWait = 25

const notifyUsage = `usage: repose-notify [--done] [--link URL [--label TEXT]] [--agent NAME] MESSAGE...

Sends MESSAGE to the project owner's notification channels (email, ntfy,
Google Chat) and returns at once. Without MESSAGE it reads the message
from stdin. With --done the message says the work they asked for is
finished. Messages are capped at 1 KB and share the project's notification
limit of 30 an hour.

--link URL gives the message a button that opens URL (http or https),
named by --label TEXT (40 characters) and the URL's host: "Compare
resumes · localhost:8742". Pass the one thing to open there rather than
in the message.
`

const askUsage = `usage: repose-ask [--options A,B,C] [--timeout 30m] [--link URL [--label TEXT]] [--agent NAME] QUESTION...
       repose-ask --secret NAME [--timeout 2h] [--link URL [--label TEXT]] [--agent NAME] REASON...
       repose-ask --cancel [ID | --all]

Sends QUESTION to the project owner's channels and waits for the reply,
which it prints on stdout. With --options (at most three) the reply is one
of them, and ntfy, email and Google Chat offer one-click buttons. The
owner can also reply on the dashboard or with ` + "`repose reply`" + ` on their
laptop. A prompt the user types to the asking agent closes its open
questions.

--secret NAME asks the owner for the value of the secret NAME instead,
with REASON saying what it is for and where they get it. They paste it on
the dashboard or into ` + "`repose reply`" + `, never into a notification. It
is stored as the project's secret NAME, and repose-ask returns once the
machine has it: the next command reads $NAME and
/run/repose/secrets/NAME. Nothing is printed on stdout. It waits 2 hours
without --timeout.

--link URL [--label TEXT] gives the question a button to what it is about,
as repose-notify --link does.

--cancel withdraws this agent's open questions, or the one with ID (printed
on stderr when it was asked), or with --all every open question on the
machine, and prints how many.

exit status: 0 answered, 1 the question was never asked, 2 usage, 3 timed
out, 4 no notification channel is on, 5 cancelled (dismissed, withdrawn,
the machine stopped, or lost), 6 the user answered in the terminal, 7
asked and still open with nothing waiting for it, 130 interrupted
`

// isSub reports whether this binary is running as notify, ask or
// presence, from its name or its first argument, and returns the
// arguments that follow.
func isSub(argv []string) (string, []string) {
	switch filepath.Base(argv[0]) {
	case "repose-notify":
		return "notify", argv[1:]
	case "repose-ask":
		return "ask", argv[1:]
	case "repose-presence":
		return "presence", argv[1:]
	}
	if len(argv) > 1 && (argv[1] == "notify" || argv[1] == "ask" || argv[1] == "presence") {
		return argv[1], argv[2:]
	}
	return "", nil
}

type subOpts struct {
	link    string
	label   string
	secret  string
	agent   string
	socket  string
	window  string
	options []string
	timeout time.Duration
	help    bool
	done    bool
	cancel  bool
	all     bool
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
		case "done":
			if ask {
				return o, fmt.Errorf("unknown flag %s", a)
			}
			o.done = true
		case "cancel", "all":
			if !ask {
				return o, fmt.Errorf("unknown flag %s", a)
			}
			if name == "cancel" {
				o.cancel = true
			} else {
				o.all = true
			}
		case "secret":
			if !ask {
				return o, fmt.Errorf("unknown flag %s", a)
			}
			o.secret, err = take()
		case "link":
			o.link, err = take()
		case "label":
			o.label, err = take()
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
	link, err := msglink.Check(o.link, o.label)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-notify: %v\n", err)
		return ExitUsage
	}
	kind := ""
	if o.done {
		kind = hooks.KindDone
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	code, body, err := call(ctx, socketClient(o.socket, Timeout), http.MethodPost, "/notify", hooks.MessagePayload{Agent: o.agent, Window: o.window, Kind: kind, Text: text, Link: link.URL, LinkLabel: link.Label})
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
	if o.cancel || o.all {
		return runCancel(o, stdout, stderr)
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
	link, err := msglink.Check(o.link, o.label)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-ask: %v\n", err)
		return ExitUsage
	}
	if o.secret != "" {
		if len(o.options) > 0 {
			_, _ = fmt.Fprintf(stderr, "repose-ask: --secret takes no --options\n\n%s", askUsage)
			return ExitUsage
		}
		if !questions.ValidSecretName(o.secret) {
			_, _ = fmt.Fprintf(stderr, "repose-ask: %q is not a secret name: use capitals, digits and _, starting with a letter (BASH_ENV, ENV and REPOSE_ENV_GEN are taken)\n", o.secret)
			return ExitUsage
		}
	}
	short := socketClient(o.socket, Timeout)
	asked := time.Now()
	st, bad := openAsk(o, hooks.AskPayload{Agent: o.agent, Window: o.window, Text: text, Options: o.options,
		TimeoutS: uint32(o.timeout / time.Second), Secret: o.secret, Link: link.URL, LinkLabel: link.Label}, sigs)
	if bad != nil {
		_, _ = fmt.Fprintf(stderr, "repose-ask: %s\n", bad.msg)
		return bad.code
	}
	// The id is what `--cancel ID` takes (I-690); stdout stays the answer.
	_, _ = fmt.Fprintf(stderr, "repose-ask: asked %s\n", st.ID)

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
					// The question is open; nobody is watching it now
					// (I-1406). Exit 1 here read as "never asked".
					done <- outcome{code: ExitAskedNotAnswered, msg: "lost guestd while waiting; question " + st.ID + " is unanswered"}
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
				done <- outcome{code: ExitAskedNotAnswered, msg: "guestd answered " + strconv.Itoa(code) + "; question " + st.ID + " is unanswered"}
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
		code, _, err := call(cctx, short, http.MethodDelete, "/ask/"+st.ID, nil)
		ccancel()
		if err != nil || code != http.StatusNoContent {
			// Saying "cancelled" here would be the ENG-105 lie in
			// miniature: the owner still has the question.
			_, _ = fmt.Fprintf(stderr, "repose-ask: %v; question %s could not be cancelled and closes 90 seconds from now\n", sig, st.ID)
			return ExitInterrupted
		}
		_, _ = fmt.Fprintf(stderr, "repose-ask: %v; the question is cancelled\n", sig)
		return ExitInterrupted
	case out := <-done:
		if out.msg != "" {
			_, _ = fmt.Fprintf(stderr, "repose-ask: %s\n", out.msg)
			return out.code
		}
		switch out.st.Reason {
		case "terminal":
			_, _ = fmt.Fprintln(stderr, "repose-ask: the user answered in the terminal")
			return ExitAnsweredInTerminal
		case "withdrawn":
			_, _ = fmt.Fprintln(stderr, "repose-ask: the question was withdrawn")
			return ExitCancelled
		}
		switch out.st.State {
		case "answered":
			if o.secret != "" {
				if out.st.Answer == questions.PushFailed {
					_, _ = fmt.Fprintf(stderr, "repose-ask: the owner set %s, but the push to this machine failed; ask them to run `repose secrets set %s` again\n", o.secret, o.secret)
					return ExitError
				}
				return secretArrived(o.secret, stderr)
			}
			_, _ = fmt.Fprintln(stdout, out.st.Answer)
			return ExitOK
		case "expired":
			// The owner may have answered in the last minutes, while the
			// push was still on its way: a secret file written since the
			// ask is the answer (I-929).
			if o.secret != "" && secretWrittenSince(o.secret, asked) {
				_, _ = fmt.Fprintf(stderr, "repose-ask: %s is set; the next command reads $%s\n", o.secret, o.secret)
				return ExitOK
			}
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

// openAsk opens the question and returns the state guestd holds for it.
//
// Two rules, both ENG-105 (DECISIONS I-1404). The time it may spend is
// the user's --timeout, bounded by LostAfter, because a question that is
// not open yet cannot be answered and waiting longer than it would live
// buys nothing. And a call that fails is never read as "the question was
// never asked" until guestd has been asked what it holds: the POST may
// have opened the question and lost only the answer to it, which is the
// defect itself -- the owner is asked, the asker reports failure, and
// the agent carries on as if the owner had said nothing.
func openAsk(o subOpts, p hooks.AskPayload, sigs <-chan os.Signal) (hooks.AskState, *outcome) {
	budget := LostAfter
	if o.timeout > 0 && o.timeout < budget {
		budget = o.timeout
	}
	deadline := time.Now().Add(budget)
	// One id for the open, however many times it is sent: guestd answers
	// a repeat with the question it already opened, so a retry never
	// puts the same question to the owner twice.
	p.RequestID = uuid.Must(uuid.NewV7()).String()
	// Every deadline on this client comes from the context: a Timeout
	// here would cap the call again, under another name.
	c := socketClient(o.socket, 0)
	// The open can now take minutes, so a Ctrl-C during it has to land:
	// it ends the attempt in flight as well as the pause.
	parent, stopParent := context.WithCancel(context.Background())
	defer stopParent()
	caught := make(chan os.Signal, 1)
	go func() {
		select {
		case sig := <-sigs:
			caught <- sig
			stopParent()
		case <-parent.Done():
		}
	}()
	interrupted := func() *os.Signal {
		select {
		case sig := <-caught:
			return &sig
		default:
			return nil
		}
	}
	var last error
	for {
		end := time.Now().Add(AskCall)
		if end.After(deadline) {
			end = deadline
		}
		ctx, cancel := context.WithDeadline(parent, end)
		code, body, err := call(ctx, c, http.MethodPost, "/ask", p)
		cancel()
		if err == nil {
			st, bad := opened(code, body, o, c)
			if sig := interrupted(); sig != nil && bad == nil {
				return hooks.AskState{}, withdrawOnSignal(c, o, st, *sig)
			}
			return st, bad
		}
		last = err
		if sig := interrupted(); sig != nil {
			// Nothing of the caller's is open unless an abandoned
			// attempt opened one, which this takes back.
			st, ok := ourOpenQuestion(c, o, p)
			if !ok {
				return hooks.AskState{}, &outcome{code: ExitInterrupted, msg: fmt.Sprintf("%v; no question was asked", *sig)}
			}
			return hooks.AskState{}, withdrawOnSignal(c, o, st, *sig)
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-time.After(retryEvery):
		case <-parent.Done():
		}
	}
	// Out of budget for the open with no answer to any attempt. An
	// attempt may still have opened the question -- guestd is the only
	// thing that knows -- and the owner may be reading it now, so take
	// it up and wait for the answer rather than report it unasked.
	// Whether it is ever answered is then the wait's outcome: the answer,
	// the question's own timeout, or 7.
	if st, ok := ourOpenQuestion(c, o, p); ok {
		return st, nil
	}
	return hooks.AskState{}, &outcome{code: ExitError,
		msg: fmt.Sprintf("guestd is not answering on %s: %v", o.socket, last)}
}

// withdrawOnSignal takes back a question the asker opened just as it was
// interrupted, and says which of the two happened: the owner no longer
// has it, or guestd would not take it back and the orphan sweep closes
// it 90 seconds from now.
func withdrawOnSignal(c *http.Client, o subOpts, st hooks.AskState, sig os.Signal) *outcome {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	code, _, err := call(ctx, c, http.MethodDelete, "/ask/"+st.ID+"?reason=interrupted", nil)
	if err != nil || code != http.StatusNoContent {
		return &outcome{code: ExitInterrupted, msg: fmt.Sprintf("%v; question %s could not be cancelled and closes 90 seconds from now", sig, st.ID)}
	}
	return &outcome{code: ExitInterrupted, msg: fmt.Sprintf("%v; the question is cancelled", sig)}
}

// opened reads guestd's answer to POST /ask.
func opened(code int, body []byte, o subOpts, c *http.Client) (hooks.AskState, *outcome) {
	if code != http.StatusCreated {
		switch {
		case code == http.StatusConflict && strings.Contains(string(body), "secret_not_relayed"):
			return hooks.AskState{}, &outcome{code: ExitError,
				msg: fmt.Sprintf("this machine's host cannot carry a secret question yet; ask the owner to run `repose secrets set %s`", o.secret)}
		case code == http.StatusBadRequest:
			return hooks.AskState{}, &outcome{code: ExitUsage, msg: "guestd refused the question: " + strings.TrimSpace(string(body))}
		default:
			return hooks.AskState{}, &outcome{code: ExitError, msg: "guestd refused the question: " + strings.TrimSpace(string(body))}
		}
	}
	var st hooks.AskState
	if json.Unmarshal(body, &st) != nil || st.ID == "" {
		return hooks.AskState{}, &outcome{code: ExitError, msg: "guestd answered something unexpected"}
	}
	if o.secret != "" && st.Secret != o.secret {
		// A guestd from before --secret took it as a question with a
		// written answer, which the owner could answer with the value in
		// a notification: take it back before anyone sees it.
		cctx, ccancel := context.WithTimeout(context.Background(), Timeout)
		_, _, _ = call(cctx, c, http.MethodDelete, "/ask/"+st.ID+"?reason=withdrawn", nil)
		ccancel()
		return hooks.AskState{}, &outcome{code: ExitError,
			msg: "this machine's guestd does not take --secret; ask the owner to run `repose secrets set " + o.secret + "`"}
	}
	return st, nil
}

// ourOpenQuestion asks guestd for the caller's own open questions and
// returns the newest whose text is the text just asked, so an open whose
// answer was lost is taken up rather than asked again. guestd scopes GET
// /ask to the asker's own agent process, so no other agent's question is
// ever adopted, and the text is matched as guestd stored it.
func ourOpenQuestion(c *http.Client, o subOpts, p hooks.AskPayload) (hooks.AskState, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), AskCall)
	defer cancel()
	q := url.Values{"agent": {o.agent}, "window": {o.window}}
	code, body, err := call(ctx, c, http.MethodGet, "/ask?"+q.Encode(), nil)
	if err != nil || code != http.StatusOK {
		return hooks.AskState{}, false
	}
	var list hooks.AskList
	if json.Unmarshal(body, &list) != nil {
		return hooks.AskState{}, false
	}
	want := questions.Clean(p.Text, questions.TextCap)
	for i := len(list.Questions) - 1; i >= 0; i-- {
		st := list.Questions[i]
		if st.ID != "" && st.State == questions.StateOpen && st.Text == want && st.Secret == p.Secret {
			return st, true
		}
	}
	return hooks.AskState{}, false
}

// secretsDir is where guestd writes the named secrets, one file each;
// REPOSE_SECRETS_DIR moves it (tests).
func secretsDir() string {
	if d := os.Getenv("REPOSE_SECRETS_DIR"); d != "" {
		return d
	}
	return "/run/repose/secrets"
}

// secretArrived is the end of an answered --secret ask (DECISIONS I-929).
// The api closes the question only once the push that carries the value
// has finished, so the file is there unless that push failed.
func secretArrived(name string, stderr io.Writer) int {
	if _, err := os.Stat(filepath.Join(secretsDir(), name)); err != nil {
		_, _ = fmt.Fprintf(stderr, "repose-ask: the owner set %s, but it has not reached this machine; ask them to run `repose secrets set %s` again\n", name, name)
		return ExitError
	}
	_, _ = fmt.Fprintf(stderr, "repose-ask: %s is set; the next command reads $%s\n", name, name)
	return ExitOK
}

// secretWrittenSince reports whether guestd wrote the secret name after t.
func secretWrittenSince(name string, t time.Time) bool {
	st, err := os.Stat(filepath.Join(secretsDir(), name))
	return err == nil && !st.ModTime().Before(t)
}

// runCancel is repose-ask --cancel [ID | --all]: withdraw open questions
// (reason withdrawn) and print how many.
func runCancel(o subOpts, stdout, stderr io.Writer) int {
	usage := func(msg string) int {
		_, _ = fmt.Fprintf(stderr, "repose-ask: %s\n\n%s", msg, askUsage)
		return ExitUsage
	}
	switch {
	case !o.cancel:
		return usage("--all goes with --cancel")
	case len(o.options) > 0 || o.timeout != 0 || o.secret != "" || o.link != "" || o.label != "":
		return usage("--cancel takes no --options, --secret, --link or --timeout")
	case len(o.rest) > 1, o.all && len(o.rest) == 1:
		return usage("--cancel takes one question ID, or --all")
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	c := socketClient(o.socket, Timeout)
	n := 1
	if len(o.rest) == 1 {
		id := o.rest[0]
		code, body, err := call(ctx, c, http.MethodDelete, "/ask/"+url.PathEscape(id)+"?reason=withdrawn", nil)
		switch {
		case err != nil:
			_, _ = fmt.Fprintf(stderr, "repose-ask: guestd is not answering on %s: %v\n", o.socket, err)
			return ExitError
		case code == http.StatusNotFound, code == http.StatusConflict:
			_, _ = fmt.Fprintf(stderr, "repose-ask: no open question %s\n", id)
			return ExitError
		case code != http.StatusNoContent:
			_, _ = fmt.Fprintf(stderr, "repose-ask: guestd refused: %s\n", strings.TrimSpace(string(body)))
			return ExitError
		}
	} else {
		q := url.Values{"reason": {"withdrawn"}}
		if o.all {
			q.Set("scope", "all")
		} else {
			q.Set("agent", o.agent)
			q.Set("window", o.window)
		}
		code, body, err := call(ctx, c, http.MethodDelete, "/ask?"+q.Encode(), nil)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "repose-ask: guestd is not answering on %s: %v\n", o.socket, err)
			return ExitError
		}
		var res hooks.AskClosed
		if code != http.StatusOK || json.Unmarshal(body, &res) != nil {
			_, _ = fmt.Fprintf(stderr, "repose-ask: guestd refused: %s\n", strings.TrimSpace(string(body)))
			return ExitError
		}
		n = res.Closed
	}
	noun := "questions"
	if n == 1 {
		noun = "question"
	}
	_, _ = fmt.Fprintf(stdout, "withdrew %d %s\n", n, noun)
	return ExitOK
}

// runSub runs notify, ask or presence with the process's real stdio and
// signals.
func runSub(name string, args []string) int {
	switch name {
	case "notify":
		return runNotify(args, os.Stdin, os.Stdout, os.Stderr)
	case "presence":
		return runPresence(args, os.Stdout, os.Stderr)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return runAsk(args, os.Stdin, os.Stdout, os.Stderr, sigs)
}
