package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Reattach (DECISIONS I-469). When an attach's ssh ends with 255, ssh's
// own failure, the connection dropped: Wi-Fi went, the laptop slept, the
// edge restarted. The session on the machine is a tmux session and is
// still there, so the input proxy waits for the machine to answer again
// and attaches anew, in the same terminal, instead of ending the command.
// tmux redraws the screen on the new attach. Only the input-proxy path can
// do this: where the CLI has become ssh, nothing is left to try again.

const (
	// reattachWait is how long a dropped attach keeps trying.
	reattachWait = 2 * time.Minute
	// reattachInterval is the pause between tries.
	reattachInterval = time.Second
	// reattachFlap: an attach that drops again this soon after a reattach
	// is not one more try will fix.
	reattachFlap = 5 * time.Second
)

// The gateway's answers that waiting does not change: the machine is not
// running, or there is no such machine (ssh-gateway.md "Gateway behaviour").
var reattachFinal = []string{"is stopped", "is being destroyed", "no such project", "is in error"}

// reattacher decides whether a dropped attach attaches again.
type reattacher struct {
	slug string
	// probe is `ssh <target> true`.
	probe func(ctx context.Context) error
	// renew issues a new certificate; nil when the command has none to
	// offer. Called at most once per drop, on a certificate refusal (a
	// relay ends when its certificate expires, I-436).
	renew    func(ctx context.Context) error
	wait     time.Duration
	interval time.Duration
	// args, when set, are ssh's arguments for the attaches after the
	// first: the session as the user left it, not the window the first
	// attach opened on.
	args []string
}

func newReattacher(t sshTarget, slug string, renew func(ctx context.Context) error) *reattacher {
	return &reattacher{
		slug:     slug,
		probe:    func(ctx context.Context) error { return runSSHOK(ctx, t, "true") },
		renew:    renew,
		wait:     reattachWait,
		interval: reattachInterval,
	}
}

// again is asked once ssh has ended with 255, after an attach that lasted
// attached; reattached says that attach was itself a reattach. It tells
// the user, waits for the machine to answer, and reports whether to
// attach again. in is the terminal's input when it is in raw mode, where
// Ctrl-C and Ctrl-D arrive as bytes and stop the wait (other keys are
// dropped); nil otherwise, where Ctrl-C ends parent. Lines end in "\r\n",
// which a raw terminal needs and a cooked one does not mind.
func (r *reattacher) again(parent context.Context, out io.Writer, in <-chan []byte, attached time.Duration, reattached bool) bool {
	say := func(format string, args ...any) {
		_, _ = fmt.Fprintf(out, "\r\nrepose: "+format+"\r\n", args...)
	}
	if reattached && attached < reattachFlap {
		say("the connection to %s dropped again at once; giving up. `repose attach %s` tries again.", r.slug, r.slug)
		return false
	}
	say("lost the connection to %s. Reconnecting; Ctrl-C stops.", r.slug)
	ctx, cancel := context.WithTimeout(parent, r.wait)
	defer cancel()
	renewed := false
	for {
		res := make(chan error, 1)
		go func() { res <- r.probe(ctx) }()
		var err error
	probe:
		for {
			select {
			case err = <-res:
				break probe
			case p, ok := <-in:
				if !ok || bytes.ContainsAny(p, "\x03\x04") {
					cancel()
					<-res
					if ok {
						say("stopped reconnecting. `repose attach %s` attaches again.", r.slug)
					}
					return false
				}
			}
		}
		if err == nil {
			return true
		}
		var se *sshError
		if errors.As(err, &se) {
			if isCertRefusal(se) && r.renew != nil {
				// A renewal that failed (the api out of reach) is tried
				// again on the next refusal; a refusal after one that
				// worked is not something waiting fixes.
				if renewed {
					say("%s", sshStderrDetail(se.Stderr))
					return false
				}
				if r.renew(ctx) == nil {
					renewed = true
					continue
				}
			}
			low := strings.ToLower(se.Stderr)
			for _, m := range reattachFinal {
				if strings.Contains(low, m) {
					say("%s", sshStderrDetail(se.Stderr))
					return false
				}
			}
		}
		if parent.Err() != nil {
			return false // the command was interrupted; it says so itself
		}
		if ctx.Err() != nil {
			say("could not reach %s for %s. `repose attach %s` attaches again once it answers.", r.slug, waitWords(r.wait), r.slug)
			return false
		}
		select {
		case <-time.After(r.interval):
		case <-ctx.Done():
		case p, ok := <-in:
			if !ok || bytes.ContainsAny(p, "\x03\x04") {
				if ok {
					say("stopped reconnecting. `repose attach %s` attaches again.", r.slug)
				}
				return false
			}
		}
	}
}

// waitWords is a wait as a person says it: "2 minutes", "5 seconds".
func waitWords(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		if d == time.Minute {
			return "a minute"
		}
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
	return fmt.Sprintf("%d seconds", int(d.Round(time.Second)/time.Second))
}

// connectionLost is ssh ending with 255, its own failure rather than the
// remote command's status.
func connectionLost(cmd *exec.Cmd) bool {
	ps := cmd.ProcessState
	return ps != nil && ps.ExitCode() == 255
}
