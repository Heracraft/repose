package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// desktopPassword is the last non-empty line `repose-guest-profile desktop
// start` printed: the password file's contents, after anything systemctl
// may have said.
func desktopPassword(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// OpenPortCmd implements `repose open PORT [--local-port N] [--no-browser]`
// (07-cli.md §5.9). It blocks in the foreground running the SSH forward,
// like plain `ssh -N -L`; callers that want it backgrounded run it in a
// goroutine.
func OpenPortCmd(ctx context.Context, e *Env, projectArg string, port int, localPort int, noBrowser bool) error {
	project, err := requireRunningProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	// The certificate and config first: a forward from a laptop whose
	// certificate expired overnight must refresh it like run does.
	target, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	if localPort == 0 {
		localPort = port
	}
	localPort, err = pickLocalPort(e, localPort, laptopPortFree, freePort)
	if err != nil {
		return err
	}
	// Where the server listens decides where the forward goes: a server on
	// ::1 alone (Vite where localhost resolves to ::1 first) is not
	// reachable at 127.0.0.1 (DECISIONS I-261), as auto-forward knows
	// (I-199). Nothing listening yet, or ss not answering: 127.0.0.1,
	// which also reaches 0.0.0.0 and ::.
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ssOut, ssErr := runSSH(lctx, target, "ss -Hltn", nil)
	cancel()
	l, found := listenerFor(string(ssOut), port)
	if ssErr == nil && !found {
		_, _ = fmt.Fprintf(e.ErrOut, "Nothing on %s is listening on port %d yet; the forward reaches it once it listens on 127.0.0.1 or 0.0.0.0.\n", project.Slug, port)
	}
	url := fmt.Sprintf("http://localhost:%d", localPort)
	_, _ = fmt.Fprintf(e.Out, "%s → %s:%d (Ctrl-C to stop)\n", url, project.Slug, port)
	if !noBrowser {
		_ = openBrowser(url)
	}
	return keepForward(ctx, e, project, target, append(ownConnection(), "-N", "-L", openForwardSpec(localPort, l)))
}

// keepForward runs a forward's ssh in the foreground until Ctrl-C or until
// it fails, and starts it again when its connection drops (I-469), so a
// forward outlives a laptop's sleep or an edge restart. The local port is
// the same each time.
func keepForward(ctx context.Context, e *Env, project *Project, t sshTarget, extra []string) error {
	re := newReattacher(t, project.Slug, renewFor(e, project))
	args := append(append([]string{}, extra...), t.Args...)
	for attempt := 0; ; attempt++ {
		cmd := exec.Command("ssh", args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, e.Out, e.ErrOut
		started := time.Now()
		err := cmd.Run()
		if ctx.Err() != nil {
			return nil // Ctrl-C: the forward was meant to end
		}
		if cmd.ProcessState == nil {
			return exitf(ExitGeneric, "Could not run ssh: %v. repose needs the OpenSSH client (`ssh`) on your PATH.", err)
		}
		if !connectionLost(cmd) || !re.again(ctx, e.ErrOut, nil, time.Since(started), attempt > 0) {
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				return silent(code)
			}
			return nil
		}
	}
}

// listenerFor finds port among `ss -Hltn` output and says where to reach
// it: 127.0.0.1 unless the only listener is on ::1. found is false when
// nothing listens on port, and the answer is then 127.0.0.1.
func listenerFor(ssOut string, port int) (l guestListener, found bool) {
	l = guestListener{Port: port, Host: "127.0.0.1"}
	for _, line := range strings.Split(ssOut, "\n") {
		got, ok := parseListenerLine(line)
		if !ok || got.Port != port {
			continue
		}
		if !found || got.Host == "127.0.0.1" {
			l = got
		}
		found = true
	}
	return l, found
}

// openForwardSpec is `ssh -L`'s argument for `repose open`: bound to the
// laptop's 127.0.0.1 only, to the guest address the listener is on.
func openForwardSpec(localPort int, l guestListener) string {
	return "127.0.0.1:" + forwardSpec(localPort, l)
}

// pickLocalPort is want when it is free on the laptop, otherwise a free
// port, with a line saying so: the remap is never silent (I-199).
func pickLocalPort(e *Env, want int, free func(int) bool, pick func() (int, error)) (int, error) {
	if free(want) {
		return want, nil
	}
	got, err := pick()
	if err != nil {
		return 0, err
	}
	_, _ = fmt.Fprintf(e.ErrOut, "port %d is taken; forwarding to %d instead\n", want, got)
	return got, nil
}

// desktopPort is the viewer's entry point in the guest (guest-conventions.md
// "Desktop"), and the laptop port `repose browser` forwards it to when free.
const desktopPort = 6080

// ownConnection keeps a forward off the shared ControlMaster (I-149):
// through a master, `ssh -N -L` hands the forward to the master and exits
// at once, so the forward would outlive Ctrl-C and die with the master's
// ControlPersist instead of with the command.
func ownConnection() []string {
	if goos() == "windows" {
		return nil
	}
	return []string{"-o", "ControlPath=none"}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	_, portStr, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(portStr)
}
